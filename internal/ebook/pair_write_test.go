package ebook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const pairTUser = "us_pair"

// pairTSetup adds one person to an environment.
func pairTSetup(e *ienv) {
	e.t.Helper()
	pairTExec(e, `INSERT INTO users(id, username, role, created_at) VALUES (?, 'Ana', 'owner', 1)`, pairTUser)
}

func pairTExec(e *ienv, q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.Writer().Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// pairTBook inserts a live book with authors.
func pairTBook(e *ienv, id, title string, authors ...string) {
	e.t.Helper()
	pairTExec(e, `INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES (?,?,?,?,?,1,1)`,
		id, e.lib.ID, e.folder.ID, "/pair/"+id, title)
	for i, a := range authors {
		pid := fmt.Sprintf("pe_%s_%d", id, i)
		pairTExec(e, `INSERT INTO people(id, name, created_at) VALUES (?,?,1)`, pid, a)
		pairTExec(e, `INSERT INTO book_people(book_id, person_id, role, idx) VALUES (?,?, 'author', ?)`, id, pid, i)
	}
}

func pairTHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// pairTFile inserts an upload-origin ebook file (no folder needed) with an edition saying title and authors.
func pairTFile(e *ienv, id, state, bookID, title string, authors ...string) {
	e.t.Helper()
	sha := pairTHash(id)
	var book any
	if bookID != "" {
		book = bookID
	}
	pairTExec(e, `INSERT INTO ebook_files(id, origin, library_id, book_id, ext, size, quick_hash, sha256, format, state, created_at, updated_at)
VALUES (?, 'upload', ?, ?, 'epub', 10, ?, ?, 'epub', ?, ?, 1)`, id, e.lib.ID, book, sha, sha, state, time.Now().UnixNano()/1000)
	if title == "" {
		return
	}
	js, _ := json.Marshal(authors)
	pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json,
chapter_count, word_count, char_count, created_at) VALUES (?,?,?,?,?,5,'epub',?,?,1,1,1,1)`,
		"ed_"+id, id, sha, Recipe, sha, title, string(js))
}

func (e *ienv) pairTFile(id string) File { return e.file(id) }

func pairTCount(e *ienv, q string, args ...any) int { return e.count(q, args...) }

func TestPairEngineAuto(t *testing.T) {
	e := newIEnv(t, true)
	e.write("Hero/01.mp3", []byte("audio one"))
	e.scan()
	b := e.book("Hero")
	pairTExec(e, `UPDATE books SET title='Hero' WHERE id=?`, b.ID)
	pairTExec(e, `INSERT INTO people(id, name, created_at) VALUES ('pe_a', 'Ann Author', 1)`)
	pairTExec(e, `INSERT INTO book_people(book_id, person_id, role, idx) VALUES (?, 'pe_a', 'author', 0)`, b.ID)
	e.write("Elsewhere/hero.epub", sampleEPUB(t, "Hero"))
	e.scan()
	f := e.fileAt(filepath.Join(e.root, "Elsewhere", "hero.epub"))
	e.waitState(f.ID)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && e.file(f.ID).BookID == "" {
		time.Sleep(20 * time.Millisecond)
	}
	got := e.file(f.ID)
	if got.BookID != b.ID || got.PairedBy != "title" {
		t.Fatalf("file on %q by %q, want %q by title", got.BookID, got.PairedBy, b.ID)
	}
}

func TestPairEngineRespectsHold(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	pairTBook(e, "bk_live", "Dune", "Frank Herbert")
	pairTBook(e, "bk_gone", "Emma", "Jane Austen")
	pairTExec(e, `UPDATE books SET missing_since=5 WHERE id='bk_gone'`)
	pairTFile(e, "f_ctrl", "ready", "", "Dune", "Frank Herbert")
	pairTFile(e, "f_person", "ready", "", "Dune", "Frank Herbert")
	pairTFile(e, "f_kept", "ready", "", "Dune", "Frank Herbert")
	pairTFile(e, "f_notsame", "ready", "", "Dune", "Frank Herbert")
	pairTFile(e, "f_missing", "ready", "", "Emma", "Jane Austen")
	pairTFile(e, "f_pending", "pending", "", "Dune", "Frank Herbert")
	pairTExec(e, `UPDATE ebook_files SET hold='person' WHERE id='f_person'`)
	pairTExec(e, `UPDATE ebook_files SET hold='kept' WHERE id='f_kept'`)
	pairTExec(e, `INSERT INTO ebook_not_same(ebook_file_id, book_id, created_at) VALUES ('f_notsame','bk_live',1)`)
	if err := e.svc.PairPending(bg); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"f_ctrl": "bk_live", "f_person": "", "f_kept": "", "f_notsame": "", "f_missing": "", "f_pending": ""} {
		if got := e.file(id).BookID; got != want {
			t.Errorf("%s on %q, want %q", id, got, want)
		}
	}
	if by := e.file("f_ctrl").PairedBy; by != "title" {
		t.Errorf("paired_by = %q", by)
	}
	// The engine's write also refuses when a person acts between its read and its write.
	pairTFile(e, "f_late", "ready", "", "Dune", "Frank Herbert")
	pairTExec(e, `UPDATE ebook_files SET hold='person' WHERE id='f_late'`)
	if err := e.svc.pairFile(bg, "f_late"); err != nil {
		t.Fatal(err)
	}
	if e.file("f_late").BookID != "" {
		t.Error("pairFile overrode a hold")
	}
}

func TestPairFolderRespectsNotSame(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	e.write("Folder Book/01.mp3", []byte("audio one"))
	e.write("Folder Book/folder.epub", []byte("e1"))
	e.scan()
	f := e.fileAt(filepath.Join(e.root, "Folder Book", "folder.epub"))
	bk := e.book("Folder Book")
	if f.BookID != bk.ID {
		t.Fatalf("folder pairing did not happen: %q", f.BookID)
	}
	if err := e.svc.Unpair(bg, f.ID, bk.ID, pairTUser); err != nil {
		t.Fatal(err)
	}
	e.scan()
	if got := e.file(f.ID); got.BookID == bk.ID {
		t.Fatalf("rescan redid an unpaired folder pairing: %+v", got)
	}
	// A not-same row alone also stops it, for a file whose hold is clear.
	pairTExec(e, `UPDATE ebook_files SET hold=NULL WHERE id=?`, f.ID)
	e.scan()
	if got := e.file(f.ID); got.BookID == bk.ID {
		t.Fatalf("not-same row ignored: %+v", got)
	}
}

func TestPairManual(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	pairTBook(e, "b1", "One")
	pairTBook(e, "b2", "Two")
	pairTBook(e, "b_gone", "Gone")
	pairTExec(e, `UPDATE books SET missing_since=5 WHERE id='b_gone'`)
	pairTFile(e, "f1", "ready", "b1", "")
	pairTFile(e, "f2", "ready", "", "")
	pairTFile(e, "f_pend", "pending", "", "")
	if err := e.svc.Pair(bg, "f2", "b1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if got := e.file("f2"); got.BookID != "b1" || got.PairedBy != "person" || got.Hold != "" {
		t.Fatalf("after pair: %+v", got)
	}
	// Re-pair moves the file: its default and every choice for it go.
	if err := e.svc.SetBookDefault(bg, "b1", "f2", pairTUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Choose(bg, pairTUser, "b1", "f1"); err != nil {
		t.Fatal(err)
	}
	pairTExec(e, `INSERT INTO ebook_not_same(ebook_file_id, book_id, created_at) VALUES ('f1','b2',1)`)
	if err := e.svc.Pair(bg, "f1", "b2", pairTUser); err != nil {
		t.Fatal(err)
	}
	if got := e.file("f1"); got.BookID != "b2" {
		t.Fatalf("not moved: %+v", got)
	}
	if n := pairTCount(e, `SELECT count(*) FROM text_choices`); n != 0 {
		t.Errorf("%d choices left for a moved file", n)
	}
	if n := pairTCount(e, `SELECT count(*) FROM ebook_not_same`); n != 0 {
		t.Errorf("not-same for the new pair kept")
	}
	if err := e.svc.Pair(bg, "f2", "b2", pairTUser); err != nil {
		t.Fatal(err)
	}
	if e.file("f2").BookDefault {
		t.Error("default flag survived a move")
	}
	if n := pairTCount(e, `SELECT count(*) FROM audit_log WHERE action='ebook.pair'`); n != 3 {
		t.Errorf("%d pair audit rows, want 3", n)
	}
	if err := e.svc.Pair(bg, "f2", "b2", pairTUser); err != nil {
		t.Errorf("pairing to the same book must be a no-op: %v", err)
	}
	for _, c := range []struct {
		file, book string
		want       error
	}{
		{"f_pend", "b1", ErrNotReady},
		{"f2", "b_gone", ErrBookNotFound},
		{"f2", "nope", ErrBookNotFound},
		{"nope", "b1", ErrFileNotFound},
	} {
		if err := e.svc.Pair(bg, c.file, c.book, pairTUser); !errors.Is(err, c.want) {
			t.Errorf("Pair(%s,%s) = %v, want %v", c.file, c.book, err, c.want)
		}
	}
	// The same upload bytes already on the book.
	pairTExec(e, `INSERT INTO ebook_files(id, origin, library_id, ext, size, quick_hash, sha256, state, created_at, updated_at)
VALUES ('f_dup','upload',?, 'epub', 10, ?, ?, 'ready', 9, 9)`, e.lib.ID, pairTHash("f2"), pairTHash("f2"))
	if err := e.svc.Pair(bg, "f_dup", "b2", pairTUser); !errors.Is(err, ErrAlreadyOnBook) {
		t.Errorf("duplicate upload: %v", err)
	}
}

func TestPairUnpair(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	pairTBook(e, "b1", "One")
	pairTFile(e, "f1", "ready", "b1", "")
	pairTFile(e, "f2", "ready", "b1", "")
	if err := e.svc.SetBookDefault(bg, "b1", "f1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Choose(bg, pairTUser, "b1", "f2"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Unpair(bg, "f1", "other", pairTUser); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("wrong book: %v", err)
	}
	if err := e.svc.Unpair(bg, "f1", "b1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if got := e.file("f1"); got.BookID == "" || got.BookID == "b1" || got.PairedBy != "" || got.Hold != "person" || !got.BookDefault || pairTCount(e, `SELECT count(*) FROM books WHERE id=? AND kind='text'`, got.BookID) != 1 {
		t.Fatalf("after unpair: %+v", got)
	}
	if n := pairTCount(e, `SELECT count(*) FROM ebook_not_same WHERE ebook_file_id='f1' AND book_id='b1'`); n != 1 {
		t.Error("no not-same row")
	}
	if n := pairTCount(e, `SELECT count(*) FROM audit_log WHERE action='ebook.unpair'`); n != 1 {
		t.Error("no audit row")
	}
	if err := e.svc.Unpair(bg, "f2", "b1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if n := pairTCount(e, `SELECT count(*) FROM text_choices`); n != 0 {
		t.Error("choice for an unpaired file kept")
	}
	pairTBook(e, "b2", "Two")
	pairTExec(e, `UPDATE ebook_files SET book_id=NULL WHERE id='f1'`) // NotSame and Keep are for files with no book
	if err := e.svc.NotSame(bg, "f1", "b2", pairTUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.NotSame(bg, "f1", "b2", pairTUser); err != nil {
		t.Errorf("not-same twice: %v", err)
	}
	if err := e.svc.Keep(bg, "f1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if e.file("f1").Hold != "kept" {
		t.Error("not kept")
	}
	pairTFile(e, "f3", "ready", "b2", "")
	if err := e.svc.Keep(bg, "f3", pairTUser); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("keep on a paired file: %v", err)
	}
	if err := e.svc.NotSame(bg, "f3", "b1", pairTUser); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("not-same on a paired file: %v", err)
	}
}

func TestPairDefaultAndChoice(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	pairTBook(e, "b1", "One")
	pairTBook(e, "b2", "Two")
	pairTFile(e, "f1", "ready", "b1", "")
	time.Sleep(2 * time.Millisecond)
	pairTFile(e, "f2", "ready", "b1", "")
	pairTFile(e, "f3", "pending", "b1", "")
	pairTFile(e, "fo", "ready", "b2", "")
	pairTExec(e, `UPDATE ebook_files SET created_at = CASE id WHEN 'f1' THEN 10 WHEN 'f2' THEN 20 ELSE 30 END`)
	want := func(label, id string) {
		t.Helper()
		f, _, ok, err := e.svc.Store().EditionFor(bg, pairTUser, "b1")
		if err != nil || !ok || f.ID != id {
			t.Errorf("%s: edition %q ok=%v err=%v, want %q", label, f.ID, ok, err, id)
		}
	}
	want("oldest ready", "f1")
	if err := e.svc.SetBookDefault(bg, "b1", "f2", pairTUser); err != nil {
		t.Fatal(err)
	}
	want("book default", "f2")
	if err := e.svc.SetBookDefault(bg, "b1", "f3", pairTUser); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("default on a pending file: %v", err)
	}
	want("default kept after a failed change", "f2")
	if err := e.svc.SetBookDefault(bg, "b1", "fo", pairTUser); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("default on another book's file: %v", err)
	}
	if err := e.svc.Choose(bg, pairTUser, "b1", "f1"); err != nil {
		t.Fatal(err)
	}
	want("own choice", "f1")
	if err := e.svc.Choose(bg, pairTUser, "b1", "f2"); err != nil {
		t.Fatal(err)
	}
	if id, _ := e.svc.Store().Choice(bg, pairTUser, "b1"); id != "" {
		t.Errorf("choosing the default left a row for %q", id)
	}
	for _, bad := range []string{"f3", "fo", "nope"} {
		if err := e.svc.Choose(bg, pairTUser, "b1", bad); !errors.Is(err, ErrFileNotFound) {
			t.Errorf("Choose(%s) = %v", bad, err)
		}
	}
	// A stale choice (file now on another book) is ignored; so is one that is no longer ready.
	pairTExec(e, `INSERT INTO text_choices(user_id, book_id, ebook_file_id, updated_at) VALUES (?, 'b1', 'fo', 1)
ON CONFLICT(user_id, book_id) DO UPDATE SET ebook_file_id='fo'`, pairTUser)
	want("choice of another book's file", "f2")
	pairTExec(e, `UPDATE text_choices SET ebook_file_id='f3' WHERE user_id=?`, pairTUser)
	want("choice not ready", "f2")
	// Unpairing the chosen file drops the choice and falls back.
	if err := e.svc.Choose(bg, pairTUser, "b1", "f1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Unpair(bg, "f1", "b1", pairTUser); err != nil {
		t.Fatal(err)
	}
	want("choice unpaired", "f2")
	if _, _, ok, _ := e.svc.Store().EditionFor(bg, pairTUser, "nobook"); ok {
		t.Error("an edition for a book with none")
	}
	if f, ok, _ := e.svc.Store().BookDefault(bg, "b2"); ok {
		t.Errorf("default for b2: %+v", f)
	}
}

type pairTStat struct {
	size  int64
	mtime time.Time
	sha   string
}

func pairTStatOf(t *testing.T, p string) pairTStat {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return pairTStat{fi.Size(), fi.ModTime(), pairTHash(string(b))}
}

func TestPairNeverWritesFiles(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	libFile := e.write("Lib/book.epub", sampleEPUB(t, "Lib"))
	upBytes := sampleEPUB(t, "Up")
	upSHA := pairTHash(string(upBytes))
	if err := os.MkdirAll(e.svc.ebooksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	upFile := e.svc.storedPath(upSHA, "epub")
	if err := os.WriteFile(upFile, upBytes, 0o444); err != nil {
		t.Fatal(err)
	}
	e.scan()
	lf := e.fileAt(libFile)
	pairTBook(e, "b1", "Lib")
	pairTBook(e, "b2", "Up")
	pairTExec(e, `INSERT INTO ebook_files(id, origin, library_id, ext, size, quick_hash, sha256, format, state, created_at, updated_at)
VALUES ('f_up','upload',?, 'epub', ?, ?, ?, 'epub', 'ready', 5, 5)`, e.lib.ID, len(upBytes), pairTHash("q"), upSHA)
	pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=? WHERE id=?`, pairTHash("lib"), lf.ID)
	libDir := filepath.Dir(libFile)
	if err := os.Chmod(libFile, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(libDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(libDir, 0o755); _ = os.Chmod(libFile, 0o644) })
	before := map[string]pairTStat{libFile: pairTStatOf(t, libFile), upFile: pairTStatOf(t, upFile)}
	steps := []func() error{
		func() error { return e.svc.Pair(bg, lf.ID, "b1", pairTUser) },
		func() error { return e.svc.Pair(bg, "f_up", "b2", pairTUser) },
		func() error { return e.svc.SetBookDefault(bg, "b2", "f_up", pairTUser) },
		func() error { return e.svc.Choose(bg, pairTUser, "b1", lf.ID) },
		func() error { return e.svc.Unpair(bg, lf.ID, "b1", pairTUser) },
		func() error {
			pairTExec(e, `UPDATE ebook_files SET book_id=NULL WHERE id=?`, lf.ID) // NotSame and Keep are for files with no book
			return e.svc.NotSame(bg, lf.ID, "b2", pairTUser)
		},
		func() error { return e.svc.Keep(bg, lf.ID, pairTUser) },
		func() error { return e.svc.Unpair(bg, "f_up", "b2", pairTUser) },
		func() error { return e.svc.PairPending(bg) },
		func() error { return e.svc.pairFile(bg, "f_up") },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		for p, was := range before {
			if now := pairTStatOf(t, p); now != was {
				t.Fatalf("step %d changed %s: %+v -> %+v", i, filepath.Base(p), was, now)
			}
		}
	}
	ents, err := os.ReadDir(libDir)
	if err != nil || len(ents) != 1 {
		t.Errorf("library folder now holds %d entries (%v)", len(ents), err)
	}
}

func TestPairRace(t *testing.T) {
	e := newIEnv(t, false)
	pairTSetup(e)
	pairTBook(e, "b_auto", "Dune", "Frank Herbert")
	pairTBook(e, "b_man", "Other")
	const n = 25
	for i := 0; i < n; i++ {
		pairTFile(e, fmt.Sprintf("f%02d", i), "ready", "", "Dune", "Frank Herbert")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	var engineErr error
	go func() {
		defer wg.Done()
		engineErr = e.svc.PairPending(bg)
	}()
	go func() {
		defer wg.Done()
		for i := n - 1; i >= 0; i-- {
			if err := e.svc.Pair(bg, fmt.Sprintf("f%02d", i), "b_man", pairTUser); err != nil {
				t.Errorf("Pair: %v", err)
			}
		}
	}()
	wg.Wait()
	if engineErr != nil {
		t.Fatal(engineErr)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("f%02d", i)
		if f := e.file(id); f.BookID != "b_man" || f.PairedBy != "person" {
			t.Errorf("%s ended on %q by %q: the person must win", id, f.BookID, f.PairedBy)
		}
	}
}
