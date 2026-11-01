package wav

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWAVBytes(t *testing.T) {
	for _, ms := range []int{0, 1000, 3000} {
		b := Bytes(ms, 440)
		if want := 44 + ms*16; len(b) != want {
			t.Fatalf("%d ms: length %d, want %d", ms, len(b), want)
		}
		for off, tag := range map[int]string{0: "RIFF", 8: "WAVE", 12: "fmt ", 36: "data"} {
			if string(b[off:off+4]) != tag {
				t.Errorf("%d ms: marker at %d = %q, want %q", ms, off, b[off:off+4], tag)
			}
		}
		if got := binary.LittleEndian.Uint32(b[24:]); got != 8000 {
			t.Errorf("%d ms: sample rate %d", ms, got)
		}
		if got := binary.LittleEndian.Uint32(b[40:]); int(got) != ms*16 {
			t.Errorf("%d ms: data size %d, want %d", ms, got, ms*16)
		}
		if got := binary.LittleEndian.Uint32(b[4:]); int(got) != len(b)-8 {
			t.Errorf("%d ms: RIFF size %d, want %d", ms, got, len(b)-8)
		}
	}
	loud := false
	for _, c := range Bytes(20, 440)[44:] {
		if c != 0 {
			loud = true
		}
	}
	if !loud {
		t.Error("tone is silent")
	}
}

func TestWAVWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b.wav")
	if err := Write(p, 100, 440); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) != 44+1600 {
		t.Fatalf("read back: %d bytes, %v", len(b), err)
	}
}
