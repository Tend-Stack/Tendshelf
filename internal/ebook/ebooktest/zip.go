// Package ebooktest builds small synthetic ebook files for tests. Every byte of text is invented here;
// nothing comes from a real book.
package ebooktest

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"hash/crc32"
	"testing"
	"time"
)

// Entry is one zip entry. Flags is the raw general-purpose flag word (bit 0 = encrypted). When
// Declared is not zero the header states that uncompressed size instead of the real one.
type Entry struct {
	Name     string
	Data     []byte
	Flags    uint16
	Declared uint64
}

// fixedTime keeps zips byte-identical between runs.
var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Zip writes entries in order with deflate and a fixed modification time.
func Zip(t testing.TB, entries []Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.Name, Method: zip.Deflate, Flags: e.Flags, Modified: fixedTime}
		if e.Declared != 0 {
			var raw bytes.Buffer
			fw, _ := flate.NewWriter(&raw, flate.BestCompression)
			_, _ = fw.Write(e.Data)
			_ = fw.Close()
			h.CRC32 = crc32.ChecksumIEEE(e.Data)
			h.CompressedSize64 = uint64(raw.Len())
			h.UncompressedSize64 = e.Declared
			w, err := zw.CreateRaw(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(raw.Bytes()); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
