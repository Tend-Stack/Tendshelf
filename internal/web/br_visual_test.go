//go:build dev

package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// M5 RD4: the reader captured beside its boards at 390, 768, 1024 and 1440 px, in the four themes with
// `make visual-full` (the fast set is visualMatrix). Needs TENDSHELF_TEST_BROWSER.

// brServe serves the reader lab as one person, with the theme from ?theme=.
func brServe(t *testing.T, l *rdrLab, ck *http.Cookie) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// brBoards are the boards the captures are read against; they are copied beside the captures.
var brBoards = []string{"ReadPhone", "ReadTablet", "ReadDesktop", "ContentsPhone", "ContentsTablet", "ContentsDesktop", "MarksPhone", "MarksTablet", "MarksDesktop",
	"ReadSettingsPhone", "ReadSettingsTablet", "ReadSettingsDesktop", "RosaReadPhone", "ReaderBookPhone", "ReaderBookTablet", "ReaderBookDesktop"}

func brCopyBoards(t *testing.T, out string) {
	t.Helper()
	for _, name := range brBoards {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "reader", name+".png"))
		if err != nil {
			t.Errorf("board %s: %v", name, err)
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "br-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}
}

// brProbe reports sideways overflow and the theme.
const brProbe = `(() => ({
  over: document.documentElement.scrollWidth > innerWidth,
  theme: document.documentElement.dataset.theme || "",
}))()`

func TestBRVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	brCopyBoards(t, out)
	seedVisualIDs(t)
	l := brSeed(t)
	owner, admin, member := brServe(t, l, l.owner), brServe(t, l, l.admin), brServe(t, l, l.reader)
	sid := l.sids()[1]
	open := func(t *testing.T, tab *cdp.Tab) { brSheetOpen(t, tab, nil) }
	type state struct {
		name, server, path string
		prepare            func(t *testing.T, tab *cdp.Tab)
		ready              string // what to wait for after load
		live               bool   // capture without freezing animations (a toolbar or popup that is mid-entry when frozen)
	}
	reader := `document.querySelector("#reader.ts-reader--pages, #reader.ts-reader--scroll") !== null && document.fonts.status === "loaded"`
	states := []state{
		{"read", owner, brPath(l, "ch=2"), nil, reader, false},
		{"read-scroll", admin, brPath(l, "ch=2"), nil, reader, false},
		{"contents", owner, brPath(l, "ch=2", "sheet=contents"), open, reader, false},
		{"marks", owner, brPath(l, "ch=2", "sheet=marks"), open, reader, false},
		{"settings", owner, brPath(l, "ch=2", "sheet=reading"), open, reader, false},
		{"search", owner, brPath(l, "ch=2", "sheet=search"), func(t *testing.T, tab *cdp.Tab) {
			open(t, tab)
			tab.Eval(`document.getElementById("reader-q").value = "orchard"; document.getElementById("reader-q").form.requestSubmit()`, nil)
			tab.WaitFor(`document.querySelector("#reader-hits mark") !== null`, 8*time.Second)
		}, reader, false},
		{"footnote", owner, brPath(l, "ch=2"), func(t *testing.T, tab *cdp.Tab) {
			tab.Eval(`document.querySelector("a[data-note-href]").click()`, nil)
			tab.WaitFor(`document.querySelector("dialog.ts-readernote--note[open]") !== null`, 8*time.Second)
			time.Sleep(300 * time.Millisecond)
		}, reader, true},
		{"selection", owner, brPath(l, "ch=2"), func(t *testing.T, tab *cdp.Tab) {
			rnSelect(tab, sid, 2, 12)
			time.Sleep(300 * time.Millisecond) // the toolbar settles under the selection
			var shown bool
			tab.Eval(`(() => { const bar = document.getElementById("reader-marks"); const r = bar.getBoundingClientRect(); return !bar.hidden && r.width > 0 && r.bottom > 0 && r.top < innerHeight; })()`, &shown)
			if !shown {
				t.Error("the selection toolbar is not on screen")
			}
		}, reader, true},
		{"book", owner, "/books/" + l.book.ID, nil, `document.readyState === "complete"`, false},
		{"book-member", member, "/books/" + l.book.ID, nil, `document.readyState === "complete"`, false},
	}
	for _, s := range states {
		for _, sh := range visualMatrix(true) {
			s, sh := s, sh
			t.Run(fmt.Sprintf("%s-%s-%d", s.name, sh.Theme, sh.Width), func(t *testing.T) {
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				sep := "?"
				if strings.Contains(s.path, "?") {
					sep = "&"
				}
				tab.Navigate(s.server + s.path + sep + "theme=" + sh.Theme)
				tab.WaitFor(s.ready, 10*time.Second)
				if s.prepare != nil {
					s.prepare(t, tab)
				}
				time.Sleep(150 * time.Millisecond)
				var m struct {
					Over  bool
					Theme string
				}
				tab.Eval(brProbe, &m)
				switch {
				case m.Over:
					t.Errorf("horizontal overflow")
				case m.Theme != sh.Theme:
					t.Errorf("data-theme = %q, want %q", m.Theme, sh.Theme)
				}
				file := filepath.Join(out, fmt.Sprintf("br-%s-%s-%d.png", s.name, sh.Theme, sh.Width))
				if !s.live {
					tab.Screenshot(file)
					return
				}
				var shot struct {
					Data string `json:"data"`
				}
				if err := tab.Call("Page.captureScreenshot", map[string]any{"format": "png"}, &shot); err != nil {
					t.Fatal(err)
				}
				png, err := base64.StdEncoding.DecodeString(shot.Data)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, png, 0o644); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// TestBRLayoutTime lays out a long chapter part with the CPU throttled 4x and turns pages: the part of
// about 40,000 UTF-16 units must lay out in under 300 ms and a turn must take under 16 ms at the 95th
// percentile (the work on the main thread, with the layout it forces). TENDSHELF_TEST_PERF=1 and
// TENDSHELF_TEST_BROWSER; the numbers go to .local/tests/perf/<run>/reader-layout.json (make perf).
func TestBRLayoutTime(t *testing.T) {
	sweepPerfOn(t)
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	units := pgPartUnits(l, 0)
	if units < 36000 || units > 44000 {
		t.Fatalf("the part holds %d units, want about 40,000", units)
	}
	type row struct {
		Viewport              string
		LayoutMs, TurnP95Ms   float64
		TurnMaxMs, TurnMedian float64
		Turns                 int
	}
	var rows []row
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			if err := tab.Call("Emulation.setCPUThrottlingRate", map[string]any{"rate": 4}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Navigate(srv.URL + l.readPath("ch=2"))
			tab.WaitFor(`window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages") !== null && performance.getEntriesByName("ts-reader-lay").length > 0`, 20*time.Second)
			var lay float64
			tab.Eval(`Math.max(...performance.getEntriesByName("ts-reader-lay").map(e => e.duration))`, &lay)
			var turned struct {
				Times []float64
				Left  float64
			}
			tab.Eval(`(() => {
				const out = [];
				const box = document.querySelector("section[data-part]");
				for (let i = 0; i < 40; i++) {
					const start = performance.now();
					window.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
					void box.scrollLeft; // the turn's layout is part of its cost
					out.push(performance.now() - start);
				}
				return { times: out, left: box.scrollLeft };
			})()`, &turned)
			turns := turned.Times
			if turned.Left <= 0 {
				t.Fatal("the turns did not move the part")
			}
			sorted := append([]float64(nil), turns...)
			sort.Float64s(sorted)
			p95 := sorted[int(float64(len(sorted))*0.95)-1]
			r := row{Viewport: vp.name, LayoutMs: lay, TurnP95Ms: p95, TurnMaxMs: sorted[len(sorted)-1], TurnMedian: sorted[len(sorted)/2], Turns: len(turns)}
			rows = append(rows, r)
			t.Logf("%s: %d units laid out in %.1f ms at 4x; %d turns, median %.2f ms, p95 %.2f ms, max %.2f ms", vp.name, units, lay, r.Turns, r.TurnMedian, p95, r.TurnMaxMs)
			if lay > 300 {
				t.Errorf("laying out the part took %.0f ms, want 300 ms or less", lay)
			}
			if p95 > 16 {
				t.Errorf("a turn took %.1f ms at the 95th percentile, want 16 ms or less", p95)
			}
		})
	}
	dir := filepath.Join("..", "..", ".local", "tests", "perf", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"units": units, "cpuThrottle": 4, "rows": rows}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "reader-layout.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("numbers: %s", filepath.Join(dir, "reader-layout.json"))
}
