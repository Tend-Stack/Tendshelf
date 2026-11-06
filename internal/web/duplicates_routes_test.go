package web

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// dupPair makes (once per key) two audiobooks in the sweep library and remembers them in e.ebookPair as
// "<key>:keep" and "<key>:drop". A merge uses its pair up, so each row and role has its own.
func dupPair(e *sweepEnv, key string) (keep, drop string) {
	if k, ok := e.ebookPair[key+":keep"]; ok {
		return k, e.ebookPair[key+":drop"]
	}
	f := library.Folder{ID: e.folderID, LibraryID: e.libID, Path: e.folderDir}
	k := librarytest.AddBook(e.t, e.a.lib, f, librarytest.BookSpec{Title: "Dupword " + key + " one", DurationMs: sweepHourMs})
	d := librarytest.AddBook(e.t, e.a.lib, f, librarytest.BookSpec{Title: "Dupword " + key + " two", DurationMs: sweepHourMs})
	e.ebookPair[key+":keep"], e.ebookPair[key+":drop"] = k.ID, d.ID
	return k.ID, d.ID
}

// dupSweepRows are the route matrix rows of Duplicate books (S4): owner and admin manage libraries; a member
// and a guest get 404 on every one.
func dupSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	key := func(verb string, role int) string { return fmt.Sprintf("dup%s%d", verb, role) }
	pair := func(verb string) func(*sweepEnv, int) (string, string) {
		return func(e *sweepEnv, role int) (string, string) { return dupPair(e, key(verb, role)) }
	}
	merge, notSame, restore := pair("merge"), pair("notsame"), pair("restore")
	return []sweepRow{
		sweepGet("GET /admin/duplicates", func(*sweepEnv, int) string { return "/admin/duplicates" }, staff),
		sweepGet("GET /admin/duplicates/merge", func(e *sweepEnv, role int) string {
			k, d := pair("view")(e, role)
			return "/admin/duplicates/merge?keep=" + k + "&drop=" + d
		}, staff),
		sweepPost("POST /admin/duplicates/merge", func(*sweepEnv, int) string { return "/admin/duplicates/merge" },
			func(e *sweepEnv, role int) url.Values {
				k, d := merge(e, role)
				return url.Values{"keep": {k}, "drop": {d}}
			}, see, "/admin/duplicates?done=merged"),
		sweepPost("POST /admin/duplicates/not-same", func(*sweepEnv, int) string { return "/admin/duplicates/not-same" },
			func(e *sweepEnv, role int) url.Values {
				k, d := notSame(e, role)
				if k > d {
					k, d = d, k
				}
				return url.Values{"a": {k}, "b": {d}}
			}, see, "/admin/duplicates?done=notsame"),
		sweepGet("GET /admin/duplicates/merged", func(*sweepEnv, int) string { return "/admin/duplicates/merged" }, staff),
		sweepPost("POST /admin/duplicates/merged/restore", func(*sweepEnv, int) string { return "/admin/duplicates/merged/restore" },
			func(e *sweepEnv, role int) url.Values {
				// The folder has to be merged away before it can be shown again; do it once per role.
				k, d := restore(e, role)
				rootKey := key("restore", role) + ":root"
				if _, done := e.ebookPair[rootKey]; !done {
					b, err := e.a.lib.Book(context.Background(), d)
					if err != nil {
						e.t.Fatal(err)
					}
					if err := e.a.lib.MergeBooks(context.Background(), k, d, ""); err != nil {
						e.t.Fatal(err)
					}
					e.ebookPair[rootKey] = b.RootPath
				}
				return url.Values{"root_path": {e.ebookPair[rootKey]}}
			}, see, "/admin/duplicates/merged?done=restored"),
	}
}

func (e *syncEnv) dupExec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.a.st.Writer().Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *syncEnv) dupCount(q string, args ...any) (n int) {
	e.t.Helper()
	if err := e.a.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

// TestDupPagesAndRoles: staff see the list and the confirm page in plain words; a member and a guest get 404
// on every route and nothing changes.
func TestDupPagesAndRoles(t *testing.T) {
	e := syncSeed(t)
	keep, drop := e.extra[0], e.extra[1]
	e.dupExec(`UPDATE audio_files SET size = 100, quick_hash = ? WHERE book_id IN (?, ?)`, strings.Repeat("a", 64), keep, drop)
	e.dupExec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id) VALUES (?, ?, 5, 10, 5, 5, 'device-test-1')`, e.uid[sweepOwner], drop)

	body := e.compareGet(sweepOwner, "/admin/duplicates", nil).Body.String()
	for _, want := range []string{dupTitle, dupAudioHead, "Syncword 0", "Syncword 1", "Merge into", dupNotSame, dupHiddenLink, "1 person has a place in it"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if !strings.Contains(e.compareGet(sweepOwner, "/admin/libraries", nil).Body.String(), "/admin/duplicates") {
		t.Error("the Libraries page does not link Duplicate books")
	}
	confirm := "/admin/duplicates/merge?keep=" + keep + "&drop=" + drop
	body = e.compareGet(sweepAdmin, confirm, nil).Body.String()
	for _, want := range []string{dupConfirmTitle, dupFilesStay, dupOneWay, dupKeepHead, dupDropHead, "Places in the book: 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm page lacks %q", want)
		}
	}
	if strings.Contains(body, dupNotSameAudio) {
		t.Error("the confirm page warns about different files for identical ones")
	}
	e.dupExec(`UPDATE audio_files SET size = 101 WHERE book_id = ?`, drop)
	if !strings.Contains(e.compareGet(sweepOwner, confirm, nil).Body.String(), dupNotSameAudio) {
		t.Error("the confirm page does not warn when the files differ")
	}
	for _, role := range []int{sweepMember, sweepGuest} {
		for _, p := range []string{"/admin/duplicates", confirm, "/admin/duplicates/merged"} {
			if rec := e.compareGet(role, p, nil); rec.Code != http.StatusNotFound {
				t.Errorf("%s GET %s: %d", sweepRoleNames[role], p, rec.Code)
			}
		}
		for _, p := range []string{"/admin/duplicates/merge", "/admin/duplicates/not-same", "/admin/duplicates/merged/restore"} {
			form := url.Values{"keep": {keep}, "drop": {drop}, "a": {keep}, "b": {drop}, "root_path": {"/x"}}
			if rec := e.comparePost(t, role, p, form); rec.Code != http.StatusNotFound {
				t.Errorf("%s POST %s: %d", sweepRoleNames[role], p, rec.Code)
			}
		}
	}
	if rec := e.a.do(http.MethodPost, "/admin/duplicates/merge", url.Values{"keep": {keep}, "drop": {drop}}, nil, e.ck[sweepOwner]); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if n := e.dupCount(`SELECT count(*) FROM books WHERE id IN (?, ?)`, keep, drop); n != 2 {
		t.Errorf("a refused request changed the books: %d left", n)
	}
	// A pair, a book that is not there, and two libraries.
	for name, form := range map[string]url.Values{
		"same book":     {"keep": {keep}, "drop": {keep}},
		"unknown":       {"keep": {keep}, "drop": {"bk_nope"}},
		"no drop":       {"keep": {keep}},
		"other library": {"keep": {keep}, "drop": {e.hidden.ID}},
	} {
		rec := e.comparePost(t, sweepOwner, "/admin/duplicates/merge", form)
		want := http.StatusNotFound
		switch name {
		case "same book", "other library":
			want = http.StatusUnprocessableEntity
		}
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", name, rec.Code, want)
		}
	}
}

// TestDupMergeBusyBook: a book that is being written to is refused in plain words and nothing moves.
func TestDupMergeBusyBook(t *testing.T) {
	e := syncSeed(t)
	keep, drop := e.extra[0], e.extra[1]
	e.dupExec(`INSERT INTO file_writes(id, job_id, book_id, audio_file_id, state, original_path, backup_path, temp_path, before_sha256, audio_hash, created_by, created_at, updated_at)
VALUES ('fw_1', 'j', ?, 'af', 'copying', '/o', '/b', '/t', 'x', 'y', 'us', 1, 1)`, drop)
	rec := e.comparePost(t, sweepOwner, "/admin/duplicates/merge", url.Values{"keep": {keep}, "drop": {drop}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "This book is being written to. Try again when that has finished.") {
		t.Fatalf("busy merge = %d", rec.Code)
	}
	if n := e.dupCount(`SELECT count(*) FROM books WHERE id = ?`, drop); n != 1 {
		t.Error("a refused merge deleted the book")
	}
}

// TestDupMergeMovesDataAndOldIdsLand: after a merge the data is on the kept book, the old id redirects (page and
// API) for people who may open the kept book, an offline outbox on the old id lands on the kept book, and the
// folder can be shown again.
func TestDupMergeMovesDataAndOldIdsLand(t *testing.T) {
	e := syncSeed(t)
	keep, drop := e.extra[0], e.extra[1]
	ownerID, memberID := e.uid[sweepOwner], e.uid[sweepMember]
	dropBook, err := e.a.lib.Book(context.Background(), drop)
	if err != nil {
		t.Fatal(err)
	}
	e.dupExec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id) VALUES (?, ?, 5000, 3600000, 5, 5, 'device-test-1')`, memberID, drop)
	e.dupExec(`INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_dup', ?, ?, 9, 1, 1)`, memberID, drop)
	e.dupExec(`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES (?, ?, 9, 1)`, e.shelf[sweepOwner], drop)
	e.dupExec(`INSERT INTO share_links(id, token_hash, book_id, created_by, expires_at, created_at) VALUES ('sl_dup', randomblob(32), ?, ?, 1000, 1)`, drop, ownerID)

	rec := e.comparePost(t, sweepOwner, "/admin/duplicates/merge", url.Values{"keep": {keep}, "drop": {drop}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/duplicates?done=merged" {
		t.Fatalf("merge = %d -> %q", rec.Code, location(rec))
	}
	if !strings.Contains(e.compareGet(sweepOwner, "/admin/duplicates?done=merged", nil).Body.String(), dupMergedBanner) {
		t.Error("the list does not say the merge happened")
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM progress WHERE book_id = '` + keep + `' AND user_id = '` + memberID + `' AND position_ms = 5000`: 1,
		`SELECT count(*) FROM bookmarks WHERE book_id = '` + keep + `' AND id = 'bm_dup'`:                                      1,
		`SELECT count(*) FROM shelf_items WHERE book_id = '` + keep + `' AND shelf_id = '` + e.shelf[sweepOwner] + `'`:         1,
		`SELECT count(*) FROM share_links WHERE book_id = '` + keep + `' AND id = 'sl_dup'`:                                    1,
		`SELECT count(*) FROM books WHERE id = '` + drop + `'`:                                                                 0,
	} {
		if n := e.dupCount(q); n != want {
			t.Errorf("%s = %d, want %d", q, n, want)
		}
	}

	// The old page address and the old API address land on the kept book, for people who may open it.
	for _, role := range []int{sweepOwner, sweepMember} {
		r := e.compareGet(role, "/books/"+drop, nil)
		if r.Code != http.StatusPermanentRedirect || location(r) != "/books/"+keep {
			t.Errorf("%s page: %d -> %q", sweepRoleNames[role], r.Code, location(r))
		}
	}
	for _, tok := range []string{e.owner, e.member} {
		r := e.send(tok, http.MethodGet, "/api/v1/books/"+drop, "")
		if r.Code != http.StatusPermanentRedirect || location(r) != "/api/v1/books/"+keep {
			t.Errorf("api: %d -> %q", r.Code, location(r))
		}
	}
	if r := e.compareGet(sweepGuest, "/books/"+drop, nil); r.Code != http.StatusNotFound && r.Code != http.StatusPermanentRedirect {
		t.Errorf("guest: %d", r.Code)
	}

	// An outbox queued on the old id: a progress save, a batch item and a bookmark all land on the kept book.
	now := time.Now().UnixMilli()
	if r := e.send(e.member, http.MethodPut, "/api/v1/progress/"+drop, fmt.Sprintf(`{"positionMs":7000,"clientUpdatedAt":%d}`, now)); r.Code != http.StatusOK {
		t.Fatalf("put progress on the old id: %d %s", r.Code, r.Body)
	}
	if n := e.dupCount(`SELECT count(*) FROM progress WHERE user_id = ? AND book_id = ? AND position_ms = 7000`, memberID, keep); n != 1 {
		t.Error("the progress save did not land on the kept book")
	}
	batch := fmt.Sprintf(`{"updates":[{"bookId":%q,"positionMs":8000,"clientUpdatedAt":%d}]}`, drop, now+1000)
	if r := e.send(e.member, http.MethodPost, "/api/v1/progress/batch", batch); r.Code != http.StatusOK || strings.Contains(r.Body.String(), `"error":{`) {
		t.Fatalf("batch on the old id: %d %s", r.Code, r.Body)
	}
	if n := e.dupCount(`SELECT count(*) FROM progress WHERE user_id = ? AND book_id = ? AND position_ms = 8000`, memberID, keep); n != 1 {
		t.Error("the batch item did not land on the kept book")
	}
	if r := e.send(e.member, http.MethodPost, "/api/v1/books/"+drop+"/bookmarks", `{"positionMs":42}`); r.Code != http.StatusCreated && r.Code != http.StatusOK {
		t.Fatalf("bookmark on the old id: %d %s", r.Code, r.Body)
	}
	if n := e.dupCount(`SELECT count(*) FROM bookmarks WHERE user_id = ? AND book_id = ? AND position_ms = 42`, memberID, keep); n != 1 {
		t.Error("the bookmark did not land on the kept book")
	}
	if n := e.dupCount(`SELECT count(*) FROM progress WHERE book_id = ?`, drop); n != 0 {
		t.Errorf("progress rows on the dropped id: %d", n)
	}

	// The merged copy is listed and can be shown again; the scan that follows is queued.
	page := e.compareGet(sweepOwner, "/admin/duplicates/merged", nil).Body.String()
	if !strings.Contains(page, dropBook.RootPath) || !strings.Contains(page, dupShowAgain) {
		t.Error("the merged copies page lacks the folder or its button")
	}
	r := e.comparePost(t, sweepOwner, "/admin/duplicates/merged/restore", url.Values{"root_path": {dropBook.RootPath}})
	if r.Code != http.StatusSeeOther || location(r) != "/admin/duplicates/merged?done=restored" {
		t.Fatalf("restore = %d -> %q", r.Code, location(r))
	}
	if m, _ := e.a.lib.MergedFolders(context.Background(), dropBook.LibraryID); len(m) != 0 {
		t.Errorf("merged folders after restore: %v", m)
	}
	if r := e.comparePost(t, sweepOwner, "/admin/duplicates/merged/restore", url.Values{"root_path": {dropBook.RootPath}}); r.Code != http.StatusNotFound {
		t.Errorf("restoring twice: %d", r.Code)
	}
}

// TestDupOldIdNeedsTheRightToTheKeptBook: when the person may not open the kept book, the old id is the usual 404
// on the page, the API and every write, and nothing is stored.
func TestDupOldIdNeedsTheRightToTheKeptBook(t *testing.T) {
	e := syncSeed(t)
	folders, err := e.a.lib.Folders(context.Background(), e.hiddenLib)
	if err != nil || len(folders) == 0 {
		t.Fatalf("folders: %v %v", folders, err)
	}
	other := librarytest.AddBook(t, e.a.lib, folders[0], librarytest.BookSpec{Title: "Hiddenword Second", DurationMs: sweepHourMs})
	if err := e.a.lib.MergeBooks(context.Background(), e.hidden.ID, other.ID, ""); err != nil {
		t.Fatal(err)
	}
	r := e.send(e.owner, http.MethodGet, "/api/v1/books/"+other.ID, "")
	if r.Code != http.StatusPermanentRedirect {
		t.Errorf("owner: %d", r.Code)
	}
	if r := e.send(e.member, http.MethodGet, "/api/v1/books/"+other.ID, ""); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "Hiddenword") {
		t.Errorf("member api: %d %s", r.Code, r.Body)
	}
	if r := e.compareGet(sweepMember, "/books/"+other.ID, nil); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "Hiddenword") {
		t.Errorf("member page: %d", r.Code)
	}
	now := time.Now().UnixMilli()
	if r := e.send(e.member, http.MethodPut, "/api/v1/progress/"+other.ID, fmt.Sprintf(`{"positionMs":7,"clientUpdatedAt":%d}`, now)); r.Code != http.StatusNotFound {
		t.Errorf("member progress: %d", r.Code)
	}
	batch := fmt.Sprintf(`{"updates":[{"bookId":%q,"positionMs":8,"clientUpdatedAt":%d}]}`, other.ID, now)
	if r := e.send(e.member, http.MethodPost, "/api/v1/progress/batch", batch); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"code":"not_found"`) {
		t.Errorf("member batch: %d %s", r.Code, r.Body)
	}
	if r := e.send(e.member, http.MethodPost, "/api/v1/books/"+other.ID+"/bookmarks", `{"positionMs":3}`); r.Code != http.StatusNotFound {
		t.Errorf("member bookmark: %d", r.Code)
	}
	if n := e.dupCount(`SELECT count(*) FROM progress WHERE user_id = ? AND book_id IN (?, ?)`, e.uid[sweepMember], e.hidden.ID, other.ID); n != 0 {
		t.Errorf("progress stored for a book the person cannot open: %d", n)
	}
}

// TestDupNotSame: "Not the same" hides the pair from the list and the page says so.
func TestDupNotSame(t *testing.T) {
	e := syncSeed(t)
	keep, drop := e.extra[0], e.extra[1]
	e.dupExec(`UPDATE audio_files SET size = 100, quick_hash = ? WHERE book_id IN (?, ?)`, strings.Repeat("b", 64), keep, drop)
	if !strings.Contains(e.compareGet(sweepOwner, "/admin/duplicates", nil).Body.String(), dupAudioHead) {
		t.Fatal("the pair is not listed")
	}
	rec := e.comparePost(t, sweepOwner, "/admin/duplicates/not-same", url.Values{"a": {keep}, "b": {drop}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/duplicates?done=notsame" {
		t.Fatalf("not-same = %d -> %q", rec.Code, location(rec))
	}
	body := e.compareGet(sweepOwner, "/admin/duplicates?done=notsame", nil).Body.String()
	if strings.Contains(body, dupAudioHead) || !strings.Contains(body, html.EscapeString(dupNotSameBanner)) {
		t.Error("the pair is still listed or the banner is missing")
	}
	if rec := e.comparePost(t, sweepOwner, "/admin/duplicates/not-same", url.Values{"a": {keep}, "b": {keep}}); rec.Code != http.StatusNotFound {
		t.Errorf("a book against itself: %d", rec.Code)
	}
}
