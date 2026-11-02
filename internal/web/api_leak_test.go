package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// TestAPILeaks gives the member and the guest a grant on the sweep library only, then checks that
// the closed library's book and a refused book of the granted library appear in no JSON answer of
// the read API, and that the closed ids answer exactly as an unknown id does.
func TestAPILeaks(t *testing.T) {
	for _, role := range []int{sweepMember, sweepGuest} {
		t.Run(sweepRoleNames[role], func(t *testing.T) {
			e := grantsLeakSeed(t)
			ctx := context.Background()
			// A refused book in the granted library: listed nowhere, listened to by nobody.
			folder, err := e.a.lib.Folder(ctx, e.folderID)
			if err != nil {
				t.Fatal(err)
			}
			refused := librarytest.AddBook(t, e.a.lib, folder, librarytest.BookSpec{
				Title: "Refusedword Ember", Authors: []string{"Refusedauthor Vale"}, DurationMs: 2 * sweepHourMs, AddedAt: 7,
			})
			if _, err := e.a.st.Writer().Exec(`UPDATE books SET refused_reason = 'drm' WHERE id = ?`, refused.ID); err != nil {
				t.Fatal(err)
			}
			if err := e.a.lib.RebuildSearch(ctx); err != nil {
				t.Fatal(err)
			}
			words := append(e.hiddenWords(), "Refusedword", "Refusedauthor", refused.ID)
			scan := func(what, body string) {
				t.Helper()
				for _, w := range words {
					if strings.Contains(body, w) {
						t.Errorf("%s contains %q", what, w)
					}
				}
			}
			// The member's shelf holds both unseen books: a book that loses visibility must vanish
			// from the shelf and its count.
			if role == sweepMember {
				for _, id := range []string{e.hidden.ID, refused.ID} {
					if err := e.a.lib.AddToShelf(ctx, e.uid[role], e.shelf[role], id); err != nil {
						t.Fatal(err)
					}
				}
			}
			tok := apiToken(t, e.sweepEnv, role)

			// Lists, counts and search.
			rec := apiGet(e.sweepEnv, tok, "/api/v1/books", nil)
			var l apiTestList
			apiDecode(t, rec, &l)
			scan("/books", rec.Body.String())
			if rec.Code != 200 || len(l.Items) != 1 || *l.Total != 1 || l.Items[0].ID != e.book.ID {
				t.Errorf("/books: %d %s", rec.Code, rec.Body)
			}
			rec = apiGet(e.sweepEnv, tok, "/api/v1/libraries", nil)
			var libs struct{ Items []apiLibrary }
			apiDecode(t, rec, &libs)
			scan("/libraries", rec.Body.String())
			if len(libs.Items) != 1 || libs.Items[0].Books != 1 {
				t.Errorf("/libraries: %s", rec.Body)
			}
			for _, q := range []string{"Hiddenword", "Hiddenwrd", "Hiddenauthor", "Hiddenseries", "Refusedword", "Refusedauthor", "Quill", "Sweepword"} {
				rec := apiGet(e.sweepEnv, tok, "/api/v1/search?q="+q, nil)
				var sr apiSearchResult
				apiDecode(t, rec, &sr)
				scan("/search?q="+q, rec.Body.String())
				if rec.Code != 200 || (q != "Sweepword" && (sr.Total != 0 || len(sr.Books) != 0 || len(sr.People) != 0 || len(sr.Series) != 0)) {
					t.Errorf("/search?q=%s: %d %s", q, rec.Code, rec.Body)
				}
			}
			for _, p := range []string{"/api/v1/shared", "/api/v1/books?genre=x", "/api/v1/books?author=" + e.hiddenAuthor,
				"/api/v1/books?series=" + e.hiddenSeries, "/api/v1/books?title=Hidden", "/api/v1/books?show=shared", "/api/v1/books?sort=added"} {
				rec := apiGet(e.sweepEnv, tok, p, nil)
				if rec.Code != 200 {
					t.Errorf("GET %s: %d", p, rec.Code)
				}
				scan(p, rec.Body.String())
			}
			if role == sweepMember {
				rec := apiGet(e.sweepEnv, tok, "/api/v1/shelves", nil)
				var sh struct{ Own []apiShelf }
				apiDecode(t, rec, &sh)
				scan("/shelves", rec.Body.String())
				if len(sh.Own) != 1 || sh.Own[0].Books != 1 {
					t.Errorf("/shelves counts: %s", rec.Body)
				}
				rec = apiGet(e.sweepEnv, tok, "/api/v1/shelves/"+e.shelf[role], nil)
				var one struct {
					Shelf apiShelf
					Books apiTestList
				}
				apiDecode(t, rec, &one)
				scan("/shelves/{id}", rec.Body.String())
				if one.Shelf.Books != 1 || len(one.Books.Items) != 1 || *one.Books.Total != 1 {
					t.Errorf("/shelves/{id}: %s", rec.Body)
				}
			}

			// Closed ids answer exactly like unknown ones.
			pairs := [][2]string{
				{"/api/v1/books/" + e.hidden.ID, "/api/v1/books/bk_unknown"},
				{"/api/v1/covers/" + e.hidden.ID, "/api/v1/covers/bk_unknown"},
				{"/api/v1/stream/" + e.hiddenAudio, "/api/v1/stream/af_unknown"},
				{"/api/v1/peaks/" + e.hiddenAudio, "/api/v1/peaks/af_unknown"},
				{"/api/v1/libraries/" + e.hiddenLib + "/books", "/api/v1/libraries/lib_unknown/books"},
			}
			for _, p := range pairs {
				closed, unknown := apiGet(e.sweepEnv, tok, p[0], nil), apiGet(e.sweepEnv, tok, p[1], nil)
				if closed.Code != http.StatusNotFound || closed.Body.String() != unknown.Body.String() {
					t.Errorf("GET %s: %d %q, unknown id: %d %q", p[0], closed.Code, closed.Body, unknown.Code, unknown.Body)
				}
			}
			// The refused book's detail shows no files and no stream.
			rec = apiGet(e.sweepEnv, tok, "/api/v1/books/"+refused.ID, nil)
			var d apiBookDetail
			apiDecode(t, rec, &d)
			if rec.Code == 200 && (len(d.Files) != 0 || d.Progress != nil || !d.Refused) {
				t.Errorf("refused book detail: %s", rec.Body)
			}

			// Control: the owner sees the closed library's book, so the scans above could have failed.
			otok := apiToken(t, e.sweepEnv, sweepOwner)
			rec = apiGet(e.sweepEnv, otok, "/api/v1/search?q=Hiddenword", nil)
			if !strings.Contains(rec.Body.String(), e.hidden.ID) {
				t.Error("the owner's search does not show the closed library's book: the scans prove nothing")
			}
			rec = apiGet(e.sweepEnv, otok, "/api/v1/books", nil)
			if !strings.Contains(rec.Body.String(), e.hidden.ID) {
				t.Error("the owner's list does not show the closed library's book")
			}
		})
	}
}
