package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

func compareSheetPath(e *sweepEnv, job, extra string) string {
	return "/books/" + e.book.ID + "/details?sheet=compare&job=" + job + extra
}

func TestCompareFlow(t *testing.T) {
	e := sweepSeed(t)
	id := e.book.ID

	// Start a lookup: a plain form is sent on to the compare sheet.
	rec := e.comparePost(t, sweepOwner, "/books/"+id+"/match", url.Values{"title": {"Sweepword Lantern"}, "author": {"Imogen Sweepauthor"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST match: %d", rec.Code)
	}
	loc := location(rec)
	m := regexp.MustCompile(`^/books/` + id + `/details\?sheet=compare&job=([^&]+)$`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("Location = %q", loc)
	}
	job := m[1]

	// While the job waits, the sheet polls it and offers "Check again".
	rec = e.compareGet(sweepOwner, loc, nil)
	body := rec.Body.String()
	for _, want := range []string{"Looking up details…", `hx-trigger="every 1s"`, "Check again", `hx-get="/hx/jobs/` + job + `?book=` + id + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("waiting sheet lacks %q", want)
		}
	}
	rec = e.compareGet(sweepOwner, "/hx/jobs/"+job+"?book="+id, compareHx)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), `every 1s`) {
		t.Errorf("polling a waiting job: %d %.120s", rec.Code, rec.Body.String())
	}

	// Apply before the job is done: 409.
	rec = e.comparePost(t, sweepOwner, "/books/"+id+"/match/apply", url.Values{"job": {job}, "c": {"0"}, "use": {"narrators"}})
	if rec.Code != http.StatusConflict {
		t.Errorf("apply of a running lookup: %d", rec.Code)
	}

	// The job finishes: the same address answers with the matches, then the table.
	done := compareSeedJob(t, e, id, true)
	rec = e.compareGet(sweepOwner, "/hx/jobs/"+done+"?book="+id, compareHx)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "Sweepword Lantern Two") || !strings.Contains(body, "Exact match") || strings.Contains(body, "every 1s") {
		t.Errorf("finished lookup: %d %.300s", rec.Code, body)
	}
	table := e.compareGet(sweepOwner, compareSheetPath(e, done, "&c=0"), nil).Body.String()
	if !strings.Contains(table, `class="ts-compare"`) || !strings.Contains(table, "Found a close match: Sweepword Lantern, Online House, 2019.") {
		t.Fatalf("no table: %.400s", compareSheetRegion(table))
	}
	// Default ticks: an empty publisher is filled; the narrator the book already has is not replaced;
	// the title is the same and cannot be ticked; the book has a cover, so the cover is not ticked.
	if in := compareInput(table, "publisher"); !strings.Contains(in, " checked") || strings.Contains(in, "disabled") {
		t.Errorf("publisher input = %s", in)
	}
	if in := compareInput(table, "narrators"); strings.Contains(in, " checked") || strings.Contains(in, "disabled") {
		t.Errorf("narrators input = %s", in)
	}
	if in := compareInput(table, "title"); !strings.Contains(in, "disabled") {
		t.Errorf("title input = %s", in)
	}
	if in := compareInput(table, "cover"); strings.Contains(in, " checked") || in == "" {
		t.Errorf("cover input = %s", in)
	}
	if !strings.Contains(table, "Show all") {
		t.Errorf("the long description has no disclosure")
	}

	// Apply only the narrators.
	rec = e.comparePost(t, sweepOwner, "/books/"+id+"/match/apply", url.Values{"job": {done}, "c": {"0"}, "use": {"narrators"}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/books/"+id+"/details?applied=1" {
		t.Fatalf("apply: %d %q", rec.Code, location(rec))
	}
	people, err := e.a.lib.BookPeople(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var narrators []string
	for _, p := range people {
		if p.Role == library.RoleNarrator {
			narrators = append(narrators, p.Name)
		}
	}
	if strings.Join(narrators, ",") != "Theo Onlinevoice" {
		t.Errorf("narrators = %q", narrators)
	}
	if b, err := e.a.lib.Book(context.Background(), id); err != nil || b.Publisher != "" {
		t.Errorf("publisher changed to %q (%v) although it was not ticked", b.Publisher, err)
	}
	var audits int
	if err := e.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'book.details.apply' AND target = ?`, "book:"+id).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("apply audit rows = %d, %v", audits, err)
	}
	page := e.compareGet(sweepOwner, location(rec), nil).Body.String()
	if !strings.Contains(page, "Used 1 change.") || !strings.Contains(page, "Go back to the details in the files") {
		t.Errorf("details tab after apply lacks the status or the reset form")
	}

	// Refusals: an unknown field and a candidate that is not there.
	for name, form := range map[string]url.Values{
		"unknown use": {"job": {done}, "c": {"0"}, "use": {"password"}},
		"tampered c":  {"job": {done}, "c": {"9"}, "use": {"narrators"}},
		"bad c":       {"job": {done}, "c": {"x"}, "use": {"narrators"}},
	} {
		if rec := e.comparePost(t, sweepOwner, "/books/"+id+"/match/apply", form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, rec.Code)
		}
	}

	// Going back to the files removes what was chosen.
	rec = e.comparePost(t, sweepOwner, "/books/"+id+"/details/reset", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", rec.Code)
	}
	if page := e.compareGet(sweepOwner, "/books/"+id+"/details", nil).Body.String(); strings.Contains(page, "Go back to the details in the files") {
		t.Errorf("the reset form is still offered after a reset")
	}

	// An empty query is refused in plain words, as a full page and as a fragment.
	rec = e.comparePost(t, sweepOwner, "/books/"+id+"/match", url.Values{"title": {"  "}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Type a title, an ASIN or an ISBN to look up.") {
		t.Errorf("empty query page: %d", rec.Code)
	}
	form := url.Values{"title": {""}, "_csrf": {e.a.tokenFor(t, e.ck[sweepOwner])}}
	rec = e.a.do(http.MethodPost, "/books/"+id+"/match", form, compareHx, e.ck[sweepOwner])
	if body := rec.Body.String(); rec.Code != http.StatusUnprocessableEntity || strings.Contains(body, "<html") || !strings.Contains(body, "Type a title, an ASIN or an ISBN") {
		t.Errorf("empty query fragment: %d %.200s", rec.Code, body)
	}
	if n := e.cn.requests(); n != 1 {
		t.Errorf("network requests = %d, want only the cover priming", n)
	}
}

func TestCompareCover(t *testing.T) {
	e := sweepSeed(t)
	path := "/books/" + e.book.ID + "/match/" + e.lookupJob + "/cover/"
	rec := e.compareGet(sweepOwner, path+"0", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "private, max-age=3600" {
		t.Errorf("cover: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}
	for _, n := range []string{"1", "9", "x", "-1"} { // no cover address, out of range, not a number
		if rec := e.compareGet(sweepOwner, path+n, nil); rec.Code != http.StatusNotFound {
			t.Errorf("cover %s: %d, want 404", n, rec.Code)
		}
	}
	before := e.cn.requests()
	e.compareGet(sweepOwner, path+"0", nil)
	if e.cn.requests() != before {
		t.Errorf("serving a cover touched the network")
	}
}

func TestCompareLeak(t *testing.T) {
	e := sweepSeed(t)
	dir := adminAudioDir(t, true)
	_, f := librarytest.NewLibrary(t, e.a.lib, "Other library", dir)
	other := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{Title: "Otherbook Candle", Authors: []string{"Olga Other"}, DurationMs: sweepHourMs, AddedAt: 6, Files: 1})
	job := e.lookupJob // a lookup of the sweep book, asked for through the other book
	for _, c := range []struct {
		name, method, path string
		form               url.Values
	}{
		{"sheet", "GET", "/books/" + other.ID + "/details?sheet=compare&job=" + job + "&c=0", nil},
		{"list", "GET", "/books/" + other.ID + "/details?sheet=compare&job=" + job, nil},
		{"cover", "GET", "/books/" + other.ID + "/match/" + job + "/cover/0", nil},
		{"poll", "GET", "/hx/jobs/" + job + "?book=" + other.ID, nil},
		{"poll without book", "GET", "/hx/jobs/" + job, nil},
		{"apply", "POST", "/books/" + other.ID + "/match/apply", url.Values{"job": {job}, "c": {"0"}, "use": {"narrators"}}},
	} {
		var rec = e.compareGet(sweepOwner, c.path, nil)
		if c.method == "POST" {
			rec = e.comparePost(t, sweepOwner, c.path, c.form)
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", c.name, rec.Code)
		}
		for _, w := range []string{"Sweepword", "Onlinevoice", "Online House"} {
			if strings.Contains(rec.Body.String(), w) {
				t.Errorf("%s: the answer contains %q", c.name, w)
			}
		}
	}
	if rec := e.compareGet(sweepOwner, "/hx/jobs/nosuchjob?book="+e.book.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown job: %d, want 404", rec.Code)
	}
}

func TestCompareUseCount(t *testing.T) {
	e := sweepSeed(t)
	base := compareSheetPath(e, e.lookupJob, "&c=0&picked=1")
	btn := func(extra string) string {
		body := e.compareGet(sweepOwner, base+extra, nil).Body.String()
		return compareTag(body, `<button[^>]*id="compare-use"[^>]*>[^<]*`)
	}
	two := btn("&use=publisher&use=narrators")
	if !strings.Contains(two, "Use 2 changes") || strings.Contains(two, "disabled") {
		t.Errorf("two ticked: %s", two)
	}
	one := btn("&use=publisher")
	if !strings.Contains(one, "Use 1 change<") && !strings.HasSuffix(one, "Use 1 change") {
		t.Errorf("one ticked: %s", one)
	}
	none := btn("")
	if !strings.Contains(none, "Use 0 changes") || !strings.Contains(none, "disabled") {
		t.Errorf("none ticked: %s", none)
	}
	// A field that cannot be ticked (the same value) does not count.
	if same := btn("&use=title"); !strings.Contains(same, "disabled") {
		t.Errorf("title counted: %s", same)
	}
	body := e.compareGet(sweepOwner, base+"&use=publisher", nil).Body.String()
	if in := compareInput(body, "narrators"); strings.Contains(in, " checked") {
		t.Errorf("a picked form must not bring back the default ticks: %s", in)
	}
}

func TestCompareNoJS(t *testing.T) {
	e := sweepSeed(t)
	queued := compareSeedJob(t, e, e.book.ID, false)
	rec := e.compareGet(sweepOwner, compareSheetPath(e, queued, ""), nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<html") || !strings.Contains(body, "<main") {
		t.Fatalf("full page: %d", rec.Code)
	}
	region := compareSheetRegion(body)
	if !strings.Contains(region, "ts-sheet-layer") || !strings.Contains(region, "Check again") ||
		!strings.Contains(region, `href="`+strings.ReplaceAll(compareSheetPath(e, queued, ""), "&", "&amp;")+`"`) {
		t.Errorf("the waiting sheet is not inside the page, or has no way to check again: %.500s", region)
	}
	// A plain GET of the lookup sheet is prefilled from the book.
	region = compareSheetRegion(e.compareGet(sweepOwner, "/books/"+e.book.ID+"/details?sheet=lookup", nil).Body.String())
	for _, want := range []string{`value="Sweepword Lantern"`, "Search more places", "Also search", `name="place" value="openlibrary"`, `name="place" value="itunes"`, `name="place" value="all"`, `for="lkp-place-all"`, "Audible is always searched.", `method="post"`} {
		if !strings.Contains(region, want) {
			t.Errorf("lookup sheet lacks %q", want)
		}
	}
}

func TestCompareCSP(t *testing.T) {
	e := sweepSeed(t)
	queued := compareSeedJob(t, e, e.book.ID, false)
	for name, path := range map[string]string{
		"lookup": "/books/" + e.book.ID + "/details?sheet=lookup",
		"wait":   compareSheetPath(e, queued, ""),
		"list":   compareSheetPath(e, e.lookupJob, ""),
		"table":  compareSheetPath(e, e.lookupJob, "&c=0"),
		"tab":    "/books/" + e.book.ID + "/details?applied=2",
		"admin":  "/admin/libraries",
	} {
		body := e.compareGet(sweepOwner, path, nil).Body.String()
		if strings.Contains(body, " style=") || strings.Contains(body, "hx-on") || strings.Contains(body, "js:") {
			t.Errorf("%s: inline style, hx-on or js: value in the page", name)
		}
		if strings.Contains(compareSheetRegion(body), "<script") {
			t.Errorf("%s: script in the sheet", name)
		}
	}
	for _, path := range []string{"/hx/jobs/" + queued + "?book=" + e.book.ID, "/hx/jobs/" + e.lookupJob + "?book=" + e.book.ID} {
		body := e.compareGet(sweepOwner, path, compareHx).Body.String()
		if strings.Contains(body, " style=") || strings.Contains(body, "<script") || strings.Contains(body, "hx-on") {
			t.Errorf("%s: unsafe markup in the fragment", path)
		}
	}
}

func TestCompareOnlineSwitch(t *testing.T) {
	e := sweepSeed(t)
	tab := "/books/" + e.book.ID + "/details"
	if body := e.compareGet(sweepOwner, tab, nil).Body.String(); !strings.Contains(body, "Look up details online") {
		t.Fatalf("the button is missing while lookups are on")
	}
	if body := e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String(); !strings.Contains(body, "Book details from the internet") || !strings.Contains(body, "Look up details when someone asks") {
		t.Errorf("the owner does not see the switch")
	}
	// Only the owner may turn it off.
	if rec := e.comparePost(t, sweepAdmin, "/admin/online", url.Values{"on": {"0"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("admin switch: %d, want 404", rec.Code)
	}
	if body := e.compareGet(sweepAdmin, "/admin/libraries", nil).Body.String(); strings.Contains(body, "Look up details when someone asks") {
		t.Errorf("the admin sees the owner's switch")
	}
	if rec := e.comparePost(t, sweepOwner, "/admin/online", url.Values{"on": {"maybe"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad value: %d, want 422", rec.Code)
	}
	if rec := e.comparePost(t, sweepOwner, "/admin/online", url.Values{"on": {"0"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("owner switch: %d", rec.Code)
	}
	on, err := metadata.OnlineEnabled(context.Background(), e.a.st.Reader())
	if err != nil || on {
		t.Fatalf("online = %v, %v after switching off", on, err)
	}
	// Off: no button, and every lookup route answers 404 for everybody.
	if body := e.compareGet(sweepOwner, tab, nil).Body.String(); strings.Contains(body, "Look up details online") {
		t.Errorf("the button is still shown while lookups are off")
	}
	id := e.book.ID
	for _, role := range []int{sweepOwner, sweepAdmin} {
		for _, p := range []string{
			tab + "?sheet=lookup", compareSheetPath(e, e.lookupJob, "&c=0"),
			"/hx/jobs/" + e.lookupJob + "?book=" + id, "/books/" + id + "/match/" + e.lookupJob + "/cover/0",
		} {
			if rec := e.compareGet(role, p, nil); rec.Code != http.StatusNotFound {
				t.Errorf("%s GET %s while off: %d, want 404", sweepRoleNames[role], p, rec.Code)
			}
		}
		for p, form := range map[string]url.Values{
			"/books/" + id + "/match":       {"title": {"x"}},
			"/books/" + id + "/match/apply": {"job": {e.lookupJob}, "c": {"0"}, "use": {"narrators"}},
		} {
			if rec := e.comparePost(t, role, p, form); rec.Code != http.StatusNotFound {
				t.Errorf("%s POST %s while off: %d, want 404", sweepRoleNames[role], p, rec.Code)
			}
		}
	}
	if rec := e.comparePost(t, sweepOwner, "/admin/online", url.Values{"on": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("owner switch on: %d", rec.Code)
	}
	if rec := e.compareGet(sweepOwner, tab+"?sheet=lookup", nil); rec.Code != http.StatusOK {
		t.Errorf("lookup sheet after switching on: %d", rec.Code)
	}
}

func TestCompareDisabledAdmin(t *testing.T) {
	e := sweepSeed(t)
	token := e.a.tokenFor(t, e.ck[sweepAdmin])
	if _, err := e.a.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.uid[sweepAdmin]); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/books/" + e.book.ID + "/details?sheet=lookup", compareSheetPath(e, e.lookupJob, "&c=0")} {
		rec := e.compareGet(sweepAdmin, p, nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "Sweepword") {
			t.Errorf("disabled admin GET %s: %d", p, rec.Code)
		}
	}
	rec := e.a.do(http.MethodPost, "/books/"+e.book.ID+"/match", url.Values{"title": {"x"}, "_csrf": {token}}, nil, e.ck[sweepAdmin])
	if rec.Code == http.StatusSeeOther && strings.Contains(location(rec), "sheet=compare") {
		t.Errorf("disabled admin started a lookup")
	}
}

func TestCompareRowsTicks(t *testing.T) {
	cur := library.BookDetail{
		Book: library.Book{ID: "b1", Title: "Same Title", Publisher: "Old House", PublishedYear: 2021},
		Card: library.BookCard{Authors: []library.PersonRef{{Name: "Ann Author"}}, Narrators: []library.PersonRef{{Name: "T. Marsh"}}},
	}
	cand := func(score float64, exact bool, cover bool) metadata.Candidate {
		c := metadata.Candidate{Score: score, Exact: exact, Details: metadata.Details{
			Title: "Same Title", Authors: []string{"Ann Author"}, Narrators: []string{"Theo Marsh"}, Publisher: "New House", Year: 2019,
			Language: "english", Description: strings.Repeat("x", 250)}}
		if cover {
			c.CoverURL, c.CoverCached = "https://m.media-amazon.com/x.jpg", true
		}
		return c
	}
	byField := func(rows []pages.CompareRow, f string) pages.CompareRow {
		for _, r := range rows {
			if r.Field == f {
				return r
			}
		}
		t.Fatalf("no row %q", f)
		return pages.CompareRow{}
	}
	src := func(m map[string]string) map[string]metadata.SourceInfo {
		out := map[string]metadata.SourceInfo{}
		for f, s := range m {
			out[f] = metadata.SourceInfo{Source: s, ObservedAt: 1_730_000_000_000}
		}
		return out
	}
	for _, tc := range []struct {
		name         string
		score        float64
		exact        bool
		src          map[string]string
		field        string
		ticked, dis  bool
		badge        string
		picked       bool
		use          map[string]bool
		cover        bool
		hasCoverNow  bool
		wantNowBadge string
	}{
		{name: "empty value is filled", score: 0.9, field: "language", ticked: true},
		{name: "folder guess is replaced", score: 0.9, src: map[string]string{"narrators": "folder"}, field: "narrators", ticked: true, wantNowBadge: "From file"},
		{name: "tag value is kept", score: 0.9, src: map[string]string{"narrators": "tags"}, field: "narrators", wantNowBadge: "From file"},
		{name: "user edit is kept", score: 0.9, exact: true, src: map[string]string{"narrators": "user"}, field: "narrators", wantNowBadge: "Edited by you"},
		{name: "online value is badged", score: 0.9, src: map[string]string{"narrators": "online"}, field: "narrators", wantNowBadge: "Online · 27 Oct"},
		{name: "weak match ticks nothing", score: 0.5, field: "language"},
		{name: "exact match is good", score: 0.1, exact: true, field: "language", ticked: true},
		{name: "same value is disabled", score: 0.9, field: "title", dis: true},
		{name: "picked untick", score: 0.9, field: "language", picked: true, use: map[string]bool{}},
		{name: "picked tick", score: 0.9, field: "narrators", picked: true, use: map[string]bool{"narrators": true}, ticked: true, wantNowBadge: "From file"},
		{name: "picked tick of a same value", score: 0.9, field: "title", picked: true, use: map[string]bool{"title": true}, dis: true},
		{name: "cover ticked without a cover", score: 0.9, field: "cover", cover: true, ticked: true},
		{name: "cover kept when the book has one", score: 0.9, field: "cover", cover: true, hasCoverNow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cur
			if tc.hasCoverNow {
				c.Card.Cover = &library.CoverRef{SHA: "abcdef0123456789"}
			}
			coverOnline := ""
			if tc.cover {
				coverOnline = "/books/b1/match/j/cover/0"
			}
			rows := compareRows(c, src(tc.src), cand(tc.score, tc.exact, tc.cover), 1_730_000_000_000, tc.use, tc.picked, "/covers/b1", coverOnline)
			r := byField(rows, tc.field)
			if r.Ticked != tc.ticked || r.Disabled != tc.dis {
				t.Errorf("ticked %v disabled %v, want %v %v", r.Ticked, r.Disabled, tc.ticked, tc.dis)
			}
			if tc.wantNowBadge != "" && r.NowBadge != tc.wantNowBadge {
				t.Errorf("badge %q, want %q", r.NowBadge, tc.wantNowBadge)
			}
		})
	}
	rows := compareRows(cur, nil, cand(0.9, false, false), 1_730_000_000_000, nil, false, "", "")
	d := byField(rows, "description")
	if r := []rune(d.Online); len(r) != 201 || len([]rune(d.OnlineMore)) != 250 {
		t.Errorf("description cut to %d runes with %d behind the disclosure", len(r), len([]rune(d.OnlineMore)))
	}
	if r := byField(rows, "publisher"); r.OnlineBadge != "Online · 27 Oct" || r.NowBadge != "From file" {
		t.Errorf("publisher badges = %q / %q", r.NowBadge, r.OnlineBadge)
	}
	for _, r := range compareRows(cur, nil, metadata.Candidate{Score: 0.9, Details: metadata.Details{Title: "Same Title", Publisher: "", Year: 0}}, 0, nil, false, "", "") {
		if !r.Disabled {
			t.Errorf("row %s can be ticked although nothing was found for it", r.Field)
		}
	}
}

// Lookups never start on their own: the service never calls a site while the person only opens sheets.
func TestCompareNoNetworkOnOpen(t *testing.T) {
	e := sweepSeed(t)
	before := e.cn.requests()
	for _, p := range []string{"/books/" + e.book.ID + "/details?sheet=lookup", compareSheetPath(e, e.lookupJob, ""), compareSheetPath(e, e.lookupJob, "&c=0")} {
		e.compareGet(sweepOwner, p, nil)
	}
	e.comparePost(t, sweepOwner, "/books/"+e.book.ID+"/match", url.Values{"title": {"Something"}})
	if e.cn.requests() != before {
		t.Errorf("a request reached the network from the web layer: %d new", e.cn.requests()-before)
	}
}
