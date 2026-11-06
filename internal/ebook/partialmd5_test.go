package ebook

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// partialMD5Offsets spells out where KOReader samples, so the test does not share the code's loop.
var partialMD5Offsets = []int64{0, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824}

func partialMD5Reference(data []byte) string {
	h := md5.New()
	for _, off := range partialMD5Offsets {
		if off >= int64(len(data)) {
			break
		}
		end := min(off+1024, int64(len(data)))
		h.Write(data[off:end])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestPartialMD5(t *testing.T) {
	for _, size := range []int{0, 1, 1000, 1024, 1025, 1500, 5000, 70000, 2 << 20} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*7 + i/251)
		}
		p := filepath.Join(t.TempDir(), "b.epub")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := PartialMD5(p)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if want := partialMD5Reference(data); got != want {
			t.Errorf("size %d: %s, want %s", size, got, want)
		}
	}
}

func TestPartialMD5Sparse(t *testing.T) {
	// A sparse 300 MiB file: the samples at 256 MiB and the ones before it count, none past the end do.
	p := filepath.Join(t.TempDir(), "big.pdf")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("tail"), 268435456+10); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := PartialMD5(p)
	if err != nil {
		t.Fatal(err)
	}
	h := md5.New()
	for range partialMD5Offsets[:10] {
		h.Write(make([]byte, 1024))
	}
	tail := make([]byte, 14)
	copy(tail[10:], "tail")
	h.Write(tail)
	if want := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Errorf("sparse: %s, want %s", got, want)
	}
}

func TestPartialMD5Missing(t *testing.T) {
	if _, err := PartialMD5(filepath.Join(t.TempDir(), "none.epub")); err == nil {
		t.Error("a missing file gave no error")
	}
}
