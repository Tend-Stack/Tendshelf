package auth

import (
	"sync"
	"time"
)

// Limiter counts failures per key in a fixed window. It holds at most
// capacity keys: when a new key arrives at capacity, expired windows are
// dropped first and then the oldest one, so an attacker rotating keys can not
// grow memory.
type Limiter struct {
	max      int
	window   time.Duration
	capacity int
	now      func() time.Time

	mu sync.Mutex
	m  map[string]*window
}

type window struct {
	start time.Time
	n     int
}

// NewLimiter allows max failures per key in each window. A nil now means time.Now.
func NewLimiter(max int, win time.Duration, capacity int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if capacity < 1 {
		capacity = 1
	}
	return &Limiter{max: max, window: win, capacity: capacity, now: now, m: make(map[string]*window)}
}

// Allow reports whether key may try again; when it may not, the duration is how
// long until its window ends.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.live(key)
	if w == nil || w.n < l.max {
		return true, 0
	}
	return false, w.start.Add(l.window).Sub(l.now())
}

// Fail records one failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w := l.live(key); w != nil {
		w.n++
		return
	}
	if len(l.m) >= l.capacity {
		l.evict()
	}
	l.m[key] = &window{start: l.now(), n: 1}
}

// Reset forgets key, after a successful sign-in.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// Len is the number of keys held.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// live returns key's window, dropping it first when it has ended. Caller holds mu.
func (l *Limiter) live(key string) *window {
	w, ok := l.m[key]
	if !ok {
		return nil
	}
	if !l.now().Before(w.start.Add(l.window)) {
		delete(l.m, key)
		return nil
	}
	return w
}

// evict makes room: expired windows first, then the oldest. Caller holds mu.
func (l *Limiter) evict() {
	now := l.now()
	for k, w := range l.m {
		if !now.Before(w.start.Add(l.window)) {
			delete(l.m, k)
		}
	}
	if len(l.m) < l.capacity {
		return
	}
	var oldestKey string
	var oldest time.Time
	first := true
	for k, w := range l.m {
		if first || w.start.Before(oldest) {
			oldestKey, oldest, first = k, w.start, false
		}
	}
	delete(l.m, oldestKey)
}
