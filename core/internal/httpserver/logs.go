package httpserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/logs"
	"github.com/fathorMB/NidaOS/core/internal/stream"
)

// LogSource gives the log lines.
type LogSource interface {
	Recent(f logs.Filter, limit int, after uint64) []logs.Line
	Subscribe() (*stream.Subscription[logs.Line], uint64)
}

// Limits of the "tail" parameter.
const (
	defaultTail = 200
	maxTail     = 5000
)

type logsBody struct {
	Lines []logs.Line `json:"lines"`
}

// droppedBody tells a client that the stream did not send some lines,
// because the client was too slow.
type droppedBody struct {
	Count uint64 `json:"count"`
}

func (r *Router) addLogRoutes() {
	if r.logs == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/logs", r.logsRecent)
	r.handle(http.MethodGet, "/api/v1/logs/stream", r.logsStream)
}

// logQuery reads the parameters source, level, q and tail.
func logQuery(w http.ResponseWriter, req *http.Request) (logs.Filter, int, bool) {
	q := req.URL.Query()
	f := logs.Filter{Source: q.Get("source"), Text: q.Get("q")}
	if v := q.Get("level"); v != "" {
		l, ok := logs.ParseLevel(v)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_level", "The level must be debug, info, warning or error.")
			return logs.Filter{}, 0, false
		}
		f.Level = l
	}
	tail := defaultTail
	if v := q.Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxTail {
			writeError(w, http.StatusBadRequest, "bad_tail", "The tail must be a number from 0 to 5000.")
			return logs.Filter{}, 0, false
		}
		tail = n
	}
	return f, tail, true
}

// logsRecent gives the last lines that the filter selects.
func (r *Router) logsRecent(w http.ResponseWriter, req *http.Request) {
	f, tail, ok := logQuery(w, req)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, logsBody{Lines: r.logs.Recent(f, tail, 0)})
}

// logsStream sends the last lines, then each new line, as the event "log".
// A client that connects again sends the header Last-Event-ID (the
// browsers do this automatically). Then the stream sends the lines after
// that number that are still in the memory buffer.
func (r *Router) logsStream(w http.ResponseWriter, req *http.Request) {
	f, tail, ok := logQuery(w, req)
	if !ok {
		return
	}
	var after uint64
	resume := false
	if v := req.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			after, resume, tail = n, true, maxTail
		}
	}

	sub, last := r.logs.Subscribe()
	defer sub.Close()
	sse, ok := newSSE(w)
	if !ok {
		return
	}

	// The lines before the subscription come from the buffer.
	for _, l := range r.logs.Recent(f, tail, after) {
		if l.Seq > last {
			break
		}
		if sse.event("log", l.Seq, l) != nil {
			return
		}
	}
	if resume && after > last {
		// The service started again: the numbers start again at 1.
		last = 0
	}

	beat := time.NewTicker(r.heartbeat)
	defer beat.Stop()
	var dropped uint64
	for {
		select {
		case <-req.Context().Done():
			return
		case l, open := <-sub.C:
			if !open {
				return
			}
			if l.Seq <= last {
				continue
			}
			if d := sub.Dropped(); d > dropped {
				if sse.event("dropped", l.Seq, droppedBody{Count: d - dropped}) != nil {
					return
				}
				dropped = d
			}
			if !f.Match(l) {
				continue
			}
			if sse.event("log", l.Seq, l) != nil {
				return
			}
		case <-beat.C:
			if sse.comment("heartbeat") != nil {
				return
			}
		}
	}
}
