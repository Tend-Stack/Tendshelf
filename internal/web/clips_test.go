package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// clipPost posts the note sheet's form as the person.
func clipPost(t *testing.T, f *bookmarkFx, ck *http.Cookie, id string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("_csrf", f.a.tokenFor(t, ck))
	if htmx {
		return f.a.do(http.MethodPost, "/bookmarks/"+id+"/note", form, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, ck)
	}
	return f.a.do(http.MethodPost, "/bookmarks/"+id+"/note", form, nil, ck)
}

func clipStored(t *testing.T, f *bookmarkFx, id string) progress.Bookmark {
	t.Helper()
	b, err := f.svc.Bookmark(context.Background(), f.ownerID, id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClipSheetAndSave(t *testing.T) {
	f := bookmarkEnv(t)
	bm := bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "Dawn", bookmarkClock)
	sheet := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark="+bm.ID)).Body.String()
	bookmarkWant(t, sheet, `name="kind"`, `value="favourite"`, `value="quote"`, `value="revisit"`, "Ends at", `name="end_at"`,
		"Leave empty for a single moment", "This clip starts at 11:00")

	rec := clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"Dawn"}, "kind": {"quote"}, "end_at": {"11:30"}}, false)
	if rec.Code != http.StatusSeeOther || location(rec) != bookmarkPath(f, "") {
		t.Fatalf("save = %d %q", rec.Code, location(rec))
	}
	got := clipStored(t, f, bm.ID)
	if got.Kind != "quote" || got.EndMs != 690_000 || got.Note != "Dawn" {
		t.Fatalf("stored = %+v", got)
	}
	// The sheet now shows the end and the kind.
	sheet = bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark="+bm.ID)).Body.String()
	bookmarkWant(t, sheet, `value="11:30"`, `value="quote" checked`)

	// An end past the book is clamped to its length (two hours).
	clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"Dawn"}, "end_at": {"3:00:00"}}, false)
	if got := clipStored(t, f, bm.ID); got.EndMs != 7_200_000 || got.Kind != "quote" {
		t.Errorf("clamped = %+v", got)
	}
	// Present but empty clears the end.
	clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"Dawn"}, "end_at": {""}}, false)
	if got := clipStored(t, f, bm.ID); got.EndMs != 0 {
		t.Errorf("cleared = %+v", got)
	}
	// An old client that posts only the note leaves the kind and end alone.
	clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"a"}, "kind": {"revisit"}, "end_at": {"12:00"}}, false)
	clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"Only the note"}}, false)
	if got := clipStored(t, f, bm.ID); got.Note != "Only the note" || got.Kind != "revisit" || got.EndMs != 720_000 {
		t.Errorf("note only = %+v", got)
	}
}

func TestClipSheetErrors(t *testing.T) {
	f := bookmarkEnv(t)
	bm := bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "", bookmarkClock)
	for name, c := range map[string]struct{ end, code, words string }{
		"at the start": {"11:00", "end_before_start", "The end must be after the start."},
		"before":       {"5:00", "end_before_start", "The end must be after the start."},
		"not a time":   {"soon", "end_not_a_time", "Write the end as minutes and seconds"},
	} {
		rec := clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"x"}, "end_at": {c.end}}, false)
		loc := location(rec)
		if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "error="+c.code) {
			t.Errorf("%s: %d %q", name, rec.Code, loc)
			continue
		}
		bookmarkWant(t, bookmarkGet(f, f.owner, loc).Body.String(), c.words, `aria-invalid="true"`)
		rec = clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"x"}, "end_at": {c.end}}, true)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.words) {
			t.Errorf("%s htmx: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := clipPost(t, f, f.owner, bm.ID, url.Values{"note": {"x"}, "kind": {"loud"}}, true); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind = %d", rec.Code)
	}
	if got := clipStored(t, f, bm.ID); got.EndMs != 0 || got.Kind != "bookmark" || got.Note != "" {
		t.Errorf("a refused form changed the bookmark: %+v", got)
	}
}

// Another person, a person who may not listen to the book, and someone signed out all get no write.
func TestClipForbidden(t *testing.T) {
	f := bookmarkEnv(t)
	bm := bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "mine", bookmarkClock)
	for _, ck := range []*http.Cookie{f.admin, f.member} {
		rec := clipPost(t, f, ck, bm.ID, url.Values{"note": {"x"}, "kind": {"quote"}, "end_at": {"12:00"}}, false)
		if rec.Code != http.StatusNotFound {
			t.Errorf("other person = %d, want 404", rec.Code)
		}
	}
	mine := bookmarkAdd(t, f, f.memberID, f.book.ID, 1000, "m", bookmarkClock)
	if rec := clipPost(t, f, f.member, mine.ID, url.Values{"kind": {"quote"}}, false); rec.Code != http.StatusNotFound {
		t.Errorf("member on a refused book = %d, want 404", rec.Code)
	}
	if rec := f.a.do(http.MethodPost, "/bookmarks/"+bm.ID+"/note", url.Values{"kind": {"quote"}}, nil); rec.Code == http.StatusOK || rec.Code == http.StatusNoContent {
		t.Errorf("signed out = %d", rec.Code)
	}
	if got := clipStored(t, f, bm.ID); got.Kind != "bookmark" || got.EndMs != 0 || got.Note != "mine" {
		t.Errorf("a refused write changed the bookmark: %+v", got)
	}
}

func TestClipTabChipsRowsAndScript(t *testing.T) {
	f := bookmarkEnv(t)
	plain := bookmarkAdd(t, f, f.ownerID, f.book.ID, 100_000, "plain", bookmarkClock)
	body := bookmarkGet(f, f.owner, bookmarkPath(f, "")).Body.String()
	bookmarkNotWant(t, body, "/clip.", "data-ts-clip-end", "Clips ·")
	_ = plain

	clip := bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "a line", bookmarkClock)
	if _, err := f.svc.UpdateBookmark(context.Background(), f.ownerID, clip.ID, progress.BookmarkPatch{EndMs: clipPtr(690_000), Kind: clipPtrS("quote")}); err != nil {
		t.Fatal(err)
	}
	body = bookmarkGet(f, f.owner, bookmarkPath(f, "")).Body.String()
	bookmarkWant(t, body, "/clip.", `data-ts-clip-end="690000"`, "Play this clip", "Clips · 1", "Quote · 1", "Bookmark · 1", "Clip · 0:30")
	// The filters.
	body = bookmarkGet(f, f.owner, bookmarkPath(f, "?only=clips")).Body.String()
	bookmarkWant(t, body, "a line")
	bookmarkNotWant(t, body, "plain")
	body = bookmarkGet(f, f.owner, bookmarkPath(f, "?kind=quote")).Body.String()
	bookmarkWant(t, body, "a line")
	bookmarkNotWant(t, body, "plain")
	body = bookmarkGet(f, f.owner, bookmarkPath(f, "?kind=revisit")).Body.String()
	bookmarkWant(t, body, "Nothing is saved as Revisit yet.")
	// Another person's tab has neither the clip nor the script.
	body = bookmarkGet(f, f.admin, bookmarkPath(f, "")).Body.String()
	bookmarkNotWant(t, body, "a line", "/clip.")

	// The export names the kind and the length.
	ex := bookmarkGet(f, f.owner, bookmarkPath(f, "/export?format=txt")).Body.String()
	bookmarkWant(t, ex, "[Quote · Clip 0:30] a line")
	ex = bookmarkGet(f, f.owner, bookmarkPath(f, "/export?format=md")).Body.String()
	bookmarkWant(t, ex, "_Quote · Clip 0:30_ a line")
}

func clipPtr(v int64) *int64    { return &v }
func clipPtrS(v string) *string { return &v }

type clipJSON struct {
	ID         string `json:"id"`
	PositionMs int64  `json:"positionMs"`
	Note       string `json:"note"`
	EndMs      *int64 `json:"endMs"`
	Kind       string `json:"kind"`
	UpdatedAt  int64  `json:"updatedAt"`
	DeletedAt  *int64 `json:"deletedAt"`
}

func TestClipAPI(t *testing.T) {
	e := syncSeed(t)
	path := "/api/v1/books/" + e.book.ID + "/bookmarks"

	// An old body gives a plain bookmark: no end, the default kind.
	var plain clipJSON
	rec := e.send(e.owner, http.MethodPost, path, `{"positionMs":4000,"note":"old"}`)
	apiDecode(t, rec, &plain)
	if rec.Code != 201 || plain.EndMs != nil || plain.Kind != "bookmark" {
		t.Fatalf("old post: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"endMs":null`) {
		t.Errorf("endMs is not null: %s", rec.Body)
	}
	// A new body makes a clip in one call; an end past the book is clamped.
	var clip clipJSON
	rec = e.send(e.owner, http.MethodPost, path, `{"positionMs":5000,"endMs":9000,"kind":"quote","note":"q","clientId":"clip-1"}`)
	apiDecode(t, rec, &clip)
	if rec.Code != 201 || clip.EndMs == nil || *clip.EndMs != 9000 || clip.Kind != "quote" {
		t.Fatalf("clip post: %d %s", rec.Code, rec.Body)
	}
	var again clipJSON
	rec = e.send(e.owner, http.MethodPost, path, `{"positionMs":5000,"endMs":9000,"kind":"quote","note":"q","clientId":"clip-1"}`)
	apiDecode(t, rec, &again)
	if rec.Code != 200 || again.ID != clip.ID {
		t.Errorf("retry: %d %s", rec.Code, rec.Body)
	}
	var far clipJSON
	apiDecode(t, e.send(e.owner, http.MethodPost, path, `{"positionMs":5000,"endMs":99999999999}`), &far)
	if far.EndMs == nil || *far.EndMs != e.book.DurationMs {
		t.Errorf("end not clamped: %+v", far)
	}
	for _, body := range []string{`{"positionMs":5000,"endMs":5000}`, `{"positionMs":5000,"endMs":100}`, `{"positionMs":5000,"kind":"loud"}`} {
		if rec := e.send(e.owner, http.MethodPost, path, body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}

	// PATCH takes any of the three; the old note-only body works; an empty body is 400 naming the field.
	one := "/api/v1/bookmarks/" + plain.ID
	var got clipJSON
	rec = e.send(e.owner, http.MethodPatch, one, `{"endMs":6000,"kind":"revisit"}`)
	apiDecode(t, rec, &got)
	if rec.Code != 200 || got.Note != "old" || got.EndMs == nil || *got.EndMs != 6000 || got.Kind != "revisit" || got.UpdatedAt <= plain.UpdatedAt-1 {
		t.Fatalf("patch clip: %d %s", rec.Code, rec.Body)
	}
	rec = e.send(e.owner, http.MethodPatch, one, `{"note":"new"}`)
	apiDecode(t, rec, &got)
	if rec.Code != 200 || got.Note != "new" || got.EndMs == nil || got.Kind != "revisit" {
		t.Errorf("patch note: %d %s", rec.Code, rec.Body)
	}
	rec = e.send(e.owner, http.MethodPatch, one, `{"endMs":0}`)
	apiDecode(t, rec, &got)
	if rec.Code != 200 || got.EndMs != nil {
		t.Errorf("clear end: %d %s", rec.Code, rec.Body)
	}
	for _, body := range []string{`{}`, `{"endMs":4000}`, `{"endMs":-5}`, `{"kind":"loud"}`} {
		if rec := e.send(e.owner, http.MethodPatch, one, body); rec.Code != 400 {
			t.Errorf("patch %s: %d", body, rec.Code)
		}
	}
	if rec := e.send(e.member, http.MethodPatch, one, `{"kind":"quote"}`); rec.Code != 404 {
		t.Errorf("someone else's patch: %d", rec.Code)
	}

	// The change feed carries the fields, a tombstone keeps them.
	if rec := e.send(e.owner, http.MethodDelete, "/api/v1/bookmarks/"+clip.ID, ""); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	var feed syncFeed[clipJSON]
	apiDecode(t, e.send(e.owner, http.MethodGet, path+"?since=0", ""), &feed)
	seen := 0
	for _, b := range feed.Items {
		if b.ID == clip.ID && b.DeletedAt != nil && b.EndMs != nil && *b.EndMs == 9000 && b.Kind == "quote" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("feed: %+v", feed.Items)
	}
}
