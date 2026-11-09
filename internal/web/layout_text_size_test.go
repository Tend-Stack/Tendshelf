package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var textsizeCardRe = regexp.MustCompile(`class="ts-card[ "]`)

// textsizeSet saves a text size for the signed-in owner (Ana).
func textsizeSet(t *testing.T, a *testApp, size string) {
	t.Helper()
	s := settingsOf(t, a)
	s.TextSize = size
	u, _, err := a.srv.users.ByUsername(context.Background(), "ana")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.srv.users.SaveSettings(context.Background(), u.ID, s); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutTextSize(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 8)
	const attr = `data-text-size="large"`

	// Standard text: no attribute, the spine shelf.
	for _, path := range []string{"/", "/me/settings"} {
		if body := browseGet(a, path, false, nil).Body.String(); strings.Contains(body, "data-text-size") {
			t.Errorf("standard %s has the attribute", path)
		}
	}
	if body := browseGet(a, "/", false, nil).Body.String(); !strings.Contains(body, "ts-shelf--scroll") {
		t.Errorf("standard Home lost its spine shelf")
	}

	textsizeSet(t, a, "large")
	for _, path := range []string{"/", "/me/settings", "/libraries"} {
		rec := browseGet(a, path, false, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), attr) {
			t.Errorf("large %s = %d, attribute missing", path, rec.Code)
		}
	}
	home := browseGet(a, "/", false, nil).Body.String()
	if strings.Contains(home, "ts-shelf--scroll") || strings.Contains(home, "ts-shelf__plank") {
		t.Errorf("large Home still has the spine shelf")
	}
	if n := len(textsizeCardRe.FindAllString(home, -1)); n < 1 || n > 4 {
		t.Errorf("large Home has %d cards, want 1 to 4", n)
	}
	if !strings.Contains(home, "ts-home__cards--grid") {
		t.Errorf("large Home lacks the card grid")
	}

	// The Settings page shows the control with the saved choice.
	body := browseGet(a, "/me/settings/accessibility", false, nil).Body.String()
	for _, frag := range []string{`id="st-text-large" name="text_size" value="large" checked`, `id="st-text-standard" name="text_size" value="standard"`, `Text size`, `Larger`} {
		if !strings.Contains(body, frag) {
			t.Errorf("settings page lacks %q", frag)
		}
	}

	// A signed-out page never carries it.
	if body := a.do(http.MethodGet, "/login", nil, nil).Body.String(); strings.Contains(body, "data-text-size") {
		t.Errorf("signed-out page has the attribute")
	}
}

func TestLayoutTextSizeLinkPage(t *testing.T) {
	e := linkNewEnv(t)
	textsizeSet(t, e.a, "large")
	m := linkURLRe.FindStringSubmatch(e.post("/books/"+e.book.ID+"/share/link", url.Values{"link_days": {"7"}}, e.owner).Body.String())
	if m == nil {
		t.Fatal("no link")
	}
	// Even the owner's own signed-in browser sees the standard size on a link page.
	if rec := e.get(m[1]); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data-text-size") {
		t.Errorf("link page = %d, attribute present", rec.Code)
	}
	if rec := e.get(m[1], e.owner); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data-text-size") {
		t.Errorf("link page as the owner = %d, attribute present", rec.Code)
	}
}

func TestSettingsTextSizeSave(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	rec := settingsPost(t, a, ck, "/me/settings", url.Values{"text_size": {"large"}}, false)
	if rec.Code != http.StatusSeeOther || settingsOf(t, a).TextSize != "large" {
		t.Fatalf("plain save = %d, stored %q", rec.Code, settingsOf(t, a).TextSize)
	}
	rec = settingsPost(t, a, ck, "/me/settings", url.Values{"text_size": {"standard"}}, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "" {
		t.Fatalf("htmx save = %d, HX-Refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
	if got := settingsOf(t, a).TextSize; got != "standard" {
		t.Fatalf("stored %q", got)
	}
}
