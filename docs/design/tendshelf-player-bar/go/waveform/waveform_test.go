package waveform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalize(t *testing.T) {
	got := Normalize([]float64{0, 0.25, 0.5, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2})
	if got[0] != 0 || got[len(got)-1] != 255 || got[2] != 180 {
		t.Fatalf("unexpected %v", got)
	}
	if len(Normalize(nil)) != 0 {
		t.Fatal("empty input should give empty output")
	}
}

func TestSlice(t *testing.T) {
	p := make([]byte, 100) // 20 s at Rate 5
	if n := len(Slice(p, 2, 4)); n != 10 {
		t.Fatalf("want 10 values, got %d", n)
	}
	if Slice(p, 30, 40) != nil {
		t.Fatal("out of range should be nil")
	}
}

func TestPeaksWithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	f := filepath.Join(t.TempDir(), "tone.m4a")
	// 2 s of tone then 2 s of silence.
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=220:duration=2",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono:d=2", "-filter_complex", "[0][1]concat=n=2:v=0:a=1", f)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v %s", err, out)
	}
	defer os.Remove(f)
	p, err := Peaks(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) < 19 || len(p) > 21 {
		t.Fatalf("want about 20 values, got %d", len(p))
	}
	if p[2] < 200 || p[len(p)-2] > 10 {
		t.Fatalf("tone should be loud and silence quiet: %v", p)
	}
}
