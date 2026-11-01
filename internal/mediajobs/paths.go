// Package mediajobs runs the media jobs that follow a scan: cover pictures with their thumbnails and
// accent colours (media.cover), and browser-friendly compatibility copies (media.compat).
//
// Library files are only read. ffmpeg writes under the data directory alone, into a ".tmp-*" file
// in the target directory that is renamed into place:
//
//	<data>/covers/<sha256>.<jpg|png>        the stored cover (the bytes of the embedded or folder picture)
//	<data>/cache/thumbs/<sha256>-<box>.jpg  thumbnails of 160, 320 and 640 px
//	<data>/cache/compat/<quick_hash>-r<recipe>.m4a  compatibility copies (a disposable cache)
//
// The package imports library, media and jobs; it never imports scan or metadata. The scanner
// reaches it through a follower (Covers.Follower) that cmd/tendshelf registers.
package mediajobs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// ThumbSizes are the thumbnail boxes in pixels, smallest first.
var ThumbSizes = []int{160, 320, 640}

const (
	dirMode = 0o750
	// fileMode is for stored covers, thumbnails and compatibility copies.
	fileMode  = 0o640
	tmpPrefix = ".tmp-"
	// tmpMaxAge is how old a leftover temporary file must be before it is swept.
	tmpMaxAge = time.Hour
)

// Enqueuer is the part of jobs.Runner the follower and the compat service use.
type Enqueuer interface {
	Enqueue(ctx context.Context, kind string, payload any, o jobs.EnqueueOptions) (string, error)
	ActiveKeys(ctx context.Context, kind, field string) (map[string]bool, error)
}

// CoversDir is where stored covers live.
func CoversDir(data string) string { return filepath.Join(data, "covers") }

// ThumbsDir is where thumbnails live.
func ThumbsDir(data string) string { return filepath.Join(data, "cache", "thumbs") }

// CompatDir is where compatibility copies live.
func CompatDir(data string) string { return filepath.Join(data, "cache", "compat") }

// ThumbPath is the thumbnail of the cover with this hash, fitted in box by box pixels.
func ThumbPath(data, sha string, box int) string {
	return filepath.Join(ThumbsDir(data), sha+"-"+strconv.Itoa(box)+".jpg")
}

// sweepTemp removes ".tmp-*" files older than an hour from dir: leftovers of a crashed run.
func sweepTemp(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && now.Sub(fi.ModTime()) > tmpMaxAge {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// writeAtomic writes data to dir/name through a temporary file in dir.
func writeAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, fileMode)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func exists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
