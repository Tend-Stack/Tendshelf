package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
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

func TestRunStartsWithoutAudioTools(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "no-such-ffprobe")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-data", dir, "-addr", "127.0.0.1:0", "-ffprobe", missing, "-ffmpeg", missing})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(db.FilePath(dir) + "-wal"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("database was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
}
