package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// compareSteps is the log the seeded lookup shows: finished, or stopped mid-search for the waiting sheet.
func compareSteps(running bool) []online.Step {
	q := `"Sweepword Lantern" by Imogen Sweepauthor`
	steps := []online.Step{
		{Provider: "audnexus", Label: "Audible catalogue", Query: q, State: online.StepDone, Count: 2},
		{Provider: "audnexus", Label: "Audnexus", Region: "US", Query: "B0SWEEP001", State: online.StepDone, Count: 1, Cached: true},
		{Provider: "audnexus", Label: "Audnexus", Region: "US", Query: "B0SWEEP002", State: online.StepRunning},
		{Provider: "openlibrary", Label: "Open Library", State: online.StepWaiting},
	}
	if running {
		return steps
	}
	steps[2].State, steps[2].Count = online.StepNone, 0
	steps[3] = online.Step{Provider: "openlibrary", Label: "Open Library", Query: q, State: online.StepFailed, Code: "busy"}
	return steps
}

// compareSetProgress gives a queued lookup the progress a running job would have written.
func compareSetProgress(t *testing.T, a *testApp, id string, steps []online.Step) {
	t.Helper()
	b, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET progress_json = ? WHERE id = ?`, string(b), id); err != nil {
		t.Fatal(err)
	}
}

func TestLookupProgressSheet(t *testing.T) {
	e := sweepSeed(t)
	id := e.book.ID
	job := compareSeedJob(t, e, id, false)
	compareSetProgress(t, e.a, job, compareSteps(true))
	poll := "/hx/jobs/" + job + "?book=" + id

	// The waiting fragment: the log lines in order, the privacy line, quick polling, and the plain link.
	rec := e.compareGet(sweepOwner, poll, compareHx)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "<html") {
		t.Fatalf("poll: %d", rec.Code)
	}
	var last int
	for _, want := range []string{
		`Audible catalogue — &#34;Sweepword Lantern&#34; by Imogen Sweepauthor — found 2 books`,
		`Audnexus (US) — B0SWEEP001 — found 1 book (saved earlier)`,
		`Audnexus (US) — B0SWEEP002`,
		`Open Library — waiting`,
		"Only the title, author, ASIN and ISBN leave your server.",
		`hx-trigger="every 1s"`, `hx-swap="outerMorph"`,
		`href="/books/` + id + `/details?sheet=compare&amp;job=` + job + `"`,
		"Check again", "ts-log__dot",
	} {
		i := strings.Index(body, want)
		if i < 0 {
			t.Errorf("waiting fragment lacks %q", want)
		}
		if strings.HasPrefix(want, "Audible") || strings.HasPrefix(want, "Audnexus") || strings.HasPrefix(want, "Open Library") {
			if i < last {
				t.Errorf("%q is out of order", want)
			}
			last = i
		}
	}
	for _, mark := range []string{"✓", "▸", "·"} {
		if !strings.Contains(body, mark) {
			t.Errorf("no mark %q", mark)
		}
	}
	// Check again is an htmx request, not a full page load.
	if strings.Contains(body, `hx-boost="false"`) {
		t.Error("Check again must not turn htmx off")
	}

	// Before the job reports anything there is no list yet, only the privacy line.
	empty := compareSeedJob(t, e, id, false)
	body = e.compareGet(sweepOwner, "/hx/jobs/"+empty+"?book="+id, compareHx).Body.String()
	if strings.Contains(body, "ts-log__list") || !strings.Contains(body, "leave your server") {
		t.Errorf("empty log: %.300s", body)
	}

	// The finished sheet keeps the log collapsed under "How Tendshelf searched".
	done := compareSeedJob(t, e, id, true)
	for _, path := range []string{"/hx/jobs/" + done + "?book=" + id, compareSheetPath(e, done, ""), compareSheetPath(e, done, "&c=0")} {
		body = e.compareGet(sweepOwner, path, compareHx).Body.String()
		i := strings.Index(body, `<details class="ts-log-review">`)
		if i < 0 || strings.HasPrefix(body[i:], `<details class="ts-log-review" open`) {
			t.Fatalf("%s: no collapsed log", path)
		}
		for _, want := range []string{"How Tendshelf searched", "Open Library is busy right now, try again later.", "Audnexus (US) — B0SWEEP002 — no match", "every 1s"} {
			if !strings.Contains(body, want) && want != "every 1s" {
				t.Errorf("%s lacks %q", path, want)
			}
		}
		if strings.Contains(body, "every 1s") {
			t.Errorf("%s still polls", path)
		}
	}
	// Who may look is unchanged: the role matrix and TestCompare* keep the 404 cases.
}

// The owner's switch answers an htmx request with the Libraries page in place: HX-Redirect would
// load the whole window and stop a playing book.
func TestLookupProgressAdminSwitchStaysInPlace(t *testing.T) {
	e := sweepSeed(t)
	form := url.Values{"on": {"1"}, "_csrf": {e.a.tokenFor(t, e.ck[sweepOwner])}}
	rec := e.a.do(http.MethodPost, "/admin/online", form, compareHx, e.ck[sweepOwner])
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), adminOnlineTitle) {
		t.Errorf("htmx: %d, HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if rec = e.comparePost(t, sweepOwner, "/admin/online", url.Values{"on": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("plain form: %d", rec.Code)
	}
}
