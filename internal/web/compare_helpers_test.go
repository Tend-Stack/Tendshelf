package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// compareNet is the network of the compare tests: it counts every request and answers a picture for
// the cover site only. No test touches the real network.
type compareNet struct {
	mu     sync.Mutex
	urls   []string
	client *online.Client
}

func (n *compareNet) RoundTrip(req *http.Request) (*http.Response, error) {
	n.mu.Lock()
	n.urls = append(n.urls, req.URL.String())
	n.mu.Unlock()
	status, body := 404, []byte(nil)
	if strings.HasPrefix(req.URL.String(), "https://m.media-amazon.com/") {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(1, 1, color.RGBA{200, 30, 30, 255})
		_ = jpeg.Encode(&buf, img, nil)
		status, body = 200, buf.Bytes()
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

func (n *compareNet) requests() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.urls)
}

// compareMod gives the app a lookup service on a fake network. The job runner of the test app is not
// started, so a lookup stays queued until a test finishes it by hand.
func compareMod(t *testing.T, cn *compareNet) func(*Options) {
	return func(o *Options) {
		svc := metadata.New(o.Library, o.Jobs, slog.New(slog.NewTextHandler(io.Discard, nil)))
		cn.client = online.New(online.Options{Cache: metadata.NewOnlineCache(o.Store), CoverDir: t.TempDir(), Transport: cn})
		svc.SetOnline(cn.client, media.Tools{}, t.TempDir())
		if err := svc.Register(o.Jobs); err != nil {
			t.Fatal(err)
		}
		o.Details = svc
	}
}

const compareCoverURL = "https://m.media-amazon.com/images/I/compare.jpg"

// compareResult is what the seeded lookup found: a good match with a narrator that differs, a
// publisher the book lacks, a description longer than the table shows, and a cover.
func compareResult() metadata.LookupResult {
	long := strings.Repeat("A long and winding description. ", 12)
	return metadata.LookupResult{
		Query:     online.Query{Title: "Sweepword Lantern"},
		FetchedAt: 1_730_000_000_000,
		Searched:  []string{"audnexus"},
		Steps:     compareSteps(false),
		Candidates: []metadata.Candidate{
			{Provider: "audnexus", ProviderLabel: "Audible", Score: 0.95, Exact: true, CoverURL: compareCoverURL, CoverCached: true,
				Details: metadata.Details{Title: "Sweepword Lantern", Narrators: []string{"Theo Onlinevoice"}, Publisher: "Online House",
					Year: 2019, Description: long}},
			{Provider: "audnexus", ProviderLabel: "Audible", Score: 0.5,
				Details: metadata.Details{Title: "Sweepword Lantern Two", Year: 2020}},
		},
	}
}

// compareFinishJob primes the cover cache and finishes a queued lookup with compareResult.
func compareFinishJob(t *testing.T, a *testApp, cn *compareNet, id string) {
	t.Helper()
	if _, _, err := cn.client.FetchCover(context.Background(), compareCoverURL); err != nil {
		t.Fatal(err)
	}
	res, err := json.Marshal(compareResult())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = ? WHERE id = ?`, string(res), id); err != nil {
		t.Fatal(err)
	}
}

// compareSeedJob queues a lookup for the book and, when done, finishes it with compareResult. It
// returns the job id.
func compareSeedJob(t *testing.T, e *sweepEnv, bookID string, done bool) string {
	t.Helper()
	id, err := e.a.srv.details.StartLookup(context.Background(), bookID, online.Query{Title: "Sweepword Lantern"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if done {
		compareFinishJob(t, e.a, e.cn, id)
	}
	return id
}

// compareGet and comparePost send a request as the owner (role 0) or another role.
func (e *sweepEnv) compareGet(role int, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return e.a.do(http.MethodGet, path, nil, mod, e.ck[role])
}

func (e *sweepEnv) comparePost(t *testing.T, role int, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("_csrf", e.a.tokenFor(t, e.ck[role]))
	return e.a.do(http.MethodPost, path, form, nil, e.ck[role])
}

func compareHx(r *http.Request) { r.Header.Set("HX-Request", "true") }

// compareTag returns the first tag of the body that matches the pattern, or "".
func compareTag(body, pattern string) string {
	return regexp.MustCompile(pattern).FindString(body)
}

// compareInput is the checkbox of one field of the table.
func compareInput(body, field string) string {
	return compareTag(body, `<input[^>]*name="use"[^>]*value="`+field+`"[^>]*>`)
}

// compareSheetRegion is the part of a page that holds the open sheet: from #sheet to the first script.
func compareSheetRegion(body string) string {
	i := strings.Index(body, `<div id="sheet"`)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	if j := strings.Index(rest, "<script"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
