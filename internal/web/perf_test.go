package web

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The performance measurement behind the exit criterion "the Library page is interactive in under
// 300 ms on a mid-range phone". `make perf` runs it. Both tests skip without TENDSHELF_TEST_PERF=1;
// the browser one also needs TENDSHELF_TEST_BROWSER.
//
// The fixture is 2,000 books (40 authors, 12 series, 10 genres) that share one real cover file with
// its 160, 320 and 640 px thumbnails. The page loads at 390 x 844 as a phone with the CPU throttled
// 4x (the usual "mid-tier phone" setting in browser tools; a GUESS that 4x on this machine
// approximates one, so the report names the CPU).

const (
	sweepPerfBooks   = 2000
	sweepPerfLoads   = 5
	sweepPerfColdRun = 3
	sweepPerfBudget  = 300.0 // ms, median
)

// sweepPerfRate is the CPU throttle: 4 unless TENDSHELF_PERF_RATE says otherwise (to see how the
// page scales on a slower phone; the budget is judged at 4).
var sweepPerfRate = func() int {
	if n, err := strconv.Atoi(os.Getenv("TENDSHELF_PERF_RATE")); err == nil && n >= 1 {
		return n
	}
	return 4
}()

func sweepPerfOn(t testing.TB) {
	t.Helper()
	if os.Getenv("TENDSHELF_TEST_PERF") != "1" {
		t.Skip("TENDSHELF_TEST_PERF=1 not set")
	}
}

// sweepPerfCover writes one JPEG cover and its three thumbnails where the cover route looks.
func sweepPerfCover(t testing.TB, dir string) (sha, rel string) {
	t.Helper()
	write := func(path string, px int) {
		img := image.NewRGBA(image.Rect(0, 0, px, px))
		for y := 0; y < px; y++ {
			for x := 0; x < px; x++ {
				img.Set(x, y, color.RGBA{uint8(40 + x*120/px), uint8(70 + y*100/px), 110, 255})
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
			t.Fatal(err)
		}
	}
	h := sha256.Sum256([]byte("perf-cover"))
	sha = hex.EncodeToString(h[:])
	rel = filepath.Join("covers", sha+".jpg")
	write(filepath.Join(dir, rel), 640)
	for _, box := range mediajobs.ThumbSizes {
		write(mediajobs.ThumbPath(dir, sha, box), box)
	}
	return sha, rel
}

// sweepPerfApp seeds the 2,000-book fixture and returns the app with its owner signed in.
func sweepPerfApp(t testing.TB, tt *testing.T) *testApp {
	t.Helper()
	a := newTestApp(tt)
	a.owner(tt)
	sha, rel := sweepPerfCover(t, a.dir)
	_, f := librarytest.NewLibrary(tt, a.lib, "Audiobooks", "/perf")
	genres := []string{"Mystery", "Literary", "Fantasy", "Romance", "Thriller", "Travel", "History", "Science", "Drama", "Humour"}
	var books []library.Book
	for i := 0; i < sweepPerfBooks; i++ {
		spec := librarytest.BookSpec{
			Title: fmt.Sprintf("Book %04d of the Archive", i), Authors: []string{fmt.Sprintf("Author %02d", i%40)},
			Narrators: []string{fmt.Sprintf("Narrator %02d", i%25)}, Genres: []string{genres[i%len(genres)], genres[(i/3)%len(genres)]},
			DurationMs: int64(1+(i*7)%30) * 3_000_000, AddedAt: int64(i+1) * 1000,
		}
		if i%6 != 5 {
			spec.Series, spec.Sequence = fmt.Sprintf("Series %02d", i%12), fmt.Sprint(i/12+1)
		}
		books = append(books, librarytest.AddBook(tt, a.lib, f, spec))
	}
	// One transaction gives every book a cover row over the shared file.
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		for _, b := range books {
			c := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/jpeg", Width: 640, Height: 640, SHA256: sha, Path: rel, Tint: "#1f4a52"}
			if err := tx.PutCover(&c); err != nil {
				return err
			}
			if err := tx.SetBookCover(b.ID, c.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

func sweepCPUModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return "unknown"
}

type sweepLoad struct {
	InteractiveMs float64 `json:"interactiveMs"`
	ReadyMs       float64 `json:"tsReadyMs"`
	DCLMs         float64 `json:"domContentLoadedMs"`
	ResponseEndMs float64 `json:"responseEndMs"`
	HTMLBytes     int64   `json:"htmlEncodedBytes"`
	ScrollPageMs  float64 `json:"firstScrollPageMs"`
}

const sweepNavTimingJS = `(() => {
  const n = performance.getEntriesByType("navigation")[0];
  const m = performance.getEntriesByName("ts-ready")[0];
  return {ready: m ? m.startTime : 0, dcl: n.domContentLoadedEventEnd, responseEnd: n.responseEnd, size: n.encodedBodySize};
})()`

// sweepScrollJS scrolls the sentinel into view and resolves with the milliseconds until the first
// infinite-scroll page has landed (a 49th card exists).
const sweepScrollJS = `new Promise((resolve) => {
  const grid = document.getElementById("library-grid");
  const t0 = performance.now();
  const check = () => { if (grid.querySelectorAll(".ts-card").length > 48) { resolve(performance.now() - t0); return true; } return false; };
  new MutationObserver((_, obs) => { if (check()) obs.disconnect(); }).observe(grid, {childList: true, subtree: true});
  document.querySelector(".ts-more").scrollIntoView({block: "center"});
  setTimeout(() => resolve(-1), 15000);
})`

func sweepMedian(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func TestPerfLibraryPage(t *testing.T) {
	sweepPerfOn(t)
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	a := sweepPerfApp(t, t)
	srv := httptest.NewServer(a.signedIn(t))
	t.Cleanup(srv.Close)
	b := cdp.Start(t, chrome)

	measure := func(cold bool) sweepLoad {
		tab := b.NewTab(390, 844, true, false)
		if err := tab.Call("Emulation.setCPUThrottlingRate", map[string]any{"rate": sweepPerfRate}, nil); err != nil {
			t.Fatal(err)
		}
		if cold {
			if err := tab.Call("Network.enable", nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := tab.Call("Network.setCacheDisabled", map[string]any{"cacheDisabled": true}, nil); err != nil {
				t.Fatal(err)
			}
		}
		tab.Navigate(srv.URL + "/libraries")
		tab.WaitFor(`performance.getEntriesByName("ts-ready").length > 0`, 10*time.Second)
		var m struct {
			Ready, DCL, ResponseEnd float64
			Size                    int64
		}
		tab.Eval(sweepNavTimingJS, &m)
		var scroll float64
		tab.Eval(sweepScrollJS, &scroll)
		return sweepLoad{InteractiveMs: max(m.Ready, m.DCL), ReadyMs: m.Ready, DCLMs: m.DCL, ResponseEndMs: m.ResponseEnd, HTMLBytes: m.Size, ScrollPageMs: scroll}
	}

	measure(false) // the warm-up: the server, the browser caches and the font files
	var runs []sweepLoad
	var inter []float64
	for i := 0; i < sweepPerfLoads; i++ {
		l := measure(false)
		runs = append(runs, l)
		inter = append(inter, l.InteractiveMs)
		if l.ScrollPageMs < 0 {
			t.Errorf("load %d: the first infinite-scroll page never arrived", i+1)
		}
	}
	var cold []sweepLoad
	for i := 0; i < sweepPerfColdRun; i++ {
		cold = append(cold, measure(true))
	}
	med := sweepMedian(inter)

	report := map[string]any{
		"cpu": sweepCPUModel(), "throttle": sweepPerfRate, "viewport": "390x844", "books": sweepPerfBooks,
		"budgetMs": sweepPerfBudget, "medianInteractiveMs": med, "pass": med <= sweepPerfBudget, "loads": runs, "coldLoadsNoCache": cold,
	}
	dir := filepath.Join("..", "..", ".local", "tests", "perf", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "library.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("cpu: %s; throttle %dx; %d books; json in %s", sweepCPUModel(), sweepPerfRate, sweepPerfBooks, dir)
	for i, l := range runs {
		t.Logf("load %d: interactive %.0f ms (ts-ready %.0f, DOMContentLoaded %.0f), responseEnd %.0f ms, html %d bytes, first scroll page %.0f ms",
			i+1, l.InteractiveMs, l.ReadyMs, l.DCLMs, l.ResponseEndMs, l.HTMLBytes, l.ScrollPageMs)
	}
	for i, l := range cold {
		t.Logf("cold load %d (cache disabled): interactive %.0f ms, responseEnd %.0f ms", i+1, l.InteractiveMs, l.ResponseEndMs)
	}
	t.Logf("median interactive %.0f ms, budget %.0f ms", med, sweepPerfBudget)
	if med > sweepPerfBudget {
		t.Errorf("median interactive %.0f ms is over the %.0f ms budget (do not add indexes here: report the numbers)", med, sweepPerfBudget)
	}
}

// TestPerfLibraryPageHandler times the Library page handler alone (no browser) over the same
// fixture, with testing.Benchmark, and logs ns/op. It has no threshold. (A Benchmark function cannot
// build the app, which needs a *testing.T, so the benchmark runs from inside this test.)
func TestPerfLibraryPageHandler(t *testing.T) {
	sweepPerfOn(t)
	a := sweepPerfApp(t, t)
	for _, path := range []string{"/libraries", "/libraries?length=short&title=book", "/hx/libraries/items?cursor=" + sweepFirstCursor(t, a)} {
		res := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				rec := a.do(http.MethodGet, path, nil, nil, a.session)
				if rec.Code != http.StatusOK {
					b.Fatalf("%s: %d", path, rec.Code)
				}
			}
		})
		t.Logf("%-60s %8d ns/op (%d runs)", path[:min(len(path), 60)], res.NsPerOp(), res.N)
	}
}

func sweepFirstCursor(t testing.TB, a *testApp) string {
	t.Helper()
	p, err := a.lib.ListBooks(context.Background(), library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}, library.ListQuery{Limit: 48})
	if err != nil || p.Next == "" {
		t.Fatalf("first page: %v %q", err, p.Next)
	}
	return p.Next
}
