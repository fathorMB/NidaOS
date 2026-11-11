package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/stream"
)

// DefaultPodLogDir is the directory of the pod logs of the kubelet.
const DefaultPodLogDir = "/var/log/pods"

// Sampler reads the counters at each interval and publishes a Snapshot.
type Sampler struct {
	Interval time.Duration
	Hub      *stream.Hub[Snapshot]
	Logger   *slog.Logger
	Now      func() time.Time
	// Databases gives the live state of the managed databases. Nil: no
	// databases in the snapshots.
	Databases func() []Database

	r    reader
	prev *raw
	seq  uint64
}

// NewSampler makes a sampler for the files under root.
func NewSampler(root, podLogDir string, interval time.Duration, logger *slog.Logger) *Sampler {
	if root == "" {
		root = "/"
	}
	if podLogDir == "" {
		podLogDir = DefaultPodLogDir
	}
	return &Sampler{
		Interval: interval,
		// A small buffer: a client that is late gets the next snapshot, not
		// a queue of old ones.
		Hub:    stream.NewHub[Snapshot](4),
		Logger: logger,
		Now:    time.Now,
		r:      reader{root: root, podLogDir: podLogDir},
	}
}

// Sample reads the counters one time. It publishes a snapshot when there
// is a previous read. It gives false when the read failed or when it was
// the first read.
func (s *Sampler) Sample() bool {
	cur, err := s.r.readRaw(s.Now())
	if err != nil {
		s.Logger.Warn("the sampler cannot read the counters", "err", err)
		return false
	}
	prev := s.prev
	s.prev = &cur
	if prev == nil {
		return false
	}
	snap := diff(*prev, cur)
	if s.Databases != nil {
		snap.Databases = s.Databases()
	}
	s.seq++
	snap.Seq = s.seq
	s.Hub.Publish(snap)
	return true
}

// Run samples at each interval until ctx stops.
func (s *Sampler) Run(ctx context.Context) {
	s.Sample()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sample()
		}
	}
}
