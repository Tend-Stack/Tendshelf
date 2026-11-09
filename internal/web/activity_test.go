package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// activitySweepRows are the route matrix rows of Activity (S9): owner and admin read it; member and guest get 404.
func activitySweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	return []sweepRow{
		sweepGet("GET /admin/activity", func(*sweepEnv, int) string { return "/admin/activity" }, staff),
		{Pattern: "GET /admin/activity", Label: "filtered", Method: http.MethodGet,
			Path: func(*sweepEnv, int) string { return "/admin/activity?kind=books&page=2" }, Want: staff},
		{Pattern: "GET /admin/activity", Label: "unknown kind", Method: http.MethodGet,
			Path: func(*sweepEnv, int) string { return "/admin/activity?kind=nope" }, Want: sweepStaff(http.StatusNotFound, http.StatusNotFound)},
	}
}

func activitySeed(t *testing.T, a *testApp, at int64, user, action, target string, detail any) {
	t.Helper()
	if err := db.Audit(t.Context(), a.st.Writer(), db.AuditEntry{At: at, UserID: user, Action: action, Target: target, Detail: detail}); err != nil {
		t.Fatal(err)
	}
}

func TestActivityRoles(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	activitySeed(t, a, 1000, sweepUserID(t, a, owner), "invite.create", "invite:x", map[string]any{"role": "member"})
	contributor := a.as(t, users.RoleMember)
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, a, contributor)); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"member": a.as(t, users.RoleMember), "contributor": contributor, "guest": a.as(t, users.RoleGuest)} {
		rec := a.do(http.MethodGet, "/admin/activity", nil, nil, ck)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "invitation") {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := a.do(http.MethodGet, "/admin/activity", nil, nil); rec.Code == http.StatusOK {
		t.Errorf("signed out = 200")
	}
	for _, role := range []users.Role{users.RoleOwner, users.RoleAdmin} {
		rec := a.do(http.MethodGet, "/admin/activity", nil, nil, a.as(t, role))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "made an invitation for a member") {
			t.Errorf("%s = %d", role, rec.Code)
		}
	}
}

func TestActivityFilterAndFallback(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	other := a.as(t, users.RoleMember)
	otherID := sweepUserID(t, a, other)
	activitySeed(t, a, 1000, otherID, "auth.login", otherID, map[string]string{"ip": "203.0.113.9", "via": "pair"})
	activitySeed(t, a, 2000, ownerID, "user.role", "user:"+otherID, map[string]any{"from": "member", "to": "admin"})
	activitySeed(t, a, 3000, ownerID, "mystery.thing", "", nil)
	activitySeed(t, a, 4000, "", "setup.code_failed", "", map[string]string{"ip": "203.0.113.9"})
	activitySeed(t, a, 5000, "usr_gone", "auth.logout", "usr_gone", nil)

	get := func(q string) string {
		rec := a.do(http.MethodGet, "/admin/activity"+q, nil, nil, owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", q, rec.Code)
		}
		return rec.Body.String()
	}
	all := get("")
	for _, want := range []string{"Something changed: mystery.thing", "A wrong setup code was tried", "Someone who has left signed out",
		"signed in by pairing a device", "made member-"} {
		if !strings.Contains(all, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(all, "203.0.113.9") {
		t.Error("an address is on the page")
	}
	if !strings.Contains(all, `datetime="1970-01-01T00:00:01Z"`) || !strings.Contains(all, `title="1 Jan 1970, 00:00 UTC"`) {
		t.Error("time element is missing its machine time or exact title")
	}
	if !strings.Contains(all, `href="/admin/users/`+otherID+`"`) {
		t.Error("target person is not linked")
	}
	byOwner := get("?user=" + ownerID)
	if !strings.Contains(byOwner, "mystery.thing") || strings.Contains(byOwner, "signed in by pairing") {
		t.Error("person filter did not narrow the list")
	}
	if kind := get("?kind=people"); !strings.Contains(kind, "admin") || strings.Contains(kind, "mystery.thing") {
		t.Error("kind filter did not narrow the list")
	}
	if got := get("?user=nobody"); !strings.Contains(got, "Nothing matches.") {
		t.Error("no empty state for a filter with no rows")
	}
}

func TestActivityPagingAndEmpty(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	if _, err := a.st.Writer().Exec(`DELETE FROM audit_log`); err != nil { // setting the owner up wrote rows
		t.Fatal(err)
	}
	if body := a.do(http.MethodGet, "/admin/activity", nil, nil, owner).Body.String(); !strings.Contains(body, "Nothing has happened yet.") {
		t.Error("no empty state")
	}
	for i := 1; i <= activityPageSize+3; i++ {
		activitySeed(t, a, int64(i*1000), ownerID, "auth.logout", ownerID, nil)
	}
	const row = `class="body ts-activity__what"`
	first := a.do(http.MethodGet, "/admin/activity", nil, nil, owner).Body.String()
	if n := strings.Count(first, row); n != activityPageSize {
		t.Errorf("first page rows = %d", n)
	}
	if !strings.Contains(first, `href="/admin/activity?page=2"`) || strings.Contains(first, ">Newer<") {
		t.Error("first page paging links wrong")
	}
	second := a.do(http.MethodGet, "/admin/activity?page=2", nil, nil, owner).Body.String()
	if n := strings.Count(second, row); n != 3 {
		t.Errorf("second page rows = %d", n)
	}
	if strings.Contains(second, ">Older<") || !strings.Contains(second, ">Newer<") {
		t.Error("second page paging links wrong")
	}
}

func TestActivityBookLinkOnlyWhenBookExists(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	_, f := librarytest.NewLibrary(t, a.lib, "Activity library", t.TempDir())
	book := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Activityword Title", DurationMs: sweepHourMs})
	activitySeed(t, a, 1000, ownerID, "book.details.apply", "book:"+book.ID, nil)
	activitySeed(t, a, 2000, ownerID, "book.cover.choose", "bk_gone", nil)
	body := a.do(http.MethodGet, "/admin/activity", nil, nil, owner).Body.String()
	if !strings.Contains(body, `href="/books/`+book.ID+`"`) || !strings.Contains(body, "Activityword Title") {
		t.Error("owner sees no book link")
	}
	if strings.Contains(body, "/books/bk_gone") {
		t.Error("link to a book that is gone")
	}
}

func TestActivitySentence(t *testing.T) {
	who := activityWho{Actor: "Wilkin", Target: "Ana", Other: "Ben"}
	for _, c := range []struct {
		action string
		d      map[string]any
		want   string
	}{
		{"auth.login", nil, "Wilkin signed in"},
		{"auth.login", map[string]any{"via": "sign_in_link"}, "Wilkin signed in with a sign-in link"},
		{"user.role", map[string]any{"to": "admin"}, "Wilkin made Ana admin"},
		{"user.stop_sharing", nil, "Wilkin stopped sharing with Ana"},
		{"grant.put", map[string]any{"user": "u"}, "Wilkin shared with Ben"},
		{"invite.create", map[string]any{"role": "guest"}, "Wilkin made an invitation for a listener"},
		{"settings.online", map[string]any{"on": true}, "Wilkin turned online lookups on"},
		{"settings.online", map[string]any{"on": false}, "Wilkin turned online lookups off"},
		{"shelf.share", map[string]any{"shared": false}, "Wilkin stopped sharing a shelf"},
		{"auth.device_revoked", nil, "Wilkin turned off a device of Ana"},
		{"brand.new_action", nil, "Something changed: brand.new_action"},
	} {
		if got := activitySentence(c.action, who, c.d); got != c.want {
			t.Errorf("%s %v = %q, want %q", c.action, c.d, got, c.want)
		}
	}
	if got := activitySentence("user.role", activityWho{Actor: "Wilkin"}, map[string]any{"to": "member"}); got != "Wilkin made someone member" {
		t.Errorf("unknown target = %q", got)
	}
}
