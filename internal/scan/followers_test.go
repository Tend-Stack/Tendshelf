package scan

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// settledLog records what a follower was told, and how many chapters it could read for each book.
type settledLog struct {
	mu       sync.Mutex
	calls    [][]string
	chapters map[string]int
}

func (l *settledLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = nil
}

func (l *settledLog) counts() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for _, c := range l.calls {
		for _, id := range c {
			out[id]++
		}
	}
	return out
}

func TestFollowersNotified(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	for _, b := range []string{"One", "Two", "Three"} {
		fx.file(b+"/01.mp3", mp3)
		fx.file(b+"/02.mp3", mp3)
		fx.book(b, "", b+"/01.mp3", b+"/02.mp3")
	}

	l := &settledLog{chapters: map[string]int{}}
	var second [][]string
	e.sc.OnBookSettled(func(ctx context.Context, ids []string) {
		chs := map[string]int{}
		for _, id := range ids {
			c, err := e.repo.Chapters(ctx, id)
			if err != nil {
				t.Errorf("Chapters(%s): %v", id, err)
			}
			chs[id] = len(c)
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		l.calls = append(l.calls, ids)
		for id, n := range chs {
			l.chapters[id] = n
		}
	})
	e.sc.OnBookSettled(func(_ context.Context, ids []string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		second = append(second, ids)
	})

	res := e.scan()
	if res.BooksSettled != 0 {
		t.Fatalf("books settled by the scan itself = %d, want 0 (nothing is probed yet)", res.BooksSettled)
	}
	e.probeAll()

	books, err := e.repo.BooksInFolder(bg, e.folder.ID)
	if err != nil || len(books) != 3 {
		t.Fatalf("books = %d, %v", len(books), err)
	}
	ids := map[string]string{}
	for _, b := range books {
		ids[b.RootPath] = b.ID
	}
	got := l.counts()
	if len(got) != 3 {
		t.Fatalf("notified %v, want the 3 books", got)
	}
	for _, b := range books {
		if got[b.ID] != 1 {
			t.Errorf("book %s notified %d times, want 1", b.RootPath, got[b.ID])
		}
		// ProbeFile path: the follower runs after commit, so the chapters are already visible.
		if l.chapters[b.ID] == 0 {
			t.Errorf("book %s: follower saw no chapters", b.RootPath)
		}
	}
	for _, c := range l.calls {
		if !sort.StringsAreSorted(c) {
			t.Errorf("ids %v not sorted", c)
		}
	}
	if len(second) != len(l.calls) {
		t.Errorf("second follower called %d times, first %d", len(second), len(l.calls))
	}

	// A no-op scan notifies nothing.
	l.reset()
	if res := e.scan(); res.BooksSettled != 0 {
		t.Errorf("no-op scan: books_settled = %d", res.BooksSettled)
	}
	if got := l.counts(); len(got) != 0 {
		t.Fatalf("no-op scan notified %v", got)
	}

	// Renaming a book folder settles that book again, from the scan, with no new probes.
	if err := os.Rename(filepath.Join(e.root, "Two"), filepath.Join(e.root, "Two renamed")); err != nil {
		t.Fatal(err)
	}
	l.reset()
	res = e.scan()
	if res.BooksSettled != 1 {
		t.Errorf("rename scan: books_settled = %d, want 1", res.BooksSettled)
	}
	got = l.counts()
	if len(got) != 1 || got[ids[filepath.Join(e.root, "Two")]] != 1 {
		t.Fatalf("rename notified %v, want only the renamed book once", got)
	}
}
