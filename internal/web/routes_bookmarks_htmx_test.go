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

// The note sheet posts through htmx so the book keeps playing: the answer is a fragment (a toast,
// the refreshed list when the Bookmarks tab is behind the sheet), never a redirect or a full page.

// bmhxPost posts a form the way htmx does: the header, the token, and the page the request came from.
func bmhxPost(t *testing.T, f *bookmarkFx, ck *http.Cookie, path, from string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", f.a.tokenFor(t, ck))
	return f.a.do(http.MethodPost, path, form, func(r *http.Request) {
		r.Header.Set("HX-Request", "true")
		if from != "" {
			r.Header.Set("HX-Current-URL", "http://example.test"+from)
		}
	}, ck)
}

func TestBookmarkSheetHTMX(t *testing.T) {
	f := bookmarkEnv(t)
	stored := func(id string) (progress.Bookmark, error) {
		return f.svc.Bookmark(context.Background(), f.ownerID, id)
	}
	tab := bookmarkPath(f, "")
	bm := bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "Draft", bookmarkClock)
	other := bookmarkAdd(t, f, f.ownerID, f.book.ID, 1_900_000, "", bookmarkClock)

	// The sheet's forms post through htmx and keep the plain form as the fallback.
	sheet := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark="+bm.ID)).Body.String()
	bookmarkWant(t, sheet, `hx-post="/bookmarks/`+bm.ID+`/note"`, `hx-post="/bookmarks/`+bm.ID+`/delete"`, `hx-swap="none"`,
		`method="post" action="/bookmarks/`+bm.ID+`/note"`, `name="_csrf"`)
	if page := bookmarkGet(f, f.owner, tab).Body.String(); !strings.Contains(page, `id="bookmark-items"`) || strings.Contains(page, `hx-swap-oob`) {
		t.Error("the tab has no list region, or the region is out of band on a page")
	}

	// Save from the Bookmarks tab: 200, a toast, the list out of band, the note stored, no redirect.
	rec := bmhxPost(t, f, f.owner, "/bookmarks/"+bm.ID+"/note", tab+"?sheet=bookmark&bookmark="+bm.ID, url.Values{"note": {"  Better <b>  "}})
	body := rec.Body.String()
	if rec.Code != http.StatusOK || location(rec) != "" {
		t.Fatalf("htmx save = %d %q, want 200 and no redirect", rec.Code, location(rec))
	}
	bookmarkWant(t, body, "Note saved.", `hx-swap-oob="beforeend:#toasts"`, `id="bookmark-items"`, `hx-swap-oob="outerHTML"`, "Better &lt;b&gt;")
	bookmarkNotWant(t, body, "<html", "Better <b>", `role="dialog"`)
	bookmarkWant(t, body, `<div id="sheet" hx-swap-oob="innerHTML"></div>`)
	if got, err := stored(bm.ID); err != nil || got.Note != "Better <b>" {
		t.Errorf("stored note %q (%v)", got.Note, err)
	}

	// From another page (the player's sheet over a book page): the toast only, no list region.
	rec = bmhxPost(t, f, f.owner, "/bookmarks/"+bm.ID+"/note", "/books/"+f.book.ID+"/chapters", url.Values{"note": {"Again"}})
	bookmarkWant(t, rec.Body.String(), "Note saved.")
	bookmarkNotWant(t, rec.Body.String(), "bookmark-items")

	// The notes filter is kept: only bookmarks with a note come back.
	rec = bmhxPost(t, f, f.owner, "/bookmarks/"+bm.ID+"/note", tab+"?only=notes", url.Values{"note": {"Kept"}})
	bookmarkWant(t, rec.Body.String(), "Kept", `aria-current="page"`)
	bookmarkNotWant(t, rec.Body.String(), "Chapter 3")

	// A note over the limit: 400 with an error toast and a fragment, the stored note is unchanged.
	rec = bmhxPost(t, f, f.owner, "/bookmarks/"+bm.ID+"/note", tab, url.Values{"note": {strings.Repeat("é", 2001)}})
	if rec.Code != http.StatusBadRequest || location(rec) != "" {
		t.Errorf("htmx long note = %d %q, want 400", rec.Code, location(rec))
	}
	bookmarkWant(t, rec.Body.String(), "Notes can be up to 2,000 characters.", `hx-swap-oob="beforeend:#toasts"`)
	bookmarkNotWant(t, rec.Body.String(), "<html")
	if got, _ := stored(bm.ID); got.Note != "Kept" {
		t.Errorf("a refused note changed it to %q", got.Note)
	}

	// Somebody else's bookmark, a person without the book, and a missing token: refused, nothing changes.
	for _, ck := range []*http.Cookie{f.admin, f.member} {
		for _, p := range []string{"/note", "/delete"} {
			if rec := bmhxPost(t, f, ck, "/bookmarks/"+bm.ID+p, tab, url.Values{"note": {"Stolen"}}); rec.Code != http.StatusNotFound {
				t.Errorf("htmx other person %s = %d, want 404", p, rec.Code)
			}
		}
	}
	rec = f.a.do(http.MethodPost, "/bookmarks/"+bm.ID+"/delete", url.Values{}, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, f.owner)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "hx-swap-oob") {
		t.Errorf("htmx delete without a token = %d, want a 403 toast fragment", rec.Code)
	}
	if got, err := stored(bm.ID); err != nil || got.Note != "Kept" {
		t.Errorf("a refused write changed the bookmark: %q %v", got.Note, err)
	}

	// Delete from the tab: the toast and the list without the bookmark.
	rec = bmhxPost(t, f, f.owner, "/bookmarks/"+bm.ID+"/delete", tab, nil)
	if rec.Code != http.StatusOK || location(rec) != "" {
		t.Fatalf("htmx delete = %d %q, want 200", rec.Code, location(rec))
	}
	bookmarkWant(t, rec.Body.String(), "Bookmark deleted.", `id="bookmark-items"`, "Chapter 3")
	bookmarkNotWant(t, rec.Body.String(), "Chapter 2 · 1:00")
	if _, err := stored(bm.ID); err == nil {
		t.Error("the bookmark is still stored")
	}
	if rec := bmhxPost(t, f, f.owner, "/bookmarks/"+bm.ID+"/delete", tab, nil); rec.Code != http.StatusNotFound {
		t.Errorf("second htmx delete = %d, want 404", rec.Code)
	}

	// Without htmx the same routes still redirect.
	plain := f.a.do(http.MethodPost, "/bookmarks/"+other.ID+"/note", url.Values{"note": {"Plain"}, "_csrf": {f.a.tokenFor(t, f.owner)}}, nil, f.owner)
	if plain.Code != http.StatusSeeOther || location(plain) != tab {
		t.Errorf("plain save = %d %q", plain.Code, location(plain))
	}
}
