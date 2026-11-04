package upload

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Limits for one archive (plan U3). The entry count and the ratio are the plan's numbers. The
// ratio is only judged on entries over minRatioBytes: a few kilobytes of padding compress far
// beyond 200:1 and are harmless, and a bomb is never small.
const (
	maxZipEntries = 2000
	maxZipRatio   = 200
	minRatioBytes = 64 << 10
)

// isZip reports whether a manifest path names a zip archive.
func isZip(p string) bool { return strings.EqualFold(filepath.Ext(p), ".zip") }

// zipFail is a permanent failure of the whole book; the words are plain, the cause goes to the log.
func zipFail(code, text string, cause error) error {
	return &jobs.Error{Code: code, Text: text, Permanent: true, Err: cause}
}

// zipLimits are the byte limits for one extraction: the most that may be written in total.
type zipLimits struct {
	budget int64
	free   int64 // free bytes where the files are written, margin already taken off
}

// extracted is one file written from an archive.
type extracted struct {
	rel  string // slash form, already checked
	file string // where it was written
	size int64
}

// extractZip writes the audio, pictures and sidecars of an archive into dst, a folder the caller
// owns, naming each file with name(i) (i counts the written files). Nothing else is written
// anywhere: entry names are checked first, links and nested archives are left out, encrypted
// entries fail the book, and the bytes actually written are counted, whatever the headers say.
// left is the number of entries not extracted (junk, links, nested archives, other kinds). When
// an entry is copy-protected audio, protected names it and nothing is written.
func extractZip(ctx context.Context, zipPath string, lim zipLimits, name func(i int) string) (out []extracted, left int, protected string, err error) {
	zr, err := zip.OpenReader(zipPath)
	// Go reports names with "..", a leading slash or a backslash as ErrInsecurePath together with a
	// usable reader; the name check below fails the book on them with its own code.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, 0, "", zipFail("zip_bad", "Tendshelf could not read that zip file. Check that it is not damaged.", err)
	}
	defer zr.Close()
	if len(zr.File) > maxZipEntries {
		return nil, 0, "", zipFail("zip_too_large", "That zip file holds too many files.", nil)
	}

	// Pass 1: names and headers only. Any bad name fails the book before a byte is written.
	var keep []*zip.File
	seen := map[string]bool{}
	var declared int64
	for _, f := range zr.File {
		n := f.Name
		isDir := strings.HasSuffix(n, "/") || f.FileInfo().IsDir()
		n = strings.TrimSuffix(n, "/")
		if msg := checkPath(n); msg != "" {
			return nil, 0, "", zipFail("bad_path", "That zip file holds a file name Tendshelf cannot use.", errors.New(msg+" "+f.Name))
		}
		if isDir {
			continue
		}
		low := strings.ToLower(n)
		if seen[low] {
			return nil, 0, "", zipFail("bad_path", "That zip file holds two files with the same name.", errors.New(f.Name))
		}
		seen[low] = true
		if f.Flags&0x1 != 0 {
			return nil, 0, "", zipFail("zip_encrypted", "That zip file is password-protected, so Tendshelf cannot open it.", nil)
		}
		if f.Mode()&fs.ModeSymlink != 0 || f.Mode()&fs.ModeType != 0 {
			left++
			continue
		}
		if !kept(n) {
			left++
			continue
		}
		if media.Classify(n) == media.KindProtected {
			return nil, 0, n, nil
		}
		if f.UncompressedSize64 == 0 {
			left++
			continue
		}
		if f.UncompressedSize64 > uint64(lim.budget) {
			return nil, 0, "", zipFail("zip_too_large", "That zip file holds more than one book can hold.", nil)
		}
		if f.UncompressedSize64 > minRatioBytes && f.CompressedSize64 > 0 &&
			f.UncompressedSize64/f.CompressedSize64 > maxZipRatio {
			return nil, 0, "", zipFail("zip_bomb", "That zip file does not look like a real book, so Tendshelf will not open it.", nil)
		}
		declared += int64(f.UncompressedSize64)
		if declared > lim.budget {
			return nil, 0, "", zipFail("zip_too_large", "That zip file holds more than one book can hold.", nil)
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		return nil, left, "", nil
	}
	if declared > lim.free {
		return nil, 0, "", &jobs.Error{Code: "no_space", Text: "The server's data folder is full. Free some space, then press Try again.", Permanent: true}
	}

	// Pass 2: extract. Every file is created new (O_EXCL) at a name we chose, so no entry name
	// reaches the file system. The bytes written are counted through a limit one over the header.
	remaining := lim.budget
	for i, f := range keep {
		if ctx.Err() != nil {
			return nil, 0, "", context.Cause(ctx)
		}
		rel := strings.TrimSuffix(f.Name, "/")
		dst := name(i)
		n, err := writeEntry(f, dst, remaining)
		if err != nil {
			return nil, 0, "", err
		}
		remaining -= n
		out = append(out, extracted{rel: path.Clean(rel), file: dst, size: n})
	}
	return out, left, "", nil
}

// writeEntry copies one entry to dst. A header that lies low (more bytes than it says) fails the
// book as too large; the ratio is checked again on the bytes read.
func writeEntry(f *zip.File, dst string, remaining int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, zipFail("zip_bad", "Tendshelf could not read that zip file. Check that it is not damaged.", err)
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	want := int64(f.UncompressedSize64)
	n, err := io.Copy(out, io.LimitReader(rc, want+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case n > want || n > remaining:
		return 0, zipFail("zip_too_large", "That zip file holds more than one book can hold.", nil)
	case err != nil && (errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrChecksum) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "flate")):
		return 0, zipFail("zip_bad", "Tendshelf could not read that zip file. Check that it is not damaged.", err)
	case err != nil:
		return 0, err
	case n != want:
		return 0, zipFail("zip_bad", "Tendshelf could not read that zip file. Check that it is not damaged.", fmt.Errorf("%s: %d bytes, header says %d", f.Name, n, want))
	}
	return n, nil
}
