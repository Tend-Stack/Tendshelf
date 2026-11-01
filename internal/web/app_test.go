package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const (
	testSetupCode = "ABCDE-FGHJK"
	testPassword  = "correct horse battery"
)

// cheapHash keeps the tests fast; one test checks the real defaults in internal/users.
var cheapHash = users.HashParams{MemoryKiB: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

// testApp is the whole handler on a throwaway database.
type testApp struct {
	h  http.Handler
	st *db.Store

	once    sync.Once
	session *http.Cookie
}

// newTestApp builds the handler on a fresh database. mods change the options first.
func newTestApp(t *testing.T, mods ...func(*Options)) *testApp {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	o := Options{Version: "test", Store: st, SetupCode: testSetupCode, Hash: cheapHash}
	for _, m := range mods {
		m(&o)
	}
	h, err := NewHandler(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return &testApp{h: h, st: st}
}

var (
	hiddenToken = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	headerToken = regexp.MustCompile(`X-CSRF-Token&#34;:&#34;([^&]+)&#34;`)
)

// pageToken pulls the CSRF token out of a page: the hidden field of a form, or the hx-headers of
// the app shell.
func pageToken(t *testing.T, body string) string {
	t.Helper()
	if m := hiddenToken.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	if m := headerToken.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	t.Fatalf("no CSRF token in the page")
	return ""
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (a *testApp) do(method, path string, form url.Values, mod func(*http.Request), cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

// anon does a GET as an anonymous visitor and returns the ts_csrf cookie and the page's token.
func (a *testApp) anon(t *testing.T, path string) (*http.Cookie, string) {
	t.Helper()
	rec := a.do(http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	c := cookieNamed(rec, csrfCookie)
	if c == nil {
		t.Fatalf("GET %s set no ts_csrf cookie", path)
	}
	return c, pageToken(t, rec.Body.String())
}

func setupForm(token string) url.Values {
	return url.Values{
		"_csrf": {token}, "code": {testSetupCode}, "display_name": {"Ana Reyes"},
		"email": {"ana@example.org"}, "username": {"ana"}, "password": {testPassword},
	}
}

// owner runs first-run setup once and returns the owner's session cookie.
func (a *testApp) owner(t *testing.T) *http.Cookie {
	t.Helper()
	a.once.Do(func() {
		c, tok := a.anon(t, "/setup")
		rec := a.do(http.MethodPost, "/setup", setupForm(tok), nil, c)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
		}
		a.session = cookieNamed(rec, "ts_session")
		if a.session == nil {
			t.Fatal("setup set no session cookie")
		}
	})
	return a.session
}

// signedIn returns a handler that adds the owner's session cookie to every request.
func (a *testApp) signedIn(t *testing.T) http.Handler {
	t.Helper()
	ck := a.owner(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
		a.h.ServeHTTP(w, r)
	})
}

// tokenFor returns the CSRF token the app shell shows to the browser holding these cookies.
func (a *testApp) tokenFor(t *testing.T, cookies ...*http.Cookie) string {
	t.Helper()
	rec := a.do(http.MethodGet, "/", nil, nil, cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: %d", rec.Code)
	}
	return pageToken(t, rec.Body.String())
}
