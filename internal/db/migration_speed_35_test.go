package db

import "testing"

func seedSpeed35(t *testing.T, st *Store) {
	t.Helper()
	seed3(t, st)
	for _, q := range []string{
		`INSERT INTO book_settings(user_id, book_id, speed, updated_at) VALUES ('us_1', 'bk_1', 2.5, 5)`,
		`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct) VALUES ('ls_1', 'us_1', 'bk_1', 'dev', 1, 2, 0, 5, 1, 300)`,
	} {
		if _, err := st.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// TestSpeed35MigrationFromPrevious: a v37 database keeps its rows and gains the wider limits.
func TestSpeed35MigrationFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 37, dir)
	seedSpeed35(t, prev)
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	checkSpeed35(t, st)
}

// TestSpeed35MigrationFromEmpty: a new database has the same limits.
func TestSpeed35MigrationFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seedSpeed35(t, st)
	checkSpeed35(t, st)
}

func checkSpeed35(t *testing.T, st *Store) {
	t.Helper()
	var speed float64
	if err := st.Reader().QueryRow(`SELECT speed FROM book_settings WHERE user_id='us_1' AND book_id='bk_1'`).Scan(&speed); err != nil || speed != 2.5 {
		t.Errorf("book_settings row = %v (err %v)", speed, err)
	}
	var pct int
	if err := st.Reader().QueryRow(`SELECT speed_pct FROM listening_sessions WHERE id='ls_1'`).Scan(&pct); err != nil || pct != 300 {
		t.Errorf("listening_sessions row = %v (err %v)", pct, err)
	}
	w := st.Writer()
	for _, q := range []string{
		`UPDATE book_settings SET speed = 3.5`,
		`UPDATE listening_sessions SET speed_pct = 350`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Errorf("%s refused: %v", q, err)
		}
	}
	for _, q := range []string{
		`UPDATE book_settings SET speed = 3.55`,
		`UPDATE listening_sessions SET speed_pct = 355`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s accepted", q)
		}
	}
	for _, ix := range []string{"book_settings_book", "listening_sessions_current", "listening_sessions_user", "listening_sessions_book", "listening_sessions_recent"} {
		var n int
		if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, ix).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing", ix)
		}
	}
	rows, err := st.Reader().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check reports a violation")
	}
}
