//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestSmoothProbe is an inventory, not an assertion: it walks every destination of the visible navigation
// (boosted htmx clicks), with and without the player bar, and logs one line per step with the layout
// shift score, the biggest shift source and the distinct tops of the navigation, player bar and heading.
// Run it with -v and read the log.

// smoothInstall starts a layout-shift observer and a per-frame recorder in window.tsProbe.
const smoothInstall = `(() => {
  const visible = (sel) => [...document.querySelectorAll(sel)].find((n) => n.offsetWidth > 0 && n.offsetHeight > 0);
  const top = (el) => el ? Math.round(el.getBoundingClientRect().top * 10) / 10 : null;
  const d = {cls: 0, worst: null, worstValue: 0, nav: new Set(), player: new Set(), head: new Set(), scroll: new Set(), height: new Set(), run: true};
  window.tsProbe = d;
  const rect = (r) => r ? [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)].join(",") : "-";
  const name = (n) => n ? (n.nodeName || "?").toLowerCase() + (n.id ? "#" + n.id : "") + (n.classList && n.classList.length ? "." + [...n.classList].slice(0, 2).join(".") : "") : "?";
  try {
    new PerformanceObserver((list) => {
      for (const e of list.getEntries()) {
        d.cls += e.value;
        if (e.value > d.worstValue) {
          d.worstValue = e.value;
          const s = (e.sources || [])[0];
          d.worst = s ? name(s.node) + " " + rect(s.previousRect) + "->" + rect(s.currentRect) : "?";
        }
      }
    }).observe({type: "layout-shift", buffered: false});
  } catch (x) {}
  const frame = () => {
    const p = document.getElementById("player");
    d.nav.add(top(visible("#nav-rail, #nav-side, #nav-tabbar, .ts-nav-tabbar")));
    d.player.add(p && !p.hidden && p.offsetHeight > 0 ? top(p) : null);
    d.head.add(top(document.querySelector("#main h1, #main h2")));
    d.scroll.add(Math.round(scrollY));
    d.height.add(document.documentElement.scrollHeight);
    if (d.run) requestAnimationFrame(frame);
  };
  frame();
  return true;
})()`

const smoothSel = `#nav-rail a, #nav-side a, #nav-tabbar a, .ts-nav-tabbar a`

const smoothHrefs = `[...document.querySelectorAll("` + smoothSel + `")].filter((a) => a.offsetWidth > 0).map((a) => a.getAttribute("href")).filter((h, i, all) => h && h.startsWith("/") && all.indexOf(h) === i)`

func smoothClickJS(href string) string {
	return `(() => { const a = [...document.querySelectorAll("` + smoothSel + `")].find((x) => x.offsetWidth > 0 && x.getAttribute("href") === ` + cdp.Quote(href) + `); if (a) a.click(); return !!a; })()`
}

const smoothRead = `(() => {
  const d = window.tsProbe; d.run = false;
  const f = (s) => [...s].filter((v) => v !== null).join("|") || "-";
  return {Path: location.pathname, Cls: Math.round(d.cls * 1000) / 1000, Worst: d.worst || "-", Nav: f(d.nav), Player: f(d.player), Head: f(d.head), Scroll: f(d.scroll), Height: f(d.height)};
})()`

type smoothStep struct {
	Path, Worst, Nav, Player, Head, Scroll, Height string
	Cls                                            float64
}

func smoothLog(t *testing.T, label string, w int, from, to string, s smoothStep) {
	t.Helper()
	t.Logf("%s %d %s->%s at=%s CLS=%.3f worst=[%s] nav=%s player=%s h1=%s scrollY=%s docH=%s",
		label, w, from, to, s.Path, s.Cls, s.Worst, s.Nav, s.Player, s.Head, s.Scroll, s.Height)
}

// smoothStepTo clicks the destination in the visible navigation and logs the step.
func smoothStepTo(t *testing.T, tab *cdp.Tab, label string, w int, from, to string) {
	t.Helper()
	var ok, installed bool
	tab.Eval(smoothInstall, &installed)
	tab.Eval(smoothClickJS(to), &ok)
	if !ok {
		t.Logf("%s %d %s->%s no such link", label, w, from, to)
		return
	}
	time.Sleep(900 * time.Millisecond)
	var s smoothStep
	tab.Eval(smoothRead, &s)
	smoothLog(t, label, w, from, to, s)
}

func smoothWalk(t *testing.T, tab *cdp.Tab, label string, w int) {
	t.Helper()
	var hrefs []string
	tab.Eval(smoothHrefs, &hrefs)
	if len(hrefs) == 0 {
		t.Logf("%s %d no navigation links", label, w)
		return
	}
	from := "start"
	for _, h := range append(append([]string{}, hrefs...), hrefs[0]) {
		smoothStepTo(t, tab, label, w, from, h)
		from = h
	}
}

// smoothBookWalk opens the first book card from /libraries and goes back.
func smoothBookWalk(t *testing.T, tab *cdp.Tab, label string, w int) {
	t.Helper()
	smoothStepTo(t, tab, label, w, "start", "/libraries")
	var href string
	tab.Eval(`(() => { const a = [...document.querySelectorAll("#main a[href^='/books/']")].find((x) => x.offsetWidth > 0 && !x.getAttribute("href").includes("/read")); return a ? a.getAttribute("href") : ""; })()`, &href)
	if href == "" {
		t.Logf("%s %d no book link on /libraries", label, w)
		return
	}
	var s smoothStep
	var ok bool
	tab.Eval(smoothInstall, &ok)
	tab.Eval(`document.querySelector("#main a[href="+`+cdp.Quote(`"`+href+`"`)+`+"]").click()`, nil)
	time.Sleep(900 * time.Millisecond)
	tab.Eval(smoothRead, &s)
	smoothLog(t, label, w, "/libraries", "book", s)
	tab.Eval(smoothInstall, &ok)
	tab.Eval(`history.back()`, nil)
	time.Sleep(900 * time.Millisecond)
	tab.Eval(smoothRead, &s)
	smoothLog(t, label, w, "book", "back", s)
}

func TestSmoothProbe(t *testing.T) {
	e := nowPanelEnv(t)
	// Many more books so Library and Search pages are taller than the window.
	for i := 0; i < 30; i++ {
		librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: fmt.Sprintf("Probe Book %02d", i), Authors: []string{"Imogen Hale"},
			DurationMs: 3_600_000, AddedAt: 1_700_000_000_000 + int64(i)})
	}
	b := chromium(t)
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 900}, {1440, 900}} {
		w, h := size[0], size[1]
		// No player yet: nothing is stored, so the bar stays hidden.
		tab := b.NewTab(w, h, w < 768, false)
		tab.Navigate(e.URL + "/libraries")
		tab.WaitFor(`document.readyState === "complete"`, 10*time.Second)
		time.Sleep(500 * time.Millisecond)
		smoothWalk(t, tab, "noplayer", w)
		smoothBookWalk(t, tab, "noplayer", w)
	}
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 900}, {1440, 900}} {
		w, h := size[0], size[1]
		// With the book loaded in the player bar.
		ptab := fullpOpen(t, e, w, h, false)
		time.Sleep(500 * time.Millisecond)
		smoothWalk(t, ptab, "player", w)
		smoothBookWalk(t, ptab, "player", w)
	}
}
