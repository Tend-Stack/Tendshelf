package mediajobs

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func sample(t *testing.T, ext string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "cover_sample."+ext))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// solidPNG is a PNG of w by h whose colour depends on n, so each n is a different picture.
func solidPNG(t *testing.T, n, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{uint8(30 + 20*n), uint8(200 - 15*n), uint8(60 + 10*n), 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// webpTools wraps the fake ffmpeg: it answers a WebP-to-PNG conversion with pngBytes (the fake cannot
// decode WebP) and passes everything else to the fake.
func webpTools(t *testing.T, base media.Tools, pngBytes []byte) media.Tools {
	t.Helper()
	dir := t.TempDir()
	pngFile := filepath.Join(dir, "out.png")
	if err := os.WriteFile(pngFile, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in file:*.webp) cat %q; exit 0;; esac; done\nexec %q \"$@\"\n", pngFile, base.FFmpeg)
	p := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	base.FFmpeg = p
	return base
}

func filesUnder(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func adoptEnv(t *testing.T) (*env, *Covers, library.Book) {
	t.Helper()
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	return e, e.covers(), e.book("A")
}

func TestCoverAdoptUpload(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	e.tools = webpTools(t, e.tools, solidPNG(t, 9, 40, 40))
	c := e.covers()
	b := e.book("A")

	for _, ext := range []string{"jpg", "png", "webp"} {
		id, err := c.Adopt(bg, b.ID, library.CoverUpload, sample(t, ext), nil)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if got := e.book("A").CoverID; got != id {
			t.Errorf("%s: cover_id = %q, want %q", ext, got, id)
		}
		cv, err := e.repo.Cover(bg, id)
		if err != nil || cv.Source != library.CoverUpload {
			t.Fatalf("%s: row %+v, %v", ext, cv, err)
		}
		for _, box := range ThumbSizes {
			if !exists(ThumbPath(e.data, cv.SHA256, box)) {
				t.Errorf("%s: no %d px thumbnail", ext, box)
			}
		}
		if ext == "webp" && cv.Mime != "image/png" {
			t.Errorf("webp stored as %s, want the converted PNG", cv.Mime)
		}
	}
	if cs := e.covers1(b); len(cs) != 3 {
		t.Errorf("covers = %d, want 3", len(cs))
	}
}

func TestCoverAdoptStage(t *testing.T) {
	_, c, _ := adoptEnv(t)
	name, err := c.Stage(sample(t, "png"))
	if err != nil || !stagedName.MatchString(name) {
		t.Fatalf("Stage = %q, %v", name, err)
	}
	if !exists(filepath.Join(IncomingDir(c.data), name)) {
		t.Fatal("staged file missing")
	}
	// A stale staged file goes at the next start; a fresh one stays.
	old := filepath.Join(IncomingDir(c.data), "0000000000000000.img")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * stagedMaxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	NewCovers(c.repo, c.tools, c.data, c.jobs, nil)
	if exists(old) || !exists(filepath.Join(IncomingDir(c.data), name)) {
		t.Error("sweep removed the wrong files")
	}
}

func TestCoverAdoptRefuses(t *testing.T) {
	e, c, b := adoptEnv(t)
	huge := append([]byte{0xff, 0xd8, 0xff}, make([]byte, maxCoverBytes-2)...) // 5 MiB + 1
	var wide bytes.Buffer
	if err := png.Encode(&wide, image.NewGray(image.Rect(0, 0, maxDimension+1, 1))); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data []byte
		code string
	}{
		{"html", []byte("<html><body>cover.jpg</body></html>"), CodeNotPicture},
		{"too large", huge, CodeTooLarge},
		{"dimension", wide.Bytes(), media.CodeImageTooLarge},
		{"truncated jpeg", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 1}, CodeNotPicture},
	}
	for _, tc := range cases {
		_, err := c.Adopt(bg, b.ID, library.CoverUpload, tc.data, nil)
		var me *media.Error
		if !errors.As(err, &me) || me.Code != tc.code || me.Text == "" {
			t.Errorf("%s: err = %v, want code %s", tc.name, err, tc.code)
		}
	}
	if cs := e.covers1(b); len(cs) != 0 {
		t.Errorf("rows left: %v", cs)
	}
	if left := filesUnder(e.data); len(left) != 0 {
		t.Errorf("files left: %v", left)
	}
	// Staging checks the first bytes and the size only; the job checks the rest.
	for _, tc := range cases[:2] {
		if _, err := c.Stage(tc.data); err == nil {
			t.Errorf("%s: Stage accepted it", tc.name)
		}
	}
	if left := filesUnder(IncomingDir(e.data)); len(left) != 0 {
		t.Errorf("staged files left: %v", left)
	}
}

func TestCoverAdoptScanKeeps(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.image("A/cover.jpg", "#2c6660", 100, 100)
	e.scanAll()
	c := e.covers()
	b := e.book("A")
	e.run(c, "A")
	id, err := c.Adopt(bg, b.ID, library.CoverUpload, solidPNG(t, 1, 80, 80), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.run(c, "A")
	if got := e.book("A").CoverID; got != id {
		t.Errorf("cover_id = %q after a scan run, want the upload %q", got, id)
	}
	if cv, err := e.repo.Cover(bg, id); err != nil || cv.Source != library.CoverUpload {
		t.Errorf("upload row: %+v, %v", cv, err)
	}
}

func TestCoverAdoptPrune(t *testing.T) {
	e, c, b := adoptEnv(t)
	var first library.Cover
	var lastID string
	for n := 1; n <= 7; n++ {
		id, err := c.Adopt(bg, b.ID, library.CoverUpload, solidPNG(t, n, 50+n, 50), nil)
		if err != nil {
			t.Fatal(err)
		}
		lastID = id
		if n == 1 {
			first, _ = e.repo.Cover(bg, id)
		}
	}
	cs := e.covers1(b)
	if len(cs) != 6 || e.book("A").CoverID != lastID {
		t.Fatalf("covers = %d, current %q, want 6 and %q", len(cs), e.book("A").CoverID, lastID)
	}
	for _, cv := range cs {
		if cv.ID == first.ID {
			t.Error("the oldest cover survived")
		}
	}
	if exists(filepath.Join(e.data, filepath.FromSlash(first.Path))) || exists(ThumbPath(e.data, first.SHA256, 160)) {
		t.Error("the pruned cover's files survived")
	}
	if !strings.HasSuffix(cs[5].Path, ".png") || !exists(filepath.Join(e.data, filepath.FromSlash(cs[5].Path))) {
		t.Error("the current cover's file is missing")
	}
}

func TestCoverChoose(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.image("A/cover.jpg", "#2c6660", 100, 100)
	e.fake("B/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	c := e.covers()
	a, other := e.book("A"), e.book("B")
	e.run(c, "A")
	folder := e.oneCover("A")
	up, err := c.Adopt(bg, a.ID, library.CoverUpload, solidPNG(t, 2, 60, 60), nil)
	if err != nil {
		t.Fatal(err)
	}
	otherUp, err := c.Adopt(bg, other.ID, library.CoverUpload, solidPNG(t, 3, 60, 60), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Choose(bg, a.ID, otherUp); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("another book's cover: %v", err)
	}
	if err := c.Choose(bg, a.ID, "cv_nothing"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown cover: %v", err)
	}
	if got := e.book("A").CoverID; got != up {
		t.Errorf("a refused choice changed the cover to %q", got)
	}
	if err := c.Choose(bg, a.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.book("A").CoverID; got != folder.ID {
		t.Errorf("cover_id = %q, want the folder cover", got)
	}
	choices, err := c.Choices(bg, a.ID)
	if err != nil || len(choices) != 2 {
		t.Errorf("Choices = %v, %v", choices, err)
	}
}
