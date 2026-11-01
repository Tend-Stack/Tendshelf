package media_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func TestMain(m *testing.M) {
	mediatest.RunFakeIfRequested()
	os.Exit(m.Run())
}

func code(t *testing.T, err error) string {
	t.Helper()
	var me *media.Error
	if !errors.As(err, &me) {
		t.Fatalf("err = %v (%T), want *media.Error", err, err)
	}
	if me.Text == "" || strings.HasSuffix(me.Text, ":") {
		t.Fatalf("code %s has no plain text: %q", me.Code, me.Text)
	}
	return me.Code
}

func TestProbeFake(t *testing.T) {
	tools := mediatest.FakeTools(t)
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name string, f mediatest.FakeFile) string {
		p := filepath.Join(dir, name)
		mediatest.WriteFake(t, p, f)
		return p
	}

	t.Run("m4b_with_chapters_and_cover", func(t *testing.T) {
		p, err := tools.Probe(ctx, write("a.m4b", mediatest.FakeFile{
			Cover: true, DurationMs: 6037, Tags: map[string]string{"Title": "Book", "ARTIST": "Ana"},
			Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 2000, "One"), mediatest.Chapter(2000, 6037, "Two")},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if p.Container != "mp4" || p.DurationMs != 6037 || p.Tags["title"] != "Book" || p.Tags["artist"] != "Ana" ||
			len(p.Audio) != 1 || len(p.Pictures) != 1 || len(p.Chapters) != 2 || p.Chapters[1].Title != "Two" || p.Encrypted {
			t.Fatalf("probe = %+v", p)
		}
		b, _ := json.Marshal(p)
		if strings.Contains(string(b), dir) {
			t.Fatalf("probe JSON carries the file path: %s", b)
		}
	})

	t.Run("mp3_track_tags", func(t *testing.T) {
		p, err := tools.Probe(ctx, write("01 - Part.mp3", mediatest.FakeFile{
			Container: "mp3", Codec: "mp3", CodecTag: "[0][0][0][0]", DurationMs: 2000,
			Tags: map[string]string{"track": "1/3", "title": "Part 1"},
		}))
		if err != nil || p.Container != "mp3" || p.Tags["track"] != "1/3" || p.Audio[0].Codec != "mp3" {
			t.Fatalf("probe = %+v, %v", p, err)
		}
	})

	t.Run("encrypted_packets", func(t *testing.T) {
		p, err := tools.Probe(ctx, write("enc.m4a", mediatest.FakeFile{Encrypted: true, Cover: true}))
		if code(t, err) != media.CodeDRM || !errors.Is(err, media.ErrProtected) {
			t.Fatalf("err = %v", err)
		}
		if !p.Encrypted || p.Protection != media.ProtectionPacketSide {
			t.Fatalf("probe = %+v", p)
		}
	})

	t.Run("protected_extension_never_execs", func(t *testing.T) {
		for _, name := range []string{"x.aax", "x.AAXC", "x.Aa", "x.m4p"} {
			for _, ff := range []string{"/nonexistent/ffprobe", ""} {
				// The file does not even exist: the extension alone refuses it.
				p, err := (media.Tools{FFprobe: ff}).Probe(ctx, filepath.Join(dir, "missing", name))
				if code(t, err) != media.CodeDRM || !errors.Is(err, media.ErrProtected) || !p.Encrypted || p.Protection != media.ProtectionExtension {
					t.Errorf("%s ffprobe=%q: probe=%+v err=%v", name, ff, p, err)
				}
			}
		}
		prot := filepath.Join(dir, "book.aax")
		mediatest.WriteProtected(t, prot)
		if _, err := tools.Probe(ctx, prot); !errors.Is(err, media.ErrProtected) {
			t.Errorf("fake-backed protected file err = %v", err)
		}
	})

	t.Run("missing_file", func(t *testing.T) {
		_, err := tools.Probe(ctx, filepath.Join(dir, "gone.m4b"))
		if code(t, err) != media.CodeFileMissing {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("directory_not_regular", func(t *testing.T) {
		_, err := tools.Probe(ctx, dir)
		if code(t, err) != media.CodeNotAudio || !errors.Is(err, media.ErrNotRegular) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("fifo_never_opened", func(t *testing.T) {
		p := filepath.Join(dir, "pipe.mp3")
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Skip("no fifo support:", err)
		}
		done := make(chan error, 1)
		go func() { _, err := tools.Probe(ctx, p); done <- err }()
		select {
		case err := <-done:
			if code(t, err) != media.CodeNotAudio || !errors.Is(err, media.ErrNotRegular) {
				t.Fatalf("err = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Probe blocked on a named pipe")
		}
	})

	t.Run("junk_not_audio", func(t *testing.T) {
		p := filepath.Join(dir, "junk.mp3")
		if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.Probe(ctx, p); code(t, err) != media.CodeNotAudio {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("ffprobe_missing", func(t *testing.T) {
		p := write("ok.mp3", mediatest.FakeFile{})
		for _, ff := range []string{"", "/nonexistent/ffprobe"} {
			_, err := (media.Tools{FFprobe: ff}).Probe(ctx, p)
			if code(t, err) != media.CodeFFprobeMissing || !errors.Is(err, media.ErrNotFound) {
				t.Errorf("ffprobe=%q err = %v", ff, err)
			}
		}
	})

	t.Run("timeout", func(t *testing.T) {
		slow := tools
		slow.ProbeTimeout = 200 * time.Millisecond
		start := time.Now()
		_, err := slow.Probe(ctx, write("slow.m4b", mediatest.FakeFile{SleepMs: 10000}))
		if code(t, err) != media.CodeProbeTimeout || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("took %s", d)
		}
	})

	t.Run("parent_cancel", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(150*time.Millisecond, cancel)
		start := time.Now()
		_, err := tools.Probe(cctx, write("slow2.m4b", mediatest.FakeFile{SleepMs: 10000}))
		var me *media.Error
		if !errors.Is(err, context.Canceled) || errors.As(err, &me) {
			t.Fatalf("err = %v, want the bare context cause", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("took %s", d)
		}
	})

	t.Run("flood", func(t *testing.T) {
		_, err := tools.Probe(ctx, write("flood.m4b", mediatest.FakeFile{FloodBytes: 5 << 20}))
		if code(t, err) != media.CodeProbeOutputTooBig {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("nonzero_exit", func(t *testing.T) {
		_, err := tools.Probe(ctx, write("exit.m4b", mediatest.FakeFile{Exit: 3, Stderr: "boom: broken moov atom"}))
		var me *media.Error
		if code(t, err) != media.CodeProbeFailed || !errors.As(err, &me) || !strings.Contains(me.Detail, "broken moov atom") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(me.Text, "boom") {
			t.Fatalf("stderr leaked into the plain text: %q", me.Text)
		}
	})

	t.Run("stderr_capped", func(t *testing.T) {
		_, err := tools.Probe(ctx, write("noisy.m4b", mediatest.FakeFile{Exit: 2, Stderr: strings.Repeat("e", 200<<10)}))
		var me *media.Error
		if code(t, err) != media.CodeProbeFailed || !errors.As(err, &me) || len(me.Detail) != 64<<10 {
			t.Fatalf("err = %v, detail length %d", err, len(me.Detail))
		}
	})

	t.Run("ffprobe_error_codes", func(t *testing.T) {
		for _, c := range []struct {
			raw  string
			exit int
			want string
		}{
			{`{"error":{"code":-2,"string":"No such file or directory"}}`, 1, media.CodeFileMissing},
			{`{"error":{"code":-13,"string":"Permission denied"}}`, 1, media.CodeFileUnreadable},
			{`{"error":{"code":-1094995529,"string":"Invalid data"}}`, 1, media.CodeNotAudio},
			{`{"error":{"code":-22,"string":"Invalid argument"}}`, 1, media.CodeNotAudio}, // ffprobe 6.1
			{`{"error":{"code":-5,"string":"I/O error"}}`, 1, media.CodeProbeFailed},
			{`{"error":{"code":-13,"string":"Permission denied"}}`, 0, media.CodeFileUnreadable},
		} {
			_, err := tools.Probe(ctx, write("err.m4b", mediatest.FakeFile{Raw: json.RawMessage(c.raw), Exit: c.exit}))
			if code(t, err) != c.want {
				t.Errorf("%s exit %d: err = %v, want %s", c.raw, c.exit, err, c.want)
			}
		}
	})

	t.Run("garbage_output_and_no_audio", func(t *testing.T) {
		_, err := tools.Probe(ctx, write("garbage.m4b", mediatest.FakeFile{Raw: json.RawMessage(`"just a string"`)}))
		if code(t, err) != media.CodeProbeFailed {
			t.Errorf("garbage: err = %v", err)
		}
		_, err = tools.Probe(ctx, write("video.mp4", mediatest.FakeFile{
			Raw: json.RawMessage(`{"streams":[{"index":0,"codec_type":"video","codec_name":"h264"}],"format":{"format_name":"mp4"}}`)}))
		if code(t, err) != media.CodeNotAudio {
			t.Errorf("no audio: err = %v", err)
		}
	})

	t.Run("relative_path", func(t *testing.T) {
		_, err := tools.Probe(ctx, "relative/a.mp3")
		var me *media.Error
		if err == nil || errors.As(err, &me) {
			t.Fatalf("err = %v, want a plain error", err)
		}
	})
}

// The fake refuses argument lists without the runner's safety flags, so it guards the real runner.
func TestFakeRejectsUnsafeArguments(t *testing.T) {
	tools := mediatest.FakeTools(t)
	p := filepath.Join(t.TempDir(), "a.mp3")
	mediatest.WriteFake(t, p, mediatest.FakeFile{})
	for name, args := range map[string][]string{
		"no whitelist":   {"-i", "file:" + p},
		"bare path":      {"-protocol_whitelist", "file,pipe", "-i", p},
		"wide whitelist": {"-protocol_whitelist", "file,pipe,http", "-i", "file:" + p},
		"input not last": {"-protocol_whitelist", "file,pipe", "-i", "file:" + p, "-show_format"},
	} {
		cmd := exec.Command(tools.FFprobe, args...)
		cmd.Env = tools.Env
		err := cmd.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 64 {
			t.Errorf("%s: err = %v, want exit 64", name, err)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]media.Kind{
		"a.m4b": media.KindAudio, "/x/B.MP3": media.KindAudio, "c.flac": media.KindAudio, "d.opus": media.KindAudio,
		"e.webm": media.KindAudio, "f.aax": media.KindProtected, "g.AAXC": media.KindProtected,
		"h.aa": media.KindProtected, "i.m4p": media.KindProtected, "cover.jpg": media.KindOther,
		"noext": media.KindOther, "x.mp3.txt": media.KindOther,
	}
	for name, want := range cases {
		if got := media.Classify(name); got != want {
			t.Errorf("Classify(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestLocate(t *testing.T) {
	exe := func(dir, name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	notFound := func(t *testing.T, err error, tool string) {
		t.Helper()
		var nf *media.NotFoundError
		if !errors.Is(err, media.ErrNotFound) || !errors.As(err, &nf) {
			t.Fatalf("err = %v, want NotFoundError", err)
		}
		_ = tool
	}

	t.Run("empty PATH", func(t *testing.T) {
		t.Setenv("PATH", "")
		tools, err := media.Locate(media.Config{})
		notFound(t, err, "ffprobe")
		if tools.FFprobe != "" || tools.FFmpeg != "" || !strings.Contains(err.Error(), "ffprobe") || !strings.Contains(err.Error(), "ffmpeg") {
			t.Fatalf("tools = %+v, err = %v", tools, err)
		}
	})

	t.Run("explicit absolute", func(t *testing.T) {
		dir := t.TempDir()
		p, q := exe(dir, "myprobe", 0o755), exe(dir, "mympeg", 0o755)
		tools, err := media.Locate(media.Config{FFprobe: p, FFmpeg: q})
		if err != nil || tools.FFprobe != p || tools.FFmpeg != q {
			t.Fatalf("tools = %+v, err = %v", tools, err)
		}
	})

	t.Run("name looked up on PATH", func(t *testing.T) {
		dir := t.TempDir()
		p := exe(dir, "myprobe", 0o755)
		t.Setenv("PATH", dir)
		tools, err := media.Locate(media.Config{FFprobe: "myprobe"})
		if tools.FFprobe != p {
			t.Fatalf("tools = %+v", tools)
		}
		notFound(t, err, "ffmpeg") // the other tool is still missing
	})

	t.Run("found tool is kept when the other is missing", func(t *testing.T) {
		dir := t.TempDir()
		p := exe(dir, "myprobe", 0o755)
		t.Setenv("PATH", "")
		tools, err := media.Locate(media.Config{FFprobe: p})
		if tools.FFprobe != p || tools.FFmpeg != "" {
			t.Fatalf("tools = %+v", tools)
		}
		notFound(t, err, "ffmpeg")
	})

	t.Run("relative path rejected", func(t *testing.T) {
		dir := t.TempDir()
		exe(dir, "myprobe", 0o755)
		t.Chdir(dir)
		t.Setenv("PATH", "")
		tools, err := media.Locate(media.Config{FFprobe: "./myprobe"})
		notFound(t, err, "ffprobe")
		if tools.FFprobe != "" {
			t.Fatalf("tools = %+v", tools)
		}
	})

	t.Run("not executable or not a file", func(t *testing.T) {
		dir := t.TempDir()
		p := exe(dir, "plain", 0o644)
		t.Setenv("PATH", "")
		for _, v := range []string{p, dir, filepath.Join(dir, "nope")} {
			tools, err := media.Locate(media.Config{FFprobe: v})
			notFound(t, err, "ffprobe")
			if tools.FFprobe != "" {
				t.Errorf("%s accepted", v)
			}
		}
	})

	t.Run("working directory on PATH does not count", func(t *testing.T) {
		dir := t.TempDir()
		exe(dir, "ffprobe", 0o755)
		t.Chdir(dir)
		t.Setenv("PATH", ".")
		tools, err := media.Locate(media.Config{})
		notFound(t, err, "ffprobe")
		if tools.FFprobe != "" {
			t.Fatalf("tools = %+v", tools)
		}
	})
}
