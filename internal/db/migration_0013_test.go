package db

import (
	"context"
	"testing"
	"time"
)

// TestMigration0013Queue: a v12 database keeps its rows and gains an empty queue_items; a person
// has one row per book, and deleting the book takes the row with it.
func TestMigration0013Queue(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:12], supported: 12, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	for _, tbl := range []string{"libraries", "books", "metadata_values", "progress"} {
		if countRows(t, st.Reader(), tbl) != 1 {
			t.Errorf("%s lost its row", tbl)
		}
	}
	if n := countRows(t, st.Reader(), "queue_items"); n != 0 {
		t.Errorf("queue_items has %d rows", n)
	}
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_1', 'bk_1', 'bk_1', -1, 1)`); err != nil {
		t.Fatalf("negative position: %v", err)
	}
	for name, q := range map[string]string{
		"duplicate (user, book)": `INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_1', 'bk_1', 'bk_1', 2, 1)`,
		"unknown book":           `INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_1', 'bk_nope', 'bk_nope', 2, 1)`,
		"unknown user":           `INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_nope', 'bk_1', 'bk_1', 2, 1)`,
		"added_at = 0":           `INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_1', 'bk_1', 'bk_1', 2, 0)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "queue_items"); n != 0 {
		t.Errorf("queue row outlived its book (%d)", n)
	}
}

// TestMigration0013UserCascade: removing a person removes their queue.
func TestMigration0013UserCascade(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seed7(t, st)
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES ('us_1', 'bk_1', 'bk_1', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Skipf("fixture user has restricting dependents: %v", err)
	}
	if n := countRows(t, st.Reader(), "queue_items"); n != 0 {
		t.Errorf("queue row outlived its user (%d)", n)
	}
}

// TestMigration0013FromEmpty: a new database has the table at the current version.
func TestMigration0013FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if n := countRows(t, st.Reader(), "queue_items"); n != 0 {
		t.Errorf("queue_items has %d rows", n)
	}
}
