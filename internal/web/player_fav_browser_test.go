//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The full player's save / star button (decision 0043 addendum): a station or an episode shows it where a book
// shows its bookmark. These tests press it in the real player and read the server's rows.

// pkFavView is what the button looks like and whether a bookmark button is drawn beside it.
type pkFavView struct {
	Shown, Pressed, BookmarkShown bool
	Label                         string
	W, H                          float64
}

func pkFav(tab *cdp.Tab) (v pkFavView) {
	tab.Eval(`(() => { const d = `+ppDialog+`, b = d.querySelector("[data-ts-full-fav]"), r = b.getBoundingClientRect();
	  return {Shown: b.offsetWidth > 0, Pressed: b.getAttribute("aria-pressed") === "true", Label: b.getAttribute("aria-label"), W: r.width, H: r.height,
	    BookmarkShown: [...d.querySelectorAll("[data-ts-bookmark]")].some((e) => e.offsetWidth > 0)}; })()`, &v)
	return v
}

func pkPressFav(t *testing.T, tab *cdp.Tab, wantPressed bool) {
	t.Helper()
	playerClickSel(t, tab, "#player-full [data-ts-full-fav]")
	tab.WaitFor(`document.querySelector("#player-full [data-ts-full-fav]").getAttribute("aria-pressed") === "`+fmt.Sprint(wantPressed)+`"`, 5*time.Second)
}

func TestPlayerKindStationSave(t *testing.T) {
	for _, size := range [][2]int{{390, 844}, {1440, 900}} {
		tab, w, id := pkStation(t, size[0], size[1], "dark")
		saved := func() bool {
			favs, err := w.fx.svc.Favourites(t.Context(), w.benID)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range favs {
				if f.ID == id {
					return true
				}
			}
			return false
		}
		v := pkFav(tab)
		if !v.Shown || v.BookmarkShown || !v.Pressed || v.Label != "Remove Harbour FM" || v.W < 44 || v.H < 44 {
			t.Errorf("%v station: %+v", size, v)
		}
		pkPressFav(t, tab, false) // it starts saved, so the first press removes
		if saved() {
			t.Errorf("%v: the station is still saved after the press", size)
		}
		// The Radio page under the player lists saved stations: it follows the player's button, both ways.
		tab.WaitFor(`document.querySelector('#main form[action$="/radio/stations/`+id+`/remove"]') === null`, 5*time.Second)
		if v = pkFav(tab); v.Label != "Save Harbour FM" {
			t.Errorf("%v: label after remove %q", size, v.Label)
		}
		pkPressFav(t, tab, true)
		if !saved() {
			t.Errorf("%v: the station is not saved after the second press", size)
		}
		tab.WaitFor(`document.querySelector('#main form[action$="/radio/stations/`+id+`/remove"]') !== null`, 5*time.Second)
		if v = pkFav(tab); v.Label != "Remove Harbour FM" {
			t.Errorf("%v: label after save %q", size, v.Label)
		}
		// A refused save keeps the state and says why in plain words.
		tab.Eval(`window.fetch = () => Promise.resolve(new Response("x", {status: 422}))`, nil)
		playerClickSel(t, tab, "#player-full [data-ts-full-fav]")
		tab.WaitFor(`[...document.querySelectorAll(".ts-toast")].some((e) => e.textContent.includes("100 stations"))`, 5*time.Second)
		if v = pkFav(tab); !v.Pressed {
			t.Errorf("%v: a refused press changed the state", size)
		}
		tab.Close()
	}
}

func TestPlayerKindEpisodeStar(t *testing.T) {
	for _, size := range [][2]int{{390, 844}, {1440, 900}} {
		tab, w, _ := pkEpisode(t, false, size[0], size[1], "dark")
		starred := func() bool {
			e, err := w.fx.svc.Episode(t.Context(), w.benID, w.eps[0])
			if err != nil {
				t.Fatal(err)
			}
			return e.State.Favourite
		}
		v := pkFav(tab)
		if !v.Shown || v.BookmarkShown || v.Pressed || v.Label != "Star" || v.W < 44 || v.H < 44 {
			t.Errorf("%v episode: %+v", size, v)
		}
		pkPressFav(t, tab, true)
		if !starred() {
			t.Errorf("%v: not starred after the press", size)
		}
		// The episode page under the player has a star of its own: it follows, and the person stays in the player.
		tab.WaitFor(`document.querySelector('#main form[action$="/favourite"] button').getAttribute("aria-pressed") === "true"`, 5*time.Second)
		var under struct {
			Open, FocusInPlayer bool
			ScrollY             float64
		}
		tab.Eval(`(() => { const d = `+ppDialog+`; return {Open: d.open, FocusInPlayer: d.contains(document.activeElement), ScrollY: window.scrollY}; })()`, &under)
		if !under.Open || !under.FocusInPlayer || under.ScrollY != 0 {
			t.Errorf("%v: after the page refresh %+v", size, under)
		}
		pkPressFav(t, tab, false)
		if starred() {
			t.Errorf("%v: still starred after the second press", size)
		}
		tab.Eval(`window.fetch = () => Promise.resolve(new Response("x", {status: 429}))`, nil)
		playerClickSel(t, tab, "#player-full [data-ts-full-fav]")
		tab.WaitFor(`[...document.querySelectorAll(".ts-toast")].some((e) => e.textContent.includes("Wait a while"))`, 5*time.Second)
		if v = pkFav(tab); v.Pressed {
			t.Errorf("%v: a refused press changed the state", size)
		}
		tab.Close()
	}
}

func TestPlayerKindBookNoFav(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	tab := ppOpen(t, e, 390, 844, "dark")
	if v := pkFav(tab); v.Shown || !v.BookmarkShown {
		t.Errorf("book: %+v", v)
	}
}

// TestPlayerKindFavContrast: the icon, pressed and not, reads at 3:1 against the player in the four themes.
func TestPlayerKindFavContrast(t *testing.T) {
	tab, _, _ := pkStation(t, 390, 844, "dark")
	probe := `(() => { const d = ` + ppDialog + `, b = d.querySelector("[data-ts-full-fav]"); return {Ground: getComputedStyle(d).backgroundColor, Ink: getComputedStyle(b).color}; })()`
	for _, theme := range themes {
		pkTheme(tab, theme)
		for _, pressed := range []string{"true", "false"} {
			tab.Eval(ppDialog+`.querySelector("[data-ts-full-fav]").setAttribute("aria-pressed", `+cdp.Quote(pressed)+`)`, nil)
			var r struct{ Ground, Ink string }
			tab.Eval(probe, &r)
			if x := pnrRatio(r.Ink, r.Ground); x < 3 {
				t.Errorf("%s pressed=%s: icon contrast %.2f < 3", theme, pressed, x)
			}
		}
	}
}
