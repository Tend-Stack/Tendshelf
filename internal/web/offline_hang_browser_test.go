//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The hung network (decision 0012, operator bug 2026-11-04): on a phone with a VPN up, airplane mode
// makes requests hang instead of failing. The worker must still answer with the offline shell or the
// stored fragment after its deadline (4 s pages, 3 s player), never wait forever.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run OfflineHang ./internal/web/

const offlineHangLimit = 6 * time.Second

// A cold start (closing and reopening the app) while requests hang shows the shell, not a splash.
func TestOfflineHangColdNavigation(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)

	e.stall.Store(true)
	start := time.Now()
	tab.Navigate(e.URL + "/")
	tab.WaitFor(`document.querySelector("#main [data-ts-offline-shell]")`, offlineHangLimit)
	if d := time.Since(start); d > offlineHangLimit {
		t.Errorf("the shell took %s, want under %s", d, offlineHangLimit)
	}
	tab.WaitFor(`!document.getElementById("ts-offline").hidden`, 5*time.Second)
}

// A boosted in-app link while requests hang swaps in the shell and shows the banner.
func TestOfflineHangBoostedClick(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)

	e.stall.Store(true)
	start := time.Now()
	tab.Eval(`document.querySelector('a[data-nav="/"]').click()`, nil)
	tab.WaitFor(`document.querySelector("#main [data-ts-offline-shell]")`, offlineHangLimit)
	if d := time.Since(start); d > offlineHangLimit {
		t.Errorf("the shell took %s, want under %s", d, offlineHangLimit)
	}
	tab.WaitFor(`!document.getElementById("ts-offline").hidden`, 5*time.Second)
}

// The player fragment of a stored book comes from the cache when the network hangs.
func TestOfflineHangPlayerFragment(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)

	e.stall.Store(true)
	var got struct {
		Status int
		Ok     bool
		Ms     int
	}
	tab.Eval(`(async () => {
	  const t0 = performance.now();
	  const res = await fetch("/hx/player/" + `+cdp.Quote(e.book.ID)+`);
	  const html = await res.text();
	  return { Status: res.status, Ok: html.includes("player-data"), Ms: Math.round(performance.now() - t0) };
	})()`, &got)
	if got.Status != 200 || !got.Ok {
		t.Errorf("fragment = %+v, want the stored player fragment", got)
	}
	if got.Ms > 5000 {
		t.Errorf("the fragment took %d ms, want about the 3 s deadline", got.Ms)
	}
}

// With the network healthy, pages and boosted clicks are as fast as before: no artificial delay.
func TestOfflineHangOnlineUnaffected(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)

	start := time.Now()
	tab.Navigate(e.URL + "/")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("online navigation took %s, want under 2s", d)
	}
	var shell bool
	tab.Eval(`Boolean(document.querySelector("[data-ts-offline-shell]"))`, &shell)
	if shell {
		t.Error("the shell showed while online")
	}

	start = time.Now()
	tab.Eval(`document.querySelector('a[data-nav="/libraries"]').click()`, nil)
	tab.WaitFor(`location.pathname === "/libraries" && document.querySelector("#main")`, 3*time.Second)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("online boosted click took %s, want under 2s", d)
	}
	tab.Eval(`Boolean(document.querySelector("[data-ts-offline-shell]"))`, &shell)
	if shell {
		t.Error("the shell showed after an online click")
	}
}
