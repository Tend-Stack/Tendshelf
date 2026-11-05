package users

import (
	"context"
	"database/sql"
	"testing"
)

// TestComfortSettings: the three comfort fields (G5) default to off, junk keeps the default, an old row without them
// loads, and a good choice round-trips.
func TestComfortSettings(t *testing.T) {
	if d := DefaultReader; d.KeepAwake || d.AutoHide || d.Warm != "off" {
		t.Fatalf("defaults = %+v", d)
	}
	for _, warm := range []string{"off", "evening", "always"} {
		s := DefaultReader
		s.Warm = warm
		if err := s.Validate(); err != nil {
			t.Errorf("warm %q: %v", warm, err)
		}
	}
	for _, warm := range []string{"", "night", "ALWAYS"} {
		s := DefaultReader
		s.Warm = warm
		if s.Validate() == nil {
			t.Errorf("warm %q was accepted", warm)
		}
	}

	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(raw string) {
		t.Helper()
		err := r.st.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, 'reader.v1', ?, 1)
				ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json`, u.ID, raw)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	load := func() ReaderSettings {
		t.Helper()
		got, err := r.LoadReader(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// An old row, stored before G5, keeps its choices and gets the comfort defaults.
	put(`{"font":"sans","size":22,"leading":"airy"}`)
	want := DefaultReader
	want.Font, want.Size, want.Leading = "sans", 22, "airy"
	if got := load(); got != want {
		t.Errorf("old row = %+v, want %+v", got, want)
	}
	// Junk of the wrong kind or outside the set falls back field by field.
	put(`{"warm":"midnight","keepAwake":"yes","autoHide":3}`)
	if got := load(); got != DefaultReader {
		t.Errorf("junk = %+v, want the defaults", got)
	}
	put(`{"warm":"evening","keepAwake":true,"autoHide":true}`)
	if got := load(); !got.KeepAwake || !got.AutoHide || got.Warm != "evening" {
		t.Errorf("good row = %+v", got)
	}
	// And a save round-trips.
	s := DefaultReader
	s.Warm, s.KeepAwake = "always", true
	if err := r.SaveReader(ctx, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if got := load(); got != s {
		t.Errorf("round trip = %+v, want %+v", got, s)
	}
}
