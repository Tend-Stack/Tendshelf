//go:build dev

package web

import (
	"testing"
	"time"
)

// TestBookPageBrowser drives the rebuilt book page in Chromium (make player): the desktop layout
// where /books/{id} shows the chapters, the tab swaps that keep the play region and the rails, the
// phone's rails under About only, the progress ruler, the one resume button while playing, and the
// tinted band in dark and the plain band in contrast. Needs TENDSHELF_TEST_BROWSER.
func TestBookPageBrowser(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	bookID, _, _, _ := bookVisualSeed(t, app)
	base := themedServer(t, app.signedIn(t))
	url := base + "/books/" + bookID

	t.Run("desktop", func(t *testing.T) {
		tab := b.NewTab(1440, 900, false, false)
		tab.Navigate(url + "?theme=dark")
		tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
		var d struct {
			AboutHidden, HomeLine, Chapters, Rails bool
			Tiles                                  int
		}
		tab.Eval(`(() => {
		  const vis = el => Boolean(el) && el.getClientRects().length > 0;
		  const home = document.querySelector("#book-tabs .is-home");
		  return {
		    aboutHidden: !vis(document.getElementById("book-tab-about")),
		    homeLine: Boolean(home) && home.id === "book-tab-chapters" && getComputedStyle(home).boxShadow !== "none",
		    chapters: vis(document.querySelector(".ts-book__aboutchapters .ts-chapter")),
		    rails: vis(document.querySelector(".ts-book__rails")),
		    tiles: document.querySelectorAll(".ts-book__tiles .ts-card").length
		  };
		})()`, &d)
		if !d.AboutHidden || !d.HomeLine || !d.Chapters || !d.Rails || d.Tiles < 3 {
			t.Errorf("1440 /books/{id}: %+v", d)
		}
		tab.Eval(`window.__play = document.getElementById("book-play")`, nil)
		tab.Eval(`document.getElementById("book-tab-details").click()`, nil)
		tab.WaitFor(`location.pathname.endsWith("/details") && document.querySelector('#book-tabs [aria-current="page"]').id === "book-tab-details"`, 5*time.Second)
		var after struct{ Rails, Same bool }
		tab.Eval(`({rails: document.querySelector(".ts-book__rails").getClientRects().length > 0, same: window.__play === document.getElementById("book-play")})`, &after)
		if !after.Rails || !after.Same {
			t.Errorf("after Details at 1440: %+v", after)
		}
	})

	t.Run("phone", func(t *testing.T) {
		tab := b.NewTab(390, 844, true, false)
		tab.Navigate(url + "?theme=dark")
		tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
		var p struct {
			Current                string
			Rails                  bool
			Fill                   string
			Chapters, Where, Short bool
		}
		tab.Eval(`(() => {
		  const vis = el => Boolean(el) && el.getClientRects().length > 0;
		  return {
		    current: document.querySelector('#book-tabs [aria-current="page"]').id,
		    rails: vis(document.querySelector(".ts-book__rails")),
		    fill: document.querySelector("#book-play .ts-ruler__fill").style.getPropertyValue("--ts-value"),
		    chapters: vis(document.querySelector(".ts-book__aboutchapters .ts-chapter")),
		    where: vis(document.querySelector(".ts-book__where")),
		    short: vis(document.querySelector(".ts-book__whereshort"))
		  };
		})()`, &p)
		if p.Current != "book-tab-about" || !p.Rails || p.Fill == "" || p.Chapters || !p.Where || p.Short {
			t.Errorf("390 About: %+v", p)
		}
		tab.Eval(`document.getElementById("book-tab-chapters").click()`, nil)
		tab.WaitFor(`location.pathname.endsWith("/chapters") && document.querySelector('#book-tabs [aria-current="page"]').id === "book-tab-chapters"`, 5*time.Second)
		var rails bool
		tab.Eval(`document.querySelector(".ts-book__rails").getClientRects().length > 0`, &rails)
		if rails {
			t.Error("390 Chapters: the rails are still visible")
		}
	})

	t.Run("band", func(t *testing.T) {
		for theme, wantGradient := range map[string]bool{"dark": true, "contrast": false} {
			tab := b.NewTab(390, 844, true, false)
			tab.Navigate(url + "?theme=" + theme)
			tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
			tab.WaitFor(`Boolean(document.querySelector(".ts-book__band.is-tinted"))`, 5*time.Second)
			var img string
			tab.Eval(`getComputedStyle(document.querySelector(".ts-book__band")).backgroundImage`, &img)
			if got := len(img) > 0 && img != "none"; got != wantGradient {
				t.Errorf("%s band background-image = %q, gradient wanted: %v", theme, img, wantGradient)
			}
		}
	})

	t.Run("play", func(t *testing.T) {
		b := chromium(t) // a browser of its own: the other subtests already used this one
		e := playerApp(t)
		tab := playBtnOpenPage(t, b, e.URL+"/books/"+e.book.ID, 390, 800)
		tab.Eval(`window.__saved = 0; document.body.addEventListener("progress-saved", () => { window.__saved++; })`, nil)
		playerClickSel(t, tab, "#book-play [data-ts-play]")
		tab.WaitFor(`window.tsPlayer.state().playing === true`, 10*time.Second)
		tab.WaitFor(`window.__saved > 0`, 30*time.Second)
		time.Sleep(600 * time.Millisecond) // the region's refresh lands after the save
		var n int
		tab.Eval(`document.querySelectorAll('[data-ts-play="`+e.book.ID+`"]').length`, &n)
		var inRegion int
		tab.Eval(`document.querySelectorAll("#book-play [data-ts-play]").length`, &inRegion)
		if n != 1 || inRegion != 1 {
			t.Errorf("after the save: %d play controls for the book, %d in #book-play, want 1 and 1", n, inRegion)
		}
		if s := playerStateOf(tab); !s.Playing {
			t.Errorf("not playing after the save: %+v", s)
		}
	})
}
