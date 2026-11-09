//go:build dev

package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The skim bar in Chromium (G4). Needs TENDSHELF_TEST_BROWSER.

// skBook is a title page and four chapters of a few screens each.
func skBook() []ebooktest.EPUBChapter {
	chs := []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Skim"}}}
	for i := 1; i <= 4; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Skim chapter %d", i), EpubType: "chapter", Paras: pgProse(fmt.Sprintf("s%d", i), 30)})
	}
	return chs
}

// skOpen opens the reader of an ofrApp book and waits until the skim bar is shown.
func skOpen(t *testing.T, b *cdp.Browser, e *ofrEnv, w, h int, mobile bool, query string) *cdp.Tab {
	t.Helper()
	return skOpenURL(t, b, e.read(query), w, h, mobile)
}

func skOpenURL(t *testing.T, b *cdp.Browser, url string, w, h int, mobile bool) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, mobile, true)
	tab.Navigate(url)
	tab.WaitFor(ofrMounted+` && document.getElementById("reader-skim") && !document.getElementById("reader-skim").hidden`, 10*time.Second)
	showBar(tab)
	return tab
}

// showBar shows the bar and dock: a phone opens with them hidden (decision 0036), and a hidden dock takes no touch.
func showBar(tab *cdp.Tab) {
	tab.Eval(`document.querySelector(".ts-readerpage").dataset.chrome = "on"`, nil)
}

// skDrag presses on the bar at one fraction of its width and lets go at another: a touch at 390 px, the mouse
// otherwise. The pause after the press lets the lazily loaded script arrive, as a real finger would.
func skDrag(t *testing.T, tab *cdp.Tab, mobile bool, from, to float64) {
	t.Helper()
	var r struct{ X, Y, W float64 }
	tab.Eval(`(() => { const r = document.getElementById("reader-skim").getBoundingClientRect(); return {X: r.x, Y: r.y + r.height / 2, W: r.width}; })()`, &r)
	x0, x1 := r.X+r.W*from, r.X+r.W*to
	if mobile {
		if err := tab.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 1}, nil); err != nil {
			t.Fatal(err)
		}
		send := func(typ string, x float64) {
			points := []map[string]any{{"x": x, "y": r.Y}}
			if typ == "touchEnd" {
				points = []map[string]any{}
			}
			if err := tab.Call("Input.dispatchTouchEvent", map[string]any{"type": typ, "touchPoints": points}, nil); err != nil {
				t.Fatal(err)
			}
		}
		send("touchStart", x0)
		time.Sleep(300 * time.Millisecond)
		send("touchMove", (x0+x1)/2)
		send("touchMove", x1)
		time.Sleep(100 * time.Millisecond)
		send("touchEnd", x1)
		return
	}
	mouse := func(typ string, x float64, buttons int) {
		p := map[string]any{"type": typ, "x": x, "y": r.Y, "button": "left", "buttons": buttons, "clickCount": 1}
		if err := tab.Call("Input.dispatchMouseEvent", p, nil); err != nil {
			t.Fatal(err)
		}
	}
	mouse("mouseMoved", x0, 0)
	mouse("mousePressed", x0, 1)
	time.Sleep(300 * time.Millisecond)
	mouse("mouseMoved", (x0+x1)/2, 1)
	mouse("mouseMoved", x1, 1)
	time.Sleep(100 * time.Millisecond)
	mouse("mouseReleased", x1, 0)
}

// skJumped waits for the page opened by a skim: bp in the address, the reader mounted again.
func skJumped(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.WaitFor(`location.search.includes("bp=") && `+ofrMounted+` && document.querySelector(".ts-reader__back") !== null`, 15*time.Second)
}

func TestSkimBrowserJump(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, skBook(), nil)
			pgFlow(t, e.l, "pages")
			tab := skOpen(t, b, e, vp.w, vp.h, vp.mobile, "ch=2")

			// Drag to 60 %: the tip names a chapter and the percent while the thumb is down; nothing is saved.
			skDrag(t, tab, vp.mobile, 0.02, 0.6)
			skJumped(t, tab)
			var chap int
			tab.Eval(`Number(document.querySelector("#reader section[data-ch]").dataset.ch)`, &chap)
			if chap < 3 {
				t.Errorf("a jump to 60 %% opened chapter %d", chap)
			}
			var href string
			tab.Eval(`document.querySelector(".ts-reader__back").getAttribute("href")`, &href)
			if !strings.Contains(href, "at=c2.") {
				t.Errorf("the chip leads to %q, want the chapter-2 place the jump left", href)
			}
			time.Sleep(4 * time.Second) // longer than the save delay
			if n := e.posts.Load(); n != 0 {
				t.Errorf("a jump alone sent %d saves", n)
			}

			// The chip returns to the place it left, then it is gone.
			tab.Eval(`document.querySelector(".ts-reader__back").click()`, nil)
			tab.WaitFor(`location.search.includes("at=c2.") && `+ofrMounted, 15*time.Second)
			tab.WaitFor(`document.querySelector(".ts-reader__back") === null`, 5*time.Second)

			// Skim again, then read on: three forward turns retire the chip, and reading saves the place.
			showBar(tab) // the page opened again with the bar away, on a phone
			skDrag(t, tab, vp.mobile, 0.02, 0.6)
			skJumped(t, tab)
			for i := 0; i < 3; i++ {
				pgNext(t, tab)
				time.Sleep(250 * time.Millisecond)
			}
			tab.WaitFor(`document.querySelector(".ts-reader__back") === null`, 5*time.Second)
			deadline := time.Now().Add(8 * time.Second)
			for e.posts.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
			}
			if e.posts.Load() == 0 {
				t.Error("reading on after a skim saved nothing")
			}
		})
	}
}

func TestSkimBrowserKeys(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, skBook(), nil)
	pgFlow(t, e.l, "pages")
	tab := skOpen(t, b, e, 1440, 900, false, "ch=2")
	value := func() (v int) {
		tab.Eval(`Number(document.getElementById("reader-skim").value)`, &v)
		return v
	}
	tab.Eval(`document.getElementById("reader-skim").focus()`, nil)
	time.Sleep(500 * time.Millisecond) // the script loads on focus
	start := value()
	pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0)
	first := value()
	pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0)
	pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0)
	if got := value(); first%100 != 0 || got-first != 200 || first <= start {
		t.Errorf("arrows: %d, %d, %d: want steps of one percent (100)", start, first, got)
	}
	var tip struct {
		Text, Valuetext string
		Hidden          bool
	}
	tab.Eval(`({Text: document.getElementById("reader-skim-tip").textContent, Valuetext: document.getElementById("reader-skim").getAttribute("aria-valuetext"), Hidden: document.getElementById("reader-skim-tip").hidden})`, &tip)
	if want := fmt.Sprintf(" · %d %%", value()/100); tip.Hidden || tip.Text != tip.Valuetext || !strings.HasSuffix(tip.Text, want) || !strings.HasPrefix(tip.Text, "Skim chapter") {
		t.Errorf("tip %+v, want a chapter title and %q", tip, want)
	}

	// Escape puts the thumb back and goes nowhere.
	pgKey(t, tab, "Escape", "Escape", 27, "", 0)
	if got := value(); got != start {
		t.Errorf("Escape left the thumb at %d, want %d", got, start)
	}
	var where string
	tab.Eval(`location.search`, &where)
	if strings.Contains(where, "bp=") {
		t.Errorf("Escape jumped: %s", where)
	}

	// Home and End are the browser's own; Enter commits.
	pgKey(t, tab, "End", "End", 35, "", 0)
	if got := value(); got != 10000 {
		t.Errorf("End: %d", got)
	}
	pgKey(t, tab, "Enter", "Enter", 13, "\r", 0)
	skJumped(t, tab)
	tab.Eval(`location.search`, &where)
	if !strings.Contains(where, "bp=10000") {
		t.Errorf("Enter opened %s", where)
	}
	if n := e.posts.Load(); n != 0 {
		t.Errorf("keys saved %d places", n)
	}
}

func TestSkimBrowserOffline(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, skBook(), nil)
	pgFlow(t, e.l, "pages")
	tab := b.NewTab(390, 844, true, true)
	ofrSeedCache(t, e, tab)
	ofrOffline(t, e, tab, true)
	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted+` && !document.getElementById("reader-skim").hidden`, 15*time.Second)
	showBar(tab)

	// Offline the shell cannot place bp: the chapter opens at its start, and the chip leads back.
	skDrag(t, tab, true, 0.02, 0.8)
	tab.WaitFor(`location.search.includes("bp=") && document.querySelector("[data-offline-shell]") && `+ofrMounted+` && document.querySelector(".ts-reader__back") !== null`, 20*time.Second)
	var chap, at string
	tab.Eval(`document.querySelector("#reader section[data-ch]").dataset.ch`, &chap)
	tab.Eval(`document.getElementById("reader").dataset.at`, &at)
	if chap != "4" && chap != "5" {
		t.Errorf("offline jump to 80 %% opened chapter %q", chap)
	}
	tab.Eval(`document.querySelector(".ts-reader__back").click()`, nil)
	tab.WaitFor(`location.search.includes("at=c2.") && `+ofrMounted+` && document.querySelector("section[data-ch='2']")`, 20*time.Second)
}

// TestSkimVisual captures the dock at rest and with the tip showing, at 390 and 1440 px (768 and 1024 lay the
// dock out the same way: the bar is as wide as the text column), in the four themes.
func TestSkimVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := pgSeedEPUB(t, skBook(), nil)
	pgFlow(t, l, "pages")
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(l.owner)
		l.a.h.ServeHTTP(w, r)
	}))
	for _, vp := range pgViewports {
		for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
			t.Run(vp.name+"-"+theme, func(t *testing.T) {
				tab := skOpenURL(t, b, base+l.readPath("ch=2&theme="+theme), vp.w, vp.h, vp.mobile)
				var m struct {
					Tall, Small, Over bool
					Inline            int
				}
				tab.Eval(`(() => {
					const bar = document.getElementById("reader-skim").getBoundingClientRect();
					return {Tall: Math.round(bar.height) >= 44, Small: bar.width < 200, Over: document.documentElement.scrollWidth > innerWidth,
						Inline: document.querySelectorAll("#reader-dock [style]:not(.ts-ruler__fill):not(.ts-ruler__tick):not(.ts-skim__tip)").length};
				})()`, &m)
				if !m.Tall || m.Small || m.Over || m.Inline != 0 {
					t.Errorf("dock probe %+v", m)
				}
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("skim-rest-%s-%d.png", theme, vp.w)))
				// Hold the thumb at 45 % and capture with the tip.
				tab.Eval(`(() => { const bar = document.getElementById("reader-skim"); bar.focus(); })()`, nil)
				time.Sleep(500 * time.Millisecond)
				tab.Eval(`(() => { const bar = document.getElementById("reader-skim"); bar.value = 4500; bar.dispatchEvent(new Event("input", { bubbles: true })); })()`, nil)
				tab.WaitFor(`!document.getElementById("reader-skim-tip").hidden`, 3*time.Second)
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("skim-tip-%s-%d.png", theme, vp.w)))
			})
		}
	}
}
