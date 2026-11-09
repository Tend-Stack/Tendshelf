package online

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// reply is one canned answer.
type reply struct {
	status   int
	body     []byte
	header   http.Header
	location string
}

// fakeNet serves canned answers by URL prefix and records every request. No test touches the network.
type fakeNet struct {
	mu     sync.Mutex
	urls   []string
	routes []route
	fail   error
	count  map[string]int
}

type route struct {
	prefix string
	reply  func(n int) reply
}

func (f *fakeNet) on(prefix string, r reply) {
	f.routes = append(f.routes, route{prefix, func(int) reply { return r }})
}

func (f *fakeNet) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := req.URL.String()
	f.urls = append(f.urls, u)
	if f.fail != nil {
		return nil, f.fail
	}
	if f.count == nil {
		f.count = map[string]int{}
	}
	for _, r := range f.routes {
		if strings.HasPrefix(u, r.prefix) {
			f.count[r.prefix]++
			rp := r.reply(f.count[r.prefix])
			if rp.status == 0 {
				rp.status = 200
			}
			h := rp.header
			if h == nil {
				h = http.Header{}
			}
			if rp.location != "" {
				h.Set("Location", rp.location)
			}
			return &http.Response{StatusCode: rp.status, Header: h, Body: io.NopCloser(bytes.NewReader(rp.body)), Request: req}, nil
		}
	}
	return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func (f *fakeNet) reset() { f.urls = nil }

type memCache struct {
	mu  sync.Mutex
	m   map[string]memEntry
	now func() time.Time
}

type memEntry struct {
	body []byte
	at   time.Time
}

func (c *memCache) Get(_ context.Context, provider, key string, maxAge time.Duration) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[provider+key]
	if !ok || c.now().Sub(e.at) >= maxAge {
		return nil, false, nil
	}
	return e.body, true, nil
}

func (c *memCache) Put(_ context.Context, provider, key string, body []byte, at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]memEntry{}
	}
	c.m[provider+key] = memEntry{body, at}
	return nil
}

type env struct {
	c      *Client
	net    *fakeNet
	cache  *memCache
	clock  time.Time
	sleeps []time.Duration
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{net: &fakeNet{}, clock: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	e.cache = &memCache{now: func() time.Time { return e.clock }}
	e.net.on("https://api.audible.com/1.0/catalog/products", reply{body: sample(t, "audible_search.json")})
	e.net.on("https://api.audnex.us/books/B0TESTBOOK", reply{body: sample(t, "audnexus_book.json")})
	e.net.on("https://api.audnex.us/books/B0OTHER001", reply{body: sample(t, "audnexus_other.json")})
	e.net.on("https://openlibrary.org/search.json", reply{body: sample(t, "openlibrary_search.json")})
	e.net.on("https://openlibrary.org/works/OL1W.json", reply{body: sample(t, "openlibrary_work.json")})
	e.net.on("https://itunes.apple.com/search", reply{body: sample(t, "itunes.json")})
	e.net.on("https://api.audnex.us/authors/B0AUTHOR01", reply{body: sample(t, "audnexus_author.json")})
	e.net.on("https://api.audnex.us/authors?", reply{body: sample(t, "audnexus_author_search.json")})
	e.c = New(Options{Cache: e.cache, CoverDir: t.TempDir(), Now: func() time.Time { return e.clock }, Transport: e.net})
	e.c.sleep = func(_ context.Context, d time.Duration) error {
		e.sleeps = append(e.sleeps, d)
		e.clock = e.clock.Add(d)
		return nil
	}
	return e
}

func TestOnlineParsers(t *testing.T) {
	a := parseAudnexusBook(sample(t, "audnexus_book.json"))
	if a == nil {
		t.Fatal("no book")
	}
	if a.Title != "The Test Match" || a.Subtitle != "A Synthetic Romance" || a.SeriesName != "The Match Series" || a.Sequence != "Book 2" {
		t.Errorf("titles: %+v", a)
	}
	if len(a.Authors) != 1 || a.Authors[0] != (Person{"Ann Author", "B0AUTHOR01"}) {
		t.Errorf("authors: %+v", a.Authors)
	}
	if strings.Join(a.Narrators, "|") != "Ben Narrator|Cara Reader" {
		t.Errorf("narrators: %v", a.Narrators)
	}
	if a.Year != 2023 || a.Publisher != "Example Audio" || a.ASIN != "B0TESTBOOK" || a.ISBN != "9781234567897" || a.Language != "English" || a.Format != "unabridged" {
		t.Errorf("fields: %+v", a)
	}
	if strings.Join(a.Genres, "|") != "Romance|Contemporary Romance" {
		t.Errorf("genres (tags are not genres): %v", a.Genres)
	}
	if !strings.Contains(a.Description, "<p>") {
		t.Errorf("description is kept raw: %q", a.Description)
	}
	if a.Rating != "4.5" || a.RatingCount != "" || a.ProviderLabel != "Audible (via Audnexus)" {
		t.Errorf("rating/label: %q %q %q", a.Rating, a.RatingCount, a.ProviderLabel)
	}
	if got := parseAudibleProducts(sample(t, "audible_search.json")); strings.Join(got, "|") != "B0TESTBOOK|B0OTHER001" {
		t.Errorf("products: %v", got)
	}
	o := parseOpenLibrarySearch(sample(t, "openlibrary_search.json"))
	if len(o) != 1 || o[0].Authors[0].Name != "Ann Author" || o[0].Year != 2023 || o[0].CoverURL != "https://covers.openlibrary.org/b/olid/OL1M-M.jpg" || o[0].Rating != "3.91" {
		t.Errorf("openlibrary: %+v", o)
	}
	if d := parseOpenLibraryWork(sample(t, "openlibrary_work.json")); d != "Work description." {
		t.Errorf("work: %q", d)
	}
	if d := parseOpenLibraryWork([]byte(`{"description":"plain"}`)); d != "plain" {
		t.Errorf("work string: %q", d)
	}
	i := parseITunes(sample(t, "itunes.json"))
	if len(i) != 1 || len(i[0].Authors) != 2 || i[0].Authors[1].Name != "Bo Co" || i[0].Year != 2023 || !strings.HasSuffix(i[0].CoverURL, "600x600bb.jpg") || i[0].ID != "42" {
		t.Errorf("itunes: %+v", i)
	}
	if parseAudnexusBook([]byte(`{"title":"no asin"}`)) != nil || parseAudnexusBook([]byte(`{"asin":"B0TESTBOOK"}`)) != nil || parseAudnexusBook([]byte(`null`)) != nil {
		t.Error("a book needs an ASIN and a title")
	}
}

func TestOnlineScore(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"abcd", "bcde", 0.75},
		{"the tide", "tide", 0.666667},
		{"ann author", "an author", 0.947368},
		{"harbour lights", "harbor lights", 0.962963},
		{"", "x", 0},
		{"", "", 1},
		{"private eyes", "private eye", 0.956522},
	} {
		if got := ratio(c.a, c.b); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("ratio(%q,%q)=%f want %f", c.a, c.b, got, c.want)
		}
	}
	if got := norm("Brontë & Co."); got != "bronte and co" {
		t.Errorf("norm=%q", got)
	}
	if got := norm("Straße Œuvre Þor"); got != "strasse oeuvre thor" {
		t.Errorf("norm=%q", got)
	}
	if norm("吾輩は猫である") == "" {
		t.Error("a Japanese title must stay non-empty")
	}
	if got := len([]rune(norm(strings.Repeat("ab ", 200)))); got > 199 {
		t.Errorf("norm cut: %d", got)
	}
	for _, r := range "àáâãäåāăą" {
		if fold(r) != "a" {
			t.Errorf("fold(%q)=%q", r, fold(r))
		}
	}
	b := Book{Title: "The Test Match", Authors: []Person{{Name: "Ann Author"}}}
	if s := Score(b, "The Test Match", "Ann Author"); s != 1 {
		t.Errorf("score=%f", s)
	}
	if s := Score(b, "Test Match", "Someone Else"); s < 0.6 || s >= 1 {
		t.Errorf("partial score=%f", s)
	}
	if s := Score(b, "", ""); s != 0.5 {
		t.Errorf("no query score=%f", s)
	}
}

func TestOnlineLookupRanksAndExact(t *testing.T) {
	e := newEnv(t)
	res, err := e.c.Lookup(context.Background(), Query{Title: "The Test Match", Author: "Ann Author"}, Providers)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, b := range res.Candidates {
		seen[b.Provider] = true
	}
	if len(seen) != 3 || len(res.Searched) != 3 || len(res.Errors) != 0 {
		t.Errorf("providers: %v %v %v", seen, res.Searched, res.Errors)
	}
	for i := 1; i < len(res.Candidates); i++ {
		if res.Candidates[i].Score > res.Candidates[i-1].Score {
			t.Errorf("not sorted at %d", i)
		}
	}
	for _, b := range res.Candidates {
		if b.Provider == "openlibrary" && b.Description != "Work description." {
			t.Errorf("work description: %q", b.Description)
		}
	}
	e = newEnv(t)
	res, err = e.c.Lookup(context.Background(), Query{Title: "x", ASIN: "b0testbook"}, nil)
	if err != nil || len(res.Candidates) != 0 {
		t.Errorf("no providers: %v %v", res, err)
	}
	res, _ = e.c.Lookup(context.Background(), Query{Title: "x", ASIN: "b0testbook"}, []string{"audnexus", "bogus"})
	if len(e.net.urls) != 1 || len(res.Candidates) != 1 || res.Candidates[0].Score != 1 || !res.Candidates[0].Exact || len(res.Searched) != 1 {
		t.Errorf("by ASIN: %v %+v", e.net.urls, res)
	}
	if _, err := e.c.Lookup(context.Background(), Query{Author: "Someone", ASIN: "bad"}, Providers); !errors.Is(err, ErrEmptyQuery) {
		t.Errorf("empty query: %v", err)
	}
}

func TestOnlineQueryOnlyFourFields(t *testing.T) {
	e := newEnv(t)
	q := Query{Title: "The Test Match", Author: "Ann Author", ISBN: "978-1-234-56789-7", Language: "French"}
	if _, err := e.c.Lookup(context.Background(), q, Providers); err != nil {
		t.Fatal(err)
	}
	if len(e.net.urls) == 0 {
		t.Fatal("no requests")
	}
	for _, u := range e.net.urls {
		low := strings.ToLower(u)
		if !strings.HasPrefix(u, "https://") || strings.Contains(low, "french") || strings.Contains(low, "language=") {
			t.Errorf("bad URL %s", u)
		}
	}
	joined := strings.Join(e.net.urls, "\n")
	if !strings.Contains(joined, "title=The%20Test%20Match") || !strings.Contains(joined, "9781234567897") {
		t.Errorf("wanted fields missing:\n%s", joined)
	}
	if got := (Query{Title: "  " + strings.Repeat("é", 300), ASIN: "b0abc1234x", ISBN: "12"}).clean(); len([]rune(got.Title)) != 200 || got.ASIN != "B0ABC1234X" || got.ISBN != "" {
		t.Errorf("clean: %+v", got)
	}
}

func TestOnlineCacheAndSpacing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := Query{Title: "The Test Match", Author: "Ann Author"}
	if _, err := e.c.Lookup(ctx, q, Providers); err != nil {
		t.Fatal(err)
	}
	first := len(e.net.urls)
	if len(e.sleeps) == 0 {
		t.Error("same-host requests were not spaced")
	}
	for _, d := range e.sleeps {
		if d <= 0 || d > time.Second {
			t.Errorf("sleep %v", d)
		}
	}
	e.net.reset()
	if _, err := e.c.Lookup(ctx, q, Providers); err != nil {
		t.Fatal(err)
	}
	if len(e.net.urls) != 0 {
		t.Errorf("second lookup made %d requests (first %d)", len(e.net.urls), first)
	}
	e.clock = e.clock.Add(cacheTTL + time.Second)
	if _, err := e.c.Lookup(ctx, q, []string{"itunes"}); err != nil || len(e.net.urls) != 1 {
		t.Errorf("expired entries are fetched again: %v %d", err, len(e.net.urls))
	}
	// gaps: audnexus 500 ms, itunes 1 s, unknown covers 1 s
	if hostGap("api.audnex.us") != 500*time.Millisecond || hostGap("itunes.apple.com") != time.Second || hostGap("covers.openlibrary.org") != time.Second {
		t.Error("host gaps")
	}

	// 429 is retried once after Retry-After, clamped to 1-10 s.
	for _, c := range []struct {
		header string
		want   time.Duration
	}{{"3", 3 * time.Second}, {"99", 10 * time.Second}, {"0", time.Second}, {"", 5 * time.Second}, {"soon", 5 * time.Second}} {
		e2 := newEnv(t)
		e2.net.routes = nil
		e2.net.routes = append(e2.net.routes, route{"https://itunes.apple.com/", func(n int) reply {
			if n == 1 {
				return reply{status: 429, header: http.Header{"Retry-After": {c.header}}}
			}
			return reply{body: sample(t, "itunes.json")}
		}})
		res, err := e2.c.Lookup(ctx, Query{Title: "x"}, []string{"itunes"})
		if err != nil || len(res.Candidates) != 1 || len(e2.net.urls) != 2 {
			t.Fatalf("retry %q: %v %v %d", c.header, err, res, len(e2.net.urls))
		}
		if e2.sleeps[0] != c.want {
			t.Errorf("Retry-After %q slept %v want %v", c.header, e2.sleeps[0], c.want)
		}
	}
	// a second 429 is "busy" and is not cached
	e3 := newEnv(t)
	e3.net.routes = []route{{"https://itunes.apple.com/", func(int) reply { return reply{status: 429} }}}
	res, _ := e3.c.Lookup(ctx, Query{Title: "x"}, []string{"itunes"})
	if res.Errors["itunes"] != "busy" || len(e3.net.urls) != 2 || len(e3.cache.m) != 0 {
		t.Errorf("busy: %v %d %d", res.Errors, len(e3.net.urls), len(e3.cache.m))
	}
	// a 404 is cached as null; a 500 is an error and is not cached
	e4 := newEnv(t)
	for i := 0; i < 2; i++ {
		res, _ = e4.c.Lookup(ctx, Query{Title: "x", ASIN: "B0NOWHERE1"}, []string{"audnexus"})
	}
	if len(res.Candidates) != 0 || len(e4.net.urls) != len(audnexusRegions) {
		t.Errorf("404: %d requests, want %d", len(e4.net.urls), len(audnexusRegions))
	}
	e5 := newEnv(t)
	e5.net.routes = []route{{"https://itunes.apple.com/", func(int) reply { return reply{status: 500} }}}
	res, _ = e5.c.Lookup(ctx, Query{Title: "x"}, []string{"itunes"})
	if res.Errors["itunes"] != "server" || len(e5.cache.m) != 0 {
		t.Errorf("500: %v", res.Errors)
	}
	// garbage in a 200 is bad_answer
	e6 := newEnv(t)
	e6.net.routes = []route{{"https://itunes.apple.com/", func(int) reply { return reply{body: []byte("<html>")} }}}
	res, _ = e6.c.Lookup(ctx, Query{Title: "x"}, []string{"itunes"})
	if res.Errors["itunes"] != "bad_answer" {
		t.Errorf("bad answer: %v", res.Errors)
	}
}

func TestOnlineRegions(t *testing.T) {
	e := newEnv(t)
	body, _ := json.Marshal(map[string]any{"asin": "B0FRENCH01", "title": "Un livre"})
	e.net.routes = []route{{"https://api.audnex.us/books/B0FRENCH01?region=fr", func(int) reply { return reply{body: body} }}}
	res, err := e.c.Lookup(context.Background(), Query{Title: "x", ASIN: "B0FRENCH01", Language: "French"}, []string{"audnexus"})
	if err != nil || len(res.Candidates) != 1 || res.Candidates[0].ASIN != "B0FRENCH01" {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.HasSuffix(e.net.urls[0], "?region=fr") || len(e.net.urls) != 1 {
		t.Errorf("language picks the store first: %v", e.net.urls)
	}
	// other stores are tried in turn when the first says 404
	e = newEnv(t)
	e.net.routes = []route{{"https://api.audnex.us/books/B0FRENCH01?region=jp", func(int) reply { return reply{body: body} }}}
	res, _ = e.c.Lookup(context.Background(), Query{Title: "x", ASIN: "B0FRENCH01", Language: "fr"}, []string{"audnexus"})
	if len(res.Candidates) != 1 || len(e.net.urls) != len(audnexusRegions) {
		t.Errorf("tried %d stores, %d found", len(e.net.urls), len(res.Candidates))
	}
	if !strings.HasSuffix(e.net.urls[0], "?region=fr") || strings.Contains(e.net.urls[1], "?region") {
		t.Errorf("order: %v", e.net.urls[:2])
	}
	// a nowhere ASIN tries every store; a title lookup only the first
	e = newEnv(t)
	res, _ = e.c.Lookup(context.Background(), Query{Title: "x", ASIN: "B0NOWHERE1"}, []string{"audnexus"})
	if len(res.Candidates) != 0 || len(e.net.urls) != len(audnexusRegions) {
		t.Errorf("nowhere: %d", len(e.net.urls))
	}
	e = newEnv(t)
	_, _ = e.c.Lookup(context.Background(), Query{Title: "The Test Match", Language: "German"}, []string{"audnexus"})
	for _, u := range e.net.urls[1:] {
		if !strings.HasSuffix(u, "?region=de") {
			t.Errorf("title lookup uses the first store only: %s", u)
		}
	}
}

func TestOnlineGuard(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	code := func(err error) string {
		var oe *Error
		if !errors.As(err, &oe) {
			t.Fatalf("not an *Error: %v", err)
		}
		if strings.Contains(oe.Text(), "http") || strings.Contains(oe.Text(), "evil") || strings.Contains(oe.Text(), "Get ") {
			t.Errorf("text leaks: %q", oe.Text())
		}
		return oe.Code
	}
	_, err := e.c.getJSON(ctx, "audnexus", "http://api.audnex.us/books/B0TESTBOOK")
	if code(err) != "refused" {
		t.Errorf("http: %v", err)
	}
	_, err = e.c.getJSON(ctx, "audnexus", "https://evil.example/x.json")
	if code(err) != "refused" {
		t.Errorf("unlisted host: %v", err)
	}
	_, err = e.c.getJSON(ctx, "audnexus", "https://api.audnex.us:8443/books/B0TESTBOOK")
	if code(err) != "refused" {
		t.Errorf("port: %v", err)
	}
	e.net.routes = []route{{"https://api.audnex.us/books/R", func(int) reply { return reply{status: 302, location: "https://evil.example/steal"} }}}
	_, err = e.c.getJSON(ctx, "audnexus", "https://api.audnex.us/books/R")
	if code(err) != "refused" {
		t.Errorf("redirect: %v", err)
	}
	for _, u := range e.net.urls {
		if strings.Contains(u, "evil") {
			t.Errorf("redirect target was dialled: %s", u)
		}
	}
	big := bytes.Repeat([]byte("a"), maxJSONBytes+1)
	e.net.routes = []route{{"https://api.audnex.us/books/BIG", func(int) reply { return reply{body: big} }}}
	_, err = e.c.getJSON(ctx, "audnexus", "https://api.audnex.us/books/BIG")
	if code(err) != "too_large" {
		t.Errorf("big: %v", err)
	}
	e.net.fail = errors.New("dial tcp 10.0.0.1: connection refused")
	_, err = e.c.getJSON(ctx, "audnexus", "https://api.audnex.us/books/OFF")
	if code(err) != "offline" || strings.Contains(err.Error(), "10.0.0.1") {
		t.Errorf("offline: %v", err)
	}
	want := "Tendshelf could not reach Open Library. Check this computer's internet connection and try again."
	if got := (&Error{Code: "offline", Site: "Open Library"}).Text(); got != want {
		t.Errorf("text: %q", got)
	}
	e.net.fail = nil
	// four hops of redirect between listed hosts stop
	e.net.routes = []route{{"https://api.audnex.us/", func(int) reply { return reply{status: 302, location: "https://api.audnex.us/again"} }}}
	_, err = e.c.getJSON(ctx, "audnexus", "https://api.audnex.us/loop")
	if code(err) != "refused" {
		t.Errorf("loop: %v", err)
	}
	// a redirect to a listed cover host is allowed for covers
	if !coverURLOK("https://archive.org/x.jpg") || !coverURLOK("https://ia800.us.archive.org/x.jpg") || coverURLOK("https://evilarchive.org/x.jpg") || coverURLOK("http://archive.org/x.jpg") {
		t.Error("cover host list")
	}
}

func TestOnlineCover(t *testing.T) {
	jpg, png, webp := sample(t, "cover_sample.jpg"), sample(t, "cover_sample.png"), sample(t, "cover_sample.webp")
	for i, c := range []struct {
		b    []byte
		want string
	}{{jpg, "jpeg"}, {png, "png"}, {webp, "webp"}, {[]byte("RIFF\x24\x00\x00\x00WAVEfmt "), ""}, {[]byte("<html>not an image</html>"), ""}, {nil, ""}} {
		if got := imageKind(c.b); got != c.want {
			t.Errorf("case %d: %q want %q", i, got, c.want)
		}
	}
	ctx := context.Background()
	const u = "https://m.media-amazon.com/images/I/test.jpg"
	e := newEnv(t)
	e.net.on("https://m.media-amazon.com/", reply{body: jpg})
	if _, _, ok := e.c.CachedCover(u); ok {
		t.Error("cache should start empty")
	}
	b, kind, err := e.c.FetchCover(ctx, u)
	if err != nil || kind != "jpeg" || !bytes.Equal(b, jpg) {
		t.Fatalf("fetch: %v %q", err, kind)
	}
	e.net.reset()
	b, kind, err = e.c.FetchCover(ctx, u)
	if err != nil || kind != "jpeg" || len(e.net.urls) != 0 || !bytes.Equal(b, jpg) {
		t.Errorf("cache hit: %v %q %d", err, kind, len(e.net.urls))
	}
	if _, k, ok := e.c.CachedCover(u); !ok || k != "jpeg" {
		t.Error("CachedCover")
	}
	e.clock = e.clock.Add(cacheTTL + time.Minute)
	if _, _, ok := e.c.CachedCover(u); ok {
		t.Error("an expired cover must not be served")
	}
	if entries, _ := os.ReadDir(e.c.coverDir); len(entries) != 1 || strings.HasPrefix(entries[0].Name(), ".") {
		t.Errorf("cover dir: %v", entries)
	}

	check := func(name, url string, body []byte, wantCode string) {
		t.Helper()
		e := newEnv(t)
		e.net.on("https://m.media-amazon.com/", reply{body: body})
		e.net.on("https://covers.openlibrary.org/", reply{body: body})
		_, _, err := e.c.FetchCover(ctx, url)
		var oe *Error
		if !errors.As(err, &oe) || oe.Code != wantCode {
			t.Errorf("%s: %v want %s", name, err, wantCode)
		}
		if e.c.coverDir != "" {
			if entries, _ := os.ReadDir(e.c.coverDir); len(entries) != 0 {
				t.Errorf("%s: left files %v", name, entries)
			}
		}
	}
	check("too big", u, append(append([]byte{}, jpg...), make([]byte, maxCoverBytes)...), "too_large")
	check("html as jpg", "https://covers.openlibrary.org/b/olid/X-M.jpg", []byte("<html>hi</html>"), "bad_answer")
	check("unlisted host", "https://evil.example/x.jpg", jpg, "refused")
	check("http", "http://m.media-amazon.com/x.jpg", jpg, "refused")
}

func TestOnlineAuthor(t *testing.T) {
	e := newEnv(t)
	a, err := e.c.Author(context.Background(), "", "ann  author")
	if err != nil || a.ASIN != "B0AUTHOR01" || a.Name != "Ann Author" || len(a.Genres) != 2 || !strings.HasSuffix(a.ImageURL, "author.jpg") {
		t.Errorf("by name: %v %+v", err, a)
	}
	if len(e.net.urls) != 2 || !strings.Contains(e.net.urls[0], "authors?name=ann%20%20author") {
		t.Errorf("urls: %v", e.net.urls)
	}
	e = newEnv(t)
	if a, _ = e.c.Author(context.Background(), "B0AUTHOR01", ""); a.Name != "Ann Author" || len(e.net.urls) != 1 {
		t.Errorf("by ASIN: %+v %v", a, e.net.urls)
	}
	if a, err = e.c.Author(context.Background(), "", "Nobody Known"); err != nil || a.ASIN != "" {
		t.Errorf("no match: %v %+v", err, a)
	}
	if _, err = e.c.Author(context.Background(), "", ""); !errors.Is(err, ErrEmptyQuery) {
		t.Errorf("empty: %v", err)
	}
}

// TestOnlineNoOtherClient walks the repository: no other package may open network connections, except
// internal/desktopshell/check.go (decision 0020) and internal/auth/oidc, the sign-in provider client (decision 0037).
func TestOnlineNoOtherClient(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	banned := []string{"http.Client{", "&http.Client", "http.DefaultClient", "http.Get(", "http.Post(", "net.Dial(", "net.DialTimeout("}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && rel != "." || rel == "internal/metadata/online" || rel == "internal/auth/oidc" || rel == "internal/testutil/webdriver" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if rel == "internal/desktopshell/check.go" {
			return nil // the desktop shell's /healthz check, the one other client (decision 0020, AGENTS.md)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, s := range banned {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s uses %s; only internal/metadata/online may open connections", rel, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOnlineCoverHosts(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"m.media-amazon.com", true},
		{"images-na.ssl-images-amazon.com", true},
		{"is1-ssl.mzstatic.com", true},
		{"covers.openlibrary.org", true},
		{"books.google.com", true},
		{"media-amazon.com.evil.example", false},
		{"evilmedia-amazon.com", false},
		{"example.com", false},
	}
	for _, c := range cases {
		if got := isCoverHost(c.host); got != c.want {
			t.Errorf("isCoverHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestChosenProviders(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"default is the reliable source only", nil, "audnexus"},
		{"open library alone", []string{"openlibrary"}, "audnexus,openlibrary"},
		{"apple books alone", []string{"itunes"}, "audnexus,itunes"},
		{"both, in list order", []string{"itunes", "openlibrary"}, "audnexus,openlibrary,itunes"},
		{"repeats once", []string{"itunes", "itunes"}, "audnexus,itunes"},
		{"junk and the retired google are ignored", []string{"google", "bogus", "", "audnexus"}, "audnexus"},
		{"junk beside a real place", []string{"google", "itunes"}, "audnexus,itunes"},
	}
	for _, c := range cases {
		if got := strings.Join(ChosenProviders(c.in), ","); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestGoogleBooksIsGone(t *testing.T) {
	if contains(Providers, "google") || label("google") != "google" {
		t.Errorf("google is still a provider: %v", Providers)
	}
	if _, ok := apiHosts["www.googleapis.com"]; ok {
		t.Error("the Google Books host is still allowed")
	}
	e := newEnv(t)
	// A saved setting or queued job that still names google asks nothing for it and sends nothing.
	res, err := e.c.Lookup(context.Background(), Query{Title: "The Test Match"}, []string{"google"})
	if err != nil || len(res.Steps) != 0 || len(res.Searched) != 0 {
		t.Errorf("google lookup: %v %+v", err, res)
	}
	if _, err := e.c.getJSON(context.Background(), "openlibrary", "https://www.googleapis.com/books/v1/volumes?q=x"); errorCode(err) != "refused" {
		t.Errorf("host refused: %v", err)
	}
	if len(e.net.urls) != 0 {
		t.Errorf("requests sent: %v", e.net.urls)
	}
}
