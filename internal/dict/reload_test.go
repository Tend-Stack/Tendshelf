package dict

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A reload that takes long must not stall lookups: they keep answering from the set they had.
func TestDictReloadDoesNotBlockLookups(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.tsv", "hello\tgreeting\n")
	set := Open(dir)
	if len(set.Find("hello")) != 1 {
		t.Fatal("first load failed")
	}
	started := make(chan struct{})
	release := make(chan struct{})
	set.onLoad = func() {
		close(started)
		<-release
	}
	write("b.tsv", "world\tplanet\n")
	done := make(chan int)
	go func() { done <- len(set.Find("world")) }()
	<-started // the reload is now running and stuck
	answered := make(chan int)
	go func() { answered <- len(set.Find("hello")) }()
	select {
	case n := <-answered:
		if n != 1 {
			t.Errorf("lookup during reload found %d, want 1 from the old set", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a lookup was stalled by the reload")
	}
	close(release)
	if n := <-done; n != 1 {
		t.Errorf("the reloading lookup found %d, want 1", n)
	}
	if len(set.Find("world")) != 1 {
		t.Error("the new file was not swapped in")
	}
}
