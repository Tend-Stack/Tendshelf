package upload

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// lroot is the chosen upload folder, opened as an os.Root. Every library-side operation goes through
// it, so a parent directory swapped for a symbolic link after a check cannot lead outside the
// folder: the kernel resolves each path below the open folder and refuses to leave it. Callers pass
// absolute paths (the ledger stores them); rel checks they lie inside before use.
type lroot struct {
	r    *os.Root
	path string
}

func openLroot(path string) (*lroot, error) {
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &lroot{r: r, path: filepath.Clean(path)}, nil
}

func (l *lroot) Close() { _ = l.r.Close() }

// rel turns an absolute path inside the folder into the name relative to it.
func (l *lroot) rel(abs string) (string, error) {
	if !within(l.path, abs) {
		return "", fmt.Errorf("upload: %q is outside the upload folder", abs)
	}
	r, err := filepath.Rel(l.path, filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	return r, nil
}

func (l *lroot) Lstat(abs string) (fs.FileInfo, error) {
	r, err := l.rel(abs)
	if err != nil {
		return nil, err
	}
	return l.r.Lstat(r)
}

func (l *lroot) Mkdir(abs string, perm os.FileMode) error {
	r, err := l.rel(abs)
	if err != nil || r == "." {
		return fmt.Errorf("upload: cannot create %q", abs)
	}
	return l.r.Mkdir(r, perm)
}

// MkdirAll creates abs and any missing folder above it inside the root, one Mkdir at a time, so every step is
// resolved below the open folder. A name that exists as anything but a folder (or as a link) is an error.
func (l *lroot) MkdirAll(abs string, perm os.FileMode) error {
	r, err := l.rel(abs)
	if err != nil {
		return err
	}
	if r == "." {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(r), "/") {
		cur = filepath.Join(cur, part)
		fi, err := l.r.Lstat(cur)
		switch {
		case err == nil:
			if !isDirNoLink(fi) {
				return fmt.Errorf("upload: %q is not a folder", filepath.Join(l.path, cur))
			}
		case errors.Is(err, fs.ErrNotExist):
			if err := l.r.Mkdir(cur, perm); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func (l *lroot) OpenFile(abs string, flag int, perm os.FileMode) (*os.File, error) {
	r, err := l.rel(abs)
	if err != nil || r == "." {
		return nil, fmt.Errorf("upload: cannot open %q", abs)
	}
	return l.r.OpenFile(r, flag, perm)
}

func (l *lroot) Open(abs string) (*os.File, error) { return l.OpenFile(abs, os.O_RDONLY, 0) }

func (l *lroot) Rename(from, to string) error {
	a, err := l.rel(from)
	if err != nil || a == "." {
		return fmt.Errorf("upload: cannot rename %q", from)
	}
	b, err := l.rel(to)
	if err != nil || b == "." {
		return fmt.Errorf("upload: cannot rename to %q", to)
	}
	return l.r.Rename(a, b)
}

// Remove deletes one file or one empty directory.
func (l *lroot) Remove(abs string) error {
	r, err := l.rel(abs)
	if err != nil || r == "." {
		return fmt.Errorf("upload: cannot remove %q", abs)
	}
	return l.r.Remove(r)
}

func (l *lroot) RemoveAll(abs string) error {
	r, err := l.rel(abs)
	if err != nil || r == "." {
		return fmt.Errorf("upload: cannot remove %q", abs)
	}
	return l.r.RemoveAll(r)
}

func (l *lroot) ReadDir(abs string) ([]fs.DirEntry, error) {
	r, err := l.rel(abs)
	if err != nil {
		return nil, err
	}
	f, err := l.r.Open(r)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}
