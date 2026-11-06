package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func mustOpen(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func embedded(t *testing.T) []migration {
	t.Helper()
	ms, err := loadMigrations(embeddedMigrations)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func rawHeader(t *testing.T, path string) Header {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return Header{
		ApplicationID: int32(binary.BigEndian.Uint32(b[68:72])),
		UserVersion:   int(int32(binary.BigEndian.Uint32(b[60:64]))),
	}
}

func pragmaInt(t *testing.T, d *sql.DB, name string) int {
	t.Helper()
	var v int
	if err := d.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func countRows(t *testing.T, d *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := d.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedAll inserts one row in each table of schema version 1.
func seedAll(t *testing.T, st *Store) {
	t.Helper()
	stmts := []string{
		`INSERT INTO settings(key, value_json) VALUES ('k', '{"a":1}')`,
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at) VALUES (zeroblob(32), 'us_1', 1, 2, 1)`,
		`INSERT INTO user_settings(user_id, key, value_json, updated_at) VALUES ('us_1', 'theme', '"dark"', 1)`,
		`INSERT INTO jobs(id, kind, state, created_at, updated_at) VALUES ('jb_1', 'x', 'queued', 1, 1)`,
		`INSERT INTO audit_log(id, at, action) VALUES ('au_1', 1, 'login')`,
	}
	for _, s := range stmts {
		if _, err := st.Writer().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

var allTables = []string{"settings", "users", "sessions", "user_settings", "jobs", "audit_log"}

// libraryTables are the tables migration 0002 adds.
var libraryTables = []string{"libraries", "library_folders", "books", "audio_files", "chapters", "metadata_values",
	"people", "book_people", "series", "book_series", "book_labels", "covers"}

// searchTables are the tables migration 0004 adds, with the FTS5 shadow tables of the contentless index
// (a contentless table has no _content table).
var searchTables = []string{"books_fts_ids", "books_fts", "books_fts_vocab",
	"books_fts_data", "books_fts_idx", "books_fts_docsize", "books_fts_config"}

// shelfTables are the tables migration 0005 adds.
var shelfTables = []string{"shelves", "shelf_items"}

// progressTables are the tables migration 0006 adds.
var progressTables = []string{"progress", "bookmarks", "listening_sessions"}

// familyTables are the tables migration 0007 adds.
var familyTables = []string{"grants", "invites", "share_links", "devices"}

// seedFamily adds one row to every family table: a library grant, an invite, a share link and a device.
func seedFamily(t *testing.T, st *Store) {
	t.Helper()
	stmts := []string{
		`INSERT INTO grants(id, user_id, scope, library_id, created_by, created_at, updated_at, expires_at) VALUES ('gr_1', 'us_1', 'library', 'lib_1', 'us_1', 1, 1, 2)`,
		`INSERT INTO invites(id, token_hash, created_by, name, role, created_at, updated_at, expires_at) VALUES ('in_1', randomblob(32), 'us_1', 'Rosa', 'member', 1, 1, 2)`,
		`INSERT INTO share_links(id, token_hash, book_id, created_by, created_at, expires_at) VALUES ('sl_1', randomblob(32), 'bk_1', 'us_1', 1, 2)`,
		`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('dv_1', 'us_1', 'Phone', 'mobile', randomblob(32), 1)`,
	}
	for _, s := range stmts {
		if _, err := st.Writer().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// seedProgress adds one progress row, one bookmark and one session of the seeded owner and book.
func seedProgress(t *testing.T, st *Store) {
	t.Helper()
	stmts := []string{
		`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id) VALUES ('us_1', 'bk_1', 5, 10, 1, 1, 'dev_test_1')`,
		`INSERT INTO bookmarks(id, user_id, book_id, position_ms, note, created_at, updated_at) VALUES ('bm_1', 'us_1', 'bk_1', 5, 'n', 1, 1)`,
		`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct) VALUES ('ls_1', 'us_1', 'bk_1', 'dev_test_1', 1, 2, 0, 5, 1, 100)`,
	}
	for _, s := range stmts {
		if _, err := st.Writer().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// seedShelves adds one shelf of the seeded owner holding the seeded book.
func seedShelves(t *testing.T, st *Store) {
	t.Helper()
	stmts := []string{
		`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('shf_1', 'us_1', 'Sleep', 1, 1)`,
		`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('shf_1', 'bk_1', 0, 1)`,
	}
	for _, s := range stmts {
		if _, err := st.Writer().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// seedLibrary adds one row to every library table, parents first, then points the book at its cover.
func seedLibrary(t *testing.T, st *Store) {
	t.Helper()
	stmts := []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		`INSERT INTO audio_files(id, book_id, path, idx, size, mtime_ns, quick_hash, updated_at) VALUES ('af_1', 'bk_1', '/audio/a/1.m4b', 0, 10, 1, '` + strings.Repeat("a", 64) + `', 1)`,
		`INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES ('bk_1', 0, 0, 10, 'One', 'file')`,
		`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES ('bk_1', 'title', 'tags', '"A"', 1)`,
		`INSERT INTO people(id, name, created_at) VALUES ('ps_1', 'Ana', 1)`,
		`INSERT INTO book_people(book_id, person_id, role) VALUES ('bk_1', 'ps_1', 'author')`,
		`INSERT INTO series(id, name, created_at) VALUES ('sr_1', 'Saga', 1)`,
		`INSERT INTO book_series(book_id, series_id, sequence) VALUES ('bk_1', 'sr_1', '1')`,
		`INSERT INTO book_labels(book_id, kind, value) VALUES ('bk_1', 'genre', 'Fantasy')`,
		`INSERT INTO covers(id, book_id, source, mime, width, height, sha256, path, created_at) VALUES ('cv_1', 'bk_1', 'embedded', 'image/jpeg', 64, 64, '` + strings.Repeat("b", 64) + `', 'cv_1.jpg', 1)`,
		`UPDATE books SET cover_id = 'cv_1' WHERE id = 'bk_1'`,
	}
	for _, s := range stmts {
		if _, err := st.Writer().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func TestOpenFresh(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	defer st.Close()

	if st.SchemaVersion() != SchemaVersion || st.Path() != FilePath(dir) {
		t.Fatalf("store = (%d, %q)", st.SchemaVersion(), st.Path())
	}
	if got := pragmaInt(t, st.Writer(), "user_version"); got != SchemaVersion {
		t.Fatalf("user_version = %d", got)
	}
	if got := pragmaInt(t, st.Writer(), "application_id"); int32(got) != ApplicationID {
		t.Fatalf("application_id = %#x", got)
	}
	var mode string
	if err := st.Writer().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	for name, d := range map[string]*sql.DB{"writer": st.Writer(), "reader": st.Reader()} {
		if got := pragmaInt(t, d, "foreign_keys"); got != 1 {
			t.Errorf("%s foreign_keys = %d", name, got)
		}
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n); err != nil || n != len(allTables)+len(libraryTables)+len(searchTables)+len(shelfTables)+len(progressTables)+len(familyTables)+20 { // +15: book_sidecars, online_cache (0010), file_writes (0011), book_lookups, online_usage (0012), queue_items (0013), uploads, upload_files, upload_paths (0014), ebook_files, editions, edition_chapters (0016), ebook_not_same, text_choices (0017), highlights (0019), merged_books, reading_positions, reading_sessions (0021), word_list (0022), book_settings (0024); 0018's runtime_components, alignments and alignment_chapters are dropped again by 0020
		t.Fatalf("tables = %d, %v", n, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(FilePath(dir))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", fi.Mode(), err)
		}
	}
	if exists(filepath.Join(dir, "backups")) {
		t.Fatal("a fresh database must not create backups/")
	}
}

func TestOpenEmptyFixture(t *testing.T) {
	dir := t.TempDir()
	// A valid SQLite file at version 0 with no tables.
	old, err := open(context.Background(), dir, openConfig{supported: 0, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	if h := rawHeader(t, FilePath(dir)); h != (Header{}) {
		t.Fatalf("fixture header = %+v", h)
	}
	st := mustOpen(t, dir)
	defer st.Close()
	if st.SchemaVersion() != SchemaVersion {
		t.Fatalf("version = %d", st.SchemaVersion())
	}
	if exists(filepath.Join(dir, "backups")) {
		t.Fatal("version 0 must not be backed up")
	}
}

func TestOpenZeroByteFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(FilePath(dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpen(t, dir)
	defer st.Close()
	if st.SchemaVersion() != SchemaVersion || countRows(t, st.Reader(), "jobs") != 0 {
		t.Fatal("zero-byte file was not initialised")
	}
}

// seeds holds one seed function per schema version: seeds[v] fills a database
// that is at version v.
var seeds = map[int]func(t *testing.T, st *Store){
	1: seedAll,
	2: func(t *testing.T, st *Store) { seedAll(t, st); seedLibrary(t, st) },
	3: seed3,
	4: seed3,
	5: func(t *testing.T, st *Store) { seed3(t, st); seedShelves(t, st) },
	6: func(t *testing.T, st *Store) { seed3(t, st); seedShelves(t, st); seedProgress(t, st) },
	7: seed7,
	8: seed7,
	9: seed7,
}

// seed7 fills a version 7 (and 8) database.
func seed7(t *testing.T, st *Store) {
	seed3(t, st)
	seedShelves(t, st)
	seedProgress(t, st)
	seedFamily(t, st)
}

// seed3 fills a version 3 database (version 4 adds only derived tables, so it seeds the same rows).
func seed3(t *testing.T, st *Store) {
	seedAll(t, st)
	seedLibrary(t, st)
	if _, err := st.Writer().Exec(`UPDATE audio_files SET probe_error = 'not_audio'`); err != nil {
		t.Fatal(err)
	}
}

// tablesAt lists the tables that exist, and hold one seeded row, at each version.
var tablesAt = map[int][]string{
	1:  allTables,
	2:  append(append([]string{}, allTables...), libraryTables...),
	3:  append(append([]string{}, allTables...), libraryTables...),
	4:  append(append(append([]string{}, allTables...), libraryTables...), "books_fts_ids"),
	5:  append(append(append(append([]string{}, allTables...), libraryTables...), "books_fts_ids"), shelfTables...),
	6:  append(append(append(append(append([]string{}, allTables...), libraryTables...), "books_fts_ids"), shelfTables...), progressTables...),
	7:  append(append(append(append(append(append([]string{}, allTables...), libraryTables...), "books_fts_ids"), shelfTables...), progressTables...), familyTables...),
	8:  nil, // 0008 adds a column and an index only; filled in init below
	9:  nil, // 0009 adds a column only; filled in init below
	10: nil, // 0010 adds tables that start empty; filled in init below
}

func TestMigrateFromPreviousFixtures(t *testing.T) {
	ms := embedded(t)
	for v := 1; v <= SchemaVersion; v++ {
		t.Run(ms[v-1].Name, func(t *testing.T) {
			dir := t.TempDir()
			// The fixture at v-1 is built from the pinned, released migrations.
			prev, err := open(context.Background(), dir, openConfig{migrations: ms[:v-1], supported: v - 1, now: time.Now})
			if err != nil {
				t.Fatal(err)
			}
			if seed := seeds[v-1]; seed != nil {
				seed(t, prev)
			}
			prev.Close()

			st := mustOpen(t, dir)
			defer st.Close()
			if st.SchemaVersion() < v {
				t.Fatalf("version = %d, want >= %d", st.SchemaVersion(), v)
			}
			if v-1 > 0 {
				if seeds[v-1] != nil {
					for _, tbl := range tablesAt[v-1] {
						if countRows(t, st.Reader(), tbl) == 0 {
							t.Errorf("%s lost its rows", tbl)
						}
					}
				}
				bks, _ := filepath.Glob(filepath.Join(dir, "backups", "tendshelf-v*.db"))
				if len(bks) != 1 {
					t.Fatalf("backups = %v", bks)
				}
			}
		})
	}
}

func TestDataSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	seedAll(t, st)
	seedLibrary(t, st)
	seedShelves(t, st)
	seedProgress(t, st)
	seedFamily(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = mustOpen(t, dir)
	defer st.Close()
	for _, tbl := range tablesAt[SchemaVersion] {
		if n := countRows(t, st.Reader(), tbl); n != 1 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	if exists(filepath.Join(dir, "backups")) {
		t.Fatal("reopening at the current version must not back up")
	}
}

func assertRefused(t *testing.T, dir string, want error) *SchemaError {
	t.Helper()
	path := FilePath(dir)
	before := hashFile(t, path)
	_, err := Open(context.Background(), dir)
	if !errors.Is(err, want) {
		t.Fatalf("Open = %v, want %v", err, want)
	}
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("not a *SchemaError: %#v", err)
	}
	if hashFile(t, path) != before {
		t.Fatal("refused database changed")
	}
	for _, side := range []string{path + "-wal", path + "-shm", filepath.Join(dir, "backups")} {
		if exists(side) {
			t.Fatalf("%s was created", side)
		}
	}
	return se
}

func setRawHeader(t *testing.T, path string, off int, v uint32) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	if _, err := f.WriteAt(b[:], int64(off)); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRefusesNewerSchemaByteIdentical(t *testing.T) {
	dir := t.TempDir()
	mustOpen(t, dir).Close()
	path := FilePath(dir)
	if exists(path+"-wal") || exists(path+"-shm") {
		t.Fatal("close left side files behind")
	}
	setRawHeader(t, path, 60, SchemaVersion+1)
	se := assertRefused(t, dir, ErrSchemaTooNew)
	if se.Found != SchemaVersion+1 {
		t.Fatalf("Found = %d", se.Found)
	}
}

func TestOpenRefusesNewerSchemaInWAL(t *testing.T) {
	src := t.TempDir()
	st := mustOpen(t, src)
	defer st.Close()
	// Move the v1 migration into the main file, then leave v99 only in the WAL.
	if _, err := st.Writer().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(FilePath(src) + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(FilePath(dir)+suffix, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := FilePath(dir)
	if h := rawHeader(t, path); h.UserVersion != SchemaVersion {
		t.Fatalf("raw header has v%d; the newer version must live only in the WAL", h.UserVersion)
	}
	dbHash, walHash := hashFile(t, path), hashFile(t, path+"-wal")
	_, err := Open(context.Background(), dir)
	var se *SchemaError
	if !errors.Is(err, ErrSchemaTooNew) || !errors.As(err, &se) || se.Found != 99 {
		t.Fatalf("Open = %v", err)
	}
	if hashFile(t, path) != dbHash || hashFile(t, path+"-wal") != walHash {
		t.Fatal("refused database or WAL changed")
	}
	if exists(filepath.Join(dir, "backups")) {
		t.Fatal("backups/ created")
	}
}

func TestOpenRefusesForeignApplicationID(t *testing.T) {
	dir := t.TempDir()
	mustOpen(t, dir).Close()
	setRawHeader(t, FilePath(dir), 68, 0x12345678)
	assertRefused(t, dir, ErrNotTendshelf)
}

func TestOpenRefusesNonSQLiteFile(t *testing.T) {
	for name, content := range map[string]string{
		"text":  strings.Repeat("this is not a database\n", 20),
		"short": "SQLite",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(FilePath(dir), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			assertRefused(t, dir, ErrNotTendshelf)
		})
	}
}

func extraMigration() migration {
	return migration{Version: SchemaVersion + 1, Name: "test", SQL: "CREATE TABLE extra (x INTEGER) STRICT;"}
}

func TestBackupBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	st := mustOpen(t, dir)
	seedAll(t, st)
	st.Close()

	now := time.Date(2026, 11, 1, 10, 0, 0, 123_000_000, time.UTC)
	cfg := openConfig{migrations: append(embedded(t), extraMigration()), supported: SchemaVersion + 1, now: func() time.Time { return now }}
	st2, err := open(context.Background(), dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if st2.SchemaVersion() != SchemaVersion+1 {
		t.Fatalf("version = %d", st2.SchemaVersion())
	}
	bk := filepath.Join(dir, "backups", fmt.Sprintf("tendshelf-v%d-20261101T100000.123Z.db", SchemaVersion))
	if !exists(bk) {
		ents, _ := os.ReadDir(filepath.Join(dir, "backups"))
		t.Fatalf("backup %s missing; have %v", bk, ents)
	}
	if h := rawHeader(t, bk); h != (Header{ApplicationID, SchemaVersion}) {
		t.Fatalf("backup header = %+v", h)
	}
	if err := verifyBackup(context.Background(), bk, SchemaVersion); err != nil {
		t.Fatal(err)
	}
	d, err := sql.Open("sqlite", roDSN(bk))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var name string
	if err := d.QueryRow("SELECT username FROM users").Scan(&name); err != nil || name != "Ana" {
		t.Fatalf("backup row = %q, %v", name, err)
	}
}

func TestBackupRotationKeepsNewestThree(t *testing.T) {
	dir := t.TempDir()
	mustOpen(t, dir).Close()
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	fakes := []string{
		fmt.Sprintf("tendshelf-v%d-20250101T000000.000Z.db", SchemaVersion),
		fmt.Sprintf("tendshelf-v%d-20250102T000000.000Z.db", SchemaVersion),
		fmt.Sprintf("tendshelf-v%d-20250103T000000.000Z.db", SchemaVersion),
		fmt.Sprintf("tendshelf-v%d-20250104T000000.000Z.db", SchemaVersion),
	}
	for _, f := range append(fakes, "notes.txt") {
		if err := os.WriteFile(filepath.Join(bdir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := openConfig{migrations: append(embedded(t), extraMigration()), supported: SchemaVersion + 1, now: func() time.Time { return now }}
	st, err := open(context.Background(), dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	ents, _ := os.ReadDir(bdir)
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	want := []string{
		"notes.txt",
		fmt.Sprintf("tendshelf-v%d-20250103T000000.000Z.db", SchemaVersion),
		fmt.Sprintf("tendshelf-v%d-20250104T000000.000Z.db", SchemaVersion),
		fmt.Sprintf("tendshelf-v%d-20260101T000000.000Z.db", SchemaVersion),
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("backups = %v, want %v", got, want)
	}
}

func TestBackupFailureLeavesDatabaseUnmigrated(t *testing.T) {
	dir := t.TempDir()
	mustOpen(t, dir).Close()
	if err := os.WriteFile(filepath.Join(dir, "backups"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := openConfig{migrations: append(embedded(t), extraMigration()), supported: SchemaVersion + 1, now: time.Now}
	if _, err := open(context.Background(), dir, cfg); err == nil {
		t.Fatal("open must fail when the backup cannot be written")
	}
	// The same database still opens at its own version, unmigrated.
	st := mustOpen(t, dir)
	defer st.Close()
	if st.SchemaVersion() != SchemaVersion || pragmaInt(t, st.Writer(), "user_version") != SchemaVersion {
		t.Fatalf("version = %d", st.SchemaVersion())
	}
}

// Released migrations must never change: pin their hashes. Add a line when a
// migration is released; never edit an existing one.
var pinnedMigrations = map[int]string{
	1:  "0e70eb5003c239205ad0afda87e019d8983e64fddce9100222314b1075bcfaaa",
	2:  "bf7ece0acfd14ae9dc766e8e4c10d4507ad8ea4b0a50ccf1db01d62ab2f5de60",
	3:  "8ebdf61fb5ed156385ecf3fea7a7f82ba0275c409a88e2824aec5a8c9f0f5a6a",
	4:  "c0424f4fb4e9c40d8863a0b1c3bcb634233f900f01e458733309816beec8e339",
	5:  "1578816fe1e33952a8c5b79a2d00152c989c1d31d618216f4fc56296f2b50c97",
	6:  "9d83b1c2c0c7c0a4f86f02e82cccb5f6d66cb1b016a9b01a2dedc5a25c25ac45",
	7:  "94d2e5b344c393f29dbaafd56c9c5885e263f80e9f84d4f04184c91f473fd046",
	8:  "bcbac9ebb7f522de507ae72a6f6ebd31b21214d43444f3b0ee6f9c81033e5c16",
	9:  "88ed2c5d9c91bc5eb0f2aed1092d38bf866660c082ebb17ea9bd543c9c43be84",
	10: "45cf0f36dc20651b2c5091980a6447ab8fd1db7651b59981a7e68f14baaf0ec5",
	11: "b8148a4007be1c37cc3de93e670620dd70a7abc28addbdc1db5925c9a3f1d873",
	12: "760860c0f8fadff0464c091cfdd00dccef4d021f1dd5e145e81336d39913a04c",
	13: "174072f5013367e258c8ff18d3b1b35d704d532fa24606876cbd75481f18f150",
	14: "052ca76dec68a1a13859fca04fb0118e903dfaa3fc3999ba9b553eb4234448cc",
	15: "272ba4886017c9f590b31d4650cdcb562e2ed8bc77692bc7a618630e2a1a736f",
	16: "ea6c953d183d6fd2c4acfbd18f351b9a6181cccb0adc7c8e1dac865cff41d7dd",
	17: "1c63d80943b163ca0cd7064d605395ece06a1ad8f78cbd5159b2403586c2a536",
	18: "71e5b383b009e119afd04e716d565b39895e1ba188ea9a7cf04db8acd89850e8",
	19: "e0784cb5c6840eb99ca9bb035a86e5b96f4c933f9cea6b17f76887f53eecbe6b",
	20: "1c060688f654fd112fd46a515f25a7db1a29459c1de7728d9d9305b2c1850f9a",
	21: "f34243cae26f6131bbf898abefba322fcfdbaf24828de685db2119d377989daf",
	22: "b13cd6c710491c6470d2bead16fd456e8089417ea9e48ddebb5a90105d86c29e",
	23: "37a0b685869fe059d163e9a7b14371ac298b1894befbbc35839b3c05aa50b0f9",
	24: "c10e2510f2ea035d33667bba001a0a361aedbd01485292712b5bdee18ffa76b0",
	25: "c89ac1c0967ae9fb68cb4232a02c8a76ff122ac8fa2bfdcb80e6fc06a9c7ccc4",
	26: "4904ad3ce993bb92e7c0f8639243d80cf0f98991ba95457d1e73b09db946e36a",
}

func TestMigrationsImmutable(t *testing.T) {
	for _, m := range embedded(t) {
		sum := sha256.Sum256([]byte(m.SQL))
		got := hex.EncodeToString(sum[:])
		want, ok := pinnedMigrations[m.Version]
		if !ok {
			t.Errorf("migration %04d_%s is not pinned; add %q", m.Version, m.Name, got)
			continue
		}
		if got != want {
			t.Errorf("migration %04d_%s changed: sha256 %s, pinned %s", m.Version, m.Name, got, want)
		}
	}
}

func TestMigrationsContiguous(t *testing.T) {
	ms := embedded(t)
	if len(ms) != SchemaVersion {
		t.Fatalf("%d migrations, SchemaVersion %d", len(ms), SchemaVersion)
	}
	for i, m := range ms {
		if m.Version != i+1 {
			t.Fatalf("migration %d has version %d", i, m.Version)
		}
	}
}

func TestLoadMigrationsRejectsBadSets(t *testing.T) {
	mk := func(names ...string) fstest.MapFS {
		f := fstest.MapFS{}
		for _, n := range names {
			f["migrations/"+n] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		}
		return f
	}
	for name, names := range map[string][]string{
		"gap":      {"0001_a.sql", "0003_c.sql"},
		"bad name": {"0001_a.sql", "second.sql"},
		"no start": {"0002_b.sql"},
	} {
		if _, err := loadMigrations(mk(names...)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestLibrarySchema(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	for _, tbl := range append(append([]string{}, libraryTables...), shelfTables...) {
		var strict int
		if err := st.Reader().QueryRow("SELECT strict FROM pragma_table_list WHERE name = ?", tbl).Scan(&strict); err != nil {
			t.Fatalf("%s: %v", tbl, err)
		}
		if strict != 1 {
			t.Errorf("%s is not STRICT", tbl)
		}
	}
	seedLibrary(t, st)
	rows, err := st.Writer().Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("foreign_key_check reports a violation")
	}
	rows.Close()

	bad := map[string]string{
		"chapter end before start": `INSERT INTO chapters(book_id, idx, start_ms, end_ms, source) VALUES ('bk_1', 1, 10, 5, 'file')`,
		"cover tint upper case":    `INSERT INTO covers(id, book_id, source, mime, width, height, sha256, path, tint, created_at) VALUES ('cv_2', 'bk_1', 'folder', 'image/png', 1, 1, '` + strings.Repeat("c", 64) + `', 'x', '#ABC', 1)`,
		"book refused reason":      `UPDATE books SET refused_reason = 'x' WHERE id = 'bk_1'`,
		"audio file encrypted 2":   `UPDATE audio_files SET encrypted = 2 WHERE id = 'af_1'`,
		"person role":              `INSERT INTO book_people(book_id, person_id, role) VALUES ('bk_1', 'ps_1', 'editor')`,
		"short quick hash":         `UPDATE audio_files SET quick_hash = 'short' WHERE id = 'af_1'`,
		"probe error upper case":   `UPDATE audio_files SET probe_error = 'BAD' WHERE id = 'af_1'`,
	}
	for name, stmt := range bad {
		if _, err := st.Writer().Exec(stmt); err == nil {
			t.Errorf("%s: statement succeeded", name)
		}
	}
}

func TestMigration0006Constraints(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seed3(t, st)
	seedProgress(t, st)
	bad := map[string]string{
		"negative position":     `UPDATE progress SET position_ms = -1`,
		"client time zero":      `UPDATE progress SET client_updated_at = 0`,
		"long device id":        `UPDATE progress SET device_id = '` + strings.Repeat("d", 65) + `'`,
		"empty device id":       `UPDATE progress SET device_id = ''`,
		"long note":             `UPDATE bookmarks SET note = '` + strings.Repeat("n", 2001) + `'`,
		"speed below range":     `UPDATE listening_sessions SET speed_pct = 40`,
		"speed above range":     `UPDATE listening_sessions SET speed_pct = 301`,
		"session ends early":    `UPDATE listening_sessions SET ended_at = 0`,
		"negative listened":     `UPDATE listening_sessions SET listened_ms = -1`,
		"finished at zero":      `UPDATE progress SET finished_at = 0`,
		"progress unknown book": `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id) VALUES ('us_1', 'nope', 0, 0, 1, 1, 'dev_test_1')`,
	}
	for name, stmt := range bad {
		if _, err := st.Writer().Exec(stmt); err == nil {
			t.Errorf("%s: statement succeeded", name)
		}
	}
	if _, err := st.Writer().Exec(`UPDATE listening_sessions SET speed_pct = 150`); err != nil {
		t.Errorf("speed 150: %v", err)
	}
	for _, c := range []struct{ name, del string }{
		{"book", `DELETE FROM books WHERE id = 'bk_1'`},
		{"user", `DELETE FROM users WHERE id = 'us_1'`},
	} {
		t.Run("cascade "+c.name, func(t *testing.T) {
			st := mustOpen(t, t.TempDir())
			defer st.Close()
			seed3(t, st)
			seedProgress(t, st)
			if _, err := st.Writer().Exec(c.del); err != nil {
				t.Fatal(err)
			}
			for _, tbl := range progressTables {
				if n := countRows(t, st.Reader(), tbl); n != 0 {
					t.Errorf("%s: %d rows left after deleting the %s", tbl, n, c.name)
				}
			}
		})
	}
}

func TestMigration0007Constraints(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seed3(t, st)
	seedFamily(t, st)
	bad := map[string]string{
		"library scope without library": `INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gr_x', 'us_1', 'library', 1, 1)`,
		"all scope with a library":      `INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gr_x', 'us_1', 'all', 'lib_1', 1, 1)`,
		"library and book both set":     `INSERT INTO grants(id, user_id, scope, library_id, book_id, created_at, updated_at) VALUES ('gr_x', 'us_1', 'library', 'lib_1', 'bk_1', 1, 1)`,
		"book scope with a library":     `INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gr_x', 'us_1', 'book', 'lib_1', 1, 1)`,
		"unknown scope":                 `INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gr_x', 'us_1', 'galaxy', 1, 1)`,
		"grant expires at creation":     `INSERT INTO grants(id, user_id, scope, created_at, updated_at, expires_at) VALUES ('gr_x', 'us_1', 'all', 5, 5, 5)`,
		"duplicate grant target":        `INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gr_x', 'us_1', 'library', 'lib_1', 1, 1)`,
		"second all grant":              `INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gr_a', 'us_1', 'all', 1, 1), ('gr_b', 'us_1', 'all', 1, 1)`,
		"grant for a missing user":      `INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gr_x', 'nobody', 'all', 1, 1)`,
		"31 byte invite token":          `INSERT INTO invites(id, token_hash, created_by, name, role, created_at, updated_at) VALUES ('in_x', randomblob(31), 'us_1', 'Rosa', 'member', 1, 1)`,
		"owner invite":                  `INSERT INTO invites(id, token_hash, created_by, name, role, created_at, updated_at) VALUES ('in_x', randomblob(32), 'us_1', 'Rosa', 'owner', 1, 1)`,
		"use count above max uses":      `UPDATE invites SET use_count = 2`,
		"invite grants not an array":    `UPDATE invites SET grants_json = '{}'`,
		"invite grants not json":        `UPDATE invites SET grants_json = 'x'`,
		"blank invite name":             `UPDATE invites SET name = '  '`,
		"share link over 90 days":       `UPDATE share_links SET expires_at = created_at + 7776000001`,
		"share link already expired":    `UPDATE share_links SET expires_at = created_at`,
		"31 byte share token":           `INSERT INTO share_links(id, token_hash, book_id, created_by, created_at, expires_at) VALUES ('sl_x', randomblob(31), 'bk_1', 'us_1', 1, 2)`,
		"revoked device keeps token":    `UPDATE devices SET revoked_at = 5`,
		"unknown device kind":           `UPDATE devices SET kind = 'toaster'`,
	}
	for name, stmt := range bad {
		if _, err := st.Writer().Exec(stmt); err == nil {
			t.Errorf("%s: statement succeeded", name)
		}
	}
	good := map[string]string{
		"share link at 90 days":   `UPDATE share_links SET expires_at = created_at + 7776000000`,
		"revoked device no token": `UPDATE devices SET token_hash = NULL, revoked_at = 5`,
		"admin invite never ends": `INSERT INTO invites(id, token_hash, created_by, name, role, created_at, updated_at) VALUES ('in_2', randomblob(32), 'us_1', 'Pia', 'admin', 1, 1)`,
		"all scope grant":         `INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gr_2', 'us_1', 'all', 1, 1)`,
		"book scope grant":        `INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_3', 'us_1', 'book', 'bk_1', 1, 1)`,
	}
	for name, stmt := range good {
		if _, err := st.Writer().Exec(stmt); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, c := range []struct {
		name, del string
		left      int // grants left of the library grant and a book grant
	}{
		{"user", `DELETE FROM users WHERE id = 'us_1'`, 0},
		{"library", `DELETE FROM libraries WHERE id = 'lib_1'`, 0},
		{"book", `DELETE FROM books WHERE id = 'bk_1'`, 1},
	} {
		t.Run("cascade "+c.name, func(t *testing.T) {
			st := mustOpen(t, t.TempDir())
			defer st.Close()
			seed3(t, st)
			seedFamily(t, st)
			if _, err := st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_b', 'us_1', 'book', 'bk_1', 1, 1)`); err != nil {
				t.Fatal(err)
			}
			if _, err := st.Writer().Exec(c.del); err != nil {
				t.Fatal(err)
			}
			if n := countRows(t, st.Reader(), "grants"); n != c.left {
				t.Errorf("%d grants left after deleting the %s, want %d", n, c.name, c.left)
			}
			if c.name == "user" {
				for _, tbl := range familyTables {
					if n := countRows(t, st.Reader(), tbl); n != 0 {
						t.Errorf("%s: %d rows left after deleting the user", tbl, n)
					}
				}
			}
		})
	}
}

func TestConstraintClassifiers(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Writer().Exec(`INSERT INTO settings(key, value_json) VALUES ('k', '1')`); err != nil {
		t.Fatal(err)
	}
	_, err := st.Writer().Exec(`INSERT INTO settings(key, value_json) VALUES ('k', '2')`)
	if !IsUniqueViolation(err) || IsForeignKeyViolation(err) {
		t.Fatalf("duplicate key: unique=%v fk=%v err=%v", IsUniqueViolation(err), IsForeignKeyViolation(err), err)
	}
	_, err = st.Writer().Exec(`INSERT INTO user_settings(user_id, key, value_json, updated_at) VALUES ('nobody', 'k', '1', 1)`)
	if !IsForeignKeyViolation(err) || IsUniqueViolation(err) {
		t.Fatalf("missing parent: unique=%v fk=%v err=%v", IsUniqueViolation(err), IsForeignKeyViolation(err), err)
	}
	if IsUniqueViolation(nil) || IsForeignKeyViolation(errors.New("x")) {
		t.Fatal("non-SQLite errors must not match")
	}
}

func TestOnlyOneOwner(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	ins := `INSERT INTO users(id, username, role, created_at) VALUES (?, ?, ?, 1)`
	if _, err := st.Writer().Exec(ins, "us_1", "a", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(ins, "us_2", "b", "owner"); err == nil {
		t.Fatal("a second owner must be rejected")
	}
	if _, err := st.Writer().Exec(ins, "us_3", "A", "member"); err == nil {
		t.Fatal("usernames are unique ignoring case")
	}
	if _, err := st.Writer().Exec(ins, "us_4", "c", "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestJobsLeaseCheck(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	ins := `INSERT INTO jobs(id, kind, state, lease_until, created_at, updated_at) VALUES (?, 'k', ?, ?, 1, 1)`
	cases := []struct {
		state string
		lease any
		ok    bool
	}{
		{"queued", nil, true},
		{"running", 100, true},
		{"running", nil, false},
		{"queued", 100, false},
		{"done", 100, false},
	}
	for i, c := range cases {
		_, err := st.Writer().Exec(ins, "jb_"+string(rune('a'+i)), c.state, c.lease)
		if (err == nil) != c.ok {
			t.Errorf("state=%s lease=%v: err=%v", c.state, c.lease, err)
		}
	}
}

func TestReaderIsQueryOnly(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Reader().Exec(`INSERT INTO settings(key, value_json) VALUES ('a', '1')`); err == nil {
		t.Fatal("the reader pool must not write")
	}
	if _, err := st.Writer().Exec(`INSERT INTO settings(key, value_json) VALUES ('a', '1')`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "settings") != 1 {
		t.Fatal("reader does not see the writer's row")
	}
}

func TestWriteTxRollback(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	ctx := context.Background()
	ins := `INSERT INTO settings(key, value_json) VALUES ('a', '1')`

	boom := errors.New("boom")
	err := st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(ins); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || countRows(t, st.Reader(), "settings") != 0 {
		t.Fatalf("error path: err=%v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("WriteTx must re-panic")
			}
		}()
		_ = st.WriteTx(ctx, func(tx *sql.Tx) error {
			_, _ = tx.Exec(ins)
			panic("kaboom")
		})
	}()
	if countRows(t, st.Reader(), "settings") != 0 {
		t.Fatal("panic path left a row")
	}
	// The single writer connection is free again.
	if err := st.WriteTx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(ins); return err }); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "settings") != 1 {
		t.Fatal("commit path lost the row")
	}
}

// Migration 0004 builds the search index from the book rows that already exist.
func TestMigration0004BuildsIndex(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:3], supported: 3, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seeds[3](t, prev)
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	var rid, want int
	if err := st.Reader().QueryRow(`SELECT rid FROM books_fts_ids WHERE book_id = 'bk_1'`).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`"ana"*`, `"saga"*`, `"a"*`} {
		var got int
		if err := st.Reader().QueryRow(`SELECT rowid FROM books_fts WHERE books_fts MATCH ?`, q).Scan(&got); err != nil || got != rid {
			t.Errorf("MATCH %s = %d, %v; want rid %d", q, got, err, rid)
		}
	}
	if err := st.Reader().QueryRow(`SELECT count(*) FROM books`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "books_fts_ids"); n != want || want != 1 {
		t.Fatalf("ids = %d, books = %d", n, want)
	}
}

func init() { tablesAt[8] = tablesAt[7]; tablesAt[9] = tablesAt[7]; tablesAt[10] = tablesAt[7] }

// TestMigration0008BookmarkClientID: bookmarks saved before 0008 survive with a NULL client id, and the
// client id is unique per person (not across people), several NULLs are fine.
func TestMigration0008BookmarkClientID(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:7], supported: 7, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	var cid sql.NullString
	if err := st.Reader().QueryRow(`SELECT client_id FROM bookmarks WHERE id = 'bm_1'`).Scan(&cid); err != nil || cid.Valid {
		t.Fatalf("old bookmark client_id = %v, %v", cid, err)
	}
	ins := `INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at, client_id) VALUES (?, ?, 'bk_1', 1, 1, 1, ?)`
	if _, err := st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES ('us_2', 'Bo', 'member', 1)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, user string
		cid      any
		ok       bool
	}{
		{"bm_a", "us_1", "c1", true}, {"bm_b", "us_2", "c1", true}, {"bm_c", "us_1", "c1", false},
		{"bm_d", "us_1", nil, true}, {"bm_e", "us_1", "", false},
	} {
		if _, err := st.Writer().Exec(ins, c.id, c.user, c.cid); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.id, err, c.ok)
		}
	}
}

// TestMigration0009InviteMail: invites made before 0009 survive with a NULL mail job.
func TestMigration0009InviteMail(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:8], supported: 8, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	var job sql.NullString
	if err := st.Reader().QueryRow(`SELECT mail_job_id FROM invites WHERE id = 'in_1'`).Scan(&job); err != nil || job.Valid {
		t.Fatalf("old invite mail_job_id = %v, %v", job, err)
	}
	if _, err := st.Writer().Exec(`UPDATE invites SET mail_job_id = 'job_1' WHERE id = 'in_1'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "invites"); got != 1 {
		t.Fatalf("invites = %d, want 1", got)
	}
}

// TestMigration0010Details: a v9 database keeps its book, details and progress rows, and gains the
// two empty tables; the cache refuses an unknown provider, a short key and a body that is not JSON.
func TestMigration0010Details(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:9], supported: 9, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	for _, tbl := range []string{"books", "metadata_values", "progress"} {
		if countRows(t, st.Reader(), tbl) != 1 {
			t.Errorf("%s lost its row", tbl)
		}
	}
	for _, tbl := range []string{"book_sidecars", "online_cache"} {
		if n := countRows(t, st.Reader(), tbl); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	key := strings.Repeat("a", 64)
	for name, q := range map[string]string{
		"provider": `INSERT INTO online_cache VALUES ('bing', '` + key + `', '{}', 1)`,
		"key":      `INSERT INTO online_cache VALUES ('google', 'short', '{}', 1)`,
		"json":     `INSERT INTO online_cache VALUES ('google', '` + key + `', '{nope', 1)`,
	} {
		if _, err := st.Writer().Exec(q); err == nil {
			t.Errorf("online_cache accepted a bad %s", name)
		}
	}
	if _, err := st.Writer().Exec(`INSERT INTO online_cache VALUES ('google', '` + key + `', '{}', 1)`); err != nil {
		t.Errorf("good cache row: %v", err)
	}
	if _, err := st.Writer().Exec(`INSERT INTO book_sidecars VALUES ('bk_1', 'sig', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "book_sidecars"); n != 0 {
		t.Errorf("sidecar signature outlived its book (%d)", n)
	}
}

// TestMigration0011FileWrites: a v10 database keeps its rows (a sidecar signature among them) and
// gains the empty ledger; the ledger refuses an unknown state and a second open row for one path,
// allows a new write after a closed one, and keeps its rows when the book is deleted.
func TestMigration0011FileWrites(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:10], supported: 10, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	if _, err := prev.Writer().Exec(`INSERT INTO book_sidecars VALUES ('bk_1', 'sig', 1)`); err != nil {
		t.Fatal(err)
	}
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	for _, tbl := range []string{"books", "metadata_values", "progress", "book_sidecars"} {
		if countRows(t, st.Reader(), tbl) != 1 {
			t.Errorf("%s lost its row", tbl)
		}
	}
	if n := countRows(t, st.Reader(), "file_writes"); n != 0 {
		t.Errorf("file_writes has %d rows", n)
	}
	ins := func(id, state, path string) error {
		_, err := st.Writer().Exec(`INSERT INTO file_writes(id, job_id, book_id, audio_file_id, state, original_path, backup_path,
  temp_path, before_sha256, audio_hash, created_by, created_at, updated_at) VALUES (?, 'jb_1', 'bk_1', 'af_1', ?, ?, 'b', 't', 'x', 'y', 'us_1', 1, 1)`,
			id, state, path)
		return err
	}
	if ins("fw_1", "bogus", "/a") == nil {
		t.Error("an unknown state was accepted")
	}
	if err := ins("fw_1", "copying", "/a"); err != nil {
		t.Fatal(err)
	}
	if ins("fw_2", "swapped", "/a") == nil {
		t.Error("a second open row for one path was accepted")
	}
	if err := ins("fw_3", "copying", "/b"); err != nil {
		t.Errorf("another path: %v", err)
	}
	if _, err := st.Writer().Exec(`UPDATE file_writes SET state = 'undone' WHERE id = 'fw_1'`); err != nil {
		t.Fatal(err)
	}
	if err := ins("fw_2", "copying", "/a"); err != nil {
		t.Errorf("a new write after a closed one: %v", err)
	}
	if _, err := st.Writer().Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "file_writes"); n != 3 {
		t.Errorf("ledger rows after the book went = %d, want 3", n)
	}
}

// A -wal file means the header is read through SQLite. Only "not a database" may read as foreign: a read
// that fails for another reason must say so (a quick restart once got "does not belong to Tendshelf").
func TestReadHeaderWithWAL(t *testing.T) {
	t.Run("not a database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, bytes.Repeat([]byte("not sqlite "), 400), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+"-wal", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readHeader(path, 1); !errors.Is(err, ErrNotTendshelf) {
			t.Fatalf("err = %v, want ErrNotTendshelf", err)
		}
	})
	t.Run("other errors stay themselves", func(t *testing.T) {
		notTendshelf := &SchemaError{Found: -1, Supported: 1, Err: ErrNotTendshelf}
		boom := errors.New("read failed")
		if got := headerErr(boom, notTendshelf); !errors.Is(got, boom) || errors.Is(got, ErrNotTendshelf) {
			t.Fatalf("got %v", got)
		}
	})
}

func TestROWaitsOnLocks(t *testing.T) {
	if !strings.Contains(roDSN("/x/tendshelf.db"), "busy_timeout") {
		t.Fatal("the read-only DSN does not wait on locks")
	}
}
