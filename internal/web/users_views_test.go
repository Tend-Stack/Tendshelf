package web

import (
	"errors"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var usersNow0 = time.Date(2026, time.November, 10, 15, 0, 0, 0, time.UTC)

func TestUsersLastActiveText(t *testing.T) {
	now := usersNow0
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	day := 24 * time.Hour
	cases := []struct {
		name         string
		active, live int64
		want         string
		isLive       bool
	}{
		{"listening 30 s ago", ago(30 * time.Second), ago(30 * time.Second), "Listening now", true},
		{"listening just past the window", ago(10 * time.Minute), ago(10 * time.Minute), "Today", false},
		{"seen 2 min ago", ago(2 * time.Minute), 0, "Now", false},
		{"seen this morning", ago(5 * time.Hour), 0, "Today", false},
		{"yesterday", ago(day + time.Hour), 0, "Yesterday", false},
		{"three days", ago(3*day + time.Hour), 0, "3 days ago", false},
		{"nine days", ago(9 * day), 0, "Last week", false},
		{"three weeks", ago(22 * day), 0, "3 weeks ago", false},
		{"long ago", ago(90 * day), 0, "Over a month ago", false},
		{"never", 0, 0, "Never", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, live := lastActiveText(now, c.active, c.live)
			if got != c.want || live != c.isLive {
				t.Fatalf("got %q live=%v, want %q live=%v", got, live, c.want, c.isLive)
			}
		})
	}
}

func TestUsersMonthText(t *testing.T) {
	for ms, want := range map[int64]string{0: "0 h", 29_000: "1 min", 40 * 60_000: "40 min", 3_600_000: "1 h", 12*3_600_000 + 1_000_000: "12 h"} {
		if got := monthText(ms); got != want {
			t.Errorf("monthText(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestUsersParseUntil(t *testing.T) {
	now := usersNow0
	end, err := parseUntil("2026-11-12", now)
	if err != nil || end != time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("future day: %d %v", end, err)
	}
	if got := untilValue(end, time.UTC); got != "2026-11-12" {
		t.Fatalf("untilValue = %q", got)
	}
	if end, err := parseUntil("2026-11-10", now); err != nil || end == 0 {
		t.Fatalf("today works until midnight: %d %v", end, err)
	}
	if end, err := parseUntil("  ", now); err != nil || end != 0 {
		t.Fatalf("empty means no end: %d %v", end, err)
	}
	for _, bad := range []string{"2026-11-09", "10/11/2026", "soon", "2026-13-40"} {
		if _, err := parseUntil(bad, now); !errors.Is(err, errUntilDay) {
			t.Errorf("parseUntil(%q) = %v, want errUntilDay", bad, err)
		}
	}
	if got := renewEnd(now); got != time.Date(2026, 12, 11, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("renewEnd = %v", time.UnixMilli(got).UTC())
	}
}

func TestUsersAccessText(t *testing.T) {
	member := users.User{Role: users.RoleMember}
	cases := []struct {
		name string
		p    share.Person
		want string
	}{
		{"owner", share.Person{User: users.User{Role: users.RoleOwner}}, "Everything"},
		{"nothing", share.Person{User: member}, "Nothing shared yet"},
		{"ended", share.Person{User: member, Grants: 2}, "Access ended"},
		{"everything", share.Person{User: member, Grants: 1, ActiveGrants: 1, Everything: true}, "Everything"},
		{"one shelf", share.Person{User: member, Grants: 1, ActiveGrants: 1, FirstName: "For Mamá", Books: 2}, "For Mamá · 2 books"},
		{"one book", share.Person{User: member, Grants: 1, ActiveGrants: 1, FirstName: "Dune", Books: 1}, "Dune · 1 book"},
		{"several", share.Person{User: member, Grants: 3, ActiveGrants: 3, FirstName: "Kids", Books: 40}, "Kids and 2 more · 40 books"},
	}
	for _, c := range cases {
		if got := accessText(c.p); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestUsersSubtitleText(t *testing.T) {
	for _, c := range []struct {
		people, waiting, listening int
		want                       string
	}{
		{5, 1, 3, "5 people · 1 invite waiting · 3 listening this week"},
		{1, 0, 0, "1 person"},
		{2, 4, 0, "2 people · 4 invites waiting"},
		{3, 0, 2, "3 people · 2 listening this week"},
	} {
		if got := subtitleText(c.people, c.waiting, c.listening); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestUsersGrantRows(t *testing.T) {
	now := usersNow0
	end := time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC).UnixMilli()
	past := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	rows := grantRows([]share.NamedGrant{
		{Grant: share.Grant{ID: "a", Scope: share.GrantAll}},
		{Grant: share.Grant{ID: "b", Scope: share.GrantLibrary, ExpiresAt: end, CanDownload: true}, Name: "Kids", Books: 12},
		{Grant: share.Grant{ID: "c", Scope: share.GrantShelf, ExpiresAt: past}, Name: "For Mamá", OwnerName: "Daniel", Books: 1},
		{Grant: share.Grant{ID: "d", Scope: share.GrantBook}, Name: "Dune"},
	}, now)
	want := []struct{ name, kind, ends string }{
		{"Everything", "Everything on this server", "No end date"},
		{"Kids", "Library · 12 books", "Until 2 Dec"},
		{"For Mamá", "Daniel's shelf · 1 book", "Ended 31 Oct"},
		{"Dune", "Book", "No end date"},
	}
	for i, w := range want {
		if rows[i].Name != w.name || rows[i].Kind != w.kind || rows[i].Ends != w.ends {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
	if !rows[2].Expired || rows[1].Expired || !rows[1].Download {
		t.Fatal("flags wrong")
	}
}
