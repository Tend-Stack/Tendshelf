//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestStatsBrowserZonePost: a browser in Asia/Tokyo opens a page; shell.js posts that zone once, the
// setting is stored, and a second page load (in a fresh tab, so sessionStorage is empty) posts nothing.
func TestStatsBrowserZonePost(t *testing.T) {
	b := chromium(t)
	a := newTestApp(t)
	a.owner(t)
	var posts atomic.Int32
	inner := a.signedIn(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/me/settings" {
			posts.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	open := func() {
		tab := b.NewTab(1024, 800, false, false)
		if err := tab.Call("Emulation.setTimezoneOverride", map[string]any{"timezoneId": "Asia/Tokyo"}, nil); err != nil {
			t.Fatal(err)
		}
		tab.Navigate(srv.URL + "/me")
		tab.WaitFor(`document.getElementById("pf-stats") !== null`, 5*time.Second)
		time.Sleep(500 * time.Millisecond) // the post is fire-and-forget
	}
	open()
	if got := settingsOf(t, a).TimeZone; got != "Asia/Tokyo" {
		t.Fatalf("stored zone %q after the first load, want Asia/Tokyo", got)
	}
	if n := posts.Load(); n != 1 {
		t.Fatalf("%d posts after the first load, want 1", n)
	}
	open()
	if n := posts.Load(); n != 1 {
		t.Fatalf("%d posts after the second load, want still 1", n)
	}
}
