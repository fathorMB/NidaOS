package auth

import (
	"sync"
	"time"
)

// Limiter slows down the guesses of the password. After FreeFailures
// wrong passwords from one address, the address must wait. The wait is
// two times longer after each new failure, to a maximum of MaxWait.
type Limiter struct {
	FreeFailures int
	MaxWait      time.Duration
	Now          func() time.Time

	mu    sync.Mutex
	state map[string]*limitState
}

type limitState struct {
	failures int
	until    time.Time
	last     time.Time
}

// NewLimiter makes a limiter with 5 free failures and a maximum wait of
// 15 minutes.
func NewLimiter() *Limiter {
	return &Limiter{FreeFailures: 5, MaxWait: 15 * time.Minute, Now: time.Now, state: map[string]*limitState{}}
}

// Wait gives the time that the address must wait before a new attempt.
// 0 is no wait.
func (l *Limiter) Wait(addr string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.state[addr]
	if !ok {
		return 0
	}
	if d := st.until.Sub(l.Now()); d > 0 {
		return d
	}
	return 0
}

// Fail records a wrong password from addr.
func (l *Limiter) Fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.prune(now)
	st := l.state[addr]
	if st == nil {
		st = &limitState{}
		l.state[addr] = st
	}
	st.failures++
	st.last = now
	if extra := st.failures - l.FreeFailures; extra > 0 {
		wait := time.Second << min(extra-1, 20)
		st.until = now.Add(min(wait, l.MaxWait))
	}
}

// Succeed forgets the failures of addr.
func (l *Limiter) Succeed(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, addr)
}

// prune forgets the addresses without a failure for one day. The caller
// holds l.mu.
func (l *Limiter) prune(now time.Time) {
	if len(l.state) < 1000 {
		return
	}
	for a, st := range l.state {
		if now.Sub(st.last) > 24*time.Hour {
			delete(l.state, a)
		}
	}
}
