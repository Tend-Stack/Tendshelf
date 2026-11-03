//go:build dev

package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// slowNet answers the lookup sites from the online package's sample files, each after a pause, so a
// test can watch the log fill. It never touches the real network.
type slowNet struct {
	compareNet
	delay  time.Duration
	runner *jobs.Runner // started by the test once the server has registered its jobs
}

var slowSamples = []struct{ prefix, file string }{
	{"https://api.audible.com/1.0/catalog/products", "audible_search.json"},
	{"https://api.audnex.us/books/B0TESTBOOK", "audnexus_book.json"},
	{"https://api.audnex.us/books/B0OTHER001", "audnexus_other.json"},
	{"https://www.googleapis.com/books/v1/volumes", "google_books.json"},
	{"https://openlibrary.org/search.json", "openlibrary_search.json"},
	{"https://openlibrary.org/works/OL1W.json", "openlibrary_work.json"},
	{"https://itunes.apple.com/search", "itunes.json"},
}

func (n *slowNet) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, s := range slowSamples {
		if strings.HasPrefix(req.URL.String(), s.prefix) {
			time.Sleep(n.delay)
			body, err := os.ReadFile(filepath.Join("..", "metadata", "online", "testdata", s.file))
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
		}
	}
	return n.compareNet.RoundTrip(req)
}

// slowLookupMod gives the app a lookup service on slowNet and a job runner that is running and
// reports progress quickly.
func slowLookupMod(t *testing.T, sn *slowNet) func(*Options) {
	return func(o *Options) {
		r, err := jobs.New(o.Store, jobs.Options{PollEvery: 50 * time.Millisecond, ProgressEvery: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		o.Jobs = r
		svc := metadata.New(o.Library, r, slog.New(slog.NewTextHandler(io.Discard, nil)))
		sn.client = online.New(online.Options{Cache: metadata.NewOnlineCache(o.Store), CoverDir: t.TempDir(), Transport: sn})
		svc.SetOnline(sn.client, media.Tools{}, t.TempDir())
		if err := svc.Register(r); err != nil {
			t.Fatal(err)
		}
		o.Details = svc
		sn.runner = r
	}
}

// TestLookupProgressBrowser: play book A, look up book B with "Search more places", watch the log
// lines appear while the sites answer, then the matches, the collapsed log, and "Use n changes" -
// the audio keeps playing and the page is never reloaded. No horizontal overflow; with reduced
// motion the lines do not animate. Needs TENDSHELF_TEST_BROWSER.
func TestLookupProgressBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range []struct {
		name    string
		w, h    int
		mobile  bool
		reduced bool
	}{{"desktop-1440", 1440, 900, false, false}, {"mobile-390", 390, 844, true, false}, {"desktop-1440-reduced", 1440, 900, false, true}} {
		t.Run(vp.name, func(t *testing.T) {
			sn := &slowNet{delay: 300 * time.Millisecond}
			e := playerAppMods(t, nil, slowLookupMod(t, sn))
			if err := sn.runner.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sn.runner.Stop(context.Background()) })
			bookB := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: "The Test Match", Authors: []string{"Ann Author"}, DurationMs: 60_000, Files: 1})
			e.store(t, 100_000, "lookupdevice01", "Other device", 0, false)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, vp.reduced)
			tab.Navigate(e.URL + "/libraries")
			tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
			bmkeyPlay(t, tab, "[data-ts-toggle]")
			tab.Eval(`window.__tsMarker = 7`, nil)

			kept := func(stage string) {
				t.Helper()
				var marker float64
				tab.Eval(`window.__tsMarker || 0`, &marker)
				if marker != 7 {
					t.Errorf("the page was reloaded by %s", stage)
				}
			}
			// Go to book B's Details tab by a boosted link, as a click in the app would.
			tab.Eval(`(() => { const a = document.createElement("a"); a.href = "/books/`+bookB.ID+`/details"; a.id = "go-b"; document.querySelector("#main").append(a); htmx.process(a); a.click(); })()`, nil)
			tab.WaitFor(`document.querySelector(".ts-book__lookup") !== null`, 10*time.Second)
			kept("opening the Details tab")
			tab.Eval(`document.querySelector(".ts-book__lookup").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet form.ts-lookup") !== null`, 5*time.Second)
			tab.Eval(`document.querySelector('#sheet input[name="more"]').click()`, nil)
			tab.Eval(`document.querySelector('#sheet form.ts-lookup button[type="submit"]').click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet .ts-log__list") !== null`, 8*time.Second)

			// Lines appear while the sites answer: the count of finished lines grows, and the privacy line is there.
			seen := map[int]bool{}
			deadline := time.Now().Add(40 * time.Second)
			for time.Now().Before(deadline) {
				var n int
				tab.Eval(`document.querySelectorAll("#sheet .ts-log__line.is-done, #sheet .ts-log__line.is-none, #sheet .ts-log__line.is-failed").length`, &n)
				seen[n] = true
				var finished bool
				tab.Eval(`document.querySelector("#sheet .ts-compare-list") !== null`, &finished)
				if finished {
					break
				}
				time.Sleep(120 * time.Millisecond)
			}
			if len(seen) < 3 {
				t.Errorf("the log did not fill line by line: finished-line counts seen %v", seen)
			}
			tab.WaitFor(`document.querySelector("#sheet .ts-compare-list") !== null`, 5*time.Second)
			kept("the lookup")

			var m struct {
				Overflow  bool
				Collapsed bool
				Privacy   bool
				Lines     int
			}
			tab.Eval(`({Overflow: document.documentElement.scrollWidth > window.innerWidth, Collapsed: !!document.querySelector("#sheet details.ts-log-review") && !document.querySelector("#sheet details.ts-log-review").open,
			  Privacy: document.querySelector("#sheet .ts-log__note") !== null, Lines: document.querySelectorAll("#sheet .ts-log__line").length})`, &m)
			if m.Overflow || !m.Collapsed || !m.Privacy || m.Lines < 5 {
				t.Errorf("finished sheet: %+v", m)
			}
			// Open the review: the log shows the four places, and nothing overflows the sheet.
			tab.Eval(`document.querySelector("#sheet details.ts-log-review summary").click()`, nil)
			tab.Eval(`({Overflow: document.documentElement.scrollWidth > window.innerWidth, Sheet: document.querySelector("#sheet .ts-sheet").scrollWidth > document.querySelector("#sheet .ts-sheet").clientWidth})`, &struct{ Overflow, Sheet bool }{})
			var text string
			tab.Eval(`document.querySelector("#sheet details.ts-log-review").textContent`, &text)
			for _, want := range []string{"Audible catalogue", "Google Books", "Open Library", "Apple Books", "Only the title, author, ASIN and ISBN leave your server."} {
				if !strings.Contains(text, want) {
					t.Errorf("review lacks %q", want)
				}
			}

			// Motion: the lines slide in, unless the person asked for less.
			var anim string
			tab.Eval(`getComputedStyle(document.querySelector("#sheet .ts-log__line")).animationName`, &anim)
			if (vp.reduced && anim != "none") || (!vp.reduced && anim == "none") {
				t.Errorf("reduced=%v animation = %q", vp.reduced, anim)
			}

			// Pick a match and use it: no control reloads the page, the book A keeps playing.
			tab.Eval(`document.querySelector("#sheet .ts-compare-list a").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet table.ts-compare") !== null`, 5*time.Second)
			kept("picking a match")
			// The cover job is not registered in this app, so leave the cover out.
			tab.Eval(`(() => { const c = document.querySelector('#sheet input[name="use"][value="cover"]:checked'); if (c) c.click(); })()`, nil)
			tab.WaitFor(`document.querySelector('#sheet input[name="use"]:checked') !== null || document.querySelector('#sheet input[name="use"]:not(:disabled)') !== null`, 5*time.Second)
			tab.Eval(`(() => { const c = document.querySelector('#sheet input[name="use"]:not(:checked):not(:disabled)'); if (document.getElementById("compare-use").disabled && c) c.click(); })()`, nil)
			tab.WaitFor(`!document.getElementById("compare-use").disabled`, 5*time.Second)
			tab.Eval(`document.getElementById("compare-use").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet .ts-sheet") === null`, 5*time.Second)
			tab.WaitFor(`document.querySelector(".ts-book__applied") !== null`, 5*time.Second)
			var after struct {
				Marker  float64
				Playing bool
			}
			tab.Eval(`({Marker: window.__tsMarker || 0, Playing: window.tsPlayer.state().playing})`, &after)
			if after.Marker != 7 || !after.Playing {
				t.Errorf("after Use: marker %v, playing %v (the page was reloaded or the audio stopped)", after.Marker, after.Playing)
			}
		})
	}
}
