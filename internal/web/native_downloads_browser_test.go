//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// a7Stub is a4Stub with the second way the app's object delivers messages, addEventListener, which native-downloads.js
// uses so it does not take over onmessage from native-audio.js.
const a7Stub = `(() => {
  window.__sent = [];
  const listeners = [];
  window.tsNative = {
    postMessage: (m) => window.__sent.push(m),
    onmessage: null,
    addEventListener: (type, fn) => { if (type === "message") listeners.push(fn); },
  };
  window.__push = (json) => { listeners.forEach((fn) => fn({ data: json })); };
})();`

// TestNativeDownloadsBrowser: on a book page in the app's window the Download button asks how the book stands, says
// "dl add <id>" when pressed, and follows what the app pushes: waiting, a percentage, then the Ready link (A7).
func TestNativeDownloadsBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := playerApp(t)
			tab := b.NewTab(w, 900, w < 768, false)
			_ = tab.Call("Page.enable", nil, nil)
			if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": a7Stub}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Navigate(e.URL + "/?shell=android&next=/books/" + e.book.ID)
			tab.WaitFor(`document.querySelector("[data-ts-download]") !== null`, 10*time.Second)
			sent := func(words string) string { return fmt.Sprintf(`window.__sent.includes(%s)`, cdp.Quote(words)) }
			tab.WaitFor(sent("dl ask "+e.book.ID), 8*time.Second)

			push := func(state string, pct int) {
				tab.Eval(fmt.Sprintf(`window.__push(%s)`, cdp.Quote(fmt.Sprintf(`{"t":"dl","bookId":%q,"state":%q,"pct":%d}`, e.book.ID, state, pct))), nil)
			}
			hidden := func(sel string) bool {
				var h bool
				tab.Eval(fmt.Sprintf(`document.querySelector(%s).hidden`, cdp.Quote(sel)), &h)
				return h
			}
			text := func(sel string) string {
				var s string
				tab.Eval(fmt.Sprintf(`document.querySelector(%s).textContent.trim()`, cdp.Quote(sel)), &s)
				return s
			}

			push("none", 0)
			if hidden("[data-ts-download]") || !hidden("[data-ts-download-status]") || text("[data-ts-download] .ts-book__label") != "Download" {
				t.Errorf("none: button hidden %v, label %q", hidden("[data-ts-download]"), text("[data-ts-download] .ts-book__label"))
			}
			tab.Eval(`document.querySelector("[data-ts-download]").click()`, nil)
			tab.WaitFor(sent("dl add "+e.book.ID), 5*time.Second)

			push("queued", 0)
			if text("[data-ts-download] .ts-book__label") != "Waiting" {
				t.Errorf("queued label %q", text("[data-ts-download] .ts-book__label"))
			}
			push("downloading", 40)
			if got := text("[data-ts-download-pct]"); got != "40%" || hidden("[data-ts-download-pct]") {
				t.Errorf("percent overlay %q", got)
			}
			var named string
			tab.Eval(`document.querySelector("[data-ts-download]").getAttribute("aria-label")`, &named)
			if named != "Downloading · 40%" {
				t.Errorf("button name %q", named)
			}
			if got := text("[data-ts-download] .ts-book__label"); got != "Download" {
				t.Errorf("downloading label %q", got)
			}
			var now string
			tab.Eval(`document.querySelector("[data-ts-download-bar]").getAttribute("aria-valuenow") + "/" + document.querySelector("[data-ts-download-bar]").hidden`, &now)
			if now != "40/false" {
				t.Errorf("progress bar %q, want 40/false", now)
			}
			// A total not known yet: plain words and a bar that is busy, not a stuck 0 %.
			tab.Eval(fmt.Sprintf(`window.__push(%s)`, cdp.Quote(fmt.Sprintf(`{"t":"dl","bookId":%q,"state":"downloading","pct":0,"known":false}`, e.book.ID))), nil)
			if got := text("[data-ts-download] .ts-book__label"); got != "Downloading…" {
				t.Errorf("unknown-size label %q", got)
			}
			var busy bool
			tab.Eval(`document.querySelector("[data-ts-download-bar]").classList.contains("ts-bar--busy")`, &busy)
			if !busy {
				t.Error("an unknown total shows no moving bar")
			}
			push("downloading", 40)
			var disabled bool
			tab.Eval(`document.querySelector("[data-ts-download]").disabled`, &disabled)
			if !disabled {
				t.Error("a running download's button can be pressed again")
			}
			push("ready", 100)
			if !hidden("[data-ts-download]") || hidden("[data-ts-download-status]") || text("[data-ts-download-label]") != "Ready offline" {
				t.Errorf("ready: link label %q", text("[data-ts-download-label]"))
			}
			var boost string
			tab.Eval(`document.querySelector("[data-ts-download-status]").getAttribute("hx-boost")`, &boost)
			if boost != "false" {
				t.Errorf("the Ready link is boosted (%q): the app would never see the navigation", boost)
			}
			push("failed", 0)
			if text("[data-ts-download] .ts-book__label") != "Try again" {
				t.Errorf("failed label %q", text("[data-ts-download] .ts-book__label"))
			}
		})
	}
}
