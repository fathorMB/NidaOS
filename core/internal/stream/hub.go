// Package stream sends each new value to all the subscribers. The metrics
// and the logs use it: one producer, many clients (decision D8).
package stream

import "sync"

// Hub sends each published value to all the subscribers. A slow subscriber
// does not stop the producer or the other subscribers: when its buffer is
// full, it does not get the value, and its Dropped counter increases.
type Hub[T any] struct {
	mu     sync.Mutex
	subs   map[*Subscription[T]]struct{}
	last   T
	hasOne bool
	buffer int
}

// Subscription is one subscriber of a Hub.
type Subscription[T any] struct {
	// C gives the values. The hub closes it when the subscription stops.
	C       <-chan T
	c       chan T
	hub     *Hub[T]
	once    sync.Once
	dropped uint64
}

// NewHub makes a hub. Each subscriber has a buffer of the given size.
func NewHub[T any](buffer int) *Hub[T] {
	if buffer < 1 {
		buffer = 1
	}
	return &Hub[T]{subs: map[*Subscription[T]]struct{}{}, buffer: buffer}
}

// Subscribe adds a subscriber. Call Close on it when the client goes.
func (h *Hub[T]) Subscribe() *Subscription[T] {
	c := make(chan T, h.buffer)
	s := &Subscription[T]{C: c, c: c, hub: h}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Publish sends v to all the subscribers and keeps it as the last value.
func (h *Hub[T]) Publish(v T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last, h.hasOne = v, true
	for s := range h.subs {
		select {
		case s.c <- v:
		default:
			s.dropped++
		}
	}
}

// Last gives the last published value. ok is false before the first value.
func (h *Hub[T]) Last() (v T, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last, h.hasOne
}

// Subscribers gives the number of subscribers.
func (h *Hub[T]) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Dropped gives the number of values that the subscriber did not get
// because its buffer was full.
func (s *Subscription[T]) Dropped() uint64 {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.dropped
}

// Close removes the subscriber and closes C. A second call does nothing.
func (s *Subscription[T]) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, s)
		close(s.c)
		s.hub.mu.Unlock()
	})
}
