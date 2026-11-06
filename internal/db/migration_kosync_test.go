package db

import "testing"

// TestMigrationKosyncFromPrevious: a database from before keeps its ebook files, and each gains an empty digest.
func TestMigrationKosyncFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 27, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(ebookFileSQL("ef_k", "library", "/audio/a/a.epub", hexA, "ready", "")); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var sha string
	var md5 *string
	if err := st.Reader().QueryRow(`SELECT sha256, partial_md5 FROM ebook_files WHERE id = 'ef_k'`).Scan(&sha, &md5); err != nil || sha != hexA || md5 != nil {
		t.Fatalf("ebook file after migration: %q %v %v", sha, md5, err)
	}
}

// TestMigrationKosyncFromEmpty: the digest and the progress table take good rows and refuse bad ones, and a
// person's rows go with the person.
func TestMigrationKosyncFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO users(id, username, display_name, password_hash, role, created_at) VALUES ('us_k', 'kay', 'Kay', 'x', 'member', 1)`); err != nil {
		t.Fatal(err)
	}
	d := "0123456789abcdef0123456789abcdef"
	ins := `INSERT INTO kosync_progress(user_id, document, progress, percentage, device, device_id, updated_at) VALUES (?,?,?,?,?,?,?)`
	if _, err := w.Exec(ins, "us_k", d, "/body/DocFragment[3]/body", 0.5, "Kobo", "dev", 5); err != nil {
		t.Fatalf("good row: %v", err)
	}
	for name, args := range map[string][]any{
		"short document": {"us_k", "abc", "p", 0.5, "d", "d", 5},
		"percentage > 1": {"us_k", "fedcba9876543210fedcba9876543210", "p", 1.5, "d", "d", 5},
		"no time":        {"us_k", "fedcba9876543210fedcba9876543210", "p", 0.5, "d", "d", 0},
		"unknown person": {"us_x", "fedcba9876543210fedcba9876543210", "p", 0.5, "d", "d", 5},
	} {
		if _, err := w.Exec(ins, args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_k'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "kosync_progress"); n != 0 {
		t.Errorf("progress rows after the person left = %d", n)
	}
}

// TestMigrationKosyncDigestShape: a digest must be 32 characters.
func TestMigrationKosyncDigestShape(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 27, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(ebookFileSQL("ef_k", "library", "/audio/a/a.epub", hexA, "ready", "")); err != nil {
		t.Fatal(err)
	}
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	if _, err := st.Writer().Exec(`UPDATE ebook_files SET partial_md5 = 'short' WHERE id = 'ef_k'`); err == nil {
		t.Error("a short digest was accepted")
	}
	if _, err := st.Writer().Exec(`UPDATE ebook_files SET partial_md5 = '0123456789abcdef0123456789abcdef' WHERE id = 'ef_k'`); err != nil {
		t.Errorf("a good digest: %v", err)
	}
}
