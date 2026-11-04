package users

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReaderSettingsLoadSave(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadReader(ctx, u.ID)
	if err != nil || got != DefaultReader {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	want := ReaderSettings{Font: "dyslexic", Size: 26, Leading: "airy", Highlight: "brighten", Word: false, Follow: false,
		FollowStyle: "gentle", ReturnAuto: true, View: "focus"}
	if err := r.SaveReader(ctx, u.ID, want); err != nil {
		t.Fatal(err)
	}
	got, err = r.LoadReader(ctx, u.ID)
	if err != nil || got != want {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	// Another person is not affected.
	if g, err := r.LoadReader(ctx, "someone-else"); err != nil || g != DefaultReader {
		t.Errorf("another person sees %+v, %v", g, err)
	}
	// Invalid values are refused and leave the stored row alone.
	for _, mod := range []func(*ReaderSettings){
		func(s *ReaderSettings) { s.Font = "comic" },
		func(s *ReaderSettings) { s.Size = 19 },
		func(s *ReaderSettings) { s.Leading = "" },
		func(s *ReaderSettings) { s.Highlight = "glow" },
		func(s *ReaderSettings) { s.FollowStyle = "wild" },
		func(s *ReaderSettings) { s.View = "tv" },
	} {
		bad := want
		mod(&bad)
		if err := r.SaveReader(ctx, u.ID, bad); !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if g, _ := r.LoadReader(ctx, u.ID); g != want {
		t.Errorf("a refused save changed the row: %+v", g)
	}
}

func TestReaderSettingsBadStoredFields(t *testing.T) {
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
	// Bad fields fall back one by one; good ones are kept.
	put(`{"font":"sans","size":99,"leading":7,"hl":"underline","word":"yes","follow":false,"view":"nope"}`)
	got, err := r.LoadReader(ctx, u.ID)
	want := DefaultReader
	want.Font, want.Highlight, want.Follow = "sans", "underline", false
	if err != nil || got != want {
		t.Fatalf("partial = %+v, %v", got, err)
	}
	// Not JSON, or not an object: all defaults.
	for _, raw := range []string{`[1,2]`, `"x"`, `null`} {
		put(raw)
		if got, err := r.LoadReader(ctx, u.ID); err != nil || got != DefaultReader {
			t.Errorf("%s: %+v, %v", raw, got, err)
		}
	}
}
