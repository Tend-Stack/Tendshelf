package desktopshell

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// fakeStarter records what the page asked for.
type fakeStarter struct {
	mu       sync.Mutex
	personal int
	origins  []string
	state    State
	err      error
}

func (f *fakeStarter) StartPersonal() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.personal++
	return f.err
}

func (f *fakeStarter) Connect(origin string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.origins = append(f.origins, origin)
	return f.err
}

func (f *fakeStarter) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func newStart(t *testing.T, st Starter) (http.Handler, string) {
	t.Helper()
	assets, err := static.Load()
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Starter: st, Assets: assets})
	_, body := do(t, h, "GET", "/", nil)
	return h, tokenIn(t, body)
}

func do(t *testing.T, h http.Handler, method, path string, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, rec.Body.String()
}

var tokenRe = regexp.MustCompile(`name="_token" value="([0-9a-f]{64})"`)

func tokenIn(t *testing.T, body string) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no form token in the page: %.300s", body)
	}
	return m[1]
}

// healthy is a Tendshelf server the check accepts.
func healthy(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","version":"x","app":"tendshelf","shell":"1"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestDesktopStartForms(t *testing.T) {
	st := &fakeStarter{}
	h, token := newStart(t, st)

	rec, body := do(t, h, "GET", "/", nil)
	for _, want := range []string{
		"How do you want to use Tendshelf?", "Just on this computer", "Your books stay here.",
		"Connect to a server", "Server address", `data-shell="desktop"`,
		`action="/start/personal"`, `action="/start/connect"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("picker lacks %q", want)
		}
	}
	for _, banned := range []string{"<script>", "manifest", "offline.js", "style=\""} {
		if strings.Contains(body, banned) {
			t.Errorf("picker contains %q", banned)
		}
	}
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("GET /: %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if _, body2 := do(t, h, "GET", "/?change=1", nil); !strings.Contains(body2, "Just on this computer") {
		t.Error("?change=1 must show the picker")
	}

	rec, body = do(t, h, "POST", "/start/personal", url.Values{"_token": {token}})
	if rec.Code != 200 || st.personal != 1 || !strings.Contains(body, "Opening your library…") {
		t.Errorf("personal: %d, %d calls, busy words: %v", rec.Code, st.personal, strings.Contains(body, "Opening your library…"))
	}

	srv := healthy(t)
	rec, _ = do(t, h, "POST", "/start/connect", url.Values{"_token": {token}, "server": {strings.TrimPrefix(srv, "http://")}})
	// No scheme means https, and the test server speaks plain http: the check says so in words.
	if rec.Code != http.StatusUnprocessableEntity || len(st.origins) != 0 {
		t.Errorf("https against an http server: %d, connected %v", rec.Code, st.origins)
	}
	rec, body = do(t, h, "POST", "/start/connect", url.Values{"_token": {token}, "server": {srv + "/"}})
	if rec.Code != 200 || len(st.origins) != 1 || st.origins[0] != srv {
		t.Errorf("connect: %d, origins %v, want [%s]", rec.Code, st.origins, srv)
	}
	if !strings.Contains(body, "Opening your library…") {
		t.Error("a successful connect shows the busy page")
	}

	// A bad address and a refused server show words and call nothing.
	rec, body = do(t, h, "POST", "/start/connect", url.Values{"_token": {token}, "server": {"https://x.example.com/path"}})
	if rec.Code != 422 || !strings.Contains(body, "Enter the address you use in your browser, like https://books.example.com.") {
		t.Errorf("bad address: %d", rec.Code)
	}
	if !strings.Contains(body, `value="https://x.example.com/path"`) {
		t.Error("the typed address must stay in the field")
	}
	if len(st.origins) != 1 {
		t.Errorf("a refused address reached the Starter: %v", st.origins)
	}
}

func TestDesktopStartPlainHTTP(t *testing.T) {
	st := &fakeStarter{}
	h, token := newStart(t, st)
	// A public name over plain http: ask again before going on. The host is never reached.
	rec, body := do(t, h, "POST", "/start/connect", url.Values{"_token": {token}, "server": {"http://books.example.com"}})
	if rec.Code != 200 || !strings.Contains(body, "Your password would travel unencrypted.") || !strings.Contains(body, "Connect anyway") {
		t.Fatalf("plain http warning missing: %d", rec.Code)
	}
	if len(st.origins) != 0 {
		t.Fatal("connected without the second press")
	}
	// A private host needs no warning.
	_, body = do(t, h, "POST", "/start/connect", url.Values{"_token": {token}, "server": {healthy(t)}})
	if strings.Contains(body, "unencrypted") {
		t.Error("warned about a loopback server")
	}
}

func TestDesktopStartBadToken(t *testing.T) {
	st := &fakeStarter{}
	h, token := newStart(t, st)
	for _, path := range []string{"/start/personal", "/start/connect"} {
		for name, form := range map[string]url.Values{
			"none":  {"server": {healthy(t)}},
			"wrong": {"_token": {strings.Repeat("0", 64)}, "server": {healthy(t)}},
			"short": {"_token": {token[:10]}, "server": {healthy(t)}},
		} {
			if rec, _ := do(t, h, "POST", path, form); rec.Code != http.StatusForbidden {
				t.Errorf("POST %s with %s token: %d, want 403", path, name, rec.Code)
			}
		}
	}
	if st.personal != 0 || len(st.origins) != 0 {
		t.Errorf("a bad token reached the Starter: %d, %v", st.personal, st.origins)
	}
	// Another process gets another token.
	_, other := newStart(t, &fakeStarter{})
	if other == token {
		t.Error("two handlers share a token")
	}
}

func TestDesktopStartFailedWords(t *testing.T) {
	st := &fakeStarter{state: State{Kind: Failed, Words: "The library folder is full."}}
	h, token := newStart(t, st)
	if _, body := do(t, h, "GET", "/", nil); !strings.Contains(body, "The library folder is full.") {
		t.Error("a failed state must show its words")
	}
	st.state = State{Kind: Busy}
	if _, body := do(t, h, "GET", "/", nil); !strings.Contains(body, "Opening your library…") || strings.Contains(body, "Connect to a server") {
		t.Error("a busy state shows only the busy page")
	}
	// A busy window ignores a second press.
	if rec, _ := do(t, h, "POST", "/start/personal", url.Values{"_token": {token}}); rec.Code != 200 || st.personal != 0 {
		t.Errorf("busy personal: %d, %d calls", rec.Code, st.personal)
	}
	// A Starter error shows plain words, not its text.
	st.state, st.err = State{}, io.ErrUnexpectedEOF
	rec, body := do(t, h, "POST", "/start/personal", url.Values{"_token": {token}})
	if rec.Code != http.StatusBadGateway || !strings.Contains(body, "Something went wrong. Please try again.") || strings.Contains(body, "unexpected EOF") {
		t.Errorf("starter error: %d, %.200s", rec.Code, body)
	}
}

func TestDesktopStartCSP(t *testing.T) {
	h, _ := newStart(t, &fakeStarter{})
	rec, body := do(t, h, "GET", "/", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "style-src 'self'") || !strings.Contains(csp, "form-action 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
	// The stylesheet the page links is served, with the same headers.
	m := regexp.MustCompile(`href="(/static/[^"]*pages-desktop[^"]*\.css)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("the page does not link pages-desktop.css")
	}
	css, _ := do(t, h, "GET", m[1], nil)
	if css.Code != 200 || css.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("stylesheet: %d, CSP %q", css.Code, css.Header().Get("Content-Security-Policy"))
	}
	// Other paths and methods are not pages.
	for _, c := range [][2]string{{"GET", "/login"}, {"GET", "/healthz"}, {"POST", "/"}, {"GET", "/start/personal"}} {
		if rec, _ := do(t, h, c[0], c[1], nil); rec.Code != 404 && rec.Code != 405 {
			t.Errorf("%s %s: %d, want 404 or 405", c[0], c[1], rec.Code)
		}
	}
}
