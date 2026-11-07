package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// pcmSquare returns n samples of a square wave of +-amp as little-endian s16.
func pcmSquare(n, amp int) []byte {
	out := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		v := int16(amp)
		if (i/4)%2 == 1 {
			v = -v
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(v))
	}
	return out
}

func reduce(t *testing.T, chunks ...[]byte) []byte {
	t.Helper()
	r := newPeakReducer(1 << 20)
	for _, c := range chunks {
		if n, err := r.Write(c); err != nil || n != len(c) {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	r.finish()
	return r.values
}

func TestPeakReducerLevels(t *testing.T) {
	for _, tc := range []struct {
		name string
		amp  int
		want int
	}{
		{"silence", 0, 0},
		{"full scale", 32767, 255},
		{"minus 20 dB", 3277, 170},
		{"minus 40 dB", 328, 85},
		{"below the floor", 20, 0},
	} {
		got := reduce(t, pcmSquare(peaksBinSample, tc.amp))
		if len(got) != 1 {
			t.Fatalf("%s: %d bins", tc.name, len(got))
		}
		if d := int(got[0]) - tc.want; d < -1 || d > 1 {
			t.Errorf("%s: level %d, want %d +-1", tc.name, got[0], tc.want)
		}
	}
}

func TestPeakReducerBins(t *testing.T) {
	// 4500 samples at 4000 per bin: one full bin and a partial one.
	if got := reduce(t, pcmSquare(4500, 8192)); len(got) != 2 {
		t.Errorf("4500 samples gave %d bins, want 2", len(got))
	}
	if got := reduce(t); len(got) != 0 {
		t.Errorf("no samples gave %d bins", len(got))
	}
	// Writing one byte at a time (an odd split of every sample) equals one write.
	pcm := append(pcmSquare(4000, 8192), pcmSquare(4000, 1000)...)
	pcm = append(pcm, pcmSquare(123, 30000)...)
	var bytewise [][]byte
	for i := range pcm {
		bytewise = append(bytewise, pcm[i:i+1])
	}
	if a, b := reduce(t, pcm), reduce(t, bytewise...); !bytes.Equal(a, b) || len(a) != 3 {
		t.Errorf("one write %v, byte-wise %v", a, b)
	}
}

func TestPeakReducerCap(t *testing.T) {
	r := newPeakReducer(2)
	_, err := r.Write(pcmSquare(3*peaksBinSample, 8192))
	if !errors.Is(err, errPeaksTooLong) || !r.over {
		t.Errorf("err = %v, over = %v", err, r.over)
	}
}

func TestPeaksCapBins(t *testing.T) {
	hour := int64(3600 * 1000)
	for _, tc := range []struct {
		name       string
		durationMs int64
		want       int
	}{
		{"unknown duration", 0, 1201},
		{"ten hours", 10 * hour, int((20*hour+600000)/PeaksBinMs) + 1},
		// A 52 hour omnibus edition must fit: a 48 hour ceiling refused these.
		{"52 hours", 52 * hour, int((104*hour+600000)/PeaksBinMs) + 1},
		{"past the ceiling", 400 * hour, int(peaksMaxMs/PeaksBinMs) + 1},
	} {
		got := peaksCapBins(tc.durationMs)
		if got != tc.want {
			t.Errorf("%s: %d bins, want %d", tc.name, got, tc.want)
		}
		if tc.durationMs > 0 && tc.durationMs <= peaksMaxMs && int64(got)*PeaksBinMs < tc.durationMs {
			t.Errorf("%s: cap %d bins is shorter than the audio", tc.name, got)
		}
	}
}

func TestPeaksEncodeDecode(t *testing.T) {
	p := Peaks{BinMs: PeaksBinMs, Values: []byte{0, 1, 170, 255}}
	enc := p.Encode()
	if len(enc) != 20 || string(enc[:4]) != "TSPK" || enc[4] != 1 || enc[12] != 60 {
		t.Fatalf("header = %v", enc[:16])
	}
	got, err := DecodePeaks(enc)
	if err != nil || got.BinMs != 500 || !bytes.Equal(got.Values, p.Values) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	mutate := func(f func(b []byte) []byte) []byte { return f(append([]byte(nil), enc...)) }
	for name, b := range map[string][]byte{
		"magic":      mutate(func(b []byte) []byte { b[0] = 'X'; return b }),
		"version":    mutate(func(b []byte) []byte { b[4] = 2; return b }),
		"short body": mutate(func(b []byte) []byte { return b[:len(b)-1] }),
		"long body":  mutate(func(b []byte) []byte { return append(b, 1) }),
		"count zero": Peaks{BinMs: 500}.Encode(),
		"no header":  enc[:8],
		"empty":      nil,
	} {
		if _, err := DecodePeaks(b); !errors.Is(err, ErrBadPeaks) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
