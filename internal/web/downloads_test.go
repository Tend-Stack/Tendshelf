package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestDownloadsPage: /me/downloads answers a signed-in person with the honest empty state, no control,
// and the nav marks Downloads as the current page. An anonymous visitor is sent to sign in.
func TestDownloadsPage(t *testing.T) {
	a := newTestApp(t)
	ck := a.as(t, users.RoleMember)
	rec := a.do(http.MethodGet, "/me/downloads", nil, nil, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/downloads = %d", rec.Code)
	}
	body := rec.Body.String()
	main := body[strings.Index(body, `id="main"`):]
	main = main[:strings.Index(main, "</main>")]
	if strings.Contains(main, "<button") {
		t.Error("the Downloads page has a button before there is anything to do")
	}
	for _, want := range []string{"<h1", "Downloads</h1>", "No downloads yet",
		"Saving books to listen without a connection is on its way. Until then, books play while your server is in reach."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The nav link to /me/downloads carries aria-current="page" (the rail, the side nav or the tab bar).
	current := regexp.MustCompile(`<a[^>]*href="/me/downloads"[^>]*aria-current="page"|<a[^>]*aria-current="page"[^>]*href="/me/downloads"`)
	if !current.MatchString(body) {
		t.Error("the nav does not mark Downloads current")
	}

	anon := a.do(http.MethodGet, "/me/downloads", nil, nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next=%2Fme%2Fdownloads" {
		t.Errorf("anonymous /me/downloads = %d %s", anon.Code, anon.Header().Get("Location"))
	}
}
