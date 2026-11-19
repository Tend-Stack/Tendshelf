package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// I3b (decision 0048): the iPhone app's window keeps downloads. Names are prefixed TestIOSDownloads.

func TestIOSDownloadsScripts(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	order := []string{"js/native-audio.", "js/native-downloads.", "js/native-feel."}
	cases := []struct {
		name   string
		cookie *http.Cookie
		want   bool
	}{
		{"ios", a1IOS, true},
		{"android", &http.Cookie{Name: shellCookie, Value: "android"}, true},
		{"desktop", &http.Cookie{Name: shellCookie, Value: "desktop"}, false},
		{"none", nil, false},
	}
	for _, c := range cases {
		cks := []*http.Cookie{ck}
		if c.cookie != nil {
			cks = append(cks, c.cookie)
		}
		body := a.do(http.MethodGet, "/", nil, nil, cks...).Body.String()
		last := -1
		for _, s := range order {
			at := strings.Index(body, s)
			if (at >= 0) != c.want {
				t.Errorf("%s: page has %q = %v, want %v", c.name, s, at >= 0, c.want)
			}
			if at >= 0 && at < last {
				t.Errorf("%s: %q is out of order", c.name, s)
			}
			last = at
		}
		if c.name == "ios" && strings.Contains(body, "native-speech.") {
			t.Errorf("ios page loads native-speech")
		}
	}
}

var downloadsNavLink = regexp.MustCompile(`<a[^>]*href="/me/downloads"[^>]*>`)

func TestIOSDownloadsNavItem(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	for name, cookie := range map[string]*http.Cookie{
		"ios":     a1IOS,
		"android": {Name: shellCookie, Value: "android"},
		"desktop": {Name: shellCookie, Value: "desktop"},
		"browser": nil,
	} {
		cks := []*http.Cookie{ck}
		if cookie != nil {
			cks = append(cks, cookie)
		}
		body := a.do(http.MethodGet, "/", nil, nil, cks...).Body.String()
		links := downloadsNavLink.FindAllString(body, -1)
		if len(links) == 0 {
			t.Fatalf("%s: no Downloads link", name)
		}
		want := name == "ios" || name == "android"
		for _, l := range links {
			if got := strings.Contains(l, `hx-boost="false"`); got != want {
				t.Errorf("%s: Downloads link %s has NoBoost = %v, want %v", name, l, got, want)
			}
		}
	}
}

func TestIOSDownloadsBookButton(t *testing.T) {
	e := sweepSeed(t)
	rec := e.a.do(http.MethodGet, "/books/"+e.book.ID, nil, nil, e.ck[sweepOwner], a1IOS)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-ts-download="`) {
		t.Errorf("ios book page = %d, Download button present = %v", rec.Code, strings.Contains(rec.Body.String(), `data-ts-download="`))
	}
}
