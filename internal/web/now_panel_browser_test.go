//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// NP2 browser tests (make player): the desktop Now playing panel in Chromium, at 1280, 1440 and 1920 px
// in the four themes, and the Home hero's Show player. The fixture is the player's book with a summary,
// a series of three, genres and one more book by the author.

// nowPanelEnv is playerApp with the panel's data around the book.
func nowPanelEnv(t *testing.T) *playerEnv {
	t.Helper()
	long := strings.Repeat("A calm harbour at dusk, and a lamp that will not go out. ", 12)
	e := playerAppWith(t, func(s *librarytest.BookSpec) {
		s.Description, s.Genres, s.PublishedYear = long, []string{"Mystery", "Sea stories"}, 2021
		s.Series, s.Sequence = "Harbour Lights", "2"
	})
	for _, b := range []struct{ title, seq string }{{"Harbour Dawn", "1"}, {"Harbour Night", "3"}} {
		librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: b.title, Authors: []string{"Imogen Hale"},
			Series: "Harbour Lights", Sequence: b.seq, DurationMs: 3_600_000, AddedAt: 1_700_000_000_000})
	}
	librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: "Salt Road", Authors: []string{"Imogen Hale"}, DurationMs: 3_600_000, AddedAt: 1_700_000_000_000})
	return e
}

// nowPanelOpen opens the Library with the book playing and the player open, and waits for the panel.
func nowPanelOpen(t *testing.T, e *playerEnv, w, h int) *cdp.Tab {
	t.Helper()
	tab := fullpOpen(t, e, w, h, true)
	bmkeyPlay(t, tab, "[data-ts-toggle]")
	fullpOpenPlayer(t, tab)
	tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__desc") !== null`, 5*time.Second)
	return tab
}

func nowPanelTheme(tab *cdp.Tab, theme string) {
	tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
	time.Sleep(200 * time.Millisecond)
}

// nowPanelRatio is JavaScript that defines window.__ratio(el): the contrast ratio of the element's text
// colour on the backgrounds behind it. A canvas turns any CSS colour (color-mix, oklab) into sRGB.
const nowPanelRatio = `window.__ratio = (el) => {
  const g = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
  const px = () => g.getImageData(0, 0, 1, 1).data;
  const chain = [];
  for (let n = el; n; n = n.parentElement) chain.unshift(getComputedStyle(n).backgroundColor);
  g.canvas.width = g.canvas.height = 1;
  g.fillStyle = "#000"; g.fillRect(0, 0, 1, 1);
  for (const c of chain) { g.fillStyle = c; g.fillRect(0, 0, 1, 1); }
  const bg = [...px()];
  g.fillStyle = getComputedStyle(el).color; g.fillRect(0, 0, 1, 1);
  const fg = [...px()];
  const lum = (c) => { const [r, gr, b] = c.slice(0, 3).map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * gr + 0.0722 * b; };
  const [hi, lo] = [lum(fg), lum(bg)].sort((a, b) => b - a);
  return (hi + 0.05) / (lo + 0.05);
}; true`

func TestNowPanelBrowser(t *testing.T) {
	e := nowPanelEnv(t)
	out := playerOut(t)
	for _, w := range []int{1280, 1440, 1920} {
		t.Run(fmt.Sprintf("desktop-%d", w), func(t *testing.T) { nowPanelDesktop(t, e, w, out) })
	}
	t.Run("sparse", func(t *testing.T) { nowPanelSparse(t, e) })
	t.Run("hero", func(t *testing.T) { nowPanelHero(t, e) })
	t.Run("narrow", func(t *testing.T) { nowPanelNarrow(t, e) })
}

func nowPanelDesktop(t *testing.T, e *playerEnv, w int, out string) {
	tab := nowPanelOpen(t, e, w, 800)
	tab.Screenshot(filepath.Join(out, fmt.Sprintf("np-open-%d.png", w)))

	// Only the bar's play and pause controls: the full player's transport and toolbar are gone.
	var bar struct{ Visible, Transport, Toolbar int }
	tab.Eval(`({
	  Visible: [...document.querySelectorAll("[data-ts-toggle], [data-ts-full-toggle]")].filter((x) => x.offsetParent).length,
	  Transport: [...document.querySelectorAll("#player-full [data-ts-full-toggle], #player-full [data-ts-full-back], #player-full [data-ts-full-forward], #player-full [data-ts-full-prev], #player-full [data-ts-full-next]")].filter((x) => x.offsetParent).length,
	  Toolbar: [...document.querySelectorAll("#player-full [data-ts-open-speed], #player-full [data-ts-open-sleep], #player-full [data-ts-open-chapters]")].filter((x) => x.offsetParent).length })`, &bar)
	if bar.Visible != 1 || bar.Transport != 0 || bar.Toolbar != 0 {
		t.Errorf("%d: play controls visible %d, full transport %d, full toolbar %d; want 1, 0, 0", w, bar.Visible, bar.Transport, bar.Toolbar)
	}

	// The panel is not modal: the page's content stops short of it instead of running under it.
	var room struct{ Content, Panel float64 }
	tab.Eval(`(() => {
	  const main = document.querySelector(".ts-main"), r = main.getBoundingClientRect();
	  return { Content: r.right - parseFloat(getComputedStyle(main).paddingRight),
	    Panel: document.getElementById("player-full").getBoundingClientRect().left };
	})()`, &room)
	if room.Content > room.Panel {
		t.Errorf("%d: the page's content reaches %.0f px, under the panel from %.0f px", w, room.Content, room.Panel)
	}

	// One scroll area, tabs that stay, no empty area under the last section.
	var sc struct {
		Scrollers      int
		Overflows      bool
		TabsTop, Body  float64
		Gap            float64
		Sub, Menu, Txt string
		Dup            bool
	}
	tab.Eval(`(() => {
	  const d = document.getElementById("player-full"), body = d.querySelector("[data-ts-full-body]");
	  const scrollers = [...d.querySelectorAll("*")].filter((x) => /auto|scroll/.test(getComputedStyle(x).overflowY) && x.scrollHeight > x.clientHeight + 1).length;
	  const overflows = body.scrollHeight > body.clientHeight;
	  body.scrollTo({ top: 700, behavior: "instant" });
	  const tabs = d.querySelector(".ts-np__tabs").getBoundingClientRect().top, top = body.getBoundingClientRect().top;
	  body.scrollTo({ top: body.scrollHeight, behavior: "instant" });
	  const gap = body.getBoundingClientRect().bottom - d.querySelector("#now-panel").getBoundingClientRect().bottom;
	  body.scrollTo({ top: 0, behavior: "instant" });
	  const text = d.innerText + document.getElementById("player").innerText;
	  return { Scrollers: scrollers, Overflows: overflows, TabsTop: tabs, Body: top, Gap: gap,
	    Sub: d.querySelector("[data-ts-full-author]").parentElement.textContent.trim().replace(/\s+/g, " "),
	    Menu: d.querySelector("[data-ts-full-extra]").textContent.trim().replace(/\s+/g, " "),
	    Dup: /Chapter (\d+) · Chapter \1\b/.test(text) };
	})()`, &sc)
	if sc.Scrollers != 1 || !sc.Overflows {
		t.Errorf("%d: %d scroll areas (want 1), overflow %v", w, sc.Scrollers, sc.Overflows)
	}
	if sc.TabsTop < sc.Body-1 || sc.TabsTop > sc.Body+1 {
		t.Errorf("%d: tabs at %.0f after scrolling, the scroll area starts at %.0f", w, sc.TabsTop, sc.Body)
	}
	if sc.Gap < 0 || sc.Gap > 80 {
		t.Errorf("%d: %.0f px of empty area under the last section", w, sc.Gap)
	}
	if !strings.Contains(sc.Sub, "read by Theo Marsh") || !strings.Contains(sc.Sub, "Imogen Hale") {
		t.Errorf("%d: subtitle %q lacks the author or the narrator", w, sc.Sub)
	}
	if !strings.Contains(sc.Menu, "Add to shelf") || !strings.Contains(sc.Menu, "Share") {
		t.Errorf("%d: the ⋯ menu's extra items are %q", w, sc.Menu)
	}
	if sc.Dup {
		t.Errorf("%d: the text repeats \"Chapter N · Chapter N\"", w)
	}

	// About: the summary is clamped until Read more opens it; series, details, genres and more by.
	var ab struct {
		Clamp, ClampOpen, Series, Here, HereText, Genres, Covers, Dts, EmptyDd, Closed, Open, Label, TabLabel, LessBelow string
		Rows                                                                                                             int
	}
	tab.Eval(`(() => {
	  const q = (s) => document.querySelector("#now-panel " + s), all = (s) => [...document.querySelectorAll("#now-panel " + s)];
	  const desc = q(".ts-np__summary > .ts-np__desc"), more = q(".ts-np__more");
	  const vis = (x) => x && x.offsetParent ? x.textContent.trim() : "";
	  const clamp = getComputedStyle(desc).webkitLineClamp;
	  more.open = true;
	  const clampOpen = getComputedStyle(desc).display + "/" + getComputedStyle(desc).webkitLineClamp;
	  const open = vis(q(".ts-np__more-open")), closed = vis(q(".ts-np__more-closed"));
	  // Open, "Show less" sits below the rest of the text, not between the two parts.
	  const rest = more.querySelector(".ts-np__desc").getBoundingClientRect(), less = more.querySelector("summary").getBoundingClientRect();
	  const lessBelow = less.top >= rest.bottom - 1 ? "yes" : "no: summary top " + less.top + ", text bottom " + rest.bottom;
	  more.open = false;
	  const here = q('.ts-np__seriesitem[aria-current="true"]');
	  return { Clamp: clamp, ClampOpen: clampOpen, Series: all(".ts-np__seriesitem").length + "", Here: here ? "1" : "0",
	    HereText: here ? here.textContent.replace(/\s+/g, " ") : "", Genres: all(".ts-np__genres a").length + "", Covers: all(".ts-np__morebook").length + "",
	    Dts: all(".ts-np__credits dt").map((x) => x.textContent.trim()).join(","), EmptyDd: all(".ts-np__credits dd").filter((x) => !x.textContent.trim()).length + "",
	    Open: open, Closed: closed, LessBelow: lessBelow, Label: q("aside, .ts-np") ? document.getElementById("now-panel").getAttribute("aria-label") : "",
	    TabLabel: q(".ts-np__tablist").getAttribute("aria-label"), Rows: all(".ts-np__credits dt").length };
	})()`, &ab)
	if ab.Clamp != "6" || !strings.HasPrefix(ab.ClampOpen, "block") {
		t.Errorf("%d: summary clamp %q, open %q; want 6 then block", w, ab.Clamp, ab.ClampOpen)
	}
	if ab.Open != "Show less" || ab.Closed != "" {
		t.Errorf("%d: Read more open shows %q and closed shows %q", w, ab.Open, ab.Closed)
	}
	if ab.LessBelow != "yes" {
		t.Errorf("%d: Show less is not below the text: %s", w, ab.LessBelow)
	}
	if ab.Series != "3" || ab.Here != "1" || !strings.Contains(ab.HereText, "here") {
		t.Errorf("%d: series rows %s, current %s, text %q", w, ab.Series, ab.Here, ab.HereText)
	}
	if ab.Genres != "2" || ab.Covers == "0" {
		t.Errorf("%d: genres %s (want 2), more by %s (want some)", w, ab.Genres, ab.Covers)
	}
	if !strings.Contains(ab.Dts, "Written by") || !strings.Contains(ab.Dts, "Read by") || ab.EmptyDd != "0" {
		t.Errorf("%d: details rows %q, empty values %s", w, ab.Dts, ab.EmptyDd)
	}
	if ab.Label == "" || ab.TabLabel == "" {
		t.Errorf("%d: the panel (%q) or its tab list (%q) has no accessible name", w, ab.Label, ab.TabLabel)
	}

	// Tabs by keyboard: the arrows move focus and selection, the tab panel is named by the tab.
	tab.Eval(`document.getElementById("now-tab-about").focus()`, nil)
	fullpKey(t, tab, "ArrowRight", 39)
	tab.WaitFor(`document.getElementById("now-tab-bookmarks").getAttribute("aria-selected") === "true" && document.activeElement.id === "now-tab-bookmarks"`, 3*time.Second)
	var tabs struct{ Labelled, Index, Other string }
	tab.Eval(`({ Labelled: document.getElementById("now-panel-tab").getAttribute("aria-labelledby"), Index: String(document.getElementById("now-tab-bookmarks").tabIndex),
	  Other: String(document.getElementById("now-tab-about").tabIndex) + document.getElementById("now-tab-about").getAttribute("aria-selected") })`, &tabs)
	if tabs.Labelled != "now-tab-bookmarks" || tabs.Index != "0" || tabs.Other != "-1false" {
		t.Errorf("%d: after ArrowRight: panel labelled by %q, tab indexes %s / %s", w, tabs.Labelled, tabs.Index, tabs.Other)
	}
	tab.WaitFor(`document.querySelector("#now-panel-tab .ts-np__empty, #now-panel-tab .ts-np__marks") !== null`, 3*time.Second)
	fullpKey(t, tab, "ArrowLeft", 37)
	tab.WaitFor(`document.getElementById("now-tab-about").getAttribute("aria-selected") === "true" && document.querySelector("#now-panel-tab .ts-np__desc") !== null`, 3*time.Second)

	// A bookmark from the bar (the B key) adds one to the count without a reload.
	var before, after string
	tab.Eval(`document.getElementById("now-panel-count").textContent.trim()`, &before)
	tab.Eval(`window.__marker = "same page"`, nil)
	tab.Eval(`document.activeElement.blur()`, nil)
	fullpKey(t, tab, "b", 66)
	want := fmt.Sprint(atoiOr0(before) + 1)
	tab.WaitFor(`document.getElementById("now-panel-count").textContent.trim() === `+cdp.Quote(want), 5*time.Second)
	tab.Eval(`document.getElementById("now-panel-count").textContent.trim() + (window.__marker || "")`, &after)
	if after != want+"same page" {
		t.Errorf("%d: bookmark count %q -> %q (want %s, no reload)", w, before, after, want)
	}

	// Contrast in the four themes. The cover's accent is turned into a bad colour on purpose: accent text
	// in the player and the panel must not depend on it.
	tab.Eval(nowPanelRatio, nil)
	for _, theme := range themes {
		nowPanelTheme(tab, theme)
		tab.Eval(`document.getElementById("player-full").style.setProperty("--cover-accent", "#ffff00")`, nil)
		for name, sel := range map[string]string{
			"time left": "[data-ts-full-left]", "series state": `.ts-np__seriesitem[aria-current="true"] .ts-np__seriesstate`,
			"Read more": ".ts-np__more-closed", "summary": ".ts-np__desc", "tab": ".ts-np__tabbtn[aria-selected=true]", "heading": ".ts-np__h",
		} {
			var r float64
			tab.Eval(`window.__ratio(document.querySelector(`+cdp.Quote("#player-full "+sel)+`))`, &r)
			if r < 4.5 {
				t.Errorf("%d %s: %s contrast %.2f, want 4.5", w, theme, name, r)
			}
		}
		tab.Eval(`document.getElementById("player-full").style.removeProperty("--cover-accent")`, nil)
	}

	// Captures: About and scrolled, then Bookmarks, dark and light.
	for _, theme := range []string{"dark", "light"} {
		nowPanelTheme(tab, theme)
		tab.Eval(`document.querySelector("[data-ts-full-body]").scrollTo({ top: 0, behavior: "instant" })`, nil)
		tab.Screenshot(filepath.Join(out, fmt.Sprintf("np-about-%s-%d.png", theme, w)))
		tab.Eval(`document.querySelector("[data-ts-full-body]").scrollTo({ top: 700, behavior: "instant" })`, nil)
		time.Sleep(100 * time.Millisecond)
		tab.Screenshot(filepath.Join(out, fmt.Sprintf("np-scrolled-%s-%d.png", theme, w)))
		tab.Eval(`document.querySelector("[data-ts-full-body]").scrollTo({ top: 0, behavior: "instant" }); document.getElementById("now-tab-bookmarks").click()`, nil)
		tab.WaitFor(`document.querySelector("#now-panel-tab .ts-np__marks") !== null`, 3*time.Second)
		tab.Screenshot(filepath.Join(out, fmt.Sprintf("np-bookmarks-%s-%d.png", theme, w)))
		tab.Eval(`document.getElementById("now-tab-about").click()`, nil)
		tab.WaitFor(`document.querySelector("#now-panel-tab .ts-np__desc") !== null`, 3*time.Second)
	}
	copyNowPanelCaptures(t, out, w)
}

func atoiOr0(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}

// copyNowPanelCaptures copies the 1440 and 1920 captures to NOW_PANEL_CAPTURES when it is set.
func copyNowPanelCaptures(t *testing.T, out string, w int) {
	dst := os.Getenv("NOW_PANEL_CAPTURES")
	if dst == "" || w < 1440 {
		return
	}
	files, _ := filepath.Glob(filepath.Join(out, fmt.Sprintf("np-*-%d.png", w)))
	_ = os.MkdirAll(dst, 0o755)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// nowPanelSparse: a book with no summary, series, genres or other books shows the dashed box and no
// empty rows.
func nowPanelSparse(t *testing.T, e *playerEnv) {
	bare := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: "Bare Book", Authors: []string{"Solo Author"}, AddedAt: 1_700_000_000_000})
	tab := nowPanelOpen(t, e, 1440, 800)
	tab.Eval(`htmx.ajax("GET", "/hx/player/panel/`+bare.ID+`", { target: "#now-panel", swap: "outerHTML" })`, nil)
	tab.WaitFor(`document.querySelector('#now-panel[data-book-id="`+bare.ID+`"]') !== null`, 5*time.Second)
	var r struct {
		Dashed, Empty, Series, More bool
		Empties                     int
	}
	tab.Eval(`({ Dashed: getComputedStyle(document.querySelector("#now-panel .ts-np__empty")).borderTopStyle === "dashed",
	  Empty: document.querySelector("#now-panel .ts-np__empty").offsetParent !== null,
	  Series: document.querySelector("#now-panel .ts-np__series") !== null, More: document.querySelector("#now-panel .ts-np__covers") !== null,
	  Empties: [...document.querySelectorAll("#now-panel dd, #now-panel dt")].filter((x) => !x.textContent.trim()).length })`, &r)
	if !r.Dashed || !r.Empty || r.Series || r.More || r.Empties != 0 {
		t.Errorf("sparse book: dashed box %v shown %v, series %v, more %v, empty rows %d", r.Dashed, r.Empty, r.Series, r.More, r.Empties)
	}
}

// nowPanelHero: on Home the hero of the playing book says Now playing and Show player (no second
// Pause); paused, it is today's single Resume.
func nowPanelHero(t *testing.T, e *playerEnv) {
	e.store(t, 10_000, "heropaneldev", "First device", time.Now().UnixMilli()-5000, false)
	b := chromium(t)
	tab := b.NewTab(1440, 900, false, true)
	tab.Navigate(e.URL + "/")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
	hero := func() (eyebrow, button string) {
		tab.Eval(`document.querySelector("#home-hero [data-ts-hero-eyebrow]").textContent.trim()`, &eyebrow)
		tab.Eval(`document.querySelector("#home-hero [data-ts-play]").textContent.trim()`, &button)
		return
	}
	if _, button := hero(); button != "Resume" {
		t.Fatalf("paused hero button says %q", button)
	}
	playerClickSel(t, tab, "#home-hero [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)
	tab.WaitFor(`document.querySelector("#home-hero [data-ts-play]").textContent.trim() === "Show player"`, 5*time.Second)
	eyebrow, button := hero()
	if eyebrow != "Now playing" || button != "Show player" {
		t.Errorf("playing hero: eyebrow %q button %q", eyebrow, button)
	}
	var pauses int
	tab.Eval(`[...document.querySelectorAll("#home-hero [data-ts-play], #home-hero [data-ts-play] *")].filter((x) => /pause/i.test(x.textContent + (x.getAttribute("aria-label") || "")) && x.offsetParent).length`, &pauses)
	if pauses != 0 {
		t.Errorf("the hero still has %d Pause controls", pauses)
	}
	playerClickSel(t, tab, "#home-hero [data-ts-play]")
	tab.WaitFor(`document.getElementById("player-full").open`, 3*time.Second)
	tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__tabbtn") !== null`, 5*time.Second)
	var playing bool
	tab.Eval(`window.tsPlayer.state().playing`, &playing)
	if !playing {
		t.Error("Show player paused the book")
	}
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`document.querySelector("#home-hero [data-ts-play]").textContent.trim() === "Resume"`, 8*time.Second)
	if eyebrow, _ := hero(); eyebrow == "Now playing" {
		t.Errorf("the paused hero's eyebrow is still %q", eyebrow)
	}
}

// nowPanelNarrow: below 1280 px the panel is not shown and never requested.
func nowPanelNarrow(t *testing.T, e *playerEnv) {
	for _, w := range []int{390, 768} {
		tab := fullpOpen(t, e, w, 900, true)
		fullpOpenPlayer(t, tab)
		var shown bool
		tab.Eval(`document.querySelector("#now-panel").offsetParent !== null || getComputedStyle(document.querySelector("#now-panel")).display !== "none"`, &shown)
		if shown {
			t.Errorf("%d: the panel is displayed", w)
		}
		var requests int
		tab.Eval(`performance.getEntriesByType("resource").filter((x) => x.name.includes("/hx/player/panel")).length`, &requests)
		if requests != 0 {
			t.Errorf("%d: %d panel requests", w, requests)
		}
		var transport bool
		tab.Eval(`document.querySelector("#player-full [data-ts-full-toggle]").offsetParent !== null`, &transport)
		if !transport {
			t.Errorf("%d: the full player lost its transport", w)
		}
	}
}
