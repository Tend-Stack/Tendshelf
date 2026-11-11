//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// steadyDistinct clicks the navigation link to href while recording probe (a JavaScript expression that
// returns an object of numbers, evaluated on every animation frame) for over a second. It returns, per key,
// the distinct values the key took. A key the probe leaves out on a frame is skipped for that frame.
func steadyDistinct(t *testing.T, tab *cdp.Tab, href, probe string) map[string][]float64 {
	t.Helper()
	return steadyClick(t, tab, `[...document.querySelectorAll("#nav-rail a, #nav-side a, #nav-tabbar a")].find((x) => x.offsetWidth > 0 && x.getAttribute("href") === `+cdp.Quote(href)+`)`, probe)
}

// steadyClick is steadyDistinct for any element: find is a JavaScript expression that returns it.
func steadyClick(t *testing.T, tab *cdp.Tab, find, probe string) map[string][]float64 {
	t.Helper()
	var ok bool
	tab.Eval(`(() => {
	  window.tsSteadyProbe = () => `+probe+`;
	  window.tsSteady = {run: true, rows: []};
	  const frame = () => {
	    window.tsSteady.rows.push(window.tsSteadyProbe());
	    if (window.tsSteady.run) requestAnimationFrame(frame);
	  };
	  frame();
	  const link = `+find+`;
	  if (!link) return false;
	  link.click();
	  return true;
	})()`, &ok)
	if !ok {
		t.Fatalf("nothing to click: %s", find)
	}
	time.Sleep(1200 * time.Millisecond)
	var got map[string][]float64
	tab.Eval(`(() => {
	  window.tsSteady.run = false;
	  const out = {};
	  for (const row of window.tsSteady.rows) {
	    for (const key in row) {
	      if (row[key] === null) continue;
	      out[key] = out[key] || [];
	      if (!out[key].includes(row[key])) out[key].push(row[key]);
	    }
	  }
	  return out;
	})()`, &got)
	return got
}

// TestDownloadsPageDoesNotJump: a block that JavaScript reveals above the empty state (storage, settings) used
// to push the empty state down 155-175 px after the page was first painted. The empty state must keep its
// place below the heading on every frame.
func TestDownloadsPageDoesNotJump(t *testing.T) {
	e := nowPanelEnv(t)
	for _, width := range []int{390, 768, 1024, 1440} {
		tab := fullpOpen(t, e, width, 900, false)
		got := steadyDistinct(t, tab, "/me/downloads", `(() => {
		  const heading = document.querySelector("#main h1");
		  const empty = document.querySelector("#main [data-ts-empty]");
		  if (!heading || !empty || location.pathname !== "/me/downloads") return {};
		  return {gap: Math.round((empty.getBoundingClientRect().top - heading.getBoundingClientRect().top) * 10) / 10};
		})()`)
		if len(got["gap"]) < 1 {
			t.Fatalf("%d px: the Downloads page was never watched", width)
		}
		if len(got["gap"]) != 1 {
			t.Errorf("%d px: the empty state moved below the heading, its distance went through %v", width, got["gap"])
		}
	}
}

// TestBookPageDoesNotJump: the book page's Download button is revealed by JavaScript after the page is painted.
// It used to add a button to the action row (the other buttons slid 34 px sideways at 768 and 1024 px) and
// to wrap the row at 1440 px (the tabs and panel dropped 68 px). Every action button and the tab bar must
// keep their place, measured from the top of the book page, on every frame.
func TestBookPageDoesNotJump(t *testing.T) {
	e := nowPanelEnv(t)
	for _, width := range []int{390, 768, 1024, 1440} {
		tab := fullpOpen(t, e, width, 900, false)
		libraries := steadyDistinct(t, tab, "/libraries", `({})`)
		_ = libraries
		tab.WaitFor(`location.pathname === "/libraries" && !!document.querySelector("#main a[href^='/books/']")`, 5*time.Second)
		got := steadyClick(t, tab, `[...document.querySelectorAll("#main a[href^='/books/']")].find((x) => x.offsetWidth > 0 && !x.getAttribute("href").includes("/read"))`, `(() => {
		  const book = document.querySelector("#main .ts-book");
		  if (!book || !location.pathname.startsWith("/books/")) return {};
		  const top = book.getBoundingClientRect().top;
		  const out = {};
		  const tabs = document.getElementById("book-tabs");
		  if (tabs) out.tabsTop = Math.round((tabs.getBoundingClientRect().top - top) * 10) / 10;
		  [...book.querySelectorAll(".ts-book__acts > *")].forEach((el, i) => {
		    if (el.hidden || el.offsetWidth === 0) return;
		    out["act" + i + "Left"] = Math.round(el.getBoundingClientRect().left * 10) / 10;
		  });
		  return out;
		})()`)
		if len(got) == 0 {
			t.Fatalf("%d px: the book page was never watched", width)
		}
		for key, values := range got {
			if len(values) != 1 {
				t.Errorf("%d px: %s moved after the page was painted, it went through %v", width, key, values)
			}
		}
	}
}
