//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rowsProbeJS describes every drawn element that matches rowSel after the page settled (decision 0044, second half):
// how far apart the vertical centres of its drawn controls are (one line means a spread under 6 px), whether the
// primary control is drawn, which keys are folded and which twins the opened menu shows, and whether any control is
// cut off by the window's edge.
const rowsProbeJS = `(async (rowSel, primarySel) => {
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(r, 200))));
  const drawn = (el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const out = [];
  for (const row of document.querySelectorAll(rowSel)) {
    if (!drawn(row)) continue;
    const ctl = [...row.querySelectorAll("a.ts-btn, button.ts-btn, summary.ts-btn, summary.ts-iconbtn, .ts-btn")].filter(drawn).filter((el) => !el.closest("[hidden], details:not([open]) > :not(summary)"));
    const centres = ctl.map((el) => { const b = el.getBoundingClientRect(); return b.top + b.height / 2; });
    const spread = centres.length ? Math.max(...centres) - Math.min(...centres) : 0;
    const clipped = ctl.some((el) => { const b = el.getBoundingClientRect(); return b.right > innerWidth + 1 || b.left < -1; });
    const small = ctl.filter((el) => { const b = el.getBoundingClientRect(); return b.height < 43.5; }).length;
    const menu = row.querySelector(".ts-moremenu, [data-ts-fit-menu]");
    const folded = [...new Set([...row.querySelectorAll("[data-ts-fit-off]")].filter((el) => !el.hidden).map((el) => el.dataset.tsFitItem))];
    let twins = [];
    if (menu && drawn(menu)) {
      menu.open = true;
      twins = [...menu.querySelectorAll("[data-ts-fit-for]")].filter(drawn).map((el) => el.dataset.tsFitFor);
      menu.open = false;
    }
    out.push({n: ctl.length, spread, primary: !primarySel || !!row.querySelector(primarySel) && drawn(row.querySelector(primarySel)),
      clipped, small, folded, twins, menuShown: !!menu && drawn(menu), fit: row.hasAttribute("data-ts-fit"), fitOn: row.hasAttribute("data-ts-fit-on"),
      tight: row.hasAttribute("data-ts-fit-tight"), over: document.documentElement.scrollWidth > innerWidth + 1,
      reachable: [...row.querySelectorAll("[data-ts-fit-off]")].filter((el) => getComputedStyle(el).display !== "none").length});
  }
  return out;
})`

type rowProbe struct {
	N                 int
	Spread            float64
	Primary           bool
	Clipped, Over     bool
	Small             int
	Folded, Twins     []string
	MenuShown         bool
	Fit, FitOn, Tight bool
	Reachable         int
}

// fitRowCase is one action row: where it is drawn, how to find it, and what must always show.
type fitRowCase struct {
	name    string
	base    func() string
	path    string
	row     string
	primary string // a selector inside the row that must always be drawn; "" for none
	menu    bool   // the row has a three-dot menu that must show when something is folded
	minRows int    // how many rows the page must have (a guard against a page that stopped drawing them)
}

// TestFitRowsBrowser: every action row of decision 0044's second half is ONE line at 390, 768, 1024 and 1440 px, at
// standard and large text. A row that folds shows its primary control and the three-dot button, every folded control
// has one visible twin in the opened menu, nothing is cut off by the window's edge and the page never scrolls sideways.
// Large text at 390 px may use the fit script's last-resort wrap (data-ts-fit-tight); nothing else may wrap.
func TestFitRowsBrowser(t *testing.T) {
	b := chromium(t)
	e := a11ySeed(t)
	v := newPodVisual(t)
	podSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r.AddCookie(v.owner)
		v.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(podSrv.Close)
	rw := newRadioWorld(t)
	rw.add(rw.ben, rw.fx.liveURL())
	radioURL := radioServe(t, rw)
	owner := func() string { return e.url("main", "owner") }
	cases := []fitRowCase{
		{"hero", owner, "/", ".ts-hero__actions", "[data-ts-play], [data-ts-read]", false, 1},
		{"episode", owner, "/podcasts/episodes/" + e.PodEpisodeID, ".ts-podep__actions", "[data-ts-play]", true, 1},
		{"show-opened", func() string { return podSrv.URL }, "/podcasts/shows/" + v.shows["Open Only Radio Hour"], ".ts-podshow__actions", ".ts-btn--primary", false, 1},
		{"show-followed", func() string { return podSrv.URL }, "/podcasts/shows/" + v.shows["The Harbour Hour"], ".ts-podshow__actions", ".ts-btn--primary", true, 1},
		{"author", owner, "/authors/" + e.AuthorID, ".ts-person__head", "", false, 1},
		{"series", owner, "/series/" + e.SeriesID, ".ts-series__acts", "", false, 1},
		{"radio-cards", func() string { return radioURL }, "/radio", ".ts-stncard__actions", ".ts-btn", false, 1},
		{"radio-hits", owner, "/radio/search?q=found", ".ts-podhit__actions", ".ts-btn", false, 1},
		{"radio-hits-world", func() string { return radioURL }, "/radio/search?q=cool", ".ts-podhit__actions", ".ts-btn", false, 1},
		{"admin-libraries", owner, "/admin/libraries", ".ts-admin__actions", ".ts-btn--primary", false, 1},
		{"admin-ebooks", owner, "/admin/ebooks", ".ts-pair__actions", ".ts-btn", true, 1},
		{"chapters-rows", owner, "/books/" + e.BookID + "/chapters/edit", ".ts-chapted__actions", ".ts-btn", false, 1},
		{"chapters-bar", owner, "/books/" + e.BookID + "/chapters/edit", ".ts-chapted__bar", ".ts-btn--primary", false, 1},
		{"bookmarks-head", owner, "/books/" + e.BookID + "/bookmarks", ".ts-bookmarks__head", "", true, 1},
		{"admin-user", owner, "/admin/users/" + e.MemberID, ".ts-users__grantactions, .ts-users__addedactions", ".ts-btn", false, 0},
		{"admin-user-stop", owner, "/admin/users/" + e.MemberID + "/stop", ".ts-users__confirm", ".ts-btn", false, 1},
		{"admin-user-disable", owner, "/admin/users/" + e.MemberID + "/disable", ".ts-users__confirm", ".ts-btn", false, 1},
		{"admin-user-remove-all", owner, "/admin/users/" + e.MemberID + "/books/remove", ".ts-users__confirm", ".ts-btn", false, 1},
		{"dup-merge", owner, a11yDupMergePath(e), ".ts-dup__actions", ".ts-btn--primary", false, 1},
		{"book-remove", func() string { return e.url("upload", "owner") }, "/books/" + e.UploadBookID + "/remove", ".ts-upset__btns", ".ts-btn", false, 1},
		{"users-invites", owner, "/admin/users", ".ts-invite__item", "", false, 0},
		{"share-invites", owner, "/share", ".ts-invite__item", "", false, 0},
		{"book-text", owner, "/books/" + e.BookID + "/text", ".ts-pair__actions", ".ts-btn", false, 1},
	}
	// oneLineAlways are the rows of the operator rule of 2026-11-15 ("do not create a new row"): not even the fit
	// script's last-resort wrap is allowed for them.
	oneLineAlways := map[string]bool{"chapters-rows": true, "chapters-bar": true, "bookmarks-head": true, "admin-user": true, "admin-user-stop": true, "admin-user-disable": true, "admin-user-remove-all": true, "dup-merge": true, "book-remove": true, "users-invites": true, "share-invites": true}
	type shot struct{ w, h int }
	sizes := []shot{{390, 900}, {768, 900}, {1024, 900}, {1440, 900}}
	for _, c := range cases {
		tab := b.NewTab(1440, 900, false, true)
		tab.Navigate(c.base() + c.path)
		tab.WaitFor(`document.readyState === "complete"`, 10*time.Second)
		for _, large := range []bool{false, true} {
			if large {
				tab.Eval(`document.documentElement.dataset.textSize = "large"`, nil)
			} else {
				tab.Eval(`delete document.documentElement.dataset.textSize`, nil)
			}
			for _, s := range sizes {
				resizeTab(t, tab, s.w, s.h)
				what := c.name + " " + strconv.Itoa(s.w) + map[bool]string{true: " large", false: ""}[large]
				var ps []rowProbe
				tab.Eval(rowsProbeJS+`(`+jsString(c.row)+`,`+jsString(c.primary)+`)`, &ps)
				if len(ps) < c.minRows {
					t.Errorf("%s: %d rows drawn, want at least %d", what, len(ps), c.minRows)
					continue
				}
				for i, p := range ps {
					what := what + " #" + strconv.Itoa(i)
					t.Logf("%s: n=%d spread=%.0f folded=%v", what, p.N, p.Spread, p.Folded)
					wrapOK := p.Tight && large && !oneLineAlways[c.name]
					if p.Spread > 6 && !wrapOK {
						t.Errorf("%s: the controls are on more than one line (centres %.0f px apart)", what, p.Spread)
					}
					if !p.Primary {
						t.Errorf("%s: the primary control is not drawn", what)
					}
					if p.Clipped || p.Over {
						t.Errorf("%s: a control is cut off or the page scrolls sideways", what)
					}
					if p.Small > 0 {
						t.Errorf("%s: %d controls under 44 px tall", what, p.Small)
					}
					if p.Fit && !p.FitOn {
						t.Errorf("%s: the fit script did not run", what)
					}
					if len(p.Folded) > 0 && !p.MenuShown {
						t.Errorf("%s: %v folded but the three-dot button is not drawn", what, p.Folded)
					}
					if !sameSet(p.Folded, p.Twins) {
						t.Errorf("%s: folded %v but the menu shows twins %v", what, p.Folded, p.Twins)
					}
					if p.Reachable > 0 {
						t.Errorf("%s: %d folded controls can still be reached", what, p.Reachable)
					}
				}
			}
		}
		tab.Close()
	}
}

// TestFitRowsEpisodeTwins: on the episode page at 390 px the star, played mark, download and save folded into the
// menu are all there, the star's twin does what the star button does, and a queue choice in the same menu answers with a
// toast without replacing the menu (its twins stay).
func TestFitRowsEpisodeTwins(t *testing.T) {
	b := chromium(t)
	e := a11ySeed(t)
	tab := b.NewTab(390, 900, true, true)
	tab.Navigate(e.url("main", "owner") + "/podcasts/episodes/" + e.PodEpisodeID)
	tab.WaitFor(`document.querySelector(".ts-podep__actions[data-ts-fit-on]") !== null`, 10*time.Second)
	var folded []string
	tab.Eval(`[...document.querySelectorAll(".ts-podep__actions [data-ts-fit-off]")].filter((e) => !e.hidden).map((e) => e.dataset.tsFitItem)`, &folded)
	if !sameSet(folded, []string{"played", "star", "download", "save"}) && len(folded) < 2 {
		t.Fatalf("at 390 px folded = %v, want most of played, star, download, save", folded)
	}
	// A queue choice: the menu stays in place with its twins.
	tab.Eval(`document.querySelector(".ts-podep__actions [data-ts-fit-menu] summary").click()`, nil)
	tab.Eval(`document.querySelector('.ts-podep__actions [data-ts-fit-menu] form button[type=submit]').click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-toast, [data-ts-toast], [role=status]:not(.ts-banner)") !== null`, 5*time.Second)
	var twins int
	var open bool
	tab.Eval(`document.querySelectorAll(".ts-podep__actions [data-ts-fit-menu] [data-ts-fit-for]").length`, &twins)
	tab.Eval(`document.querySelector(".ts-podep__actions [data-ts-fit-menu]").open`, &open)
	if twins != 4 || open {
		t.Errorf("after a queue choice: %d twins (want 4), menu open = %v (want closed)", twins, open)
	}
	// The star's twin presses the star button: the page reloads with the episode starred (the label turns to its opposite).
	var before string
	tab.Eval(`document.querySelector(".ts-podep__actions [data-ts-fit-item=star] button").getAttribute("aria-pressed")`, &before)
	tab.Eval(`document.querySelector(".ts-podep__actions [data-ts-fit-menu] summary").click()`, nil)
	tab.Eval(`document.querySelector('.ts-podep__actions [data-ts-fit-for=star]').click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-podep__actions [data-ts-fit-item=star] button").getAttribute("aria-pressed") !== `+jsString(before), 8*time.Second)
}

// TestFitRowsInboxTwins: on the Ebooks to pair page at 390 px a pairing card keeps Make one book and folds the later
// answers into the three-dot menu; the "Not the same book" twin presses its form.
func TestFitRowsInboxTwins(t *testing.T) {
	b := chromium(t)
	e := a11ySeed(t)
	tab := b.NewTab(390, 900, true, true)
	tab.Navigate(e.url("main", "owner") + "/admin/ebooks")
	tab.WaitFor(`document.querySelector(".ts-pair__actions[data-ts-fit-on]") !== null`, 10*time.Second)
	var p []rowProbe
	tab.Eval(rowsProbeJS+`(".ts-pair__actions[data-ts-fit]", ".ts-btn")`, &p)
	if len(p) == 0 || len(p[0].Folded) == 0 || !sameSet(p[0].Folded, p[0].Twins) {
		t.Fatalf("pairing row at 390 px: %+v, want folded answers with matching twins", p)
	}
	tab.Eval(`document.querySelector(".ts-pair__actions[data-ts-fit] .ts-moremenu summary").click()`, nil)
	var label string
	tab.Eval(`document.querySelector(".ts-pair__actions[data-ts-fit] [data-ts-fit-for]:not([hidden])").textContent.trim()`, &label)
	if strings.TrimSpace(label) == "" {
		t.Errorf("the first twin has no words")
	}
}
