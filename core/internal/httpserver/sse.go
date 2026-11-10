package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// SSEHeartbeat is the time between two comment lines when there is no
// data. The comment keeps the connection open through the gateway and the
// proxies, and lets the client see that the stream is alive.
const SSEHeartbeat = 15 * time.Second

// sseWriter writes Server-Sent Events. The format is the format of the
// EventSource interface of the browsers:
//
//	id: 42
//	event: metrics
//	data: {...}
type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

// newSSE writes the headers of a stream. It gives false when the
// connection cannot send data at once.
func newSSE(w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "The connection cannot send a stream.")
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// Tell nginx-type proxies not to keep the data in a buffer.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w: w, f: f}, true
}

// event writes one event with a JSON body.
func (s *sseWriter) event(name string, id uint64, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "id: %s\nevent: %s\ndata: %s\n\n", strconv.FormatUint(id, 10), name, b); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// comment writes a comment line. The clients ignore it.
func (s *sseWriter) comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}
