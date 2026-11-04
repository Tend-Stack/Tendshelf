package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

// The folder browser lists folder names so an owner can pick a library folder instead of typing
// its path. It reads directories only: it never opens a file, never follows a symbolic link, never
// returns a file name, and every listing is bounded in entries, folders and time.
const (
	maxBrowseEntries = 5000
	maxBrowseFolders = 500
	browseBatch      = 256
	browseDeadline   = 3 * time.Second
)

// ErrNotFolder means the path is not absolute, does not exist, is not a folder or can not be read.
var ErrNotFolder = errors.New("scan: not a folder")

// ErrTooSlow means reading the folder took longer than the browser allows.
var ErrTooSlow = errors.New("scan: folder took too long")

// Place is a starting point for browsing. Label is interface copy.
type Place struct{ Label, Path string }

// Listing is what ListFolders found in one folder.
type Listing struct {
	Path string
	// Parent is the folder above Path, or "" at a volume root.
	Parent string
	// Folders are the sub-folder names, sorted without regard to case.
	Folders []string
	// AudioHere counts the audio (and copy-protected audio) files directly in Path.
	AudioHere int
	// TooMany is set when the folder has too many entries or folders to list; Folders is then nil.
	TooMany bool
}

// Places lists the starting points that exist on this kind of computer, home folder first, without
// repeats. isDir tells whether a path is a folder (tests pass a fake; the web layer passes a stat).
func Places(goos, home string, isDir func(string) bool) []Place {
	var out []Place
	seen := map[string]bool{}
	add := func(label, path string) {
		if path == "" || seen[path] || !isDir(path) {
			return
		}
		seen[path] = true
		if label == "" {
			label = path
		}
		out = append(out, Place{Label: label, Path: path})
	}
	if filepath.IsAbs(home) {
		add("Home folder", filepath.Clean(home))
	}
	switch goos {
	case "windows":
		for c := 'A'; c <= 'Z'; c++ {
			add(fmt.Sprintf("Drive %c:", c), fmt.Sprintf("%c:\\", c))
		}
	case "darwin":
		add("", "/Volumes")
		add("This computer", "/")
	default:
		for _, p := range []string{"/media", "/run/media", "/mnt", "/srv"} {
			add("", p)
		}
		for i := 1; i <= 9; i++ {
			add("", fmt.Sprintf("/volume%d", i))
		}
		add("This computer", "/")
	}
	return out
}

// rootHidden reports whether a folder at the top of a file system is an operating-system folder
// nobody keeps audiobooks in. It is applied only directly under a volume root.
func rootHidden(goos, name string) bool {
	var names []string
	switch goos {
	case "windows":
		names = []string{"windows", "program files", "program files (x86)", "programdata"}
	case "darwin":
		names = []string{"system", "library", "private", "cores", "dev", "bin", "sbin", "usr", "etc", "var", "tmp", "applications"}
	default:
		names = []string{"bin", "boot", "dev", "etc", "lib", "lib32", "lib64", "libx32", "proc", "root", "run", "sbin", "snap", "sys", "tmp", "usr", "var"}
	}
	lower := strings.ToLower(name)
	for _, n := range names {
		if lower == n {
			return true
		}
	}
	return false
}

// isVolumeRoot reports whether p is the top of a file system ("/" or "C:\").
func isVolumeRoot(p string) bool { return filepath.Dir(p) == p }

// ListFolders lists the folders directly inside path. hide, when set, drops a folder by its full
// path (the web layer hides the data folder). Symbolic links are neither listed nor followed, hidden
// and junk names are skipped as the scan's walk skips them, and file names are never returned: the
// only file-derived result is AudioHere.
func ListFolders(ctx context.Context, path string, hide func(full string) bool) (Listing, error) {
	if !filepath.IsAbs(path) {
		return Listing{}, ErrNotFolder
	}
	path = filepath.Clean(path)
	ctx, cancel := context.WithTimeout(ctx, browseDeadline)
	defer cancel()
	if ctx.Err() != nil {
		return Listing{}, ErrTooSlow
	}
	f, err := os.Open(path)
	if err != nil {
		return Listing{}, ErrNotFolder
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.IsDir() {
		return Listing{}, ErrNotFolder
	}
	l := Listing{Path: path}
	atRoot := isVolumeRoot(path)
	if !atRoot {
		l.Parent = filepath.Dir(path)
	}
	read := 0
	for {
		if ctx.Err() != nil {
			return Listing{}, ErrTooSlow
		}
		n := browseBatch
		if left := maxBrowseEntries + 1 - read; left < n {
			n = left
		}
		ents, err := f.ReadDir(n)
		for _, d := range ents {
			read++
			if read > maxBrowseEntries {
				return Listing{Path: path, Parent: l.Parent, TooMany: true}, nil
			}
			name := d.Name()
			if SkipName(name) || d.Type()&fs.ModeSymlink != 0 {
				continue
			}
			switch {
			case d.IsDir():
				if atRoot && rootHidden(runtime.GOOS, name) {
					continue
				}
				if hide != nil && hide(filepath.Join(path, name)) {
					continue
				}
				l.Folders = append(l.Folders, name)
			case d.Type().IsRegular():
				if k := media.Classify(name); k == media.KindAudio || k == media.KindProtected {
					l.AudioHere++
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Listing{}, ErrNotFolder
		}
	}
	if len(l.Folders) > maxBrowseFolders {
		return Listing{Path: path, Parent: l.Parent, TooMany: true}, nil
	}
	sort.Slice(l.Folders, func(i, j int) bool {
		a, b := strings.ToLower(l.Folders[i]), strings.ToLower(l.Folders[j])
		if a != b {
			return a < b
		}
		return l.Folders[i] < l.Folders[j]
	})
	return l, nil
}
