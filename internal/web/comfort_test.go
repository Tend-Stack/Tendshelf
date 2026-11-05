package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestComfortRoute: the three comfort settings (G5) are saved through the settings route, written on the reader root,
// shown in the sheet, and the script is on the page only for a person who turned one on.
func TestComfortRoute(t *testing.T) {
	l := rdrSeed(t)
	page := func() string {
		t.Helper()
		rec := l.get(l.owner, l.readPath(""), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("reader: %d", rec.Code)
		}
		return rec.Body.String()
	}

	// Off by default: attributes say so, the module is not on the page, but the settings marker knows its address.
	body := page()
	for _, want := range []string{`data-keep-awake="false"`, `data-warm="off"`, `data-auto-hide="false"`, `data-comfort-src="/static/js/reader-comfort`} {
		if !strings.Contains(body, want) {
			t.Errorf("default page lacks %q", want)
		}
	}
	if strings.Count(body, "reader-comfort") != 1 {
		t.Errorf("the default page names reader-comfort.js %d times, want once (the marker's data-comfort-src)", strings.Count(body, "reader-comfort"))
	}

	// Each setting alone brings the module.
	for _, kv := range [][]string{{"keep_awake", "1"}, {"warm", "evening"}, {"warm", "always"}, {"auto_hide", "1"}} {
		if code, _, _ := rsetPost(t, l, l.owner, true, kv...); code != http.StatusNoContent {
			t.Fatalf("%v: %d", kv, code)
		}
		body := page()
		if strings.Count(body, "reader-comfort") != 2 {
			t.Errorf("%v: the page names reader-comfort.js %d times, want twice (marker and module)", kv, strings.Count(body, "reader-comfort"))
		}
		rsetPost(t, l, l.owner, true, "keep_awake", "0", "warm", "off", "auto_hide", "0")
		if got := l.rsetStored(t, l.owner); got.KeepAwake || got.AutoHide || got.Warm != "off" {
			t.Errorf("after %v and back: %+v", kv, got)
		}
	}

	// A junk choice is refused and changes nothing.
	if code, _, _ := rsetPost(t, l, l.owner, true, "warm", "midnight"); code != http.StatusBadRequest {
		t.Errorf("warm=midnight: %d, want 400", code)
	}

	// The sheet carries the choices under its "More" disclosure, in the words of the plan.
	rsetPost(t, l, l.owner, true, "warm", "always", "keep_awake", "1")
	sheet := l.get(l.owner, l.readPath("sheet=reading"), false).Body.String()
	for _, want := range []string{"Keep the screen on", "Warm light", "In the evening", "Hide the bar after a moment", `id="rs-warm-always" name="warm" value="always" checked`, "<summary"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the sheet lacks %q", want)
		}
	}
}
