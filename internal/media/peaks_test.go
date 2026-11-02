package media_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func TestPeaksFake(t *testing.T) {
	ctx := context.Background()
	tools, runs := ffTools(t, mediatest.FakeFFmpegPCMEnv+"=2000:8192,1000:0,1000:3277")
	p := fakeBook(t, mediatest.FakeFile{DurationMs: 4000})

	got, err := tools.Peaks(ctx, p, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if got.BinMs != 500 || len(got.Values) != 8 {
		t.Fatalf("peaks = %+v", got)
	}
	// 8192 is -12 dB (204), silence is 0, 3277 is -20 dB (170).
	for i, want := range []int{204, 204, 204, 204, 0, 0, 170, 170} {
		if d := int(got.Values[i]) - want; d < -1 || d > 1 {
			t.Errorf("bin %d = %d, want %d", i, got.Values[i], want)
		}
	}
	want := "-filter_threads 1 -threads 1 -i file:" + p + " -map 0:a:0 -vn -sn -dn -ac 1 -ar 8000 -c:a pcm_s16le -f s16le pipe:1"
	if log := runs(); len(log) != 1 || log[0] != common+" "+want {
		t.Errorf("argv = %q", log)
	}
}

func TestPeaksFailures(t *testing.T) {
	ctx := context.Background()
	p := fakeBook(t, mediatest.FakeFile{})

	// No samples at all: ffmpeg exits 0 with nothing on stdout.
	tools, _ := ffTools(t, mediatest.FakeFFmpegPCMEnv+"=0:0")
	if _, err := tools.Peaks(ctx, p, 1000); code(t, err) != media.CodeFFmpegFailed {
		t.Errorf("zero samples: %v", err)
	}
	// More audio than twice the duration plus ten minutes.
	tools, _ = ffTools(t, mediatest.FakeFFmpegPCMEnv+"=620000:100")
	if _, err := tools.Peaks(ctx, p, 1000); code(t, err) != media.CodeFFmpegFailed || !strings.Contains(err.Error(), "longer than expected") {
		t.Errorf("over the cap: %v", err)
	}
	// A failing exit.
	tools, _ = ffTools(t, mediatest.FakeFFmpegExitEnv+"=1")
	if _, err := tools.Peaks(ctx, p, 1000); code(t, err) != media.CodeFFmpegFailed {
		t.Errorf("exit 1: %v", err)
	}
	// A deadline.
	tools, _ = ffTools(t, mediatest.FakeFFmpegSleepEnv+"=3000")
	tools.FFmpegTimeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := tools.Peaks(ctx, p, 1000); code(t, err) != media.CodeFFmpegTimeout || time.Since(start) > 2*time.Second {
		t.Errorf("timeout: %v after %s", err, time.Since(start))
	}
}

func TestPeaksProtectedNeverRuns(t *testing.T) {
	tools, runs := ffTools(t)
	aax := filepath.Join(t.TempDir(), "book.aax")
	mediatest.WriteProtected(t, aax)
	_, err := tools.Peaks(context.Background(), aax, 1000)
	if code(t, err) != media.CodeDRM || !errors.Is(err, media.ErrProtected) {
		t.Errorf("err = %v", err)
	}
	if len(runs()) != 0 {
		t.Errorf("ffmpeg ran on a protected file: %q", runs())
	}
}
