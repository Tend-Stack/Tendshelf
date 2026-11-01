package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func fbMkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListFoldersOnlyFolders(t *testing.T) {
	root := t.TempDir()
	fbMkdir(t, root, "Saltmarsh")
	fbMkdir(t, root, "kids")
	for _, n := range []string{"secret-title.mp3", "b.m4b", "c.aax", "notes.txt", "cover.jpg"} {
		touch(t, filepath.Join(root, n))
	}
	l, err := ListFolders(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"kids", "Saltmarsh"}; !reflect.DeepEqual(l.Folders, want) {
		t.Errorf("Folders = %v, want %v", l.Folders, want)
	}
	if l.AudioHere != 3 {
		t.Errorf("AudioHere = %d, want 3 (mp3, m4b, aax; not txt or jpg)", l.AudioHere)
	}
	if l.Path != root || l.Parent != filepath.Dir(root) || l.TooMany {
		t.Errorf("listing = %+v", l)
	}
}

func TestListFoldersSkips(t *testing.T) {
	root := t.TempDir()
	fbMkdir(t, root, "Books")
	fbMkdir(t, root, ".hidden")
	fbMkdir(t, root, "@eaDir")
	fbMkdir(t, root, "#recycle")
	data := fbMkdir(t, root, "tendshelf-data")
	target := fbMkdir(t, t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(root, "link-to-dir")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	touch(t, filepath.Join(target, "x.mp3"))
	if err := os.Symlink(filepath.Join(target, "x.mp3"), filepath.Join(root, "linked.mp3")); err != nil {
		t.Fatal(err)
	}
	l, err := ListFolders(context.Background(), root, func(full string) bool { return full == data })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Books"}; !reflect.DeepEqual(l.Folders, want) {
		t.Errorf("Folders = %v, want %v", l.Folders, want)
	}
	if l.AudioHere != 0 {
		t.Errorf("a linked audio file was counted: %d", l.AudioHere)
	}
}

func TestListFoldersSorted(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"banana", "Apple", "cherry", "apple2", "Zed"} {
		fbMkdir(t, root, n)
	}
	l, err := ListFolders(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Apple", "apple2", "banana", "cherry", "Zed"}; !reflect.DeepEqual(l.Folders, want) {
		t.Errorf("Folders = %v, want %v", l.Folders, want)
	}
}

func TestListFoldersCaps(t *testing.T) {
	root := t.TempDir()
	for i := 0; i <= maxBrowseEntries; i++ {
		touch(t, filepath.Join(root, fmt.Sprintf("f%05d.txt", i)))
	}
	l, err := ListFolders(context.Background(), root, nil)
	if err != nil || !l.TooMany || l.Folders != nil || l.Path != root {
		t.Errorf("5,001 entries: %+v, %v", l, err)
	}

	exact := t.TempDir()
	for i := 0; i < maxBrowseEntries; i++ {
		touch(t, filepath.Join(exact, fmt.Sprintf("f%05d.txt", i)))
	}
	if l, err = ListFolders(context.Background(), exact, nil); err != nil || l.TooMany {
		t.Errorf("exactly 5,000 entries: %+v, %v", l, err)
	}

	dirs := t.TempDir()
	for i := 0; i <= maxBrowseFolders; i++ {
		fbMkdir(t, dirs, fmt.Sprintf("d%03d", i))
	}
	l, err = ListFolders(context.Background(), dirs, nil)
	if err != nil || !l.TooMany || l.Folders != nil {
		t.Errorf("501 folders: %+v, %v", l, err)
	}
}

func TestListFoldersErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.mp3")
	touch(t, file)
	locked := fbMkdir(t, root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	cases := map[string]string{"relative": "a/b", "empty": "", "missing": filepath.Join(root, "nope"), "file": file}
	if os.Geteuid() != 0 && runtime.GOOS != "windows" {
		cases["unreadable"] = locked
	}
	for name, p := range cases {
		if _, err := ListFolders(context.Background(), p, nil); !errors.Is(err, ErrNotFolder) {
			t.Errorf("%s: err = %v, want ErrNotFolder", name, err)
		}
	}
}

func TestListFoldersDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListFolders(ctx, t.TempDir(), nil); !errors.Is(err, ErrTooSlow) {
		t.Errorf("err = %v, want ErrTooSlow", err)
	}
}

func TestListFoldersRootSystemNames(t *testing.T) {
	cases := []struct {
		goos, name string
		want       bool
	}{
		{"linux", "usr", true}, {"linux", "proc", true}, {"linux", "run", true}, {"linux", "Var", true},
		{"linux", "media", false}, {"linux", "mnt", false}, {"linux", "srv", false}, {"linux", "home", false}, {"linux", "volume1", false},
		{"darwin", "System", true}, {"darwin", "Applications", true}, {"darwin", "Volumes", false}, {"darwin", "Users", false},
		{"windows", "Windows", true}, {"windows", "program files (x86)", true}, {"windows", "ProgramData", true},
		{"windows", "Users", false}, {"windows", "Audiobooks", false},
		{"linux", "bin|boot", false},
	}
	for _, c := range cases {
		if got := rootHidden(c.goos, c.name); got != c.want {
			t.Errorf("rootHidden(%s, %q) = %v, want %v", c.goos, c.name, got, c.want)
		}
	}
	if !isVolumeRoot(string(filepath.Separator)) || isVolumeRoot(t.TempDir()) {
		t.Error("isVolumeRoot")
	}
}

func TestPlaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the cases spell paths the POSIX way")
	}
	exists := func(set ...string) func(string) bool {
		m := map[string]bool{}
		for _, s := range set {
			m[s] = true
		}
		return func(p string) bool { return m[p] }
	}
	cases := []struct {
		name, goos, home string
		dirs             []string
		want             []Place
	}{
		{"linux", "linux", "/home/ann", []string{"/home/ann", "/media", "/mnt", "/volume1", "/volume3", "/"},
			[]Place{{"Home folder", "/home/ann"}, {"/media", "/media"}, {"/mnt", "/mnt"}, {"/volume1", "/volume1"}, {"/volume3", "/volume3"}, {"This computer", "/"}}},
		{"linux run media", "linux", "/home/ann", []string{"/run/media", "/srv", "/"},
			[]Place{{"/run/media", "/run/media"}, {"/srv", "/srv"}, {"This computer", "/"}}},
		{"home is root, no repeat", "linux", "/", []string{"/"}, []Place{{"Home folder", "/"}}},
		{"home missing", "linux", "/home/gone", []string{"/mnt"}, []Place{{"/mnt", "/mnt"}}},
		{"relative home ignored", "linux", "home/ann", []string{"/mnt", "home/ann"}, []Place{{"/mnt", "/mnt"}}},
		{"darwin", "darwin", "/Users/ann", []string{"/Users/ann", "/Volumes", "/"},
			[]Place{{"Home folder", "/Users/ann"}, {"/Volumes", "/Volumes"}, {"This computer", "/"}}},
		{"windows drives", "windows", "/home/ann", []string{"/home/ann", `C:\`, `D:\`},
			[]Place{{"Home folder", "/home/ann"}, {"Drive C:", `C:\`}, {"Drive D:", `D:\`}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Places(c.goos, c.home, exists(c.dirs...))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Places = %v, want %v", got, c.want)
			}
		})
	}
}
