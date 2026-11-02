package share

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// sharedSources runs SharedBooks for u and returns "book/sharer" strings, sorted.
func sharedSources(t *testing.T, f *fixture, u users.User) []string {
	t.Helper()
	flt := f.c.SharedBooks(u)
	rows, err := f.st.Reader().Query(flt.SQL, flt.Args...)
	if err != nil {
		t.Fatalf("%s: %v\n%s", u.ID, err, flt.SQL)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var book string
		var at int64
		var sharer *string
		if err := rows.Scan(&book, &at, &sharer); err != nil {
			t.Fatal(err)
		}
		who := "-"
		if sharer != nil {
			who = *sharer
		}
		out = append(out, book+"/"+who)
	}
	sort.Strings(out)
	return out
}

func TestSharedBooks(t *testing.T) {
	f := openFixture(t)
	for _, c := range []struct {
		user     string
		args     int
		flagged  bool
		disabled bool
	}{
		{"u_own", 5, true, false}, {"u_adm", 5, true, false}, {"u_lib", 5, true, false},
		{"u_g", 4, false, false}, {"u_dis", 0, false, true},
	} {
		flt := f.c.SharedBooks(f.user(c.user))
		if len(flt.Args) != c.args {
			t.Errorf("%s: %d args, want %d", c.user, len(flt.Args), c.args)
		}
		if got := strings.Contains(flt.SQL, "shared = 1"); got != c.flagged {
			t.Errorf("%s: shared-flag branch %v, want %v", c.user, got, c.flagged)
		}
		if got := strings.HasSuffix(flt.SQL, "WHERE 0"); got != c.disabled {
			t.Errorf("%s: empty %v, want %v", c.user, got, c.disabled)
		}
		if n := strings.Count(flt.SQL, "?"); n != len(flt.Args) {
			t.Errorf("%s: %d placeholders, %d args", c.user, n, len(flt.Args))
		}
	}
	if flt := f.c.SharedBooks(users.User{ID: "x", Role: "weird"}); !strings.HasSuffix(flt.SQL, "WHERE 0") {
		t.Error("an unknown role must share nothing")
	}
}

func TestSharedBooksSources(t *testing.T) {
	f := openFixture(t)
	// Grants only: the shelf grant and the book grants; library and all grants are not shares.
	for user, want := range map[string][]string{
		"u_shelf": {"bk_b2/-"}, "u_g": {"bk_b1/-"}, "u_lib": nil, "u_all": nil,
		"u_exp": nil, "u_memshelf": nil, "u_none": nil, "u_dis": nil,
	} {
		if got := sharedSources(t, f, f.user(user)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", user, got, want)
		}
	}
	// A creator is named; a removed creator is NULL.
	f.exec(t, `UPDATE grants SET created_by = 'u_own' WHERE user_id IN ('u_shelf', 'u_g')`)
	if got := sharedSources(t, f, f.user("u_shelf")); !reflect.DeepEqual(got, []string{"bk_b2/u_own"}) {
		t.Errorf("u_shelf with creator: %v", got)
	}
	// The shared flag: members and staff get it, a guest and the owner of the shelf never.
	f.exec(t, `UPDATE shelves SET shared = 1 WHERE id = 'sh_adm'`)
	for user, want := range map[string][]string{
		"u_lib": {"bk_b2/u_adm"}, "u_own": {"bk_b2/u_adm"}, "u_adm": nil, "u_g": {"bk_b1/u_own"},
		"u_shelf": {"bk_b2/u_adm", "bk_b2/u_own"},
	} {
		if got := sharedSources(t, f, f.user(user)); !reflect.DeepEqual(got, want) {
			t.Errorf("flag, %s: %v, want %v", user, got, want)
		}
	}
	// A member-owned shelf with the flag is no source, and a demoted owner stops sharing.
	f.exec(t, `UPDATE shelves SET shared = 1 WHERE id = 'sh_mem'`)
	if got := sharedSources(t, f, f.user("u_lib")); !reflect.DeepEqual(got, []string{"bk_b2/u_adm"}) {
		t.Errorf("member shelf flag: %v", got)
	}
	f.exec(t, `UPDATE users SET role = 'member' WHERE id = 'u_adm'`)
	if got := sharedSources(t, f, f.user("u_lib")); got != nil {
		t.Errorf("demoted owner: %v", got)
	}
	f.exec(t, `UPDATE users SET role = 'admin', disabled = 1 WHERE id = 'u_adm'`)
	if got := sharedSources(t, f, f.user("u_lib")); got != nil {
		t.Errorf("disabled owner: %v", got)
	}
}

// TestSharedBooksAgreesWithCan: every book the grant branches name for a member or guest passes Can.
func TestSharedBooksAgreesWithCan(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	for id, u := range f.users {
		if u.Disabled {
			continue
		}
		for _, src := range sharedSources(t, f, u) {
			book := strings.SplitN(src, "/", 2)[0]
			if !f.c.Can(ctx, u, ViewBook, Target{Kind: TargetBook, ID: book, LibraryID: fixtureBooks[book]}) {
				t.Errorf("%s: %s is shared but Can(ViewBook) refuses", id, book)
			}
		}
	}
}

func TestSharerSQLUnchangedCovers(t *testing.T) {
	const want = "share_o.disabled = 0 AND share_o.role IN ('owner','admin')"
	if got := sharerSQL("share_o"); got != want {
		t.Errorf("sharerSQL = %q", got)
	}
	if got := SharerSQL("o"); got != "o.disabled = 0 AND o.role IN ('owner','admin')" {
		t.Errorf("SharerSQL = %q", got)
	}
	for name, sql := range map[string]string{"coversSQL": coversSQL("g", "b"), "libraryCoversSQL": libraryCoversSQL("l")} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s lost the sharer condition", name)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("SharerSQL accepted an unsafe alias")
		}
	}()
	_ = SharerSQL("share_x")
}
