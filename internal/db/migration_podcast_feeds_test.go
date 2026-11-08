package db

import "testing"

// TestMigrationPodcastFeedsFromPrevious: a v35 database keeps its users and gains an empty
// podcast_feeds table with its index.
func TestMigrationPodcastFeedsFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 35, dir)
	seed7(t, prev)
	users := countRows(t, prev.Reader(), "users")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "users"); got != users || got == 0 {
		t.Errorf("user rows = %d, want %d", got, users)
	}
	if got := countRows(t, st.Reader(), "podcast_feeds"); got != 0 {
		t.Errorf("podcast_feeds rows = %d, want 0", got)
	}
	if n := indexCount(t, st, "podcast_feeds_user"); n != 1 {
		t.Errorf("podcast_feeds_user indexes = %d, want 1", n)
	}
}

// TestMigrationPodcastFeedsFromEmpty: exactly one target is allowed, a short hash is refused,
// and deleting the person deletes the feed.
func TestMigrationPodcastFeedsFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`); err != nil {
		t.Fatal(err)
	}
	const ins = `INSERT INTO podcast_feeds (id, token_hash, user_id, book_id, series_id, created_at) VALUES `
	for _, q := range []string{
		ins + `('f1', randomblob(32), 'us_1', 'b1', NULL, 1)`,
		ins + `('f2', randomblob(32), 'us_1', NULL, 's1', 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"both targets": ins + `('f3', randomblob(32), 'us_1', 'b1', 's1', 1)`,
		"no target":    ins + `('f4', randomblob(32), 'us_1', NULL, NULL, 1)`,
		"short hash":   ins + `('f5', x'00', 'us_1', 'b1', NULL, 1)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "podcast_feeds"); got != 0 {
		t.Errorf("rows after user delete = %d", got)
	}
}
