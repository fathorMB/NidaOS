package logs

import (
	"context"
	"sync"

	"github.com/fathorMB/NidaOS/core/internal/stream"
)

// DefaultBufferLines is the number of lines that the pipeline keeps in
// memory for the new clients.
const DefaultBufferLines = 5000

// Pipeline gives each line a sequence number, keeps the last lines in
// memory and sends each line to all the subscribers.
type Pipeline struct {
	Hub *stream.Hub[Line]

	mu   sync.Mutex
	seq  uint64
	ring []Line
	next int
	full bool
}

// NewPipeline makes a pipeline that keeps the last size lines.
func NewPipeline(size int) *Pipeline {
	if size < 1 {
		size = DefaultBufferLines
	}
	return &Pipeline{
		// A burst of lines must not make a client lose lines at once.
		Hub:  stream.NewHub[Line](1024),
		ring: make([]Line, size),
	}
}

// Emit adds one line. The sources call it.
func (p *Pipeline) Emit(l Line) {
	p.mu.Lock()
	p.seq++
	l.Seq = p.seq
	p.ring[p.next] = l
	p.next = (p.next + 1) % len(p.ring)
	if p.next == 0 {
		p.full = true
	}
	// Publish under the lock, thus the subscribers get the lines in the
	// order of their numbers.
	p.Hub.Publish(l)
	p.mu.Unlock()
}

// Recent gives the last lines that the filter selects, the oldest first.
// It gives a maximum of limit lines, and only lines after the number
// after (0 for all).
func (p *Pipeline) Recent(f Filter, limit int, after uint64) []Line {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []Line{}
	if limit <= 0 {
		return out
	}
	n := p.next
	if p.full {
		n = len(p.ring)
	}
	// From the newest to the oldest, then reverse.
	for i := 0; i < n && len(out) < limit; i++ {
		l := p.ring[(p.next-1-i+len(p.ring))%len(p.ring)]
		if l.Seq <= after {
			break
		}
		if f.Match(l) {
			out = append(out, l)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Subscribe gives the lines of the live stream and the number of the
// last line before the subscription, under one lock. Thus a client can
// read Recent up to that number and then the stream, with no gap and no
// line two times.
func (p *Pipeline) Subscribe() (*stream.Subscription[Line], uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Hub.Subscribe(), p.seq
}

// Reader is a source of lines, for example Journal or Pods.
type Reader interface {
	Run(ctx context.Context, emit func(Line))
}

// RunSources starts each reader and returns when ctx stops and all the
// readers stopped.
func (p *Pipeline) RunSources(ctx context.Context, sources ...Reader) {
	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Go(func() { s.Run(ctx, p.Emit) })
	}
	wg.Wait()
}
