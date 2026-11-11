package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/logs"
)

func emitN(p *logs.Pipeline, n int, level, source string) {
	for range n {
		p.Emit(logs.Line{Level: level, Source: logs.ServiceSource(source), Message: source + " " + level})
	}
}

func TestLogsRecent(t *testing.T) {
	p := logs.NewPipeline(100)
	emitN(p, 3, logs.LevelInfo, "k3s")
	emitN(p, 2, logs.LevelError, "nida-core")
	r := NewRouter(Deps{Logs: p})

	var body logsBody
	if code := get(t, r, "/api/v1/logs", &body); code != 200 || len(body.Lines) != 5 {
		t.Errorf("all: %d %d", code, len(body.Lines))
	}
	if code := get(t, r, "/api/v1/logs?level=warn", &body); code != 200 || len(body.Lines) != 2 {
		t.Errorf("warn: %d %+v", code, body.Lines)
	}
	if code := get(t, r, "/api/v1/logs?source=service/k3s&tail=2", &body); code != 200 || len(body.Lines) != 2 || body.Lines[1].Seq != 3 {
		t.Errorf("k3s tail 2: %d %+v", code, body.Lines)
	}
	if code := get(t, r, "/api/v1/logs?q=CORE", &body); code != 200 || len(body.Lines) != 2 {
		t.Errorf("text: %d %+v", code, body.Lines)
	}

	var e Error
	if code := get(t, r, "/api/v1/logs?level=loud", &e); code != 400 || e.Code != "bad_level" {
		t.Errorf("bad level: %d %+v", code, e)
	}
	if code := get(t, r, "/api/v1/logs?tail=-1", &e); code != 400 || e.Code != "bad_tail" {
		t.Errorf("bad tail: %d %+v", code, e)
	}
}

// openLogStream opens the log stream with an optional Last-Event-ID.
func openLogStream(t *testing.T, ctx context.Context, url, lastID string) <-chan sseEvent {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	return readEvents(t, bufio.NewScanner(resp.Body))
}

func nextLine(t *testing.T, c <-chan sseEvent) (string, logs.Line) {
	t.Helper()
	for {
		ev := next(t, c)
		if ev.name == "comment" {
			continue
		}
		var l logs.Line
		if ev.name == "log" {
			if err := json.Unmarshal([]byte(ev.data), &l); err != nil {
				t.Fatal(err)
			}
		}
		return ev.name, l
	}
}

func TestLogsStream(t *testing.T) {
	p := logs.NewPipeline(100)
	emitN(p, 3, logs.LevelInfo, "k3s")
	emitN(p, 1, logs.LevelError, "k3s")
	srv := httptest.NewServer(NewRouter(Deps{Logs: p}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// tail=2: the last two lines first, then the live lines.
	events := openLogStream(t, ctx, srv.URL+"/api/v1/logs/stream?tail=2", "")
	for _, want := range []uint64{3, 4} {
		if name, l := nextLine(t, events); name != "log" || l.Seq != want {
			t.Fatalf("got %s %+v, want seq %d", name, l, want)
		}
	}
	p.Emit(logs.Line{Level: logs.LevelInfo, Source: logs.ServiceSource("sshd"), Message: "accepted"})
	if _, l := nextLine(t, events); l.Seq != 5 || l.Source.Name != "sshd" {
		t.Fatalf("live line = %+v", l)
	}

	// A filtered stream gets only the errors.
	errs := openLogStream(t, ctx, srv.URL+"/api/v1/logs/stream?level=error&tail=0", "")
	for p.Hub.Subscribers() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	p.Emit(logs.Line{Level: logs.LevelInfo, Source: logs.ServiceSource("a"), Message: "skip"})
	p.Emit(logs.Line{Level: logs.LevelError, Source: logs.ServiceSource("a"), Message: "keep"})
	if _, l := nextLine(t, errs); l.Message != "keep" || l.Seq != 7 {
		t.Fatalf("filtered line = %+v", l)
	}
}

func TestLogsStreamResume(t *testing.T) {
	p := logs.NewPipeline(100)
	emitN(p, 10, logs.LevelInfo, "k3s")
	srv := httptest.NewServer(NewRouter(Deps{Logs: p}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The client saw line 7: it gets 8, 9 and 10, then the live lines.
	events := openLogStream(t, ctx, srv.URL+"/api/v1/logs/stream?tail=1", "7")
	for _, want := range []uint64{8, 9, 10} {
		if _, l := nextLine(t, events); l.Seq != want {
			t.Fatalf("seq = %d, want %d", l.Seq, want)
		}
	}
	p.Emit(logs.Line{Level: logs.LevelInfo, Source: logs.ServiceSource("k3s"), Message: "new"})
	if _, l := nextLine(t, events); l.Seq != 11 {
		t.Fatalf("live seq = %d", l.Seq)
	}
}

func TestLogsStreamDropped(t *testing.T) {
	p := logs.NewPipeline(100)
	srv := httptest.NewServer(NewRouter(Deps{Logs: p}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/logs/stream?tail=0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for p.Hub.Subscribers() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	// The client does not read: more lines than the buffers of the hub
	// and of the connection. Then the client reads.
	emitN(p, 50000, logs.LevelInfo, "flood")
	events := readEvents(t, bufio.NewScanner(resp.Body))
	p.Emit(logs.Line{Level: logs.LevelInfo, Source: logs.ServiceSource("last"), Message: "end"})
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the stream closed")
			}
			if ev.name == "dropped" {
				return
			}
		case <-deadline:
			t.Fatal("no dropped event")
		}
	}
}

func TestLogsWithoutSource(t *testing.T) {
	if code := get(t, NewRouter(Deps{}), "/api/v1/logs", nil); code != http.StatusNotFound {
		t.Errorf("status = %d", code)
	}
}
