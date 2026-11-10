package db

import "testing"

// TestMigrationQueueEpisodesFromPrevious: a v40 database keeps its queued books, in order, and the
// rebuilt table names each by item_id and book_id.
func TestMigrationQueueEpisodesFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 40, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(`INSERT INTO queue_items(user_id, book_id, position, added_at) VALUES ('us_1', 'bk_1', -3, 7)`); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var item, book string
	var episode *string
	var pos, at int64
	if err := st.Reader().QueryRow(`SELECT item_id, book_id, episode_id, position, added_at FROM queue_items WHERE user_id = 'us_1'`).
		Scan(&item, &book, &episode, &pos, &at); err != nil {
		t.Fatal(err)
	}
	if item != "bk_1" || book != "bk_1" || episode != nil || pos != -3 || at != 7 {
		t.Errorf("row = %s %s %v %d %d", item, book, episode, pos, at)
	}
	for _, ix := range []string{"queue_items_order", "queue_items_book", "queue_items_episode"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
}

// TestMigrationQueueEpisodesFromEmpty: a new database holds books and episodes in one queue, enforces
// "exactly one of the two", and a row goes with its book, episode or person.
func TestMigrationQueueEpisodesFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seed7(t, st)
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO podcast_shows(id, feed_url, created_at, updated_at) VALUES ('ps_1', 'https://a.example/f.xml', 1, 1)`,
		`INSERT INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES ('pe_1', 'ps_1', 'g1', 'One', 'https://a.example/1.mp3', 1, 1)`,
		`INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_1', 'bk_1', 'bk_1', 1, 1)`,
		`INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES ('us_1', 'pe_1', 'pe_1', 2, 2)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"neither book nor episode": `INSERT INTO queue_items(user_id, item_id, position, added_at) VALUES ('us_1', 'x', 3, 3)`,
		"both book and episode":    `INSERT INTO queue_items(user_id, item_id, book_id, episode_id, position, added_at) VALUES ('us_1', 'bk_1', 'bk_1', 'pe_1', 3, 3)`,
		"item_id not the id":       `INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES ('us_1', 'other', 'pe_1', 3, 3)`,
		"same item twice":          `INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES ('us_1', 'pe_1', 'pe_1', 3, 3)`,
		"unknown episode":          `INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES ('us_1', 'pe_nope', 'pe_nope', 3, 3)`,
		"added_at = 0":             `INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES ('us_1', 'pe_1', 'pe_1', 3, 0)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM podcast_episodes WHERE id = 'pe_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "queue_items"); n != 1 {
		t.Errorf("queue rows after the episode went = %d, want 1", n)
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "queue_items"); n != 0 {
		t.Errorf("queue rows after the book went = %d, want 0", n)
	}
}
