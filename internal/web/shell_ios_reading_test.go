package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// I3d (decision 0048): the iPhone app reads downloaded books offline at tsread://app. Names are prefixed TestIOSReading.

// TestIOSReadingOfflineScript: offline.js (the reading flush) loads in both phone apps and stays out of the desktop shell.
func TestIOSReadingOfflineScript(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	for name, c := range map[string]struct {
		cookie *http.Cookie
		want   bool
	}{
		"ios":     {a1IOS, true},
		"android": {&http.Cookie{Name: shellCookie, Value: "android"}, true},
		"desktop": {&http.Cookie{Name: shellCookie, Value: "desktop"}, false},
		"browser": {nil, true},
	} {
		cks := []*http.Cookie{ck}
		if c.cookie != nil {
			cks = append(cks, c.cookie)
		}
		body := a.do(http.MethodGet, "/", nil, nil, cks...).Body.String()
		if got := strings.Contains(body, "js/offline."); got != c.want {
			t.Errorf("%s: page loads offline.js = %v, want %v", name, got, c.want)
		}
	}
}

func TestIOSReadingNoOfflineScript(t *testing.T) {
	for shell, want := range map[string]bool{"": false, "android": false, "ios": false, "desktop": true, "other": true} {
		if got := noOfflineScript(shell); got != want {
			t.Errorf("noOfflineScript(%q) = %v, want %v", shell, got, want)
		}
	}
}

// TestIOSReadingStoredShell: the stored shell the app keeps for tsread://app carries the iPhone mark and the same CSP.
func TestIOSReadingStoredShell(t *testing.T) {
	l := rdrSeed(t)
	rec := l.a.do(http.MethodGet, "/offline/books/"+l.book.ID+"/read", nil, nil, l.owner, a1IOS)
	if rec.Code != http.StatusOK {
		t.Fatalf("stored shell: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-shell="ios"`) {
		t.Error("the stored shell does not carry data-shell=ios")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != contentSecurityPolicy {
		t.Errorf("CSP = %q, want the app's own", csp)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, ` style="`) {
		t.Error("inline script or style in the stored shell")
	}
}

// TestIOSReadingScripts: the pieces that cross the bridge keep their words (text checks of the JS, which a browser test covers).
func TestIOSReadingScripts(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("static/js/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for name, words := range map[string][]string{
		"native-downloads.js": {`"rd ask"`, `"rd done "`, "ts:reading:adopted", "tsReaderOfflineSrc"},
		"reader-offline.js":   {"export function adopt(", "ts.offline.reading.seq", `"tsread:"`},
		"offline.js":          {"ts:reading:adopted"},
	} {
		src := read(name)
		for _, w := range words {
			if !strings.Contains(src, w) {
				t.Errorf("%s lacks %q", name, w)
			}
		}
	}
}
