package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// inviteWeb is the Users environment plus helpers to make invites and send the anonymous requests
// a person with a link makes.
type inviteWeb struct {
	*usersEnv
}

func inviteNewWeb(t *testing.T) *inviteWeb { return &inviteWeb{usersNewEnv(t)} }

// coverBook gives a book a real cover file, so the invite cover route has bytes to send.
func (e *inviteWeb) coverBook(b library.Book) {
	e.t.Helper()
	sha := fmt.Sprintf("%064x", 0xabc123)
	rel := sweepWriteCover(e.t, e.a.dir, sha)
	cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha, Path: rel, Tint: "#1f4a52"}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, cover.ID)
	}); err != nil {
		e.t.Fatal(err)
	}
}

// create makes an invite as the person with this id and returns it with its token.
func (e *inviteWeb) create(by string, s share.InviteSpec) (share.Invite, string) {
	e.t.Helper()
	ctx := context.Background()
	u, err := e.a.srv.users.ByID(ctx, by)
	if err != nil {
		e.t.Fatal(err)
	}
	var inv share.Invite
	var tok string
	err = e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		inv, tok, err = e.a.srv.invites.Create(ctx, tx, u, s)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return inv, tok
}

// memberInvite is a member invite to the whole library of the environment.
func (e *inviteWeb) memberInvite(by string) (share.Invite, string) {
	return e.create(by, share.InviteSpec{
		Name: "Rosa", Email: "rosa@example.org", Note: "Hope you enjoy these.", Role: users.RoleMember,
		Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.lib.ID, Download: true}},
	})
}

func (e *inviteWeb) adminInvite() (share.Invite, string) {
	return e.create(e.ownerID, share.InviteSpec{Name: "Ana", Role: users.RoleAdmin})
}

// anonGet opens a link as a visitor holding the given csrf cookie (nil: a new visitor).
func (e *inviteWeb) anonGet(path string, c *http.Cookie) *httptest.ResponseRecorder {
	return e.a.do(http.MethodGet, path, nil, nil, c)
}

// accept posts the invite form as a visitor, with the CSRF token of the sign-in page.
func (e *inviteWeb) accept(tok string) *httptest.ResponseRecorder {
	e.t.Helper()
	c, token := e.a.anon(e.t, "/login")
	return e.a.do(http.MethodPost, "/invite/"+tok, url.Values{"_csrf": {token}}, nil, c)
}

var inviteCSRFField = regexp.MustCompile(`name="_csrf" value="[^"]*"`)

// inviteBody removes the one thing that differs between two visitors, their CSRF token.
func inviteBody(rec *httptest.ResponseRecorder) string {
	return inviteCSRFField.ReplaceAllString(rec.Body.String(), `name="_csrf" value=""`)
}

func TestInvitePage(t *testing.T) {
	e := inviteNewWeb(t)
	inv, tok := e.memberInvite(e.ownerID)
	rec := e.anonGet("/invite/"+tok, nil)
	usersWant(t, rec, http.StatusOK, "valid invite")
	body := rec.Body.String()
	for _, want := range []string{"From Ana", "Ana invited you to listen to", "<em>Usersword One</em>", "and 2 other books", "“Hope you enjoy these.”", "Start listening",
		"No sign-up needed. Free, and private to your family."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	h := rec.Header()
	if h.Get("Referrer-Policy") != "same-origin" || h.Get("X-Robots-Tag") != "noindex" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %v", h)
	}
	if strings.Contains(body, "Sign out and use this invite") {
		t.Error("a visitor sees the signed-in notice")
	}
	_ = inv

	// an admin invite names the server and shows no book
	_, atok := e.adminInvite()
	body = e.anonGet("/invite/"+atok, nil).Body.String()
	if !strings.Contains(body, "Ana invited you to Tendshelf") || strings.Contains(body, "Usersword") {
		t.Errorf("admin invite page: %s", body)
	}
}

func TestInviteOneAnswerForEveryBrokenLink(t *testing.T) {
	e := inviteNewWeb(t)
	ctx := context.Background()
	ck, csrf := e.a.anon(t, "/login")
	_, revoked := e.memberInvite(e.ownerID)
	_, rotated := e.memberInvite(e.ownerID)
	_, refused := e.create(e.adminID, share.InviteSpec{Name: "Pia", Role: users.RoleGuest, Grants: []share.InviteGrant{{Scope: share.GrantAll}}})
	inv, _ := e.memberInvite(e.ownerID)
	var rotatedAway string
	_ = e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		u, _ := e.a.srv.users.ByID(ctx, e.ownerID)
		var err error
		rotatedAway, err = e.a.srv.invites.Rotate(ctx, tx, u, inv.ID)
		return err
	})
	_ = rotatedAway
	e.exec(`UPDATE invites SET revoked_at = 5 WHERE token_hash = ?`, mustHash(t, revoked))
	// rotate the "rotated" invite and keep only the old token
	var id string
	_ = e.a.st.Reader().QueryRow(`SELECT id FROM invites WHERE token_hash = ?`, mustHash(t, rotated)).Scan(&id)
	_ = e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		u, _ := e.a.srv.users.ByID(ctx, e.ownerID)
		_, err := e.a.srv.invites.Rotate(ctx, tx, u, id)
		return err
	})
	e.exec(`UPDATE users SET role = 'member' WHERE id = ?`, e.adminID) // the inviter of "refused" loses the right
	unknown, _ := auth.NewToken()
	var first string
	for name, tok := range map[string]string{"unknown": unknown, "revoked": revoked, "rotated away": rotated, "refused": refused, "malformed": "short"} {
		rec := e.anonGet("/invite/"+tok, ck)
		usersWant(t, rec, http.StatusNotFound, name)
		if !strings.Contains(rec.Body.String(), "This invite link doesn&#39;t work") {
			t.Errorf("%s: wrong page", name)
		}
		if first == "" {
			first = inviteBody(rec)
		} else if inviteBody(rec) != first {
			t.Errorf("%s: the answer differs from the others", name)
		}
		post := e.a.do(http.MethodPost, "/invite/"+tok, url.Values{"_csrf": {csrf}}, nil, ck)
		usersWant(t, post, http.StatusNotFound, name+" POST")
		if inviteBody(post) != first {
			t.Errorf("%s POST: the answer differs", name)
		}
	}
}

func mustHash(t *testing.T, tok string) []byte {
	t.Helper()
	h, ok := auth.HashToken(tok)
	if !ok {
		t.Fatal("bad token")
	}
	return h[:]
}

func TestInviteEndedAndUsed(t *testing.T) {
	e := inviteNewWeb(t)
	_, expired := e.memberInvite(e.ownerID)
	e.exec(`UPDATE invites SET created_at = 1, expires_at = 2 WHERE token_hash = ?`, mustHash(t, expired))
	rec := e.anonGet("/invite/"+expired, nil)
	usersWant(t, rec, http.StatusGone, "expired")
	if b := rec.Body.String(); !strings.Contains(b, "This invite has ended") || !strings.Contains(b, "Ask Ana Reyes for a new link.") {
		t.Errorf("expired page: %s", b)
	}
	_, used := e.memberInvite(e.ownerID)
	usersWant(t, e.accept(used), http.StatusSeeOther, "first use")
	rec = e.anonGet("/invite/"+used, nil)
	usersWant(t, rec, http.StatusGone, "used")
	if b := rec.Body.String(); !strings.Contains(b, "This invite was already used") || !strings.Contains(b, `href="/login"`) {
		t.Errorf("used page: %s", b)
	}
	usersWant(t, e.accept(used), http.StatusGone, "second use")
	if e.count(`SELECT count(*) FROM users WHERE display_name = 'Rosa'`) != 1 {
		t.Error("the second use made a second account")
	}
}

func TestInviteLimiter(t *testing.T) {
	e := inviteNewWeb(t)
	_, good := e.memberInvite(e.ownerID)
	unknown, _ := auth.NewToken()
	for i := 0; i < 20; i++ {
		usersWant(t, e.anonGet("/invite/"+unknown, nil), http.StatusNotFound, fmt.Sprint("bad ", i))
	}
	rec := e.anonGet("/invite/"+good, nil)
	usersWant(t, rec, http.StatusTooManyRequests, "21st request")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	if e.count(`SELECT use_count FROM invites`) != 0 {
		t.Error("a limited request used the invite")
	}
}

func TestInviteAcceptFlow(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)

	// no token, nothing happens
	c, _ := e.a.anon(t, "/invite/"+tok)
	usersWant(t, e.a.do(http.MethodPost, "/invite/"+tok, url.Values{}, nil, c), http.StatusForbidden, "no CSRF token")
	if e.count(`SELECT use_count FROM invites`) != 0 {
		t.Fatal("a refused post used the invite")
	}

	rec := e.accept(tok)
	usersWant(t, rec, http.StatusSeeOther, "accept")
	loc := location(rec)
	if !strings.HasPrefix(loc, "/welcome?book=") {
		t.Fatalf("Location %q", loc)
	}
	sess := cookieNamed(rec, auth.CookieName)
	if sess == nil {
		t.Fatal("no session cookie")
	}
	var id, role string
	if err := e.a.st.Reader().QueryRow(`SELECT id, role FROM users WHERE display_name = 'Rosa'`).Scan(&id, &role); err != nil || role != "member" {
		t.Fatalf("new user: %v role %q", err, role)
	}
	gs, _ := e.a.srv.grants.ForUser(context.Background(), id)
	if len(gs) != 1 || gs[0].Scope != share.GrantLibrary || !gs[0].CanDownload || gs[0].CreatedBy != e.ownerID {
		t.Fatalf("grants = %+v", gs)
	}
	// the new person is signed in and can see the library, not the Users area
	usersWant(t, e.get(sess, "/"), http.StatusOK, "home")
	usersWant(t, e.get(sess, "/admin/users"), http.StatusNotFound, "users area")
	if body := e.get(sess, loc).Body.String(); !strings.Contains(body, "Keep your place, Rosa?") {
		t.Errorf("welcome page: %s", body)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'invite.accept'`) != 1 {
		t.Error("no accept audit row")
	}
}

func TestInviteSignedInVisitor(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)
	rec := e.get(e.admin, "/invite/"+tok)
	usersWant(t, rec, http.StatusOK, "signed-in GET")
	body := rec.Body.String()
	if !strings.Contains(body, "You're signed in as") || !strings.Contains(body, "This invite is for someone new.") ||
		!strings.Contains(body, "Sign out and use this invite") || !strings.Contains(body, "Back to your books") {
		t.Errorf("signed-in page: %s", body)
	}
	if e.count(`SELECT use_count FROM invites`) != 0 {
		t.Fatal("a GET used the invite")
	}
	post := e.a.do(http.MethodPost, "/invite/"+tok, url.Values{"_csrf": {pageToken(t, body)}}, nil, e.admin)
	usersWant(t, post, http.StatusSeeOther, "signed-in POST")
	fresh := cookieNamed(post, auth.CookieName)
	if fresh == nil || fresh.Value == e.admin.Value {
		t.Fatal("no new session")
	}
	// the old cookie is no longer a session
	old := e.get(e.admin, "/")
	if old.Code != http.StatusSeeOther || !strings.HasPrefix(location(old), "/login") {
		t.Errorf("old session: %d %s", old.Code, location(old))
	}
	usersWant(t, e.get(fresh, "/"), http.StatusOK, "new session")
}

func TestInviteCover(t *testing.T) {
	e := inviteNewWeb(t)
	e.coverBook(e.books[0])
	_, tok := e.create(e.ownerID, share.InviteSpec{Name: "Rosa", Role: users.RoleMember,
		Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: e.books[0].ID}}})
	page := e.anonGet("/invite/"+tok, nil).Body.String()
	if !strings.Contains(page, "/invite/"+tok+"/cover?w=640") {
		t.Fatalf("page lacks the cover address: %s", page)
	}
	rec := e.anonGet("/invite/"+tok+"/cover?w=320", nil)
	usersWant(t, rec, http.StatusOK, "cover")
	if rec.Header().Get("Cache-Control") != "private, no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/") || rec.Body.Len() == 0 {
		t.Errorf("cover headers %v, %d bytes", rec.Header(), rec.Body.Len())
	}
	unknown, _ := auth.NewToken()
	usersWant(t, e.anonGet("/invite/"+unknown+"/cover", nil), http.StatusNotFound, "unknown cover")
	// once used, the cover is gone
	usersWant(t, e.accept(tok), http.StatusSeeOther, "accept")
	usersWant(t, e.anonGet("/invite/"+tok+"/cover", nil), http.StatusNotFound, "used cover")
}

func TestInviteWelcome(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)
	rec := e.accept(tok)
	sess := cookieNamed(rec, auth.CookieName)
	loc := location(rec)
	book := strings.TrimPrefix(loc, "/welcome?book=")

	// a password that is too short, then a good one
	token := pageToken(t, e.get(sess, loc).Body.String())
	bad := e.a.do(http.MethodPost, "/welcome", url.Values{"_csrf": {token}, "keep": {"password"}, "username": {"rosa-home"}, "password": {"short"}, "book": {book}}, nil, sess)
	usersWant(t, bad, http.StatusUnprocessableEntity, "short password")
	if !strings.Contains(bad.Body.String(), "Use at least 10 characters.") {
		t.Error("no message for the short password")
	}
	// a username that is taken
	taken := e.a.do(http.MethodPost, "/welcome", url.Values{"_csrf": {token}, "keep": {"password"}, "username": {"ANA"}, "password": {"a long enough password"}}, nil, sess)
	if taken.Code != http.StatusUnprocessableEntity {
		// "ana" is not a user here (setup made the owner "ana"): setup form uses username ana
		t.Fatalf("taken username: %d", taken.Code)
	}
	if !strings.Contains(taken.Body.String(), "That username is taken.") {
		t.Error("no message for the taken username")
	}
	ok := e.a.do(http.MethodPost, "/welcome", url.Values{"_csrf": {token}, "keep": {"password"}, "username": {"Rosa.Home"}, "password": {"a long enough password"}, "book": {book}}, nil, sess)
	usersWant(t, ok, http.StatusSeeOther, "credentials")
	if location(ok) != "/books/"+book {
		t.Errorf("Location %q", location(ok))
	}
	// now it has a password: GET goes on, POST is refused, sign in works
	again := e.get(sess, "/welcome")
	usersWant(t, again, http.StatusSeeOther, "welcome again")
	usersWant(t, e.a.do(http.MethodPost, "/welcome", url.Values{"_csrf": {token}, "keep": {"password"}, "username": {"other"}, "password": {"another long password"}}, nil, sess),
		http.StatusNotFound, "second set")
	c, ctoken := e.a.anon(t, "/login")
	login := e.a.do(http.MethodPost, "/login", url.Values{"_csrf": {ctoken}, "username": {"rosa.home"}, "password": {"a long enough password"}}, nil, c)
	usersWant(t, login, http.StatusSeeOther, "sign in with the new credentials")
}

func TestInviteWelcomePhoneAndBookParam(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.create(e.ownerID, share.InviteSpec{Name: "Gus", Role: users.RoleGuest,
		Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: e.books[0].ID}}})
	sess := cookieNamed(e.accept(tok), auth.CookieName)
	token := e.a.tokenFor(t, sess)
	phone := e.a.do(http.MethodPost, "/welcome", url.Values{"_csrf": {token}, "keep": {"phone"}, "book": {e.books[0].ID}}, nil, sess)
	usersWant(t, phone, http.StatusSeeOther, "phone")
	if location(phone) != "/books/"+e.books[0].ID {
		t.Errorf("Location %q", location(phone))
	}
	// a book the guest cannot see is not a way to learn it exists: back to Home
	other := e.a.do(http.MethodPost, "/welcome", url.Values{"_csrf": {token}, "keep": {"phone"}, "book": {e.books[1].ID}}, nil, sess)
	if location(other) != "/" {
		t.Errorf("unviewable book: Location %q", location(other))
	}
	if get := e.get(sess, "/welcome?book="+e.books[1].ID); strings.Contains(get.Body.String(), e.books[1].ID) {
		t.Error("the welcome page repeats a book the person cannot view")
	}
	// the account is still without a password
	if e.count(`SELECT count(*) FROM users WHERE display_name = 'Gus' AND password_hash IS NULL`) != 1 {
		t.Error("choosing the phone changed credentials")
	}
}

func TestInviteProfileRow(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)
	sess := cookieNamed(e.accept(tok), auth.CookieName)
	me := e.get(sess, "/me").Body.String()
	if !strings.Contains(me, "Add a password so you can sign in elsewhere") || !strings.Contains(me, `href="/welcome"`) || strings.Contains(me, "Change password") {
		t.Errorf("password-less profile: %s", me)
	}
	if me := e.get(e.owner, "/me").Body.String(); strings.Contains(me, "Add a password so you can sign in elsewhere") || !strings.Contains(me, "Change password") {
		t.Error("an owner with a password sees the wrong row")
	}
}

func TestInviteUsersPageRotateAndCancel(t *testing.T) {
	e := inviteNewWeb(t)
	member, mtok := e.memberInvite(e.ownerID)
	admin, atok := e.adminInvite()

	owner := e.get(e.owner, "/admin/users").Body.String()
	if !strings.Contains(owner, "Invites · 2") || strings.Count(owner, "hasn&#39;t opened the invite yet") != 2 || !strings.Contains(owner, "Copy link again") || !strings.Contains(owner, "Cancel invite") {
		t.Errorf("owner page: invites missing")
	}
	if !strings.Contains(owner, "Member · No end date") || !strings.Contains(owner, "Admin · No end date") {
		t.Error("meta lines missing")
	}
	adm := e.get(e.admin, "/admin/users").Body.String()
	if !strings.Contains(adm, "Invites · 1") || !strings.Contains(adm, "/admin/invites/"+member.ID+"/link") || strings.Contains(adm, admin.ID) {
		t.Error("an admin sees an admin invite, or misses the member one")
	}
	if strings.Contains(adm, atok) || strings.Contains(owner, mtok) {
		t.Error("a token is on the Users page")
	}
	if body := e.get(e.owner, "/").Body.String(); !strings.Contains(body, "2 waiting") {
		t.Error("sidebar count for the owner")
	}
	if body := e.get(e.admin, "/").Body.String(); !strings.Contains(body, "1 waiting") {
		t.Error("sidebar count for the admin")
	}

	// the other admin invite cannot be rotated or cancelled by an admin; members are refused everywhere
	_, member2 := e.person(users.RoleMember)
	for _, path := range []string{"/admin/invites/" + admin.ID + "/link", "/admin/invites/" + admin.ID + "/cancel"} {
		usersWant(t, e.post(e.admin, path, nil, false), http.StatusNotFound, "admin on admin invite "+path)
	}
	for _, path := range []string{"/admin/invites/" + member.ID + "/link", "/admin/invites/" + member.ID + "/cancel"} {
		usersWant(t, e.post(member2, path, nil, false), http.StatusNotFound, "member "+path)
	}
	usersWant(t, e.post(e.owner, "/admin/invites/inv_nope/link", nil, false), http.StatusNotFound, "unknown invite")

	// rotate: 200 with the new address once, the old link stops working
	rec := e.post(e.admin, "/admin/invites/"+member.ID+"/link", nil, false)
	usersWant(t, rec, http.StatusOK, "rotate")
	body := rec.Body.String()
	m := regexp.MustCompile(`value="(http://example\.com/invite/[A-Za-z0-9_-]{43})"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no new address in the page: %s", body)
	}
	if !strings.Contains(body, "New link for Rosa") || !strings.Contains(body, "The old link no longer works.") || location(rec) != "" {
		t.Error("new-link panel wrong")
	}
	newTok := strings.TrimPrefix(m[1], "http://example.com/invite/")
	usersWant(t, e.anonGet("/invite/"+mtok, nil), http.StatusNotFound, "old link")
	usersWant(t, e.anonGet("/invite/"+newTok, nil), http.StatusOK, "new link")
	if again := e.get(e.owner, "/admin/users").Body.String(); strings.Contains(again, newTok) {
		t.Error("the address is shown a second time")
	}

	// cancel: 303 back, the link is gone, the count drops; cancelling again still answers 303
	cancel := e.post(e.admin, "/admin/invites/"+member.ID+"/cancel", nil, false)
	usersWant(t, cancel, http.StatusSeeOther, "cancel")
	if location(cancel) != "/admin/users" {
		t.Errorf("Location %q", location(cancel))
	}
	usersWant(t, e.post(e.admin, "/admin/invites/"+member.ID+"/cancel", nil, false), http.StatusSeeOther, "cancel again")
	usersWant(t, e.anonGet("/invite/"+newTok, nil), http.StatusNotFound, "cancelled link")
	if body := e.get(e.admin, "/").Body.String(); strings.Contains(body, "waiting") {
		t.Error("the admin's count did not drop")
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'invite.revoke'`) != 1 {
		t.Error("revoke audit rows")
	}
	// the owner still has the admin invite
	if !strings.Contains(e.get(e.owner, "/admin/users").Body.String(), "Invites · 1") {
		t.Error("owner's tab count")
	}
}

// TestInviteParallelAcceptsMakeOneAccount posts one link from eight browsers at once: one gets in,
// the rest hear it was used, and exactly one account exists.
func TestInviteParallelAcceptsMakeOneAccount(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)
	codes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		c, token := e.a.anon(t, "/login")
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- e.a.do(http.MethodPost, "/invite/"+tok, url.Values{"_csrf": {token}}, nil, c).Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[http.StatusSeeOther] != 1 || got[http.StatusGone] != 7 {
		t.Fatalf("answers = %v, want one 303 and seven 410", got)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE display_name = 'Rosa'`); n != 1 {
		t.Fatalf("%d accounts", n)
	}
}

func TestInviteRechecksTheInviterAtAcceptance(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.create(e.adminID, share.InviteSpec{Name: "Rosa", Role: users.RoleMember,
		Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: e.books[0].ID}}})
	usersWant(t, e.anonGet("/invite/"+tok, nil), http.StatusOK, "before")
	e.exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.adminID)
	usersWant(t, e.accept(tok), http.StatusNotFound, "after the inviter was turned off")
	if e.count(`SELECT count(*) FROM users WHERE display_name = 'Rosa'`) != 0 {
		t.Error("an account was made from a refused invite")
	}
}

func TestInvitePathsAndLogRedaction(t *testing.T) {
	for p, want := range map[string]bool{
		"/invite/abc": true, "/invite/abc/cover": true, "/invite/": false, "/invite": false, "/invite//cover": false,
		"/invite/abc/other": false, "/invite/abc/cover/": false, "/invites/abc": false, "/invite/a/b/c": false, "/admin/invites/x/link": false,
	} {
		if got := isInvitePath(p); got != want {
			t.Errorf("isInvitePath(%q) = %v, want %v", p, got, want)
		}
	}
	for p, want := range map[string]string{
		"/invite/SECRETTOKEN":       "/invite/…",
		"/invite/SECRETTOKEN/cover": "/invite/…/cover",
		"/books/abc":                "/books/abc",
		"/":                         "/",
	} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		if got := logPath(r); got != want {
			t.Errorf("logPath(%q) = %q, want %q", p, got, want)
		}
	}
	// only those two shapes are public; everything else under /invite/ goes to the login page
	a := newTestApp(t)
	a.owner(t)
	for _, p := range []string{"/invite/", "/invite/abc/other", "/invite/abc/cover/"} {
		rec := a.do(http.MethodGet, p, nil, nil)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
			t.Errorf("anonymous %s: %d %s", p, rec.Code, location(rec))
		}
	}
}
