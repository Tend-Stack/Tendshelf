package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFolderHasAudio(t *testing.T) {
	ctx := context.Background()
	has := func(root string) (bool, error) { return FolderHasAudio(ctx, root) }

	empty := t.TempDir()
	if got, err := has(empty); err != nil || got {
		t.Fatalf("empty: %v, %v", got, err)
	}
	touch(t, filepath.Join(empty, "cover.jpg"))
	touch(t, filepath.Join(empty, ".hidden/a.mp3"))
	touch(t, filepath.Join(empty, ".b.mp3"))
	if got, err := has(empty); err != nil || got {
		t.Fatalf("only cover and hidden files: %v, %v", got, err)
	}

	mp3 := t.TempDir()
	touch(t, filepath.Join(mp3, "Book/part1.mp3"))
	if got, err := has(mp3); err != nil || !got {
		t.Fatalf("mp3: %v, %v", got, err)
	}
	aax := t.TempDir()
	touch(t, filepath.Join(aax, "x.aax"))
	if got, err := has(aax); err != nil || !got {
		t.Fatalf("only .aax: %v, %v", got, err)
	}

	if _, err := has(filepath.Join(empty, "nope")); !errors.Is(err, ErrFolderUnavailable) {
		t.Fatalf("missing root: %v", err)
	}
	if _, err := has(filepath.Join(empty, "cover.jpg")); !errors.Is(err, ErrFolderUnavailable) {
		t.Fatalf("file as root: %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root can read a mode 000 directory")
	}
	locked := filepath.Join(t.TempDir(), "r")
	touch(t, filepath.Join(locked, "sub/b.mp3"))
	touch(t, filepath.Join(locked, "ok/c.txt"))
	sub := filepath.Join(locked, "sub")
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	if _, err := has(locked); !errors.Is(err, ErrFolderUnavailable) {
		t.Fatalf("unreadable subfolder: %v", err)
	}
}
