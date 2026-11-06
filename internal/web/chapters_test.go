package web

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// chedEnv is the bookmark fixture (a two-hour book with four chapters from the file) seen by the chapter editor.
type chedEnv struct {
	*bookmarkFx
	guest *http.Cookie
}

func chedNew(t *testing.T) *chedEnv {
	t.Helper()
	f := bookmarkEnv(t)
	return &chedEnv{bookmarkFx: f, guest: f.a.as(t, users.RoleGuest)}
}

// path is the POST address (and the reset routes); edit is the page.
func (e *chedEnv) path(suffix string) string { return "/books/" + e.book.ID + "/chapters" + suffix }
func (e *chedEnv) edit(suffix string) string { return e.path("/edit" + suffix) }

func (e *chedEnv) stored(t *testing.T) []library.Chapter {
	t.Helper()
	rows, err := e.a.lib.Chapters(context.Background(), e.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// form is the form of the stored list, as the page draws it, plus the given extras.
func (e *chedEnv) form(t *testing.T, op string, extra url.Values) url.Values {
	t.Helper()
	rows := e.stored(t)
	f := url.Values{"rev": {library.ChaptersRev(rows)}, "n": {strconv.Itoa(len(rows))}, "op": {op}}
	for i, c := range rows {
		f.Set("start_"+strconv.Itoa(i), clockMsText(c.StartMs))
		title := c.Title
		if title == "" {
			title = "Chapter " + strconv.Itoa(i+1)
		}
		f.Set("title_"+strconv.Itoa(i), title)
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func (e *chedEnv) post(t *testing.T, ck *http.Cookie, path string, f url.Values) (code int, body string) {
	t.Helper()
	rec := e.a.adminPostAs(t, ck, path, f)
	return rec.Code, rec.Body.String()
}

func TestChapterEditRoles(t *testing.T) {
	e := chedNew(t)
	for _, c := range []struct {
		name string
		ck   *http.Cookie
		ok   bool
	}{{"owner", e.owner, true}, {"admin", e.admin, true}, {"member", e.member, false}, {"guest", e.guest, false}} {
		t.Run(c.name, func(t *testing.T) {
			want := http.StatusNotFound
			if c.ok {
				want = http.StatusOK
			}
			if rec := browseGet(e.a, e.edit(""), false, c.ck); rec.Code != want {
				t.Errorf("GET: %d, want %d", rec.Code, want)
			}
			if code, _ := e.post(t, c.ck, e.path(""), e.form(t, "del:1", nil)); code != want {
				t.Errorf("POST draft: %d, want %d", code, want)
			}
			if !c.ok {
				if code, _ := e.post(t, c.ck, e.path(""), e.form(t, "save", url.Values{"title_0": {"Hacked"}})); code != http.StatusNotFound {
					t.Errorf("POST save: %d", code)
				}
				if code, _ := e.post(t, c.ck, e.path("/reset"), url.Values{}); code != http.StatusNotFound {
					t.Errorf("POST reset: %d", code)
				}
			}
			if rec := browseGet(e.a, e.path("/reset"), false, c.ck); rec.Code != http.StatusNotFound {
				t.Errorf("GET reset (nothing edited): %d", rec.Code)
			}
		})
	}
	// Anonymous (a share link visitor has no session either): never the page.
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		p := e.path("")
		if m == http.MethodGet {
			p = e.edit("")
		}
		if rec := e.a.do(m, p, nil, nil); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "Edit chapters") {
			t.Errorf("anonymous %s: %d", m, rec.Code)
		}
	}
	if got := e.stored(t); got[0].Title != "Arrival" || got[0].Source != "file" {
		t.Errorf("a refused request changed the rows: %+v", got)
	}
}

func TestChapterEditTextBook404(t *testing.T) {
	e := chedNew(t)
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET kind = 'text' WHERE id = ?`, e.book.ID); err != nil {
		t.Fatal(err)
	}
	if rec := browseGet(e.a, e.edit(""), false, e.owner); rec.Code != http.StatusNotFound {
		t.Errorf("GET text book: %d", rec.Code)
	}
	if code, _ := e.post(t, e.owner, e.path(""), e.form(t, "save", nil)); code != http.StatusNotFound {
		t.Errorf("POST text book: %d", code)
	}
}

func TestChapterEditDraftsStoreNothing(t *testing.T) {
	e := chedNew(t)
	before := e.stored(t)
	cases := []struct {
		name  string
		op    string
		extra url.Values
		want  []string
	}{
		{"add", "add:0", nil, []string{`value="5:00"`}},
		{"add after last", "add:3", nil, []string{`value="1:30:00"`}},
		{"delete", "del:1", nil, nil},
		{"shift", "shift", url.Values{"shift_by": {"-12.5"}, "shift_from": {"2"}}, []string{`value="9:47.500"`, `value="29:47.500"`}},
		{"bad shift", "shift", url.Values{"shift_by": {"-9999"}, "shift_from": {"2"}}, []string{"Nothing was moved"}},
		{"bad shift seconds", "shift", url.Values{"shift_by": {"soon"}}, []string{"Write the move in seconds"}},
		{"bad clock on save", "save", url.Values{"start_1": {"ten"}}, []string{"Write the time like"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := e.post(t, e.owner, e.path(""), e.form(t, c.op, c.extra))
			if code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("the page lacks %q", w)
				}
			}
			if got := e.stored(t); !reflect.DeepEqual(got, before) {
				t.Errorf("a draft stored rows: %+v", got)
			}
		})
	}
	// Deleting the first row makes the next one open the book.
	_, body := e.post(t, e.owner, e.path(""), e.form(t, "del:0", nil))
	if !strings.Contains(body, `name="start_0" value="0:00"`) || strings.Contains(body, "Arrival") {
		t.Error("deleting row 0 left the list not starting at 0:00")
	}
}

func TestChapterEditSave(t *testing.T) {
	e := chedNew(t)
	rec := e.a.adminPostAs(t, e.owner, e.path(""), e.form(t, "save", url.Values{"title_0": {"  Opening "}, "start_1": {"10:00.250"}}))
	if rec.Code != http.StatusSeeOther || location(rec) != e.edit("?saved=1") {
		t.Fatalf("save: %d %q", rec.Code, location(rec))
	}
	got := e.stored(t)
	if len(got) != 4 || got[0].Title != "Opening" || got[1].StartMs != 600_250 || got[0].EndMs != 600_250 || got[3].EndMs != 7_200_000 {
		t.Errorf("stored %+v", got)
	}
	for _, c := range got {
		if c.Source != "user" {
			t.Errorf("source %q", c.Source)
		}
	}
	if page := browseGet(e.a, e.edit("?saved=1"), false, e.owner).Body.String(); !strings.Contains(page, "Chapters saved.") || !strings.Contains(page, "Go back to the chapters in the files") {
		t.Error("the saved page lacks the status line or the reset link")
	}
	// A lookup's rows are stored as online.
	rec = e.a.adminPostAs(t, e.owner, e.path(""), e.form(t, "save", url.Values{"origin": {"online"}, "title_2": {"Middle"}}))
	if rec.Code != http.StatusSeeOther || e.stored(t)[2].Source != "online" {
		t.Errorf("online save: %d %+v", rec.Code, e.stored(t))
	}
	// An htmx save answers 200 with the page (never a redirect that would stop the player).
	rec = e.a.do(http.MethodPost, e.path(""), func() url.Values {
		f := e.form(t, "save", nil)
		f.Set("_csrf", e.a.tokenFor(t, e.owner))
		return f
	}(), hx, e.owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="main"`) || !strings.Contains(rec.Body.String(), "Chapters saved.") {
		t.Errorf("boosted save: %d", rec.Code)
	}
}

func TestChapterEditSaveRefusals(t *testing.T) {
	e := chedNew(t)
	before := e.stored(t)
	cases := []struct {
		name  string
		extra url.Values
		want  string
	}{
		{"stale rev", url.Values{"rev": {"0000000000000000"}}, "changed while you were editing"},
		{"first not zero", url.Values{"start_0": {"0:05"}}, "has to start at 0:00"},
		{"out of order", url.Values{"start_2": {"5:00"}}, "after the one before it"},
		{"past the end", url.Values{"start_3": {"2:00:00"}}, "before the book ends"},
		{"no title", url.Values{"title_1": {"  "}}, "Give this chapter a name"},
		{"bad clock", url.Values{"start_1": {"1:75"}}, "Write the time like"},
		{"nothing", url.Values{"n": {"0"}}, "at least one chapter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := e.post(t, e.owner, e.path(""), e.form(t, "save", c.extra))
			if code != http.StatusOK || !strings.Contains(body, c.want) {
				t.Errorf("%d, lacks %q", code, c.want)
			}
			if got := e.stored(t); !reflect.DeepEqual(got, before) {
				t.Errorf("rows changed: %+v", got)
			}
		})
	}
	if code, _ := e.post(t, e.owner, e.path(""), e.form(t, "save", url.Values{"n": {"1001"}})); code != http.StatusBadRequest {
		t.Errorf("n=1001: %d", code)
	}
	// Missing CSRF token.
	f := e.form(t, "save", nil)
	if rec := e.a.do(http.MethodPost, e.path(""), f, nil, e.owner); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
	// A boosted request that is refused gets the page in #main, not a 5xx.
	f.Set("_csrf", e.a.tokenFor(t, e.owner))
	f.Set("rev", "stale")
	rec := e.a.do(http.MethodPost, e.path(""), f, hx, e.owner)
	if rec.Code >= 500 || !strings.Contains(rec.Body.String(), `id="main"`) {
		t.Errorf("boosted refusal: %d", rec.Code)
	}
}

func TestChapterEditReset(t *testing.T) {
	e := chedNew(t)
	if code, _ := e.post(t, e.owner, e.path(""), e.form(t, "save", url.Values{"title_0": {"Mine"}})); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if rec := browseGet(e.a, e.path("/reset"), false, e.owner); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Replace my chapters") {
		t.Fatalf("confirm page: %d", rec.Code)
	}
	// The fixture's file has no chapters of its own, so the reset gives back an empty list.
	rec := e.a.adminPostAs(t, e.owner, e.path("/reset"), url.Values{})
	if rec.Code != http.StatusSeeOther || location(rec) != e.edit("") {
		t.Errorf("reset: %d %q", rec.Code, location(rec))
	}
	if got := e.stored(t); len(got) != 0 {
		t.Errorf("rows after the reset: %+v", got)
	}
}

func TestChapterEditResetBusy(t *testing.T) {
	e := chedNew(t)
	f := browseLibrary(t, e.a, "Unsettled")
	b := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{Title: "Unread", DurationMs: 600_000, AddedAt: 6, Files: 1, Unsettled: true})
	err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error {
		return tx.ReplaceChapters(b.ID, []library.Chapter{{StartMs: 0, EndMs: 600_000, Title: "Mine", Source: "user"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.a.adminPostAs(t, e.owner, "/books/"+b.ID+"/chapters/reset", url.Values{})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Some files are still being read") {
		t.Errorf("busy reset: %d", rec.Code)
	}
	if got, _ := e.a.lib.Chapters(context.Background(), b.ID); len(got) != 1 || got[0].Title != "Mine" {
		t.Errorf("rows after a busy reset: %+v", got)
	}
}

func TestChapterEditLinkOnBookPage(t *testing.T) {
	e := chedNew(t)
	owner := browseGet(e.a, "/books/"+e.book.ID+"/chapters", false, e.owner).Body.String()
	if !strings.Contains(owner, ">Edit chapters<") {
		t.Error("the owner's Chapters tab lacks the link")
	}
	if member := browseGet(e.a, "/books/"+e.book.ID+"/chapters", false, e.member).Body.String(); strings.Contains(member, "/chapters/edit") {
		t.Error("a member sees the link")
	}
}

func TestChapterEditClock(t *testing.T) {
	good := map[string]int64{"0:00": 0, "1:02": 62_000, "12:30": 750_000, "1:02:03": 3_723_000, "0:00.5": 500, "1:02:03.045": 3_723_045, "10:00.250": 600_250}
	for in, want := range good {
		if got, ok := parseClock(in); !ok || got != want {
			t.Errorf("parseClock(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "5", "1:75", "1:2:3:4", "a:bc", "1:02.", "1:02.1234", "-1:00", "1:60:00", ":30"} {
		if _, ok := parseClock(in); ok {
			t.Errorf("parseClock(%q) accepted", in)
		}
	}
	for ms, want := range map[int64]string{0: "0:00", 62_000: "1:02", 600_250: "10:00.250", 3_723_000: "1:02:03"} {
		if got := clockMsText(ms); got != want {
			t.Errorf("clockMsText(%d) = %q, want %q", ms, got, want)
		}
	}
}
