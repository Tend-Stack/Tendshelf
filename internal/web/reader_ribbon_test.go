package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestBookmarkRemoveField checks the ribbon's second press: remove=1 takes away the signed-in person's own bookmarks on a
// sentence and nobody else's, and the answer redraws the sentence's block.
func TestBookmarkRemoveField(t *testing.T) {
	l := rdrSeed(t)
	post := "/books/" + l.book.ID + "/bookmarks"
	sid, _ := l.sidIn(0)
	other := l.sids()[2]
	ctx := context.Background()
	readerID := sweepUserID(t, l.a, l.reader)
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "reader": l.reader} {
		rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {sid}}, ck)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s mark: %d %s", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `hx-swap-oob="outerHTML"`) || !strings.Contains(rec.Body.String(), `data-bm="`+sid+`"`) {
			t.Errorf("%s: creating answers without the block redrawn: %s", name, rec.Body.String())
		}
	}
	if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {other}}, l.owner); rec.Code != http.StatusOK {
		t.Fatalf("owner second mark: %d", rec.Code)
	}
	rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {sid}, "remove": {"1"}}, l.reader)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Bookmark removed.") ||
		!strings.Contains(rec.Body.String(), `hx-swap-oob`) || strings.Contains(rec.Body.String(), `data-bm="`+sid+`"`) {
		t.Errorf("reader remove: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := l.a.srv.progress.TextBookmarks(ctx, readerID, l.book.ID); len(got) != 0 {
		t.Errorf("the reader's own bookmark is still there: %v", got)
	}
	if got, _ := l.a.srv.progress.TextBookmarks(ctx, l.ownerID, l.book.ID); len(got) != 2 {
		t.Errorf("the reader's remove touched the owner's bookmarks: %d left, want 2", len(got))
	}
	// Absent is success.
	if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {sid}, "remove": {"1"}}, l.reader); rec.Code != http.StatusOK {
		t.Errorf("absent bookmark: %d", rec.Code)
	}
	// Unknown sentence, half a pair, and people who may not read.
	if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {"c2.p1.s99"}, "remove": {"1"}}, l.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown sid: %d", rec.Code)
	}
	if rec := l.hx(t, http.MethodPost, post, url.Values{"sid": {sid}, "remove": {"1"}}, l.owner); rec.Code != http.StatusBadRequest {
		t.Errorf("sid without edition: %d", rec.Code)
	}
	for name, ck := range map[string]*http.Cookie{"noRead": l.noRead, "guestNoR": l.guestNoR} {
		if rec := l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {sid}, "remove": {"1"}}, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if got, _ := l.a.srv.progress.TextBookmarks(ctx, l.ownerID, l.book.ID); len(got) != 2 {
		t.Errorf("a refused remove changed the owner's bookmarks: %d", len(got))
	}
	// The owner removes their own.
	rec = l.hx(t, http.MethodPost, post, url.Values{"edition_id": {l.ed.ID}, "sid": {sid}, "remove": {"1"}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner remove: %d", rec.Code)
	}
	if got, _ := l.a.srv.progress.TextBookmarks(ctx, l.ownerID, l.book.ID); len(got) != 1 {
		t.Errorf("owner remove left %d bookmarks, want 1", len(got))
	}
}
