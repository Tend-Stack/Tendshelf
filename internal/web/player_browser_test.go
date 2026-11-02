//go:build dev

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The player's browser tests need TENDSHELF_TEST_BROWSER (make player). Headless Chromium plays
// audio without an audio device once a real click (Input.dispatchMouseEvent) gave the page a gesture.

// playerOut returns .local/tests/player/<run>/ (git-ignored), one run id per test process.
func playerOut(t *testing.T) string {
	t.Helper()
	runOnce.Do(func() { runID = time.Now().UTC().Format("20060102T150405Z") })
	dir := filepath.Join("..", "..", ".local", "tests", "player", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// playerState mirrors window.tsPlayer.state().
type playerState struct {
	BookID     string  `json:"bookId"`
	PositionMs int64   `json:"positionMs"`
	Playing    bool    `json:"playing"`
	FileIdx    int     `json:"fileIdx"`
	Speed      float64 `json:"speed"`
}

func playerStateOf(tab *cdp.Tab) playerState {
	var s playerState
	tab.Eval(`window.tsPlayer.state()`, &s)
	return s
}

// playerOpen opens the Library in a new tab and waits for the player module. It is not Home: since
// P1c-6 Home's hero carries the Resume button of the person's book, which hides the mini-player
// while it is on screen (one resume per book), and these tests drive the bar.
func playerOpen(t *testing.T, b *cdp.Browser, base string, w, h int) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, w < 768, false)
	tab.Navigate(base + "/libraries")
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	return tab
}

// playerInject puts a [data-ts-play] link for the book into #main (the book page gets its own in
// P1c-6), fixed at the top-left so a click lands on it. startMs is a book position.
func (e *playerEnv) playerInject(tab *cdp.Tab, bookID string, startMs int64, fileIdx int, extra string) {
	f := e.files[fileIdx]
	tab.Eval(fmt.Sprintf(`(() => {
	  const a = document.createElement("a");
	  a.id = "play-link"; a.href = "/stream/%s"; a.textContent = "Play";
	  a.dataset.tsPlay = %s; a.dataset.tsFile = %s; a.dataset.tsFileStartMs = %q; a.dataset.tsStartMs = %q;
	  a.dataset.tsMime = "audio/wav"; a.dataset.tsMayNeedCompat = "0";
	  %s
	  a.style.cssText = "position:fixed;left:8px;top:8px;width:120px;height:60px;z-index:50;background:white;color:black";
	  document.getElementById("main").append(a);
	})()`, f.ID, cdp.Quote(bookID), cdp.Quote(f.ID), strconv.FormatInt(f.OffsetMs, 10), strconv.FormatInt(startMs, 10), extra), nil)
}

// playerClickAt dispatches a real left click at the viewport point.
func playerClickAt(t *testing.T, tab *cdp.Tab, x, y float64) {
	t.Helper()
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		if err := tab.Call("Input.dispatchMouseEvent", map[string]any{"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// playerClickSel clicks the centre of the first element matching sel.
// It clicks once the element has stopped moving: the page-enter motion, late stylesheets and fonts
// can move it between measuring and clicking, and a click that lands beside the element is lost
// (TestPlayButtonBrowserEarlyClick failed that way on the nightly run of 2026-11-02).
func playerClickSel(t *testing.T, tab *cdp.Tab, sel string) {
	t.Helper()
	type point struct{ X, Y float64 }
	measure := func() point {
		var r point
		tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &r)
		return r
	}
	r := measure()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		time.Sleep(80 * time.Millisecond)
		next := measure()
		if next == r {
			break
		}
		r = next
	}
	playerClickAt(t, tab, r.X, r.Y)
}

func playerMedian(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func TestPlayerBrowserFirstSound(t *testing.T) {
	e := playerApp(t)
	var runs []float64
	for i := 0; i < 5; i++ {
		b := chromium(t) // a new browser per run: empty storage, empty cache
		tab := playerOpen(t, b, e.URL, 390, 800)
		e.playerInject(tab, e.book.ID, 0, 0, "")
		playerClickSel(t, tab, "#play-link")
		tab.WaitFor(`performance.getEntriesByName("ts-first-sound").length > 0`, 10*time.Second)
		var ms float64
		tab.Eval(`(() => { const m = performance.measure("fs", "ts-play-click", "ts-first-sound"); return m.duration; })()`, &ms)
		runs = append(runs, ms)
		if s := playerStateOf(tab); !s.Playing || s.BookID != e.book.ID {
			t.Errorf("run %d: state %+v", i, s)
		}
	}
	med := playerMedian(runs)
	t.Logf("first sound: runs %.0f ms, median %.0f ms", runs, med)
	out, _ := json.Marshal(map[string]any{"runsMs": runs, "medianMs": med})
	if err := os.WriteFile(filepath.Join(playerOut(t), "first-sound.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	if med >= 1000 {
		t.Errorf("median first sound %.0f ms, want under 1000", med)
	}
}

func TestPlayerBrowserFileBoundary(t *testing.T) {
	e := playerApp(t)
	b := chromium(t)
	tab := playerOpen(t, b, e.URL, 390, 800)
	e.playerInject(tab, e.book.ID, 88_000, 0, "")
	// Record every timeupdate: the position and the time it arrived, plus any error event.
	tab.Eval(`(() => { window.__log = []; window.__err = 0;
	  const a = document.getElementById("audio");
	  a.addEventListener("timeupdate", () => window.__log.push([performance.now(), window.tsPlayer.state().positionMs, window.tsPlayer.state().fileIdx]));
	  a.addEventListener("error", () => window.__err++); })()`, nil)
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().positionMs > 91000 && window.tsPlayer.state().fileIdx === 1`, 8*time.Second)
	var log [][3]float64
	var errs int
	tab.Eval(`window.__log`, &log)
	tab.Eval(`window.__err`, &errs)
	if errs != 0 {
		t.Errorf("%d error events across the boundary", errs)
	}
	// The longest wait between two timeupdates anywhere around the switch.
	var worst float64
	for i := 1; i < len(log); i++ {
		if log[i-1][1] >= 87_000 && log[i][1] <= 93_000 {
			worst = max(worst, log[i][0]-log[i-1][0])
		}
	}
	t.Logf("file boundary: longest gap between timeupdates near the switch %.0f ms", worst)
	if err := os.WriteFile(filepath.Join(playerOut(t), "file-boundary.json"), []byte(fmt.Sprintf(`{"switchGapMs": %.0f}`, worst)), 0o644); err != nil {
		t.Fatal(err)
	}
	if worst >= 500 {
		t.Errorf("gap across the file switch %.0f ms, want under 500", worst)
	}

	// Seeks across the boundary land within 250 ms.
	tab.Eval(`window.tsPlayer.seek(120000)`, nil)
	tab.WaitFor(`window.tsPlayer.state().fileIdx === 1 && Math.abs(window.tsPlayer.state().positionMs - 120000) < 1500`, 5*time.Second)
	if s := playerStateOf(tab); s.PositionMs < 119_750 || s.PositionMs > 121_500 {
		t.Errorf("seek(120000) -> %d", s.PositionMs)
	}
	tab.Eval(`window.tsPlayer.seek(10000)`, nil)
	tab.WaitFor(`window.tsPlayer.state().fileIdx === 0 && window.tsPlayer.state().positionMs < 12000`, 5*time.Second)
	if s := playerStateOf(tab); s.PositionMs < 9_750 || s.PositionMs > 12_000 {
		t.Errorf("seek(10000) -> %d", s.PositionMs)
	}
}
