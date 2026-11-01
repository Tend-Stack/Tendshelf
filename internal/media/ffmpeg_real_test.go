package media_test

import (
	"bytes"
	"context"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestRealPictures runs the exact picture commands on real ffmpeg: `make test-media`.
func TestRealPictures(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()

	book := filepath.Join(dir, "book.m4b")
	mediatest.GenM4B(t, tools, book, mediatest.Synth{Title: "Covered", Cover: true})
	p, err := tools.Probe(ctx, book)
	if err != nil || len(p.Pictures) != 1 {
		t.Fatalf("probe: %+v, %v", p.Pictures, err)
	}
	data, ext, err := tools.ExtractPicture(ctx, book, p.Pictures[0])
	if err != nil || ext != "jpg" {
		t.Fatalf("ExtractPicture: ext %q, %v", ext, err)
	}
	if im, err := jpeg.Decode(bytes.NewReader(data)); err != nil || im.Bounds().Dx() != 64 {
		t.Errorf("extracted picture is not the 64 px jpeg: %v", err)
	}

	// The extracted bytes equal the picture that was embedded (stream copy).
	src := filepath.Join(dir, "src.jpg")
	mediatest.GenImage(t, tools, src, "#8a4b2f", 64, 64)
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("embedded cover bytes differ: %d bytes, want %d", len(data), len(want))
	}

	// WebP goes to PNG; ffmpeg's libwebp encoder makes the sample.
	webp := filepath.Join(dir, "cover.webp")
	mediatest.GenImage(t, tools, webp, "#2c6660", 120, 80)
	pngData, err := tools.ImageToPNG(ctx, webp)
	if err != nil {
		t.Fatal(err)
	}
	if im, err := png.Decode(bytes.NewReader(pngData)); err != nil || im.Bounds().Dx() != 120 || im.Bounds().Dy() != 80 {
		t.Errorf("webp to png: %v", err)
	}

	// Thumbnails fit the box and never enlarge.
	big := filepath.Join(dir, "big.png")
	mediatest.GenImage(t, tools, big, "#2c6660", 800, 400)
	for box, wantSize := range map[int][2]int{160: {160, 80}, 320: {320, 160}, 640: {640, 320}, 1000: {800, 400}} {
		dst := filepath.Join(dir, ".tmp-thumb.jpg")
		if err := tools.Thumbnail(ctx, big, dst, box); err != nil {
			t.Fatalf("box %d: %v", box, err)
		}
		f, err := os.Open(dst)
		if err != nil {
			t.Fatal(err)
		}
		im, err := jpeg.Decode(f)
		f.Close()
		if err != nil || im.Bounds().Dx() != wantSize[0] || im.Bounds().Dy() != wantSize[1] {
			t.Errorf("box %d: got %v, %v; want %v", box, im.Bounds(), err, wantSize)
		}
	}
}

// TestRealCompat makes compatibility copies from FLAC (encode) and an M4B (stream copy).
func TestRealCompat(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()
	flac := filepath.Join(dir, "a.flac")
	m4b := filepath.Join(dir, "b.m4b")
	mediatest.GenFLAC(t, tools, flac, mediatest.Synth{Title: "F"})
	mediatest.GenM4B(t, tools, m4b, mediatest.Synth{Title: "M", Cover: true,
		Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 3000, "One"), mediatest.Chapter(3000, 6000, "Two")}})
	for _, src := range []string{flac, m4b} {
		in, err := tools.Probe(ctx, src)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, ".tmp-"+filepath.Base(src)+".m4a")
		if err := tools.Compat(ctx, src, dst, in.Audio[0].Codec, in.DurationMs); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		out, err := tools.Probe(ctx, dst)
		if err != nil {
			t.Fatalf("%s: probe of the copy: %v", src, err)
		}
		d := out.DurationMs - in.DurationMs
		if out.Container != "mp4" || out.Audio[0].Codec != "aac" || d < -1000 || d > 1000 || len(out.Chapters) != 0 || len(out.Pictures) != 0 {
			t.Errorf("%s: copy = %s/%s %d ms (source %d ms), %d chapters, %d pictures", src, out.Container, out.Audio[0].Codec, out.DurationMs, in.DurationMs, len(out.Chapters), len(out.Pictures))
		}
	}
}
