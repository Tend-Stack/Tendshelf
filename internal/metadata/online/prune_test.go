package online

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneCovers(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	c := New(Options{CoverDir: dir, Now: func() time.Time { return now }})
	write := func(name string, size int, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	write("expired.img", 10, cacheTTL+time.Hour)
	write("oldest.img", 40, 3*24*time.Hour)
	write("middle.img", 40, 2*24*time.Hour)
	write("newest.img", 40, time.Hour)
	write("other.txt", 500, cacheTTL*2) // not ours: never touched
	n, err := c.PruneCovers(100)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("removed %d, want 2 (the expired one and the oldest over the cap)", n)
	}
	for name, want := range map[string]bool{"expired.img": false, "oldest.img": false, "middle.img": true, "newest.img": true, "other.txt": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s present = %v, want %v", name, err == nil, want)
		}
	}
	if n, err := New(Options{CoverDir: filepath.Join(dir, "missing")}).PruneCovers(1); n != 0 || err != nil {
		t.Errorf("missing folder: %d, %v", n, err)
	}
}
