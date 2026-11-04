package ebook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const tbUser2 = "us_two"

func tbUsers(e *ienv) {
	e.t.Helper()
	pairTSetup(e)
	pairTExec(e, `INSERT INTO users(id, username, role, created_at) VALUES (?, 'Bo', 'member', 1)`, tbUser2)
}

func tbPosition(e *ienv, user, book, edition string, at int64, pct int) {
	e.t.Helper()
	var ed any
	if edition != "" {
		ed = edition
	}
	pairTExec(e, `INSERT INTO reading_positions(user_id, book_id, edition_id, chapter_idx, char_pos, percent_bp, client_updated_at, server_updated_at, device_id)
VALUES (?,?,?,1,?,?,?,?,'dev_test_1')`, user, book, ed, pct, pct, at, at)
}

func tbPct(e *ienv, user, book string) int {
	var n int
	if err := e.st.Reader().QueryRow(`SELECT percent_bp FROM reading_positions WHERE user_id=? AND book_id=?`, user, book).Scan(&n); err != nil {
		return -1
	}
	return n
}

func tbMerged(e *ienv, from string) bool {
	return e.count(`SELECT count(*) FROM books WHERE id=?`, from) == 0
}

func TestMergeConfident(t *testing.T) {
	t.Run("isbn", func(t *testing.T) {
		e := newIEnv(t, false)
		tbAudio(e, "a1", "Anything Else", "9780306406157")
		f := tbText(e, "f1", "Completely Different", "9780306406157")
		if err := e.svc.mergePending(bg, "", ""); err != nil {
			t.Fatal(err)
		}
		if got := e.file("f1"); got.BookID != "a1" || got.PairedBy != "isbn" || got.MergedFrom != f.BookID || got.BookDefault {
			t.Fatalf("file = %+v", got)
		}
		if !tbMerged(e, f.BookID) || e.count(`SELECT count(*) FROM merged_books WHERE from_id=? AND to_id='a1'`, f.BookID) != 1 {
			t.Error("text book not merged away with its alias")
		}
		if e.count(`SELECT count(*) FROM audit_log WHERE action='ebook.merge'`) != 1 {
			t.Error("no audit row")
		}
	})
	t.Run("title and surname", func(t *testing.T) {
		e := newIEnv(t, false)
		tbAudio(e, "a1", "The Long Way Home", "", "Ann Author")
		f := tbText(e, "f1", "The Long Way Home", "", "Ann Author")
		if err := e.svc.PairBook(bg, "a1"); err != nil { // the OnLocal path
			t.Fatal(err)
		}
		if e.file("f1").BookID != "a1" || !tbMerged(e, f.BookID) {
			t.Fatal("title plus author did not merge")
		}
	})
	t.Run("folder", func(t *testing.T) {
		e := newIEnv(t, false)
		p := e.write("F/x.epub", []byte("not an epub"))
		e.scan()
		lf := e.fileAt(p)
		sha := pairTHash("fold")
		pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=? WHERE id=?`, sha, lf.ID)
		pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, chapter_count, word_count, char_count, created_at)
VALUES ('ed_f', ?, ?, ?, ?, 5, 'epub', 'Unrelated', 1, 1, 1, 1)`, lf.ID, sha, Recipe, sha)
		if err := e.svc.ensureFile(bg, lf.ID); err != nil {
			t.Fatal(err)
		}
		pairTExec(e, `INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('a1', ?, ?, ?, 'Beside', 1, 1)`,
			e.lib.ID, e.folder.ID, filepath.Join(e.root, "F"))
		if err := e.svc.mergePending(bg, "", ""); err != nil {
			t.Fatal(err)
		}
		if got := e.file(lf.ID); got.BookID != "a1" || got.PairedBy != "folder" {
			t.Fatalf("file = %+v", got)
		}
	})
}

func TestMergeUnsureStays(t *testing.T) {
	e := newIEnv(t, false)
	tbAudio(e, "a1", "Dune", "", "Frank Herbert")
	tbAudio(e, "a2", "Dune", "", "Frank Herbert")
	f := tbText(e, "f1", "Dune", "", "Frank Herbert")
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := e.file("f1"); got.BookID != f.BookID || tbKind(e, got.BookID) != "text" {
		t.Fatalf("two matching audiobooks, yet the file moved: %+v", got)
	}
	// A weak match (same author only) stays a suggestion too.
	tbAudio(e, "a3", "Other Title", "", "Ann Author")
	g := tbText(e, "f2", "A Different Book", "", "Ann Author")
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if e.file("f2").BookID != g.BookID {
		t.Error("an author-only match merged")
	}
}

func TestMergeMovesData(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "a1", "Moves", "9780306406157")
	f := tbText(e, "f1", "Moves", "9780306406157")
	from, ed := f.BookID, "ed_f1"
	// Positions: user 1 has the newer place on the text; user 2 has the newer on the audiobook.
	tbPosition(e, pairTUser, from, ed, 2000, 700)
	tbPosition(e, pairTUser, "a1", "", 1000, 100)
	tbPosition(e, tbUser2, from, ed, 1000, 300)
	tbPosition(e, tbUser2, "a1", "", 3000, 900)
	pairTExec(e, `INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, created_at, updated_at)
VALUES ('hl1', ?, ?, ?, 1, 'c1.p1.s0', 0, 'c1.p1.s0', 3, 'abc', 1, 1)`, pairTUser, from, ed)
	pairTExec(e, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, edition_id, sid, created_at, updated_at) VALUES ('bm1', ?, ?, 0, ?, 'c1.p1.s0', 1, 1)`, pairTUser, from, ed)
	pairTExec(e, `INSERT INTO reading_sessions(id, user_id, book_id, device_id, started_at, ended_at, chars_read, active_ms) VALUES ('rs1', ?, ?, 'dev_test_1', 1, 2, 5, 5)`, pairTUser, from)
	// Shelves: one holds only the text book, one holds both.
	for _, q := range []string{
		`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_a', '` + pairTUser + `', 'A', 1, 1)`,
		`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_b', '` + pairTUser + `', 'B', 1, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_a', '` + from + `', 0, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_b', '` + from + `', 0, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_b', 'a1', 1, 1)`,
	} {
		pairTExec(e, q)
	}
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if !tbMerged(e, from) {
		t.Fatal("not merged")
	}
	if tbPct(e, pairTUser, "a1") != 700 || tbPct(e, tbUser2, "a1") != 900 {
		t.Errorf("newest place did not win both ways: %d %d", tbPct(e, pairTUser, "a1"), tbPct(e, tbUser2, "a1"))
	}
	for _, c := range []struct{ q string }{
		{`SELECT count(*) FROM highlights WHERE id='hl1' AND book_id='a1'`},
		{`SELECT count(*) FROM bookmarks WHERE id='bm1' AND book_id='a1'`},
		{`SELECT count(*) FROM reading_sessions WHERE id='rs1' AND book_id='a1'`},
		{`SELECT count(*) FROM shelf_items WHERE shelf_id='sh_a' AND book_id='a1'`},
		{`SELECT count(*) FROM shelf_items WHERE shelf_id='sh_b' AND book_id='a1'`},
	} {
		if e.count(c.q) != 1 {
			t.Errorf("not moved: %s", c.q)
		}
	}
	if e.count(`SELECT count(*) FROM shelf_items WHERE book_id=?`, from) != 0 {
		t.Error("shelf rows still on the merged-away book")
	}
	if e.count(`SELECT count(*) FROM progress`) != 0 {
		t.Error("listening progress was written")
	}
	// The alias resolves.
	if to, ok := e.repo.MergedInto(bg, from); !ok || to != "a1" {
		t.Errorf("alias = %q %v", to, ok)
	}
}

func TestMergeGuard(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *ienv, from string)
		want  bool // merged
	}{
		{"book grant", func(e *ienv, from string) {
			pairTExec(e, `INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('g1', ?, 'book', ?, 1, 1)`, tbUser2, from)
		}, false},
		{"shelf grant", func(e *ienv, from string) {
			pairTExec(e, `INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh', ?, 'S', 1, 1)`, pairTUser)
			pairTExec(e, `INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh', ?, 0, 1)`, from)
			pairTExec(e, `INSERT INTO grants(id, user_id, scope, shelf_id, created_at, updated_at) VALUES ('g1', ?, 'shelf', 'sh', 1, 1)`, tbUser2)
		}, false},
		{"shared shelf", func(e *ienv, from string) {
			pairTExec(e, `INSERT INTO shelves(id, owner_id, name, shared, created_at, updated_at) VALUES ('sh', ?, 'S', 1, 1, 1)`, pairTUser)
			pairTExec(e, `INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh', ?, 0, 1)`, from)
		}, false},
		{"share link", func(e *ienv, from string) {
			pairTExec(e, `INSERT INTO share_links(id, token_hash, book_id, created_by, expires_at, created_at) VALUES ('sl', randomblob(32), ?, ?, 100, 1)`, from, pairTUser)
		}, false},
		{"private shelf is fine", func(e *ienv, from string) {
			pairTExec(e, `INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh', ?, 'S', 1, 1)`, pairTUser)
			pairTExec(e, `INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh', ?, 0, 1)`, from)
		}, true},
		{"other library with data", func(e *ienv, from string) {
			tbOtherLibrary(e)
			tbPosition(e, pairTUser, from, "ed_f1", 5, 50)
		}, false},
		{"other library without data", func(e *ienv, from string) { tbOtherLibrary(e) }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newIEnv(t, false)
			tbUsers(e)
			target := "a1"
			f := tbText(e, "f1", "Guarded", "9780306406157")
			if c.name == "other library with data" || c.name == "other library without data" {
				target = "a_other"
			}
			c.setup(e, f.BookID)
			if target == "a1" {
				tbAudio(e, "a1", "Guarded", "9780306406157")
			}
			if err := e.svc.mergePending(bg, "", ""); err != nil {
				t.Fatal(err)
			}
			if merged := tbMerged(e, f.BookID); merged != c.want {
				t.Fatalf("merged = %v, want %v", merged, c.want)
			}
			if !c.want && e.file("f1").BookID != f.BookID {
				t.Error("a refused merge still moved the file")
			}
		})
	}
	// A person's own merge skips the guard.
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "a1", "Guarded", "")
	f := tbText(e, "f1", "Whatever", "")
	pairTExec(e, `INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('g1', ?, 'book', ?, 1, 1)`, tbUser2, f.BookID)
	if err := e.svc.MergeBook(bg, f.BookID, "a1", pairTUser); err != nil {
		t.Fatal(err)
	}
	if !tbMerged(e, f.BookID) || e.file("f1").PairedBy != "person" {
		t.Error("a person's merge did not go through")
	}
	if err := e.svc.MergeBook(bg, "nobook", "a1", pairTUser); !errors.Is(err, ErrBookNotFound) {
		t.Errorf("merge of an unknown book: %v", err)
	}
}

// tbOtherLibrary adds a second library holding the audiobook a_other (same ISBN as the text book).
func tbOtherLibrary(e *ienv) {
	e.t.Helper()
	pairTExec(e, `INSERT INTO libraries(id, name, created_at) VALUES ('lib_2', 'Two', 1)`)
	pairTExec(e, `INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_2', 'lib_2', '/two', 1)`)
	pairTExec(e, `INSERT INTO books(id, library_id, folder_id, root_path, title, isbn, added_at, updated_at) VALUES ('a_other', 'lib_2', 'lf_2', '/two/a', 'Guarded', '9780306406157', 1, 1)`)
}

func TestMergeAfterTags(t *testing.T) {
	e := newIEnv(t, false)
	tbAudio(e, "a1", "track 01", "", "") // before its tags are read the audiobook says nothing useful
	f := tbText(e, "f1", "A Tagged Book", "", "Ann Author")
	if err := e.svc.PairBook(bg, "a1"); err != nil {
		t.Fatal(err)
	}
	if tbMerged(e, f.BookID) {
		t.Fatal("merged on a bad title")
	}
	pairTExec(e, `UPDATE books SET title='A Tagged Book' WHERE id='a1'`)
	pairTExec(e, `INSERT INTO people(id, name, created_at) VALUES ('pe_t', 'Ann Author', 1) ON CONFLICT DO NOTHING`)
	pairTExec(e, `INSERT INTO book_people(book_id, person_id, role, idx) VALUES ('a1', 'pe_t', 'author', 0)`)
	if err := e.svc.PairBook(bg, "a1"); err != nil { // what metadata's OnLocal hook calls
		t.Fatal(err)
	}
	if !tbMerged(e, f.BookID) {
		t.Fatal("not merged once the tags were right")
	}
}

func TestMergeNeverWritesFiles(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	libFile := e.write("Lib/book.epub", sampleEPUB(t, "Lib"))
	e.scan()
	lf := e.fileAt(libFile)
	pairTExec(e, `UPDATE ebook_files SET state='ready', sha256=? WHERE id=?`, pairTHash("w"), lf.ID)
	pairTExec(e, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, isbn, chapter_count, word_count, char_count, created_at)
VALUES ('ed_w', ?, ?, ?, ?, 5, 'epub', 'Lib', '9780306406157', 1, 1, 1, 1)`, lf.ID, pairTHash("w"), Recipe, pairTHash("w"))
	tbAudio(e, "a1", "Lib", "9780306406157")
	libDir := filepath.Dir(libFile)
	if err := os.Chmod(libFile, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(libDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(libDir, 0o755); _ = os.Chmod(libFile, 0o644) })
	before := pairTStatOf(t, libFile)
	steps := []func() error{
		func() error { return e.svc.ensureFile(bg, lf.ID) },
		func() error { return e.svc.mergePending(bg, "", "") },
		func() error { return e.svc.Unpair(bg, lf.ID, "a1", pairTUser) },
		func() error { return e.svc.MergeBook(bg, e.file(lf.ID).BookID, "a1", pairTUser) },
		func() error { return e.svc.Unpair(bg, lf.ID, "a1", pairTUser) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if now := pairTStatOf(t, libFile); now != before {
			t.Fatalf("step %d changed the file: %+v -> %+v", i, before, now)
		}
	}
	if ents, err := os.ReadDir(libDir); err != nil || len(ents) != 1 {
		t.Errorf("library folder holds %d entries (%v)", len(ents), err)
	}
}

func TestMergeRace(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	const n = 12
	for i := 0; i < n; i++ {
		tbAudio(e, fmt.Sprintf("a%02d", i), fmt.Sprintf("Race %d", i), "", "Ann Author")
		tbText(e, fmt.Sprintf("f%02d", i), fmt.Sprintf("Race %d", i), "", "Ann Author")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	var engineErr error
	go func() {
		defer wg.Done()
		engineErr = e.svc.mergePending(bg, "", "")
	}()
	go func() {
		defer wg.Done()
		for i := n - 1; i >= 0; i-- {
			id := fmt.Sprintf("f%02d", i)
			// A person splits a file the engine may just have merged; either order must leave rows consistent.
			if f := e.file(id); f.BookID == fmt.Sprintf("a%02d", i) {
				if err := e.svc.Unpair(bg, id, f.BookID, pairTUser); err != nil && !errors.Is(err, ErrFileNotFound) {
					t.Errorf("Unpair: %v", err)
				}
			}
		}
	}()
	wg.Wait()
	if engineErr != nil {
		t.Fatal(engineErr)
	}
	for i := 0; i < n; i++ {
		f := e.file(fmt.Sprintf("f%02d", i))
		if f.BookID == "" || e.count(`SELECT count(*) FROM books WHERE id=?`, f.BookID) != 1 {
			t.Errorf("%s ended with no book (%q)", f.ID, f.BookID)
		}
		if f.Hold == "person" && tbKind(e, f.BookID) != "text" {
			t.Errorf("%s was split but sits on a %s book", f.ID, tbKind(e, f.BookID))
		}
	}
}

func TestMergeNoWiden(t *testing.T) {
	// The guard keeps a text book with a book grant from merging; the grant would otherwise reach the audio.
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "a1", "Wide", "9780306406157")
	f := tbText(e, "f1", "Wide", "9780306406157")
	pairTExec(e, `INSERT INTO grants(id, user_id, scope, book_id, can_read, created_at, updated_at) VALUES ('g1', ?, 'book', ?, 1, 1, 1)`, tbUser2, f.BookID)
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM grants WHERE book_id='a1'`) != 0 || e.file("f1").BookID != f.BookID {
		t.Error("a grant on the text book reached the audiobook")
	}
}

func TestSplitRemembered(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	tbAudio(e, "a1", "Split Me", "9780306406157")
	f := tbText(e, "f1", "Split Me", "9780306406157")
	old := f.BookID
	if err := e.svc.mergePending(bg, "", ""); err != nil || !tbMerged(e, old) {
		t.Fatalf("setup merge: %v", err)
	}
	tbPosition(e, pairTUser, "a1", "ed_f1", 100, 400) // a place made in the ebook after the merge
	pairTExec(e, `INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, created_at, updated_at)
VALUES ('hl1', ?, 'a1', 'ed_f1', 1, 'c1.p1.s0', 0, 'c1.p1.s0', 3, 'abc', 1, 1)`, pairTUser)
	pairTExec(e, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_audio', ?, 'a1', 5000, 1, 1)`, pairTUser)
	if err := e.svc.Unpair(bg, "f1", "a1", pairTUser); err != nil {
		t.Fatal(err)
	}
	got := e.file("f1")
	if got.BookID != old || got.Hold != "person" || !got.BookDefault || tbKind(e, old) != "text" {
		t.Fatalf("after split = %+v (kind %s); want the old id %s back", got, tbKind(e, old), old)
	}
	if e.count(`SELECT count(*) FROM merged_books`) != 0 {
		t.Error("alias not removed")
	}
	if tbPct(e, pairTUser, old) != 400 || tbPct(e, pairTUser, "a1") != -1 {
		t.Error("reading place did not go back")
	}
	if e.count(`SELECT count(*) FROM highlights WHERE id='hl1' AND book_id=?`, old) != 1 || e.count(`SELECT count(*) FROM bookmarks WHERE id='bm_audio' AND book_id='a1'`) != 1 {
		t.Error("marks: the ebook's went back, the audiobook's stayed")
	}
	if e.count(`SELECT count(*) FROM ebook_not_same WHERE ebook_file_id='f1' AND book_id='a1'`) != 1 {
		t.Error("no not-same row")
	}
	// Neither the engine nor a suggestion brings it back.
	if err := e.svc.PairPending(bg); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if e.file("f1").BookID != old {
		t.Error("the engine merged a split book again")
	}
	if s, err := e.svc.SuggestionsForBook(bg, "a1", 5); err != nil || len(s) != 0 {
		t.Errorf("suggestions = %v, %v", s, err)
	}
}

func TestSplitTwoFiles(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	f1 := tbText(e, "f1", "Two Formats", "9780306406157")
	pairTFile(e, "f2", "ready", "", "Two Formats")
	pairTExec(e, `UPDATE editions SET isbn='9780306406157' WHERE ebook_file_id='f2'`)
	if err := e.svc.PairPending(bg); err != nil { // no audiobook yet: the second format joins the first's text book
		t.Fatal(err)
	}
	if e.file("f2").BookID != f1.BookID {
		t.Fatalf("f2 should join the text book: %+v", e.file("f2"))
	}
	tbAudio(e, "a1", "Two Formats", "9780306406157")
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if e.file("f1").BookID != "a1" || e.file("f2").BookID != "a1" {
		t.Fatalf("both formats should be on the audiobook: %+v %+v", e.file("f1"), e.file("f2"))
	}
	if err := e.svc.Unpair(bg, "f2", "a1", pairTUser); err != nil {
		t.Fatal(err)
	}
	a, b := e.file("f1"), e.file("f2")
	if a.BookID != f1.BookID || b.BookID != f1.BookID || tbKind(e, a.BookID) != "text" {
		t.Fatalf("both files should leave together for %s: %+v %+v", f1.BookID, a, b)
	}
	if !a.BookDefault || b.BookDefault || a.Hold != "person" || b.Hold != "person" {
		t.Errorf("default/hold: %+v %+v", a, b)
	}
	if e.count(`SELECT count(*) FROM ebook_not_same WHERE book_id='a1'`) != 2 {
		t.Error("a not-same row per file")
	}
}

// TestMergeNoWidenLibrary: a member who can read the text book's library, and has nothing in the audiobook's
// library, still cannot reach the audiobook after the two merge across libraries.
func TestMergeNoWidenLibrary(t *testing.T) {
	e := newIEnv(t, false)
	tbUsers(e)
	f := tbText(e, "f1", "Cross", "9780306406157")
	tbOtherLibrary(e)
	pairTExec(e, `INSERT INTO grants(id, user_id, scope, library_id, can_read, created_at, updated_at) VALUES ('g_lib', ?, 'library', ?, 1, 1, 1)`, tbUser2, e.lib.ID)
	member := users.User{ID: tbUser2, Role: users.RoleMember}
	checker := share.New(e.st)
	target := share.Target{Kind: share.TargetBook, ID: "a_other", LibraryID: "lib_2"}
	for _, a := range []share.Action{share.ListenBook, share.ReadText} {
		if checker.Can(bg, member, a, target) {
			t.Fatalf("setup: member can already %s the other library's book", a)
		}
	}
	if err := e.svc.mergePending(bg, "", ""); err != nil {
		t.Fatal(err)
	}
	if !tbMerged(e, f.BookID) {
		t.Fatal("the cross-library merge without data should go through")
	}
	for _, a := range []share.Action{share.ListenBook, share.ReadText} {
		if checker.Can(bg, member, a, target) {
			t.Errorf("after the merge the member can %s the audiobook", a)
		}
	}
}
