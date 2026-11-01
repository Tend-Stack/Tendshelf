package media_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

const common = "-nostdin -hide_banner -loglevel error -protocol_whitelist file,pipe -y"

// ffTools returns fake tools whose ffmpeg logs each run's arguments to the returned file.
func ffTools(t *testing.T, extraEnv ...string) (media.Tools, func() []string) {
	t.Helper()
	tools := mediatest.FakeTools(t)
	logPath := filepath.Join(t.TempDir(), "ffmpeg.log")
	tools.Env = append(tools.Env, mediatest.FakeFFmpegLogEnv+"="+logPath)
	tools.Env = append(tools.Env, extraEnv...)
	return tools, func() []string {
		b, err := os.ReadFile(logPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func fakeBook(t *testing.T, f mediatest.FakeFile) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "book.m4b")
	mediatest.WriteFake(t, p, f)
	return p
}

func TestExtractPictureFake(t *testing.T) {
	tools, runs := ffTools(t)
	ctx := context.Background()
	p := fakeBook(t, mediatest.FakeFile{Cover: true, CoverColor: "#8a4b2f"})

	data, ext, err := tools.ExtractPicture(ctx, p, media.Picture{Index: 1, Codec: "mjpeg"})
	if err != nil || ext != "jpg" {
		t.Fatalf("mjpeg: ext %q, err %v", ext, err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("not a 64 px jpeg: %v", err)
	}
	if want := common + " -i file:" + p + " -map 0:1 -frames:v 1 -c:v copy -f image2pipe pipe:1"; runs()[0] != want {
		t.Errorf("args =\n%s\nwant\n%s", runs()[0], want)
	}

	data, ext, err = tools.ExtractPicture(ctx, p, media.Picture{Index: 2, Codec: "gif"})
	if err != nil || ext != "png" {
		t.Fatalf("gif: ext %q, err %v", ext, err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("not a png: %v", err)
	}
	if want := common + " -i file:" + p + " -map 0:2 -frames:v 1 -c:v png -f image2pipe pipe:1"; runs()[1] != want {
		t.Errorf("args =\n%s\nwant\n%s", runs()[1], want)
	}

	_, ext, err = tools.ExtractPicture(ctx, p, media.Picture{Index: 1, Codec: "png"})
	if err != nil || ext != "png" {
		t.Fatalf("png: ext %q, err %v", ext, err)
	}
	if want := common + " -i file:" + p + " -map 0:1 -frames:v 1 -c:v copy -f image2pipe pipe:1"; runs()[2] != want {
		t.Errorf("args =\n%s\nwant\n%s", runs()[2], want)
	}

	if _, _, err := tools.ExtractPicture(ctx, fakeBook(t, mediatest.FakeFile{}), media.Picture{Index: 1, Codec: "mjpeg"}); code(t, err) != media.CodeFFmpegFailed {
		t.Errorf("no cover: err = %v", err)
	}
}

func TestImageToPNGAndThumbnailFake(t *testing.T) {
	tools, runs := ffTools(t)
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "cover.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 400, 200))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := tools.ImageToPNG(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if im, err := png.Decode(bytes.NewReader(data)); err != nil || im.Bounds().Dx() != 400 {
		t.Errorf("ImageToPNG: %v", err)
	}
	if want := common + " -i file:" + src + " -map 0:v:0 -frames:v 1 -c:v png -f image2pipe pipe:1"; runs()[0] != want {
		t.Errorf("args =\n%s\nwant\n%s", runs()[0], want)
	}

	dst := filepath.Join(dir, ".tmp-thumb")
	if err := tools.Thumbnail(ctx, src, dst, 160); err != nil {
		t.Fatal(err)
	}
	vf := "scale=w='min(160,iw)':h='min(160,ih)':force_original_aspect_ratio=decrease:flags=lanczos"
	want := common + " -i file:" + src + " -map 0:v:0 -frames:v 1 -vf " + vf + " -c:v mjpeg -q:v 3 -f image2 -update 1 file:" + dst
	if runs()[1] != want {
		t.Errorf("args =\n%s\nwant\n%s", runs()[1], want)
	}
	for box, wantW := range map[int]int{160: 160, 640: 400} { // never enlarged
		if err := tools.Thumbnail(ctx, src, dst, box); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(dst)
		if err != nil {
			t.Fatal(err)
		}
		im, err := jpeg.Decode(f)
		f.Close()
		if err != nil || im.Bounds().Dx() != wantW {
			t.Errorf("box %d: width %d, err %v; want %d", box, im.Bounds().Dx(), err, wantW)
		}
	}
	if err := tools.Thumbnail(ctx, src, "relative/out", 160); err == nil {
		t.Error("a relative output path should be refused")
	}
}

func TestFFmpegRefusals(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// A protected input never reaches exec, even if ffmpeg cannot be run at all.
	protected := filepath.Join(dir, "book.aax")
	mediatest.WriteProtected(t, protected)
	broken := media.Tools{FFmpeg: "/nonexistent/ffmpeg"}
	calls := map[string]func() error{
		"ExtractPicture": func() error {
			_, _, err := broken.ExtractPicture(ctx, protected, media.Picture{Index: 1, Codec: "mjpeg"})
			return err
		},
		"ImageToPNG": func() error { _, err := broken.ImageToPNG(ctx, protected); return err },
		"Thumbnail":  func() error { return broken.Thumbnail(ctx, protected, filepath.Join(dir, "o"), 160) },
		"Compat":     func() error { return broken.Compat(ctx, protected, filepath.Join(dir, "o"), "aac", 1000) },
	}
	for name, call := range calls {
		err := call()
		if code(t, err) != media.CodeDRM || !errors.Is(err, media.ErrProtected) {
			t.Errorf("%s on a protected file: %v", name, err)
		}
	}

	// No ffmpeg configured, or one that does not exist.
	plain := fakeBook(t, mediatest.FakeFile{Cover: true})
	for name, tools := range map[string]media.Tools{"unset": {}, "nonexistent": broken} {
		_, _, err := tools.ExtractPicture(ctx, plain, media.Picture{Index: 1, Codec: "mjpeg"})
		if code(t, err) != media.CodeFFmpegMissing || !errors.Is(err, media.ErrNotFound) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := (media.Tools{FFmpeg: "ffmpeg"}).ExtractPicture(ctx, "relative.m4b", media.Picture{}); err == nil {
		t.Error("a relative input path should be refused")
	}
}

func TestFFmpegTimeoutExitAndCap(t *testing.T) {
	ctx := context.Background()
	p := fakeBook(t, mediatest.FakeFile{Cover: true})
	pic := media.Picture{Index: 1, Codec: "mjpeg"}

	slow, _ := ffTools(t, mediatest.FakeFFmpegSleepEnv+"=5000")
	slow.FFmpegTimeout = 300 * time.Millisecond
	start := time.Now()
	_, _, err := slow.ExtractPicture(ctx, p, pic)
	if code(t, err) != media.CodeFFmpegTimeout || time.Since(start) > 4*time.Second {
		t.Errorf("timeout: err = %v after %s", err, time.Since(start))
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := slow.ExtractPicture(cctx, p, pic); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled parent: err = %v", err)
	}

	failing, _ := ffTools(t, mediatest.FakeFFmpegExitEnv+"=3")
	_, _, err = failing.ExtractPicture(ctx, p, pic)
	var me *media.Error
	if code(t, err) != media.CodeFFmpegFailed || !errors.As(err, &me) || !strings.Contains(me.Detail, "failing on request") {
		t.Errorf("exit 3: err = %v", err)
	}
	if strings.Contains(me.Text, "failing") {
		t.Errorf("stderr leaked into the plain text: %q", me.Text)
	}

	bad := fakeBook(t, mediatest.FakeFile{Cover: true, FFmpegExit: 2})
	tools, _ := ffTools(t)
	if _, _, err := tools.ExtractPicture(ctx, bad, pic); code(t, err) != media.CodeFFmpegFailed {
		t.Errorf("FakeFile.FFmpegExit: err = %v", err)
	}

	flood, _ := ffTools(t, mediatest.FakeFFmpegFloodEnv+"=22000000")
	_, _, err = flood.ExtractPicture(ctx, p, pic)
	if code(t, err) != media.CodeFFmpegFailed || !strings.Contains(err.Error(), "more than") {
		t.Errorf("cap: err = %v", err)
	}
}

func TestFakeFFmpegRejectsUnsafeArguments(t *testing.T) {
	// The fake refuses a command without -protocol_whitelist file,pipe or without file: inputs, so a
	// regression in the runner fails every test that converts something.
	p := fakeBook(t, mediatest.FakeFile{Cover: true})
	for name, args := range map[string][]string{
		"no whitelist": {"-nostdin", "-i", "file:" + p, "-f", "image2pipe", "pipe:1"},
		"bare path":    {"-nostdin", "-protocol_whitelist", "file,pipe", "-i", p, "-f", "image2pipe", "pipe:1"},
		"wide open":    {"-nostdin", "-protocol_whitelist", "file,pipe,http", "-i", "file:" + p, "-f", "image2pipe", "pipe:1"},
	} {
		cmd := exec.Command(os.Args[0], args...)
		cmd.Env = []string{"TENDSHELF_FAKE_FFPROBE=1"}
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 64 || !strings.Contains(string(out), "unexpected arguments") {
			t.Errorf("%s: err %v, output %q", name, err, out)
		}
	}
}
