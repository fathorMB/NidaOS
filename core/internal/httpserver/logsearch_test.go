package httpserver

import (
	"net/http"
	"testing"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/logs"
)

// fakeSearcher keeps the last query.
type fakeSearcher struct {
	q   logs.Query
	err error
}

func (f *fakeSearcher) Search(q logs.Query) (logs.Result, error) {
	f.q = q
	return logs.Result{Lines: []logs.Line{{Message: "found"}}, NextCursor: "2026-11-10:5"}, f.err
}

func TestLogsSearch(t *testing.T) {
	fs := &fakeSearcher{}
	r := NewRouter(Deps{LogStore: fs})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	var res logs.Result
	code := get(t, r, "/api/v1/logs/search?source=pod/apps&level=warn&q=timeout&since=2h&until=2026-11-10T11:30:00Z&limit=50&cursor=2026-11-10:9", &res)
	if code != 200 || len(res.Lines) != 1 || res.NextCursor != "2026-11-10:5" {
		t.Fatalf("%d %+v", code, res)
	}
	q := fs.q
	if q.Source != "pod/apps" || q.Level != logs.LevelWarning || q.Text != "timeout" || q.Limit != 50 || q.Cursor != "2026-11-10:9" ||
		!q.Since.Equal(now.Add(-2*time.Hour)) || !q.Until.Equal(time.Date(2026, 11, 10, 11, 30, 0, 0, time.UTC)) {
		t.Errorf("query = %+v", q)
	}

	get(t, r, "/api/v1/logs/search?since=7d", &res)
	if !fs.q.Since.Equal(now.Add(-7*24*time.Hour)) || fs.q.Limit != defaultSearchLimit {
		t.Errorf("query = %+v", fs.q)
	}

	for path, code := range map[string]string{
		"/api/v1/logs/search?since=yesterday":   "bad_since",
		"/api/v1/logs/search?until=-3h":         "bad_until",
		"/api/v1/logs/search?since=1h&until=2h": "bad_range",
		"/api/v1/logs/search?limit=0":           "bad_limit",
		"/api/v1/logs/search?limit=5000":        "bad_limit",
		"/api/v1/logs/search?level=loud":        "bad_level",
	} {
		var e Error
		if status := get(t, r, path, &e); status != http.StatusBadRequest || e.Code != code {
			t.Errorf("%s: %d %+v, want %s", path, status, e, code)
		}
	}

	fs.err = logs.ErrBadCursor
	var e Error
	if status := get(t, r, "/api/v1/logs/search?cursor=x", &e); status != 400 || e.Code != "bad_cursor" {
		t.Errorf("bad cursor: %d %+v", status, e)
	}
}

func TestLogsSearchWithoutStore(t *testing.T) {
	if code := get(t, NewRouter(Deps{}), "/api/v1/logs/search", nil); code != http.StatusNotFound {
		t.Errorf("status = %d", code)
	}
}

// TestLogsSearchRealStore writes lines through the pipeline into a real
// store and finds them with the HTTP path.
func TestLogsSearchRealStore(t *testing.T) {
	store, err := logs.OpenStore(t.TempDir(), 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := logs.NewPipeline(10)
	p.Sink = store.Append
	for i := range 25 {
		lvl := logs.LevelInfo
		if i%5 == 0 {
			lvl = logs.LevelError
		}
		p.Emit(logs.Line{Time: time.Now().UTC(), Level: lvl, Source: logs.ServiceSource("k3s"), Message: "line"})
	}
	r := NewRouter(Deps{Logs: p, LogStore: store})

	// The memory has only 10 lines; the store has all 25.
	var mem logsBody
	get(t, r, "/api/v1/logs?tail=100", &mem)
	var res logs.Result
	get(t, r, "/api/v1/logs/search?limit=1000", &res)
	if len(mem.Lines) != 10 || len(res.Lines) != 25 {
		t.Errorf("memory %d, store %d", len(mem.Lines), len(res.Lines))
	}
	get(t, r, "/api/v1/logs/search?level=error&since=1h", &res)
	if len(res.Lines) != 5 {
		t.Errorf("errors = %d", len(res.Lines))
	}
}
