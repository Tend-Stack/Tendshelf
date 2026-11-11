package db

import "testing"

// TestMigrationPodcastOpensFromPrevious: a v42 database keeps its shows, follows and places, and gains podcast_opens.
func TestMigrationPodcastOpensFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 42, dir)
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_1', 'https://a.example/f.xml', 1, 1)`,
		`INSERT INTO podcast_subscriptions(user_id, show_id, followed_at, seen_at) VALUES ('us_1', 'ps_1', 1, 1)`,
		`INSERT INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES ('pe_1', 'ps_1', 'g', 'One', 'https://a.example/1.mp3', 1, 1)`,
		`INSERT INTO podcast_episode_state(user_id, episode_id, updated_at) VALUES ('us_1', 'pe_1', 1)`,
	} {
		if _, err := prev.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion || v < 43 {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var n int
	for _, q := range []string{
		`SELECT count(*) FROM podcast_shows`, `SELECT count(*) FROM podcast_subscriptions`, `SELECT count(*) FROM podcast_episode_state`,
	} {
		if err := st.Reader().QueryRow(q).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s = %d, %v", q, n, err)
		}
	}
	for _, name := range []string{"podcast_opens", "podcast_opens_show", "podcast_opens_recent"} {
		if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s missing", name)
		}
	}
}

// TestMigrationPodcastOpensFromEmpty: one row per person and show, a positive time, and the row goes
// with its person and with its show.
func TestMigrationPodcastOpensFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'member', 1), ('us_2', 'Bo', 'member', 1)`,
		`INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_1', 'https://a.example/f.xml', 1, 1), ('ps_2', 'https://b.example/f.xml', 1, 1)`,
		`INSERT INTO podcast_opens(user_id, show_id, opened_at) VALUES ('us_1', 'ps_1', 5), ('us_2', 'ps_1', 5), ('us_1', 'ps_2', 5)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Exec(`INSERT INTO podcast_opens(user_id, show_id, opened_at) VALUES ('us_1', 'ps_1', 6)`); err == nil {
		t.Error("duplicate open accepted")
	}
	if _, err := w.Exec(`INSERT INTO podcast_opens(user_id, show_id, opened_at) VALUES ('us_2', 'ps_2', 0)`); err == nil {
		t.Error("opened_at 0 accepted")
	}
	count := func() int {
		var n int
		if err := st.Reader().QueryRow(`SELECT count(*) FROM podcast_opens`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_2'`); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Errorf("after deleting a user: %d rows, want 2", n)
	}
	if _, err := w.Exec(`DELETE FROM podcast_shows WHERE id = 'ps_2'`); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Errorf("after deleting a show: %d rows, want 1", n)
	}
}
