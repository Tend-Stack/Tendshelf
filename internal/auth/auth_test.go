package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const (
	testCode = "ABCDE-FGHJK"
	testPW   = "correct horse battery"
)

var cheap = users.HashParams{MemoryKiB: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

type env struct {
	st    *db.Store
	users *users.Repo
	sess  *Sessions
	svc   *Service
	clk   *clock
	owner users.User
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := &clock{t: time.UnixMilli(1_700_000_000_000)}
	ur := users.New(st, users.Options{Hash: cheap, Now: clk.Now})
	ss := NewSessions(st, clk.Now)
	return &env{st: st, users: ur, sess: ss, clk: clk,
		svc: NewService(st, ur, ss, ServiceOptions{SetupCode: testCode, Now: clk.Now})}
}

var ip1 = netip.MustParseAddr("203.0.113.7")

// withOwner runs setup so the env has an owner named "ana".
func (e *env) withOwner(t *testing.T) *env {
	t.Helper()
	l, err := e.svc.Setup(context.Background(), testCode,
		users.NewUser{Username: "ana", DisplayName: "Ana", Password: testPW}, ip1, "ua")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	e.owner = l.User
	return e
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTokenHashOnlyStored(t *testing.T) {
	e := newEnv(t).withOwner(t)
	var tok string
	if err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = e.sess.Create(context.Background(), tx, e.owner.ID, "ua")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token: %v len %d", err, len(raw))
	}
	sum := sha256.Sum256(raw)
	if n := e.count(t, `SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, sum[:]); n != 1 {
		t.Fatal("the SHA-256 of the token is not stored")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions WHERE token_hash = ? OR user_agent = ?`, raw, tok); n != 0 {
		t.Fatal("the raw token is stored")
	}
	if got, ok := HashToken(tok); !ok || got != sum {
		t.Fatal("HashToken disagrees with Create")
	}
}

func TestHashTokenRejectsBadInput(t *testing.T) {
	good, _ := NewToken()
	for name, tok := range map[string]string{
		"empty": "", "short": good[:40], "long": good + "A", "padded": good + "=",
		"std alphabet": strings.ReplaceAll(good, "-", "+") + "+", "space": " " + good[1:], "newline": good[:42] + "\n",
	} {
		if _, ok := HashToken(tok); ok {
			t.Errorf("%s accepted", name)
		}
	}
	if _, ok := HashToken(good); !ok {
		t.Fatal("good token rejected")
	}
	if a, _ := NewToken(); a == good {
		t.Fatal("tokens repeat")
	}
}

func TestSessionExpired(t *testing.T) {
	e := newEnv(t).withOwner(t)
	tok := e.login(t)
	if _, _, _, err := e.sess.Lookup(context.Background(), tok); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	e.clk.Add(SessionTTL + time.Millisecond)
	if _, _, _, err := e.sess.Lookup(context.Background(), tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired: %v", err)
	}
	h, _ := HashToken(tok)
	if n := e.count(t, `SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, h[:]); n != 0 {
		t.Fatal("expired row not deleted")
	}
}

// login signs the owner in on a fresh session and returns the token.
func (e *env) login(t *testing.T) string {
	t.Helper()
	l, err := e.svc.Login(context.Background(), "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return l.Token
}

func TestSessionSlidingRefresh(t *testing.T) {
	e := newEnv(t).withOwner(t)
	tok := e.login(t)
	h, _ := HashToken(tok)
	exp := func() (last, expires int64) {
		_ = e.st.Reader().QueryRow(`SELECT last_seen_at, expires_at FROM sessions WHERE token_hash = ?`, h[:]).Scan(&last, &expires)
		return
	}
	last0, exp0 := exp()

	e.clk.Add(59 * time.Minute)
	_, _, refreshed, err := e.sess.Lookup(context.Background(), tok)
	if err != nil || refreshed {
		t.Fatalf("+59m: refreshed=%v err=%v", refreshed, err)
	}
	if l, x := exp(); l != last0 || x != exp0 {
		t.Fatal("a write happened inside the hour")
	}

	e.clk.Add(2 * time.Minute) // +61 min
	_, u, refreshed, err := e.sess.Lookup(context.Background(), tok)
	if err != nil || !refreshed {
		t.Fatalf("+61m: refreshed=%v err=%v", refreshed, err)
	}
	now := e.clk.Now().UnixMilli()
	l, x := exp()
	if l != now || x != now+SessionTTL.Milliseconds() {
		t.Fatalf("last=%d expires=%d, now=%d", l, x, now)
	}
	if u.LastSeenAt != now {
		t.Fatalf("user last seen %d", u.LastSeenAt)
	}
	var ul int64
	_ = e.st.Reader().QueryRow(`SELECT last_seen_at FROM users WHERE id = ?`, e.owner.ID).Scan(&ul)
	if ul != now {
		t.Fatalf("users.last_seen_at = %d", ul)
	}
}

func TestSessionDisabledUser(t *testing.T) {
	e := newEnv(t).withOwner(t)
	tok := e.login(t)
	if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.sess.Lookup(context.Background(), tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestLoginRotatesSession(t *testing.T) {
	e := newEnv(t).withOwner(t)
	first := e.login(t)
	_, _, _, _ = e.sess.Lookup(context.Background(), first)
	h, _ := HashToken(first)
	prev := Session{Hash: h, UserID: e.owner.ID}
	l, err := e.svc.Login(context.Background(), "ANA", testPW, ip1, "ua", &prev)
	if err != nil {
		t.Fatal(err)
	}
	if l.Token == first {
		t.Fatal("token reused")
	}
	if _, _, _, err := e.sess.Lookup(context.Background(), first); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old token still valid: %v", err)
	}
	if _, _, _, err := e.sess.Lookup(context.Background(), l.Token); err != nil {
		t.Fatalf("new token: %v", err)
	}
	// Expired sessions are swept at sign-in.
	e.clk.Add(SessionTTL + time.Hour)
	if _, err := e.svc.Login(context.Background(), "ana", testPW, ip1, "ua", nil); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions after sweep = %d", n)
	}
}

func TestLoginSameErrorForAllFailures(t *testing.T) {
	e := newEnv(t).withOwner(t)
	if _, err := e.st.Writer().Exec(`INSERT INTO users (id, username, display_name, role, password_hash, disabled, created_at)
		VALUES ('u2','dis','Dis','member', ?, 1, 1)`, e.hashOf(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`INSERT INTO users (id, username, display_name, role, disabled, created_at)
		VALUES ('u3','nopw','No','member',0,1)`); err != nil {
		t.Fatal(err)
	}
	before := e.count(t, `SELECT COUNT(*) FROM sessions`)
	for name, c := range map[string][2]string{
		"unknown":      {"ghost", testPW},
		"wrong":        {"ana", "wrong password!"},
		"disabled":     {"dis", testPW},
		"no password":  {"nopw", testPW},
		"empty":        {"", ""},
		"huge":         {"ana", strings.Repeat("x", 5000)},
		"long name":    {strings.Repeat("a", 100), testPW},
		"whitespace":   {"  ", testPW},
		"sql-ish name": {"ana' OR '1'='1", testPW},
	} {
		l, err := e.svc.Login(context.Background(), c[0], c[1], netip.MustParseAddr("198.51.100.9"), "ua", nil)
		if !errors.Is(err, ErrBadCredentials) || err.Error() != ErrBadCredentials.Error() || l.Token != "" {
			t.Errorf("%s: %+v %v", name, l, err)
		}
	}
	var detail string
	_ = e.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'auth.login_failed' ORDER BY id LIMIT 1`).Scan(&detail)
	if !strings.Contains(detail, "198.51.100.9") {
		t.Fatalf("audit detail %q", detail)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions`); n != before {
		t.Fatalf("sessions after failures = %d, before %d", n, before)
	}
}

func (e *env) hashOf(t *testing.T) string {
	t.Helper()
	h, err := e.users.HashPassword(context.Background(), testPW)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestLoginRateLimitPerIP(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ip := netip.MustParseAddr("198.51.100.20")
	for i := 0; i < ipMaxFails; i++ {
		// Different names: the per-user limit must not be what stops us.
		if _, err := e.svc.Login(context.Background(), "ghost"+string(rune('a'+i)), "x", ip, "ua", nil); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	before := e.count(t, `SELECT COUNT(*) FROM sessions`)
	_, err := e.svc.Login(context.Background(), "ana", testPW, ip, "ua", nil)
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > 15*time.Minute {
		t.Fatalf("21st try with the right password: %v", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions`); n != before {
		t.Fatal("a session was created while limited")
	}
	// Another address is unaffected; so is the same /32 after the window.
	if _, err := e.svc.Login(context.Background(), "ana", testPW, netip.MustParseAddr("198.51.100.21"), "ua", nil); err != nil {
		t.Fatalf("other ip: %v", err)
	}
	e.clk.Add(15 * time.Minute)
	if _, err := e.svc.Login(context.Background(), "ana", testPW, ip, "ua", nil); err != nil {
		t.Fatalf("after window: %v", err)
	}
	// Setup shares the address limit.
	for i := 0; i < ipMaxFails; i++ {
		_, _ = e.svc.Setup(context.Background(), "WRONG-CODE0", users.NewUser{}, ip, "ua")
	}
	if _, err := e.svc.Login(context.Background(), "ana", testPW, ip, "ua", nil); !errors.As(err, &rl) {
		t.Fatalf("setup failures should limit login: %v", err)
	}
}

func TestLoginRateLimitPerUser(t *testing.T) {
	e := newEnv(t).withOwner(t)
	for i := 0; i < userMaxFails; i++ {
		ip := netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", 1+i))
		if _, err := e.svc.Login(context.Background(), "ana", "wrong password!", ip, "ua", nil); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	var rl *RateLimitedError
	_, err := e.svc.Login(context.Background(), "ana", testPW, netip.MustParseAddr("192.0.2.200"), "ua", nil)
	if !errors.As(err, &rl) {
		t.Fatalf("11th from a new address: %v", err)
	}
	// An unknown name from the same new address is a plain failure, not limited:
	// the limit must not reveal that "ana" exists... it is the user key that limits.
	if _, err := e.svc.Login(context.Background(), "ghost", "x", netip.MustParseAddr("192.0.2.201"), "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestLoginSuccessResetsUserLimit(t *testing.T) {
	e := newEnv(t).withOwner(t)
	for i := 0; i < userMaxFails-1; i++ {
		ip := netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", 1+i))
		_, _ = e.svc.Login(context.Background(), "ana", "wrong password!", ip, "ua", nil)
	}
	if _, err := e.svc.Login(context.Background(), "ana", testPW, netip.MustParseAddr("192.0.2.100"), "ua", nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < userMaxFails; i++ {
		ip := netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", 100+i))
		if _, err := e.svc.Login(context.Background(), "ana", "wrong password!", ip, "ua", nil); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("after reset, try %d: %v", i, err)
		}
	}
}

func TestLimiterBounded(t *testing.T) {
	clk := &clock{t: time.Unix(1000, 0)}
	l := NewLimiter(2, time.Minute, 8, clk.Now)
	for i := 0; i < 100; i++ {
		l.Fail(string(rune('a'+i%26)) + string(rune('a'+i/26)))
		clk.Add(time.Second)
		if l.Len() > 8 {
			t.Fatalf("len %d at %d", l.Len(), i)
		}
	}
	// The oldest key was dropped, the newest kept.
	l.Fail("newest")
	l.Fail("newest")
	if ok, _ := l.Allow("newest"); ok {
		t.Fatal("newest should be limited")
	}
	l.Reset("newest")
	if ok, _ := l.Allow("newest"); !ok {
		t.Fatal("reset failed")
	}
	// Expired windows are dropped before live ones.
	l2 := NewLimiter(1, time.Minute, 2, clk.Now)
	l2.Fail("old")
	clk.Add(30 * time.Second)
	l2.Fail("mid")
	clk.Add(40 * time.Second) // "old" expired, "mid" live
	l2.Fail("new")
	if ok, _ := l2.Allow("mid"); ok {
		t.Fatal("live window evicted instead of the expired one")
	}
	if l2.Len() != 2 {
		t.Fatalf("len %d", l2.Len())
	}
}

func TestIPKeyIPv6Prefix(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":                "203.0.113.7",
		"::ffff:203.0.113.7":         "203.0.113.7",
		"2001:db8:1:2:aaaa:bbbb:1:2": "2001:db8:1:2::/64",
		"2001:db8:1:2:cccc:dddd:3:4": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":            "2001:db8:1:3::/64",
		"fe80::1%eth0":               "fe80::/64",
	} {
		if got := IPKey(netip.MustParseAddr(in)); got != want {
			t.Errorf("IPKey(%s) = %s, want %s", in, got, want)
		}
	}
	if IPKey(netip.Addr{}) == "" {
		t.Error("zero addr has no key")
	}
}

func TestClientIPTrustedProxy(t *testing.T) {
	px, err := ParseProxies("10.0.0.0/8, 192.168.1.5, ::1")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct client ignores header", "203.0.113.7:5555", []string{"1.1.1.1"}, "203.0.113.7"},
		{"trusted proxy, one hop", "10.1.2.3:80", []string{"198.51.100.4"}, "198.51.100.4"},
		{"spoofed left entry ignored", "10.1.2.3:80", []string{"6.6.6.6, 198.51.100.4"}, "198.51.100.4"},
		{"two proxies", "10.1.2.3:80", []string{"198.51.100.4, 10.9.9.9"}, "198.51.100.4"},
		{"two header lines", "10.1.2.3:80", []string{"6.6.6.6", "198.51.100.4"}, "198.51.100.4"},
		{"exact ip proxy", "192.168.1.5:1", []string{"198.51.100.4"}, "198.51.100.4"},
		{"other lan host not trusted", "192.168.1.6:1", []string{"198.51.100.4"}, "192.168.1.6"},
		{"malformed entry", "10.1.2.3:80", []string{"198.51.100.4, not-an-ip"}, "10.1.2.3"},
		{"no header", "10.1.2.3:80", nil, "10.1.2.3"},
		{"all trusted", "10.1.2.3:80", []string{"10.5.5.5, 10.6.6.6"}, "10.5.5.5"},
		{"ipv6 loopback proxy", "[::1]:80", []string{"198.51.100.4"}, "198.51.100.4"},
		{"mapped client", "10.1.2.3:80", []string{"::ffff:198.51.100.4"}, "198.51.100.4"},
		{"bad remote", "garbage", []string{"1.1.1.1"}, ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for _, h := range c.xff {
			r.Header.Add("X-Forwarded-For", h)
		}
		got := px.ClientIP(r)
		if c.want == "" {
			if got.IsValid() {
				t.Errorf("%s: %v", c.name, got)
			}
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s: got %v want %s", c.name, got, c.want)
		}
	}
	var none Proxies
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:80"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := none.ClientIP(r); got.String() != "10.1.2.3" {
		t.Errorf("no proxies configured must trust no header, got %v", got)
	}
	for _, bad := range []string{"nope", "10.0.0.0/33", "1.2.3"} {
		if _, err := ParseProxies(bad); err == nil {
			t.Errorf("ParseProxies(%q) accepted", bad)
		}
	}
	if p, err := ParseProxies(""); err != nil || len(p.p) != 0 {
		t.Errorf("empty: %v %v", p, err)
	}
}

func TestProxiesSecure(t *testing.T) {
	px, _ := ParseProxies("10.0.0.1")
	mk := func(remote, proto string, secure bool) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		if secure {
			r.TLS = &tls.ConnectionState{}
		}
		return r
	}
	for name, c := range map[string]struct {
		r    *http.Request
		want bool
	}{
		"plain":                  {mk("203.0.113.1:1", "", false), false},
		"direct tls":             {mk("203.0.113.1:1", "", true), true},
		"untrusted claims https": {mk("203.0.113.1:1", "https", false), false},
		"trusted https":          {mk("10.0.0.1:1", "https", false), true},
		"trusted HTTPS list":     {mk("10.0.0.1:1", "HTTPS, http", false), true},
		"trusted http":           {mk("10.0.0.1:1", "http", false), false},
		"trusted none":           {mk("10.0.0.1:1", "", false), false},
	} {
		if got := px.Secure(c.r); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestSafeNext(t *testing.T) {
	same := []string{"/", "/libraries/x?y=1", "/books/bk_1#frag", "/search?q=a%20b", "/a/b/", "/loginx", "/setupx", "/logout2"}
	for _, s := range same {
		if got := SafeNext(s); got != s {
			t.Errorf("SafeNext(%q) = %q", s, got)
		}
	}
	bad := []string{
		"//evil.com", `/\evil.com`, "https://evil.com", "http:/x", "javascript:alert(1)", "/login", "/login?next=/", "/logout?x",
		"/setup", "/setup/server", "/a%0d%0aSet-Cookie:x", "/\x00", "", strings.Repeat("/a", 1025), "/%5Cevil.com", "/%2F/evil.com",
		"evil.com", "/a\nb", "/a\x7f", "/ok\tnot", "///x", "/%2f%2fevil", "/a b\r",
	}
	for _, s := range bad {
		if got := SafeNext(s); got != "/" {
			t.Errorf("SafeNext(%q) = %q, want /", s, got)
		}
	}
	if got := SafeNext("/" + strings.Repeat("a", 2047)); got != "/"+strings.Repeat("a", 2047) {
		t.Error("2048 bytes should pass")
	}
}

func TestSetupCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c := NewSetupCode()
		if len(c) != 11 || c[5] != '-' || strings.Trim(c[:5]+c[6:], "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" {
			t.Fatalf("shape: %q", c)
		}
		if seen[c] {
			t.Fatal("code repeated")
		}
		seen[c] = true
	}
	if !SetupCodeOK("ABCDE-FGHJK", "abcde-fghjk") || !SetupCodeOK("ABCDE-FGHJK", " ABCDEFGHJK ") ||
		!SetupCodeOK("ABCDE-FGHJK", "ABCDE FGHJK") {
		t.Fatal("tolerant forms rejected")
	}
	for _, got := range []string{"", "ABCDE-FGHJ", "ABCDE-FGHJKK", "ABCDE-FGHJL", "x"} {
		if SetupCodeOK("ABCDE-FGHJK", got) {
			t.Errorf("%q accepted", got)
		}
	}
	if SetupCodeOK("", "") || SetupCodeOK("", "anything") || SetupCodeOK(" - ", "") {
		t.Fatal("an empty real code must never match")
	}
}

func TestAuditActions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, _ = e.svc.Setup(ctx, "WRONG-CODE0", users.NewUser{}, ip1, "ua")
	e.withOwner(t)
	_, _ = e.svc.Login(ctx, "ana", "wrong password!", ip1, "ua", nil)
	l, err := e.svc.Login(ctx, "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := HashToken(l.Token)
	if err := e.svc.Logout(ctx, Session{Hash: h, UserID: l.User.ID}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"setup.code_failed", "setup.owner_created", "auth.login_failed", "auth.login", "auth.logout"} {
		if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = ?`, a) < 1 {
			t.Errorf("no audit row for %s", a)
		}
	}
	var uidNull int
	_ = e.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'setup.code_failed' AND user_id IS NULL`).Scan(&uidNull)
	if uidNull != 1 {
		t.Fatal("code_failed row has a user")
	}
	var all string
	_ = e.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&all)
	for _, secret := range []string{testPW, "wrong password!", testCode, l.Token, "WRONG-CODE0"} {
		if strings.Contains(all, secret) {
			t.Fatalf("audit log contains a secret %q", secret)
		}
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions after logout = %d (setup session should remain)", n)
	}
}

func TestSetupErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.svc.Setup(ctx, testCode, users.NewUser{Username: "x", DisplayName: "", Password: "short"}, ip1, "ua")
	var fe users.FieldErrors
	if !errors.As(err, &fe) || fe["username"] == "" || fe["password"] != "too_short" {
		t.Fatalf("validation: %v", err)
	}
	if ok, _ := e.svc.OwnerExists(ctx); ok {
		t.Fatal("owner after invalid setup")
	}
	e.withOwner(t)
	if ok, _ := e.svc.OwnerExists(ctx); !ok {
		t.Fatal("no owner after setup")
	}
	_, err = e.svc.Setup(ctx, testCode, users.NewUser{Username: "ben", DisplayName: "Ben", Password: testPW}, ip1, "ua")
	if !errors.Is(err, users.ErrOwnerExists) {
		t.Fatalf("second setup: %v", err)
	}
	if _, err := NewService(e.st, e.users, e.sess, ServiceOptions{}).Setup(ctx, "", users.NewUser{}, ip1, ""); !errors.Is(err, ErrSetupCode) {
		t.Fatalf("empty configured code: %v", err)
	}
}

func TestMiddlewareContextAndCookies(t *testing.T) {
	e := newEnv(t).withOwner(t)
	tok := e.login(t)
	var got users.User
	var seen bool
	h := Middleware(e.sess, Proxies{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, seen = UserFrom(r.Context())
		if _, ok := SessionFrom(r.Context()); ok != seen {
			t.Error("session and user disagree")
		}
	}))
	do := func(cookie string) *httptest.ResponseRecorder {
		seen = false
		r := httptest.NewRequest("GET", "/", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do(tok); !seen || got.Username != "ana" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("valid: seen=%v cookies=%v", seen, w.Result().Cookies())
	}
	if w := do(""); seen || len(w.Result().Cookies()) != 0 {
		t.Fatal("no cookie: nothing to do")
	}
	w := do("bogus")
	cs := w.Result().Cookies()
	if seen || len(cs) != 1 || cs[0].MaxAge != -1 || cs[0].Name != CookieName {
		t.Fatalf("invalid cookie not cleared: %v", cs)
	}
	e.clk.Add(2 * time.Hour)
	w = do(tok)
	cs = w.Result().Cookies()
	if !seen || len(cs) != 1 || cs[0].Value != tok || cs[0].MaxAge != 2592000 || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode || cs[0].Path != "/" {
		t.Fatalf("refresh cookie: %+v", cs)
	}
	if cs[0].Secure {
		t.Fatal("Secure on plain http")
	}
	if !bytes.Equal([]byte(cs[0].Value), []byte(tok)) {
		t.Fatal("token changed on refresh")
	}
}
