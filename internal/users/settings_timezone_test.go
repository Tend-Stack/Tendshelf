package users

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidTimeZone(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"Europe/Madrid", true}, {"UTC", true}, {"Asia/Tokyo", true},
		{"", false}, {"Local", false}, {"Mars/Base", false}, {strings.Repeat("a", 65), false},
	} {
		if got := ValidTimeZone(c.in); got != c.want {
			t.Errorf("ValidTimeZone(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSettingsTimeZone(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	s := DefaultSettings
	s.TimeZone = "Mars/Base"
	if err := r.SaveSettings(ctx, u.ID, s); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("invalid zone: %v", err)
	}
	s.TimeZone = "Europe/Madrid"
	if err := r.SaveSettings(ctx, u.ID, s); err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadSettings(ctx, u.ID)
	if err != nil || got.TimeZone != "Europe/Madrid" {
		t.Fatalf("load: %+v %v", got, err)
	}
	l, err := r.Look(ctx, u.ID)
	if err != nil || l.TimeZone != "Europe/Madrid" {
		t.Fatalf("look: %+v %v", l, err)
	}
}
