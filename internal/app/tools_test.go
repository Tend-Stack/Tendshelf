package app

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

func TestLocateToolsWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	missing := filepath.Join(t.TempDir(), "no-such-ffprobe")
	tools := locateTools(media.Config{FFprobe: missing}, log)
	if tools.FFprobe != "" {
		t.Fatalf("FFprobe = %q, want empty", tools.FFprobe)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "audio tools not found; files will be listed but not read") || !strings.Contains(out, "ffprobe") {
		t.Fatalf("log = %q", out)
	}

	// A tool that is there is returned and nothing is logged for it.
	buf.Reset()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tools = locateTools(media.Config{FFprobe: exe, FFmpeg: exe}, log)
	if tools.FFprobe != exe || tools.FFmpeg != exe || buf.Len() != 0 {
		t.Fatalf("tools = %+v, log = %q", tools, buf.String())
	}
}
