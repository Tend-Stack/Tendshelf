package users

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSettingsTextSize(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, _ := createOwner(t, r, goodUser("ana"))
	if got, _ := r.LoadSettings(ctx, u.ID); got.TextSize != "standard" {
		t.Fatalf("default text size = %q", got.TextSize)
	}
	s := DefaultSettings
	s.TextSize = "large"
	if err := r.SaveSettings(ctx, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.LoadSettings(ctx, u.ID); got.TextSize != "large" {
		t.Fatalf("saved text size = %q", got.TextSize)
	}
	s.TextSize = "huge"
	if err := r.SaveSettings(ctx, u.ID, s); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("huge saved: %v", err)
	}
	// A stored junk value reads as standard.
	q := fmt.Sprintf(`UPDATE user_settings SET value_json = '"huge"' WHERE user_id = '%s' AND key = 'ui.text_size'`, u.ID)
	if _, err := r.st.Writer().ExecContext(ctx, q); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.LoadSettings(ctx, u.ID); got.TextSize != "standard" {
		t.Fatalf("junk text size = %q", got.TextSize)
	}
}

func TestRepoLook(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, _ := createOwner(t, r, goodUser("ana"))
	put := func(key, raw string) {
		t.Helper()
		if _, err := r.st.Writer().ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, ?, ?, 1)
			ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json`, u.ID, key, raw); err != nil {
			t.Fatal(err)
		}
	}
	look := func(want Look, name string) {
		t.Helper()
		if got, err := r.Look(ctx, u.ID); err != nil || got != want {
			t.Errorf("%s: Look = %+v, %v; want %+v", name, got, err, want)
		}
	}
	look(Look{}, "absent")
	put("ui.theme", `"sepia"`)
	look(Look{Theme: "sepia"}, "theme only")
	put("ui.text_size", `"large"`)
	look(Look{Theme: "sepia", TextSize: "large"}, "both")
	put("ui.text_size", `"huge"`)
	look(Look{Theme: "sepia"}, "invalid size")
}
