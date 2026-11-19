//go:build dev

package web

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// iosDefectOut is where the captures go: TENDSHELF_VISUAL_OUT when set, else the run's player folder.
func iosDefectOut(t *testing.T) string {
	if d := os.Getenv("TENDSHELF_VISUAL_OUT"); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	return playerOut(t)
}

// TestHomeBrowserQuestionToastCoverAndWhere covers three phone defects seen on Home: the "Continue there?"
// question squeezed beside its buttons, the cover-less hero showing its title underlined like a link, and a
// where-line that mixed "min" and "m".
func TestHomeBrowserQuestionToastCoverAndWhere(t *testing.T) {
	e := playerApp(t)
	out := iosDefectOut(t)
	streamExec(t, e.a, `DELETE FROM covers`) // the book has no cover (books.cover_id goes NULL)
	e.store(t, 10_000, "sweepfirstdev", "First device", time.Now().UnixMilli()-5000, false)
	for _, w := range []int{390, 768, 1440} {
		t.Run("question-"+strconv.Itoa(w), func(t *testing.T) {
			tab := playerOpen(t, chromium(t), e.URL, w, 800)
			tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
			playerClickSel(t, tab, "[data-ts-toggle]")
			tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 11500`, 8*time.Second)
			playerClickSel(t, tab, "[data-ts-toggle]")
			tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
			e.store(t, 80_000, "sweepotherdev", "Laptop test", time.Now().UnixMilli()+500, false)
			tab.Eval(`window.tsPlayer.debug.setPausedAt(Date.now() - 61000)`, nil)
			playerClickSel(t, tab, "[data-ts-toggle]")
			tab.WaitFor(`document.querySelector("#toasts .ts-toast button")`, 8*time.Second)
			time.Sleep(600 * time.Millisecond) // the toast's entrance
			tab.Screenshot(filepath.Join(out, "question-"+strconv.Itoa(w)+".png"))
			var m struct {
				ToastW, TextW, MinBtnH, MinBtnW float64
				Wrapped                         bool
			}
			tab.Eval(`(() => {
		  const toast = document.querySelector("#toasts .ts-toast--ask");
		  const text = toast.querySelector(".ts-toast__text").getBoundingClientRect();
		  const bs = [...toast.querySelectorAll("button")].map(b => b.getBoundingClientRect());
		  return {ToastW: toast.getBoundingClientRect().width, TextW: text.width,
		    MinBtnH: Math.min(...bs.map(b => b.height)), MinBtnW: Math.min(...bs.map(b => b.width)),
		    Wrapped: Math.min(...bs.map(b => b.top)) >= text.bottom - 1};
		})()`, &m)
			if m.MinBtnH < 44 || m.MinBtnW < 44 {
				t.Errorf("%d px: buttons are %.0f x %.0f, want at least 44", w, m.MinBtnW, m.MinBtnH)
			}
			if w == 390 && (!m.Wrapped || m.TextW < m.ToastW*0.7) {
				t.Errorf("390 px: the words are %.0f of %.0f wide, buttons below is %v; want wide words with the buttons below", m.TextW, m.ToastW, m.Wrapped)
			}
			tab.Close()
		})
	}

	// Home: the hero's cover has no picture file here, so the fallback shows the title, never as a link.
	t.Run("hero", func(t *testing.T) {
		tab := playerOpen(t, chromium(t), e.URL, 390, 844)
		tab.Navigate(e.URL + "/")
		tab.WaitFor(`document.querySelector(".ts-hero__cover")`, 8*time.Second)
		tab.Screenshot(filepath.Join(out, "home-hero-390.png"))
		var h struct {
			Decoration, Alt, Where string
		}
		tab.Eval(`(() => {
	  const a = document.querySelector(".ts-hero__cover");
	  const title = a.querySelector(".ts-cover__title");
	  const w = document.querySelector(".ts-hero__progress");
	  return {Decoration: getComputedStyle(title || a).textDecorationLine,
	    Alt: a.querySelector("img[alt]:not([alt=''])") ? "alt" : "", Where: w ? w.textContent : ""};
	})()`, &h)
		if h.Decoration != "none" || h.Alt != "" {
			t.Errorf("hero cover text decoration = %q, alt image %q; want none and none", h.Decoration, h.Alt)
		}
		t.Logf("where: %q", h.Where)
	})
}
