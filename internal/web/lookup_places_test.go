package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// jobProviders is the places a queued lookup or bulk job will ask.
func jobProviders(t *testing.T, e *sweepEnv, id string) string {
	t.Helper()
	var raw string
	if err := e.a.st.Reader().QueryRow(`SELECT payload_json FROM jobs WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p struct{ Providers []string }
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return strings.Join(p.Providers, ",")
}

// checkedPlaces lists the place checkboxes of a page that are ticked.
func checkedPlaces(body string) string {
	var out []string
	for _, tag := range regexp.MustCompile(`<input[^>]*name="place"[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(tag, " checked") {
			out = append(out, regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(tag)[1])
		}
	}
	return strings.Join(out, ",")
}

func TestPlaceChoicesTable(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"nothing saved", nil, ""},
		{"one", []string{"openlibrary"}, "openlibrary"},
		{"other", []string{"itunes"}, "itunes"},
		{"all shows only All", []string{"openlibrary", "itunes"}, "all"},
		{"junk and google ignored", []string{"google", "x"}, ""},
	}
	for _, c := range cases {
		var on []string
		for _, p := range placeChoices(c.in) {
			if p.Checked {
				on = append(on, p.Value)
			}
		}
		if strings.Join(on, ",") != c.want {
			t.Errorf("%s: ticked %v, want %q", c.name, on, c.want)
		}
	}
}

func TestPostedPlacesTable(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
		want string
		set  bool
	}{
		{"default", url.Values{"places_set": {"1"}}, "", true},
		{"open library", url.Values{"places_set": {"1"}, "place": {"openlibrary"}}, "openlibrary", true},
		{"apple books", url.Values{"places_set": {"1"}, "place": {"itunes"}}, "itunes", true},
		{"all", url.Values{"places_set": {"1"}, "place": {"all"}}, "openlibrary,itunes", true},
		{"all beside one", url.Values{"places_set": {"1"}, "place": {"itunes", "all"}}, "openlibrary,itunes", true},
		{"junk and google", url.Values{"places_set": {"1"}, "place": {"google", "audnexus", "<x>"}}, "", true},
		{"an old form", url.Values{"title": {"x"}}, "", false},
	}
	for _, c := range cases {
		r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(c.form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		got, set := postedPlaces(r)
		if strings.Join(got, ",") != c.want || set != c.set {
			t.Errorf("%s: %v %v, want %q %v", c.name, got, set, c.want, c.set)
		}
	}
}

func TestLookupPlacesFlow(t *testing.T) {
	e := sweepSeed(t)
	id := e.book.ID
	sheet := "/books/" + id + "/details?sheet=lookup"
	start := func(form url.Values) string {
		form.Set("title", "Sweepword Lantern")
		rec := e.comparePost(t, sweepOwner, "/books/"+id+"/match", form)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST match: %d", rec.Code)
		}
		m := regexp.MustCompile(`job=([^&]+)$`).FindStringSubmatch(location(rec))
		if m == nil {
			t.Fatalf("Location %q", location(rec))
		}
		return jobProviders(t, e, m[1])
	}
	// A normal search asks the reliable source only, and nothing is ticked.
	if got := checkedPlaces(e.compareGet(sweepOwner, sheet, nil).Body.String()); got != "" {
		t.Errorf("default sheet ticks %q", got)
	}
	if got := start(url.Values{"places_set": {"1"}}); got != "audnexus" {
		t.Errorf("default asks %q", got)
	}
	// Each place alone, and the choice is remembered for the next time.
	if got := start(url.Values{"places_set": {"1"}, "place": {"openlibrary"}}); got != "audnexus,openlibrary" {
		t.Errorf("open library asks %q", got)
	}
	if got := checkedPlaces(e.compareGet(sweepOwner, sheet, nil).Body.String()); got != "openlibrary" {
		t.Errorf("remembered %q", got)
	}
	if got := start(url.Values{"places_set": {"1"}, "place": {"itunes"}}); got != "audnexus,itunes" {
		t.Errorf("apple books asks %q", got)
	}
	// All; the sheet then shows only All ticked.
	if got := start(url.Values{"places_set": {"1"}, "place": {"all"}}); got != "audnexus,openlibrary,itunes" {
		t.Errorf("all asks %q", got)
	}
	if got := checkedPlaces(e.compareGet(sweepOwner, sheet, nil).Body.String()); got != "all" {
		t.Errorf("remembered %q", got)
	}
	// Junk and the retired google are ignored.
	if got := start(url.Values{"places_set": {"1"}, "place": {"google", "bogus"}}); got != "audnexus" {
		t.Errorf("junk asks %q", got)
	}
	// Nothing ticked is a choice too; a form without the marker leaves the saved choice alone.
	start(url.Values{"places_set": {"1"}, "place": {"itunes"}})
	start(url.Values{"place": {"openlibrary"}})
	if got := checkedPlaces(e.compareGet(sweepOwner, sheet, nil).Body.String()); got != "itunes" {
		t.Errorf("an old form changed the saved choice: %q", got)
	}
	// An old saved row that names google reads as none and the sheet still renders.
	if _, err := e.a.st.Writer().Exec(`UPDATE user_settings SET value_json = '{"places":["google"]}' WHERE key = 'lookup.v1'`); err != nil {
		t.Fatal(err)
	}
	rec := e.compareGet(sweepOwner, sheet, nil)
	if rec.Code != http.StatusOK || checkedPlaces(rec.Body.String()) != "" || strings.Contains(rec.Body.String(), "Google") {
		t.Errorf("old google row: %d %q", rec.Code, checkedPlaces(rec.Body.String()))
	}
	// An empty query still refuses, and keeps the box open on the ticked places.
	rec = e.comparePost(t, sweepOwner, "/books/"+id+"/match", url.Values{"places_set": {"1"}, "place": {"itunes"}})
	if rec.Code != http.StatusUnprocessableEntity || checkedPlaces(rec.Body.String()) != "itunes" || !strings.Contains(rec.Body.String(), "<details class=\"ts-lookup__more\" open") {
		t.Errorf("refusal: %d %q", rec.Code, checkedPlaces(rec.Body.String()))
	}
	// A member without edit rights cannot save a choice or start a lookup.
	if rec := e.comparePost(t, sweepMember, "/books/"+id+"/match", url.Values{"title": {"x"}, "places_set": {"1"}, "place": {"all"}}); rec.Code != http.StatusNotFound {
		t.Errorf("member POST match: %d", rec.Code)
	}
}

func TestBulkPlacesFollowTheSameChoice(t *testing.T) {
	e := sweepSeed(t)
	rec := e.comparePost(t, sweepOwner, lookupsLibraryPath(e, "lookups"), url.Values{"places_set": {"1"}, "place": {"itunes"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", rec.Code)
	}
	j, _, found, err := e.a.srv.details.BulkStatus(t.Context(), e.libID)
	if err != nil || !found {
		t.Fatalf("no bulk job: %v %v", found, err)
	}
	if got := jobProviders(t, e, j.ID); got != "audnexus,itunes" {
		t.Errorf("bulk asks %q", got)
	}
	// The sheet on a book page shows the same choice, and so does the Libraries page, with a label on each box.
	if got := checkedPlaces(e.compareGet(sweepOwner, "/books/"+e.book.ID+"/details?sheet=lookup", nil).Body.String()); got != "itunes" {
		t.Errorf("book sheet %q", got)
	}
	if err := e.a.srv.details.StopBulk(t.Context(), e.libID, ""); err != nil {
		t.Fatal(err)
	}
	body := adminHTMLUnescape(e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String())
	if got := checkedPlaces(body); got == "" || strings.Trim(strings.ReplaceAll(got, "itunes", ""), ",") != "" {
		t.Errorf("libraries page ticks %q", got)
	}
	for _, want := range []string{`for="place-openlibrary-`, `for="place-itunes-`, `for="place-all-`, ">Open Library<", ">Apple Books<", ">All<", "Also search"} {
		if !strings.Contains(body, want) {
			t.Errorf("libraries page lacks %q", want)
		}
	}
	if strings.Contains(body, "Google") {
		t.Error("Google Books is still on the Libraries page")
	}
	// A guest has no way in.
	if rec := e.comparePost(t, sweepMember, lookupsLibraryPath(e, "lookups"), url.Values{"place": {"all"}}); rec.Code == http.StatusSeeOther {
		t.Errorf("member started a bulk lookup: %d", rec.Code)
	}
}

// Each place reports in plain words; a failure never shows the site's own answer.
func TestLogLinesSayEachPlacePlainly(t *testing.T) {
	steps := []online.Step{
		{Provider: "audnexus", Label: "Audible catalogue", Query: `"X"`, State: online.StepDone, Count: 1},
		{Provider: "openlibrary", Label: "Open Library", Query: `"X"`, State: online.StepFailed, Code: "busy"},
		{Provider: "itunes", Label: "Apple Books", Query: `"X"`, State: online.StepFailed, Code: "offline"},
		{Provider: "itunes", Label: "Apple Books", State: online.StepFailed, Code: "server"},
		{Provider: "itunes", Label: "Apple Books", State: online.StepFailed, Code: "weird 500 <html>"},
		{Provider: "itunes", Label: "Apple Books", Query: `"X"`, State: online.StepNone},
	}
	var got []string
	for _, l := range logLines(steps) {
		got = append(got, l.Text)
	}
	want := []string{
		`Audible catalogue — "X" — found 1 book`,
		"Open Library is busy right now, try again later.",
		"Could not reach Apple Books.",
		"Apple Books is busy right now, try again later.",
		"Could not search Apple Books, try again later.",
		`Apple Books — "X" — no match`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if strings.Contains(l, "Failed:") || strings.Contains(l, "<html>") {
			t.Errorf("raw text in %q", l)
		}
	}
}

// One place failing never hides another's matches, and when every place failed each is named.
func TestLookupFailureNamesEachPlace(t *testing.T) {
	steps := []online.Step{
		{Label: "Open Library", State: online.StepFailed, Code: "busy"},
		{Label: "Apple Books", State: online.StepFailed, Code: "offline"},
	}
	res := metadata.LookupResult{Errors: map[string]string{"openlibrary": "busy", "itunes": "offline"}, Searched: []string{"openlibrary", "itunes"}}
	if got := lookupFailure(res, steps); got != "Open Library is busy right now, try again later. Could not reach Apple Books." {
		t.Errorf("both failed: %q", got)
	}
	res.Searched = []string{"audnexus", "openlibrary", "itunes"}
	if got := lookupFailure(res, steps); got != "" {
		t.Errorf("a place answered, so there is no whole-lookup failure: %q", got)
	}
}
