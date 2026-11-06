package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var testAddr = netip.MustParseAddr("203.0.113.7")

// freshSession signs the owner in on another session, as a second window of the same person.
func (a *testApp) freshSession(t *testing.T) *http.Cookie {
	t.Helper()
	owner := sweepUserID(t, a, a.owner(t))
	var tok string
	err := a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewSessions(a.st, nil).Create(context.Background(), tx, owner, "test")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: tok}
}

// The RFC 7636 appendix B pair: a verifier and its S256 challenge.
const (
	a1Verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	a1Challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

var a1Android = &http.Cookie{Name: shellCookie, Value: "android"}

// a1Flow is one app's start of the sign-in.
type a1Flow struct{ verifier, challenge string }

func a1NewFlow() a1Flow {
	v, _ := token.New()
	sum := sha256.Sum256([]byte(v))
	return a1Flow{v, base64.RawURLEncoding.EncodeToString(sum[:])}
}

// a1Connect plays the person's side: open the consent page in the app window, tap Connect. It returns
// the answer to the POST.
func a1Connect(t *testing.T, a *testApp, challenge string, cks ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	cks = append(cks, a1Android)
	page := a.do(http.MethodGet, "/app/connect?challenge="+url.QueryEscape(challenge), nil, nil, cks...)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /app/connect = %d", page.Code)
	}
	return a.do(http.MethodPost, "/app/connect", url.Values{"_csrf": {pageToken(t, page.Body.String())}, "challenge": {challenge}}, nil, cks...)
}

// a1CodeOf takes the code out of the 303 the way the app's window does.
func a1CodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || u.Host != "" || u.Scheme != "" || u.Path != "/app/connect/done" || u.Query().Get("code") == "" {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
	return u.Query().Get("code")
}

func a1SwapBody(code, verifier string, mod func(map[string]string)) string {
	m := map[string]string{"code": code, "verifier": verifier, "name": "Pixel", "kind": "mobile"}
	if mod != nil {
		mod(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func a1Swap(a *testApp, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return devicePost(a, "application/json", body, mod)
}

func a1Devices(t *testing.T, a *testApp) int {
	t.Helper()
	return a.count(t, `SELECT COUNT(*) FROM devices`)
}

func a1Field(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct{ Error struct{ Field string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("not JSON: %q", rec.Body)
	}
	return e.Error.Field
}

// a1LogBuffer captures slog for the test, so nothing that logs a secret goes unnoticed.
func a1LogBuffer(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestA1ConnectHappy(t *testing.T) {
	logs := a1LogBuffer(t)
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()

	// Anonymous: the gate sends the person to sign in and keeps where they were going.
	anon := a.do(http.MethodGet, "/app/connect?challenge="+f.challenge, nil, nil, a1Android)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next="+url.QueryEscape("/app/connect?challenge="+f.challenge) {
		t.Fatalf("anonymous = %d %q", anon.Code, anon.Header().Get("Location"))
	}

	page := a.do(http.MethodGet, "/app/connect?challenge="+f.challenge, nil, nil, ck, a1Android)
	body := page.Body.String()
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("page = %d, Cache-Control %q", page.Code, page.Header().Get("Cache-Control"))
	}
	for _, want := range []string{"Connect this phone", "This phone will be able to play, download and keep your place.", ">Connect<",
		`name="challenge" value="` + f.challenge + `"`, `action="/app/connect"`, `hx-boost="false"`, "data-shell=\"android\""} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if !strings.Contains(body, "This address is not private.") {
		t.Error("a plain http page lacks the warning")
	}
	if secure := a.do(http.MethodGet, "/app/connect?challenge="+f.challenge, nil, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
	}, ck, a1Android); secure.Code != http.StatusOK {
		t.Fatalf("secure page = %d", secure.Code)
	}

	rec := a1Connect(t, a, f.challenge, ck)
	code := a1CodeOf(t, rec)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("POST Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	// The page the 303 names holds no code, even though its request does.
	done := a.do(http.MethodGet, "/app/connect/done?code="+code, nil, nil, ck, a1Android)
	if done.Code != http.StatusOK || strings.Contains(done.Body.String(), code) || !strings.Contains(done.Body.String(), "Go back to the Tendshelf app.") {
		t.Errorf("done = %d, holds the code: %v", done.Code, strings.Contains(done.Body.String(), code))
	}
	if strings.Contains(page.Body.String(), code) || strings.Contains(rec.Body.String(), code) {
		t.Error("a page holds the code")
	}

	sw := a1Swap(a, a1SwapBody(code, f.verifier, nil), nil)
	if sw.Code != http.StatusCreated || sw.Header().Get("Cache-Control") != "no-store" || len(sw.Result().Cookies()) != 0 {
		t.Fatalf("swap = %d %v %s", sw.Code, sw.Header(), sw.Body)
	}
	var got deviceIssued
	if err := json.Unmarshal(sw.Body.Bytes(), &got); err != nil || got.Token == "" || got.Device.Kind != "mobile" || got.Device.Name != "Pixel" || got.User.Username != "ana" {
		t.Fatalf("shape: %+v %v", got, err)
	}

	// The device is listed as "This phone" in the window's own devices page, and the token works.
	devices := a.do(http.MethodGet, "/me/devices", nil, nil, ck)
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), "This phone") {
		t.Errorf("devices page = %d, lacks This phone", devices.Code)
	}
	if me := deviceBearerReq(a, http.MethodGet, "/api/v1/me", got.Token, nil); me.Code != http.StatusOK {
		t.Errorf("token on /api/v1/me = %d", me.Code)
	}
	if a.count(t, `SELECT COUNT(*) FROM sessions WHERE device_id = ?`, got.Device.ID) != 1 {
		t.Error("the session is not bound")
	}
	if a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.device_issued' AND detail_json LIKE '%"via":"code"%'`) != 1 {
		t.Error("the swap is not audited with via code")
	}

	// A wrong try, so the failure paths log too; then nothing anywhere may hold a secret.
	a1Swap(a, a1SwapBody(code, f.verifier, nil), nil)
	var audit string
	_ = a.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&audit)
	for name, secret := range map[string]string{"code": code, "verifier": f.verifier, "token": got.Token} {
		if strings.Contains(audit, secret) {
			t.Errorf("the audit log holds the %s", name)
		}
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log holds the %s", name)
		}
	}
}

func TestA1ConnectPageForbidden(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()

	// No window mark: words, no form, no code; the POST makes nothing.
	page := a.do(http.MethodGet, "/app/connect?challenge="+f.challenge, nil, nil, ck)
	if page.Code != http.StatusForbidden || strings.Contains(page.Body.String(), "<form") && strings.Contains(page.Body.String(), "/app/connect\"") ||
		!strings.Contains(page.Body.String(), "Open Tendshelf on your phone to connect it.") {
		t.Errorf("no mark: %d", page.Code)
	}
	tok := a.tokenFor(t, ck)
	if rec := a.do(http.MethodPost, "/app/connect", url.Values{"_csrf": {tok}, "challenge": {f.challenge}}, nil, ck); rec.Code != http.StatusForbidden || rec.Header().Get("Location") != "" {
		t.Errorf("POST without the mark = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Other marks are not the Android app.
	for _, v := range []string{"desktop", "ios", "browser", "ANDROID"} {
		c := &http.Cookie{Name: shellCookie, Value: v}
		if rec := a.do(http.MethodGet, "/app/connect?challenge="+f.challenge, nil, nil, ck, c); rec.Code != http.StatusForbidden {
			t.Errorf("mark %q = %d", v, rec.Code)
		}
	}

	// Bad challenges: 404 on both methods.
	short := f.challenge[:42]
	for name, ch := range map[string]string{"empty": "", "short": short, "long": f.challenge + "A", "plain verifier": f.verifier + f.verifier,
		"padded": f.challenge + "=", "not base64": strings.Repeat("!", 43)} {
		if rec := a.do(http.MethodGet, "/app/connect?challenge="+url.QueryEscape(ch), nil, nil, ck, a1Android); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d", name, rec.Code)
		}
		rec := a.do(http.MethodPost, "/app/connect", url.Values{"_csrf": {tok}, "challenge": {ch}}, nil, ck, a1Android)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d", name, rec.Code)
		}
	}

	// No CSRF token, a cross-site request and a stranger's cookie jar all make nothing.
	if rec := a.do(http.MethodPost, "/app/connect", url.Values{"challenge": {f.challenge}}, nil, ck, a1Android); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token = %d", rec.Code)
	}
	cross := a.do(http.MethodPost, "/app/connect", url.Values{"_csrf": {tok}, "challenge": {f.challenge}}, func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}, ck, a1Android)
	if cross.Code != http.StatusForbidden {
		t.Errorf("cross-site = %d", cross.Code)
	}
	anon := a.do(http.MethodPost, "/app/connect", url.Values{"challenge": {f.challenge}}, nil, a1Android)
	if anon.Code == http.StatusSeeOther && strings.Contains(anon.Header().Get("Location"), "code=") {
		t.Error("an anonymous POST made a code")
	}
	if a.count(t, `SELECT COUNT(*) FROM device_codes`) != 0 {
		t.Error("a refused request stored a code")
	}

	// A device token on any of the three routes: 401 in words, not a page.
	dev := deviceIssue(t, a)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/app/connect?challenge=" + f.challenge}, {http.MethodPost, "/app/connect"}, {http.MethodGet, "/app/connect/done"},
	} {
		rec := deviceBearerReq(a, c.method, c.path, dev.Token, func(r *http.Request) { r.AddCookie(a1Android) })
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Device tokens work only for /api/v1") {
			t.Errorf("bearer %s %s = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	// A token and a session cookie together stay 400.
	mixed := deviceBearerReq(a, http.MethodGet, "/api/v1/me", dev.Token, func(r *http.Request) { r.AddCookie(ck) })
	if mixed.Code != http.StatusBadRequest {
		t.Errorf("bearer plus cookie = %d", mixed.Code)
	}
}

func TestA1SwapForbidden(t *testing.T) {
	type step func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) (body string)
	right := func(code string, f a1Flow) string { return a1SwapBody(code, f.verifier, nil) }
	endSession := func(t *testing.T, a *testApp, ck *http.Cookie) {
		t.Helper()
		rec := a.do(http.MethodPost, "/logout", url.Values{"_csrf": {a.tokenFor(t, ck)}}, nil, ck)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("logout = %d", rec.Code)
		}
	}
	cases := []struct {
		name string
		want int
		do   step
	}{
		{"wrong verifier", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, a1NewFlow().verifier, nil)
		}},
		{"verifier of another code", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			g := a1NewFlow()
			a1CodeOf(t, a1Connect(t, a, g.challenge, ck)) // replaces the first code, as one session holds one
			return a1SwapBody(code, g.verifier, nil)
		}},
		{"challenge as verifier", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.challenge, nil)
		}},
		{"short verifier", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.verifier[:42], nil)
		}},
		{"no verifier", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, "", nil)
		}},
		{"unknown code", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			other, _ := token.New()
			return a1SwapBody(other, f.verifier, nil)
		}},
		{"malformed code", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody("abc", f.verifier, nil)
		}},
		{"expired", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			if _, err := a.st.Writer().Exec(`UPDATE device_codes SET expires_at = ?`, a.srv.now().UnixMilli()-1); err != nil {
				t.Fatal(err)
			}
			return right(code, f)
		}},
		{"code with a password", 400, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.verifier, func(m map[string]string) { m["username"], m["password"] = "ana", testPassword })
		}},
		{"kind desktop", 400, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.verifier, func(m map[string]string) { m["kind"] = "desktop" })
		}},
		{"kind opds", 400, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.verifier, func(m map[string]string) { m["kind"] = "opds" })
		}},
		{"kind koreader", 400, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.verifier, func(m map[string]string) { m["kind"] = "koreader" })
		}},
		{"no kind", 400, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			return a1SwapBody(code, f.verifier, func(m map[string]string) { delete(m, "kind") })
		}},
		{"signed out", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			endSession(t, a, ck)
			return right(code, f)
		}},
		{"person disabled", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			if _, err := a.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
				t.Fatal(err)
			}
			return right(code, f)
		}},
		{"password changed", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			sess, _, _, err := a.srv.sessions.Lookup(context.Background(), ck.Value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.srv.auth.ChangePassword(context.Background(), sess, testPassword, "another long password", testAddr); err != nil {
				t.Fatal(err)
			}
			return right(code, f)
		}},
		{"person deleted", 401, func(t *testing.T, a *testApp, ck *http.Cookie, f a1Flow, code string) string {
			if _, err := a.st.Writer().Exec(`DELETE FROM users`); err != nil {
				t.Fatal(err)
			}
			return right(code, f)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp(t)
			ck := a.owner(t)
			f := a1NewFlow()
			code := a1CodeOf(t, a1Connect(t, a, f.challenge, ck))
			before := a1Devices(t, a)
			body := c.do(t, a, ck, f, code)
			rec := a1Swap(a, body, nil)
			if rec.Code != c.want {
				t.Fatalf("swap = %d %s, want %d", rec.Code, rec.Body, c.want)
			}
			if c.want == 401 && !strings.Contains(rec.Body.String(), "This sign-in has expired. Start again from the app.") {
				t.Errorf("message: %s", rec.Body)
			}
			if c.name == "code with a password" && a1Field(t, rec) != "code" || strings.HasPrefix(c.name, "kind") && a1Field(t, rec) != "kind" || c.name == "no kind" && a1Field(t, rec) != "kind" {
				t.Errorf("field = %q", a1Field(t, rec))
			}
			if got := a1Devices(t, a); got != before {
				t.Errorf("devices %d -> %d", before, got)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Error("the answer set a cookie")
			}
		})
	}
}

func TestA1SwapReplayAndBurn(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()
	code := a1CodeOf(t, a1Connect(t, a, f.challenge, ck))
	if rec := a1Swap(a, right1(code, f), nil); rec.Code != http.StatusCreated {
		t.Fatalf("first use = %d", rec.Code)
	}
	before := a1Devices(t, a)
	if rec := a1Swap(a, right1(code, f), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("second use = %d", rec.Code)
	}
	// A wrong verifier burns the code: the right one fails afterwards.
	g := a1NewFlow()
	code2 := a1CodeOf(t, a1Connect(t, a, g.challenge, ck))
	if rec := a1Swap(a, a1SwapBody(code2, f.verifier, nil), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong verifier = %d", rec.Code)
	}
	if rec := a1Swap(a, right1(code2, g), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("right verifier after a wrong one = %d", rec.Code)
	}
	if a1Devices(t, a) != before {
		t.Error("a refused swap added a device")
	}
}

func right1(code string, f a1Flow) string { return a1SwapBody(code, f.verifier, nil) }

func TestA1SwapLimit(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()
	code := a1CodeOf(t, a1Connect(t, a, f.challenge, ck))
	for i := 0; i < keyFailMax; i++ {
		if rec := a1Swap(a, a1SwapBody("abc", f.verifier, nil), nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d", i, rec.Code)
		}
	}
	// Past the limit even the good code is refused, before it is read: it stays unspent.
	rec := a1Swap(a, right1(code, f), nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("past the limit = %d %v", rec.Code, rec.Header())
	}
	if a.count(t, `SELECT COUNT(*) FROM device_codes`) != 1 {
		t.Error("the limited request spent the code")
	}
	// Another address is not affected.
	elsewhere := a1Swap(a, right1(code, f), func(r *http.Request) { r.RemoteAddr = "198.51.100.9:4000" })
	if elsewhere.Code != http.StatusCreated {
		t.Errorf("another address = %d %s", elsewhere.Code, elsewhere.Body)
	}
}

func TestA1SwapRebind(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()
	first := a1Swap(a, right1(a1CodeOf(t, a1Connect(t, a, f.challenge, ck)), f), nil)
	var dev deviceIssued
	if err := json.Unmarshal(first.Body.Bytes(), &dev); err != nil || dev.Token == "" {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}

	// The phone reconnects: a second window session, the token as caller. 200, no token, no new device.
	g := a1NewFlow()
	ck2 := a.freshSession(t)
	code := a1CodeOf(t, a1Connect(t, a, g.challenge, ck2))
	before := a1Devices(t, a)
	rec := a1Swap(a, right1(code, g), func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+dev.Token) })
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out["token"] != nil || out["device"] == nil || a1Devices(t, a) != before {
		t.Fatalf("rebind = %d %s", rec.Code, rec.Body)
	}
	if a.count(t, `SELECT COUNT(*) FROM sessions WHERE device_id = ?`, dev.Device.ID) != 2 {
		t.Error("the new session is not bound to the old device")
	}

	// A dead bearer is a 401 from the gate and the code is still good without it.
	h := a1NewFlow()
	code2 := a1CodeOf(t, a1Connect(t, a, h.challenge, ck2))
	dead, _ := token.New()
	if rec := a1Swap(a, right1(code2, h), func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+dead) }); rec.Code != http.StatusUnauthorized {
		t.Errorf("dead bearer = %d", rec.Code)
	}
	if rec := a1Swap(a, right1(code2, h), nil); rec.Code != http.StatusCreated {
		t.Errorf("the code after a dead bearer = %d", rec.Code)
	}

	// A bearer of another person: 409, nothing bound, code burned.
	m := a.as(t, users.RoleMember)
	mid := sweepUserID(t, a, m)
	mtok := deviceBearer(t, a, mid)
	i := a1NewFlow()
	code3 := a1CodeOf(t, a1Connect(t, a, i.challenge, ck2))
	other := a1Swap(a, right1(code3, i), func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+mtok) })
	if other.Code != http.StatusConflict {
		t.Errorf("another person's bearer = %d %s", other.Code, other.Body)
	}
	if rec := a1Swap(a, right1(code3, i), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a code burned by a 409 = %d", rec.Code)
	}
}

func TestA1SwapDeviceLimit(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	owner := sweepUserID(t, a, ck)
	var first string
	for i := 0; i < auth.MaxLiveDevices-1; i++ {
		tok := deviceBearer(t, a, owner)
		if first == "" {
			first = tok
		}
	}
	f := a1NewFlow()
	rec := a1Swap(a, right1(a1CodeOf(t, a1Connect(t, a, f.challenge, ck)), f), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the last slot = %d %s", rec.Code, rec.Body)
	}
	var dev deviceIssued
	_ = json.Unmarshal(rec.Body.Bytes(), &dev)
	g := a1NewFlow()
	ck2 := a.freshSession(t)
	code := a1CodeOf(t, a1Connect(t, a, g.challenge, ck2))
	full := a1Swap(a, right1(code, g), nil)
	if full.Code != http.StatusConflict {
		t.Fatalf("at the limit = %d %s", full.Code, full.Body)
	}
	// The same phone can still reconnect at the limit.
	h := a1NewFlow()
	code2 := a1CodeOf(t, a1Connect(t, a, h.challenge, ck2))
	if rec := a1Swap(a, right1(code2, h), func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+dev.Token) }); rec.Code != http.StatusOK {
		t.Errorf("rebind at the limit = %d %s", rec.Code, rec.Body)
	}
}
