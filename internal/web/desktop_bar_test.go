package web

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestDeskBarCrumbs: the pages that sit under the Library name it; the others have no crumb.
func TestDeskBarCrumbs(t *testing.T) {
	for path, want := range map[string]string{
		"/books/b1":         "/libraries",
		"/books/b1/read":    "/libraries",
		"/series/s1":        "/libraries",
		"/authors/a1":       "/libraries",
		"/narrators/n1":     "/libraries",
		"/":                 "",
		"/libraries":        "",
		"/me":               "",
		"/bookshelf-of-all": "",
	} {
		got := crumbFor(path)
		if got.Href != want || (want != "" && got.Label != "Library") {
			t.Errorf("crumbFor(%q) = %+v, want href %q", path, got, want)
		}
	}
}

// TestDeskBarServed: a signed-in page carries the bar only for the desktop cookie, and a browser's
// page has none of it.
func TestDeskBarServed(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	desktop := a.do(http.MethodGet, "/", nil, nil, shellCk("desktop"), owner).Body.String()
	for _, want := range []string{`id="desktop-bar"`, "js/desktop-shell", `aria-label="Close"`, "Quit Tendshelf"} {
		if !strings.Contains(desktop, want) {
			t.Errorf("desktop page lacks %q", want)
		}
	}
	browser := a.do(http.MethodGet, "/", nil, nil, owner).Body.String()
	for _, not := range []string{"desktop-bar", "ts-wbar", "desktop-shell", "Quit Tendshelf"} {
		if strings.Contains(browser, not) {
			t.Errorf("browser page contains %q", not)
		}
	}
}

// TestDeskBarScrollbarRuleShellOnly: the rule that starts the page's scrollbar below the title bar lives in
// the desktop stylesheet, which only the desktop cookie loads, and every selector in it is scoped to the shell.
func TestDeskBarScrollbarRuleShellOnly(t *testing.T) {
	css, err := os.ReadFile("static/css/pages-desktop.css")
	if err != nil {
		t.Fatal(err)
	}
	var rules int
	for _, line := range strings.Split(string(css), "\n") {
		if strings.Contains(line, "::-webkit-scrollbar") {
			rules++
			if !strings.HasPrefix(line, `html[data-shell="desktop"]::-webkit-scrollbar`) {
				t.Errorf("scrollbar rule not scoped to the shell: %q", line)
			}
		}
	}
	if rules == 0 || !strings.Contains(string(css), "::-webkit-scrollbar-track { margin-block-start: 2.75rem;") {
		t.Errorf("desktop stylesheet lacks the track margin rule (%d scrollbar rules)", rules)
	}
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	desktop := a.do(http.MethodGet, "/", nil, nil, shellCk("desktop"), owner).Body.String()
	browser := a.do(http.MethodGet, "/", nil, nil, owner).Body.String()
	if !strings.Contains(desktop, "pages-desktop") {
		t.Error("desktop page does not load pages-desktop.css")
	}
	if strings.Contains(browser, "pages-desktop") {
		t.Error("browser page loads pages-desktop.css")
	}
	for _, name := range []string{"base", "utilities", "components", "layout", "player"} {
		if b, err := os.ReadFile("static/css/" + name + ".css"); err == nil && strings.Contains(string(b), "::-webkit-scrollbar-track") {
			t.Errorf("%s.css styles a scrollbar track for every page", name)
		}
	}
}
