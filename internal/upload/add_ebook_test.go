package upload

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// eadmEPUB is a small valid EPUB with the given title and author.
func eadmEPUB(t *testing.T, title, author string) []byte {
	t.Helper()
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: title, Authors: []string{author},
		Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"It was a quiet morning in the harbour town."}}}})
}

// eadmIngest wires an ebook service and a running job runner to the env's database and returns a scanner
// that hands ebooks to it, the way the server does.
func eadmIngest(t *testing.T, e *env) *scan.Scanner {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner, err := jobs.New(e.st, jobs.Options{Logger: log, PollEvery: 20 * time.Millisecond, ProgressEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	svc := ebook.New(e.st, e.repo, runner, e.data, log)
	if err := svc.Register(runner); err != nil {
		t.Fatal(err)
	}
	if err := runner.Start(bg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = runner.Stop(ctx)
	})
	sc := scan.New(e.repo, mediatest.FakeTools(t), &scanEnq{}, log)
	sc.OnEbooks(svc.ScanSink())
	sc.OnScanFinished(svc.Finisher())
	return sc
}

// eadmScanLast runs the scoped scan the last placement queued, then waits for the text book to appear.
func eadmScanLast(t *testing.T, e *env, sc *scan.Scanner) {
	t.Helper()
	var p scan.ScanPayload
	q := e.enq.of(scan.KindScan)
	if err := json.Unmarshal(q[len(q)-1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.ScanSubtree(bg, p.LibraryID, p.FolderID, p.Subdir, nil); err != nil {
		t.Fatal(err)
	}
}

func eadmWaitBooks(t *testing.T, e *env, want int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if booksCount(t, e) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var dump string
	rows, _ := e.st.Reader().Query(`SELECT path, state, COALESCE(refused_reason,''), COALESCE(error_code,''), COALESCE(book_id,'') FROM ebook_files`)
	for rows.Next() {
		var a, b, c, d, f string
		_ = rows.Scan(&a, &b, &c, &d, &f)
		dump += a + "|" + b + "|" + c + "|" + d + "|" + f + "\n"
	}
	rows.Close()
	t.Fatalf("books = %d, want %d; files:\n%s", booksCount(t, e), want, dump)
}

func TestAddEbookManifest(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	for _, c := range []struct {
		name string
		m    Manifest
		code string // "" = accepted
		file string
	}{
		{"ebook only", files("Tale.epub"), "", ""},
		{"pdf alone", files("paper.pdf"), "", ""},
		{"mixed", files("a.mp3", "a.epub"), "", ""},
		{"several ebooks", files("one.epub", "two.mobi"), "", ""},
		{"kfx", files("Tale.kfx"), "copy_protected", "Tale.kfx"},
		{"kfx-zip among others", files("a.epub", "b.kfx-zip"), "copy_protected", "b.kfx-zip"},
		{"no book file", files("cover.jpg", "notes.txt"), "no_audio", ""},
		{"text file is not an ebook", files("notes.txt", "essay.md"), "no_audio", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Create(bg, me, e.lib.ID, c.m)
			if c.code == "" {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				return
			}
			ue := wantErr(t, err, 422, c.code)
			if c.file != "" && ue.Field != c.file {
				t.Errorf("field = %q, want %q", ue.Field, c.file)
			}
			if c.code == "no_audio" && ue.Message != "There is no audio or ebook file in it." {
				t.Errorf("message = %q", ue.Message)
			}
		})
	}
}

func TestAddEbookPlace(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	sc := eadmIngest(t, e)
	data := eadmEPUB(t, "The Harbour Town", "Zed Writer")
	v := e.send("whatever it was called", book{"download (3).epub": data})
	res := e.mustPlace(v.ID)
	if res.State != StatePlaced {
		t.Fatalf("state = %s", res.State)
	}
	// The name came from the file's own details, not the file name or the label.
	got := tree(t, e.root)
	want := []string{"Zed Writer/", "Zed Writer/The Harbour Town/", "Zed Writer/The Harbour Town/download (3).epub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	placed, err := os.ReadFile(filepath.Join(e.root, "Zed Writer", "The Harbour Town", "download (3).epub"))
	if err != nil || string(placed) != string(data) {
		t.Fatalf("placed file differs from the upload: %v", err)
	}
	if len(e.enq.of(scan.KindScan)) != 1 {
		t.Fatalf("scans queued = %d", len(e.enq.of(scan.KindScan)))
	}
	eadmScanLast(t, e, sc)
	eadmWaitBooks(t, e, 1)
	var kind, title string
	if err := e.st.Reader().QueryRow(`SELECT kind, title FROM books`).Scan(&kind, &title); err != nil {
		t.Fatal(err)
	}
	if kind != "text" || title != "The Harbour Town" {
		t.Errorf("book = %s %q", kind, title)
	}
	if strings.Contains(strings.Join(tree(t, e.root), " "), tempPrefix) {
		t.Error("the build folder was left behind")
	}
}

func TestAddEbookRefusals(t *testing.T) {
	adept := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Locked", Rights: true,
		Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"text"}}}})
	for _, c := range []struct {
		name, file, code string
		data             []byte
	}{
		{"copy-protected", "Locked.epub", "drm", adept},
		{"damaged", "Broken.epub", CodeUnreadable, []byte("this is not a zip file at all, only some bytes")},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			v := e.send("Book", book{"Fine.epub": eadmEPUB(t, "Fine", "Ann Author"), c.file: c.data})
			res := e.mustPlace(v.ID)
			if res.State != StateRefused {
				t.Fatalf("state = %s", res.State)
			}
			if got := tree(t, e.root); len(got) != 0 {
				t.Errorf("something reached the library folder: %v", got)
			}
			if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
				t.Errorf("staged bytes kept: %v", err)
			}
			u := e.upload(v.ID)
			if u.RefusedFile != c.file || u.ErrorCode != c.code {
				t.Errorf("upload = %q %q, want %q %q", u.RefusedFile, u.ErrorCode, c.file, c.code)
			}
			if len(e.enq.of(scan.KindScan)) != 0 {
				t.Error("a refused upload queued an import")
			}
		})
	}
}

func TestAddEbookRemove(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	sc := eadmIngest(t, e)
	v := e.send("x", book{"a.epub": eadmEPUB(t, "Gone Book", "Ann Author")})
	e.mustPlace(v.ID)
	eadmScanLast(t, e, sc)
	eadmWaitBooks(t, e, 1)
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if !e.svc.Removable(bg, id) {
		t.Fatal("an uploaded, untouched ebook is not removable")
	}
	res, err := e.svc.Remove(bg, me, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || len(res.Kept) != 0 {
		t.Errorf("result = %+v", res)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("folders it made are still there: %v", got)
	}
	if n := booksCount(t, e); n != 0 {
		t.Errorf("books = %d", n)
	}
	if n := countRows(t, e, "ebook_files"); n != 0 {
		t.Errorf("ebook_files = %d: the row would bring the book back", n)
	}
	// A later scan and catch-up must not revive it.
	eadmScanLast(t, e, sc)
	time.Sleep(200 * time.Millisecond)
	if n := booksCount(t, e); n != 0 {
		t.Errorf("books after a scan = %d", n)
	}
}

func TestAddEbookRemoveKeepsChanged(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	sc := eadmIngest(t, e)
	v := e.send("x", book{"a.epub": eadmEPUB(t, "Marked Book", "Ann Author")})
	e.mustPlace(v.ID)
	eadmScanLast(t, e, sc)
	eadmWaitBooks(t, e, 1)
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.root, "Ann Author", "Marked Book", "a.epub")
	if err := os.WriteFile(file, []byte("my own copy, different"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Remove(bg, me, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Kept, []string{file}) || len(res.Deleted) != 0 {
		t.Errorf("result = %+v", res)
	}
	if b, _ := os.ReadFile(file); string(b) != "my own copy, different" {
		t.Error("their file changed")
	}
	if n := booksCount(t, e); n != 1 {
		t.Errorf("the book went although a file is still there: %d", n)
	}
}
