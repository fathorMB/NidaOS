package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/metrics"
	"github.com/fathorMB/NidaOS/core/internal/stream"
)

func TestMetricsLast(t *testing.T) {
	hub := stream.NewHub[metrics.Snapshot](4)
	r := NewRouter(Deps{Metrics: hub})

	var e Error
	if code := get(t, r, "/api/v1/metrics", &e); code != http.StatusServiceUnavailable || e.Code != "metrics_unavailable" {
		t.Errorf("before the first snapshot: %d %+v", code, e)
	}
	hub.Publish(metrics.Snapshot{Seq: 7})
	var s metrics.Snapshot
	if code := get(t, r, "/api/v1/metrics", &s); code != 200 || s.Seq != 7 {
		t.Errorf("after: %d %+v", code, s)
	}
}

func TestMetricsWithoutSource(t *testing.T) {
	r := NewRouter(Deps{})
	var e Error
	if code := get(t, r, "/api/v1/metrics/stream", &e); code != http.StatusNotFound {
		t.Errorf("status = %d", code)
	}
}

// sseEvent is one event that the test client reads.
type sseEvent struct {
	id, name, data string
}

// readEvents reads the events of a stream and sends them on a channel. It
// ignores the comments.
func readEvents(t *testing.T, sc *bufio.Scanner) <-chan sseEvent {
	out := make(chan sseEvent, 16)
	go func() {
		defer close(out)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.name != "" {
					out <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, ":"):
				out <- sseEvent{name: "comment"}
			case strings.HasPrefix(line, "id: "):
				ev.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.name = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.data = line[6:]
			}
		}
	}()
	return out
}

func next(t *testing.T, c <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-c:
		if !ok {
			t.Fatal("the stream closed")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event in 3 seconds")
	}
	return sseEvent{}
}

func TestMetricsStream(t *testing.T) {
	hub := stream.NewHub[metrics.Snapshot](4)
	hub.Publish(metrics.Snapshot{Seq: 1})
	r := NewRouter(Deps{Metrics: hub})
	r.heartbeat = 50 * time.Millisecond
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/metrics/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	events := readEvents(t, bufio.NewScanner(resp.Body))

	// The last snapshot comes first.
	ev := next(t, events)
	if ev.name != "metrics" || ev.id != "1" {
		t.Fatalf("first event = %+v", ev)
	}

	// Then each new snapshot. Skip the heartbeats.
	for hub.Subscribers() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	hub.Publish(metrics.Snapshot{Seq: 2, IntervalSeconds: 2})
	for {
		ev = next(t, events)
		if ev.name != "comment" {
			break
		}
	}
	var s metrics.Snapshot
	if err := json.Unmarshal([]byte(ev.data), &s); err != nil || ev.id != "2" || s.IntervalSeconds != 2 {
		t.Fatalf("second event = %+v, err = %v", ev, err)
	}

	// With no new data, the stream sends heartbeats.
	if ev = next(t, events); ev.name != "comment" {
		t.Errorf("want a heartbeat, got %+v", ev)
	}

	// When the client goes, the server removes the subscription.
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for hub.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the subscription stays after the client went")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
