package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

var akeyRE = regexp.MustCompile(`\b[a-z2-7]{24}\b`)

// akeyPost makes a key as a signed-in person.
func akeyPost(t *testing.T, l *opdsLab, who, name, kind string) (code int, body string, h http.Header) {
	t.Helper()
	ck := l.cookies[who]
	rec := l.a.do(http.MethodPost, "/me/app-keys", url.Values{"_csrf": {l.a.tokenFor(t, ck)}, "name": {name}, "kind": {kind}}, nil, ck)
	return rec.Code, rec.Body.String(), rec.Header()
}

// TestAppKeyShownOnce: the answer to the POST carries the key and the setup words, no-store; it is never
// stored, audited, or shown again, and it works until turned off.
func TestAppKeyShownOnce(t *testing.T) {
	l := newOPDSLab(t)
	code, body, h := akeyPost(t, l, "m_ok", "Kobo", "opds")
	if code != http.StatusOK || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("POST = %d, Cache-Control %q", code, h.Get("Cache-Control"))
	}
	key := akeyRE.FindString(body[strings.Index(body, "Your new key"):])
	if key == "" {
		t.Fatalf("no key in the answer:\n%s", body)
	}
	for _, want := range []string{"http://example.com/opds", l.username(t, "m_ok"), "only time it is shown"} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer lacks %q", want)
		}
	}
	h2 := auth.KeyHash(auth.KindOPDS, key)
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM devices WHERE token_hash = ? AND kind = 'opds' AND name = 'Kobo'`, h2[:]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hash stored: %d, %v", n, err)
	}
	var leak int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE detail_json LIKE '%'||?||'%' OR target LIKE '%'||?||'%'`, key, key).Scan(&leak); err != nil || leak != 0 {
		t.Errorf("the key is in the audit log: %d, %v", leak, err)
	}
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'key.create'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("key.create rows = %d, %v", n, err)
	}
	page := l.a.do(http.MethodGet, "/me/devices", nil, nil, l.cookies["m_ok"]).Body.String()
	if strings.Contains(page, key) || !strings.Contains(page, "Kobo") || !strings.Contains(page, "Turn off") {
		t.Error("the devices page shows the key again, or does not list the key")
	}
	if rec := l.get("/opds", l.username(t, "m_ok"), key, nil); rec.Code != http.StatusOK {
		t.Errorf("new key = %d", rec.Code)
	}
	// A koreader key is for /kosync, and the page says so.
	_, body, _ = akeyPost(t, l, "m_ok", "", "koreader")
	if !strings.Contains(body, "/kosync") {
		t.Error("the koreader answer lacks the sync address")
	}
	// The key made for KOReader does not open OPDS.
	ko := akeyRE.FindString(body[strings.Index(body, "Your new key"):])
	if rec := l.get("/opds", l.username(t, "m_ok"), ko, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("koreader key on opds = %d", rec.Code)
	}
}

// TestAppKeyRefusals: a bad kind, a missing session, a bearer token, no CSRF token and the limit.
func TestAppKeyRefusals(t *testing.T) {
	l := newOPDSLab(t)
	if code, _, _ := akeyPost(t, l, "m_ok", "x", "web"); code != http.StatusBadRequest {
		t.Errorf("kind web = %d, want 400", code)
	}
	if code, _, _ := akeyPost(t, l, "m_ok", "x", ""); code != http.StatusBadRequest {
		t.Errorf("no kind = %d, want 400", code)
	}
	if rec := l.a.do(http.MethodPost, "/me/app-keys", url.Values{"kind": {"opds"}}, nil); rec.Code == http.StatusOK {
		t.Error("anonymous POST made a key")
	}
	if rec := l.a.do(http.MethodPost, "/me/app-keys", url.Values{"kind": {"opds"}}, nil, l.cookies["m_ok"]); rec.Code == http.StatusOK {
		t.Error("a POST without the CSRF token made a key")
	}
	// A key (or any bearer) cannot make keys: it is not a page credential.
	rec := l.a.do(http.MethodPost, "/me/app-keys", url.Values{"kind": {"opds"}}, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+l.secrets["m_ok"])
	})
	if rec.Code == http.StatusOK {
		t.Error("a bearer token made a key")
	}
	// Up to the limit of live devices, then 409 in plain words.
	for i := 0; i < auth.MaxLiveDevices; i++ {
		if code, _, _ := akeyPost(t, l, "m_two", "k", "opds"); code != http.StatusOK && code != http.StatusConflict {
			t.Fatalf("key %d = %d", i, code)
		}
	}
	code, body, _ := akeyPost(t, l, "m_two", "k", "opds")
	if code != http.StatusConflict || !strings.Contains(body, "Turn one off first") {
		t.Errorf("past the limit = %d %q", code, body)
	}
}
