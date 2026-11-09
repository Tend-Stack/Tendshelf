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
	want := DefaultReader
	want.Font, want.Size, want.Leading = "dyslexic", 26, "airy"
	want.Margin, want.Justify, want.Hyphens, want.Flow, want.TapSwap = "wide", true, false, "pages", true
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
		func(s *ReaderSettings) { s.Margin = "huge" },
		func(s *ReaderSettings) { s.Flow = "" },
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
	put(`{"font":"sans","size":99,"leading":7}`)
	got, err := r.LoadReader(ctx, u.ID)
	want := DefaultReader
	want.Font = "sans"
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

// TestXReaderSettingsOldJSON: a reader.v1 row stored while the reader had voice settings still loads. The keys that
// are gone are ignored and the rest is kept.
func TestXReaderSettingsOldJSON(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	err = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, 'reader.v1', ?, 1)`,
			u.ID, `{"font":"dyslexic","size":22,"leading":"airy","hl":"underline","word":false,"follow":false,"followStyle":"gentle","returnAuto":true,"view":"focus"}`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadReader(ctx, u.ID)
	want := DefaultReader
	want.Font, want.Size, want.Leading = "dyslexic", 22, "airy"
	if err != nil || got != want {
		t.Fatalf("old JSON = %+v, %v; want %+v", got, err, want)
	}
}

func TestFeelVolumeKeys(t *testing.T) {
	if DefaultReader.VolumeKeys {
		t.Fatal("volume keys default on")
	}
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultReader
	want.VolumeKeys = true
	if err := r.SaveReader(ctx, u.ID, want); err != nil {
		t.Fatal(err)
	}
	if got, err := r.LoadReader(ctx, u.ID); err != nil || !got.VolumeKeys {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if got := parseReader(`{"volumeKeys":1}`); got.VolumeKeys {
		t.Errorf("a number parsed as true")
	}
}

func TestParseReaderAutoIsPages(t *testing.T) {
	for raw, want := range map[string]string{
		`{"flow":"auto"}`:   "pages",
		`{"flow":"scroll"}`: "scroll",
		`{"flow":"pages"}`:  "pages",
		`{}`:                "pages",
		`{"flow":"zigzag"}`: "pages",
	} {
		if got := parseReader(raw).Flow; got != want {
			t.Errorf("%s: flow = %q, want %q", raw, got, want)
		}
	}
	if DefaultReader.Flow != "pages" {
		t.Errorf("default flow = %q", DefaultReader.Flow)
	}
	// An old sheet can still post "auto".
	old := DefaultReader
	old.Flow = "auto"
	if err := old.Validate(); err != nil {
		t.Errorf("auto no longer validates: %v", err)
	}
}

// TestReaderSettingsTurn: a person who never chose a page turn gets the flip; a stored value outside its set is the default;
// Validate refuses one outside the set.
func TestReaderSettingsTurn(t *testing.T) {
	if got := parseReader(`{"size":22}`).Turn; got != "flip" {
		t.Errorf("no turn stored: %q, want flip", got)
	}
	if got := parseReader(`{"turn":"slide"}`).Turn; got != "slide" {
		t.Errorf("stored slide: %q", got)
	}
	if got := parseReader(`{"turn":"curl"}`).Turn; got != "flip" {
		t.Errorf("stored curl: %q, want the default", got)
	}
	r := DefaultReader
	r.Turn = "curl"
	if err := r.Validate(); err == nil {
		t.Error("Validate accepted a turn outside its set")
	}
}
