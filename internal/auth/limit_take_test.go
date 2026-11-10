package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterTakeIsAtomic(t *testing.T) {
	l := NewLimiter(5, time.Hour, 10, nil)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Take("k") {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 5 {
		t.Errorf("took %d, want 5", ok.Load())
	}
	if !l.Take("other") {
		t.Error("another key has its own budget")
	}
	if allowed, _ := l.Allow("k"); allowed {
		t.Error("budget spent but Allow says yes")
	}
}
