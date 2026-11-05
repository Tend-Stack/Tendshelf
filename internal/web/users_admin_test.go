package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// usersEnv is an app with an owner, an admin, a library of three books and a few people.
type usersEnv struct {
	t       *testing.T
	a       *testApp
	owner   *http.Cookie
	admin   *http.Cookie
	ownerID string
	adminID string
	lib     library.Library
	books   []library.Book
}

func usersNewEnv(t *testing.T) *usersEnv {
	t.Helper()
	a := newTestApp(t)
	e := &usersEnv{t: t, a: a}
	e.owner = a.as(t, users.RoleOwner)
	e.admin = a.as(t, users.RoleAdmin)
	e.ownerID = sweepUserID(t, a, e.owner)
	e.adminID = sweepUserID(t, a, e.admin)
	l, f := librarytest.NewLibrary(t, a.lib, "Family library", adminAudioDir(t, true))
	e.lib = l
	// Distinct times, newest first, so "the newest book" (an invite's featured title) never depends on random ids.
	for i, title := range []string{"Usersword One", "Usersword Two", "Usersword Three"} {
		e.books = append(e.books, librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Files: 1, DurationMs: sweepHourMs, AddedAt: int64(7 - i)}))
	}
	return e
}

// person adds a member or listener with a session and returns the id and the cookie.
func (e *usersEnv) person(role users.Role) (string, *http.Cookie) {
	e.t.Helper()
	ck := e.a.as(e.t, role)
	return sweepUserID(e.t, e.a, ck), ck
}

func (e *usersEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.a.st.Writer().Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *usersEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.a.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *usersEnv) get(ck *http.Cookie, path string) *httptest.ResponseRecorder {
	return e.a.do(http.MethodGet, path, nil, nil, ck)
}

// post sends a form with the cookie's CSRF token; hx adds the htmx request header.
func (e *usersEnv) post(ck *http.Cookie, path string, form url.Values, hx bool) *httptest.ResponseRecorder {
	e.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", e.a.tokenFor(e.t, ck))
	return e.a.do(http.MethodPost, path, form, func(r *http.Request) {
		if hx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

func usersWant(t *testing.T, rec *httptest.ResponseRecorder, code int, what string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("%s: got %d, want %d", what, rec.Code, code)
	}
}

func TestUsersNobodyActsOnThemselves(t *testing.T) {
	e := usersNewEnv(t)
	for _, who := range []struct {
		name string
		ck   *http.Cookie
		id   string
	}{{"owner", e.owner, e.ownerID}, {"admin", e.admin, e.adminID}} {
		base := "/admin/users/" + who.id
		for _, p := range []string{"", "/disable", "/stop"} {
			usersWant(t, e.get(who.ck, base+p), 404, who.name+" GET "+p)
		}
		for _, p := range []string{"/role", "/disable", "/enable", "/stop", "/grants", "/grants/x", "/grants/x/renew", "/grants/x/remove"} {
			usersWant(t, e.post(who.ck, base+p, url.Values{"role": {"guest"}, "scope": {"all"}}, false), 404, who.name+" POST "+p)
		}
	}
	// The owner and the admin are unchanged.
	if n := e.count(`SELECT count(*) FROM users WHERE id IN (?, ?) AND disabled = 0 AND role IN ('owner','admin')`, e.ownerID, e.adminID); n != 2 {
		t.Fatalf("an account changed: %d", n)
	}
}

func TestUsersRoleChange(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.person(users.RoleMember)
	e.exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at) VALUES ('gr_u1', ?, 'library', ?, 1, 0, 1, 1)`, id, e.lib.ID)

	// An admin may not make an admin; nothing is written.
	usersWant(t, e.post(e.admin, "/admin/users/"+id+"/role", url.Values{"role": {"admin"}}, false), 404, "admin promotes")
	if e.count(`SELECT count(*) FROM users WHERE id = ? AND role = 'member'`, id) != 1 || e.count(`SELECT count(*) FROM audit_log WHERE action = 'user.role'`) != 0 {
		t.Fatal("the refused promotion wrote something")
	}
	// The owner may; the grants go with it.
	usersWant(t, e.post(e.owner, "/admin/users/"+id+"/role", url.Values{"role": {"admin"}}, false), 303, "owner promotes")
	if e.count(`SELECT count(*) FROM users WHERE id = ? AND role = 'admin'`, id) != 1 {
		t.Fatal("role not changed")
	}
	if e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id) != 0 {
		t.Fatal("grants of the new admin were kept")
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'user.role'`) != 1 || e.count(`SELECT count(*) FROM audit_log WHERE action = 'grant.revoke'`) != 1 {
		t.Fatal("audit rows missing")
	}
	// Now an admin target: the admin can no longer touch them; the owner demotes.
	usersWant(t, e.get(e.admin, "/admin/users/"+id), 404, "admin sees an admin")
	usersWant(t, e.post(e.owner, "/admin/users/"+id+"/role", url.Values{"role": {"guest"}}, false), 303, "owner demotes")
	// A bad role is 422 in plain words; a role of "owner" is refused as not found.
	rec := e.post(e.owner, "/admin/users/"+id+"/role", url.Values{"role": {"king"}}, false)
	usersWant(t, rec, 422, "bad role")
	if !strings.Contains(rec.Body.String(), "ts-users__error") || !strings.Contains(rec.Body.String(), usersErrRole) {
		t.Fatal("the page lacks the message")
	}
	usersWant(t, e.post(e.owner, "/admin/users/"+id+"/role", url.Values{"role": {"owner"}}, false), 422, "make an owner")
	if e.count(`SELECT count(*) FROM users WHERE role = 'owner'`) != 1 {
		t.Fatal("a second owner")
	}
}

func TestUsersOwnerNeverChanged(t *testing.T) {
	e := usersNewEnv(t)
	for _, p := range []string{"/role", "/disable"} {
		usersWant(t, e.post(e.admin, "/admin/users/"+e.ownerID+p, url.Values{"role": {"member"}}, false), 404, "admin on owner "+p)
	}
	if e.count(`SELECT count(*) FROM users WHERE id = ? AND role = 'owner' AND disabled = 0`, e.ownerID) != 1 {
		t.Fatal("the owner changed")
	}
}

func TestUsersDisableAndEnable(t *testing.T) {
	e := usersNewEnv(t)
	id, ck := e.person(users.RoleMember)
	e.exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('dv1', ?, 'Phone', 'mobile', randomblob(32), 1)`, id)
	e.exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at) VALUES ('gr_u1', ?, 'library', ?, 1, 0, 1, 1)`, id, e.lib.ID)
	usersWant(t, e.get(ck, "/"), 200, "member before")

	rec := e.get(e.owner, "/admin/users/"+id+"/disable")
	usersWant(t, rec, 200, "confirm page")
	if !strings.Contains(rec.Body.String(), "can&#39;t sign in until you turn the account back on") && !strings.Contains(rec.Body.String(), "can't sign in until you turn the account back on") {
		t.Fatal("the confirm lacks its words")
	}
	usersWant(t, e.post(e.owner, "/admin/users/"+id+"/disable", nil, false), 303, "disable")
	if e.count(`SELECT count(*) FROM sessions WHERE user_id = ?`, id) != 0 {
		t.Fatal("sessions left")
	}
	if e.count(`SELECT count(*) FROM devices WHERE user_id = ? AND revoked_at IS NOT NULL AND token_hash IS NULL`, id) != 1 {
		t.Fatal("device token not revoked")
	}
	if e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id) != 1 {
		t.Fatal("grants must stay")
	}
	if got := e.get(ck, "/"); got.Code != 303 || !strings.HasPrefix(got.Header().Get("Location"), "/login") {
		t.Fatalf("the signed-out person gets %d %q", got.Code, got.Header().Get("Location"))
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action IN ('user.disable','user.signout')`) != 2 {
		t.Fatal("audit rows missing")
	}

	usersWant(t, e.post(e.owner, "/admin/users/"+id+"/enable", nil, false), 303, "enable")
	var tok string
	err := e.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewSessions(e.a.st, nil).Create(context.Background(), tx, id, "again")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	usersWant(t, e.get(&http.Cookie{Name: auth.CookieName, Value: tok}, "/"), 200, "after enable")
}

func TestUsersRecheckAfterRoleChange(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.person(users.RoleMember)
	usersWant(t, e.get(e.admin, "/admin/users/"+id), 200, "page before")
	audits := e.count(`SELECT count(*) FROM audit_log`)
	// Between the page and the click the person becomes an admin.
	e.exec(`UPDATE users SET role = 'admin' WHERE id = ?`, id)
	usersWant(t, e.post(e.admin, "/admin/users/"+id+"/role", url.Values{"role": {"guest"}}, false), 404, "stale role change")
	usersWant(t, e.post(e.admin, "/admin/users/"+id+"/disable", nil, false), 404, "stale disable")
	if e.count(`SELECT count(*) FROM users WHERE id = ? AND role = 'admin' AND disabled = 0`, id) != 1 || e.count(`SELECT count(*) FROM audit_log`) != audits {
		t.Fatal("a stale change wrote something")
	}
}

func TestUsersGrants(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.person(users.RoleGuest)
	base := "/admin/users/" + id

	// Add a library grant with an end date.
	until := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	usersWant(t, e.post(e.admin, base+"/grants", url.Values{"scope": {"library"}, "library": {e.lib.ID}, "download": {"1"}, "until": {until}}, false), 303, "add")
	var gid string
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM grants WHERE user_id = ?`, id).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM grants WHERE id = ? AND can_download = 1 AND can_read = 0 AND expires_at > ?`, gid, time.Now().UnixMilli()) != 1 {
		t.Fatal("grant not as asked")
	}
	page := e.get(e.owner, base).Body.String()
	for _, w := range []string{"Family library", "Library · 3 books", "Until ", "Can read", "Renew for 30 days"} {
		if w == "Renew for 30 days" {
			if strings.Contains(page, w) {
				t.Fatalf("an active grant offers %q", w)
			}
			continue
		}
		if !strings.Contains(page, w) {
			t.Fatalf("the person page lacks %q", w)
		}
	}

	// Update the flags and drop the end.
	usersWant(t, e.post(e.admin, base+"/grants/"+gid, url.Values{"read": {"1"}}, false), 303, "update")
	if e.count(`SELECT count(*) FROM grants WHERE id = ? AND can_download = 0 AND can_read = 1 AND expires_at IS NULL`, gid) != 1 {
		t.Fatal("update not applied")
	}
	// An ended grant offers renewal, which keeps the flags.
	e.exec(`UPDATE grants SET created_at = 1, expires_at = 2 WHERE id = ?`, gid)
	if !strings.Contains(e.get(e.admin, base).Body.String(), "Renew for 30 days") {
		t.Fatal("no renew on an ended grant")
	}
	usersWant(t, e.post(e.admin, base+"/grants/"+gid+"/renew", nil, false), 303, "renew")
	if e.count(`SELECT count(*) FROM grants WHERE id = ? AND can_read = 1 AND expires_at > ?`, gid, time.Now().AddDate(0, 0, 30).UnixMilli()) != 1 {
		t.Fatal("renew not applied")
	}
	// Somebody else's grant id is not found.
	other, _ := e.person(users.RoleMember)
	usersWant(t, e.post(e.admin, "/admin/users/"+other+"/grants/"+gid+"/remove", nil, false), 404, "foreign grant")
	// Remove, add two, stop sharing.
	usersWant(t, e.post(e.admin, base+"/grants/"+gid+"/remove", nil, false), 303, "remove")
	if e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id) != 0 {
		t.Fatal("grant not removed")
	}
	usersWant(t, e.post(e.admin, base+"/grants", url.Values{"scope": {"all"}}, false), 303, "add all")
	usersWant(t, e.post(e.admin, base+"/grants", url.Values{"scope": {"book"}, "book": {e.books[0].ID}}, false), 303, "add book")
	usersWant(t, e.get(e.admin, base+"/stop"), 200, "stop confirm")
	usersWant(t, e.post(e.admin, base+"/stop", nil, false), 303, "stop")
	if e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id) != 0 || e.count(`SELECT count(*) FROM audit_log WHERE action = 'user.stop_sharing'`) != 1 {
		t.Fatal("stop sharing incomplete")
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'grant.put'`) != 5 || e.count(`SELECT count(*) FROM audit_log WHERE action = 'grant.revoke'`) != 3 {
		t.Fatalf("grant audit rows: put %d revoke %d", e.count(`SELECT count(*) FROM audit_log WHERE action = 'grant.put'`), e.count(`SELECT count(*) FROM audit_log WHERE action = 'grant.revoke'`))
	}
	// An admin takes no grants.
	usersWant(t, e.post(e.owner, base+"/role", url.Values{"role": {"admin"}}, false), 303, "promote")
	usersWant(t, e.post(e.owner, base+"/grants", url.Values{"scope": {"all"}}, false), 404, "grant to an admin")
}

func TestUsersFormErrors(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.person(users.RoleMember)
	path := "/admin/users/" + id + "/grants"
	past := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	for _, c := range []struct {
		name string
		form url.Values
		msg  string
	}{
		{"no scope", url.Values{}, usersErrTarget},
		{"library without a library", url.Values{"scope": {"library"}}, usersErrTarget},
		{"unknown library", url.Values{"scope": {"library"}, "library": {"nope"}}, usersErrTarget},
		{"member's shelf", url.Values{"scope": {"shelf"}, "shelf": {"nope"}}, usersErrTarget},
		{"past day", url.Values{"scope": {"all"}, "until": {past}}, usersErrDay},
		{"not a date", url.Values{"scope": {"all"}, "until": {"tomorrow"}}, usersErrDay},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := e.post(e.owner, path, c.form, false)
			usersWant(t, rec, 422, "page")
			if !strings.Contains(rec.Body.String(), `class="ts-users__error" role="alert"`) || !strings.Contains(rec.Body.String(), c.msg) {
				t.Fatal("the page lacks the message")
			}
			rec = e.post(e.owner, path, c.form, true)
			usersWant(t, rec, 422, "htmx")
			if !strings.Contains(rec.Body.String(), `hx-swap-oob="beforeend:#toasts"`) || !strings.Contains(rec.Body.String(), c.msg) || strings.Contains(rec.Body.String(), "<html") {
				t.Fatalf("htmx answer is not a toast: %.200s", rec.Body.String())
			}
		})
	}
	if e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id) != 0 {
		t.Fatal("a refused form wrote a grant")
	}
}

func TestUsersListing(t *testing.T) {
	e := usersNewEnv(t)
	rosa, _ := e.person(users.RoleGuest)
	maya, _ := e.person(users.RoleMember)
	lena, _ := e.person(users.RoleMember)
	tomas, _ := e.person(users.RoleGuest)
	e.exec(`UPDATE users SET display_name = 'Rosa' WHERE id = ?`, rosa)
	e.exec(`UPDATE users SET display_name = 'Maya' WHERE id = ?`, maya)
	e.exec(`UPDATE users SET display_name = 'Lena' WHERE id = ?`, lena)
	e.exec(`UPDATE users SET display_name = 'Tomas' WHERE id = ?`, tomas)

	sh, err := e.a.lib.CreateShelf(context.Background(), e.ownerID, "For Mamá")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range e.books[:2] {
		if err := e.a.lib.AddToShelf(context.Background(), e.ownerID, sh.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	e.exec(`INSERT INTO grants(id, user_id, scope, shelf_id, created_at, updated_at) VALUES ('g1', ?, 'shelf', ?, 1, 1)`, rosa, sh.ID)
	e.exec(`INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('g2', ?, 'all', 1, 1)`, maya)
	e.exec(`INSERT INTO grants(id, user_id, scope, book_id, expires_at, created_at, updated_at) VALUES ('g3', ?, 'book', ?, 2, 1, 1)`, tomas, e.books[2].ID)

	now := time.Now().UnixMilli()
	e.exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
		VALUES ('ls1', ?, ?, 'dev', ?, ?, 0, 1, 7200000, 100)`, maya, e.books[2].ID, now-60_000, now-5_000)

	rec := e.get(e.owner, "/admin/users")
	usersWant(t, rec, 200, "listing")
	body := rec.Body.String()
	for _, w := range []string{"Users &amp; invites", "6 people", "1 listening this week", "People · 6", "Listening now", "2 h", "Access ended",
		"Nothing shared yet", "Everything", "For Mamá · 2 books", "(you)", "Listener", "Turned off"} {
		if w == "Turned off" {
			if strings.Contains(body, w) {
				t.Fatal("nobody is turned off yet")
			}
			continue
		}
		if !strings.Contains(body, w) {
			t.Errorf("the listing lacks %q", w)
		}
	}
	if strings.Contains(body, "Usersword") {
		t.Fatal("the listing names a book")
	}
	// The owner is first; the admin's row has no link to themselves, the owner's none either.
	if strings.Contains(body, `href="/admin/users/`+e.ownerID+`"`) {
		t.Fatal("the owner links to themselves")
	}
	if !strings.Contains(body, `href="/admin/users/`+rosa+`"`) {
		t.Fatal("no link to a listener")
	}
	// An admin sees no link to the owner or another admin.
	if strings.Contains(e.get(e.admin, "/admin/users").Body.String(), `href="/admin/users/`+e.ownerID+`"`) {
		t.Fatal("the admin may open the owner")
	}
	// The fragment is the list alone.
	frag := e.get(e.owner, "/hx/admin/users").Body.String()
	if !strings.HasPrefix(strings.TrimSpace(frag), `<section id="users"`) || strings.Contains(frag, "<html") {
		t.Fatalf("fragment: %.80s", frag)
	}
	// The person page never says what anyone listens to.
	if strings.Contains(e.get(e.owner, "/admin/users/"+maya).Body.String(), "Usersword") {
		t.Fatal("the person page names a listened book")
	}
	// A book search lists matches as choices.
	found := e.get(e.owner, "/admin/users/"+rosa+"?q=Usersword").Body.String()
	if strings.Count(found, `name="book"`) != 3 {
		t.Fatalf("book search found %d", strings.Count(found, `name="book"`))
	}
}

func TestUsersSidebarAndProfile(t *testing.T) {
	e := usersNewEnv(t)
	_, member := e.person(users.RoleMember)
	_, guest := e.person(users.RoleGuest)
	now := time.Now().UnixMilli()
	invite := func(id string, exp any, used int, revoked any) {
		e.exec(`INSERT INTO invites(id, token_hash, created_by, name, role, expires_at, max_uses, use_count, revoked_at, created_at, updated_at)
			VALUES (?, randomblob(32), ?, 'N', 'member', ?, 1, ?, ?, 1, 1)`, id, e.ownerID, exp, used, revoked)
	}
	invite("i_revoked", nil, 0, 5)
	invite("i_expired", now-10, 0, nil)
	invite("i_used", nil, 1, nil)
	if body := e.get(e.owner, "/").Body.String(); !strings.Contains(body, "Users &amp; invites") || strings.Contains(body, "waiting") {
		t.Fatal("sidebar: link missing or a count for dead invites")
	}
	invite("i_live", now+60_000, 0, nil)
	body := e.get(e.owner, "/").Body.String()
	if !strings.Contains(body, "1 waiting") {
		t.Fatal("sidebar lacks '1 waiting'")
	}
	if me := e.get(e.admin, "/me").Body.String(); !strings.Contains(me, "Users &amp; invites") || !strings.Contains(me, "1 invite waiting") {
		t.Fatal("the profile lacks the Users row")
	}
	for name, ck := range map[string]*http.Cookie{"member": member, "guest": guest} {
		for _, p := range []string{"/", "/me"} {
			b := e.get(ck, p).Body.String()
			if strings.Contains(b, "/admin/users") || strings.Contains(b, "Users &amp; invites") {
				t.Fatalf("%s sees the Users link on %s", name, p)
			}
		}
		usersWant(t, e.get(ck, "/admin/users"), 404, name+" page")
	}
}
