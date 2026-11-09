//go:build dev

package web

import (
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The page turn in Chromium (decision 0036, package B). Needs TENDSHELF_TEST_BROWSER. Unlike the other page tests these
// leave motion on: the flip is the thing under test. Frames of a paused flight go to TENDSHELF_FLIP_OUT when it is set.

// flipSeed is a book of one chapter that mixes what a page may hold: headings, quotes, a picture, a highlight target and a note.
func flipSeed(t *testing.T) *rdrLab {
	t.Helper()
	p := func(tag string, n int) string {
		var sb strings.Builder
		for _, s := range pgProse(tag, n) {
			sb.WriteString("<p>" + s + "</p>")
		}
		return sb.String()
	}
	raw := p("a", 3) + `<h3>The long road</h3>` + p("b", 2) + `<blockquote><p>` + pgProse("q", 1)[0] + `</p></blockquote>` + p("c", 4) +
		`<img src="pic.png" alt="Lamp"/>` + p("d", 4) + `<p>Marked<a epub:type="noteref" href="ch3.xhtml#n1">1</a> text. ` + pgProse("e", 1)[0] + `</p>` + p("f", 24)
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Pager"}},
		{Title: "One", EpubType: "chapter", Raw: raw},
		{Title: "Two", EpubType: "chapter", Raw: p("g", 12) + `<aside epub:type="footnote" id="n1"><p>Footnote words.</p></aside>`},
	}
	l := pgSeedEPUB(t, chs, func(o *ebooktest.EPUBOptions) {
		o.ManifestXML = `<item id="p" href="pic.png" media-type="image/png"/>`
		o.Extra = []ebooktest.Entry{{Name: "OEBPS/pic.png", Data: rsPNG(240, 160)}}
	})
	pgFlow(t, l, "pages")
	return l
}

// flipLong is a plain book with many pages, for the tests that turn a lot.
func flipLong(t *testing.T) *rdrLab {
	t.Helper()
	l := pgSeed(t, 2, 160)
	pgFlow(t, l, "pages")
	return l
}

// flipOpen opens the reader with motion on, and waits for the flip module to be here.
func flipOpen(t *testing.T, b *cdp.Browser, srv *httptest.Server, l *rdrLab, w, h int, mobile bool, query string) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, mobile, false)
	tab.Navigate(srv.URL + l.readPath(query))
	tab.WaitFor(`window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages") !== null`, 8*time.Second)
	tab.WaitFor(`performance.getEntriesByType("resource").some(e => e.name.includes("reader-flip"))`, 5*time.Second)
	time.Sleep(150 * time.Millisecond)
	return tab
}

// flipPause stops every animation of a flight at the given part of its length, and returns how many there were.
func flipPause(tab *cdp.Tab, fraction float64) int {
	var n int
	tab.Eval(fmt.Sprintf(`(() => { const all = document.getAnimations().filter(a => a.effect.target && a.effect.target.closest(".ts-flip"));
		for (const a of all) { a.pause(); a.currentTime = %f * a.effect.getTiming().duration; } return all.length; })()`, fraction), &n)
	return n
}

func flipShot(t *testing.T, tab *cdp.Tab, path string) {
	t.Helper()
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
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
}

func flipWaitStage(tab *cdp.Tab) {
	tab.WaitFor(`document.querySelector(".ts-flip:not(.ts-flip--wait)") !== null`, 3*time.Second)
}

// flipWaitGone waits until no stage is left.
func flipWaitGone(tab *cdp.Tab) {
	tab.WaitFor(`document.querySelector(".ts-flip") === null`, 3*time.Second)
}

func flipTheme(tab *cdp.Tab, theme string) {
	tab.Eval(fmt.Sprintf(`document.documentElement.dataset.theme = %q`, theme), nil)
	time.Sleep(100 * time.Millisecond)
}

func flipKey(t *testing.T, tab *cdp.Tab, forward bool) {
	if forward {
		pgNext(t, tab)
	} else {
		pgPrev(t, tab)
	}
}

// TestFlipFrames takes frames of paused flights, forward and back, for looking at. It checks only that a stage exists.
func TestFlipFrames(t *testing.T) {
	out := os.Getenv("TENDSHELF_FLIP_OUT")
	b := chromium(t)
	l := flipSeed(t)
	srv := pgServe(t, l)
	if out != "" {
		_ = os.MkdirAll(out, 0o755)
	}
	for _, vp := range pgViewports {
		for _, theme := range []string{"dark", "light"} {
			t.Run(vp.name+"-"+theme, func(t *testing.T) {
				tab := flipOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
				flipTheme(tab, theme)
				pgNext(t, tab)
				pgNext(t, tab)
				time.Sleep(500 * time.Millisecond)
				for _, back := range []bool{false, true} {
					for _, f := range []float64{0.25, 0.5, 0.75} {
						time.Sleep(200 * time.Millisecond)
						flipKey(t, tab, !back)
						flipWaitStage(tab)
						if flipPause(tab, f) == 0 {
							t.Fatal("no animation in the stage")
						}
						time.Sleep(120 * time.Millisecond)
						if out != "" {
							name := "forward"
							if back {
								name = "back"
							}
							flipShot(t, tab, filepath.Join(out, fmt.Sprintf("%s-%s-%s-%d.png", vp.name, theme, name, int(f*100))))
						}
						tab.Eval(`document.getAnimations().forEach(a => a.finish())`, nil)
						flipWaitGone(tab)
						if back { // forward again so the next frame has a page to go back to
							pgNext(t, tab)
							time.Sleep(600 * time.Millisecond)
						}
					}
				}
			})
		}
	}
}

// flipAudit puts a leaf in the air for the accessibility audit: it asks for motion, turns a page and holds the flight half way.
func flipAudit(t *testing.T, tab *cdp.Tab, _ *a11yEnv) {
	t.Helper()
	if err := tab.Call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "no-preference"}}}, nil); err != nil {
		t.Fatal(err)
	}
	tab.Eval(`document.dispatchEvent(new Event("ts-reader-look"))`, nil)
	tab.WaitFor(`performance.getEntriesByType("resource").some(e => e.name.includes("reader-flip"))`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	pgNext(t, tab)
	flipWaitStage(tab)
	flipPause(tab, 0.5)
}
