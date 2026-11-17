package online

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// customEnv is a Client whose guarded client resolves names from a table and dials one test server
// whatever address it was given, so no test touches a real network.
type customEnv struct {
	c      *Client
	srv    *httptest.Server
	dialed []string
	mu     sync.Mutex
	names  map[string][]netip.Addr
	cache  *memCache
}

func newCustomEnv(t *testing.T, srv *httptest.Server) *customEnv {
	t.Helper()
	e := &customEnv{srv: srv, names: map[string][]netip.Addr{}}
	clock := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	e.cache = &memCache{now: func() time.Time { return clock }}
	o := Options{
		Cache:     e.cache,
		CoverDir:  t.TempDir(),
		Now:       func() time.Time { return clock },
		Sleep:     func(context.Context, time.Duration) error { return nil },
		Transport: &fakeNet{},
		CustomLookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if a, ok := e.names[host]; ok {
				return a, nil
			}
			return nil, errors.New("no such host")
		},
		CustomDial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			e.mu.Lock()
			e.dialed = append(e.dialed, addr)
			e.mu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}
	if srv.TLS != nil {
		pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
		o.CustomRootCAs = pool
	}
	e.c = New(o)
	return e
}

func (e *customEnv) name(host string, addrs ...string) {
	for _, a := range addrs {
		e.names[host] = append(e.names[host], netip.MustParseAddr(a))
	}
}

func (e *customEnv) dialCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.dialed)
}

func (e *customEnv) set(base, auth string) {
	e.c.SetCustom([]CustomProvider{{ID: "cp_1", Name: "My shelf", BaseURL: base, Auth: auth}})
}

func code(err error) string {
	var oe *Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return fmt.Sprint(err)
}

const oneMatch = `{"matches":[{"title":"The Hobbit","author":"J. R. R. Tolkien"}]}`

func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}
}

func TestParseCustomSearch(t *testing.T) {
	long := strings.Repeat("é", 600)
	desc := strings.Repeat("d", 20100)
	many := make([]string, 30)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprintf("g%d", i))
	}
	tests := []struct {
		name  string
		in    string
		check func(t *testing.T, b []Book)
		bad   bool
	}{
		{name: "not an object", in: `[]`, bad: true},
		{name: "no matches", in: `{"x":1}`, bad: true},
		{name: "matches not a list", in: `{"matches":{}}`, bad: true},
		{name: "not json", in: `nope`, bad: true},
		{name: "skip without title", in: `{"matches":[{"author":"A"},{"title":" "},{"title":"Kept"}]}`, check: func(t *testing.T, b []Book) {
			if len(b) != 1 || b[0].Title != "Kept" {
				t.Errorf("books = %+v", b)
			}
		}},
		{name: "authors and narrators split", in: `{"matches":[{"title":"T","author":"A One, B Two ,","narrator":"N1,N2"}]}`, check: func(t *testing.T, b []Book) {
			if len(b[0].Authors) != 2 || b[0].Authors[1].Name != "B Two" || len(b[0].Narrators) != 2 || b[0].Narrators[0] != "N1" {
				t.Errorf("book = %+v", b[0])
			}
		}},
		{name: "year, series, language, key", in: `{"matches":[{"title":"T","publishedYear":"2019","series":[{"series":"Saga","sequence":"2"},{"series":"Other","sequence":"9"}],"language":"english"}]}`, check: func(t *testing.T, b []Book) {
			x := b[0]
			if x.Year != 2019 || x.SeriesName != "Saga" || x.Sequence != "2" || x.Language != "English" || x.Provider != "custom:cp_1" || x.ProviderLabel != "Shelf" {
				t.Errorf("book = %+v", x)
			}
		}},
		{name: "numeric year", in: `{"matches":[{"title":"T","publishedYear":2020}]}`, check: func(t *testing.T, b []Book) {
			if b[0].Year != 2020 {
				t.Errorf("year = %d", b[0].Year)
			}
		}},
		{name: "ids", in: `{"matches":[{"title":"A","isbn":"978-0-261-10221-7","asin":"b0abcdefgh"},{"title":"B","isbn":"9780261102217"},{"title":"C","isbn":"bad","asin":"short"}]}`, check: func(t *testing.T, b []Book) {
			if b[0].ID != "B0ABCDEFGH" || b[0].ISBN != "9780261102217" || b[1].ID != "9780261102217" || b[2].ID != "" || b[2].ISBN != "" || b[2].ASIN != "" {
				t.Errorf("books = %+v", b)
			}
		}},
		{name: "genres deduped and capped", in: `{"matches":[{"title":"T","genres":["Fantasy","Fantasy",3,` + strings.Join(many, ",") + `]}]}`, check: func(t *testing.T, b []Book) {
			if len(b[0].Genres) != 20 || b[0].Genres[0] != "Fantasy" || b[0].Genres[1] != "g0" {
				t.Errorf("genres = %v", b[0].Genres)
			}
		}},
		{name: "covers", in: `{"matches":[{"title":"A","cover":"https://x.example/c.jpg"},{"title":"B","cover":"http://127.0.0.1:3001/c.jpg"},{"title":"C","cover":"http://other.example/c.jpg"},{"title":"D","cover":"ftp://x/c"}]}`, check: func(t *testing.T, b []Book) {
			want := []string{"https://x.example/c.jpg", "http://127.0.0.1:3001/c.jpg", "", ""}
			for i, w := range want {
				if b[i].CoverURL != w {
					t.Errorf("cover %d = %q, want %q", i, b[i].CoverURL, w)
				}
			}
		}},
		{name: "cuts", in: `{"matches":[{"title":"` + long + `","subtitle":"` + long + `","publisher":"` + long + `","description":"` + desc + `","series":[{"series":"` + long + `"}]}]}`, check: func(t *testing.T, b []Book) {
			x := b[0]
			if len([]rune(x.Title)) != 500 || len([]rune(x.Subtitle)) != 500 || len([]rune(x.Publisher)) != 500 || len([]rune(x.SeriesName)) != 500 || len([]rune(x.Description)) != 20000 {
				t.Errorf("lengths = %d %d %d %d %d", len([]rune(x.Title)), len([]rune(x.Subtitle)), len([]rune(x.Publisher)), len([]rune(x.SeriesName)), len([]rune(x.Description)))
			}
		}},
		{name: "tags and duration ignored, five at most", in: `{"matches":[{"title":"1","tags":"x","duration":10},{"title":"2"},{"title":"3"},{"title":"4"},{"title":"5"},{"title":"6"}]}`, check: func(t *testing.T, b []Book) {
			if len(b) != 5 {
				t.Errorf("books = %d, want 5", len(b))
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := parseCustomSearch(json.RawMessage(tc.in), "custom:cp_1", "Shelf", "http://127.0.0.1:3001")
			if tc.bad {
				if code(err) != "bad_answer" {
					t.Fatalf("err = %v, want bad_answer", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, b)
		})
	}
}

func TestCustomSendsOnlyTitleAndAuthor(t *testing.T) {
	var mu sync.Mutex
	var got []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
		jsonHandler(oneMatch)(w, r)
	}))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	e.set("http://127.0.0.1:"+portOf(srv)+"/abs/", "Bearer secret")
	ctx := context.Background()
	q := Query{Title: "The Hobbit", Author: "Tolkien", ASIN: "B0ABCDEFGH", ISBN: "9780261102217", Language: "english"}
	books, err := e.c.searchCustom(ctx, "custom:cp_1", q.clean())
	if err != nil || len(books) != 1 {
		t.Fatalf("books = %d, err = %v", len(books), err)
	}
	if _, err := e.c.searchCustom(ctx, "custom:cp_1", Query{Title: "Solo"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("requests = %d", len(got))
	}
	r := got[0]
	if r.URL.Path != "/abs/search" {
		t.Errorf("path = %q", r.URL.Path)
	}
	keys := []string{}
	for k := range r.URL.Query() {
		keys = append(keys, k)
	}
	if len(keys) != 3 || r.URL.Query().Get("mediaType") != "book" || r.URL.Query().Get("query") != "The Hobbit" || r.URL.Query().Get("author") != "Tolkien" {
		t.Errorf("query = %v", r.URL.RawQuery)
	}
	if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("User-Agent") != defaultUA || r.Header.Get("Referer") != "" {
		t.Errorf("headers = %v", r.Header)
	}
	second := got[1]
	if second.URL.Query().Has("author") || len(second.URL.Query()) != 2 {
		t.Errorf("author sent when empty: %v", second.URL.RawQuery)
	}
	if second.Header.Get("Cookie") != "" {
		t.Errorf("cookie sent back: %q", second.Header.Get("Cookie"))
	}
	// Without a header the Authorization line is absent.
	e.set("http://127.0.0.1:"+portOf(srv), "")
	if _, err := e.c.searchCustom(ctx, "custom:cp_1", Query{Title: "X"}); err != nil {
		t.Fatal(err)
	}
	if h := got[2].Header.Get("Authorization"); h != "" {
		t.Errorf("Authorization = %q, want none", h)
	}
}

func portOf(srv *httptest.Server) string {
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return p
}

func TestCustomSkipsWithoutTitle(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++ }))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	e.set("http://127.0.0.1:"+portOf(srv), "")
	b, err := e.c.searchCustom(context.Background(), "custom:cp_1", Query{ASIN: "B0ABCDEFGH", ISBN: "9780261102217"})
	if b != nil || err != nil || n != 0 {
		t.Errorf("books = %v, err = %v, requests = %d", b, err, n)
	}
	if b, err := e.c.searchCustom(context.Background(), "custom:gone", Query{Title: "X"}); b != nil || err != nil || n != 0 {
		t.Errorf("unknown key asked: %v %v %d", b, err, n)
	}
}

func TestCustomGuardRefuses(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(oneMatch))
	defer srv.Close()
	port := portOf(srv)
	tests := []struct {
		name, base, want string
		addrs            []string
	}{
		{"link-local name", "https://meta.example", "blocked", []string{"169.254.169.254"}},
		{"link-local literal", "http://169.254.169.254", "blocked", nil},
		{"unspecified literal", "http://0.0.0.0:" + port, "blocked", nil},
		{"http name to public", "http://meta.example:" + port, "insecure", []string{"8.8.8.8"}},
		{"http name mixed answers", "http://meta.example:" + port, "insecure", []string{"192.168.1.5", "8.8.8.8"}},
		{"http name public and refused", "http://meta.example:" + port, "insecure", []string{"169.254.1.1", "8.8.8.8"}},
		{"port 25", "http://127.0.0.1:25", "blocked", nil},
		{"port 22 name", "https://meta.example:22", "blocked", []string{"8.8.8.8"}},
		{"name that does not resolve", "https://nowhere.example", "offline", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newCustomEnv(t, srv)
			e.name("meta.example", tc.addrs...)
			e.set(tc.base, "")
			_, err := e.c.TestCustom(context.Background(), "cp_1")
			if code(err) != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if e.dialCount() != 0 {
				t.Errorf("dialled %v", e.dialed)
			}
		})
	}
}

func TestCustomGuardRedirects(t *testing.T) {
	hops := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/other"):
			http.Redirect(w, r, "http://localhost:"+portOf(srv)+"/search", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/chain"):
			var n int
			fmt.Sscanf(r.URL.Path, "/chain%d", &n)
			if n >= hops {
				jsonHandler(oneMatch)(w, r)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/chain%d", n+1), http.StatusFound)
		default:
			jsonHandler(oneMatch)(w, r)
		}
	}))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	e.name("localhost", "127.0.0.1")
	base := "http://127.0.0.1:" + portOf(srv)

	// A redirect to another host is refused, even a local one.
	e.set(base+"/other", "")
	if _, err := e.c.TestCustom(context.Background(), "cp_1"); code(err) != "blocked" {
		t.Errorf("other host: err = %v, want blocked", err)
	}
	// Three hops are allowed, four are not. The search path is /chain0/search, so route by prefix.
	for _, tc := range []struct {
		hops int
		ok   bool
	}{{3, true}, {4, false}} {
		hops = tc.hops
		e.set(base+"/chain0", "")
		_, err := e.c.TestCustom(context.Background(), "cp_1")
		if tc.ok && err != nil {
			t.Errorf("%d hops: err = %v", tc.hops, err)
		}
		if !tc.ok && code(err) != "blocked" {
			t.Errorf("%d hops: err = %v, want blocked", tc.hops, err)
		}
	}
}

func TestCustomRedirectRules(t *testing.T) {
	// The redirect rule itself, without a network: same origin only, https to http refused, 4 hops refused.
	hc := newCustomHTTP(Options{})
	mk := func(raw string, typed bool) *http.Request {
		r, _ := http.NewRequestWithContext(withPolicy(context.Background(), raw, typed), http.MethodGet, raw, nil)
		return r
	}
	first := mk("https://meta.example/search", true)
	tests := []struct {
		name string
		req  *http.Request
		via  []*http.Request
		want bool
	}{
		{"same origin", mk("https://meta.example/next", true), []*http.Request{first}, true},
		{"default port is the same", mk("https://META.example:443/next", true), []*http.Request{first}, true},
		{"other host", mk("https://evil.example/next", true), []*http.Request{first}, false},
		{"https to http", mk("http://meta.example/next", true), []*http.Request{first}, false},
		{"other port", mk("https://meta.example:8443/next", true), []*http.Request{first}, false},
		{"four hops", mk("https://meta.example/next", true), []*http.Request{first, first, first, first}, false},
		{"cover https hop", mk("https://pics.example/a", false), []*http.Request{first}, true},
		{"cover http hop", mk("http://pics.example/a", false), []*http.Request{first}, false},
	}
	for _, tc := range tests {
		tc.req.Header.Set("Referer", "x")
		err := hc.CheckRedirect(tc.req, tc.via)
		if (err == nil) != tc.want {
			t.Errorf("%s: err = %v, want ok %v", tc.name, err, tc.want)
		}
		if err == nil && tc.req.Header.Get("Referer") != "" {
			t.Errorf("%s: Referer kept", tc.name)
		}
	}
}

func TestCustomGuardAllows(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(oneMatch))
	defer srv.Close()
	port := portOf(srv)
	for _, tc := range []struct {
		name, base string
		addrs      []string
	}{
		{"http literal loopback", "http://127.0.0.1:" + port, nil},
		{"http name to private", "http://meta.lan:" + port, []string{"192.168.1.5"}},
		{"http name to tailnet", "http://meta.ts:" + port, []string{"100.100.1.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCustomEnv(t, srv)
			e.name("meta.lan", "192.168.1.5")
			e.name("meta.ts", "100.100.1.1")
			e.name("meta.example", "8.8.8.8")
			e.set(tc.base, "")
			n, err := e.c.TestCustom(context.Background(), "cp_1")
			if err != nil || n != 1 {
				t.Fatalf("found = %d, err = %v", n, err)
			}
		})
	}
	tls := httptest.NewTLSServer(jsonHandler(oneMatch))
	defer tls.Close()
	e := newCustomEnv(t, tls)
	e.set("https://127.0.0.1:"+portOf(tls), "")
	if n, err := e.c.TestCustom(context.Background(), "cp_1"); err != nil || n != 1 {
		t.Fatalf("tls: found = %d, err = %v", n, err)
	}
}

func TestCustomLimits(t *testing.T) {
	var status int
	var calls int
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if status == 429 && calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		if status == 429 {
			jsonHandler(oneMatch)(w, r)
			return
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	e.set("http://127.0.0.1:"+portOf(srv), "tok")
	big := `{"matches":[],"pad":"` + strings.Repeat("a", 2<<20) + `"}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"too large", 200, big, "too_large"},
		{"unauthorized", 401, "", "unauthorized"},
		{"forbidden", 403, "", "unauthorized"},
		{"not found", 404, "", "bad_answer"},
		{"server", 500, "", "server"},
		{"not json", 200, "<html>", "bad_answer"},
	} {
		status, body, calls = tc.status, tc.body, 0
		_, err := e.c.TestCustom(context.Background(), "cp_1")
		if code(err) != tc.want {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
		var oe *Error
		if !errors.As(err, &oe) || oe.Site != "My shelf" {
			t.Errorf("%s: site = %+v", tc.name, oe)
		}
		if strings.Contains(err.Error(), "tok") || strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("%s: error leaks: %q", tc.name, err.Error())
		}
	}
	status, calls = 429, 0
	if n, err := e.c.TestCustom(context.Background(), "cp_1"); err != nil || n != 1 || calls != 2 {
		t.Errorf("429 then 200: found = %d, err = %v, calls = %d", n, err, calls)
	}
}

func TestCustomNotCached(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(oneMatch))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	e.set("http://127.0.0.1:"+portOf(srv), "")
	for i := 0; i < 2; i++ {
		if _, err := e.c.TestCustom(context.Background(), "cp_1"); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.cache.m) != 0 {
		t.Errorf("cache entries = %d, want none", len(e.cache.m))
	}
	if e.dialCount() != 2 {
		t.Errorf("dials = %d, want 2 (answers are not cached)", e.dialCount())
	}
}

func TestCustomCoverRules(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 20)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("cover request carried the header")
		}
		w.Write(png)
	}))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	port := portOf(srv)
	origin := "http://127.0.0.1:" + port

	// With no site configured nothing changes: only the allowlist.
	for _, u := range []string{"https://pics.example/c.png", origin + "/c.png"} {
		if _, ok := e.c.coverAllowed(u); ok {
			t.Errorf("no sites: %s allowed", u)
		}
	}
	if r, ok := e.c.coverAllowed("https://covers.openlibrary.org/a.jpg"); !ok || r != routeAllowlist {
		t.Error("allowlisted cover lost")
	}
	e.set(origin, "tok")
	for _, tc := range []struct {
		u    string
		ok   bool
		want coverRoute
	}{
		{"https://covers.openlibrary.org/a.jpg", true, routeAllowlist},
		{origin + "/c.png", true, routeProvider},
		{"https://pics.example/c.png", true, routePublic},
		{"https://pics.example:8443/c.png", true, routePublic},
		{"https://pics.example:25/c.png", false, 0},
		{"http://pics.example/c.png", false, 0},
		{"ftp://pics.example/c.png", false, 0},
		{"https://u:p@pics.example/c.png", false, 0},
	} {
		r, ok := e.c.coverAllowed(tc.u)
		if ok != tc.ok || (ok && r != tc.want) {
			t.Errorf("coverAllowed(%s) = %v %v, want %v %v", tc.u, r, ok, tc.want, tc.ok)
		}
	}
	// The provider's own origin is fetched; a name that resolves to a private address is not.
	if b, kind, err := e.c.FetchCover(context.Background(), origin+"/c.png"); err != nil || kind != "png" || len(b) == 0 {
		t.Errorf("provider cover: %v %q %v", len(b), kind, err)
	}
	e.name("pics.example", "10.0.0.5")
	if _, _, err := e.c.FetchCover(context.Background(), "https://pics.example/c.png"); code(err) != "blocked" {
		t.Errorf("private cover host: err = %v, want blocked", err)
	}
	e.name("pics2.example", "8.8.8.8")
	if b, kind, err := e.c.FetchCover(context.Background(), "https://pics2.example:8443/c.png"); err == nil {
		t.Errorf("public https cover reached a plain server: %d %s", len(b), kind)
	}
}

func TestLookupWithCustom(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(`{"matches":[{"title":"The Hobbit","author":"J. R. R. Tolkien","asin":"B0ABCDEFGH"}]}`))
	defer srv.Close()
	e := newCustomEnv(t, srv)
	e.set("http://127.0.0.1:"+portOf(srv), "")
	chosen := ChosenProviders([]string{"custom:cp_1", "custom:gone", "custom:cp_1", "custom:bad key", "openlibrary"})
	want := []string{"audnexus", "openlibrary", "custom:cp_1", "custom:gone"}
	if strings.Join(chosen, ",") != strings.Join(want, ",") {
		t.Fatalf("chosen = %v, want %v", chosen, want)
	}
	res, err := e.c.LookupWith(context.Background(), Query{Title: "The Hobbit", Author: "Tolkien"}, []string{"custom:cp_1", "custom:gone"}, LookupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Searched) != 1 || res.Searched[0] != "custom:cp_1" {
		t.Errorf("searched = %v (a removed site must drop silently)", res.Searched)
	}
	if len(res.Steps) != 1 || res.Steps[0].Label != "My shelf" || res.Steps[0].Provider != "custom:cp_1" {
		t.Errorf("steps = %+v", res.Steps)
	}
	if res.SentBy()["custom:cp_1"] != 1 {
		t.Errorf("SentBy = %v", res.SentBy())
	}
	if len(res.Candidates) != 1 || res.Candidates[0].ProviderLabel != "My shelf" || res.Candidates[0].Score <= 0 {
		t.Errorf("candidates = %+v", res.Candidates)
	}
}

func TestCustomKeys(t *testing.T) {
	for s, want := range map[string]bool{
		"custom:cp_01HX": true, "custom:a-B_9": true, "custom:": false, "custom:a b": false,
		"custom:" + strings.Repeat("a", 40): true, "custom:" + strings.Repeat("a", 41): false,
		"openlibrary": false, "Custom:x": false, "custom:é": false,
	} {
		if IsCustomKey(s) != want {
			t.Errorf("IsCustomKey(%q) = %v", s, !want)
		}
	}
	extras := []string{}
	for i := 0; i < 15; i++ {
		extras = append(extras, fmt.Sprintf("custom:cp_%d", i))
	}
	if got := ChosenProviders(extras); len(got) != 11 {
		t.Errorf("chosen = %d, want audnexus + 10", len(got))
	}
}
