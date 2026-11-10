package db

import "testing"

// TestMigrationPodcastsFromPrevious: a v39 database keeps its users and gains the seven empty
// podcast and radio tables with their indexes.
func TestMigrationPodcastsFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 39, dir)
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
	for _, table := range []string{"podcast_shows", "podcast_episodes", "podcast_subscriptions", "podcast_episode_state",
		"radio_stations", "radio_favourites", "radio_plays"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s rows = %d, want 0", table, got)
		}
	}
	for _, ix := range []string{"podcast_episodes_show", "podcast_episodes_cached", "podcast_subscriptions_show",
		"podcast_state_history", "podcast_state_fav", "podcast_state_episode", "radio_plays_recent"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
}

// TestMigrationPodcastsFromEmpty: a new database enforces the CHECKs and uniqueness, and rows go
// with their user or show.
func TestMigrationPodcastsFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_1', 'https://a.example/f.xml', 1, 1)`,
		`INSERT INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES ('pe_1', 'ps_1', 'g1', 'One', 'https://a.example/1.mp3', 1, 1)`,
		`INSERT INTO podcast_subscriptions(user_id, show_id, followed_at, seen_at) VALUES ('us_1', 'ps_1', 1, 1)`,
		`INSERT INTO podcast_episode_state(user_id, episode_id, updated_at) VALUES ('us_1', 'pe_1', 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"speed 4":            `UPDATE podcast_subscriptions SET speed = 4`,
		"skip intro 601":     `UPDATE podcast_subscriptions SET skip_intro_s = 601`,
		"keep latest 0":      `UPDATE podcast_subscriptions SET keep_latest = 0`,
		"kind video":         `UPDATE podcast_episodes SET kind = 'video'`,
		"negative position":  `UPDATE podcast_episode_state SET position_ms = -1`,
		"short feed address": `INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_2', 'x://a', 1, 1)`,
		"same feed twice":    `INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_3', 'https://a.example/f.xml', 1, 1)`,
		"same guid twice":    `INSERT INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES ('pe_2', 'ps_1', 'g1', 'Two', 'https://a.example/2.mp3', 1, 1)`,
		"station duplicate":  `INSERT INTO radio_stations(id, stream_url, name, created_at, updated_at) VALUES ('rs_1', 'https://r.example/s', 'R', 1, 1), ('rs_2', 'https://r.example/s', 'R', 1, 1)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`UPDATE podcast_subscriptions SET speed = 3.5, skip_intro_s = 600`); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"podcast_subscriptions", "podcast_episode_state"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s after the user went = %d, want 0", table, got)
		}
	}
	if _, err := w.Exec(`DELETE FROM podcast_shows`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "podcast_episodes"); got != 0 {
		t.Errorf("episodes after the show went = %d, want 0", got)
	}
}
