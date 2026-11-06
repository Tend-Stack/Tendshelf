package library_test

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

const (
	dupHour = int64(3_600_000)
	dupUser = "us_a"
	dupMem  = "us_b"
)

// dupRepo is a repo with two people and one library folder.
func dupRepo(t *testing.T) (*library.Repo, *db.Store, library.Folder) {
	t.Helper()
	r, st := newRepo(t)
	shelfUser(t, st, dupUser)
	shelfUser(t, st, dupMem)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	return r, st, f
}

func dupAdd(t *testing.T, r *library.Repo, f library.Folder, title string, dur int64) library.Book {
	t.Helper()
	return librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title, DurationMs: dur})
}

func dupExec(t *testing.T, st *db.Store, q string, args ...any) {
	t.Helper()
	if _, err := st.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func dupInt(t *testing.T, st *db.Store, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// dupProgress stores listening progress for a person on a book; at orders the saves (larger is newer).
func dupProgress(t *testing.T, st *db.Store, user, book string, pos, at int64) {
	t.Helper()
	dupExec(t, st, `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, ?, ?, ?, ?, 'device-test-1')`, user, book, pos, 2*dupHour, at, at)
}

func dupHash(c byte) string { return strings.Repeat(string(c), 64) }

// dupFiles replaces a book's audio files with files of the given sizes and hash letters ("size:letter").
func dupFiles(t *testing.T, st *db.Store, bookID string, files ...string) {
	t.Helper()
	dupExec(t, st, `DELETE FROM audio_files WHERE book_id = ?`, bookID)
	for i, f := range files {
		var size int64
		var letter string
		if _, err := fmt.Sscanf(f, "%d:%s", &size, &letter); err != nil {
			t.Fatal(err)
		}
		dupExec(t, st, `INSERT INTO audio_files(id, book_id, path, idx, size, mtime_ns, quick_hash, updated_at) VALUES (?, ?, ?, ?, ?, 1, ?, 1)`,
			fmt.Sprintf("af_%s_%d", bookID, i), bookID, fmt.Sprintf("/x/%s/%d.mp3", bookID, i), i, size, dupHash(letter[0]))
	}
}

// dupTableHandling says what MergeBooks does with every table that has a foreign key to books. A new table
// with such a key fails TestDupEveryBookTableHandled until it is listed here on purpose.
var dupTableHandling = map[string]string{
	// moved onto the kept book
	"progress": "moved", "listening_sessions": "moved", "queue_items": "moved", "book_settings": "moved", "grants": "moved",
	"share_links": "moved", "ebook_files": "moved", "ebook_not_same": "moved", "reading_positions": "moved",
	"reading_sessions": "moved", "highlights": "moved", "bookmarks": "moved", "word_list": "moved", "shelf_items": "moved",
	"text_choices": "moved", "merged_books": "moved", "merged_folders": "moved",
	// the copy's own description and files: they go with it
	"audio_files": "cascades", "chapters": "cascades", "metadata_values": "cascades", "book_people": "cascades",
	"book_series": "cascades", "book_labels": "cascades", "book_sidecars": "cascades", "book_lookups": "cascades",
	"book_not_duplicates": "cascades",
	// the cover moves when the kept book has none; otherwise it goes with the copy
	"covers": "cover",
}

func TestDupEveryBookTableHandled(t *testing.T) {
	_, st, _ := dupRepo(t)
	rows, err := st.Reader().Query(`SELECT m.name FROM sqlite_schema m JOIN pragma_foreign_key_list(m.name) f
WHERE m.type = 'table' AND f."table" = 'books' GROUP BY m.name ORDER BY m.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		found = append(found, n)
		if dupTableHandling[n] == "" {
			t.Errorf("table %s has a foreign key to books and MergeBooks does not handle it: move it or say it cascades", n)
		}
	}
	for n := range dupTableHandling {
		if sort.SearchStrings(found, n) == len(found) || found[sort.SearchStrings(found, n)] != n {
			t.Errorf("table %s is listed but has no foreign key to books", n)
		}
	}
}

func TestDupMergeMovesEverything(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", 2*dupHour), dupAdd(t, r, f, "Drop", 2*dupHour)
	dupProgress(t, st, dupUser, drop.ID, 30*60_000, 100)
	for _, q := range []string{
		`INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_1', 'us_a', '` + drop.ID + `', 5, 1, 1)`,
		`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct) VALUES ('ls_1', 'us_a', '` + drop.ID + `', 'd', 1, 2, 0, 1, 1, 100)`,
		`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_1', 'us_a', 'Mine', 1, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_1', '` + drop.ID + `', 0, 1)`,
		`INSERT INTO queue_items(user_id, book_id, position, added_at) VALUES ('us_a', '` + drop.ID + `', 1, 1)`,
		`INSERT INTO book_settings(user_id, book_id, speed, updated_at) VALUES ('us_a', '` + drop.ID + `', 1.5, 1)`,
		`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_1', 'us_b', 'book', '` + drop.ID + `', 1, 1)`,
		`INSERT INTO share_links(id, token_hash, book_id, created_by, expires_at, created_at) VALUES ('sl_1', randomblob(32), '` + drop.ID + `', 'us_a', 1000, 1)`,
		`INSERT INTO reading_positions(user_id, book_id, chapter_idx, char_pos, percent_bp, client_updated_at, server_updated_at, device_id) VALUES ('us_a', '` + drop.ID + `', 1, 0, 100, 5, 5, 'd')`,
	} {
		dupExec(t, st, q)
	}
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"progress", "bookmarks", "listening_sessions", "shelf_items", "queue_items", "book_settings", "grants", "share_links", "reading_positions"} {
		if n := dupInt(t, st, `SELECT count(*) FROM `+tbl+` WHERE book_id = ?`, keep.ID); n != 1 {
			t.Errorf("%s on the kept book = %d, want 1", tbl, n)
		}
		if n := dupInt(t, st, `SELECT count(*) FROM `+tbl+` WHERE book_id = ?`, drop.ID); n != 0 {
			t.Errorf("%s still on the dropped book = %d", tbl, n)
		}
	}
	if n := dupInt(t, st, `SELECT position_ms FROM progress WHERE book_id = ?`, keep.ID); n != 30*60_000 {
		t.Errorf("position = %d, want it unchanged", n)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM books WHERE id = ?`, drop.ID); n != 0 {
		t.Error("dropped book row is still there")
	}
	if to, ok := r.MergedInto(ctx, drop.ID); !ok || to != keep.ID {
		t.Errorf("MergedInto = %q, %v", to, ok)
	}
	m, err := r.MergedFolders(ctx, drop.LibraryID)
	if err != nil || m[drop.RootPath] != keep.ID || len(m) != 1 {
		t.Errorf("merged folders = %v, %v", m, err)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM audit_log WHERE action = 'book.merge'`); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

func TestDupMergeNewestProgressWinsBothWays(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", 2*dupHour), dupAdd(t, r, f, "Drop", 2*dupHour)
	// us_a: the dropped copy is newer. us_b: the kept copy is newer.
	dupProgress(t, st, dupUser, keep.ID, 1000, 100)
	dupProgress(t, st, dupUser, drop.ID, 2000, 200)
	dupProgress(t, st, dupMem, keep.ID, 3000, 300)
	dupProgress(t, st, dupMem, drop.ID, 4000, 250)
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	if n := dupInt(t, st, `SELECT position_ms FROM progress WHERE user_id = 'us_a' AND book_id = ?`, keep.ID); n != 2000 {
		t.Errorf("us_a position = %d, want the newer 2000", n)
	}
	if n := dupInt(t, st, `SELECT position_ms FROM progress WHERE user_id = 'us_b' AND book_id = ?`, keep.ID); n != 3000 {
		t.Errorf("us_b position = %d, want the newer 3000", n)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM progress`); n != 2 {
		t.Errorf("progress rows = %d, want one per person", n)
	}
}

func TestDupMergeCapsPositionAtKeptLength(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", dupHour), dupAdd(t, r, f, "Drop", 2*dupHour)
	dupProgress(t, st, dupUser, drop.ID, 90*60_000, 100)
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	if n := dupInt(t, st, `SELECT position_ms FROM progress WHERE book_id = ?`, keep.ID); n != dupHour {
		t.Errorf("position = %d, want it capped at %d", n, dupHour)
	}
	if n := dupInt(t, st, `SELECT duration_ms FROM progress WHERE book_id = ?`, keep.ID); n != dupHour {
		t.Errorf("duration = %d, want %d", n, dupHour)
	}
}

func TestDupMergePersonWithDataOnBoth(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", dupHour), dupAdd(t, r, f, "Drop", dupHour)
	for _, q := range []string{
		`INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_k', 'us_a', '` + keep.ID + `', 5, 1, 1)`,
		`INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_d', 'us_a', '` + drop.ID + `', 9, 1, 1)`,
		`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_1', 'us_a', 'Mine', 1, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_1', '` + keep.ID + `', 0, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_1', '` + drop.ID + `', 1, 1)`,
		`INSERT INTO book_settings(user_id, book_id, speed, updated_at) VALUES ('us_a', '` + keep.ID + `', 1.2, 1)`,
		`INSERT INTO book_settings(user_id, book_id, speed, updated_at) VALUES ('us_a', '` + drop.ID + `', 2.0, 1)`,
		`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_k', 'us_b', 'book', '` + keep.ID + `', 1, 1)`,
		`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_d', 'us_b', 'book', '` + drop.ID + `', 1, 1)`,
	} {
		dupExec(t, st, q)
	}
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM bookmarks WHERE user_id = 'us_a' AND book_id = ?`, keep.ID); n != 2 {
		t.Errorf("bookmarks on the kept book = %d, want both", n)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM shelf_items WHERE shelf_id = 'sh_1'`); n != 1 {
		t.Errorf("shelf items = %d, want the book once", n)
	}
	if n := dupInt(t, st, `SELECT speed * 10 FROM book_settings WHERE user_id = 'us_a'`); n != 12 {
		t.Errorf("speed x10 = %d, want the kept book's own 12", n)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM grants WHERE user_id = 'us_b'`); n != 1 {
		t.Errorf("grants = %d, want one", n)
	}
}

func TestDupMergeChain(t *testing.T) {
	r, st, f := dupRepo(t)
	a, b, c := dupAdd(t, r, f, "A", dupHour), dupAdd(t, r, f, "B", dupHour), dupAdd(t, r, f, "C", dupHour)
	dupProgress(t, st, dupUser, c.ID, 777, 100)
	if err := r.MergeBooks(ctx, b.ID, c.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	if err := r.MergeBooks(ctx, a.ID, b.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{b.ID, c.ID} {
		if to, ok := r.MergedInto(ctx, old); !ok || to != a.ID {
			t.Errorf("%s resolves to %q, %v; want %s", old, to, ok, a.ID)
		}
	}
	m, _ := r.MergedFolders(ctx, a.LibraryID)
	if len(m) != 2 || m[b.RootPath] != a.ID || m[c.RootPath] != a.ID {
		t.Errorf("merged folders = %v, want both under %s", m, a.ID)
	}
	if n := dupInt(t, st, `SELECT position_ms FROM progress WHERE book_id = ?`, a.ID); n != 777 {
		t.Errorf("position after the chain = %d", n)
	}
}

func TestDupMergeRefusedPairs(t *testing.T) {
	r, st, f := dupRepo(t)
	a, b := dupAdd(t, r, f, "A", dupHour), dupAdd(t, r, f, "B", dupHour)
	_, other := librarytest.NewLibrary(t, r, "Other", "/other")
	far := dupAdd(t, r, other, "Far", dupHour)
	gone := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", MissingSince: 5})
	text, err := r.CreateTextBook(ctx, library.TextBookIn{LibraryID: f.LibraryID, FolderID: f.ID, RootPath: "/audio/ebook", Title: "Text"})
	if err != nil {
		t.Fatal(err)
	}
	dupExec(t, st, `INSERT INTO file_writes(id, job_id, book_id, audio_file_id, state, original_path, backup_path, temp_path, before_sha256, audio_hash, created_by, created_at, updated_at)
VALUES ('fw_1', 'j', ?, 'af', 'copying', '/o', '/b', '/t', 'x', 'y', 'us_a', 1, 1)`, b.ID)
	cases := []struct {
		name       string
		keep, drop string
		want       error
	}{
		{"same book", a.ID, a.ID, library.ErrMergeSame},
		{"other library", a.ID, far.ID, library.ErrMergeLibrary},
		{"unknown", a.ID, "bk_nope", library.ErrBookNotFound},
		{"text book dropped", a.ID, text, library.ErrBookNotFound},
		{"text book kept", text, a.ID, library.ErrBookNotFound},
		{"kept book cannot be listed", gone.ID, a.ID, library.ErrBookNotFound},
		{"open file write on the dropped book", a.ID, b.ID, library.ErrMergeBusy},
		{"open file write on the kept book", b.ID, a.ID, library.ErrMergeBusy},
	}
	for _, c := range cases {
		if err := r.MergeBooks(ctx, c.keep, c.drop, dupUser); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if n := dupInt(t, st, `SELECT count(*) FROM books`); n != 5 {
		t.Errorf("books = %d, a refused merge changed rows", n)
	}
	// A finished write does not block.
	dupExec(t, st, `UPDATE file_writes SET state = 'confirmed'`)
	if err := r.MergeBooks(ctx, a.ID, b.ID, dupUser); err != nil {
		t.Errorf("merge after the write finished: %v", err)
	}
}

func TestDupMergeCoverMovesWhenKeptHasNone(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", dupHour), dupAdd(t, r, f, "Drop", dupHour)
	dupExec(t, st, `INSERT INTO covers(id, book_id, source, mime, width, height, sha256, path, created_at) VALUES ('cv_d', ?, 'folder', 'image/png', 1, 1, ?, 'p', 1)`, drop.ID, dupHash('c'))
	dupExec(t, st, `UPDATE books SET cover_id = 'cv_d' WHERE id = ?`, drop.ID)
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	var cover sql.NullString
	if err := st.Reader().QueryRow(`SELECT cover_id FROM books WHERE id = ?`, keep.ID).Scan(&cover); err != nil || cover.String != "cv_d" {
		t.Errorf("kept cover = %v, %v", cover, err)
	}
	if n := dupInt(t, st, `SELECT count(*) FROM covers WHERE id = 'cv_d' AND book_id = ?`, keep.ID); n != 1 {
		t.Error("the cover did not move to the kept book")
	}
}

func TestDupRestoreMergedFolder(t *testing.T) {
	r, _, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", dupHour), dupAdd(t, r, f, "Drop", dupHour)
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	ok, err := r.RestoreMergedFolder(ctx, drop.LibraryID, drop.RootPath, dupUser)
	if err != nil || !ok {
		t.Fatalf("restore = %v, %v", ok, err)
	}
	if m, _ := r.MergedFolders(ctx, drop.LibraryID); len(m) != 0 {
		t.Errorf("merged folders after restore = %v", m)
	}
	if ok, _ := r.RestoreMergedFolder(ctx, drop.LibraryID, drop.RootPath, dupUser); ok {
		t.Error("a second restore reported a folder")
	}
}

func TestDupDeletingKeptBookFreesFolder(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", dupHour), dupAdd(t, r, f, "Drop", dupHour)
	if err := r.MergeBooks(ctx, keep.ID, drop.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	dupExec(t, st, `DELETE FROM books WHERE id = ?`, keep.ID)
	if m, _ := r.MergedFolders(ctx, drop.LibraryID); len(m) != 0 {
		t.Errorf("folder still remembered after the kept book was deleted: %v", m)
	}
}
