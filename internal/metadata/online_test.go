package metadata_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

func lookupSample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "online", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lookupNet serves canned answers by URL prefix and records every request. No test touches the network.
type lookupNet struct {
	mu     sync.Mutex
	urls   []string
	routes []lookupRoute
	fail   int // when set, every request answers with this status
}

type lookupRoute struct {
	prefix string
	body   []byte
}

func (n *lookupNet) on(prefix string, body []byte) {
	n.routes = append(n.routes, lookupRoute{prefix, body})
}

func (n *lookupNet) requests() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.urls)
}

func (n *lookupNet) RoundTrip(req *http.Request) (*http.Response, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	u := req.URL.String()
	n.urls = append(n.urls, u)
	if n.fail != 0 {
		return &http.Response{StatusCode: n.fail, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
	}
	status, body := 404, []byte(nil)
	for _, r := range n.routes {
		if strings.HasPrefix(u, r.prefix) {
			status, body = 200, r.body
			break
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

// lookupEnv is a scanned library with a started job runner and a service wired to a fake network.
type lookupEnv struct {
	*env
	runner  *jobs.Runner
	svc     *metadata.Service
	net     *lookupNet
	dataDir string
}

func newLookupEnv(t *testing.T) *lookupEnv { return newLookupEnvWith(t, nil) }

// newLookupEnvWith is newLookupEnv with a hook that adjusts the online client's options.
func newLookupEnvWith(t *testing.T, tune func(*online.Options)) *lookupEnv {
	t.Helper()
	tools := mediatest.FakeTools(t)
	e := newEnv(t, tools)
	runner, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	le := &lookupEnv{env: e, runner: runner, net: &lookupNet{}, dataDir: t.TempDir()}
	le.svc = metadata.New(e.repo, runner, quiet())
	opts := online.Options{Cache: metadata.NewOnlineCache(e.st), CoverDir: t.TempDir(), Transport: le.net}
	if tune != nil {
		tune(&opts)
	}
	client := online.New(opts)
	le.svc.SetOnline(client, tools, le.dataDir)
	if err := le.svc.Register(runner); err != nil {
		t.Fatal(err)
	}
	if err := runner.Start(bg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = runner.Stop(ctx)
	})
	le.net.on("https://api.audible.com/1.0/catalog/products", lookupSample(t, "audible_search.json"))
	le.net.on("https://api.audnex.us/books/B0TESTBOOK", lookupSample(t, "audnexus_book.json"))
	le.net.on("https://api.audnex.us/books/B0OTHER001", lookupSample(t, "audnexus_other.json"))
	le.net.on("https://api.audnex.us/authors/B0AUTHOR01", lookupSample(t, "audnexus_author.json"))
	le.net.on("https://api.audnex.us/authors?", lookupSample(t, "audnexus_author_search.json"))
	le.net.on("https://m.media-amazon.com/", lookupSample(t, "cover_sample.jpg"))
	return le
}

func (e *lookupEnv) wait(id string) jobs.Job {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		j, err := e.runner.Get(bg, id)
		if err != nil {
			e.t.Fatal(err)
		}
		if j.State == jobs.StateDone || j.State == jobs.StateFailed {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("job %s did not finish", id)
	return jobs.Job{}
}

// lookup runs a lookup job for the book and returns its result.
func (e *lookupEnv) lookup(bookID string, q online.Query) metadata.LookupResult {
	e.t.Helper()
	id, err := e.svc.StartLookup(bg, bookID, q, nil, "")
	if err != nil {
		e.t.Fatal(err)
	}
	if j := e.wait(id); j.State != jobs.StateDone {
		e.t.Fatalf("lookup job: %s %s", j.State, j.ErrorCode)
	}
	_, res, err := e.svc.LookupResultFor(bg, id, bookID)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func candidateTitled(t *testing.T, res metadata.LookupResult, title string) metadata.Candidate {
	t.Helper()
	for _, c := range res.Candidates {
		if c.Details.Title == title {
			return c
		}
	}
	t.Fatalf("no candidate titled %q in %d candidates", title, len(res.Candidates))
	return metadata.Candidate{}
}

const lookupBookDir = "Ann Author/Plain Title"

// newLookupBook scans a one-file book with a title and author but no narrator or year.
func newLookupBook(t *testing.T) (*lookupEnv, string) {
	e := newLookupEnv(t)
	e.write(lookupBookDir+"/p.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Plain Title", "artist": "Ann Author"}})
	e.scanAndProbe()
	b := e.book(lookupBookDir)
	e.local(b.ID)
	return e, b.ID
}

func TestLookupFromOnline(t *testing.T) {
	d := metadata.FromOnline(online.Book{
		Title: "The Test Match", Subtitle: "A Synthetic Romance", Publisher: "Example Audio", Language: "english",
		Description: "<p>A <b>dual-narrator</b> story. Full cast performance. Fish &amp; chips.</p>",
		ISBN:        "978-1-234-56789-7", ASIN: "b0testbook", Year: 2023, Format: "unabridged",
		SeriesName: "The Match Series", Sequence: "Book 2",
		Authors:   []online.Person{{Name: "Ann Author", ASIN: "B0AUTHOR01"}},
		Narrators: []string{"Ben Narrator", "Cara Reader", "Ben Narrator"}, Genres: []string{"Romance"},
	})
	if d.Title != "The Test Match" || d.ISBN != "9781234567897" || d.ASIN != "B0TESTBOOK" || d.Year != 2023 {
		t.Errorf("fields: %+v", d)
	}
	if len(d.Series) != 1 || d.Series[0] != (metadata.SeriesRef{Name: "The Match Series", Sequence: "2"}) {
		t.Errorf("series: %+v", d.Series)
	}
	if strings.Contains(d.Description, "<") || !strings.Contains(d.Description, "Fish & chips") {
		t.Errorf("description: %q", d.Description)
	}
	if d.Abridged == nil || *d.Abridged || d.Dramatized == nil || !*d.Dramatized {
		t.Errorf("production: abridged=%v dramatized=%v", d.Abridged, d.Dramatized)
	}
	if !slices.Equal(d.Narrators, []string{"Ben Narrator", "Cara Reader"}) || !slices.Equal(d.Authors, []string{"Ann Author"}) {
		t.Errorf("people: %v %v", d.Narrators, d.Authors)
	}
	long := metadata.FromOnline(online.Book{Title: "T", Description: strings.Repeat("é", 2500)})
	if n := len([]rune(long.Description)); n != 2000 {
		t.Errorf("description length %d", n)
	}
	if a := metadata.FromOnline(online.Book{Title: "T", Format: "Abridged"}).Abridged; a == nil || !*a {
		t.Error("abridged format")
	}
}

func TestLookupJob(t *testing.T) {
	e, id := newLookupBook(t)
	q, err := e.svc.LookupQuery(bg, id)
	if err != nil || q.Title != "Plain Title" || q.Author != "Ann Author" {
		t.Fatalf("prefilled query: %+v %v", q, err)
	}
	if _, err := e.svc.StartLookup(bg, id, online.Query{}, nil, ""); !errors.Is(err, online.ErrEmptyQuery) {
		t.Errorf("empty query: %v", err)
	}
	// An old cache row is pruned at the start of the job; a fresh one stays.
	old, fresh := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for key, at := range map[string]int64{old: 1, fresh: time.Now().UnixMilli()} {
		if _, err := e.st.Writer().Exec(`INSERT INTO online_cache(provider, query_key, response_json, fetched_at) VALUES ('google', ?, '{}', ?)`, key, at); err != nil {
			t.Fatal(err)
		}
	}
	res := e.lookup(id, online.Query{Title: "The Test Match", Author: "Ann Author"})
	if e.count(`SELECT COUNT(*) FROM online_cache WHERE query_key = ?`, old) != 0 || e.count(`SELECT COUNT(*) FROM online_cache WHERE query_key = ?`, fresh) != 1 {
		t.Error("cache pruning")
	}
	if len(res.Candidates) != 2 || !slices.Equal(res.Searched, []string{"audnexus"}) {
		t.Fatalf("candidates %d, searched %v, errors %v", len(res.Candidates), res.Searched, res.Errors)
	}
	for i := 1; i < len(res.Candidates); i++ {
		if res.Candidates[i].Score > res.Candidates[i-1].Score {
			t.Errorf("not sorted by score: %v", res.Candidates)
		}
	}
	top := candidateTitled(t, res, "The Test Match")
	if top.Score < res.Candidates[0].Score {
		t.Error("the matching book should come first")
	}
	if !top.CoverCached || top.CoverURL == "" || top.ProviderLabel != "Audible (via Audnexus)" || top.AuthorASINs["Ann Author"] != "B0AUTHOR01" {
		t.Errorf("top candidate: %+v", top)
	}
	if b, kind, ok := e.svc.CachedCover(top.CoverURL); !ok || kind != "jpeg" || len(b) == 0 {
		t.Errorf("cached cover: %d %q %v", len(b), kind, ok)
	}
	if other := candidateTitled(t, res, "Another Book"); other.CoverCached {
		t.Error("a candidate without a cover URL cannot have a cached cover")
	}
	if top.Details.Series[0].Sequence != "2" {
		t.Errorf("series: %+v", top.Details.Series)
	}

	// Turned off: no job starts, and a job that was already queued fails without a request.
	if err := metadata.SetOnlineEnabled(bg, e.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'settings.online'`) != 1 {
		t.Error("the switch is audited")
	}
	if _, err := e.svc.StartLookup(bg, id, q, nil, ""); !errors.Is(err, metadata.ErrOnlineOff) {
		t.Errorf("start while off: %v", err)
	}
	before := e.net.requests()
	jid, err := e.runner.Enqueue(bg, metadata.KindOnline, metadata.LookupPayload{BookID: id, Query: q, Providers: []string{"audnexus"}}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(jid); j.State != jobs.StateFailed || j.ErrorCode != "online_off" || e.net.requests() != before {
		t.Errorf("queued job while off: %s %s, %d new requests", j.State, j.ErrorCode, e.net.requests()-before)
	}
	if on, err := metadata.OnlineEnabled(bg, e.st.Reader()); err != nil || on {
		t.Errorf("switch: %v %v", on, err)
	}
}

func TestLookupSettingDefaultsOn(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	if on, err := metadata.OnlineEnabled(bg, e.st.Reader()); err != nil || !on {
		t.Errorf("a missing key means on: %v %v", on, err)
	}
	if err := metadata.SetOnlineEnabled(bg, e.st, true, ""); err != nil {
		t.Fatal(err)
	}
	if on, _ := metadata.OnlineEnabled(bg, e.st.Reader()); !on {
		t.Error("switched on")
	}
}

func TestLookupResultForOtherBook(t *testing.T) {
	e, id := newLookupBook(t)
	e.write("Bo Book/Second/s.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Second"}})
	e.scanAndProbe()
	other := e.book("Bo Book/Second")
	jid, err := e.svc.StartLookup(bg, id, online.Query{Title: "The Test Match"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	e.wait(jid)
	if _, _, err := e.svc.LookupResultFor(bg, jid, other.ID); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("other book: %v", err)
	}
	if _, _, err := e.svc.LookupResultFor(bg, "jb_missing", id); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
	// A job of another kind is not a lookup result either.
	mj, err := e.runner.Enqueue(bg, metadata.Kind, metadata.Payload{BookID: id}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e.wait(mj)
	if _, _, err := e.svc.LookupResultFor(bg, mj, id); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("other kind: %v", err)
	}
	if _, _, err := e.svc.LookupResultFor(bg, jid, id); err != nil {
		t.Errorf("own book: %v", err)
	}
}
