package media

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const quickHashSpan = 1 << 20

// QuickHash fingerprints a file cheaply, to recognise a moved or renamed file without reading
// all of it: SHA-256 over the size (uint64, big-endian), the first 1 MiB and the last 1 MiB (the
// two never overlap; a file under 2 MiB is hashed whole). It returns the lower-case hex digest and
// the size. The file is opened read-only and only when it is a regular file, so a named pipe or
// device can never block it.
func QuickHash(path string) (hash string, size int64, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %s", ErrNotRegular, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return QuickHashFile(f)
}

// QuickHashFile is QuickHash for a file the caller has already opened (for example through an
// os.Root, so the path cannot be swapped for a link). The caller closes it.
func QuickHashFile(f *os.File) (hash string, size int64, err error) {
	path := f.Name()
	// Size comes from the open file, so it is consistent with the bytes read below.
	fi, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	n := fi.Size()
	headLen := min(n, quickHashSpan)
	tailStart := max(headLen, n-quickHashSpan)

	h := sha256.New()
	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], uint64(n))
	h.Write(sz[:])
	for _, span := range [][2]int64{{0, headLen}, {tailStart, n}} {
		if err := copySpan(h, f, span[0], span[1]); err != nil {
			return "", 0, fmt.Errorf("quick hash of %s: %w", path, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// copySpan writes bytes [from, to) of f to w. A file that shrank mid-read is io.ErrUnexpectedEOF.
func copySpan(w io.Writer, f *os.File, from, to int64) error {
	if to <= from {
		return nil
	}
	got, err := io.Copy(w, io.NewSectionReader(f, from, to-from))
	if err != nil {
		return err
	}
	if got != to-from {
		return io.ErrUnexpectedEOF
	}
	return nil
}
