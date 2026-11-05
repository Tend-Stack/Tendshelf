package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// deviceBody is the JSON the token route takes, for the owner "ana" unless changed.
func deviceBody(mod func(map[string]string)) string {
	m := map[string]string{"username": "ana", "password": testPassword, "name": "Kitchen phone", "kind": "mobile"}
	if mod != nil {
		mod(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// devicePost sends a raw body to POST /api/v1/auth/token.
func devicePost(a *testApp, contentType, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, apiTokenPath, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

type deviceIssued struct {
	Token  string `json:"token"`
	Device struct {
		ID, Name, Kind string
		CreatedAt      int64
	} `json:"device"`
	User struct {
		ID, Username, DisplayName, Role string
	} `json:"user"`
}

// deviceIssue signs the owner in with a token and returns it. The owner exists once a.owner ran.
func deviceIssue(t *testing.T, a *testApp) deviceIssued {
	t.Helper()
	a.owner(t)
	rec := devicePost(a, "application/json", deviceBody(nil), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	var out deviceIssued
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func deviceBearerReq(a *testApp, method, path, tok string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return a.do(method, path, nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+tok)
		if mod != nil {
			mod(r)
		}
	})
}

func deviceErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct{ Error struct{ Code string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("not JSON: %q", rec.Body)
	}
	return e.Error.Code
}

func TestAPITokenIssue(t *testing.T) {
	a := newTestApp(t)
	got := deviceIssue(t, a)
	rec := devicePost(a, "application/json; charset=utf-8", deviceBody(nil), nil)
	if rec.Code != 201 || rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("issue: %d %v", rec.Code, rec.Header())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a token answer set a cookie")
	}
	if got.Device.Name != "Kitchen phone" || got.Device.Kind != "mobile" || got.Device.ID == "" || got.Device.CreatedAt == 0 ||
		got.User.Username != "ana" || got.User.Role != "owner" {
		t.Fatalf("shape: %+v", got)
	}
	raw, err := base64.RawURLEncoding.DecodeString(got.Token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token: %v", err)
	}
	sum := sha256.Sum256(raw)
	var n int
	if err := a.st.Reader().QueryRow(`SELECT COUNT(*) FROM devices WHERE token_hash = ?`, sum[:]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hash stored: %d %v", n, err)
	}
}

func TestAPITokenRefusals(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	json_ := "application/json"
	for _, c := range []struct {
		name, ct, body string
		want           int
		code           string
	}{
		{"form body", "application/x-www-form-urlencoded", "username=ana", 415, "unsupported"},
		{"no content type", "", deviceBody(nil), 415, "unsupported"},
		{"too large", json_, deviceBody(func(m map[string]string) { m["name"] = strings.Repeat("x", 5000) }), 413, "too_large"},
		{"bad json", json_, "{nope", 400, "invalid"},
		{"bad kind", json_, deviceBody(func(m map[string]string) { m["kind"] = "tv" }), 400, "invalid"},
		{"no kind", json_, deviceBody(func(m map[string]string) { delete(m, "kind") }), 400, "invalid"},
		{"wrong password", json_, deviceBody(func(m map[string]string) { m["password"] = "nope" }), 401, "unauthorized"},
		{"unknown user", json_, deviceBody(func(m map[string]string) { m["username"] = "ghost" }), 401, "unauthorized"},
	} {
		rec := devicePost(a, c.ct, c.body, nil)
		if rec.Code != c.want || deviceErrCode(t, rec) != c.code {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	// Unknown JSON fields are ignored.
	rec := devicePost(a, json_, `{"username":"ana","password":"`+testPassword+`","kind":"web","extra":1}`, nil)
	if rec.Code != 201 {
		t.Errorf("extra field: %d %s", rec.Code, rec.Body)
	}
}

func TestAPITokenCap(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	for i := 0; i < auth.MaxLiveDevices; i++ {
		if _, err := a.st.Writer().Exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at)
			SELECT 'dev_cap'||?, id, 'x', 'web', randomblob(32), 1 FROM users WHERE username = 'ana'`, i); err != nil {
			t.Fatal(err)
		}
	}
	// Cap rows are old: make them recent so the idle rule does not retire them first.
	if _, err := a.st.Writer().Exec(`UPDATE devices SET created_at = ?`, a.srv.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	rec := devicePost(a, "application/json", deviceBody(nil), nil)
	if rec.Code != 409 || deviceErrCode(t, rec) != "conflict" {
		t.Fatalf("51st: %d %s", rec.Code, rec.Body)
	}
}

func TestAPITokenRateLimit(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	bad := deviceBody(func(m map[string]string) { m["password"] = "wrong" })
	for i := 0; i < 10; i++ {
		if rec := devicePost(a, "application/json", bad, nil); rec.Code != 401 {
			t.Fatalf("try %d: %d", i, rec.Code)
		}
	}
	rec := devicePost(a, "application/json", deviceBody(nil), nil) // right password, but the account is locked
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Code != 429 || deviceErrCode(t, rec) != "rate_limited" || err != nil || secs < 1 {
		t.Fatalf("limited: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var n int
	_ = a.st.Reader().QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	if n != 0 {
		t.Fatal("a token was issued while limited")
	}
}

func TestAPIBearerAndCookie(t *testing.T) {
	a := newTestApp(t)
	tok := deviceIssue(t, a)

	rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok.Token, nil)
	var me struct {
		User   struct{ Username string }
		Device *struct{ ID, Name, Kind string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || rec.Code != 200 || me.Device == nil || me.Device.ID != tok.Device.ID || me.User.Username != "ana" {
		t.Fatalf("bearer me: %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a bearer answer set a cookie")
	}

	rec = a.do(http.MethodGet, "/api/v1/me", nil, nil, a.owner(t))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"device":null`) {
		t.Fatalf("cookie me: %d %s", rec.Code, rec.Body)
	}

	rec = a.do(http.MethodGet, "/api/v1/me", nil, nil)
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" || deviceErrCode(t, rec) != "unauthorized" {
		t.Fatalf("anonymous me: %d %v", rec.Code, rec.Header())
	}

	// A token works nowhere outside /api/v1.
	rec = deviceBearerReq(a, http.MethodGet, "/me", tok.Token, nil)
	if rec.Code != 401 || deviceErrCode(t, rec) != "unauthorized" {
		t.Fatalf("bearer on /me: %d %s", rec.Code, rec.Body)
	}
	// An unknown or malformed token is refused, never treated as anonymous.
	for _, bad := range []string{"", "abc", strings.Repeat("A", 43)} {
		if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", bad, nil); rec.Code != 401 {
			t.Errorf("token %q: %d", bad, rec.Code)
		}
	}
	// The scheme is case-insensitive.
	rec = a.do(http.MethodGet, "/api/v1/me", nil, func(r *http.Request) { r.Header.Set("Authorization", "bearer "+tok.Token) })
	if rec.Code != 200 {
		t.Fatalf("lower-case scheme: %d", rec.Code)
	}
	// Both credentials at once are refused, on the API and elsewhere.
	for _, p := range []string{"/api/v1/me", "/me", "/"} {
		rec = a.do(http.MethodGet, p, nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }, a.owner(t))
		if rec.Code != 400 || deviceErrCode(t, rec) != "invalid" {
			t.Errorf("cookie and bearer on %s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestAPIBearerDelete(t *testing.T) {
	a := newTestApp(t)
	tok := deviceIssue(t, a)

	// The cookie path keeps CSRF and has no device to sign out.
	if rec := a.do(http.MethodDelete, apiTokenPath, nil, nil, a.owner(t)); rec.Code != 403 {
		t.Fatalf("cookie DELETE without a token: %d", rec.Code)
	}
	form := url.Values{"_csrf": {a.tokenFor(t, a.owner(t))}}
	if rec := a.do(http.MethodDelete, apiTokenPath, form, nil, a.owner(t)); rec.Code != 400 || deviceErrCode(t, rec) != "invalid" {
		t.Fatalf("cookie DELETE with a token: %d %s", rec.Code, rec.Body)
	}

	// A bearer request has no ambient credential, so it needs no CSRF token.
	rec := deviceBearerReq(a, http.MethodDelete, apiTokenPath, tok.Token, nil)
	if rec.Code != 204 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("bearer DELETE: %d %v", rec.Code, rec.Header())
	}
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok.Token, nil); rec.Code != 401 {
		t.Fatalf("after DELETE: %d", rec.Code)
	}
}

func TestAPICrossSite(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	rec := devicePost(a, "application/json", deviceBody(nil), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") })
	if rec.Code != 403 || deviceErrCode(t, rec) != "forbidden" {
		t.Fatalf("cross-site token POST: %d %s", rec.Code, rec.Body)
	}
	rec = devicePost(a, "application/json", deviceBody(nil), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") })
	if rec.Code != 201 {
		t.Fatalf("same-origin token POST: %d", rec.Code)
	}
}

func TestDeviceSignOutOwn(t *testing.T) {
	a := newTestApp(t)
	tok := deviceIssue(t, a)
	ck := a.owner(t)
	post := func(path string) *httptest.ResponseRecorder {
		return a.do(http.MethodPost, path, url.Values{"_csrf": {a.tokenFor(t, ck)}}, nil, ck)
	}
	if rec := post("/me/devices/dev_nothere/signout"); rec.Code != 404 {
		t.Fatalf("unknown id: %d", rec.Code)
	}
	if rec := post("/me/devices/%00/signout"); rec.Code != 404 {
		t.Fatalf("malformed id: %d", rec.Code)
	}
	// Another person's device is a 404, and keeps working.
	member := a.as(t, users.RoleMember)
	memberTok := deviceBearer(t, a, sweepUserID(t, a, member))
	var memberDev string
	_ = a.st.Reader().QueryRow(`SELECT id FROM devices WHERE user_id <> (SELECT id FROM users WHERE username = 'ana') LIMIT 1`).Scan(&memberDev)
	if rec := post("/me/devices/" + memberDev + "/signout"); rec.Code != 404 {
		t.Fatalf("another person's device: %d", rec.Code)
	}
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", memberTok, nil); rec.Code != 200 {
		t.Fatalf("member token after refused revoke: %d", rec.Code)
	}

	rec := post("/me/devices/" + tok.Device.ID + "/signout")
	if rec.Code != 303 || location(rec) != "/me/devices" {
		t.Fatalf("own device: %d %q", rec.Code, location(rec))
	}
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok.Token, nil); rec.Code != 401 {
		t.Fatalf("revoked token still works: %d", rec.Code)
	}
}

func TestDeviceDisabledUserRefused(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, member)
	tok := deviceBearer(t, a, memberID)
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok, nil); rec.Code != 200 {
		t.Fatalf("before: %d", rec.Code)
	}
	form := url.Values{"_csrf": {a.tokenFor(t, owner)}}
	if rec := a.do(http.MethodPost, "/admin/users/"+memberID+"/disable", form, nil, owner); rec.Code != 303 {
		t.Fatalf("disable: %d", rec.Code)
	}
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok, nil); rec.Code != 401 {
		t.Fatalf("disabled person's token: %d", rec.Code)
	}
}

func TestDeviceAdminSignOut(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, member)
	tok := deviceBearer(t, a, memberID)
	var devID string
	_ = a.st.Reader().QueryRow(`SELECT id FROM devices WHERE user_id = ?`, memberID).Scan(&devID)
	send := func(ck *http.Cookie, userID, did string) *httptest.ResponseRecorder {
		return a.do(http.MethodPost, "/admin/users/"+userID+"/devices/"+did+"/signout",
			url.Values{"_csrf": {a.tokenFor(t, ck)}}, nil, ck)
	}
	if rec := send(member, memberID, devID); rec.Code != 404 {
		t.Fatalf("member on self: %d", rec.Code)
	}
	if rec := send(owner, memberID, "dev_nothere"); rec.Code != 404 {
		t.Fatalf("unknown device: %d", rec.Code)
	}
	if rec := send(owner, memberID, "0123456789abcdef"); rec.Code != 404 {
		t.Fatalf("unknown session: %d", rec.Code)
	}
	if rec := send(owner, memberID, devID); rec.Code != 303 || location(rec) != "/admin/users#users-devices" {
		t.Fatalf("owner revokes member device: %d %q", rec.Code, location(rec))
	}
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok, nil); rec.Code != 401 {
		t.Fatalf("token after admin revoke: %d", rec.Code)
	}
	// A member's browser session ends by its public id.
	h, _ := auth.HashToken(member.Value)
	if rec := send(owner, memberID, auth.PublicID(h)); rec.Code != 303 {
		t.Fatalf("owner ends member session: %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/", nil, nil, member); rec.Code == 200 {
		t.Fatal("the member's session survived")
	}
}

// deviceLog collects slog output for a test.
type deviceLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *deviceLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func TestAPITokenNeverLogged(t *testing.T) {
	var logs deviceLog
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := newTestApp(t)
	tok := deviceIssue(t, a)
	deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok.Token, nil)
	deviceBearerReq(a, http.MethodGet, "/me", tok.Token, nil)
	deviceBearerReq(a, http.MethodDelete, apiTokenPath, tok.Token, nil)
	devicePost(a, "application/json", deviceBody(func(m map[string]string) { m["password"] = "wrong-secret-pw" }), nil)

	logs.mu.Lock()
	logged := logs.buf.String()
	logs.mu.Unlock()
	for _, secret := range []string{tok.Token, testPassword, "wrong-secret-pw"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("the log contains a secret: %q", secret)
		}
	}
	var n int
	if err := a.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail_json LIKE '%'||?||'%' OR target LIKE '%'||?||'%'`,
		tok.Token, tok.Token).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit rows holding the token: %d %v", n, err)
	}
	var issued int
	_ = a.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.device_issued'`).Scan(&issued)
	if issued != 1 {
		t.Fatalf("issue not audited: %d", issued)
	}
}
