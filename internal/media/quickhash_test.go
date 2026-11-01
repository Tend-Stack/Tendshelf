package media_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

const mib = 1 << 20

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + 3) % 251)
	}
	return b
}

// reference hashes the whole definition from a byte slice, the slow way.
func reference(b []byte) string {
	n := len(b)
	head := b[:min(n, mib)]
	tail := b[max(len(head), n-mib):]
	h := sha256.New()
	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], uint64(n))
	h.Write(sz[:])
	h.Write(head)
	h.Write(tail)
	return hex.EncodeToString(h.Sum(nil))
}

func TestQuickHash(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{0, 1, mib - 1, mib, mib + 1, 2 * mib, 2*mib + 1} {
		p := filepath.Join(dir, "f.bin")
		data := pattern(n)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		got, size, err := media.QuickHash(p)
		if err != nil || size != int64(n) || got != reference(data) {
			t.Errorf("size %d: hash %s size %d err %v; want %s", n, got, size, err, reference(data))
		}
	}
}

func TestQuickHashGoldens(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	for _, c := range []struct {
		n    int
		want string
	}{
		{0, "af5570f5a1810b7af78caf4bc70a660f0df51e42baf91d4de5b2328de0e83dfc"},
		{3*mib + 7, "15da23af2740fd1928ea6d92b475fb58f05dff2b803bcb9054da6a1a55c98026"},
	} {
		if err := os.WriteFile(p, pattern(c.n), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _, err := media.QuickHash(p); err != nil || got != c.want {
			t.Errorf("size %d: hash %s, err %v; want %s", c.n, got, err, c.want)
		}
	}
}

func TestQuickHashMiddleIsIgnoredEdgesAreNot(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	data := pattern(4 * mib)
	write := func() string {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		h, _, err := media.QuickHash(p)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := write()
	data[2*mib] ^= 0xff
	if write() != base {
		t.Error("a change in the middle must not change the quick hash")
	}
	data[10] ^= 0xff
	if write() == base {
		t.Error("a change in the first MiB must change the quick hash")
	}
}

func TestQuickHashNotRegularOrMissing(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := media.QuickHash(dir); !errors.Is(err, media.ErrNotRegular) {
		t.Errorf("directory err = %v", err)
	}
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no fifo support:", err)
	}
	if _, _, err := media.QuickHash(fifo); !errors.Is(err, media.ErrNotRegular) {
		t.Errorf("fifo err = %v", err)
	}
	if _, _, err := media.QuickHash(filepath.Join(dir, "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing err = %v", err)
	}
}
