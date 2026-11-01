package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

const refusedSentence = "This file is copy-protected, so Tendshelf will not open it."

var mp3 = mediatest.FakeFile{Container: "mp3", Codec: "mp3", CodecTag: "", BitRate: 64000}

// buildFixture writes a 200-book library of fake files plus every kind of noise, and returns what
// a scan must make of it.
func buildFixture(t *testing.T, root string) *fixture {
	t.Helper()
	fx := newFixture(t, root)
	one := func(rootRel, refused string, rels ...string) { fx.book(rootRel, refused, rels...) }

	for i := 0; i < 20; i++ { // 20 singles at the library root
		rel := fmt.Sprintf("S%02d.m4b", i)
		fx.file(rel, mediatest.FakeFile{})
		one(rel, "", rel)
	}
	for i := 0; i < 30; i++ { // Author/Book
		rel := fmt.Sprintf("Au%02d/Book%02d/b.m4b", i, i)
		fx.file(rel, mediatest.FakeFile{})
		one(filepath.ToSlash(filepath.Dir(rel)), "", rel)
	}
	for i := 0; i < 30; i++ { // Author/Series/Book N
		rel := fmt.Sprintf("Auth%02d/Series/Book %d/x.m4b", i, i+1)
		fx.file(rel, mediatest.FakeFile{})
		one(filepath.ToSlash(filepath.Dir(rel)), "", rel)
	}
	for i := 0; i < 30; i++ { // numbered mp3 folders, 3 to 12 parts
		n := 3 + i%10
		var rels []string
		for p := 1; p <= n; p++ {
			rel := fmt.Sprintf("Num/N%02d/%02d.mp3", i, p)
			f := mp3
			fx.file(rel, f)
			rels = append(rels, rel)
		}
		one(fmt.Sprintf("Num/N%02d", i), "", rels...)
	}
	for i := 0; i < 20; i++ { // multi-disc
		var rels []string
		for d := 1; d <= 2+i%2; d++ {
			for p := 1; p <= 2; p++ {
				rel := fmt.Sprintf("Disc/D%02d/CD%d/%02d.mp3", i, d, p)
				fx.file(rel, mp3)
				rels = append(rels, rel)
			}
		}
		one(fmt.Sprintf("Disc/D%02d", i), "", rels...)
	}
	for i := 0; i < 10; i++ { // containers: 2 loose files and 1 sub folder
		dir := fmt.Sprintf("Cont%02d", i)
		for _, n := range []string{"Loose A.m4b", "Loose B.m4b"} {
			rel := dir + "/" + n
			fx.file(rel, mediatest.FakeFile{})
			one(rel, "", rel)
		}
		rel := dir + "/Sub/x.m4b"
		fx.file(rel, mediatest.FakeFile{})
		one(dir+"/Sub", "", rel)
	}
	for i := 0; i < 10; i++ { // two distinct m4b in one folder
		for _, n := range []string{"Alpha.m4b", "Beta.m4b"} {
			rel := fmt.Sprintf("Two%02d/%s", i, n)
			fx.file(rel, mediatest.FakeFile{})
			one(rel, "", rel)
		}
	}
	for i := 0; i < 5; i++ { // split-part m4b
		r1, r2 := fmt.Sprintf("Split%02d/Book - Part 1.m4b", i), fmt.Sprintf("Split%02d/Book - Part 2.m4b", i)
		fx.file(r2, mediatest.FakeFile{})
		fx.file(r1, mediatest.FakeFile{})
		one(fmt.Sprintf("Split%02d", i), "", r1, r2)
	}
	for i := 0; i < 3; i++ { // protected by extension
		rel := fmt.Sprintf("Prot/P%d/x.aax", i)
		fx.file(rel, mediatest.FakeFile{})
		one(filepath.ToSlash(filepath.Dir(rel)), library.RefusedDRM, rel)
	}
	for i := 0; i < 2; i++ { // protected by the stream
		rel := fmt.Sprintf("Prot/E%d/x.m4b", i)
		fx.file(rel, mediatest.FakeFile{Encrypted: true})
		one(filepath.ToSlash(filepath.Dir(rel)), library.RefusedDRM, rel)
	}
	for i := 0; i < 10; i++ { // flac, opus and mixed
		dir := fmt.Sprintf("Mix/M%02d", i)
		var rels []string
		for _, f := range []struct {
			name string
			ff   mediatest.FakeFile
		}{
			{"a.flac", mediatest.FakeFile{Container: "flac", Codec: "flac"}},
			{"b.opus", mediatest.FakeFile{Container: "ogg", Codec: "opus"}},
			{"c.mp3", mp3},
		}[:1+i%3] {
			rel := dir + "/" + f.name
			fx.file(rel, f.ff)
			rels = append(rels, rel)
		}
		one(dir, "", rels...)
	}

	// Noise: none of it may become a book.
	outside := t.TempDir()
	for _, p := range []string{".hidden/x.m4b", "@eaDir/x.mp3", "#recycle/x.mp3", "Au00/Book00/._b.m4b", "Au00/.DS_Store"} {
		fx.file(p, mediatest.FakeFile{})
	}
	for _, p := range []string{"cover.jpg", "notes.txt", "Au00/Book00/cover.jpg"} {
		if err := os.WriteFile(fx.abs(p), []byte("not audio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mediatest.WriteFake(t, filepath.Join(outside, "o.mp3"), mediatest.FakeFile{})
	if err := os.MkdirAll(filepath.Join(outside, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	mediatest.WriteFake(t, filepath.Join(outside, "dir", "p.mp3"), mediatest.FakeFile{})
	if err := os.Symlink(filepath.Join(outside, "dir"), fx.abs("LinkDir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "o.mp3"), fx.abs("Au00/Book00/l.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fx.abs("Au00/Book00/f.mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestScanFixture200(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := buildFixture(t, e.root)
	if len(fx.books) != 200 {
		t.Fatalf("fixture has %d books, want 200", len(fx.books))
	}

	// First scan: every book exists, files are queued for probing, protected files are not.
	res := e.scan()
	if res.BooksAdded != 200 || res.SymlinksSkipped != 2 || res.FoldersSkipped != 0 {
		t.Fatalf("first scan = %+v", res)
	}
	protected := 3
	if res.ProbesEnqueued != res.FilesAdded-protected || e.enq.count() != res.ProbesEnqueued {
		t.Fatalf("probes enqueued = %d (recorded %d), files added = %d", res.ProbesEnqueued, e.enq.count(), res.FilesAdded)
	}
	if n := e.probeAll(); n != res.ProbesEnqueued {
		t.Fatalf("probed %d files, want %d", n, res.ProbesEnqueued)
	}
	fx.verify(e)
	refused, err := e.repo.RefusedBooks(bg, e.lib.ID)
	if err != nil || len(refused) != 5 {
		t.Fatalf("refused books = %d, %v", len(refused), err)
	}
	for _, b := range refused {
		if RefusalText(b.RefusedReason) != refusedSentence {
			t.Errorf("%s: refusal text = %q", b.RootPath, RefusalText(b.RefusedReason))
		}
	}

	t.Run("noop", func(t *testing.T) {
		before, queued := e.changes(), e.enq.count()
		res := e.scan()
		if after := e.changes(); after != before {
			t.Fatalf("a scan with nothing changed made %d row changes", after-before)
		}
		if e.enq.count() != queued || res.ProbesEnqueued != 0 {
			t.Fatalf("a scan with nothing changed queued %d probes", e.enq.count()-queued)
		}
		want := Result{Folders: 1, FilesSeen: res.FilesSeen, BooksRefused: 5, SymlinksSkipped: 2}
		if res != want {
			t.Fatalf("result = %+v, want %+v", res, want)
		}
	})

	t.Run("rename a book folder", func(t *testing.T) {
		old, renamed := fx.abs("Num/N05"), fx.abs("Num/N05 Renamed")
		b := e.bookAt(old)
		ids := fileIDs(t, e, b.ID)
		if err := os.Rename(old, renamed); err != nil {
			t.Fatal(err)
		}
		res := e.scan()
		if res.BooksMoved != 1 || res.BooksAdded != 0 || res.FilesMoved != len(ids) || res.ProbesEnqueued != 0 {
			t.Fatalf("result = %+v", res)
		}
		nb := e.bookAt(renamed)
		if nb.ID != b.ID || nb.Title != "N05 Renamed" || nb.MissingSince != 0 {
			t.Fatalf("book = %+v, want id %s", nb, b.ID)
		}
		if got := fileIDs(t, e, nb.ID); fmt.Sprint(got) != fmt.Sprint(ids) {
			t.Fatalf("file IDs changed: %v -> %v", ids, got)
		}
		fx.relocate("Num/N05", "Num/N05 Renamed")
		fx.verify(e)
	})

	t.Run("move a file to another book", func(t *testing.T) {
		src, dst := fx.abs("Num/N09/03.mp3"), fx.abs("Num/N08/99.mp3")
		to := e.bookAt(fx.abs("Num/N08"))
		id := e.fileAt(src).ID
		if err := os.Rename(src, dst); err != nil {
			t.Fatal(err)
		}
		res := e.scan()
		if res.FilesMoved != 1 || res.FilesAdded != 0 || res.BooksAdded != 0 || res.ProbesEnqueued != 0 {
			t.Fatalf("result = %+v", res)
		}
		if f := e.fileAt(dst); f.ID != id || f.BookID != to.ID {
			t.Fatalf("moved file = %+v, want id %s in book %s", f, id, to.ID)
		}
		fx.moveFile("Num/N09", "Num/N08", "Num/N09/03.mp3", "Num/N08/99.mp3")
		fx.verify(e)
	})

	t.Run("rename a single file", func(t *testing.T) {
		old, renamed := fx.abs("S03.m4b"), fx.abs("S03 renamed.m4b")
		b, fid := e.bookAt(old), e.fileAt(old).ID
		if err := os.Rename(old, renamed); err != nil {
			t.Fatal(err)
		}
		res := e.scan()
		if res.BooksMoved != 1 || res.BooksAdded != 0 || res.FilesMoved != 1 {
			t.Fatalf("result = %+v", res)
		}
		nb := e.bookAt(renamed)
		if nb.ID != b.ID || nb.Title != "S03 renamed" || e.fileAt(renamed).ID != fid {
			t.Fatalf("book = %+v", nb)
		}
		fx.relocate("S03.m4b", "S03 renamed.m4b")
		fx.verify(e)
	})

	t.Run("delete a file, then restore it", func(t *testing.T) {
		path := fx.abs("Num/N10/02.mp3")
		saved, mtime := readKeepMtime(t, path)
		id := e.fileAt(path).ID
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		res := e.scan()
		if res.FilesMissing != 1 || res.BooksMissing != 0 {
			t.Fatalf("result = %+v", res)
		}
		if f := e.fileAt(path); f.ID != id || f.MissingSince == 0 {
			t.Fatalf("row = %+v", f)
		}
		gone := fx.dropFile("Num/N10", "Num/N10/02.mp3")
		fx.verify(e) // re-finalised without it

		writeKeepMtime(t, path, saved, mtime)
		res = e.scan()
		if res.FilesRestored != 1 || res.FilesChanged != 0 || res.ProbesEnqueued != 0 {
			t.Fatalf("restore result = %+v", res)
		}
		if f := e.fileAt(path); f.ID != id || f.MissingSince != 0 {
			t.Fatalf("restored row = %+v", f)
		}
		fx.restoreFile("Num/N10", "Num/N10/02.mp3", gone)
		fx.verify(e)
	})

	t.Run("delete all of a book's files, then restore them", func(t *testing.T) {
		rels := []string{"Num/N11/01.mp3", "Num/N11/02.mp3", "Num/N11/03.mp3", "Num/N11/04.mp3"}
		type kept struct {
			data  []byte
			mtime time.Time
		}
		saved := map[string]kept{}
		for _, r := range rels {
			d, m := readKeepMtime(t, fx.abs(r))
			saved[r] = kept{d, m}
			if err := os.Remove(fx.abs(r)); err != nil {
				t.Fatal(err)
			}
		}
		// Some other files of the book stay, so remove the rest too.
		rest, _ := filepath.Glob(filepath.Join(fx.abs("Num/N11"), "*.mp3"))
		for _, p := range rest {
			r, _ := filepath.Rel(fx.root, p)
			d, m := readKeepMtime(t, p)
			saved[filepath.ToSlash(r)] = kept{d, m}
			os.Remove(p)
		}
		res := e.scan()
		if res.BooksMissing != 1 || res.FilesMissing != len(saved) {
			t.Fatalf("result = %+v (files %d)", res, len(saved))
		}
		b := e.bookAt(fx.abs("Num/N11"))
		if b.MissingSince == 0 {
			t.Fatal("book not marked missing")
		}
		if got := e.fileAt(fx.abs(rels[0])); got.MissingSince == 0 {
			t.Fatal("file row not marked missing")
		}

		for r, k := range saved {
			writeKeepMtime(t, fx.abs(r), k.data, k.mtime)
		}
		res = e.scan()
		if res.BooksRestored != 1 || res.FilesRestored != len(saved) || res.ProbesEnqueued != 0 {
			t.Fatalf("restore result = %+v", res)
		}
		if b := e.bookAt(fx.abs("Num/N11")); b.MissingSince != 0 {
			t.Fatal("book still missing")
		}
		fx.verify(e)
	})

	t.Run("touch resets the probe", func(t *testing.T) {
		path := fx.abs("S07.m4b")
		before := e.fileAt(path)
		if before.ProbeJSON == nil {
			t.Fatal("setup: not probed")
		}
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
		queued := e.enq.count()
		res := e.scan()
		if res.FilesChanged != 1 || res.ProbesEnqueued != 1 || e.enq.count() != queued+1 || e.enq.ids[queued] != before.ID {
			t.Fatalf("result = %+v", res)
		}
		if f := e.fileAt(path); f.ID != before.ID || f.ProbeJSON != nil || f.MtimeNs == before.MtimeNs {
			t.Fatalf("row after touch = %+v", f)
		}
		e.probeAll()
		if f := e.fileAt(path); f.ProbeJSON == nil {
			t.Fatal("not probed again")
		}
		fx.verify(e)
	})

	t.Run("duplicate copy", func(t *testing.T) {
		src, dup := fx.abs("S08.m4b"), fx.abs("Dup/Copy of S08.m4b")
		orig := e.fileAt(src)
		data, mtime := readKeepMtime(t, src)
		writeKeepMtime(t, dup, data, mtime)
		res := e.scan()
		if res.FilesAdded != 1 || res.FilesMoved != 0 || res.BooksAdded != 1 || res.ProbesEnqueued != 1 {
			t.Fatalf("result = %+v", res)
		}
		if f := e.fileAt(dup); f.ID == orig.ID {
			t.Fatal("the copy took the original's identity")
		}
		after := e.fileAt(src)
		if after.ID != orig.ID || after.Path != src || after.MissingSince != 0 || after.BookID != orig.BookID {
			t.Fatalf("original changed: %+v", after)
		}
		fx.dur[dup] = fx.dur[src]
		fx.book("Dup", "", "Dup/Copy of S08.m4b")
		e.probeAll()
		fx.verify(e)
	})
}

func fileIDs(t *testing.T, e *env, bookID string) []string {
	t.Helper()
	fs, err := e.repo.AudioFiles(bg, bookID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func readKeepMtime(t *testing.T, path string) ([]byte, time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return data, fi.ModTime()
}

func writeKeepMtime(t *testing.T, path string, data []byte, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// relocate renames a book and its files in the expectations after a folder or file rename.
func (fx *fixture) relocate(oldRel, newRel string) {
	o, n := fx.abs(oldRel), fx.abs(newRel)
	w := fx.books[o]
	delete(fx.books, o)
	fx.books[n] = w
	for i, f := range w.files {
		if rel, err := filepath.Rel(o, f); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			w.files[i] = filepath.Join(n, rel)
		} else if f == o {
			w.files[i] = n
		}
		fx.dur[w.files[i]] = fx.dur[f]
	}
}

func (fx *fixture) moveFile(fromBook, toBook, fromRel, toRel string) {
	from, to := fx.abs(fromRel), fx.abs(toRel)
	fx.dur[to] = fx.dur[from]
	delete(fx.dur, from)
	fb, tb := fx.books[fx.abs(fromBook)], fx.books[fx.abs(toBook)]
	fb.files = without(fb.files, from)
	tb.files = append(tb.files, to) // 99 sorts last
}

func (fx *fixture) dropFile(book, rel string) (idx int) {
	b := fx.books[fx.abs(book)]
	for i, f := range b.files {
		if f == fx.abs(rel) {
			idx = i
		}
	}
	b.files = without(b.files, fx.abs(rel))
	return idx
}

func (fx *fixture) restoreFile(book, rel string, idx int) {
	b := fx.books[fx.abs(book)]
	b.files = append(b.files[:idx], append([]string{fx.abs(rel)}, b.files[idx:]...)...)
}

func without(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}
