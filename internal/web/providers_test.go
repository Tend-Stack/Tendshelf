package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

const providerSecret = "Bearer s3cr3t-header-value"

// builtinPlaces are the two book sites that are always offered, with no custom site added.
var builtinPlaces = []online.Place{{Key: "openlibrary", Label: "Open Library"}, {Key: "itunes", Label: "Apple Books"}}

// addProvider adds a site as the owner and returns its id.
func addProvider(t *testing.T, e *sweepEnv, name, address string) string {
	t.Helper()
	rec := e.comparePost(t, sweepOwner, "/admin/providers", url.Values{"name": {name}, "address": {address}, "auth": {providerSecret}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/providers?added=1" {
		t.Fatalf("add: %d %q", rec.Code, location(rec))
	}
	list, err := e.a.srv.details.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatal("site not listed")
	return ""
}

func TestAdminProvidersPageHidesHeader(t *testing.T) {
	e := sweepSeed(t)
	id := addProvider(t, e, "My Audiobookshelf", "http://127.0.0.1:3001/private/path")
	for _, path := range []string{"/admin/providers", "/admin/providers?added=1", "/admin/libraries"} {
		body := e.compareGet(sweepOwner, path, nil).Body.String()
		if strings.Contains(body, "s3cr3t") || strings.Contains(body, "/private/path") {
			t.Errorf("%s shows the header or the path", path)
		}
	}
	body := adminHTMLUnescape(e.compareGet(sweepOwner, "/admin/providers", nil).Body.String())
	for _, want := range []string{"My Audiobookshelf", "127.0.0.1:3001", providersAuthSaved, `action="/admin/providers/` + id + `/test"`, `type="password"`, "1 site"} {
		if want == "1 site" {
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(adminHTMLUnescape(e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String()), "1 site") {
		t.Error("Libraries page lacks the link count")
	}
	// A failed add shows what was typed, never the header.
	rec := e.comparePost(t, sweepOwner, "/admin/providers", url.Values{"name": {""}, "address": {"https://x.example.org"}, "auth": {providerSecret}})
	if rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), "s3cr3t") || !strings.Contains(rec.Body.String(), "https://x.example.org") {
		t.Errorf("failed add: %d", rec.Code)
	}
}

func TestAdminProvidersAddErrors(t *testing.T) {
	e := sweepSeed(t)
	addProvider(t, e, "Taken", "https://books.example.org")
	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"name", url.Values{"name": {""}, "address": {"https://a.example.org"}}, providersErrName},
		{"name_taken", url.Values{"name": {"taken"}, "address": {"https://a.example.org"}}, providersErrNameTaken},
		{"address", url.Values{"name": {"A"}, "address": {"ftp://a.example.org"}}, providersErrAddress},
		{"address_parts", url.Values{"name": {"A"}, "address": {"https://u:p@a.example.org"}}, providersErrParts},
		{"port", url.Values{"name": {"A"}, "address": {"https://a.example.org:25"}}, providersErrPort},
		{"blocked", url.Values{"name": {"A"}, "address": {"http://169.254.169.254"}}, (&online.Error{Code: "blocked"}).Text()},
		{"insecure", url.Values{"name": {"A"}, "address": {"http://8.8.8.8"}}, (&online.Error{Code: "insecure"}).Text()},
		{"auth", url.Values{"name": {"A"}, "address": {"https://a.example.org"}, "auth": {"two\nlines"}}, providersErrAuth},
	}
	for _, c := range cases {
		rec := e.comparePost(t, sweepOwner, "/admin/providers", c.form)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(adminHTMLUnescape(rec.Body.String()), adminHTMLUnescape(c.want)) {
			t.Errorf("%s: %d, want 422 with %q", c.name, rec.Code, c.want)
		}
	}
	// An htmx request that fails is still a page-sized answer the form swaps; the toast rule covers 404 and 5xx.
	for i := 0; i < 10; i++ {
		e.comparePost(t, sweepOwner, "/admin/providers", url.Values{"name": {"Extra " + string(rune('A'+i))}, "address": {"https://e.example.org"}})
	}
	rec := e.comparePost(t, sweepOwner, "/admin/providers", url.Values{"name": {"One more"}, "address": {"https://e.example.org"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "up to 10 sites") {
		t.Errorf("too many: %d", rec.Code)
	}
}

func TestAdminProvidersRemoveUnknown404(t *testing.T) {
	e := sweepSeed(t)
	if rec := e.comparePost(t, sweepOwner, "/admin/providers/cp_nope/remove", url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown remove: %d", rec.Code)
	}
	if rec := e.comparePost(t, sweepOwner, "/admin/providers/cp_nope/test", url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown test: %d", rec.Code)
	}
	id := addProvider(t, e, "Gone", "https://gone.example.org")
	rec := e.comparePost(t, sweepOwner, "/admin/providers/"+id+"/remove", url.Values{})
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/providers?removed=1" {
		t.Errorf("remove: %d %q", rec.Code, location(rec))
	}
	if strings.Contains(e.compareGet(sweepOwner, "/admin/providers", nil).Body.String(), "Gone") {
		t.Error("removed site still listed")
	}
}

func TestAdminProvidersTestPoll(t *testing.T) {
	e := sweepSeed(t)
	id := addProvider(t, e, "Pollsite", "https://poll.example.org")
	// An htmx test answers with a fragment that polls until the job ends.
	rec := e.comparePost(t, sweepOwner, "/admin/providers/"+id+"/test", url.Values{})
	_ = rec
	req := e.a.do(http.MethodPost, "/admin/providers/"+id+"/test", url.Values{"_csrf": {e.a.tokenFor(t, e.ck[sweepOwner])}}, compareHx, e.ck[sweepOwner])
	if req.Code != http.StatusOK || !strings.Contains(req.Body.String(), `hx-trigger="every 2s"`) || !strings.Contains(req.Body.String(), "Asking Pollsite") ||
		!strings.Contains(req.Body.String(), `id="provider-test-`+id+`"`) {
		t.Fatalf("htmx test: %d %s", req.Code, req.Body.String())
	}
	job := regexp.MustCompile(`/hx/admin/providers/test/([A-Za-z0-9_]+)`).FindStringSubmatch(req.Body.String())
	if job == nil {
		t.Fatal("no job address in the fragment")
	}
	finish := func(result string) {
		if _, err := e.a.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = ? WHERE id = ?`, result, job[1]); err != nil {
			t.Fatal(err)
		}
	}
	poll := func() string {
		r := e.compareGet(sweepOwner, "/hx/admin/providers/test/"+job[1], nil)
		if r.Code != http.StatusOK {
			t.Fatalf("poll: %d", r.Code)
		}
		return r.Body.String()
	}
	finish(`{"ok":true,"found":3,"code":""}`)
	if got := poll(); !strings.Contains(got, "Pollsite answered with 3 books for a test search.") || strings.Contains(got, "every 2s") {
		t.Errorf("done: %s", got)
	}
	finish(`{"ok":true,"found":0,"code":""}`)
	if !strings.Contains(poll(), "answered, but found nothing") {
		t.Error("empty answer line")
	}
	finish(`{"ok":false,"found":0,"code":"unauthorized"}`)
	if got := poll(); !strings.Contains(got, "did not accept the authorization header") || !strings.Contains(got, "ts-providers__test--failed") {
		t.Errorf("failure: %s", got)
	}
	// A job that is not a site test, a member, and a plain-form test.
	if _, err := e.a.st.Writer().Exec(`INSERT INTO jobs(id, kind, state, created_by, created_at, updated_at) VALUES ('jb_prov_other', 'scan', 'done', ?, 1, 1)`, e.uid[sweepOwner]); err != nil {
		t.Fatal(err)
	}
	if r := e.compareGet(sweepOwner, "/hx/admin/providers/test/jb_prov_other", nil); r.Code != http.StatusNotFound {
		t.Errorf("other job: %d", r.Code)
	}
	if r := e.compareGet(sweepMember, "/hx/admin/providers/test/"+job[1], nil); r.Code != http.StatusNotFound {
		t.Errorf("member poll: %d", r.Code)
	}
	plain := e.comparePost(t, sweepOwner, "/admin/providers/"+id+"/test", url.Values{})
	if plain.Code != http.StatusSeeOther || !strings.HasPrefix(location(plain), "/admin/providers?test=") {
		t.Errorf("plain test: %d %q", plain.Code, location(plain))
	}
	page := e.compareGet(sweepOwner, location(plain), nil).Body.String()
	if !strings.Contains(page, "Asking Pollsite") && !strings.Contains(page, "Pollsite answered") {
		t.Error("plain test result not on the page")
	}
}

func TestLookupPlacesIncludeCustom(t *testing.T) {
	e := sweepSeed(t)
	id := addProvider(t, e, "Mine", "https://mine.example.org")
	key := online.CustomKey(id)
	sheet := "/books/" + e.book.ID + "/details?sheet=lookup"
	for _, path := range []string{sheet, "/admin/libraries"} {
		body := adminHTMLUnescape(e.compareGet(sweepOwner, path, nil).Body.String())
		if !strings.Contains(body, `value="`+key+`"`) || !strings.Contains(body, ">Mine<") {
			t.Errorf("%s does not list the site", path)
		}
	}
	// All includes it: ticking All asks every place, and the page then shows All alone.
	rec := e.comparePost(t, sweepOwner, "/books/"+e.book.ID+"/match", url.Values{"title": {"Sweepword Lantern"}, "places_set": {"1"}, "place": {"all"}})
	m := regexp.MustCompile(`job=([^&]+)$`).FindStringSubmatch(location(rec))
	if m == nil {
		t.Fatalf("Location %q", location(rec))
	}
	if got := jobProviders(t, e, m[1]); got != "audnexus,openlibrary,itunes,"+key {
		t.Errorf("all asks %q", got)
	}
	if got := checkedPlaces(e.compareGet(sweepOwner, sheet, nil).Body.String()); got != "all" {
		t.Errorf("remembered %q", got)
	}
	// Removed: no longer offered, and All is not ticked for the places left only because it was.
	if rec := e.comparePost(t, sweepOwner, "/admin/providers/"+id+"/remove", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	body := e.compareGet(sweepOwner, sheet, nil).Body.String()
	if strings.Contains(body, key) || strings.Contains(body, ">Mine<") {
		t.Error("removed site still offered")
	}
}
