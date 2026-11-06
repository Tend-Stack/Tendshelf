package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// Automatic and bulk lookups (AL-4): who may use the routes, what the status shows, and that the
// answers stay in place for htmx.

func lookupsLibraryPath(e *sweepEnv, suffix string) string {
	return "/admin/libraries/" + e.libID + "/" + suffix
}

func lookupsAutoAt(t *testing.T, e *sweepEnv) int64 {
	t.Helper()
	l, err := e.a.lib.Library(context.Background(), e.libID)
	if err != nil {
		t.Fatal(err)
	}
	return l.AutoLookupAt
}

func lookupsOff(t *testing.T, e *sweepEnv) {
	t.Helper()
	if err := metadata.SetOnlineEnabled(context.Background(), e.a.st, false, ""); err != nil {
		t.Fatal(err)
	}
}

// lookupsNeedsLook records that the seeded book's lookup ended with several matches.
func lookupsNeedsLook(t *testing.T, e *sweepEnv, outcome string) {
	t.Helper()
	err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error {
		return tx.PutBookLookup(e.book.ID, outcome, "audnexus", "", time.Now().UnixMilli())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoLookupRoutes(t *testing.T) {
	e := sweepSeed(t)
	path := lookupsLibraryPath(e, "auto-lookup")
	// The owner and the admin may turn it on and off; the page answers in place for htmx.
	for _, role := range []int{sweepOwner, sweepAdmin} {
		rec := e.comparePost(t, role, path, url.Values{"on": {"0", "1"}})
		if rec.Code != http.StatusSeeOther || location(rec) != "/admin/libraries" {
			t.Fatalf("%s on: %d -> %q", sweepRoleNames[role], rec.Code, location(rec))
		}
		if lookupsAutoAt(t, e) == 0 {
			t.Errorf("%s: the setting is not on", sweepRoleNames[role])
		}
		rec = e.comparePost(t, role, path, url.Values{"on": {"0"}})
		if rec.Code != http.StatusSeeOther || lookupsAutoAt(t, e) != 0 {
			t.Errorf("%s off: %d, at %d", sweepRoleNames[role], rec.Code, lookupsAutoAt(t, e))
		}
	}
	// htmx gets the Libraries page, not a redirect.
	form := url.Values{"on": {"1"}, "_csrf": {e.a.tokenFor(t, e.ck[sweepOwner])}}
	rec := e.a.do(http.MethodPost, path, form, compareHx, e.ck[sweepOwner])
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), lookupAutoLabel) {
		t.Errorf("htmx: %d, HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if rec = e.comparePost(t, sweepOwner, path, url.Values{"on": {"maybe"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad value: %d", rec.Code)
	}
	// Member, guest and anonymous get 404 (anonymous is sent to sign in), and nothing changes.
	before := lookupsAutoAt(t, e)
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := e.comparePost(t, role, path, url.Values{"on": {"0"}}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", sweepRoleNames[role], rec.Code)
		}
	}
	if rec := e.a.do(http.MethodPost, path, url.Values{"on": {"0"}}, nil); rec.Code == http.StatusOK || rec.Code == http.StatusSeeOther && location(rec) == "/admin/libraries" {
		t.Errorf("anonymous: %d", rec.Code)
	}
	// A missing CSRF token is refused.
	if rec := e.a.do(http.MethodPost, path, url.Values{"on": {"0"}}, nil, e.ck[sweepOwner]); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if lookupsAutoAt(t, e) != before {
		t.Error("a refused request changed the setting")
	}
	// An unknown library is 404.
	if rec := e.comparePost(t, sweepOwner, "/admin/libraries/nope/auto-lookup", url.Values{"on": {"1"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
	// With lookups off every route answers 404 for everyone.
	lookupsOff(t, e)
	for _, p := range []string{"auto-lookup", "lookups", "lookups/stop", "lookups/" + e.book.ID + "/dismiss"} {
		if rec := e.comparePost(t, sweepOwner, lookupsLibraryPath(e, p), url.Values{"on": {"1"}}); rec.Code != http.StatusNotFound {
			t.Errorf("off, POST %s: %d", p, rec.Code)
		}
	}
	if rec := e.compareGet(sweepOwner, lookupsLibraryPath(e, "lookups"), nil); rec.Code != http.StatusNotFound {
		t.Errorf("off, GET lookups: %d", rec.Code)
	}
}

func TestBulkStartStopAndPage(t *testing.T) {
	e := sweepSeed(t)
	rec := e.comparePost(t, sweepOwner, lookupsLibraryPath(e, "lookups"), url.Values{"places_set": {"1"}, "place": {"all"}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/libraries" {
		t.Fatalf("start: %d -> %q", rec.Code, location(rec))
	}
	j, _, found, err := e.a.srv.details.BulkStatus(context.Background(), e.libID)
	if err != nil || !found {
		t.Fatalf("no bulk job: %v %v", found, err)
	}
	var p metadata.BulkPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || len(p.Providers) < 2 {
		t.Errorf("the All box did not add providers: %+v %v", p, err)
	}
	body := adminHTMLUnescape(e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String())
	for _, want := range []string{`hx-get="/hx/jobs/` + j.ID + `"`, `hx-trigger="every 2s"`, "Looking up 0 of 0 books", ">Stop<", lookupAutoHint} {
		if !strings.Contains(body, want) {
			t.Errorf("running page lacks %q", want)
		}
	}
	if strings.Contains(body, `action="/admin/libraries/`+e.libID+`/lookups"`) {
		t.Error("the start button shows while a lookup is running")
	}
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := e.comparePost(t, role, lookupsLibraryPath(e, "lookups/stop"), url.Values{}); rec.Code != http.StatusNotFound {
			t.Errorf("%s stop: %d", sweepRoleNames[role], rec.Code)
		}
	}
	rec = e.comparePost(t, sweepAdmin, lookupsLibraryPath(e, "lookups/stop"), url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("stop: %d", rec.Code)
	}
	body = e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String()
	if strings.Contains(body, `hx-get="/hx/jobs/`+j.ID) || !strings.Contains(body, "The lookup stopped.") || !strings.Contains(body, `action="/admin/libraries/`+e.libID+`/lookups"`) {
		t.Error("the stopped lookup is not shown as stopped with its start button back")
	}
}

func TestBulkJobView(t *testing.T) {
	e := sweepSeed(t)
	ctx := context.Background()
	id, err := e.a.srv.details.StartBulk(ctx, e.libID, "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	set := func(state string, prog metadata.BulkProgress, code string) {
		t.Helper()
		b, _ := json.Marshal(prog)
		e.a.adminSetJobState(t, id, state)
		if _, err := e.a.st.Writer().Exec(`UPDATE jobs SET progress_json = ?, result_json = ?, error_code = ? WHERE id = ?`,
			string(b), string(b), code, id); err != nil {
			t.Fatal(err)
		}
	}
	get := func(role int) *httptest.ResponseRecorder {
		return e.compareGet(role, "/hx/jobs/"+id, compareHx)
	}
	// Running with counts.
	set("running", metadata.BulkProgress{Done: 3, Total: 10, Matched: 1, NeedsLook: 1, NotFound: 1, Failed: 2}, "")
	rec := get(sweepAdmin)
	body := rec.Body.String()
	for _, want := range []string{"Looking up 3 of 10 books · 1 found · 1 need a look · 1 not found · 2 could not be checked", `hx-trigger="every 2s"`} {
		if rec.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("running: %d lacks %q", rec.Code, want)
		}
	}
	if strings.Contains(body, `role="status"`) {
		t.Error("a plain running status has a live region")
	}
	// Paused: the line names the time in the server's zone and the page keeps polling.
	until := time.Now().Add(20 * time.Minute)
	set("running", metadata.BulkProgress{Done: 3, Total: 10, PausedUntil: until.UnixMilli(), PauseReason: metadata.PauseBusy}, "")
	body = get(sweepOwner).Body.String()
	if !strings.Contains(body, "Paused because Audible asked for a break. Continues at "+until.Local().Format("15:04")+".") || !strings.Contains(body, `role="status"`) {
		t.Errorf("paused (busy): %s", body)
	}
	set("running", metadata.BulkProgress{PausedUntil: until.UnixMilli(), PauseReason: metadata.PauseDailyCap}, "")
	if body = adminHTMLUnescape(get(sweepOwner).Body.String()); !strings.Contains(body, "Paused for today to stay within the sites' limits. Continues at ") {
		t.Errorf("paused (daily): %s", body)
	}
	// Done stops polling.
	set("done", metadata.BulkProgress{Done: 5, Total: 5, Matched: 3, NeedsLook: 1, NotFound: 1}, "")
	body = get(sweepOwner).Body.String()
	if !strings.Contains(body, "Done: 3 found, 1 need a look, 1 not found.") || strings.Contains(body, "hx-get") {
		t.Errorf("done: %s", body)
	}
	set("failed", metadata.BulkProgress{}, "provider_refusing")
	if body = get(sweepOwner).Body.String(); !strings.Contains(body, "Audible kept refusing, so Tendshelf stopped.") || strings.Contains(body, "hx-get") {
		t.Errorf("refusing: %s", body)
	}
	// Who may see it.
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := get(role); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", sweepRoleNames[role], rec.Code)
		}
	}
	if rec := e.a.do(http.MethodGet, "/hx/jobs/"+id, nil, compareHx); rec.Code == http.StatusOK {
		t.Error("anonymous sees the job")
	}
	lookupsOff(t, e)
	if rec := get(sweepOwner); rec.Code != http.StatusNotFound {
		t.Errorf("off: %d", rec.Code)
	}
}

func TestNeedsLookPage(t *testing.T) {
	e := sweepSeed(t)
	path := lookupsLibraryPath(e, "lookups")
	if body := e.compareGet(sweepOwner, path, nil).Body.String(); !strings.Contains(body, lookupNeedsEmpty) {
		t.Errorf("empty list: %s", body)
	}
	lookupsNeedsLook(t, e, library.LookupNeedsLook)
	rec := e.compareGet(sweepAdmin, path, nil)
	body := rec.Body.String()
	for _, want := range []string{lookupNeedsTitle, "Sweepword Lantern", "Several possible matches", `href="/books/` + e.book.ID + `/details?sheet=lookup"`, ">Compare<", ">Leave as it is<"} {
		if rec.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("list: %d lacks %q", rec.Code, want)
		}
	}
	// The Libraries page links to it with the count.
	if page := e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String(); !strings.Contains(page, `href="`+path+`"`) || !strings.Contains(page, "1 need a look") {
		t.Error("the Libraries page does not link to the list")
	}
	lookupsNeedsLook(t, e, library.LookupNoMatch)
	if body = e.compareGet(sweepOwner, path, nil).Body.String(); !strings.Contains(body, "Nothing found") {
		t.Error("no-match book is not marked Nothing found")
	}
	for _, role := range []int{sweepMember, sweepGuest} {
		if rec := e.compareGet(role, path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", sweepRoleNames[role], rec.Code)
		}
	}
	// A book of another library cannot be dismissed through this one.
	other, _ := librarytest.NewLibrary(t, e.a.lib, "Other library", adminAudioDir(t, true))
	dis := "/admin/libraries/" + other.ID + "/lookups/" + e.book.ID + "/dismiss"
	if rec := e.comparePost(t, sweepOwner, dis, url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("other library's book: %d", rec.Code)
	}
	if rec := e.comparePost(t, sweepMember, lookupsLibraryPath(e, "lookups/"+e.book.ID+"/dismiss"), url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("member dismiss: %d", rec.Code)
	}
	rec = e.comparePost(t, sweepOwner, lookupsLibraryPath(e, "lookups/"+e.book.ID+"/dismiss"), url.Values{})
	if rec.Code != http.StatusSeeOther || location(rec) != path {
		t.Fatalf("dismiss: %d -> %q", rec.Code, location(rec))
	}
	if body = e.compareGet(sweepOwner, path, nil).Body.String(); strings.Contains(body, "Sweepword Lantern") {
		t.Error("a dismissed book is still listed")
	}
	// htmx dismiss answers the list in place.
	lookupsNeedsLook(t, e, library.LookupNeedsLook)
	form := url.Values{"_csrf": {e.a.tokenFor(t, e.ck[sweepOwner])}}
	rec = e.a.do(http.MethodPost, lookupsLibraryPath(e, "lookups/"+e.book.ID+"/dismiss"), form, compareHx, e.ck[sweepOwner])
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), lookupNeedsTitle) {
		t.Errorf("htmx dismiss: %d", rec.Code)
	}
}

func TestAddFolderAutoLookup(t *testing.T) {
	e := sweepSeed(t)
	ctx := context.Background()
	if body := e.compareGet(sweepOwner, "/admin/libraries/folders/new", nil).Body.String(); !strings.Contains(body, lookupAutoLabel) || strings.Contains(body, `value="1" checked`) {
		t.Error("the add-folder page lacks an unticked checkbox")
	}
	post := func(form url.Values) {
		t.Helper()
		form.Set("folder", adminAudioDir(t, true))
		if rec := e.comparePost(t, sweepOwner, "/admin/libraries/folders", form); rec.Code != http.StatusSeeOther {
			t.Fatalf("add folder: %d %s", rec.Code, rec.Body.String())
		}
	}
	autoOf := func(name string) int64 {
		libs, err := e.a.lib.Libraries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range libs {
			if l.Name == name {
				return l.AutoLookupAt
			}
		}
		t.Fatalf("no library %q", name)
		return 0
	}
	post(url.Values{"new_name": {"Ticked"}, "auto_lookup": {"1"}})
	post(url.Values{"new_name": {"Unticked"}})
	if autoOf("Ticked") == 0 || autoOf("Unticked") != 0 {
		t.Errorf("ticked %d, unticked %d", autoOf("Ticked"), autoOf("Unticked"))
	}
	// Unticked never turns an existing library off.
	if err := e.a.srv.details.SetAutoLookup(ctx, e.libID, true, ""); err != nil {
		t.Fatal(err)
	}
	post(url.Values{"library": {e.libID}})
	if lookupsAutoAt(t, e) == 0 {
		t.Error("adding a folder without the tick turned the setting off")
	}
	lookupsOff(t, e)
	if body := e.compareGet(sweepOwner, "/admin/libraries/folders/new", nil).Body.String(); strings.Contains(body, lookupAutoLabel) {
		t.Error("the checkbox shows while lookups are off")
	}
	post(url.Values{"new_name": {"Off"}, "auto_lookup": {"1"}})
	if autoOf("Off") != 0 {
		t.Error("lookups are off but the library was set to look up new books")
	}
}

func TestSetupFolderAutoLookup(t *testing.T) {
	for _, tick := range []bool{true, false} {
		a := newTestApp(t, compareMod(t, &compareNet{}))
		c, tok := a.anon(t, "/setup")
		rec := a.do(http.MethodPost, "/setup", setupForm(tok), nil, c)
		owner := cookieNamed(rec, "ts_session")
		a.adminPostAs(t, owner, "/setup/server", url.Values{"name": {"Shelf"}})
		body := adminHTMLUnescape(a.do(http.MethodGet, "/setup/folder", nil, nil, owner).Body.String())
		if !strings.Contains(body, lookupAutoLabel) || !strings.Contains(body, lookupAutoHint) {
			t.Fatal("the setup step lacks the checkbox")
		}
		form := url.Values{"folder": {adminAudioDir(t, true)}}
		if tick {
			form.Set("auto_lookup", "1")
		}
		if rec = a.adminPostAs(t, owner, "/setup/folder", form); rec.Code != http.StatusSeeOther {
			t.Fatalf("folder: %d", rec.Code)
		}
		libs, err := a.lib.Libraries(context.Background())
		if err != nil || len(libs) != 1 {
			t.Fatalf("libraries: %v %v", libs, err)
		}
		if (libs[0].AutoLookupAt > 0) != tick {
			t.Errorf("tick %v: auto at %d", tick, libs[0].AutoLookupAt)
		}
	}
}
