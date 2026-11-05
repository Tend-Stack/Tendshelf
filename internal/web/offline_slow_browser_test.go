//go:build dev

package web

import (
	"testing"
	"time"
)

// A slow server is not a hung one. Run by:
// TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run OfflineSlow ./internal/web/

// With no shell stored there is nothing else to show, so a page that takes longer than the 4 s
// deadline still arrives. The worker used to answer Response.error() and the browser showed its own
// error page (nightly TestPageWeight, net::ERR_FAILED).
func TestOfflineSlowNoShellGetsPage(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b) // no seed: the worker has no shell stored

	e.lag.Store(6000)
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`location.pathname === "/libraries" && Boolean(document.querySelector("#main")) && !document.querySelector("[data-ts-offline-shell]")`, 20*time.Second)
	var errPage bool
	tab.Eval(`document.URL.startsWith("chrome-error:")`, &errPage)
	if errPage {
		t.Error("the browser showed its own error page, want the real page")
	}
}

// With a shell stored, a slow server lands on the shell after the deadline; while the person is
// online the page then asks again by itself and the real page replaces the shell once the server answers.
func TestOfflineSlowWithShellRecovers(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)

	e.lag.Store(60_000)
	tab.Navigate(e.URL + "/")
	tab.WaitFor(`document.querySelector("#main [data-ts-offline-shell]")`, offlineHangLimit)
	e.lag.Store(0)
	tab.WaitFor(`!document.querySelector("[data-ts-offline-shell]") && Boolean(document.querySelector("#main"))`, 25*time.Second)
}
