//go:build dev

package db

import "sync"

var (
	seqMu  sync.Mutex
	seqKey string
	seqN   uint64
)

// SeedIDs makes NewID answer with a counting sequence (fixed time part, then a counter in Crockford
// base32) so two runs of the same visual test build the same ids, and so the id-derived looks (spine
// heights, avatar colours) repeat. The counter restarts only when key changes, so several apps in one
// test never repeat an id. An empty key goes back to random ids. Compiled only with -tags dev; a release binary cannot reach it.
func SeedIDs(key string) {
	seqMu.Lock()
	defer seqMu.Unlock()
	if key == "" {
		seqKey = ""
		idHook.Store(nil)
		return
	}
	if key == seqKey && idHook.Load() != nil {
		return
	}
	seqKey, seqN = key, 0
	hook := func(prefix string) string {
		seqMu.Lock()
		defer seqMu.Unlock()
		seqN++
		n := seqN
		var tail [12]byte
		for i := len(tail) - 1; i >= 0; i-- {
			tail[i] = crockford[n&31]
			n >>= 5
		}
		return prefix + "_01TESTSEED0000" + string(tail[:])
	}
	idHook.Store(&hook)
}
