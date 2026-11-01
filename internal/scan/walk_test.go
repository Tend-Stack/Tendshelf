package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rels(root string, fs []Found) []string {
	var out []string
	for _, f := range fs {
		r, _ := filepath.Rel(root, f.Path)
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func TestWalkIgnoresNoise(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"Au/B/01.mp3", "Au/B/02.M4B", "Au/B/x.aax", "top.flac",
		".hidden/x.m4b", "@eaDir/x.mp3", "#recycle/x.mp3", "#snapshot/x.mp3", "__MACOSX/x.mp3", "lost+found/x.mp3",
		"$RECYCLE.BIN/x.mp3", "System Volume Information/x.mp3", "Au/B/._01.mp3", "Au/B/.DS_Store", "Au/B/cover.jpg", "Au/B/notes.txt",
		"Au/B/@eaDir/y.mp3",
	} {
		touch(t, filepath.Join(root, p))
	}
	fs, st, err := walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Au/B/01.mp3", "Au/B/02.M4B", "Au/B/x.aax", "top.flac"}
	if got := rels(root, fs); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if st.Symlinks != 0 || st.TooDeep != 0 || len(st.Unreadable) != 0 {
		t.Fatalf("stats = %+v", st)
	}
	byName := map[string]Found{}
	for _, f := range fs {
		byName[f.Name] = f
	}
	if f := byName["x.aax"]; f.Kind != media.KindProtected || f.RelDir != "Au/B" || f.Size != 1 || f.MtimeNs == 0 {
		t.Fatalf("x.aax = %+v", f)
	}
	if f := byName["top.flac"]; f.RelDir != "" || f.Kind != media.KindAudio {
		t.Fatalf("top.flac = %+v", f)
	}
}

func TestWalkSkipsLinksAndPipes(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	touch(t, filepath.Join(root, "real/a.mp3"))
	touch(t, filepath.Join(outside, "secret/s.mp3"))
	touch(t, filepath.Join(outside, "l.mp3"))
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "linkdir")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "l.mp3"), filepath.Join(root, "real/l.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "real/loop")); err != nil { // a cycle
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "real/f.mp3"), 0o644); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	done := make(chan struct{})
	var fs []Found
	var st WalkStats
	var err error
	go func() {
		fs, st, err = walk(context.Background(), root)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("walk blocked on a named pipe")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rels(root, fs), []string{"real/a.mp3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if st.Symlinks != 3 {
		t.Fatalf("Symlinks = %d, want 3", st.Symlinks)
	}
}

func TestWalkUnavailableFolder(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.mp3")
	touch(t, file)
	for name, p := range map[string]string{"missing": filepath.Join(dir, "nope"), "not a directory": file} {
		if _, _, err := walk(context.Background(), p); !errors.Is(err, errFolderUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWalkUnreadableSubtree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode 000 directory")
	}
	root := t.TempDir()
	touch(t, filepath.Join(root, "ok/a.mp3"))
	touch(t, filepath.Join(root, "locked/b.mp3"))
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	fs, st, err := walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rels(root, fs), []string{"ok/a.mp3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(st.Unreadable, []string{locked}) {
		t.Fatalf("Unreadable = %v", st.Unreadable)
	}
}

func TestWalkDepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := root
	for i := 0; i < maxDepth+3; i++ {
		deep = filepath.Join(deep, "d")
	}
	touch(t, filepath.Join(deep, "deep.mp3"))
	touch(t, filepath.Join(root, "d/d/ok.mp3"))
	fs, st, err := walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rels(root, fs), []string{"d/d/ok.mp3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if st.TooDeep == 0 {
		t.Fatal("TooDeep not counted")
	}
}

func TestWalkStopsOnCancel(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "a/b.mp3"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := walk(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
