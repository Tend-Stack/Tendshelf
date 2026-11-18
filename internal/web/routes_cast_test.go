package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// castLab is the feed lab (books, people, one https name behind a trusted proxy) with casting switched on for chosen
// people.
type castLab struct{ *feedLab }

func newCastLab(t *testing.T) *castLab { return &castLab{newFeedLab(t)} }

// setCast writes the casting switch of a lab person straight into the database.
func (l *castLab) setCast(t *testing.T, who string, on bool) {
	t.Helper()
	ctx := context.Background()
	set, err := l.a.srv.users.LoadSettings(ctx, l.ids[who])
	if err != nil {
		t.Fatal(err)
	}
	set.Cast = on
	if err := l.a.srv.users.SaveSettings(ctx, l.ids[who], set); err != nil {
		t.Fatal(err)
	}
}

// mintReq posts to /casting/address as a lab person.
func (l *castLab) mintReq(t *testing.T, who, kind, id string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	ck := l.cookies[who]
	csrf := l.a.tokenFor(t, ck)
	return l.a.do(http.MethodPost, "/casting/address", url.Values{"kind": {kind}, "id": {id}}, func(r *http.Request) {
		proxyDefault(r)
		r.Header.Set("X-CSRF-Token", csrf)
		for _, m := range mod {
			m(r)
		}
	}, ck)
}

// mint turns casting on for who, mints and returns the raw token and the answered base.
func (l *castLab) mint(t *testing.T, who, kind, id string) (tok, base string) {
	t.Helper()
	l.setCast(t, who, true)
	rec := l.mintReq(t, who, kind, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint %s %s = %d %s", who, kind, rec.Code, rec.Body.String())
	}
	var out struct {
		Base      string `json:"base"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ExpiresAt == 0 {
		t.Fatalf("mint body %q: %v", rec.Body.String(), err)
	}
	m := regexp.MustCompile(`^https://podcasts\.example/cast/([A-Za-z0-9_-]{43})$`).FindStringSubmatch(out.Base)
	if m == nil {
		t.Fatalf("base = %q", out.Base)
	}
	return m[1], out.Base
}

func TestCastMintAndFetch(t *testing.T) {
	l := newCastLab(t)
	tok, base := l.mint(t, "owner", "book", l.multi.ID)
	if !strings.HasPrefix(base, "https://podcasts.example/cast/") {
		t.Fatalf("base %q", base)
	}
	f := l.files[l.multi.ID][1]
	rec := l.get("/cast/" + tok + "/audio/" + f.ID)
	if rec.Code != http.StatusOK || !equalBytes(rec.Body.Bytes(), l.body[f.ID]) {
		t.Fatalf("audio = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	h := rec.Header()
	want := map[string]string{"Cache-Control": "private, no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox", "Access-Control-Allow-Origin": "*"}
	for k, v := range want {
		if h.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, h.Get(k), v)
		}
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials header sent")
	}
	head := l.a.do(http.MethodHead, "/cast/"+tok+"/audio/"+f.ID, nil, proxyDefault)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD = %d, %d bytes", head.Code, head.Body.Len())
	}
	if rec := l.get("/cast/" + tok + "/art"); rec.Code != http.StatusNotFound { // multi has no cover
		t.Errorf("art without a cover = %d", rec.Code)
	}
	tok2, _ := l.mint(t, "owner", "book", l.single.ID)
	if rec := l.get("/cast/" + tok2 + "/art"); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/") {
		t.Errorf("cover art = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestCastRangeAndCompat(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.multi.ID)
	f := l.files[l.multi.ID][0]
	rec := l.get("/cast/"+tok+"/audio/"+f.ID, func(r *http.Request) { r.Header.Set("Range", "bytes=2-9") })
	if rec.Code != http.StatusPartialContent || !equalBytes(rec.Body.Bytes(), l.body[f.ID][2:10]) || rec.Header().Get("Content-Range") == "" {
		t.Errorf("range = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	// ?compat=1 takes the compat path: a file the browser needs no copy for is refused there, as on /stream.
	if rec := l.get("/cast/" + tok + "/audio/" + f.ID + "?compat=1"); rec.Code != http.StatusNotFound {
		t.Errorf("compat on a plain file = %d", rec.Code)
	}
	if rec := l.get("/cast/" + tok + "/audio/" + f.ID + "?compat=2"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad compat = %d", rec.Code)
	}
}

func TestCastMintForbiddenRoles(t *testing.T) {
	l := newCastLab(t)
	// Casting off: 404 even for the owner with the right.
	if rec := l.mintReq(t, "owner", "book", l.single.ID); rec.Code != http.StatusNotFound {
		t.Errorf("casting off = %d", rec.Code)
	}
	for _, who := range []string{"m_dl", "m_part", "g_dl"} {
		l.setCast(t, who, true)
	}
	l.setCast(t, "owner", true)
	// m_part has only library 1: sb is in library 2.
	if rec := l.mintReq(t, "m_part", "book", l.sb.ID); rec.Code != http.StatusNotFound {
		t.Errorf("no grant = %d", rec.Code)
	}
	// A refused book and a text-only (missing) book are not playable.
	for name, id := range map[string]string{"refused": l.refused.ID, "unknown": "bk_nope"} {
		if rec := l.mintReq(t, "owner", "book", id); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := l.mintReq(t, "owner", "book", l.single.ID, func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("json body = %d", rec.Code)
	}
	for _, c := range []struct{ kind, id string }{{"stations", "x"}, {"file", "x"}, {"book", ""}, {"book", strings.Repeat("x", 80)}} {
		if rec := l.mintReq(t, "owner", c.kind, c.id); rec.Code != http.StatusBadRequest {
			t.Errorf("%q %q = %d", c.kind, c.id, rec.Code)
		}
	}
	// A disabled person cannot mint (the gate refuses before the handler).
	csrf := l.a.tokenFor(t, l.cookies["m_dl"])
	l.exec(t, `UPDATE users SET disabled = 1 WHERE id = ?`, l.ids["m_dl"])
	dis := l.a.do(http.MethodPost, "/casting/address", url.Values{"kind": {"book"}, "id": {l.single.ID}}, func(r *http.Request) {
		proxyDefault(r)
		r.Header.Set("X-CSRF-Token", csrf)
	}, l.cookies["m_dl"])
	if dis.Code == http.StatusOK {
		t.Errorf("disabled = %d", dis.Code)
	}
	// Podcasts are off in this lab: an episode id is a 404.
	if rec := l.mintReq(t, "owner", "episode", "pe_x"); rec.Code != http.StatusNotFound {
		t.Errorf("episode with podcasts off = %d", rec.Code)
	}
	// With the right: 200, no-store, JSON.
	rec := l.mintReq(t, "m_part", "book", l.single.ID)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("member with the right = %d %v", rec.Code, rec.Header())
	}
	// Anonymous: the gate sends the visitor away.
	if rec := l.a.do(http.MethodPost, "/casting/address", url.Values{"kind": {"book"}, "id": {l.single.ID}}, proxyDefault); rec.Code == http.StatusOK {
		t.Errorf("anonymous = %d", rec.Code)
	}
}

func TestCastUnreachableHost(t *testing.T) {
	l := newCastLab(t)
	l.setCast(t, "owner", true)
	for name, mod := range map[string]func(*http.Request){
		"ip":        func(r *http.Request) { r.Host = "127.0.0.1:8095" },
		"localhost": func(r *http.Request) { r.Host = "localhost" },
		"insecure":  func(r *http.Request) { r.Header.Del("X-Forwarded-Proto") },
	} {
		rec := l.mintReq(t, "owner", "book", l.single.ID, mod)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "unreachable") {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body.String())
		}
	}
	var n int
	l.a.st.Reader().QueryRow(`SELECT count(*) FROM cast_tokens`).Scan(&n)
	if n != 0 {
		t.Errorf("%d tokens minted for unreachable hosts", n)
	}
}

func TestCastFetchRechecksGrant(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "g_dl", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	if rec := l.get("/cast/" + tok + "/audio/" + f); rec.Code != http.StatusOK {
		t.Fatalf("before = %d", rec.Code)
	}
	l.exec(t, `DELETE FROM grants WHERE user_id = ?`, l.ids["g_dl"])
	if rec := l.get("/cast/" + tok + "/audio/" + f); rec.Code != http.StatusNotFound || rec.Body.String() != "Not found.\n" {
		t.Errorf("after = %d %q", rec.Code, rec.Body.String())
	}
}

func TestCastRevokedOnSignOut(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "m_dl", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	ck := l.cookies["m_dl"]
	rec := l.a.do(http.MethodPost, "/logout", url.Values{"_csrf": {l.a.tokenFor(t, ck)}}, proxyDefault, ck)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := l.get("/cast/" + tok + "/audio/" + f); rec.Code != http.StatusNotFound {
		t.Errorf("after sign-out = %d", rec.Code)
	}
}

func TestCastRevokedWhenTurnedOff(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	ck := l.owner
	rec := l.a.do(http.MethodPost, "/me/settings", url.Values{"cast": {"0", "0"}}, func(r *http.Request) {
		proxyDefault(r)
		r.Header.Set("X-CSRF-Token", l.a.tokenFor(t, ck))
		r.Header.Set("HX-Request", "true")
	}, ck)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("settings = %d, refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
	if r := l.get("/cast/" + tok + "/audio/" + f); r.Code != http.StatusNotFound {
		t.Errorf("after off = %d", r.Code)
	}
	var n int
	l.a.st.Reader().QueryRow(`SELECT count(*) FROM cast_tokens WHERE user_id = ?`, l.ownerID).Scan(&n)
	if n != 0 {
		t.Errorf("%d tokens left", n)
	}
}

// TestCastOffAtFetch: the switch is flipped in the database only, so the tokens still exist; the fetch re-reads it.
func TestCastOffAtFetch(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	l.setCast(t, "owner", false)
	if rec := l.get("/cast/" + tok + "/audio/" + f); rec.Code != http.StatusNotFound {
		t.Errorf("casting off at fetch = %d", rec.Code)
	}
}

func TestCastExpiredAtFetch(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	*l.clock = l.clock.Add(share.CastMaxLife + time.Hour)
	var before int
	l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log`).Scan(&before)
	if rec := l.get("/cast/" + tok + "/audio/" + f); rec.Code != http.StatusNotFound {
		t.Errorf("expired = %d", rec.Code)
	}
	var after int
	l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log`).Scan(&after)
	if after != before {
		t.Error("an expired token was counted as a guess")
	}
}

func TestCastWrongKindAndOtherBook(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.multi.ID)
	if rec := l.get("/cast/" + tok + "/episode"); rec.Code != http.StatusNotFound {
		t.Errorf("episode path with a book token = %d", rec.Code)
	}
	other := l.files[l.single.ID][0].ID
	if rec := l.get("/cast/" + tok + "/audio/" + other); rec.Code != http.StatusNotFound {
		t.Errorf("another book's file = %d", rec.Code)
	}
	if rec := l.get("/cast/" + tok + "/audio/af_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown file = %d", rec.Code)
	}
	// A book token for a copy-protected file in a book that is not refused as a whole.
	ptok, _ := l.mint(t, "owner", "book", l.protected.ID)
	if rec := l.get("/cast/" + ptok + "/audio/" + l.files[l.protected.ID][0].ID); rec.Code != http.StatusNotFound {
		t.Errorf("protected file = %d", rec.Code)
	}
	// A station token is not served in this package (decision 0047, P3).
	stok, _, err := l.a.srv.casts.Mint(context.Background(), l.ownerID, share.CastStation, "st_1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/audio/" + other, "/episode", "/art"} {
		if rec := l.get("/cast/" + stok + p); rec.Code != http.StatusNotFound {
			t.Errorf("station token on %s = %d", p, rec.Code)
		}
	}
}

func TestCastMethods(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	for _, p := range []string{"/cast/" + tok + "/audio/" + f, "/cast/" + tok + "/episode", "/cast/" + tok + "/art"} {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			rec := l.a.do(m, p, url.Values{"x": {"y"}}, proxyDefault)
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s = %d, Allow %q", m, p, rec.Code, rec.Header().Get("Allow"))
			}
		}
	}
}

func TestCastPathClean(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	for _, p := range []string{"/cast/x/../audio/y", "/cast//" + tok + "/audio/" + f, "/cast/" + tok + "//audio/" + f,
		"/cast/" + tok + "/./audio/" + f, "/cast/" + tok + "/../" + tok + "/audio/" + f, "/cast/" + tok, "/cast/", "/cast/" + tok + "/nothing"} {
		rec := l.get(p)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" || strings.Contains(rec.Body.String(), tok) {
			t.Errorf("%s = %d, location %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestCastNoCookieEitherWay(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	// A valid session cookie plus a bad token is the plain 404 with no cookie.
	bad := "/cast/" + strings.Repeat("D", 43) + "/audio/" + f
	rec := l.get(bad, func(r *http.Request) { r.AddCookie(l.owner) })
	if rec.Code != http.StatusNotFound || rec.Body.String() != "Not found.\n" || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("bad token with a session = %d %q %v", rec.Code, rec.Body.String(), rec.Header()["Set-Cookie"])
	}
	// A good token answers with no Set-Cookie, with or without a cookie.
	for _, mod := range []func(*http.Request){func(*http.Request) {}, func(r *http.Request) { r.AddCookie(l.owner) }} {
		rec := l.get("/cast/"+tok+"/audio/"+f, mod)
		if rec.Code != http.StatusOK || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("good token = %d, cookie %v", rec.Code, rec.Header()["Set-Cookie"])
		}
	}
	// The cast address does not read the session: someone else's cookie changes nothing.
	other := l.a.as(t, users.RoleMember)
	if rec := l.get("/cast/"+tok+"/audio/"+f, func(r *http.Request) { r.AddCookie(other) }); rec.Code != http.StatusOK {
		t.Errorf("good token with another cookie = %d", rec.Code)
	}
}

func TestCastTokenOnlyOnCastPaths(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	fileID := l.files[l.single.ID][0].ID
	paths := []string{"/", "/me", "/me/devices", "/stream/" + fileID, "/hx/player/" + l.single.ID, "/api/v1/books", "/podcasts/audio/pe_x",
		"/feeds/" + tok + "/feed.xml", "/covers/" + l.single.ID, "/admin", "/opds", "/s/" + tok, "/" + tok}
	ways := map[string]func(*http.Request){
		"segment": nil,
		"cookie":  func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "ts_session", Value: tok}) },
		"bearer":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) },
		"basic":   func(r *http.Request) { r.SetBasicAuth("owner", tok) },
		"query":   func(r *http.Request) { r.URL.RawQuery = "token=" + tok },
		"header":  func(r *http.Request) { r.Header.Set("X-Api-Key", tok) },
	}
	for _, p := range paths {
		for name, mod := range ways {
			var m []func(*http.Request)
			if mod != nil {
				m = append(m, mod)
			}
			rec := l.get(p, m...)
			// The cookie chain may clear a session cookie it does not know; it must never set a live one.
			live := false
			for _, c := range rec.Result().Cookies() {
				live = live || c.Name == "ts_session" && c.Value != ""
			}
			if rec.Code == http.StatusOK || live {
				t.Errorf("%s with the token as %s = %d, cookie %q", p, name, rec.Code, rec.Header().Get("Set-Cookie"))
			}
		}
	}
}

func TestCastGuessLimited(t *testing.T) {
	l := newCastLab(t)
	good, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	same := func(r *http.Request) { r.Header.Set("X-Forwarded-For", "198.51.100.77") }
	guess := strings.Repeat("B", 43)
	for i := 0; i < keyFailMax; i++ {
		if rec := l.get("/cast/"+guess+"/audio/"+f, same); rec.Code != http.StatusNotFound {
			t.Fatalf("guess %d = %d", i, rec.Code)
		}
	}
	rec := l.get("/cast/"+good+"/audio/"+f, same)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("limited = %d, retry %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	rows, err := l.a.st.Reader().Query(`SELECT detail_json FROM audit_log WHERE action = 'cast.failed'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var d string
		rows.Scan(&d)
		n++
		if strings.Contains(d, guess) || strings.Contains(d, good) {
			t.Errorf("audit row holds a token: %s", d)
		}
	}
	if n != keyFailMax {
		t.Errorf("audit rows = %d, want %d", n, keyFailMax)
	}
	if rec := l.get("/cast/" + good + "/audio/" + f); rec.Code != http.StatusOK {
		t.Errorf("other address = %d", rec.Code)
	}
}

func TestSettingsCastSwitch(t *testing.T) {
	l := newCastLab(t)
	rec := l.a.do(http.MethodGet, "/me/settings/playback", nil, proxyDefault, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, s := range []string{"Cast to a TV or speaker", "Google's casting code runs in this browser when you open the player.", "Off: nothing from Google loads.",
		`name="cast"`, "ends every casting address at once"} {
		if !strings.Contains(body, s) {
			t.Errorf("settings page lacks %q", s)
		}
	}
	if strings.Contains(body, "can't start here") || strings.Contains(body, "a TV can't reach it") {
		t.Error("a note shows on a secure, reachable address")
	}
	post := func(v string) *httptest.ResponseRecorder {
		return l.a.do(http.MethodPost, "/me/settings", url.Values{"cast": {"0", v}}, func(r *http.Request) {
			proxyDefault(r)
			r.Header.Set("X-CSRF-Token", l.a.tokenFor(t, l.owner))
			r.Header.Set("HX-Request", "true")
		}, l.owner)
	}
	if rec := post("1"); rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
		t.Errorf("save on = %d, refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
	if set, _ := l.a.srv.users.LoadSettings(context.Background(), l.ownerID); !set.Cast {
		t.Error("cast not saved")
	}
	// The same value again changes nothing, so the page is not reloaded for it.
	if rec := post("1"); rec.Header().Get("HX-Refresh") != "" {
		t.Errorf("unchanged save refreshes: %q", rec.Header().Get("HX-Refresh"))
	}
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	if rec := post("0"); rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
		t.Errorf("save off = %d, refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
	if _, _, err := l.a.srv.casts.Lookup(context.Background(), tok); err == nil {
		t.Error("token survived turning casting off")
	}
	// Notes: not secure, not reachable, in an app.
	for name, c := range map[string]struct {
		mod  func(*http.Request)
		note string
	}{
		"insecure":    {func(r *http.Request) { r.Header.Del("X-Forwarded-Proto") }, "This address is not secure (https), so casting can't start here."},
		"unreachable": {func(r *http.Request) { r.Host = "127.0.0.1:8095" }, "This server is open only on this computer, so a TV can't reach it."},
		"shell":       {func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "ts_shell", Value: "desktop"}) }, "Casting works in Chrome, not in this app."},
	} {
		rec := l.a.do(http.MethodGet, "/me/settings/playback", nil, func(r *http.Request) { proxyDefault(r); c.mod(r) }, l.owner)
		if !strings.Contains(rec.Body.String(), c.note) {
			t.Errorf("%s: note %q missing (status %d)", name, c.note, rec.Code)
		}
	}
}
