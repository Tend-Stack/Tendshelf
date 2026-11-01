package library_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

var ctx = context.Background()

func hash(c byte) string { return strings.Repeat(string(c), 64) }

func newRepo(t *testing.T) (*library.Repo, *db.Store) {
	t.Helper()
	st, err := db.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := library.New(st)
	tick := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r.SetNow(func() time.Time { tick = tick.Add(time.Second); return tick })
	return r, st
}

// fixture creates a library with one folder and returns their IDs.
func fixture(t *testing.T, r *library.Repo) (library.Library, library.Folder) {
	t.Helper()
	lib, err := r.CreateLibrary(ctx, "Books", 0)
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.AddFolder(ctx, lib.ID, "/audio")
	if err != nil {
		t.Fatal(err)
	}
	return lib, f
}

func addBook(t *testing.T, r *library.Repo, lib library.Library, f library.Folder, root string) library.Book {
	t.Helper()
	b := library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: root, Title: "T"}
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.InsertBook(&b) }); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateLibraryValidates(t *testing.T) {
	r, _ := newRepo(t)
	cases := []struct {
		name     string
		in       string
		interval int
		wantErr  bool
		wantName string
	}{
		{"plain", "Books", 0, false, "Books"},
		{"trimmed", "  Spoken  ", 60, false, "Spoken"},
		{"empty", "   ", 0, true, ""},
		{"too long", strings.Repeat("é", 201), 0, true, ""},
		{"max length", strings.Repeat("é", 200), 0, false, strings.Repeat("é", 200)},
		{"negative interval", "X", -1, true, ""},
		{"week", "Weekly", 10080, false, "Weekly"},
		{"over a week", "Over", 10081, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, err := r.CreateLibrary(ctx, c.in, c.interval)
			if c.wantErr {
				if !errors.Is(err, library.ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil || l.Name != c.wantName || !strings.HasPrefix(l.ID, "lib_") || l.CreatedAt == 0 {
				t.Fatalf("library = %+v, %v", l, err)
			}
			got, err := r.Library(ctx, l.ID)
			if err != nil || got != l {
				t.Fatalf("read back = %+v, %v", got, err)
			}
		})
	}
	libs, err := r.Libraries(ctx)
	if err != nil || len(libs) != 4 {
		t.Fatalf("Libraries = %d, %v", len(libs), err)
	}
	if _, err := r.Library(ctx, "lib_nope"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("missing library err = %v", err)
	}
}

func TestAddFolderOverlap(t *testing.T) {
	r, _ := newRepo(t)
	a, err := r.CreateLibrary(ctx, "A", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.CreateLibrary(ctx, "B", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddFolder(ctx, a.ID, "/a/book/"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		lib  string
		path string
		want error
	}{
		{"equal", a.ID, "/a/book", library.ErrFolderOverlap},
		{"equal other library", b.ID, "/a/book", library.ErrFolderOverlap},
		{"equal unclean", a.ID, "/a/./x/../book", library.ErrFolderOverlap},
		{"parent", b.ID, "/a", library.ErrFolderOverlap},
		{"root", b.ID, "/", library.ErrFolderOverlap},
		{"child", b.ID, "/a/book/part1", library.ErrFolderOverlap},
		{"sibling with shared prefix", a.ID, "/a/books", nil},
		{"shorter sibling", a.ID, "/a/boo", nil},
		{"relative", a.ID, "a/other", library.ErrInvalid},
		{"empty", a.ID, "", library.ErrInvalid},
		{"unknown library", "lib_nope", "/z", library.ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := r.AddFolder(ctx, c.lib, c.path)
			if c.want == nil {
				if err != nil || f.Path != strings.TrimSuffix(c.path, "/") {
					t.Fatalf("AddFolder = %+v, %v", f, err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	fs, err := r.Folders(ctx, a.ID)
	if err != nil || len(fs) != 3 || fs[0].Path != "/a/boo" || fs[1].Path != "/a/book" || fs[2].Path != "/a/books" {
		t.Fatalf("Folders = %+v, %v", fs, err)
	}
	got, err := r.Folder(ctx, fs[1].ID)
	if err != nil || got != fs[1] {
		t.Fatalf("Folder = %+v, %v", got, err)
	}
}

func TestBookRoundTrip(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)

	// Unknowns are stored as NULL, not as zero values.
	min := addBook(t, r, lib, f, "/audio/min")
	var year, missing sql.NullInt64
	var coverS, refusedS sql.NullString
	if err := st.Reader().QueryRow(`SELECT published_year, cover_id, refused_reason, missing_since FROM books WHERE id = ?`, min.ID).
		Scan(&year, &coverS, &refusedS, &missing); err != nil {
		t.Fatal(err)
	}
	if year.Valid || coverS.Valid || refusedS.Valid || missing.Valid {
		t.Fatalf("expected NULLs, got year=%v cover=%v refused=%v missing=%v", year, coverS, refusedS, missing)
	}
	if !strings.HasPrefix(min.ID, "bk_") || min.AddedAt == 0 || min.AddedAt != min.UpdatedAt {
		t.Fatalf("book = %+v", min)
	}

	full := library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: "/audio/full", Title: "Full", SortTitle: "full",
		Subtitle: "Sub", Description: "D", Publisher: "P", PublishedYear: 1999, Language: "en", ISBN: "1", ASIN: "B0",
		Abridged: true, Explicit: true, DurationMs: 123456, RefusedReason: library.RefusedDamaged}
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.InsertBook(&full) }); err != nil {
		t.Fatal(err)
	}
	got, err := r.Book(ctx, full.ID)
	if err != nil || got != full {
		t.Fatalf("round trip:\n got %+v\nwant %+v\nerr %v", got, full, err)
	}
	byPath, err := r.BookByRootPath(ctx, "/audio/full")
	if err != nil || byPath.ID != full.ID {
		t.Fatalf("BookByRootPath = %+v, %v", byPath, err)
	}
	if _, err := r.BookByRootPath(ctx, "/audio/none"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}

	// UpdateBook changes details, keeps added_at, moves updated_at.
	full.Title, full.PublishedYear, full.RefusedReason = "Renamed", 0, ""
	before := full.UpdatedAt
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.UpdateBook(full) }); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Book(ctx, full.ID)
	if got.Title != "Renamed" || got.PublishedYear != 0 || got.RefusedReason != "" || got.AddedAt != full.AddedAt || got.UpdatedAt <= before {
		t.Fatalf("after update = %+v", got)
	}
	if err := st.Reader().QueryRow(`SELECT published_year FROM books WHERE id = ?`, full.ID).Scan(&year); err != nil || year.Valid {
		t.Fatalf("year after clearing = %v, %v", year, err)
	}

	books, err := r.BooksInFolder(ctx, f.ID)
	if err != nil || len(books) != 2 || books[0].RootPath != "/audio/full" || books[1].RootPath != "/audio/min" {
		t.Fatalf("BooksInFolder = %+v, %v", books, err)
	}

	// Validation and constraints.
	bad := []struct {
		name string
		b    library.Book
		want error
	}{
		{"relative root", library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: "x"}, library.ErrInvalid},
		{"bad refused reason", library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: "/audio/r", RefusedReason: "x"}, library.ErrInvalid},
		{"duplicate root", library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: "/audio/min"}, library.ErrConflict},
		{"unknown folder", library.Book{LibraryID: lib.ID, FolderID: "lf_nope", RootPath: "/audio/u"}, library.ErrNotFound},
		{"folder of another library", library.Book{LibraryID: "lib_other", FolderID: f.ID, RootPath: "/audio/o"}, library.ErrInvalid},
	}
	for _, c := range bad {
		b := c.b
		err := r.Update(ctx, func(tx *library.Tx) error { return tx.InsertBook(&b) })
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestUpdateBookNotFound(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	err := r.Update(ctx, func(tx *library.Tx) error {
		return tx.UpdateBook(library.Book{ID: "bk_nope", LibraryID: lib.ID, FolderID: f.ID, RootPath: "/audio/x"})
	})
	if !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	err = r.Update(ctx, func(tx *library.Tx) error { return tx.SetBookMissing("bk_nope", 5) })
	if !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("SetBookMissing err = %v", err)
	}
}

func file(bookID, path string, idx int) *library.AudioFile {
	return &library.AudioFile{BookID: bookID, Path: path, Idx: idx, Size: 100, MtimeNs: 5, QuickHash: hash('a'), Container: "mp4", Codec: "aac"}
}

func TestPutAudioFileUpsert(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b")

	a := file(b.ID, "/audio/b/1.m4b", 0)
	a.ProbeJSON = []byte(`{"v":1}`)
	a.SHA256 = hash('c')
	put := func(f *library.AudioFile) error {
		return r.Update(ctx, func(tx *library.Tx) error { return tx.PutAudioFile(f) })
	}
	if err := put(a); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.ID, "af_") || a.UpdatedAt == 0 {
		t.Fatalf("file = %+v", a)
	}
	got, err := r.AudioFiles(ctx, b.ID)
	if err != nil || len(got) != 1 || string(got[0].ProbeJSON) != `{"v":1}` || got[0].SHA256 != hash('c') || got[0].Path != a.Path {
		t.Fatalf("AudioFiles = %+v, %v", got, err)
	}

	// The same ID with a new path is a move, not a new row.
	moved := *a
	moved.Path, moved.Size, moved.ProbeJSON, moved.SHA256 = "/audio/b/moved.m4b", 200, nil, ""
	if err := put(&moved); err != nil {
		t.Fatal(err)
	}
	got, _ = r.AudioFiles(ctx, b.ID)
	if len(got) != 1 || got[0].ID != a.ID || got[0].Path != moved.Path || got[0].Size != 200 || got[0].ProbeJSON != nil || got[0].SHA256 != "" {
		t.Fatalf("after move = %+v", got)
	}
	var probe, sum sql.NullString
	if err := st.Reader().QueryRow(`SELECT probe_json, sha256 FROM audio_files WHERE id = ?`, a.ID).Scan(&probe, &sum); err != nil || probe.Valid || sum.Valid {
		t.Fatalf("expected NULL probe_json and sha256: %v %v %v", probe, sum, err)
	}

	// Another file may not take a path in use.
	clash := file(b.ID, moved.Path, 1)
	if err := put(clash); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("path clash err = %v", err)
	}
	for name, mod := range map[string]func(*library.AudioFile){
		"relative path": func(f *library.AudioFile) { f.Path = "x.mp3" },
		"short hash":    func(f *library.AudioFile) { f.QuickHash = "abc" },
		"bad probe":     func(f *library.AudioFile) { f.ProbeJSON = []byte("{") },
		"negative size": func(f *library.AudioFile) { f.Size = -1 },
	} {
		c := file(b.ID, "/audio/b/2.mp3", 1)
		mod(c)
		if err := put(c); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := put(file("bk_nope", "/audio/zz.mp3", 0)); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown book err = %v", err)
	}
}

func TestMissingKept(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b")
	a1, a2 := file(b.ID, "/audio/b/1.mp3", 0), file(b.ID, "/audio/b/2.mp3", 1)
	err := r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.PutAudioFile(a1); err != nil {
			return err
		}
		return tx.PutAudioFile(a2)
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := func() int64 { g, _ := r.Book(ctx, b.ID); return g.UpdatedAt }
	before := updated()
	err = r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.SetAudioFileMissing(a2.ID, 777); err != nil {
			return err
		}
		return tx.SetBookMissing(b.ID, 888)
	})
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := r.AudioFiles(ctx, b.ID)
	g, _ := r.Book(ctx, b.ID)
	if len(fs) != 2 || fs[0].MissingSince != 0 || fs[1].MissingSince != 777 || g.MissingSince != 888 || g.UpdatedAt != before {
		t.Fatalf("files = %+v book = %+v", fs, g)
	}
	inFolder, err := r.BooksInFolder(ctx, f.ID)
	if err != nil || len(inFolder) != 1 {
		t.Fatalf("a missing book must still be listed for the scan: %+v, %v", inFolder, err)
	}
	// 0 clears.
	err = r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.SetAudioFileMissing(a2.ID, 0); err != nil {
			return err
		}
		return tx.SetBookMissing(b.ID, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	fs, _ = r.AudioFiles(ctx, b.ID)
	g, _ = r.Book(ctx, b.ID)
	if fs[1].MissingSince != 0 || g.MissingSince != 0 {
		t.Fatalf("not cleared: %+v %+v", fs, g)
	}
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.SetAudioFileMissing("af_nope", 1) }); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestReplaceChapters(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b")
	set := func(chs []library.Chapter) error {
		return r.Update(ctx, func(tx *library.Tx) error { return tx.ReplaceChapters(b.ID, chs) })
	}
	first := []library.Chapter{
		{StartMs: 0, EndMs: 1000, Title: "One", Source: library.ChapterFile},
		{StartMs: 1000, EndMs: 1000, Title: "", Source: library.ChapterFile},
		{StartMs: 1000, EndMs: 3000, Title: "Three", Source: library.ChapterFile},
	}
	if err := set(first); err != nil {
		t.Fatal(err)
	}
	got, err := r.Chapters(ctx, b.ID)
	if err != nil || len(got) != 3 {
		t.Fatalf("Chapters = %+v, %v", got, err)
	}
	for i, c := range got {
		want := first[i]
		want.Idx = i
		if c != want {
			t.Errorf("chapter %d = %+v, want %+v", i, c, want)
		}
	}
	for name, chs := range map[string][]library.Chapter{
		"end before start": {{StartMs: 5, EndMs: 4, Source: library.ChapterFile}},
		"bad source":       {{StartMs: 0, EndMs: 4, Source: "guess"}},
		"negative start":   {{StartMs: -1, EndMs: 4, Source: library.ChapterFile}},
	} {
		if err := set(chs); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// An error anywhere in Update rolls back the earlier writes of the same Update.
	boom := errors.New("boom")
	err = r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.ReplaceChapters(b.ID, []library.Chapter{{StartMs: 0, EndMs: 9, Title: "New", Source: library.ChapterUser}}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	got, _ = r.Chapters(ctx, b.ID)
	if len(got) != 3 || got[0].Title != "One" {
		t.Fatalf("rollback failed: %+v", got)
	}
	// A failing replace after a delete leaves the old list in place.
	if err := set(nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.Chapters(ctx, b.ID); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.ReplaceChapters("bk_nope", nil) }); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("unknown book err = %v", err)
	}
}

func TestAudioFilesByQuickHashScoped(t *testing.T) {
	r, _ := newRepo(t)
	libA, fA := fixture(t, r)
	libB, err := r.CreateLibrary(ctx, "Other", 0)
	if err != nil {
		t.Fatal(err)
	}
	fB, err := r.AddFolder(ctx, libB.ID, "/other")
	if err != nil {
		t.Fatal(err)
	}
	bA := addBook(t, r, libA, fA, "/audio/b")
	bB := addBook(t, r, libB, fB, "/other/b")
	err = r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.PutAudioFile(file(bA.ID, "/audio/b/1.mp3", 0)); err != nil {
			return err
		}
		x := file(bA.ID, "/audio/b/2.mp3", 1)
		x.QuickHash = hash('d')
		if err := tx.PutAudioFile(x); err != nil {
			return err
		}
		return tx.PutAudioFile(file(bB.ID, "/other/b/1.mp3", 0))
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.AudioFilesByQuickHash(ctx, libA.ID, hash('a'))
	if err != nil || len(got) != 1 || got[0].Path != "/audio/b/1.mp3" {
		t.Fatalf("scoped = %+v, %v", got, err)
	}
	if got, _ = r.AudioFilesByQuickHash(ctx, libB.ID, hash('a')); len(got) != 1 || got[0].Path != "/other/b/1.mp3" {
		t.Fatalf("other library = %+v", got)
	}
	if got, _ = r.AudioFilesByQuickHash(ctx, libA.ID, hash('z')); len(got) != 0 {
		t.Fatalf("unknown hash = %+v", got)
	}
	inFolder, err := r.AudioFilesInFolder(ctx, fA.ID)
	if err != nil || len(inFolder) != 2 || inFolder[0].Idx != 0 || inFolder[1].Idx != 1 {
		t.Fatalf("AudioFilesInFolder = %+v, %v", inFolder, err)
	}
}

func TestCascadeDeletes(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b")
	err := r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.PutAudioFile(file(b.ID, "/audio/b/1.mp3", 0)); err != nil {
			return err
		}
		return tx.ReplaceChapters(b.ID, []library.Chapter{{StartMs: 0, EndMs: 1, Source: library.ChapterFile}})
	})
	if err != nil {
		t.Fatal(err)
	}
	// Rows no writer exists for yet: links, a cover the book points at, metadata values.
	for _, s := range []string{
		`INSERT INTO people(id, name, created_at) VALUES ('ps_1', 'Ana', 1)`,
		`INSERT INTO book_people(book_id, person_id, role) VALUES ('` + b.ID + `', 'ps_1', 'author')`,
		`INSERT INTO series(id, name, created_at) VALUES ('sr_1', 'Saga', 1)`,
		`INSERT INTO book_series(book_id, series_id) VALUES ('` + b.ID + `', 'sr_1')`,
		`INSERT INTO book_labels(book_id, kind, value) VALUES ('` + b.ID + `', 'tag', 'x')`,
		`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES ('` + b.ID + `', 'title', 'tags', '"T"', 1)`,
		`INSERT INTO covers(id, book_id, source, mime, width, height, sha256, path, created_at) VALUES ('cv_1', '` + b.ID + `', 'folder', 'image/png', 1, 1, '` + hash('e') + `', 'p', 1)`,
		`UPDATE books SET cover_id = 'cv_1' WHERE id = '` + b.ID + `'`,
	} {
		if _, err := st.Writer().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if _, err := st.Writer().Exec(`DELETE FROM libraries WHERE id = ?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"library_folders", "books", "audio_files", "chapters", "book_people", "book_series",
		"book_labels", "metadata_values", "covers"} {
		var n int
		if err := st.Reader().QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s has %d rows after deleting the library (%v)", tbl, n, err)
		}
	}
	// People and series are shared across libraries and stay.
	var n int
	if err := st.Reader().QueryRow(`SELECT (SELECT count(*) FROM people) + (SELECT count(*) FROM series)`).Scan(&n); err != nil || n != 2 {
		t.Errorf("people and series = %d, %v", n, err)
	}
	rows, err := st.Writer().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check reports a violation")
	}
}
