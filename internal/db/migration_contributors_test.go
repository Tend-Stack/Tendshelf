package db

import "testing"

// TestMigrationContributorsFromPrevious: a v33 database keeps its users and invites with the flag at 0, gains
// the two tables, and its placed uploads (not the refused ones) become upload_books rows.
func TestMigrationContributorsFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 33, dir)
	seed7(t, prev)
	for _, q := range []string{
		`INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, book_path, created_at, updated_at) VALUES ('up_1', 'lib_1', 'lf_1', 'us_1', 'A', 'placed', 1, '/audio/A', 1, 5)`,
		`INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, book_path, created_at, updated_at) VALUES ('up_2', 'lib_1', 'lf_1', 'us_1', 'B', 'refused', 1, '/audio/B', 1, 5)`,
		`INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, book_path, created_at, updated_at) VALUES ('up_3', 'lib_1', 'lf_1', 'us_1', 'A again', 'placed', 1, '/audio/A', 1, 9)`,
	} {
		if _, err := prev.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	users := countRows(t, prev.Reader(), "users")
	invites := countRows(t, prev.Reader(), "invites")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if got := countRows(t, st.Reader(), "users"); got != users || got == 0 {
		t.Errorf("users = %d, want %d", got, users)
	}
	if got := countRows(t, st.Reader(), "invites"); got != invites || got == 0 {
		t.Errorf("invites = %d, want %d", got, invites)
	}
	var flagged int
	if err := st.Reader().QueryRow(`SELECT (SELECT count(*) FROM users WHERE adds_books <> 0) + (SELECT count(*) FROM invites WHERE adds_books <> 0)`).Scan(&flagged); err != nil || flagged != 0 {
		t.Errorf("flagged rows = %d, %v", flagged, err)
	}
	var n int
	var upload string
	if err := st.Reader().QueryRow(`SELECT count(*), COALESCE(max(upload_id), '') FROM upload_books`).Scan(&n, &upload); err != nil {
		t.Fatal(err)
	}
	if n != 1 || upload != "up_3" {
		t.Errorf("upload_books = %d rows from %q, want 1 from the newest (up_3)", n, upload)
	}
}

// TestMigrationContributorsFromEmpty: a new database has the tables, the index and the flag.
func TestMigrationContributorsFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	for _, name := range []string{"contributor_folders", "upload_books"} {
		if got := countRows(t, st.Reader(), name); got != 0 {
			t.Errorf("%s rows = %d, want 0", name, got)
		}
	}
	if n := indexCount(t, st, "upload_books_by"); n != 1 {
		t.Errorf("upload_books_by indexes = %d, want 1", n)
	}
}

// TestMigrationContributorsChecks: the flag is refused on every role but member, for users and invites.
func TestMigrationContributorsChecks(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, role := range []string{"owner", "admin", "guest"} {
		if _, err := w.Exec(`INSERT INTO users(id, username, role, adds_books, created_at) VALUES ('u_`+role+`', '`+role+`', '`+role+`', 1, 1)`); err == nil {
			t.Errorf("users: flag on %s was accepted", role)
		}
	}
	if _, err := w.Exec(`INSERT INTO users(id, username, role, adds_books, created_at) VALUES ('u_m', 'm', 'member', 2, 1)`); err == nil {
		t.Error("users: adds_books = 2 was accepted")
	}
	if _, err := w.Exec(`INSERT INTO users(id, username, role, adds_books, created_at) VALUES ('u_c', 'c', 'member', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`UPDATE users SET role = 'guest' WHERE id = 'u_c'`); err == nil {
		t.Error("users: a flagged member became a guest without clearing the flag")
	}
	ins := `INSERT INTO invites(id, token_hash, created_by, name, role, adds_books, created_at, updated_at) VALUES (?, randomblob(32), 'u_c', 'Rosa', ?, 1, 1, 1)`
	for _, role := range []string{"admin", "guest"} {
		if _, err := w.Exec(ins, "in_"+role, role); err == nil {
			t.Errorf("invites: flag on %s was accepted", role)
		}
	}
	if _, err := w.Exec(ins, "in_ok", "member"); err != nil {
		t.Errorf("invites: flag on member refused: %v", err)
	}
}
