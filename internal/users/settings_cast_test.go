package users

import (
	"context"
	"testing"
)

func TestSettingsCastDefaultOff(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadSettings(ctx, u.ID)
	if err != nil || got.Cast {
		t.Fatalf("default = %+v, %v", got, err)
	}
	got.Cast = true
	if err := r.SaveSettings(ctx, u.ID, got); err != nil {
		t.Fatal(err)
	}
	if got, err = r.LoadSettings(ctx, u.ID); err != nil || !got.Cast {
		t.Fatalf("saved true loads %+v, %v", got, err)
	}
}
