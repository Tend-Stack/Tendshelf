package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const chlkASIN = "B0TESTBOOK"

// chlkNew is the chapter editor fixture (a two-hour book with four file chapters) on an app with a
// lookup service on a fake network. The job runner is not started, so a test finishes a job by hand.
func chlkNew(t *testing.T) *chedEnv {
	t.Helper()
	a := newTestApp(t, compareMod(t, &compareNet{}))
	f := &bookmarkFx{a: a}
	f.owner, f.admin, f.member = a.as(t, users.RoleOwner), a.as(t, users.RoleAdmin), a.as(t, users.RoleMember)
	f.book = bookmarkBook(t, a, "Lantern Tide")
	return &chedEnv{bookmarkFx: f, guest: a.as(t, users.RoleGuest)}
}

func chlkList(titles ...string) online.ChapterList {
	l := online.ChapterList{ASIN: chlkASIN, Accurate: true, RuntimeMs: 7_200_000}
	for i, title := range titles {
		l.Chapters = append(l.Chapters, online.ChapterItem{StartMs: int64(i) * 1_800_000, Title: title})
	}
	return l
}

// chlkJob queues a chapter lookup for the book and, when a list is given, finishes it with it.
func (e *chedEnv) chlkJob(t *testing.T, bookID string, l *online.ChapterList) string {
	t.Helper()
	id, err := e.a.srv.details.StartChapterLookup(context.Background(), bookID, chlkASIN, "")
	if err != nil {
		t.Fatal(err)
	}
	if l != nil {
		res, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = ? WHERE id = ?`, string(res), id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (e *chedEnv) chlkJobs(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, metadata.KindChapterLookup).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestChapterLookupRoles(t *testing.T) {
	e := chlkNew(t)
	for _, c := range []struct {
		name string
		ck   *http.Cookie
		ok   bool
	}{{"owner", e.owner, true}, {"admin", e.admin, true}, {"member", e.member, false}, {"guest", e.guest, false}} {
		t.Run(c.name, func(t *testing.T) {
			before := e.chlkJobs(t)
			rec := e.a.adminPostAs(t, c.ck, e.path("/lookup"), url.Values{"asin": {"b0testbook"}})
			if c.ok {
				if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), e.edit("?lookup=")) {
					t.Errorf("start: %d %q", rec.Code, location(rec))
				}
				return
			}
			if rec.Code != http.StatusNotFound || e.chlkJobs(t) != before {
				t.Errorf("start: %d, jobs %d -> %d", rec.Code, before, e.chlkJobs(t))
			}
			job := e.chlkJob(t, e.book.ID, nil)
			if code := browseGet(e.a, "/hx/jobs/"+job+"?book="+e.book.ID, true, c.ck).Code; code != http.StatusNotFound {
				t.Errorf("poll: %d", code)
			}
			if code, _ := e.post(t, c.ck, e.path(""), e.form(t, "lookup_all", url.Values{"job": {job}})); code != http.StatusNotFound {
				t.Errorf("use: %d", code)
			}
		})
	}
}

func TestChapterLookupBadASIN(t *testing.T) {
	e := chlkNew(t)
	for _, asin := range []string{"", "short", "B0TESTBOOK1", "B0TEST BOO", "B0TEST-BOO"} {
		rec := e.a.adminPostAs(t, e.owner, e.path("/lookup"), url.Values{"asin": {asin}})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "That does not look like an ASIN.") {
			t.Errorf("%q: %d", asin, rec.Code)
		}
	}
	if n := e.chlkJobs(t); n != 0 {
		t.Errorf("%d jobs started", n)
	}
}

func TestChapterLookupOff(t *testing.T) {
	e := chlkNew(t)
	if err := metadata.SetOnlineEnabled(context.Background(), e.a.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if rec := e.a.adminPostAs(t, e.owner, e.path("/lookup"), url.Values{"asin": {chlkASIN}}); rec.Code != http.StatusNotFound || e.chlkJobs(t) != 0 {
		t.Errorf("start while off: %d", rec.Code)
	}
	if page := browseGet(e.a, e.edit(""), false, e.owner).Body.String(); strings.Contains(page, "Look up chapters") {
		t.Error("the form shows while lookups are off")
	}
}

func TestChapterLookupPageAndPoll(t *testing.T) {
	e := chlkNew(t)
	l := chlkList("One", "Two", "Three", "Four")
	waiting, done := e.chlkJob(t, e.book.ID, nil), ""
	if page := browseGet(e.a, e.edit(""), false, e.owner).Body.String(); !strings.Contains(page, "Look up chapters") || !strings.Contains(page, `name="asin"`) {
		t.Error("the page lacks the lookup form")
	}
	page := browseGet(e.a, e.edit("?lookup="+waiting), false, e.owner).Body.String()
	if !strings.Contains(page, "Asking Audnexus for the chapters.") || !strings.Contains(page, `hx-get="/hx/jobs/`+waiting+`?book=`+e.book.ID+`"`) {
		t.Errorf("waiting box missing")
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE jobs SET state = 'failed', error_code = 'no_chapters', error_text = ? WHERE id = ?`, "Audnexus has no chapters for this book.", waiting); err != nil {
		t.Fatal(err)
	}
	if frag := browseGet(e.a, "/hx/jobs/"+waiting+"?book="+e.book.ID, true, e.owner).Body.String(); !strings.Contains(frag, "Audnexus has no chapters for this book.") || strings.Contains(frag, "hx-get") {
		t.Errorf("failed box: %s", frag)
	}
	// Retry the same book and ASIN: the failed job is not reused, a finished list shows its numbers.
	done = e.chlkJob(t, e.book.ID, &l)
	frag := browseGet(e.a, "/hx/jobs/"+done+"?book="+e.book.ID+"&n=4", true, e.owner).Body.String()
	for _, want := range []string{"Audnexus lists 4 chapters, 2:00:00 long. This book is 2:00:00 long.", "Use titles and times", "Use titles only", `name="job" value="` + done + `"`} {
		if !strings.Contains(frag, want) {
			t.Errorf("done box lacks %q", want)
		}
	}
	if strings.Contains(frag, "may not line up") {
		t.Error("a matching length warns")
	}
	// A different number of rows: no titles-only button. A different length and inaccurate: a warning.
	l2 := chlkList("One", "Two")
	l2.Accurate, l2.RuntimeMs = false, 7_100_000
	other := e.chlkJob(t, e.book.ID, &l2)
	frag = browseGet(e.a, "/hx/jobs/"+other+"?book="+e.book.ID+"&n=4", true, e.owner).Body.String()
	if strings.Contains(frag, "Use titles only") || !strings.Contains(frag, "may not line up") {
		t.Errorf("unequal box: %s", frag)
	}
	// A job of another book is no job: the page, the poll and the book named in the poll.
	b2 := bookmarkBook(t, e.a, "Other Book")
	foreign := e.chlkJob(t, b2.ID, &l)
	if code := browseGet(e.a, e.edit("?lookup="+foreign), false, e.owner).Code; code != http.StatusNotFound {
		t.Errorf("page with a foreign job: %d", code)
	}
	if code := browseGet(e.a, "/hx/jobs/"+foreign+"?book="+e.book.ID, true, e.owner).Code; code != http.StatusNotFound {
		t.Errorf("poll of a foreign job: %d", code)
	}
}

func TestChapterLookupUseAllStoresNothingUntilSave(t *testing.T) {
	e := chlkNew(t)
	l := chlkList("  First  ", "Second", strings.Repeat("x", 600), "")
	l.Chapters = append(l.Chapters, online.ChapterItem{StartMs: 7_200_000, Title: "Past the end"}, online.ChapterItem{StartMs: 9_000_000, Title: "Way past"})
	job := e.chlkJob(t, e.book.ID, &l)
	before := e.stored(t)

	f := e.form(t, "lookup_all", url.Values{"job": {job}, "title_0": {"FORGED"}})
	code, body := e.post(t, e.owner, e.path(""), f)
	if code != http.StatusOK {
		t.Fatalf("use: %d", code)
	}
	for _, want := range []string{`value="First"`, `value="Second"`, `name="origin" value="online"`, `name="n" value="4"`, "Chapter 4", "2 chapters start after the end of this book and were left out.", "Chapters filled in."} {
		if !strings.Contains(body, want) {
			t.Errorf("draft lacks %q", want)
		}
	}
	if strings.Contains(body, "FORGED") || strings.Contains(body, "Past the end") {
		t.Error("the form's titles were used, or a row past the end was kept")
	}
	if strings.Count(body, "xxxxxxxxxx") == 0 || strings.Contains(body, strings.Repeat("x", 501)) {
		t.Error("a long title is cut to 500 letters")
	}
	if got := e.stored(t); len(got) != len(before) || got[0].Title != before[0].Title || got[0].Source != library.ChapterFile {
		t.Errorf("a draft stored something: %+v", got)
	}

	// Save, as the page would post it: the rows are stored as online.
	save := url.Values{"rev": {library.ChaptersRev(before)}, "n": {"4"}, "origin": {"online"}, "op": {"save"}}
	for i, c := range []string{"0:00", "30:00", "1:00:00", "1:30:00"} {
		save.Set("start_"+string(rune('0'+i)), c)
		save.Set("title_"+string(rune('0'+i)), []string{"First", "Second", "Third", "Chapter 4"}[i])
	}
	rec := e.a.adminPostAs(t, e.owner, e.path(""), save)
	got := e.stored(t)
	if rec.Code != http.StatusSeeOther || len(got) != 4 || got[0].Title != "First" || got[3].Source != "online" {
		t.Errorf("save: %d %+v", rec.Code, got)
	}
}

func TestChapterLookupTitlesOnly(t *testing.T) {
	e := chlkNew(t)
	equal := chlkList("Alpha", "Beta", "Gamma", "Delta")
	equal.Chapters[1].StartMs = 123_000 // Audible's times differ; they are not used
	job := e.chlkJob(t, e.book.ID, &equal)
	code, body := e.post(t, e.owner, e.path(""), e.form(t, "lookup_titles", url.Values{"job": {job}, "origin": {"user"}}))
	if code != http.StatusOK {
		t.Fatalf("titles: %d", code)
	}
	for _, want := range []string{`value="Alpha"`, `value="Delta"`, `value="10:00"`, "Titles filled in."} {
		if !strings.Contains(body, want) {
			t.Errorf("draft lacks %q", want)
		}
	}
	if strings.Contains(body, `value="2:03"`) || strings.Contains(body, `name="origin" value="online"`) {
		t.Error("Audible's times or origin were taken")
	}

	// Unequal counts are refused and the draft stays as it was.
	short := chlkList("Alpha", "Beta")
	job2 := e.chlkJob(t, e.book.ID, &short)
	code, body = e.post(t, e.owner, e.path(""), e.form(t, "lookup_titles", url.Values{"job": {job2}}))
	if code != http.StatusOK || !strings.Contains(body, "The number of chapters is not the same") || strings.Contains(body, `value="Alpha"`) {
		t.Errorf("unequal counts: %d", code)
	}
	// A forged job of another book is a 404 and a job still running is not used.
	b2 := bookmarkBook(t, e.a, "Other Book")
	foreign := e.chlkJob(t, b2.ID, &equal)
	if code, _ := e.post(t, e.owner, e.path(""), e.form(t, "lookup_all", url.Values{"job": {foreign}})); code != http.StatusNotFound {
		t.Errorf("foreign job: %d", code)
	}
	if code, _ := e.post(t, e.owner, e.path(""), e.form(t, "lookup_all", url.Values{"job": {"jb_nothing"}})); code != http.StatusNotFound {
		t.Errorf("unknown job: %d", code)
	}
	running := e.chlkJob(t, e.book.ID, nil)
	if _, err := e.a.st.Writer().Exec(`UPDATE jobs SET state = 'running', lease_until = 4102444800000 WHERE id = ?`, running); err != nil {
		t.Fatal(err)
	}
	if code, body := e.post(t, e.owner, e.path(""), e.form(t, "lookup_all", url.Values{"job": {running}})); code != http.StatusOK || !strings.Contains(body, "The chapters are not ready yet.") {
		t.Errorf("running job: %d", code)
	}
}
