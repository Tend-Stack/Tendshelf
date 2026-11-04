package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

// maxDepth is how many directories below the library folder the walk reads. Deeper entries are
// ignored and counted, so a runaway tree cannot hold a scan forever.
const maxDepth = 16

// errFolderUnavailable means the library folder is gone, is not a directory or cannot be read.
var errFolderUnavailable = errors.New("scan: folder unavailable")

// Found is one audio file the walk saw. Path is absolute; RelDir is the directory below the
// library folder with forward slashes ("" for a file directly in it).
type Found struct {
	Path, RelDir, Name string
	Kind               media.Kind
	Size, MtimeNs      int64
}

// WalkStats counts what the walk skipped on purpose.
type WalkStats struct {
	Symlinks   int      // links to files or directories, never followed
	TooDeep    int      // entries deeper than maxDepth
	Unreadable []string // directories (or files) that could not be read; nothing under them changes
}

// SkipName reports whether a directory or file name is junk that is never scanned: hidden entries
// (which covers .DS_Store and the ._ files macOS leaves on other file systems) and the folders NAS
// boxes, Windows and macOS keep beside a library.
func SkipName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "@eadir", "#recycle", "#snapshot", "__macosx", "lost+found", "$recycle.bin", "system volume information":
		return true
	}
	return false
}

// walk lists the audio files under root. It reads directories and file metadata only: it never
// follows a symbolic link, never opens a file, and a pipe or device is skipped by its type before
// anything touches it. A directory that cannot be read is recorded in WalkStats.Unreadable and
// its subtree is skipped. The result is sorted by path.
func walk(ctx context.Context, root string) (files []Found, st WalkStats, err error) {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return nil, st, errFolderUnavailable
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if werr != nil {
			if path == root {
				return errFolderUnavailable
			}
			// A directory whose listing failed (WalkDir calls again with the error), or an entry
			// that vanished or cannot be statted.
			if d != nil && d.IsDir() {
				st.Unreadable = append(st.Unreadable, path)
				return fs.SkipDir
			}
			if !errors.Is(werr, fs.ErrNotExist) {
				st.Unreadable = append(st.Unreadable, path)
			}
			return nil
		}
		if path == root {
			return nil
		}
		if SkipName(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			st.Symlinks++
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator)) // directories above the entry
		if d.IsDir() {
			if depth >= maxDepth {
				st.TooDeep++
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		kind := media.Classify(d.Name())
		if kind == media.KindOther {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			if !errors.Is(ierr, fs.ErrNotExist) {
				st.Unreadable = append(st.Unreadable, path)
			}
			return nil
		}
		relDir := filepath.ToSlash(filepath.Dir(rel))
		if relDir == "." {
			relDir = ""
		}
		files = append(files, Found{Path: path, RelDir: relDir, Name: d.Name(), Kind: kind,
			Size: info.Size(), MtimeNs: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, st, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(st.Unreadable)
	return files, st, nil
}
