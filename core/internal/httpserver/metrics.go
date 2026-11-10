package httpserver

import (
	"net/http"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/metrics"
	"github.com/fathorMB/NidaOS/core/internal/stream"
)

// MetricsSource gives the snapshots of the sampler.
type MetricsSource interface {
	Last() (metrics.Snapshot, bool)
	Subscribe() *stream.Subscription[metrics.Snapshot]
}

func (r *Router) addMetricsRoutes() {
	if r.metrics == nil {
		return
	}
	r.handle(http.MethodGet, "/api/v1/metrics", r.metricsLast)
	r.handle(http.MethodGet, "/api/v1/metrics/stream", r.metricsStream)
}

// metricsLast gives the last snapshot, for a client that does not need
// the stream (for example "nida status").
func (r *Router) metricsLast(w http.ResponseWriter, _ *http.Request) {
	snap, ok := r.metrics.Last()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", "The first snapshot is not ready. Try again in a few seconds.")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// metricsStream sends each snapshot as the event "metrics". It sends the
// last snapshot first, thus the client shows data at once.
func (r *Router) metricsStream(w http.ResponseWriter, req *http.Request) {
	sub := r.metrics.Subscribe()
	defer sub.Close()

	sse, ok := newSSE(w)
	if !ok {
		return
	}
	var lastSeq uint64
	if snap, ok := r.metrics.Last(); ok {
		if sse.event("metrics", snap.Seq, snap) != nil {
			return
		}
		lastSeq = snap.Seq
	}

	beat := time.NewTicker(r.heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case snap, open := <-sub.C:
			if !open {
				return
			}
			// The subscription can hold the snapshot that was sent first.
			if snap.Seq <= lastSeq {
				continue
			}
			if sse.event("metrics", snap.Seq, snap) != nil {
				return
			}
			lastSeq = snap.Seq
		case <-beat.C:
			if sse.comment("heartbeat") != nil {
				return
			}
		}
	}
}
