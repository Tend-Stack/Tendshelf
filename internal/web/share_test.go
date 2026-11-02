package web

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// sharepage tests: the Share & invite page (P3-4a). The helpers are prefixed sharepage so they
// cannot collide with the ones other packages of tests add.

var sharepageLinkRe = regexp.MustCompile(`value="http://example\.com/invite/([A-Za-z0-9_-]{43})"`)

// sharepageForm is a valid form for a listener to the whole library; extra overrides fields.
func sharepageForm(e *inviteWeb, extra url.Values) url.Values {
	f := url.Values{"name": {"Rosa"}, "role": {"guest"}, "scope": {"library"}, "library": {e.lib.ID}, "read": {"1"}, "expires": {"7d"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

// sharepageToken reads the raw token out of the page, failing when there is none.
func sharepageToken(t *testing.T, body string) string {
	t.Helper()
	m := sharepageLinkRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no invite link in the page")
	}
	return m[1]
}

func (e *inviteWeb) sharepageName(id string) string {
	e.t.Helper()
	var n string
	if err := e.a.st.Reader().QueryRow(`SELECT display_name FROM users WHERE id = ?`, id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// sharepageGrant gives a person a grant straight in the database.
func (e *inviteWeb) sharepageGrant(user, scope, column, target string) string {
	e.t.Helper()
	id := "gr_" + user + "_" + scope + target
	e.exec(`INSERT INTO grants(id, user_id, scope, `+column+`, can_download, can_read, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 0, 1, 1)`,
		id, user, scope, target)
	return id
}

func TestSharePageRefusals(t *testing.T) {
	e := inviteNewWeb(t)
	_, member := e.person(users.RoleMember)
	_, guest := e.person(users.RoleGuest)
	before := e.count(`SELECT count(*) FROM invites`)
	book := "/books/" + e.books[0].ID + "/share"
	for _, ck := range []*http.Cookie{member, guest} {
		for _, p := range []string{"/share", book} {
			usersWant(t, e.get(ck, p), http.StatusNotFound, "GET "+p)
			usersWant(t, e.post(ck, p, sharepageForm(e, nil), false), http.StatusNotFound, "POST "+p)
		}
	}
	if e.count(`SELECT count(*) FROM invites`) != before {
		t.Error("a refused request wrote an invite")
	}
	rec := e.a.do(http.MethodGet, "/share", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
		t.Errorf("visitor: %d %q", rec.Code, location(rec))
	}
	usersWant(t, e.get(e.owner, "/books/bk_nope/share"), http.StatusNotFound, "unknown book")
	usersWant(t, e.post(e.owner, "/books/bk_nope/share", sharepageForm(e, nil), false), http.StatusNotFound, "POST unknown book")
}

func TestSharePageCreateRoundTrip(t *testing.T) {
	e := inviteNewWeb(t)
	bid := e.books[0].ID
	rec := e.post(e.owner, "/books/"+bid+"/share", sharepageForm(e, url.Values{"scope": {"book"}, "note": {"Hi"}}), false)
	usersWant(t, rec, http.StatusOK, "create")
	body := rec.Body.String()
	tok := sharepageToken(t, body)
	if location(rec) != "" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers: loc=%q cc=%q rp=%q", location(rec), rec.Header().Get("Cache-Control"), rec.Header().Get("Referrer-Policy"))
	}
	if !strings.Contains(body, "Invite link for Rosa") || !strings.Contains(body, "It works once, for 7 days.") || !strings.Contains(body, "Invite someone else") {
		t.Error("link panel words")
	}
	var role, grants string
	var exp int64
	var hash []byte
	if err := e.a.st.Reader().QueryRow(`SELECT role, grants_json, expires_at, token_hash FROM invites WHERE name = 'Rosa'`).Scan(&role, &grants, &exp, &hash); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`[{"scope":"book","target":%q,"download":true,"read":true}]`, bid)
	if role != "guest" || grants != want {
		t.Errorf("role=%s grants=%s", role, grants)
	}
	if d := time.UnixMilli(exp).Sub(time.Now().Add(7 * 24 * time.Hour)); d < -time.Minute || d > time.Minute {
		t.Errorf("expires_at is %v off seven days", d)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	sum := sha256.Sum256(raw)
	if string(hash) != string(sum[:]) {
		t.Error("token_hash is not the SHA-256 of the link's token")
	}
	for _, q := range []string{`SELECT count(*) FROM invites WHERE instr(grants_json, ?1) OR instr(note, ?1) OR instr(name, ?1)`,
		`SELECT count(*) FROM audit_log WHERE instr(detail_json, ?1) OR instr(target, ?1)`} {
		if e.count(q, tok) != 0 {
			t.Errorf("the raw token is stored: %s", q)
		}
	}
	// the page is not the only place: a second look shows no link
	if again := e.get(e.owner, "/share").Body.String(); strings.Contains(again, tok) {
		t.Error("the link is shown a second time")
	}
	// the round trip: the link opens, accepting makes a listener with one book and downloads
	usersWant(t, e.anonGet("/invite/"+tok, nil), http.StatusOK, "invite page")
	usersWant(t, e.accept(tok), http.StatusSeeOther, "accept")
	if n := e.count(`SELECT count(*) FROM users u JOIN grants g ON g.user_id = u.id WHERE u.role = 'guest' AND g.scope = 'book' AND g.book_id = ? AND g.can_download = 1`, bid); n != 1 {
		t.Errorf("accepted guests with the book grant: %d", n)
	}
}

func TestSharePageExpiry(t *testing.T) {
	e := inviteNewWeb(t)
	for _, c := range []struct {
		val   string
		hours int64 // -1: never
	}{{"1d", 24}, {"30d", 720}, {"never", -1}} {
		rec := e.post(e.owner, "/share", sharepageForm(e, url.Values{"name": {"Pat " + c.val}, "expires": {c.val}}), false)
		usersWant(t, rec, http.StatusOK, c.val)
		var exp *int64
		if err := e.a.st.Reader().QueryRow(`SELECT expires_at FROM invites WHERE name = ?`, "Pat "+c.val).Scan(&exp); err != nil {
			t.Fatal(err)
		}
		if c.hours < 0 {
			if exp != nil {
				t.Errorf("never: expires_at %d", *exp)
			}
			if !strings.Contains(rec.Body.String(), "until you stop it") {
				t.Error("never: link note")
			}
			continue
		}
		if d := time.UnixMilli(*exp).Sub(time.Now().Add(time.Duration(c.hours) * time.Hour)); d < -time.Minute || d > time.Minute {
			t.Errorf("%s: %v off", c.val, d)
		}
	}
	rec := e.post(e.owner, "/share", sharepageForm(e, url.Values{"expires": {"2d"}}), false)
	usersWant(t, rec, http.StatusUnprocessableEntity, "2d")
	if !strings.Contains(rec.Body.String(), "Choose how long the link works.") {
		t.Error("expiry message")
	}
	f := sharepageForm(e, nil)
	f.Del("expires") // absent means seven days
	usersWant(t, e.post(e.owner, "/share", f, false), http.StatusOK, "no expiry field")
}

func TestSharePageRoles(t *testing.T) {
	e := inviteNewWeb(t)
	before := e.count(`SELECT count(*) FROM invites`)
	usersWant(t, e.post(e.admin, "/share", sharepageForm(e, url.Values{"role": {"admin"}}), false), http.StatusNotFound, "admin offers admin")
	if e.count(`SELECT count(*) FROM invites`) != before {
		t.Error("a forged admin invite was written")
	}
	usersWant(t, e.post(e.owner, "/share", sharepageForm(e, url.Values{"role": {"owner"}}), false), http.StatusUnprocessableEntity, "unknown role")
	rec := e.post(e.owner, "/share", sharepageForm(e, url.Values{"role": {"admin"}, "scope": {"book"}, "book": {e.books[0].ID}}), false)
	usersWant(t, rec, http.StatusOK, "owner offers admin")
	var grants string
	if err := e.a.st.Reader().QueryRow(`SELECT grants_json FROM invites WHERE role = 'admin'`).Scan(&grants); err != nil || grants != "[]" {
		t.Errorf("admin invite grants %q %v", grants, err)
	}
	usersWant(t, e.post(e.admin, "/share", sharepageForm(e, url.Values{"role": {"member"}, "name": {"Mia"}}), false), http.StatusOK, "admin offers member")
	if e.count(`SELECT count(*) FROM invites WHERE role = 'member' AND name = 'Mia'`) != 1 {
		t.Error("member invite missing")
	}
	// what the form offers follows the person
	if body := e.get(e.admin, "/share").Body.String(); strings.Contains(body, `value="admin"`) || !strings.Contains(body, `value="member"`) {
		t.Error("an admin is offered the admin role, or not the member role")
	}
	if body := e.get(e.owner, "/share").Body.String(); !strings.Contains(body, `value="admin"`) || !strings.Contains(body, "Can also make shelves.") {
		t.Error("the owner is not offered the admin role")
	}
}

func TestSharePageFormErrors(t *testing.T) {
	e := inviteNewWeb(t)
	_, memberCk := e.person(users.RoleMember)
	memberID := sweepUserID(t, e.a, memberCk)
	e.exec(`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_mem', ?, 'Mine', 1, 1)`, memberID)
	before := e.count(`SELECT count(*) FROM invites`)
	for _, c := range []struct {
		what  string
		extra url.Values
		drop  string
		want  string
	}{
		{"no name", url.Values{"name": {""}}, "", "Add a name so you know whose link it is."},
		{"long name", url.Values{"name": {strings.Repeat("é", 81)}}, "", "Keep the name under 80 characters."},
		{"long note", url.Values{"note": {strings.Repeat("n", 501)}}, "", "Keep the note under 500 characters."},
		{"no scope", nil, "scope", "Choose what they can listen to."},
		{"unknown library", url.Values{"library": {"lib_nope"}}, "", "Choose what they can listen to."},
		{"unknown book", url.Values{"scope": {"book"}, "book": {"bk_nope"}}, "", "Choose what they can listen to."},
		{"no book", url.Values{"scope": {"book"}}, "library", "Choose what they can listen to."},
		{"a member's shelf", url.Values{"scope": {"shelf"}, "shelf": {"sh_mem"}}, "", "Choose what they can listen to."},
	} {
		f := sharepageForm(e, c.extra)
		if c.drop != "" {
			f.Del(c.drop)
		}
		rec := e.post(e.owner, "/share", f, false)
		usersWant(t, rec, http.StatusUnprocessableEntity, c.what)
		body := rec.Body.String()
		if !strings.Contains(body, c.want) || !strings.Contains(body, `role="alert"`) {
			t.Errorf("%s: message %q missing", c.what, c.want)
		}
		if c.what == "long note" && !strings.Contains(body, `value="Rosa"`) {
			t.Error("the typed name was lost")
		}
		htmx := e.post(e.owner, "/share", f, true)
		if htmx.Code != http.StatusUnprocessableEntity || !strings.Contains(htmx.Body.String(), c.want) || strings.Contains(htmx.Body.String(), "<html") {
			t.Errorf("%s: htmx answer %d is not a toast", c.what, htmx.Code)
		}
	}
	if e.count(`SELECT count(*) FROM invites`) != before {
		t.Error("a refused form wrote an invite")
	}
	form := sharepageForm(e, nil)
	rec := e.a.do(http.MethodPost, "/share", form, nil, e.owner)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d", rec.Code)
	}
}

func TestSharePageLists(t *testing.T) {
	e := inviteNewWeb(t)
	a, _ := e.person(users.RoleMember)
	b, _ := e.person(users.RoleGuest)
	c, _ := e.person(users.RoleMember)
	d, _ := e.person(users.RoleGuest) // shares nothing
	e.sharepageGrant(a, "book", "book_id", e.books[0].ID)
	e.sharepageGrant(b, "book", "book_id", e.books[1].ID)
	e.sharepageGrant(c, "library", "library_id", e.lib.ID)
	e.create(e.ownerID, share.InviteSpec{Name: "Ana", Role: users.RoleAdmin})
	e.create(e.ownerID, share.InviteSpec{Name: "Ines", Role: users.RoleGuest, Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: e.books[1].ID}}})
	e.create(e.ownerID, share.InviteSpec{Name: "Lib", Role: users.RoleGuest, Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.lib.ID}}})

	owner := e.get(e.owner, "/share").Body.String()
	for _, id := range []string{a, b, c} {
		if !strings.Contains(owner, e.sharepageName(id)) {
			t.Errorf("%s is not listed", e.sharepageName(id))
		}
	}
	for _, id := range []string{d, e.ownerID, e.adminID} {
		if strings.Contains(owner, `aria-label="Stop sharing with `+e.sharepageName(id)+`"`) {
			t.Errorf("%s is listed", e.sharepageName(id))
		}
	}
	if !strings.Contains(owner, "3 people · 3 invites waiting") || !strings.Contains(owner, "Ana") || !strings.Contains(owner, "Admin link") {
		t.Errorf("owner subtitle or admin invite missing")
	}
	if !strings.Contains(owner, "Ines · hasn&#39;t opened it yet") || !strings.Contains(owner, `Link to &#34;`+e.books[1].Title+`&#34; · works until you stop it`) {
		t.Error("pending lines")
	}
	adm := e.get(e.admin, "/share").Body.String()
	if strings.Contains(adm, "Ana") || !strings.Contains(adm, "2 invites waiting") {
		t.Error("an admin sees the admin invite")
	}
	// a book's page lists the people and invites that open it, and has the book chosen
	page := e.get(e.owner, "/books/"+e.books[0].ID+"/share").Body.String()
	for _, id := range []string{a, c} {
		if !strings.Contains(page, e.sharepageName(id)) {
			t.Errorf("%s is missing from the book's list", e.sharepageName(id))
		}
	}
	if strings.Contains(page, `aria-label="Stop sharing with `+e.sharepageName(b)+`"`) || strings.Contains(page, "Ines") || strings.Contains(page, "Ana") || !strings.Contains(page, "Lib · hasn") {
		t.Error("the book's list is not narrowed to the book")
	}
	if !strings.Contains(page, `value="book" checked`) || !strings.Contains(page, `name="book" value="`+e.books[0].ID+`"`) || !strings.Contains(page, e.books[0].Title) {
		t.Error("the book is not chosen")
	}
	// the person with a library grant has the book through it: no stop button, a way to their page
	if !strings.Contains(page, "Has this through the library “Family library”") || strings.Contains(page, `aria-label="Stop sharing with `+e.sharepageName(c)+`"`) ||
		!strings.Contains(page, `aria-label="Manage `+e.sharepageName(c)+`"`) {
		t.Error("a wider share is not explained")
	}
	if !strings.Contains(page, `aria-label="Stop sharing with `+e.sharepageName(a)+`"`) {
		t.Error("a direct book share has no stop button")
	}
	// search on /share finds books and chooses "Just one book"
	found := e.get(e.owner, "/share?q=Usersword").Body.String()
	if !strings.Contains(found, `name="book" value="`+e.books[0].ID+`"`) || !strings.Contains(found, `value="book" checked`) {
		t.Error("book search results")
	}
}

func TestSharePageBackFlows(t *testing.T) {
	e := inviteNewWeb(t)
	inv, tok := e.memberInvite(e.ownerID)
	rec := e.post(e.admin, "/admin/invites/"+inv.ID+"/link", url.Values{"back": {"/share"}}, false)
	usersWant(t, rec, http.StatusOK, "copy link from the share page")
	body := rec.Body.String()
	newTok := sharepageToken(t, body)
	if !strings.Contains(body, "Invite someone") || !strings.Contains(body, "New link for Rosa") || rec.Header().Get("Cache-Control") != "no-store" || location(rec) != "" {
		t.Error("share page with the new link")
	}
	usersWant(t, e.anonGet("/invite/"+tok, nil), http.StatusNotFound, "old link")
	usersWant(t, e.anonGet("/invite/"+newTok, nil), http.StatusOK, "new link")
	// a back that is not one of the two share addresses falls back to the Users page
	for _, bad := range []string{"//evil.example", "https://evil.example/share", "/share?x=1", "/books//share", "/admin/users"} {
		rec := e.post(e.owner, "/admin/invites/"+inv.ID+"/link", url.Values{"back": {bad}}, false)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Users &amp; invites") {
			t.Errorf("back %q: %d", bad, rec.Code)
		}
		c, _ := e.create(e.ownerID, share.InviteSpec{Name: "X", Role: users.RoleAdmin})
		rec = e.post(e.owner, "/admin/invites/"+c.ID+"/cancel", url.Values{"back": {bad}}, false)
		if rec.Code != http.StatusSeeOther || location(rec) != "/admin/users" {
			t.Errorf("cancel back %q: %d %q", bad, rec.Code, location(rec))
		}
	}
	// cancel with a good back returns there
	again, _ := e.memberInvite(e.ownerID)
	rec = e.post(e.owner, "/admin/invites/"+again.ID+"/cancel", url.Values{"back": {"/share"}}, false)
	if rec.Code != http.StatusSeeOther || location(rec) != "/share" {
		t.Errorf("cancel back: %d %q", rec.Code, location(rec))
	}
	bookBack := "/books/" + e.books[0].ID + "/share"
	again, _ = e.memberInvite(e.ownerID)
	rec = e.post(e.owner, "/admin/invites/"+again.ID+"/cancel", url.Values{"back": {bookBack}}, false)
	if location(rec) != bookBack {
		t.Errorf("cancel back to a book's page: %q", location(rec))
	}

	// stop sharing: the confirmation keeps the way back, the action returns there
	id, _ := e.person(users.RoleMember)
	g := e.sharepageGrant(id, "library", "library_id", e.lib.ID)
	conf := e.get(e.owner, "/admin/users/"+id+"/stop?back="+url.QueryEscape("/share")).Body.String()
	if !strings.Contains(conf, `href="/share"`) || !strings.Contains(conf, `name="back" value="/share"`) || !strings.Contains(conf, `action="/admin/users/`+id+`/stop"`) {
		t.Error("the stop confirmation does not return to /share")
	}
	plain := e.get(e.owner, "/admin/users/"+id+"/stop?back="+url.QueryEscape("//evil.example")).Body.String()
	if strings.Contains(plain, "evil.example") || !strings.Contains(plain, `href="/admin/users/`+id+`"`) {
		t.Error("a bad back reached the confirmation")
	}
	rec = e.post(e.owner, "/admin/users/"+id+"/stop", url.Values{"back": {"/share"}}, false)
	if rec.Code != http.StatusSeeOther || location(rec) != "/share" || e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id) != 0 {
		t.Errorf("stop: %d %q", rec.Code, location(rec))
	}
	_ = g

	// from a book's page, stop removes only the grant that gives the book
	id2, _ := e.person(users.RoleGuest)
	bid := e.books[0].ID
	gb := e.sharepageGrant(id2, "book", "book_id", bid)
	e.sharepageGrant(id2, "book", "book_id", e.books[1].ID)
	conf = e.get(e.owner, "/admin/users/"+id2+"/stop?back="+url.QueryEscape(bookBack)).Body.String()
	if !strings.Contains(conf, "Stop sharing "+e.books[0].Title+" with "+e.sharepageName(id2)+"?") || !strings.Contains(conf, `action="/admin/users/`+id2+`/grants/`+gb+`/remove"`) {
		t.Errorf("per-book stop confirmation: %s", conf)
	}
	rec = e.post(e.owner, "/admin/users/"+id2+"/grants/"+gb+"/remove", url.Values{"back": {bookBack}}, false)
	if rec.Code != http.StatusSeeOther || location(rec) != bookBack || e.count(`SELECT count(*) FROM grants WHERE user_id = ?`, id2) != 1 {
		t.Errorf("per-book stop: %d %q", rec.Code, location(rec))
	}
}

func TestSharePageLinksOnBookAndUsers(t *testing.T) {
	e := inviteNewWeb(t)
	memberID, member := e.person(users.RoleMember)
	e.sharepageGrant(memberID, "library", "library_id", e.lib.ID)
	link := `href="/books/` + e.books[0].ID + `/share"`
	for _, path := range []string{"/books/" + e.books[0].ID, "/hx/books/" + e.books[0].ID + "/play"} {
		for name, ck := range map[string]*http.Cookie{"owner": e.owner, "admin": e.admin} {
			if !strings.Contains(e.get(ck, path).Body.String(), link) {
				t.Errorf("%s: no Share link on %s", name, path)
			}
		}
		if strings.Contains(e.get(member, path).Body.String(), link) {
			t.Errorf("a member sees the Share link on %s", path)
		}
	}
	if !strings.Contains(e.get(e.owner, "/admin/users").Body.String(), `href="/share"`) {
		t.Error("the Users page has no Invite someone link")
	}
}

func TestSharePureWords(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	names := map[string]string{"library:l1": "Kids", "book:b1": "Summer reads"}
	day := int64(24 * 3_600_000)
	for _, c := range []struct {
		inv  share.Invite
		want string
	}{
		{share.Invite{Role: users.RoleAdmin}, "Admin link · works until you stop it"},
		{share.Invite{Role: users.RoleGuest, Grants: []share.InviteGrant{{Scope: share.GrantAll}}, ExpiresAt: now.UnixMilli() + 5*day}, "Link to everything · works for 5 more days"},
		{share.Invite{Role: users.RoleGuest, Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: "b1"}}, ExpiresAt: now.UnixMilli() + day/2}, `Link to "Summer reads" · works for 1 more day`},
		{share.Invite{Role: users.RoleGuest, Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: "l1"}, {Scope: share.GrantBook, Target: "b1"}}}, `Link to "Kids" and 1 more · works until you stop it`},
	} {
		if got := sharePendingMeta(c.inv, names, now); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
	for _, c := range []struct{ in, want string }{
		{"/share", "/share"}, {"/books/abc_DEF-1/share", "/books/abc_DEF-1/share"},
		{"//evil", ""}, {"/share/", ""}, {"/books/a/b/share", ""}, {"/books//share", ""}, {"http://x/share", ""}, {"", ""},
	} {
		got, _, ok := validShareBack(c.in)
		if (c.want == "") == ok || got != c.want {
			t.Errorf("validShareBack(%q) = %q, %v", c.in, got, ok)
		}
	}
	for _, c := range []struct {
		in    string
		ok    bool
		never bool
	}{{"", true, false}, {"1d", true, false}, {"30d", true, false}, {"never", true, true}, {"2d", false, false}} {
		ms, _, ok := shareExpiry(c.in, now)
		if ok != c.ok || (c.ok && (ms == 0) != c.never) {
			t.Errorf("shareExpiry(%q) = %d, %v", c.in, ms, ok)
		}
	}
	if got := shareRoleChoices(users.User{Role: users.RoleAdmin}, ""); len(got) != 2 || got[0].Value != "guest" || !got[0].On {
		t.Errorf("admin roles %+v", got)
	}
}
