package mediajobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func sha(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func jpegSize(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return cfg.Width, cfg.Height
}

// oneCover returns the book's only cover row and checks it is the book's cover.
func (e *env) oneCover(rel string) library.Cover {
	e.t.Helper()
	b := e.book(rel)
	cs := e.covers1(b)
	if len(cs) != 1 {
		e.t.Fatalf("%s: %d cover rows, want 1", rel, len(cs))
	}
	if b.CoverID != cs[0].ID {
		e.t.Errorf("%s: cover_id = %q, want %q", rel, b.CoverID, cs[0].ID)
	}
	return cs[0]
}

func TestCoverPreference(t *testing.T) {
	e := newEnv(t)
	brown := mediatest.FakeFile{Cover: true, CoverColor: "#8a4b2f"}
	e.fake("A/01.m4b", brown)
	e.image("A/cover.PNG", "#2c6660", 300, 200)
	e.fake("B/01.m4b", brown)
	e.image("B/folder.jpg", "#2c6660", 120, 120)
	e.fake("C/01.m4b", brown)
	e.image("C/folder.jpg", "#2c6660", 120, 120)
	e.image("C/cover.jpg", "#aa3333", 80, 90)
	e.fake("D/01.m4b", brown)
	e.fake("E/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	c := e.covers()

	if out := e.run(c, "A"); out != OutcomeSet {
		t.Fatalf("A: %s", out)
	}
	a := e.oneCover("A")
	if a.Source != library.CoverFolder || a.Mime != "image/png" || a.Width != 300 || a.Height != 200 {
		t.Errorf("A (cover.PNG, case-insensitive) = %+v", a)
	}
	if want := sha(t, e.path("A/cover.PNG")); a.SHA256 != want || a.Path != "covers/"+want+".png" {
		t.Errorf("A stored as %s, want the file's own bytes %s", a.Path, want)
	}
	if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(a.Tint) || !strings.Contains(string(a.Accent), `"dark"`) {
		t.Errorf("A tint %q accent %s", a.Tint, a.Accent)
	}
	if got := listDir(t, CoversDir(e.data)); len(got) != 1 {
		t.Errorf("covers dir = %v", got)
	}
	// Thumbnails: three sizes, fitted in the box, never enlarged (300x200 source).
	for box, want := range map[int][2]int{160: {160, 106}, 320: {300, 200}, 640: {300, 200}} {
		if w, h := jpegSize(t, ThumbPath(e.data, a.SHA256, box)); w != want[0] || h != want[1] {
			t.Errorf("thumb %d = %dx%d, want %v", box, w, h, want)
		}
	}
	if got := listDir(t, ThumbsDir(e.data)); len(got) != 3 {
		t.Errorf("thumbs dir = %v", got)
	}

	e.run(c, "B")
	if b := e.oneCover("B"); b.Source != library.CoverFolder || b.Mime != "image/jpeg" || b.Width != 120 {
		t.Errorf("B (folder.jpg beats embedded) = %+v", b)
	}
	e.run(c, "C")
	if cc := e.oneCover("C"); cc.Width != 80 {
		t.Errorf("C: cover.* should beat folder.*: %+v", cc)
	}
	e.run(c, "D")
	d := e.oneCover("D")
	if d.Source != library.CoverEmbedded || d.Mime != "image/jpeg" || d.Width != 64 {
		t.Errorf("D (embedded) = %+v", d)
	}
	if out := e.run(c, "E"); out != OutcomeNone {
		t.Errorf("E: %s", out)
	}
	if cs := e.covers1(e.book("E")); len(cs) != 0 || e.book("E").CoverID != "" {
		t.Errorf("E got covers %v", cs)
	}
}

func TestCoverFallbacks(t *testing.T) {
	e := newEnv(t)
	brown := mediatest.FakeFile{Cover: true, CoverColor: "#8a4b2f"}

	e.fake("Damaged/01.m4b", brown)
	if err := os.WriteFile(e.path("Damaged/cover.jpg"), []byte("not a picture at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.fake("Link/01.m4b", brown)
	outside := e.image("../outside.jpg", "#2c6660", 90, 90)
	if err := os.Symlink(outside, e.path("Link/cover.jpg")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	e.fake("Huge/01.m4b", brown)
	e.image("Huge/cover.jpg", "#2c6660", 10001, 1)
	e.fake("Webp/01.m4b", brown)
	if err := os.WriteFile(e.path("Webp/cover.webp"), []byte("RIFFxxxxWEBPjunk"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.fake("solo.m4b", brown) // a Single book: images beside it are not its own
	e.image("cover.jpg", "#2c6660", 77, 77)
	e.scanAll()
	c := e.covers()

	for _, rel := range []string{"Damaged", "Link", "Huge", "Webp", "solo.m4b"} {
		e.run(c, rel)
		cv := e.oneCover(rel)
		if cv.Source != library.CoverEmbedded || cv.Width != 64 {
			t.Errorf("%s: want the embedded picture, got %+v", rel, cv)
		}
	}
}

func TestCoverFFmpegMissingIsAnError(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{Cover: true})
	e.scanAll()
	tools := e.tools
	tools.FFmpeg = ""
	c := NewCovers(e.repo, tools, e.data, e.enq, nil)
	_, err := c.Run(bg, e.book("A").ID, nil)
	var me *media.Error
	if !errors.As(err, &me) || me.Code != media.CodeFFmpegMissing {
		t.Fatalf("err = %v", err)
	}
	if je := jobError(err); je == nil {
		t.Error("jobError dropped the error")
	}
}

func TestCoverNeverTouchesUnplayableFiles(t *testing.T) {
	e := newEnv(t)
	e.fake("Enc/01.m4a", mediatest.FakeFile{Encrypted: true, Cover: true})
	if err := os.MkdirAll(e.path("Prot"), 0o755); err != nil {
		t.Fatal(err)
	}
	mediatest.WriteProtected(t, e.path("Prot/01.aax"))
	e.scanAll()
	tools := e.tools
	tools.FFmpeg = "/nonexistent/ffmpeg" // any exec fails loudly
	c := NewCovers(e.repo, tools, e.data, e.enq, nil)
	for _, rel := range []string{"Enc", "Prot"} {
		if out := e.run(c, rel); out != OutcomeNone {
			t.Errorf("%s: %s, want none", rel, out)
		}
	}
	if e.ffmpegRuns() != 0 {
		t.Error("ffmpeg ran")
	}
	if got := listDir(t, CoversDir(e.data)); len(got) != 0 {
		t.Errorf("files written: %v", got)
	}
}

func fileTimes(t *testing.T, paths ...string) []int64 {
	t.Helper()
	var out []int64
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fi.ModTime().UnixNano())
	}
	return out
}

func TestCoverSecondRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.fake("Folder/01.m4b", mediatest.FakeFile{})
	e.image("Folder/cover.jpg", "#2c6660", 400, 400)
	e.fake("Embedded/01.m4b", mediatest.FakeFile{Cover: true})
	e.scanAll()
	c := e.covers()

	for _, rel := range []string{"Folder", "Embedded"} {
		e.run(c, rel)
		cv := e.oneCover(rel)
		paths := []string{filepath.Join(e.data, filepath.FromSlash(cv.Path))}
		for _, box := range ThumbSizes {
			paths = append(paths, ThumbPath(e.data, cv.SHA256, box))
		}
		times := fileTimes(t, paths...)
		changes, runs := e.changes(), e.ffmpegRuns()

		if out := e.run(c, rel); out != OutcomeUnchanged {
			t.Errorf("%s: second run = %s", rel, out)
		}
		if got := e.changes(); got != changes {
			t.Errorf("%s: second run changed %d rows", rel, got-changes)
		}
		// A folder picture needs no ffmpeg at all; an embedded one is extracted again to be compared.
		wantRuns := runs
		if rel == "Embedded" {
			wantRuns++
		}
		if got := e.ffmpegRuns(); got != wantRuns {
			t.Errorf("%s: ffmpeg ran %d times in the second run, want %d", rel, got-runs, wantRuns-runs)
		}
		for i, tm := range fileTimes(t, paths...) {
			if tm != times[i] {
				t.Errorf("%s: %s was rewritten", rel, paths[i])
			}
		}
	}
}

func TestCoverUploadIsKept(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.image("A/cover.jpg", "#2c6660", 100, 100)
	e.scanAll()
	c := e.covers()
	b := e.book("A")

	// The owner uploaded a picture first: it is the book's cover.
	up := strings.Repeat("e", 64)
	if _, err := e.st.Writer().Exec(`INSERT INTO covers(id, book_id, source, mime, width, height, sha256, path, created_at)
  VALUES ('cv_up', ?, 'upload', 'image/png', 10, 10, ?, 'covers/up.png', 1)`, b.ID, up); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`UPDATE books SET cover_id = 'cv_up' WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	e.run(c, "A")
	if got := e.book("A").CoverID; got != "cv_up" {
		t.Errorf("cover_id = %q, want the upload kept", got)
	}
	if cs := e.covers1(b); len(cs) != 2 {
		t.Errorf("covers = %v, want the upload and the folder row", cs)
	}

	// Replacing the folder picture drops the old folder row and files, never the upload.
	old := e.covers1(b)[1]
	e.image("A/cover.jpg", "#aa3333", 200, 100)
	if out := e.run(c, "A"); out != OutcomeSet {
		t.Errorf("replacement: %s", out)
	}
	cs := e.covers1(b)
	if len(cs) != 2 || e.book("A").CoverID != "cv_up" {
		t.Fatalf("after replacement: %v, cover_id %q", cs, e.book("A").CoverID)
	}
	for _, cv := range cs {
		if cv.SHA256 == old.SHA256 {
			t.Error("old folder row survived")
		}
	}
	if exists(filepath.Join(e.data, filepath.FromSlash(old.Path))) || exists(ThumbPath(e.data, old.SHA256, 160)) {
		t.Error("old files survived")
	}
}

func TestCoverReplacedAndShared(t *testing.T) {
	e := newEnv(t)
	for _, b := range []string{"A", "B"} {
		e.fake(b+"/01.m4b", mediatest.FakeFile{})
		e.image(b+"/cover.jpg", "#2c6660", 100, 100)
	}
	e.scanAll()
	c := e.covers()
	e.run(c, "A")
	e.run(c, "B")
	a, b := e.oneCover("A"), e.oneCover("B")
	if a.SHA256 != b.SHA256 || a.ID == b.ID {
		t.Fatalf("A %+v B %+v: want one picture, two rows", a, b)
	}
	if n := len(listDir(t, CoversDir(e.data))); n != 1 {
		t.Fatalf("shared picture stored %d times", n)
	}

	// A changes its picture: the shared file stays for B.
	e.image("A/cover.jpg", "#aa3333", 150, 100)
	e.run(c, "A")
	a2 := e.oneCover("A")
	if a2.SHA256 == a.SHA256 || a2.ID == a.ID {
		t.Fatalf("A did not change: %+v", a2)
	}
	if !exists(filepath.Join(e.data, filepath.FromSlash(b.Path))) || !exists(ThumbPath(e.data, b.SHA256, 640)) {
		t.Error("the shared file went while B still uses it")
	}
	if n := len(listDir(t, CoversDir(e.data))); n != 2 {
		t.Errorf("covers dir has %d files, want 2", n)
	}

	// B changes too: now the old picture has no user and goes with its thumbnails.
	e.image("B/cover.jpg", "#33aa33", 90, 90)
	e.run(c, "B")
	if exists(filepath.Join(e.data, filepath.FromSlash(a.Path))) || exists(ThumbPath(e.data, a.SHA256, 160)) {
		t.Error("an unused picture was kept")
	}
	if n, m := len(listDir(t, CoversDir(e.data))), len(listDir(t, ThumbsDir(e.data))); n != 2 || m != 6 {
		t.Errorf("covers %d, thumbs %d; want 2 and 6", n, m)
	}
}

func TestCoverGoneMissingAndLostLease(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.image("A/cover.jpg", "#2c6660", 100, 100)
	e.scanAll()
	c := e.covers()
	if out, err := c.Run(bg, "bk_nope", nil); err != nil || out != OutcomeGone {
		t.Errorf("gone: %q, %v", out, err)
	}

	lost := errors.New("lease lost")
	_, err := c.Run(bg, e.book("A").ID, func(context.Context, *sql.Tx) error { return lost })
	if !errors.Is(err, lost) {
		t.Fatalf("guard error = %v", err)
	}
	if cs := e.covers1(e.book("A")); len(cs) != 0 {
		t.Errorf("a run without its lease wrote %v", cs)
	}
	if n := len(listDir(t, CoversDir(e.data))) + len(listDir(t, ThumbsDir(e.data))); n != 0 {
		t.Errorf("a run without its lease left %d files", n)
	}

	if err := e.repo.Update(bg, func(tx *library.Tx) error { return tx.SetBookMissing(e.book("A").ID, 5) }); err != nil {
		t.Fatal(err)
	}
	if out := e.run(c, "A"); out != OutcomeMissing {
		t.Errorf("missing: %s", out)
	}
}

func TestSweepTempAndAtomicWrites(t *testing.T) {
	e := newEnv(t)
	dir := CoversDir(e.data)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old, fresh := filepath.Join(dir, ".tmp-old"), filepath.Join(dir, ".tmp-fresh")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := e.covers().now().Add(-2 * tmpMaxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	e.covers() // registration sweeps
	if exists(old) || !exists(fresh) {
		t.Errorf("sweep: old %v, fresh %v", exists(old), exists(fresh))
	}

	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.image("A/cover.jpg", "#2c6660", 100, 100)
	e.scanAll()
	e.run(e.covers(), "A")
	for _, d := range []string{ThumbsDir(e.data)} {
		for _, n := range listDir(t, d) {
			if strings.HasPrefix(n, tmpPrefix) {
				t.Errorf("temporary file left in %s: %s", d, n)
			}
		}
	}
}
