package ebook

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNoImage is returned by OpenImage when the entry is missing, too large or not a picture the reader
// serves (JPEG, PNG, GIF, WebP; SVG and everything else are refused).
var ErrNoImage = errors.New("ebook: no such image")

// MaxImageBytes caps one picture the reader serves.
const MaxImageBytes = 20 << 20

// OpenImage reads one picture out of an EPUB, read-only, through the same bounded zip view the parser
// uses. zipPath comes from a stored document (Block.Img), never from a request; it is only ever looked
// up in the archive, never joined to a file system path. kind is the sniffed type ("jpeg", "png", "gif"
// or "webp").
func OpenImage(path, zipPath string, lim Limits) (data []byte, kind string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if fi.Size() > lim.MaxFileBytes {
		return nil, "", ErrNoImage
	}
	zb, err := openZip(f, fi.Size(), lim)
	if err != nil {
		return nil, "", ErrNoImage
	}
	zf := zb.find(zipPath)
	if zf == nil || zf.UncompressedSize64 > MaxImageBytes {
		return nil, "", ErrNoImage
	}
	data, err = zb.Read(zipPath)
	if err != nil {
		return nil, "", ErrNoImage
	}
	if kind = ImageKind(data); kind == "" {
		return nil, "", ErrNoImage
	}
	return data, kind, nil
}

// FilePath is where the bytes of an ebook file are read from: its place in the library, or Tendshelf's own
// copy of an upload.
func (s *Service) FilePath(f File) string {
	if f.Origin == "upload" {
		return s.storedPath(f.SHA256, f.Ext)
	}
	return filepath.Clean(f.Path)
}

// Limits are the bounds every read of an ebook file uses.
func (s *Service) Limits() Limits { return s.limits }
