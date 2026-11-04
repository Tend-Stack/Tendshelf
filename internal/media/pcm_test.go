package media_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func TestPCM16kArgs(t *testing.T) {
	ctx := context.Background()
	tools, runs := ffTools(t, mediatest.FakeFFmpegPCMEnv+"=100:8192")
	p := fakeBook(t, mediatest.FakeFile{DurationMs: 60000})
	var buf bytes.Buffer
	if err := tools.PCM16k(ctx, p, 29000, 32000, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Error("no samples")
	}
	want := "-ss 29.000 -i file:" + p + " -t 32.000 -map 0:a:0 -vn -sn -dn -ac 1 -ar 16000 -c:a pcm_s16le -f s16le pipe:1"
	if log := runs(); len(log) != 1 || log[0] != common+" "+want {
		t.Errorf("argv = %q", log)
	}

	// Output past durMs*32 + 1 MiB is refused and never reaches the writer.
	tools, _ = ffTools(t, mediatest.FakeFFmpegPCMEnv+"=620000:100")
	buf.Reset()
	err := tools.PCM16k(ctx, p, 0, 1000, &buf)
	if code(t, err) != media.CodeFFmpegFailed || !strings.Contains(err.Error(), "more audio") {
		t.Errorf("over the cap: %v", err)
	}
	if buf.Len() > 1000*32+1<<20 {
		t.Errorf("wrote %d bytes past the cap", buf.Len())
	}

	// The deadline applies.
	tools, _ = ffTools(t, mediatest.FakeFFmpegSleepEnv+"=3000")
	tools.FFmpegTimeout = 100 * time.Millisecond
	if err := tools.PCM16k(ctx, p, 0, 1000, &buf); code(t, err) != media.CodeFFmpegTimeout {
		t.Errorf("timeout: %v", err)
	}
}

func TestPCM16kProtectedNeverRuns(t *testing.T) {
	tools, runs := ffTools(t)
	aax := filepath.Join(t.TempDir(), "book.aax")
	mediatest.WriteProtected(t, aax)
	err := tools.PCM16k(context.Background(), aax, 0, 1000, &bytes.Buffer{})
	if code(t, err) != media.CodeDRM || len(runs()) != 0 {
		t.Errorf("err = %v, runs = %q", err, runs())
	}
}
