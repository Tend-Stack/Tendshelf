package mediajobs

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestRealCovers runs the cover job over files made by real ffmpeg: `make test-mediajobs-real`.
func TestRealCovers(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	e := newEnvWith(t, tools, "")
	mk := func(rel string) string {
		p := e.path(rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mediatest.GenM4B(t, tools, mk("Embedded/book.m4b"), mediatest.Synth{Title: "Embedded", Cover: true})
	mediatest.GenM4B(t, tools, mk("Webp/book.m4b"), mediatest.Synth{Title: "Webp", Cover: true})
	mediatest.GenImage(t, tools, mk("Webp/cover.webp"), "#2c6660", 800, 400)
	e.scanAll()
	c := e.covers()

	// The embedded picture is stored with its own bytes.
	if out := e.run(c, "Embedded"); out != OutcomeSet {
		t.Fatalf("Embedded: %s", out)
	}
	emb := e.oneCover("Embedded")
	want := mk("expected.jpg")
	mediatest.GenImage(t, tools, want, "#8a4b2f", 64, 64)
	wantBytes, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(e.data, filepath.FromSlash(emb.Path)))
	if err != nil || !bytes.Equal(got, wantBytes) {
		t.Errorf("stored embedded cover differs from the embedded picture (%d vs %d bytes, %v)", len(got), len(wantBytes), err)
	}
	if emb.Source != library.CoverEmbedded || emb.Mime != "image/jpeg" || emb.Tint == "" || len(emb.Accent) == 0 {
		t.Errorf("embedded row = %+v", emb)
	}
	for _, box := range ThumbSizes {
		if w, h := jpegSize(t, ThumbPath(e.data, emb.SHA256, box)); w != 64 || h != 64 { // never enlarged
			t.Errorf("thumb %d = %dx%d", box, w, h)
		}
	}

	// A folder WebP is converted to PNG by ffmpeg and stored as PNG; thumbnails fit the box.
	if out := e.run(c, "Webp"); out != OutcomeSet {
		t.Fatalf("Webp: %s", out)
	}
	wp := e.oneCover("Webp")
	if wp.Source != library.CoverFolder || wp.Mime != "image/png" || wp.Width != 800 || wp.Height != 400 {
		t.Errorf("webp row = %+v", wp)
	}
	data, err := os.ReadFile(filepath.Join(e.data, filepath.FromSlash(wp.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("stored webp cover is not a PNG: %v", err)
	}
	for box, want := range map[int][2]int{160: {160, 80}, 320: {320, 160}, 640: {640, 320}} {
		if w, h := jpegSize(t, ThumbPath(e.data, wp.SHA256, box)); w != want[0] || h != want[1] {
			t.Errorf("webp thumb %d = %dx%d, want %v", box, w, h, want)
		}
	}
	if out := e.run(c, "Webp"); out != OutcomeUnchanged {
		t.Errorf("second run: %s", out)
	}
}

// TestRealCompat makes a compatibility copy of an Opus file through the service, and converts a
// FLAC (encoded) and an M4B (stream copy) through the media runner, which the policy exempts.
func TestRealCompat(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	e := newEnvWith(t, tools, "")
	for _, d := range []string{"Opus", "Flac", "M4b"} {
		if err := os.MkdirAll(e.path(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mediatest.GenOpus(t, tools, e.path("Opus/a.opus"), mediatest.Synth{Title: "O"})
	mediatest.GenFLAC(t, tools, e.path("Flac/a.flac"), mediatest.Synth{Title: "F"})
	mediatest.GenM4B(t, tools, e.path("M4b/b.m4b"), mediatest.Synth{Title: "M", Cover: true})
	e.scanAll()
	c := e.compat(1 << 30)

	f := e.file("Opus")
	if out, err := c.Run(bg, f.ID, nil); err != nil || out != OutcomeBuilt {
		t.Fatalf("Run(opus) = %q, %v", out, err)
	}
	if p, ok, err := c.Lookup(bg, f.ID); err != nil || !ok {
		t.Errorf("Lookup = %q, %v, %v", p, ok, err)
	}
	for _, rel := range []string{"Flac", "M4b"} {
		f := e.file(rel)
		dst := filepath.Join(t.TempDir(), "copy.m4a")
		if err := tools.Compat(bg, f.Path, dst, f.Codec, f.DurationMs); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if err := c.verify(bg, dst, f.DurationMs); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
}
