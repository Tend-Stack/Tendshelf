package users

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadSettings(ctx, u.ID)
	if err != nil || got != DefaultSettings {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	want := Settings{Theme: "sepia", Speed: 1.25, SkipBackS: 10, SkipForwardS: 60, AutoRewind: false, NextInSeries: false, PlayerTint: false, BigButtons: true, TextSize: "large", Nav: "full"}
	if err := r.SaveSettings(ctx, u.ID, want); err != nil {
		t.Fatal(err)
	}
	got, err = r.LoadSettings(ctx, u.ID)
	if err != nil || got != want {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestSettingsValidate(t *testing.T) {
	mod := func(f func(*Settings)) Settings { s := DefaultSettings; f(&s); return s }
	tests := []struct {
		name string
		s    Settings
		ok   bool
	}{
		{"defaults", DefaultSettings, true},
		{"speed min", mod(func(s *Settings) { s.Speed = 0.5 }), true},
		{"speed max 3.5", mod(func(s *Settings) { s.Speed = 3.5 }), true},
		{"speed 1.15", mod(func(s *Settings) { s.Speed = 1.15 }), true},
		{"speed 0.49", mod(func(s *Settings) { s.Speed = 0.49 }), false},
		{"speed 3.55", mod(func(s *Settings) { s.Speed = 3.55 }), false},
		{"speed 1.07", mod(func(s *Settings) { s.Speed = 1.07 }), false},
		{"skip back 20", mod(func(s *Settings) { s.SkipBackS = 20 }), false},
		{"skip forward 20", mod(func(s *Settings) { s.SkipForwardS = 20 }), false},
		{"theme unknown", mod(func(s *Settings) { s.Theme = "neon" }), false},
		{"theme empty", mod(func(s *Settings) { s.Theme = "" }), false},
		{"text size huge", mod(func(s *Settings) { s.TextSize = "huge" }), false},
		{"text size large", mod(func(s *Settings) { s.TextSize = "large" }), true},
	}
	for _, tc := range tests {
		err := tc.s.Validate()
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, ErrInvalidSetting)) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	r := newRepo(t, Options{})
	u, _ := createOwner(t, r, goodUser("ana"))
	bad := DefaultSettings
	bad.Speed = 9
	if err := r.SaveSettings(context.Background(), u.ID, bad); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("SaveSettings invalid = %v", err)
	}
}

func TestSaveSettingsOnlyChangedKeys(t *testing.T) {
	clock := time.UnixMilli(1_000_000)
	r := newRepo(t, Options{Now: func() time.Time { return clock }})
	ctx := context.Background()
	u, _ := createOwner(t, r, goodUser("ana"))
	count := func() int {
		var n int
		if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM user_settings WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	updated := func(key string) int64 {
		var at int64
		if err := r.st.Reader().QueryRowContext(ctx, `SELECT updated_at FROM user_settings WHERE user_id = ? AND key = ?`, u.ID, key).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	// Saving the defaults stores the theme only: a chosen theme is always kept.
	if err := r.SaveSettings(ctx, u.ID, DefaultSettings); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("rows after defaults = %d, want 1", n)
	}
	clock = clock.Add(time.Minute)
	s := DefaultSettings
	s.BigButtons = true
	if err := r.SaveSettings(ctx, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("rows after one change = %d, want 2", n)
	}
	if at := updated("ui.theme"); at != 1_000_000 {
		t.Fatalf("unchanged theme was rewritten: updated_at %d", at)
	}
	if at := updated("ui.big_buttons"); at != 1_060_000 {
		t.Fatalf("big_buttons updated_at = %d", at)
	}
	// An unknown key and an invalid stored value are ignored on read.
	for _, q := range []string{
		`INSERT INTO user_settings VALUES ('%s', 'nope.key', '1', 1)`,
		`UPDATE user_settings SET value_json = '"neon"' WHERE user_id = '%s' AND key = 'ui.theme'`,
	} {
		if _, err := r.st.Writer().ExecContext(ctx, fmt.Sprintf(q, u.ID)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.LoadSettings(ctx, u.ID)
	if err != nil || got.Theme != "system" || !got.BigButtons {
		t.Fatalf("load with junk = %+v, %v", got, err)
	}
}

func TestThemeRead(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, _ := createOwner(t, r, goodUser("ana"))
	if th, err := r.Theme(ctx, u.ID); err != nil || th != "" {
		t.Fatalf("absent theme = %q, %v", th, err)
	}
	s := DefaultSettings
	s.Theme = "contrast"
	if err := r.SaveSettings(ctx, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if th, err := r.Theme(ctx, u.ID); err != nil || th != "contrast" {
		t.Fatalf("theme = %q, %v", th, err)
	}
	if _, err := r.st.Writer().ExecContext(ctx, `UPDATE user_settings SET value_json = '7' WHERE key = 'ui.theme'`); err != nil {
		t.Fatal(err)
	}
	if th, err := r.Theme(ctx, u.ID); err != nil || th != "" {
		t.Fatalf("invalid stored theme = %q, %v", th, err)
	}
}

func TestSetPassword(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, _ := createOwner(t, r, goodUser("ana"))
	hash, err := r.HashPassword(ctx, "another long password")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetPassword(ctx, r.st.Writer(), u.ID, hash); err != nil {
		t.Fatal(err)
	}
	_, stored, err := r.ByUsername(ctx, "ana")
	if err != nil || stored != hash {
		t.Fatalf("stored hash differs: %v", err)
	}
	if err := r.SetPassword(ctx, r.st.Writer(), "usr_missing", hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown account = %v", err)
	}
}

func TestFeelSkipQuiet(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadSettings(ctx, u.ID)
	if err != nil || got.SkipQuiet {
		t.Fatalf("default = %+v, %v", got, err)
	}
	got.SkipQuiet = true
	if err := r.SaveSettings(ctx, u.ID, got); err != nil {
		t.Fatal(err)
	}
	if got, err = r.LoadSettings(ctx, u.ID); err != nil || !got.SkipQuiet {
		t.Fatalf("saved true loads %+v, %v", got, err)
	}
	if _, err := r.st.Writer().Exec(`UPDATE user_settings SET value_json = '"yes"' WHERE user_id = ? AND key = ?`, u.ID, keyQuietSkip); err != nil {
		t.Fatal(err)
	}
	if got, err = r.LoadSettings(ctx, u.ID); err != nil || got.SkipQuiet {
		t.Fatalf("stored \"yes\" loads %+v, %v", got, err)
	}
}
