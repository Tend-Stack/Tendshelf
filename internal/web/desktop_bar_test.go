package web

import (
	"net/http"
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
