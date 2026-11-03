//go:build dev

package web

import (
	"testing"
	"time"
)

// TestBrowserLargeTextLive: choosing Larger on the Settings page changes the text size at once, with no
// reload; a reload keeps it; and at 1440 px the large-text shell is the rail, not the sidebar (P3-11).
func TestBrowserLargeTextLive(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 1440, 900, true)
	tab.Eval(`window.location.assign("/me/settings/accessibility")`, nil)
	tab.WaitFor(`!!document.querySelector('label[for="st-text-large"]')`, 10*time.Second)
	var side struct {
		Root  float64
		Attr  string
		Side  string
		Rail  string
		Mark  bool
		Count int
	}
	measure := func() {
		tab.Eval(`({ Root: parseFloat(getComputedStyle(document.documentElement).fontSize), Attr: document.documentElement.dataset.textSize || "",
		  Side: getComputedStyle(document.querySelector(".ts-nav-side")).display, Rail: getComputedStyle(document.querySelector(".ts-nav-rail")).display,
		  Mark: window.__mark === 1 })`, &side)
	}
	tab.Eval(`window.__mark = 1`, nil)
	measure()
	if side.Root != 16 || side.Attr != "" || side.Side == "none" {
		t.Fatalf("before: %+v", side)
	}
	tab.Eval(`document.querySelector('label[for="st-text-large"]').click()`, nil)
	tab.WaitFor(`document.documentElement.dataset.textSize === "large"`, 5*time.Second)
	measure()
	if side.Root != 20 || !side.Mark || side.Side != "none" || side.Rail == "none" {
		t.Fatalf("after the click: %+v (root 20, mark kept, sidebar off, rail on)", side)
	}
	// The save lands, and a reload keeps the choice, rendered by the server.
	time.Sleep(800 * time.Millisecond)
	tab.Eval(`window.location.reload()`, nil)
	tab.WaitFor(`document.readyState === "complete" && !window.__mark`, 10*time.Second)
	measure()
	if side.Attr != "large" || side.Root != 20 || side.Side != "none" {
		t.Fatalf("after reload: %+v", side)
	}
	// And back to Standard.
	tab.Eval(`document.querySelector('label[for="st-text-standard"]').click()`, nil)
	tab.WaitFor(`!document.documentElement.dataset.textSize`, 5*time.Second)
	measure()
	if side.Root != 16 || side.Side == "none" {
		t.Fatalf("back to standard: %+v", side)
	}
}
