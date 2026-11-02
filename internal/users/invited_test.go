package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestSuggestUsername(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{"Rosa", "rosa-"},
		{"Rosa María", "rosamaria-"},
		{"Ñandú Çelik", "nanducelik-"},
		{"Zoë Üner", "zoeuner-"},
		{"Al", "listener-"},
		{"王小明", "listener-"},
		{"A very long family name indeed", "averylongfamilynamei-"},
		{"  --  ", "listener-"},
	} {
		got := SuggestUsername(tc.name)
		if !strings.HasPrefix(got, tc.prefix) {
			t.Errorf("SuggestUsername(%q) = %q, want prefix %q", tc.name, got, tc.prefix)
		}
		if !usernameRE.MatchString(got) {
			t.Errorf("SuggestUsername(%q) = %q does not match the username rule", tc.name, got)
		}
		if len(got) != len(tc.prefix)+4 {
			t.Errorf("SuggestUsername(%q) = %q: want 4 random characters", tc.name, got)
		}
	}
}

func TestCreateInvitedAndFirstCredentials(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	var u User
	err := r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var e error
		u, e = r.CreateInvited(ctx, tx, NewUser{DisplayName: "Rosa", Email: "rosa@example.org", Role: RoleMember}, "")
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleMember || !usernameRE.MatchString(u.Username) || u.Email != "rosa@example.org" {
		t.Fatalf("user = %+v", u)
	}
	if has, err := r.HasPassword(ctx, u.ID); err != nil || has {
		t.Fatalf("HasPassword = %v, %v; want false", has, err)
	}
	if _, hash, err := r.ByUsername(ctx, u.Username); err != nil || hash != "" {
		t.Fatalf("ByUsername hash = %q, %v", hash, err)
	}
	for _, role := range []Role{RoleOwner, "", "root"} {
		err := r.st.WriteTx(ctx, func(tx *sql.Tx) error {
			_, e := r.CreateInvited(ctx, tx, NewUser{DisplayName: "X", Role: role}, "")
			return e
		})
		if !errors.Is(err, ErrRole) {
			t.Errorf("role %q: err = %v, want ErrRole", role, err)
		}
	}

	hash, _ := r.HashPassword(ctx, "a long enough password")
	set := func(id, name string) error {
		return r.st.WriteTx(ctx, func(tx *sql.Tx) error { return r.SetFirstCredentials(ctx, tx, id, name, hash) })
	}
	if err := set(u.ID, "rosa-home"); err != nil {
		t.Fatal(err)
	}
	if has, _ := r.HasPassword(ctx, u.ID); !has {
		t.Fatal("HasPassword = false after SetFirstCredentials")
	}
	if err := set(u.ID, "rosa-other"); !errors.Is(err, ErrHasPassword) {
		t.Fatalf("second set: err = %v, want ErrHasPassword", err)
	}
	if got, _ := r.ByID(ctx, u.ID); got.Username != "rosa-home" {
		t.Fatalf("username changed to %q", got.Username)
	}
	if err := set("usr_nobody", "nobody-here"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrNotFound", err)
	}

	// a username already in use is a field error
	var v User
	_ = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var e error
		v, e = r.CreateInvited(ctx, tx, NewUser{DisplayName: "Pablo", Role: RoleGuest}, "")
		return e
	})
	var fe FieldErrors
	if err := set(v.ID, "ROSA-HOME"); !errors.As(err, &fe) || fe["username"] != "taken" {
		t.Fatalf("taken username: err = %v", err)
	}
}
