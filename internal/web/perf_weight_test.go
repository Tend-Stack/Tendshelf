package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestPageWeight measures what a cold load of each page sends: bytes by type (the encoded body, as it
// crossed the wire) and the number of requests, at the four audit widths, with the HTTP cache off. The
// numbers are deterministic, so the test needs only the browser (TENDSHELF_TEST_BROWSER), not
// TENDSHELF_TEST_PERF. `make page-weight` runs it; the JSON goes to .local/tests/perf/<run>/weight.json.
//
// HTML is measured as served: dynamic HTML is not compressed today (only static files and /sw.js carry
// Content-Encoding), so htmlCompressed in the JSON says what gzip would save. Compressing HTML is not
// done here.

// weightBudget is the most one page may send, whatever the width. Requests counts the document too.
type weightBudget struct {
	HTML, CSS, JS, Font, Img int64
	Requests                 int
}

// weightBudgets are set from measurement on 2026-11-04: the maximum over three runs and four widths
// of each number, times 1.15, rounded up to the next KiB; requests are the maximum plus 2. Never raise
// one without the operator.
var weightBudgets = map[string]weightBudget{
	"home":        {HTML: 68608, CSS: 297984, JS: 194560, Font: 1075200, Img: 38912, Requests: 61}, // Img raised for the row of three covers (decision 0021, PR3): 33761 B measured x 1.15, next KiB
	"library":     {HTML: 87040, CSS: 297984, JS: 194560, Font: 791552, Img: 95232, Requests: 93},
	"book":        {HTML: 55296, CSS: 297984, JS: 194560, Font: 791552, Img: 29696, Requests: 55},
	"listening":   {HTML: 164864, CSS: 297984, JS: 194560, Font: 791552, Img: 34816, Requests: 62},
	"search":      {HTML: 59392, CSS: 297984, JS: 194560, Font: 791552, Img: 39936, Requests: 65},
	"profile":     {HTML: 64512, CSS: 328704, JS: 197632, Font: 791552, Img: 57344, Requests: 62}, // decision 0021, set the same way from the first measurement (2026-11-05)
	"settings":    {HTML: 49152, CSS: 297984, JS: 194560, Font: 791552, Img: 1024, Requests: 45},
	"reader":      {HTML: 52224, CSS: 321536, JS: 234496, Font: 1075200, Img: 1024, Requests: 56}, // M5 RD4, set the same way from the first measurement (2026-11-05)
	"admin-users": {HTML: 47104, CSS: 297984, JS: 194560, Font: 791552, Img: 1024, Requests: 45},
}

// weightProbeJS waits until ts-ready exists and no new resource entry has appeared for 500 ms (10 s at
// most), then sums encodedBodySize by type. Type comes from the file extension, then from the
// initiator (img). The navigation entry is the HTML.
const weightProbeJS = `new Promise((resolve) => {
  const start = performance.now();
  let count = -1;
  let stableSince = performance.now();
  const tick = () => {
    const n = performance.getEntriesByType("resource").length;
    const ready = performance.getEntriesByName("ts-ready").length > 0;
    if (n !== count) { count = n; stableSince = performance.now(); }
    if ((ready && performance.now() - stableSince >= 500) || performance.now() - start > 10000) {
      const sum = {html: performance.getEntriesByType("navigation")[0].encodedBodySize, css: 0, js: 0, font: 0, img: 0, other: 0, requests: 1 + n};
      for (const e of performance.getEntriesByType("resource")) {
        const path = new URL(e.name).pathname;
        let kind = "other";
        if (/\.css$/.test(path)) kind = "css";
        else if (/\.(js|mjs)$/.test(path)) kind = "js";
        else if (/\.(woff2?|ttf|otf)$/.test(path)) kind = "font";
        else if (e.initiatorType === "img" || /\.(jpe?g|png|webp|svg|gif|avif)$/.test(path)) kind = "img";
        sum[kind] += e.encodedBodySize;
      }
      resolve(sum);
      return;
    }
    setTimeout(tick, 50);
  };
  tick();
})`

type weightSample struct {
	HTML, CSS, JS, Font, Img, Other int64
	Requests                        int
}

func TestPageWeight(t *testing.T) {
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	a, bookID := sweepPerfPagesApp(t)
	h := a.signedIn(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	b := cdp.Start(t, chrome)

	// M5 RD4: the reader, on its own small app (a text book with one synthetic EPUB), served as its owner.
	rl := rdrSeed(t)
	rsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(rl.owner)
		rl.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(rsrv.Close)
	pages := append(sweepPerfPages(bookID), sweepPerfPage{"settings", "/me/settings"}, sweepPerfPage{"profile", "/me"}, sweepPerfPage{"admin-users", "/admin/users"}, sweepPerfPage{"reader", rl.readPath("")})
	report := map[string]any{}
	for _, pg := range pages {
		max := weightSample{}
		perWidth := map[string]weightSample{}
		for _, w := range sweepPerfWidths {
			tab := b.NewTab(w.W, w.H, w.Mobile, true)
			if err := tab.Call("Network.enable", nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := tab.Call("Network.setCacheDisabled", map[string]any{"cacheDisabled": true}, nil); err != nil {
				t.Fatal(err)
			}
			base := srv.URL
			if pg.Name == "reader" {
				base = rsrv.URL
			}
			tab.Navigate(base + pg.Path)
			var s weightSample
			tab.Eval(weightProbeJS, &s)
			tab.Close()
			perWidth[fmt.Sprint(w.W)] = s
			t.Logf("%-12s %4d px: html %6d css %6d js %6d font %6d img %6d other %d requests %d", pg.Name, w.W, s.HTML, s.CSS, s.JS, s.Font, s.Img, s.Other, s.Requests)
			max.HTML, max.CSS, max.JS = maxI64(max.HTML, s.HTML), maxI64(max.CSS, s.CSS), maxI64(max.JS, s.JS)
			max.Font, max.Img = maxI64(max.Font, s.Font), maxI64(max.Img, s.Img)
			max.Requests = max2(max.Requests, s.Requests)
		}
		report[pg.Name] = map[string]any{"widths": perWidth, "max": max, "htmlCompressed": weightGzipSizeOf(t, pg.Name == "reader", h, a, rl, pg.Path)}
		t.Logf("%-12s max: html %d css %d js %d font %d img %d requests %d", pg.Name, max.HTML, max.CSS, max.JS, max.Font, max.Img, max.Requests)

		bud, ok := weightBudgets[pg.Name]
		if !ok {
			t.Errorf("%s: no weight budget", pg.Name)
			continue
		}
		for _, c := range []struct {
			what      string
			got, want int64
		}{{"html", max.HTML, bud.HTML}, {"css", max.CSS, bud.CSS}, {"js", max.JS, bud.JS}, {"font", max.Font, bud.Font}, {"img", max.Img, bud.Img}, {"requests", int64(max.Requests), int64(bud.Requests)}} {
			if c.got > c.want {
				t.Errorf("%s: %s is %d, over the budget of %d", pg.Name, c.what, c.got, c.want)
			}
		}
	}

	dir := filepath.Join("..", "..", ".local", "tests", "perf", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"books": sweepPerfBooks, "budgets": weightBudgets, "pages": report}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "weight.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("json in %s", dir)
}

// weightGzipSize is the size the page's HTML would have if gzip were applied to it (reported only).
// weightGzipSizeOf is weightGzipSize for the main app, or for the reader's own app when reader is true.
func weightGzipSizeOf(t *testing.T, reader bool, h http.Handler, a *testApp, rl *rdrLab, path string) int {
	t.Helper()
	if !reader {
		return weightGzipSize(t, h, a, path)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(rl.get(rl.owner, path, false).Body.Bytes())
	zw.Close()
	return buf.Len()
}

func weightGzipSize(t *testing.T, h http.Handler, a *testApp, path string) int {
	t.Helper()
	rec := a.do(http.MethodGet, path, nil, nil, a.owner(t))
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(rec.Body.Bytes())
	zw.Close()
	return buf.Len()
}

func maxI64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}

func max2(a, b int) int {
	if b > a {
		return b
	}
	return a
}
