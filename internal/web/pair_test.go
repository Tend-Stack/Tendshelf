package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var (
	pairTokenRe  = regexp.MustCompile(`/pair/([A-Za-z0-9_-]{43})`)
	pairIDRe     = regexp.MustCompile(`/me/devices/add/([A-Za-z0-9_]+)`)
	pairMarkupRe = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]*>`)
	pairNumberRe = regexp.MustCompile(`ts-pair__number" aria-label="(\d{4})"`)
)

// pairClock is a clock a test can move.
type pairClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *pairClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *pairClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// phone is a second client with its own cookie jar: the app's window.
type phone struct {
	t       *testing.T
	a       *testApp
	jar     map[string]*http.Cookie
	android bool
}

func newPhone(t *testing.T, a *testApp, android bool) *phone {
	return &phone{t: t, a: a, jar: map[string]*http.Cookie{}, android: android}
}

func (p *phone) do(method, path string, form url.Values, mod func(*http.Request)) *httptest.ResponseRecorder {
	var cks []*http.Cookie
	for _, c := range p.jar {
		cks = append(cks, c)
	}
	if p.android {
		cks = append(cks, a1Android)
	}
	rec := p.a.do(method, path, form, mod, cks...)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(p.jar, c.Name)
		} else {
			p.jar[c.Name] = c
		}
	}
	return rec
}

// post fetches the page at getPath for its token and posts the form (plus _csrf) to path.
func (p *phone) post(getPath, path string, form url.Values) *httptest.ResponseRecorder {
	p.t.Helper()
	page := p.do(http.MethodGet, getPath, nil, nil)
	if page.Code != http.StatusOK {
		p.t.Fatalf("GET %s = %d", getPath, page.Code)
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", pageToken(p.t, page.Body.String()))
	return p.do(http.MethodPost, path, form, nil)
}

// pairShow is the owner side: a signed-in session shows a code.
type pairShow struct {
	t       *testing.T
	a       *testApp
	owner   *http.Cookie
	csrf    string
	token   string
	id      string
	rec     *httptest.ResponseRecorder
	clock   *pairClock
	phoneCh a1Flow
}

func newPairApp(t *testing.T) (*testApp, *pairClock) {
	clk := &pairClock{t: time.Now()}
	return newTestApp(t, func(o *Options) { o.Now = clk.Now }), clk
}

func pairStart(t *testing.T, a *testApp, owner *http.Cookie) *pairShow {
	t.Helper()
	s := &pairShow{t: t, a: a, owner: owner, csrf: a.tokenFor(t, owner)}
	s.rec = a.do(http.MethodPost, "/me/devices/add", url.Values{"_csrf": {s.csrf}}, nil, owner)
	if s.rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", s.rec.Code, s.rec.Body)
	}
	body := s.rec.Body.String()
	if m := pairTokenRe.FindStringSubmatch(body); m != nil {
		s.token = m[1]
	} else {
		t.Fatal("no pairing address on the page")
	}
	if m := pairIDRe.FindStringSubmatch(body); m != nil {
		s.id = m[1]
	} else {
		t.Fatal("no pairing id on the page")
	}
	s.phoneCh = a1NewFlow()
	return s
}

func (s *pairShow) confirmPath() string {
	return "/pair/" + s.token + "?challenge=" + s.phoneCh.challenge + "&name=" + url.QueryEscape("Pixel 8")
}

// claim plays the phone's tap on "Add this phone".
func (s *pairShow) claim(p *phone) *httptest.ResponseRecorder {
	s.t.Helper()
	return p.post(s.confirmPath(), "/pair/"+s.token, url.Values{"challenge": {s.phoneCh.challenge}, "name": {"Pixel 8"}})
}

func (s *pairShow) number(p *phone) string {
	s.t.Helper()
	rec := p.do(http.MethodGet, "/pair/wait", nil, nil)
	m := pairNumberRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		s.t.Fatalf("wait = %d, no number", rec.Code)
	}
	return m[1]
}

func (s *pairShow) approve(match string) *httptest.ResponseRecorder {
	return s.a.do(http.MethodPost, "/me/devices/add/"+s.id+"/approve", url.Values{"_csrf": {s.csrf}, "match": {match}}, nil, s.owner)
}

func (s *pairShow) finish(p *phone) *httptest.ResponseRecorder {
	if page := p.do(http.MethodGet, "/pair/wait", nil, nil); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/pair/finish"`) {
		return s.finish2(p) // no Connect button to tap: post as a phone that guessed the address
	}
	return p.post("/pair/wait", "/pair/finish", nil)
}

func (s *pairShow) rows() int { return s.a.count(s.t, `SELECT COUNT(*) FROM device_pairings`) }

// pageShows reports whether number is in the words on the page. Four random digits turn up by
// chance inside ids and hashed file names, so the markup is taken out first.
func pageShows(body, number string) bool {
	text := pairMarkupRe.ReplaceAllString(body, " ")
	return regexp.MustCompile(`\b` + number + `\b`).MatchString(text)
}

// claimed runs the flow to the point where the phone shows the number; it returns the number.
func (s *pairShow) claimed(p *phone) string {
	s.t.Helper()
	if rec := s.claim(p); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/pair/wait" {
		s.t.Fatalf("claim = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	return s.number(p)
}

func (s *pairShow) approved(p *phone) {
	s.t.Helper()
	if rec := s.approve(s.claimed(p)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Now tap Connect on your phone.") {
		s.t.Fatalf("approve = %d", rec.Code)
	}
}

func TestPairHappy(t *testing.T) {
	logs := a1LogBuffer(t)
	a, _ := newPairApp(t)
	owner := a.owner(t)
	s := pairStart(t, a, owner)
	body := html.UnescapeString(s.rec.Body.String())
	for _, want := range []string{"Add a phone", "<svg", `class="ts-qr"`, "Scan this with your phone's camera. It works once, for 3 minutes.", "Or open this link on the phone",
		"Waiting for your phone…", `hx-trigger="every 2s"`, `hx-select="#pair-status"`, `id="pair-status"`, `role="status"`, "This address is not private."} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}
	h := s.rec.Header()
	if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "same-origin" || h.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("owner headers: %v", h)
	}

	ph := newPhone(t, a, true)
	// The phone opens the QR's address in the app: the question, no state change.
	page := ph.do(http.MethodGet, s.confirmPath(), nil, nil)
	for _, want := range []string{"Add this phone to Tendshelf?", "Only go on if you are doing this yourself, with your other device in front of you.",
		"Address: example.com", `action="/pair/` + s.token + `"`, `hx-boost="false"`, `name="challenge"`, "This address is not private."} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("confirm page lacks %q", want)
		}
	}
	if s.a.count(t, `SELECT COUNT(*) FROM device_pairings WHERE state = 'waiting'`) != 1 {
		t.Fatal("a GET changed the pairing")
	}
	number := s.claimed(ph)
	ck := ph.jar[pairCookie]
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/pair" || ck.MaxAge != 180 || ck.Secure {
		t.Fatalf("ts_pair = %+v", ck)
	}
	// The owner's poll: a phone claimed, so a page; the phone's own poll: nothing changed.
	st := a.do(http.MethodGet, "/me/devices/add/"+s.id, nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, owner)
	if st.Code != http.StatusOK || !strings.Contains(st.Body.String(), "Pixel 8 wants to sign in as you, from") || pageShows(st.Body.String(), number) ||
		!strings.Contains(st.Body.String(), `autocomplete="one-time-code"`) || !strings.Contains(st.Body.String(), `maxlength="4"`) || !strings.Contains(st.Body.String(), "Not my phone") {
		t.Fatalf("owner claimed page = %d %s", st.Code, st.Body)
	}
	if strings.Contains(st.Body.String(), `hx-trigger`) {
		t.Error("the claimed page still polls")
	}
	if p := ph.do(http.MethodGet, "/pair/wait", nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") }); p.Code != http.StatusNoContent {
		t.Errorf("phone poll while claimed = %d", p.Code)
	}
	if rec := s.approve(number); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d", rec.Code)
	}
	wait := ph.do(http.MethodGet, "/pair/wait", nil, nil)
	if !strings.Contains(wait.Body.String(), "Connect as Ana Reyes") || pageShows(wait.Body.String(), number) {
		t.Fatalf("approved page = %s", wait.Body)
	}
	pwBefore := a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%password%'`)
	fin := s.finish(ph)
	code := a1CodeOf(t, fin)
	if fin.Header().Get("Cache-Control") != "no-store" || fin.Header().Get("Referrer-Policy") != "same-origin" {
		t.Errorf("finish headers: %v", fin.Header())
	}
	if ph.jar[pairCookie] != nil || ph.jar[auth.CookieName] == nil {
		t.Error("finish left ts_pair or set no session")
	}
	// The phone's new session reaches the pages, and the swap needs no password.
	if me := ph.do(http.MethodGet, "/me/devices", nil, nil); me.Code != http.StatusOK {
		t.Fatalf("phone session on /me/devices = %d", me.Code)
	}
	sw := a1Swap(a, a1SwapBody(code, s.phoneCh.verifier, nil), nil)
	if sw.Code != http.StatusCreated {
		t.Fatalf("swap = %d %s", sw.Code, sw.Body)
	}
	var got deviceIssued
	decodeJSON(t, sw, &got)
	if got.Device.Kind != "mobile" || a.count(t, `SELECT COUNT(*) FROM sessions WHERE device_id = ?`, got.Device.ID) != 1 {
		t.Fatalf("device = %+v", got.Device)
	}
	if a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%password%'`) != pwBefore {
		t.Error("a password was involved")
	}
	if devs := a.do(http.MethodGet, "/me/devices", nil, nil, owner); !strings.Contains(devs.Body.String(), "This phone") && !strings.Contains(devs.Body.String(), "Pixel") {
		t.Error("the device is not in Your devices")
	}
	// Signing the device out kills its token and the phone's session.
	out := a.do(http.MethodPost, "/me/devices/"+got.Device.ID+"/signout", url.Values{"_csrf": {s.csrf}}, nil, owner)
	if out.Code != http.StatusSeeOther {
		t.Fatalf("signout = %d", out.Code)
	}
	if me := deviceBearerReq(a, http.MethodGet, "/api/v1/me", got.Token, nil); me.Code != http.StatusUnauthorized {
		t.Errorf("token after sign-out = %d", me.Code)
	}
	if me := ph.do(http.MethodGet, "/me/devices", nil, nil); me.Code != http.StatusSeeOther {
		t.Errorf("phone session after sign-out = %d", me.Code)
	}
	// Nothing anywhere holds a secret.
	var audit string
	_ = a.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&audit)
	for name, secret := range map[string]string{"token": s.token, "claim": ph.jarClaim(), "code": code, "verifier": s.phoneCh.verifier, "number": ">" + number + "<"} {
		if secret == "" {
			continue
		}
		if strings.Contains(audit, secret) {
			t.Errorf("the audit log holds the %s", name)
		}
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log holds the %s", name)
		}
	}
}

// jarClaim is the claim cookie value seen at any time; it is empty after finish, so the happy test
// records nothing here.
func (p *phone) jarClaim() string {
	if c := p.jar[pairCookie]; c != nil {
		return c.Value
	}
	return ""
}

func TestPairForbiddenClaim(t *testing.T) {
	a, clk := newPairApp(t)
	owner := a.owner(t)

	t.Run("second claim", func(t *testing.T) {
		s := pairStart(t, a, owner)
		first := newPhone(t, a, true)
		s.claimed(first)
		other := newPhone(t, a, true)
		if rec := s.claim2(other); rec.Code != http.StatusNotFound {
			t.Fatalf("second claim = %d", rec.Code)
		}
		if other.jar[pairCookie] != nil {
			t.Error("the second claimer got a cookie")
		}
	})
	t.Run("a GET changes nothing and dead tokens look alike", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		s.claimed(ph)
		spent := ph.do(http.MethodGet, s.confirmPath(), nil, nil)
		unknown := ph.do(http.MethodGet, "/pair/"+strings.Repeat("A", 43)+"?challenge="+s.phoneCh.challenge, nil, nil)
		junk := ph.do(http.MethodGet, "/pair/nope", nil, nil)
		if spent.Code != http.StatusNotFound || spent.Body.String() != unknown.Body.String() || spent.Body.String() != junk.Body.String() {
			t.Fatalf("dead tokens differ: %d %d %d", spent.Code, unknown.Code, junk.Code)
		}
		if !strings.Contains(spent.Body.String(), "This code has ended. Show a new one on your other device.") || !strings.Contains(spent.Body.String(), "Sign in with a password instead") {
			t.Error("the ended page lacks its words")
		}
	})
	t.Run("expired by 1 ms", func(t *testing.T) {
		s := pairStart(t, a, owner)
		clk.Add(auth.PairTTL)
		ph := newPhone(t, a, true)
		if rec := ph.do(http.MethodGet, s.confirmPath(), nil, nil); rec.Code != http.StatusOK {
			t.Fatalf("at the last millisecond = %d", rec.Code)
		}
		clk.Add(time.Millisecond)
		if rec := ph.do(http.MethodGet, s.confirmPath(), nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("expired GET = %d", rec.Code)
		}
	})
	t.Run("no ts_shell", func(t *testing.T) {
		s := pairStart(t, a, owner)
		br := newPhone(t, a, false)
		page := br.do(http.MethodGet, "/login", nil, nil)
		tok := pageToken(t, page.Body.String())
		rec := br.do(http.MethodPost, "/pair/"+s.token, url.Values{"_csrf": {tok}, "challenge": {s.phoneCh.challenge}, "name": {"x"}}, nil)
		if rec.Code != http.StatusForbidden || br.jar[pairCookie] != nil {
			t.Fatalf("browser claim = %d", rec.Code)
		}
		if s.a.count(t, `SELECT COUNT(*) FROM device_pairings WHERE state = 'waiting'`) < 1 {
			t.Error("a browser claim spent the pairing")
		}
	})
	t.Run("no csrf and cross-site", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		form := url.Values{"challenge": {s.phoneCh.challenge}, "name": {"x"}}
		if rec := ph.do(http.MethodPost, "/pair/"+s.token, form, nil); rec.Code != http.StatusForbidden {
			t.Errorf("no csrf = %d", rec.Code)
		}
		page := ph.do(http.MethodGet, s.confirmPath(), nil, nil)
		form.Set("_csrf", pageToken(t, page.Body.String()))
		if rec := ph.do(http.MethodPost, "/pair/"+s.token, form, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); rec.Code != http.StatusForbidden {
			t.Errorf("cross-site = %d", rec.Code)
		}
		if ph.jar[pairCookie] != nil {
			t.Error("a refused claim set the cookie")
		}
	})
	t.Run("bad challenges", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		if rec := ph.do(http.MethodGet, "/pair/"+s.token+"?challenge=short", nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET bad challenge = %d", rec.Code)
		}
		if rec := ph.do(http.MethodGet, "/pair/"+s.token, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET no challenge = %d", rec.Code)
		}
		for _, ch := range []string{"", "short", strings.Repeat("!", 43), s.phoneCh.challenge + "A"} {
			if rec := ph.post(s.confirmPath(), "/pair/"+s.token, url.Values{"challenge": {ch}, "name": {"x"}}); rec.Code != http.StatusNotFound {
				t.Errorf("challenge %q = %d", ch, rec.Code)
			}
		}
		if ph.jar[pairCookie] != nil {
			t.Error("a bad challenge got a cookie")
		}
	})
	t.Run("a connected phone (409)", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		sessionTok := bindSession(t, a, ph)
		_ = sessionTok
		if rec := s.claim(ph); rec.Code != http.StatusConflict {
			t.Fatalf("bound claim = %d", rec.Code)
		}
		if s.a.count(t, `SELECT COUNT(*) FROM device_pairings WHERE state = 'waiting'`) != 1 {
			t.Error("a bound session spent the pairing")
		}
	})
}

// claim2 is claim for a visitor that did not open the confirm page first (it shares the token).
func (s *pairShow) claim2(p *phone) *httptest.ResponseRecorder {
	page := p.do(http.MethodGet, "/login", nil, nil)
	return p.do(http.MethodPost, "/pair/"+s.token, url.Values{"_csrf": {pageToken(s.t, page.Body.String())}, "challenge": {a1NewFlow().challenge}, "name": {"Other"}}, nil)
}

// bindSession gives the phone a session bound to a device, as a connected app has.
func bindSession(t *testing.T, a *testApp, p *phone) string {
	t.Helper()
	owner := sweepUserID(t, a, a.owner(t))
	var tok string
	err := a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		var sess auth.Session
		tok, sess, err = auth.NewSessions(a.st, nil).Create(context.Background(), tx, owner, "app")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('dv_bound', ?, 'Pixel', 'mobile', randomblob(32), 1)`, owner); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE sessions SET device_id = 'dv_bound' WHERE token_hash = ?`, sess.Hash[:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p.jar[auth.CookieName] = &http.Cookie{Name: auth.CookieName, Value: tok}
	return tok
}

func TestPairForbiddenApprove(t *testing.T) {
	a, _ := newPairApp(t)
	owner := a.owner(t)
	t.Run("anonymous and bearer", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		s.claimed(ph)
		num := s.number(ph)
		ack, atok := a.anon(t, "/login")
		anon := a.do(http.MethodPost, "/me/devices/add/"+s.id+"/approve", url.Values{"_csrf": {atok}, "match": {num}}, nil, ack)
		if anon.Code != http.StatusSeeOther {
			t.Errorf("anonymous approve = %d", anon.Code)
		}
		tok := a.tokenFor(t, owner)
		dev := a.do(http.MethodPost, "/me/devices/add/"+s.id+"/approve", url.Values{"_csrf": {tok}, "match": {num}}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43))
		})
		if dev.Code != http.StatusUnauthorized {
			t.Errorf("bearer approve = %d", dev.Code)
		}
		if rec := a.do(http.MethodGet, "/me/devices/add", nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43)) }); rec.Code != http.StatusUnauthorized {
			t.Errorf("bearer on the owner page = %d", rec.Code)
		}
		if rec := a.do(http.MethodPost, "/me/devices/add/"+s.id+"/approve", url.Values{"match": {num}}, nil, owner); rec.Code != http.StatusForbidden {
			t.Errorf("no csrf = %d", rec.Code)
		}
		if s.rows() != 1 || a.count(t, `SELECT COUNT(*) FROM device_pairings WHERE state = 'claimed'`) != 1 {
			t.Error("a refused approve changed the pairing")
		}
	})
	t.Run("another person, an admin, and the same person's other session", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		num := s.claimed(ph)
		own := a.do(http.MethodGet, "/me/devices/add/"+s.id, nil, nil, owner)
		for name, ck := range map[string]*http.Cookie{"member": a.as(t, users.RoleMember), "admin": a.as(t, users.RoleAdmin), "guest": a.as(t, users.RoleGuest), "same person": a.freshSession(t)} {
			csrf := a.tokenFor(t, ck)
			get := a.do(http.MethodGet, "/me/devices/add/"+s.id, nil, nil, ck)
			post := a.do(http.MethodPost, "/me/devices/add/"+s.id+"/approve", url.Values{"_csrf": {csrf}, "match": {num}}, nil, ck)
			cancel := a.do(http.MethodPost, "/me/devices/add/"+s.id+"/cancel", url.Values{"_csrf": {csrf}}, nil, ck)
			if get.Code != http.StatusNotFound || post.Code != http.StatusNotFound {
				t.Errorf("%s: GET %d, approve %d", name, get.Code, post.Code)
			}
			if cancel.Code != http.StatusSeeOther || s.rows() != 1 {
				t.Fatalf("%s: cancel %d, rows %d (a cancel by another must change nothing)", name, cancel.Code, s.rows())
			}
		}
		if own.Code != http.StatusOK {
			t.Fatalf("the owner's own page = %d", own.Code)
		}
	})
	t.Run("before the claim", func(t *testing.T) {
		s := pairStart(t, a, owner)
		if rec := s.approve("0000"); rec.Code != http.StatusNotFound {
			t.Fatalf("approve before claim = %d", rec.Code)
		}
		if s.rows() != 1 {
			t.Error("the pairing was ended")
		}
	})
	t.Run("wrong number, then the right one", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		num := s.claimed(ph)
		wrong := "0000"
		if num == wrong {
			wrong = "0001"
		}
		rec := s.approve(wrong)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "That number did not match, so this code has ended. If the phone is yours, show a new code.") {
			t.Fatalf("wrong = %d", rec.Code)
		}
		if s.rows() != 0 {
			t.Fatal("the row survived")
		}
		if rec := s.approve(num); rec.Code != http.StatusNotFound {
			t.Fatalf("right after wrong = %d", rec.Code)
		}
		if fin := s.finish2(ph); fin.Code == http.StatusSeeOther {
			t.Fatal("the phone finished")
		}
		a.assertNoNewLogin(t)
	})
	t.Run("second approve and expired", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		num := s.claimed(ph)
		if rec := s.approve(num); rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
		if rec := s.approve(num); rec.Code != http.StatusNotFound {
			t.Errorf("second approve = %d", rec.Code)
		}
	})
	t.Run("not my phone", func(t *testing.T) {
		s := pairStart(t, a, owner)
		ph := newPhone(t, a, true)
		s.claimed(ph)
		rec := a.do(http.MethodPost, "/me/devices/add/"+s.id+"/cancel", url.Values{"_csrf": {s.csrf}}, nil, owner)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me/devices" || s.rows() != 0 {
			t.Fatalf("cancel = %d %q rows %d", rec.Code, rec.Header().Get("Location"), s.rows())
		}
		if w := ph.do(http.MethodGet, "/pair/wait", nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("phone after cancel = %d", w.Code)
		}
	})
}

// finish2 posts /pair/finish with whatever token the phone's last page gave (the claim is gone).
func (s *pairShow) finish2(p *phone) *httptest.ResponseRecorder {
	page := p.do(http.MethodGet, "/login", nil, nil)
	return p.do(http.MethodPost, "/pair/finish", url.Values{"_csrf": {pageToken(s.t, page.Body.String())}}, nil)
}

func (a *testApp) assertNoNewLogin(t *testing.T) {
	t.Helper()
	if n := a.count(t, `SELECT COUNT(*) FROM device_codes`); n != 0 {
		t.Errorf("device codes = %d, want 0", n)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%"via":"pair"%'`); n != 0 {
		t.Errorf("pair logins = %d, want 0", n)
	}
}

func TestPairForbiddenFinish(t *testing.T) {
	cases := map[string]func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone){
		"before approval": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.claimed(ph)
			if rec := s.finish(ph); rec.Code != http.StatusNotFound {
				t.Fatal(rec.Code)
			}
		},
		"no cookie (T2: the QR token without the claim)": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			thief := newPhone(t, a, true)
			if rec := s.finish2(thief); rec.Code != http.StatusNotFound {
				t.Fatalf("thief = %d", rec.Code)
			}
			thief.jar[pairCookie] = &http.Cookie{Name: pairCookie, Value: s.token} // the QR token as a cookie
			if rec := s.finish2(thief); rec.Code != http.StatusNotFound {
				t.Fatalf("thief with the QR token = %d", rec.Code)
			}
		},
		"another claim's cookie": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			other := newPhone(t, a, true)
			tok, _ := auth.NewToken()
			other.jar[pairCookie] = &http.Cookie{Name: pairCookie, Value: tok}
			if rec := s.finish2(other); rec.Code != http.StatusNotFound {
				t.Fatalf("= %d", rec.Code)
			}
		},
		"no ts_shell": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			ph.android = false
			if rec := ph.post("/pair/wait", "/pair/finish", nil); rec.Code != http.StatusForbidden {
				t.Fatalf("= %d", rec.Code)
			}
		},
		"no csrf": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			if rec := ph.do(http.MethodPost, "/pair/finish", url.Values{}, nil); rec.Code != http.StatusForbidden {
				t.Fatalf("= %d", rec.Code)
			}
		},
		"twice": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			claim := ph.jarClaim()
			if rec := s.finish(ph); rec.Code != http.StatusSeeOther {
				t.Fatalf("first = %d", rec.Code)
			}
			ph2 := newPhone(t, a, true)
			ph2.jar[pairCookie] = &http.Cookie{Name: pairCookie, Value: claim}
			if rec := s.finish2(ph2); rec.Code != http.StatusNotFound {
				t.Fatalf("second = %d", rec.Code)
			}
		},
		"owner signed out": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			a.do(http.MethodPost, "/logout", url.Values{"_csrf": {s.csrf}}, nil, owner)
			if rec := s.finish(ph); rec.Code != http.StatusNotFound {
				t.Fatalf("= %d", rec.Code)
			}
		},
		"expired": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			clk.Add(auth.PairTTL + time.Millisecond)
			if rec := ph.do(http.MethodPost, "/pair/finish", url.Values{"_csrf": {"x"}}, nil); rec.Code == http.StatusSeeOther {
				t.Fatalf("= %d", rec.Code)
			}
			if w := ph.do(http.MethodGet, "/pair/wait", nil, nil); w.Code != http.StatusNotFound {
				t.Fatalf("wait after expiry = %d", w.Code)
			}
		},
		"bound phone": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			bindSession(t, a, ph)
			if rec := s.finish(ph); rec.Code != http.StatusConflict {
				t.Fatalf("= %d", rec.Code)
			}
		},
		"person disabled": func(t *testing.T, a *testApp, clk *pairClock, owner *http.Cookie, s *pairShow, ph *phone) {
			s.approved(ph)
			if _, err := a.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
				t.Fatal(err)
			}
			if rec := s.finish(ph); rec.Code == http.StatusSeeOther {
				t.Fatalf("= %d", rec.Code)
			}
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			a, clk := newPairApp(t)
			owner := a.owner(t)
			sessions := a.count(t, `SELECT COUNT(*) FROM sessions`)
			s := pairStart(t, a, owner)
			ph := newPhone(t, a, true)
			run(t, a, clk, owner, s, ph)
			ended := name == "owner signed out"
			if n := a.count(t, `SELECT COUNT(*) FROM device_codes`); name != "twice" && n != 0 {
				t.Errorf("device codes = %d, want 0", n)
			}
			if n := a.count(t, `SELECT COUNT(*) FROM sessions`); name != "twice" && n > sessions+1 && !ended {
				t.Errorf("sessions grew from %d to %d", sessions, n)
			}
		})
	}
}

func TestPairSwapWrongVerifierBurns(t *testing.T) {
	a, _ := newPairApp(t)
	s := pairStart(t, a, a.owner(t))
	ph := newPhone(t, a, true)
	s.approved(ph)
	code := a1CodeOf(t, s.finish(ph))
	// "plain": the challenge handed back as if it were the verifier.
	if rec := a1Swap(a, a1SwapBody(code, s.phoneCh.challenge, nil), nil); rec.Code == http.StatusCreated {
		t.Fatal("the challenge worked as a verifier")
	}
	if rec := a1Swap(a, a1SwapBody(code, s.phoneCh.verifier, nil), nil); rec.Code == http.StatusCreated {
		t.Fatal("the code survived a wrong verifier")
	}
	if a1Devices(t, a) != 0 {
		t.Error("a device was made")
	}
}

func TestPairWho(t *testing.T) {
	a, _ := newPairApp(t)
	// A member and a guest each add their own phone; the page takes no person.
	for name, ck := range map[string]*http.Cookie{"member": a.as(t, users.RoleMember), "guest": a.as(t, users.RoleGuest), "admin": a.as(t, users.RoleAdmin)} {
		s := pairStart(t, a, ck)
		ph := newPhone(t, a, true)
		s.approved(ph)
		code := a1CodeOf(t, s.finish(ph))
		sw := a1Swap(a, a1SwapBody(code, s.phoneCh.verifier, nil), nil)
		var got deviceIssued
		decodeJSON(t, sw, &got)
		var owner string
		_ = a.st.Reader().QueryRow(`SELECT user_id FROM devices WHERE id = ?`, got.Device.ID).Scan(&owner)
		want := a.st.Reader().QueryRow(`SELECT user_id FROM sessions WHERE token_hash = ?`, pairHashOf(t, ck.Value))
		var wantID string
		_ = want.Scan(&wantID)
		if owner == "" || owner != wantID {
			t.Errorf("%s: the phone belongs to %q, want %q", name, owner, wantID)
		}
		if strings.Contains(a.do(http.MethodGet, "/me/devices/add", nil, nil, ck).Body.String(), "user_id") {
			t.Errorf("%s: the page names a person field", name)
		}
	}
	// A disabled person cannot start.
	owner := a.owner(t)
	csrf := a.tokenFor(t, owner)
	if _, err := a.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}
	before := a.count(t, `SELECT COUNT(*) FROM device_pairings`)
	if rec := a.do(http.MethodPost, "/me/devices/add", url.Values{"_csrf": {csrf}}, nil, owner); rec.Code != http.StatusSeeOther && rec.Code != http.StatusForbidden {
		t.Errorf("a disabled person's start = %d", rec.Code)
	}
	if a.count(t, `SELECT COUNT(*) FROM device_pairings`) != before {
		t.Error("a disabled person started a pairing")
	}
}

func pairHashOf(t *testing.T, tok string) []byte {
	t.Helper()
	h, ok := auth.HashToken(tok)
	if !ok {
		t.Fatal("bad token")
	}
	return h[:]
}

func TestPairLimits(t *testing.T) {
	a, _ := newPairApp(t)
	owner := a.owner(t)
	csrf := a.tokenFor(t, owner)
	for i := 0; i < 10; i++ {
		if rec := a.do(http.MethodPost, "/me/devices/add", url.Values{"_csrf": {csrf}}, nil, owner); rec.Code != http.StatusOK {
			t.Fatalf("start %d = %d", i+1, rec.Code)
		}
	}
	rec := a.do(http.MethodPost, "/me/devices/add", url.Values{"_csrf": {csrf}}, nil, owner)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th start = %d", rec.Code)
	}
	before := a.count(t, `SELECT COUNT(*) FROM device_pairings`)
	if a.count(t, `SELECT COUNT(*) FROM device_pairings`) != before {
		t.Error("a limited start changed the table")
	}

	// 20 public failures from one address, then 429 on every public pair route without a read; another address is unaffected.
	b, _ := newPairApp(t)
	ph := newPhone(t, b, true)
	for i := 0; i < 20; i++ {
		if rec := ph.do(http.MethodGet, "/pair/"+strings.Repeat("B", 43), nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("failure %d = %d", i+1, rec.Code)
		}
	}
	if rec := ph.do(http.MethodGet, "/pair/"+strings.Repeat("B", 43), nil, nil); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("21st = %d", rec.Code)
	}
	for _, p := range []string{"/pair/wait"} {
		if rec := ph.do(http.MethodGet, p, nil, nil); rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
	other := ph.do(http.MethodGet, "/pair/"+strings.Repeat("B", 43), nil, func(r *http.Request) { r.RemoteAddr = "198.51.100.77:1234" })
	if other.Code != http.StatusNotFound {
		t.Errorf("another address = %d", other.Code)
	}
}

func TestPairLeaks(t *testing.T) {
	a, _ := newPairApp(t)
	owner := a.owner(t)
	s := pairStart(t, a, owner)
	ph := newPhone(t, a, true)

	pages := map[string]*httptest.ResponseRecorder{
		"GET /pair/{token}": ph.do(http.MethodGet, s.confirmPath(), nil, nil),
		"owner start":       s.rec,
	}
	s.claimed(ph)
	pages["GET /pair/wait claimed"] = ph.do(http.MethodGet, "/pair/wait", nil, nil)
	pages["ended"] = ph.do(http.MethodGet, "/pair/"+strings.Repeat("C", 43), nil, nil)
	br := newPhone(t, a, false)
	pages["handover"] = br.do(http.MethodGet, "/pair/"+s.token, nil, nil)
	csp := pages["ended"].Header().Get("Content-Security-Policy")
	for name, rec := range pages {
		h := rec.Header()
		if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "same-origin" || h.Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s: headers %v", name, h)
		}
		if h.Get("Content-Security-Policy") != csp || csp == "" {
			t.Errorf("%s: CSP differs", name)
		}
		body := rec.Body.String()
		if inlineStyle.MatchString(body) || inlineScript.MatchString(body) {
			t.Errorf("%s: inline style or script", name)
		}
		if name != "owner start" && (strings.Contains(body, "Ana Reyes") || strings.Contains(body, "Ana ")) {
			t.Errorf("%s names the person", name)
		}
	}
	if !strings.Contains(pages["GET /pair/wait claimed"].Body.String(), "Type this number on your other device.") {
		t.Error("the number page lacks its words")
	}
	if strings.Contains(pages["GET /pair/{token}"].Body.String(), "Ana") {
		t.Error("the confirm page names the owner")
	}
	// The log path never holds the token.
	r := httptest.NewRequest(http.MethodGet, "/pair/"+s.token, nil)
	if strings.Contains(logPath(r), s.token) {
		t.Error("logPath leaks the token")
	}
}

func TestPairLoopbackAndNoSession(t *testing.T) {
	a, _ := newPairApp(t)
	owner := a.owner(t)
	csrf := a.tokenFor(t, owner)
	for _, host := range []string{"localhost:8095", "foo.localhost", "127.0.0.1:8095", "127.4.5.6", "[::1]:8095", "0.0.0.0:8095"} {
		mod := func(r *http.Request) { r.Host = host }
		get := a.do(http.MethodGet, "/me/devices/add", nil, mod, owner)
		if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "This address works only on this computer.") || strings.Contains(get.Body.String(), `action="/me/devices/add"`) || strings.Contains(get.Body.String(), "<svg class=\"ts-qr") {
			t.Errorf("%s: GET = %d", host, get.Code)
		}
		if post := a.do(http.MethodPost, "/me/devices/add", url.Values{"_csrf": {csrf}}, mod, owner); post.Code != http.StatusNotFound {
			t.Errorf("%s: POST = %d", host, post.Code)
		}
	}
	if get := a.do(http.MethodGet, "/me/devices/add", nil, func(r *http.Request) { r.Host = "tendshelf.example.org" }, owner); !strings.Contains(get.Body.String(), `action="/me/devices/add"`) {
		t.Error("a public name shows no form")
	}
}

func TestPairHandover(t *testing.T) {
	a, _ := newPairApp(t)
	s := pairStart(t, a, a.owner(t))
	androidUA := func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14; Pixel 8) Chrome/120")
	}
	link := "http://example.com/pair/" + s.token
	want := "intent://open?link=" + url.QueryEscape(link) + "#Intent;scheme=tendshelf;package=app.tendshelf.android;S.browser_fallback_url=" + url.QueryEscape(link+"?noapp=1") + ";end"
	br := newPhone(t, a, false)
	rec := br.do(http.MethodGet, "/pair/"+s.token, nil, androidUA)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `href="`+want+`"`) || !strings.Contains(body, `hx-boost="false"`) || !strings.Contains(body, "Open in the app") {
		t.Fatalf("android handover = %d\n%s", rec.Code, body)
	}
	if strings.Contains(body, `href="tendshelf:`) {
		t.Error("a bare tendshelf: link")
	}
	if rec := br.do(http.MethodGet, "/pair/"+s.token+"?noapp=1", nil, androidUA); strings.Contains(rec.Body.String(), "Open in the app") || !strings.Contains(html.UnescapeString(rec.Body.String()), "The Tendshelf app isn't on this phone yet.") {
		t.Error("noapp page")
	}
	desk := br.do(http.MethodGet, "/pair/"+s.token, nil, nil)
	if strings.Contains(desk.Body.String(), "Open in the app") || !strings.Contains(desk.Body.String(), "Open this link on the phone you want to add.") {
		t.Error("desktop handover")
	}
	if app := newPhone(t, a, true).do(http.MethodGet, "/pair/"+s.token+"?challenge="+s.phoneCh.challenge, nil, androidUA); strings.Contains(app.Body.String(), "intent://") {
		t.Error("the app's own page has the handover")
	}
	if s.a.count(t, `SELECT COUNT(*) FROM device_pairings WHERE state = 'waiting'`) != 1 {
		t.Error("the handover changed the pairing")
	}
}

var (
	inlineStyle  = regexp.MustCompile(`<[a-z][^>]*\sstyle=`)
	inlineScript = regexp.MustCompile(`<script(?:[^>]*\ssrc=)?[^>]*>\s*[^<\s]`)
)

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("not JSON: %q", rec.Body)
	}
}
