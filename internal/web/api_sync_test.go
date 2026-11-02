package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// P3-9b: the progress and bookmark sync endpoints of the JSON API.

type syncEnv struct {
	*grantsLeakEnv
	owner, member string // bearer tokens
	extra         []string
}

// syncSeed is the leak app plus two more books the owner and the member can both listen to.
func syncSeed(t *testing.T) *syncEnv {
	t.Helper()
	e := &syncEnv{grantsLeakEnv: grantsLeakSeed(t)}
	folder, err := e.a.lib.Folder(context.Background(), e.folderID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		b := librarytest.AddBook(t, e.a.lib, folder, librarytest.BookSpec{
			Title: fmt.Sprintf("Syncword %d", i), Authors: []string{"Syncauthor"}, DurationMs: sweepHourMs, AddedAt: int64(10 + i),
		})
		e.extra = append(e.extra, b.ID)
	}
	e.owner = apiToken(t, e.sweepEnv, sweepOwner)
	e.member = apiToken(t, e.sweepEnv, sweepMember)
	return e
}

// jsonReq serves a JSON request through the app. mod adjusts the request (credentials) first.
func jsonReq(a *testApp, method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	if mod != nil {
		mod(r)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, r)
	return rec
}

// send makes a JSON request with a bearer token.
func (e *syncEnv) send(tok, method, path, body string) *httptest.ResponseRecorder {
	return jsonReq(e.a, method, path, body, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
}

type syncProgress struct {
	BookID          string `json:"bookId"`
	PositionMs      int64  `json:"positionMs"`
	ClientUpdatedAt int64  `json:"clientUpdatedAt"`
	ServerUpdatedAt int64  `json:"serverUpdatedAt"`
	DeviceID        string `json:"deviceId"`
	DeviceLabel     string `json:"deviceLabel"`
	Finished        bool   `json:"finished"`
}

type syncPut struct {
	Applied   bool         `json:"applied"`
	Progress  syncProgress `json:"progress"`
	ServerNow int64        `json:"serverNow"`
}

func putBody(pos, client int64, extra string) string {
	return fmt.Sprintf(`{"positionMs":%d,"clientUpdatedAt":%d%s}`, pos, client, extra)
}

func TestAPIProgressPut(t *testing.T) {
	e := syncSeed(t)
	path := "/api/v1/progress/" + e.book.ID
	now := time.Now().UnixMilli()

	rec := e.send(e.owner, http.MethodPut, path, putBody(5000, now, ""))
	var got syncPut
	apiDecode(t, rec, &got)
	if rec.Code != 200 || !got.Applied || got.Progress.PositionMs != 5000 || got.Progress.BookID != e.book.ID {
		t.Fatalf("first save: %d %s", rec.Code, rec.Body)
	}
	if got.Progress.DeviceLabel != "API test" || got.Progress.DeviceID == "" {
		t.Errorf("bearer defaults: id %q label %q", got.Progress.DeviceID, got.Progress.DeviceLabel)
	}

	// Older client time loses; newer wins; force beats a newer stored value.
	rec = e.send(e.owner, http.MethodPut, path, putBody(9000, now-1000, ""))
	apiDecode(t, rec, &got)
	if got.Applied || got.Progress.PositionMs != 5000 {
		t.Errorf("older write applied: %s", rec.Body)
	}
	rec = e.send(e.owner, http.MethodPut, path, putBody(7000, now+10, ""))
	apiDecode(t, rec, &got)
	if !got.Applied || got.Progress.PositionMs != 7000 {
		t.Errorf("newer write: %s", rec.Body)
	}
	rec = e.send(e.owner, http.MethodPut, path, putBody(1000, now-5000, `,"force":true`))
	apiDecode(t, rec, &got)
	if !got.Applied || got.Progress.PositionMs != 1000 {
		t.Errorf("force: %s", rec.Body)
	}
	// A client clock far ahead is clamped to the server time plus a minute.
	rec = e.send(e.owner, http.MethodPut, path, putBody(2000, now+10*3600*1000, ""))
	apiDecode(t, rec, &got)
	if got.Progress.ClientUpdatedAt > got.ServerNow+60000 {
		t.Errorf("not clamped: client %d server %d", got.Progress.ClientUpdatedAt, got.ServerNow)
	}
	// The GET is the same route as the player's.
	rec = e.send(e.owner, http.MethodGet, path, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"positionMs":2000`) {
		t.Errorf("GET: %d %s", rec.Code, rec.Body)
	}

	bad := []struct{ name, body, field string }{
		{"no position", `{"clientUpdatedAt":5}`, "positionMs"},
		{"negative position", putBody(-1, 5, ""), "positionMs"},
		{"no time", `{"positionMs":1}`, "clientUpdatedAt"},
		{"zero time", putBody(1, 0, ""), "clientUpdatedAt"},
		{"negative listened", putBody(1, 5, `,"listenedMs":-3`), "listenedMs"},
	}
	for _, c := range bad {
		rec := e.send(e.owner, http.MethodPut, path, c.body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"field":"`+c.field+`"`) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	if rec := e.send(e.owner, http.MethodPut, path, `{nope`); rec.Code != 400 {
		t.Errorf("broken JSON: %d", rec.Code)
	}
	// 415 before the body is read, 413 past the limit; both in JSON.
	rec = deviceBearerReq(e.a, http.MethodPut, path, e.owner, func(r *http.Request) {
		r.Header.Set("Content-Type", "text/plain")
	})
	if rec.Code != 415 || deviceErrCode(t, rec) != "unsupported" {
		t.Errorf("content type: %d %s", rec.Code, rec.Body)
	}
	rec = e.send(e.owner, http.MethodPut, path, putBody(1, 5, `,"x":"`+strings.Repeat("a", progressBodyLimit)+`"`))
	if rec.Code != 413 || deviceErrCode(t, rec) != "too_large" {
		t.Errorf("too large: %d %s", rec.Code, rec.Body)
	}
}

func TestAPIProgressSession(t *testing.T) {
	e := syncSeed(t)
	path := "/api/v1/progress/" + e.book.ID
	ck := e.ck[sweepOwner]
	tok := e.a.tokenFor(t, ck)
	send := func(body string, csrf bool) *httptest.ResponseRecorder {
		return jsonReq(e.a, http.MethodPut, path, body, func(r *http.Request) {
			r.AddCookie(ck)
			if csrf {
				r.Header.Set("X-CSRF-Token", tok)
			}
		})
	}
	now := time.Now().UnixMilli()
	if rec := send(putBody(100, now, `,"deviceId":"webdevice1"`), false); rec.Code != 403 {
		t.Errorf("no CSRF: %d %s", rec.Code, rec.Body)
	}
	rec := send(putBody(100, now, `,"deviceId":"webdevice1"`), true)
	var got syncPut
	apiDecode(t, rec, &got)
	if rec.Code != 200 || !got.Applied || got.Progress.DeviceID != "webdevice1" {
		t.Errorf("session save: %d %s", rec.Code, rec.Body)
	}
	if rec := send(putBody(100, now, ""), true); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"deviceId"`) {
		t.Errorf("session without a device id: %d %s", rec.Code, rec.Body)
	}
	// Mixed credentials are refused.
	rec = e.a.do(http.MethodGet, path, nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+e.owner) }, ck)
	if rec.Code != 400 {
		t.Errorf("mixed: %d", rec.Code)
	}
}

type syncFeed[T any] struct {
	Items     []T     `json:"items"`
	Next      *string `json:"next"`
	ServerNow int64   `json:"serverNow"`
}

func TestAPIProgressFeed(t *testing.T) {
	e := syncSeed(t)
	now := time.Now().UnixMilli()
	books := append([]string{e.book.ID}, e.extra...)
	for i, id := range books {
		if rec := e.send(e.owner, http.MethodPut, "/api/v1/progress/"+id, putBody(int64(1000+i), now, "")); rec.Code != 200 {
			t.Fatalf("seed %s: %d %s", id, rec.Code, rec.Body)
		}
		time.Sleep(2 * time.Millisecond)
	}
	// Page through with limit 1: every book exactly once, then null.
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		path := "/api/v1/progress?since=0&limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec := e.send(e.owner, http.MethodGet, path, "")
		var f syncFeed[syncProgress]
		apiDecode(t, rec, &f)
		if rec.Code != 200 || len(f.Items) != 1 || f.ServerNow == 0 {
			t.Fatalf("page %d: %d %s", pages, rec.Code, rec.Body)
		}
		if seen[f.Items[0].BookID] {
			t.Fatalf("book %s twice", f.Items[0].BookID)
		}
		seen[f.Items[0].BookID] = true
		pages++
		if f.Next == nil {
			break
		}
		cursor = *f.Next
	}
	if pages != 3 || len(seen) != 3 {
		t.Errorf("pages %d, books %v", pages, seen)
	}
	// since: only what changed after it, nothing after the newest change.
	var all syncFeed[syncProgress]
	apiDecode(t, e.send(e.owner, http.MethodGet, "/api/v1/progress", ""), &all)
	newest := all.Items[len(all.Items)-1].ServerUpdatedAt
	var none syncFeed[syncProgress]
	rec := e.send(e.owner, http.MethodGet, fmt.Sprintf("/api/v1/progress?since=%d", newest), "")
	apiDecode(t, rec, &none)
	if rec.Code != 200 || len(none.Items) != 0 || none.Next != nil || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("since newest: %s", rec.Body)
	}
	// Bad input.
	for _, q := range []string{"since=-1", "since=abc", "limit=0", "limit=501", "cursor=!!", "cursor=" + base64.RawURLEncoding.EncodeToString([]byte(`{"u":1,"i":""}`)),
		"cursor=" + base64.RawURLEncoding.EncodeToString([]byte(`[]`))} {
		if rec := e.send(e.owner, http.MethodGet, "/api/v1/progress?"+q, ""); rec.Code != 400 {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	// A person only gets their own rows, and none on a book they cannot see.
	if _, err := e.a.srv.progress.Save(context.Background(), progress.Update{UserID: e.uid[sweepMember], BookID: e.hidden.ID,
		PositionMs: 5, DurationMs: 10, ClientUpdatedAt: now, DeviceID: "hiddendevice01"}); err != nil {
		t.Fatal(err)
	}
	rec = e.send(e.member, http.MethodGet, "/api/v1/progress", "")
	var mine syncFeed[syncProgress]
	apiDecode(t, rec, &mine)
	for _, it := range mine.Items {
		if it.BookID == e.hidden.ID || it.BookID == books[1] {
			t.Errorf("member sees %v", it)
		}
	}
	if strings.Contains(rec.Body.String(), e.hidden.ID) {
		t.Errorf("hidden book in the member's feed: %s", rec.Body)
	}
}

func TestAPIProgressBatch(t *testing.T) {
	e := syncSeed(t)
	now := time.Now().UnixMilli()
	item := func(id string, pos, at int64) string {
		return fmt.Sprintf(`{"bookId":%q,"positionMs":%d,"clientUpdatedAt":%d}`, id, pos, at)
	}
	body := `{"updates":[` + strings.Join([]string{
		item(e.book.ID, 100, now),
		item(e.hidden.ID, 100, now),
		item("bk_unknown", 100, now),
		item(e.extra[0], -5, now),
		item(e.book.ID, 50, now-1000), // duplicate, older: processed in order, not applied
		item(e.extra[1], 300, now),
	}, ",") + `]}`
	member := e.send(e.member, http.MethodPost, "/api/v1/progress/batch", body)
	owner := e.send(e.owner, http.MethodPost, "/api/v1/progress/batch", body)
	type result struct {
		BookID   string        `json:"bookId"`
		Applied  bool          `json:"applied"`
		Progress *syncProgress `json:"progress"`
		Error    *struct {
			Code, Message, Field string
		} `json:"error"`
	}
	var got struct {
		Results []result `json:"results"`
	}
	apiDecode(t, member, &got)
	r := got.Results
	if member.Code != 200 || len(r) != 6 {
		t.Fatalf("member batch: %d %s", member.Code, member.Body)
	}
	// The hidden book and the unknown one answer with the same error, byte for byte.
	if r[1].Error == nil || r[2].Error == nil || *r[1].Error != *r[2].Error || r[1].Error.Code != "not_found" || r[1].Progress != nil {
		t.Errorf("hidden vs unknown: %+v %+v", r[1], r[2])
	}
	if r[0].Error != nil || !r[0].Applied || r[3].Error == nil || r[3].Error.Field != "positionMs" || r[4].Applied || r[4].Progress == nil || r[5].Error != nil {
		t.Errorf("results: %s", member.Body)
	}
	apiDecode(t, owner, &got)
	if got.Results[1].Error != nil {
		t.Errorf("owner can see the hidden book: %s", owner.Body)
	}
	// Size limits.
	if rec := e.send(e.owner, http.MethodPost, "/api/v1/progress/batch", `{"updates":[]}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"updates"`) {
		t.Errorf("empty: %d %s", rec.Code, rec.Body)
	}
	many := strings.TrimSuffix(strings.Repeat(item(e.book.ID, 1, now)+",", 101), ",")
	if rec := e.send(e.owner, http.MethodPost, "/api/v1/progress/batch", `{"updates":[`+many+`]}`); rec.Code != 400 {
		t.Errorf("101 updates: %d", rec.Code)
	}
	big := `{"updates":[` + item(e.book.ID, 1, now) + `],"pad":"` + strings.Repeat("a", progressBatchLimit) + `"}`
	if rec := e.send(e.owner, http.MethodPost, "/api/v1/progress/batch", big); rec.Code != 413 {
		t.Errorf("too large: %d", rec.Code)
	}
}

type syncBookmark struct {
	ID         string `json:"id"`
	ClientID   string `json:"clientId"`
	BookID     string `json:"bookId"`
	PositionMs int64  `json:"positionMs"`
	Note       string `json:"note"`
	UpdatedAt  int64  `json:"updatedAt"`
	DeletedAt  *int64 `json:"deletedAt"`
}

func TestAPIBookmarks(t *testing.T) {
	e := syncSeed(t)
	path := "/api/v1/books/" + e.book.ID + "/bookmarks"

	// Create, then a retry with the same client id returns the same bookmark and saves nothing new.
	rec := e.send(e.owner, http.MethodPost, path, `{"positionMs":4000,"note":"first","clientId":"01HX-client_1"}`)
	var bm syncBookmark
	apiDecode(t, rec, &bm)
	if rec.Code != 201 || bm.PositionMs != 4000 || bm.Note != "first" || bm.ClientID != "01HX-client_1" || bm.DeletedAt != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var again syncBookmark
	rec = e.send(e.owner, http.MethodPost, path, `{"positionMs":4000,"note":"first","clientId":"01HX-client_1"}`)
	apiDecode(t, rec, &again)
	if rec.Code != 200 || again.ID != bm.ID {
		t.Errorf("retry: %d %s", rec.Code, rec.Body)
	}
	var list syncFeed[syncBookmark]
	apiDecode(t, e.send(e.owner, http.MethodGet, path, ""), &list)
	n := 0
	for _, b := range list.Items {
		if b.ClientID == "01HX-client_1" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("retry made %d copies", n)
	}
	// The id on another book, or by another person, is a conflict.
	for name, c := range map[string]struct{ tok, path string }{
		"another book":    {e.owner, "/api/v1/books/" + e.extra[0] + "/bookmarks"},
		"another person":  {e.member, path},
		"person and book": {e.member, "/api/v1/books/" + e.extra[0] + "/bookmarks"},
	} {
		rec := e.send(c.tok, http.MethodPost, c.path, `{"positionMs":1,"clientId":"01HX-client_1"}`)
		if rec.Code != 409 || deviceErrCode(t, rec) != "conflict" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// Without an id, two posts are two bookmarks. A position past the end is clamped.
	var a, b syncBookmark
	apiDecode(t, e.send(e.owner, http.MethodPost, path, `{"positionMs":99999999999}`), &a)
	apiDecode(t, e.send(e.owner, http.MethodPost, path, `{"positionMs":99999999999}`), &b)
	if a.ID == b.ID || a.PositionMs != e.book.DurationMs {
		t.Errorf("no id: %+v %+v duration %d", a, b, e.book.DurationMs)
	}
	for _, body := range []string{`{}`, `{"positionMs":-1}`, `{"positionMs":1,"clientId":"bad id!"}`, `{"positionMs":1,"note":"` + strings.Repeat("x", 2001) + `"}`} {
		if rec := e.send(e.owner, http.MethodPost, path, body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}

	// Edit and delete; the deleted one stays in the changes as a tombstone.
	since := b.UpdatedAt // after both creations; the edit and the delete below are the only changes
	time.Sleep(3 * time.Millisecond)
	var edited syncBookmark
	rec = e.send(e.owner, http.MethodPatch, "/api/v1/bookmarks/"+bm.ID, `{"note":"second"}`)
	apiDecode(t, rec, &edited)
	if rec.Code != 200 || edited.Note != "second" {
		t.Errorf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/bookmarks/"+bm.ID, `{}`); rec.Code != 400 {
		t.Errorf("patch without note: %d", rec.Code)
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/bookmarks/"+bm.ID, `{"note":"`+strings.Repeat("x", apiBookmarkBody)+`"}`); rec.Code != 413 {
		t.Errorf("patch too large: %d", rec.Code)
	}
	if rec := e.send(e.member, http.MethodDelete, "/api/v1/bookmarks/"+bm.ID, ""); rec.Code != 404 {
		t.Errorf("someone else's delete: %d", rec.Code)
	}
	time.Sleep(3 * time.Millisecond)
	if rec := e.send(e.owner, http.MethodDelete, "/api/v1/bookmarks/"+a.ID, ""); rec.Code != 204 || rec.Body.Len() != 0 {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(e.owner, http.MethodDelete, "/api/v1/bookmarks/"+a.ID, ""); rec.Code != 404 {
		t.Errorf("delete twice: %d", rec.Code)
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/bookmarks/"+a.ID, `{"note":"x"}`); rec.Code != 404 {
		t.Errorf("patch a deleted one: %d", rec.Code)
	}
	// Plain list hides tombstones; the changes show them.
	apiDecode(t, e.send(e.owner, http.MethodGet, path, ""), &list)
	for _, x := range list.Items {
		if x.ID == a.ID {
			t.Errorf("deleted bookmark in the plain list")
		}
	}
	var ch syncFeed[syncBookmark]
	apiDecode(t, e.send(e.owner, http.MethodGet, fmt.Sprintf("%s?since=%d", path, since), ""), &ch)
	tomb := false
	for _, x := range ch.Items {
		if x.ID == a.ID && x.DeletedAt != nil {
			tomb = true
		}
		if x.ID == b.ID {
			t.Errorf("unchanged bookmark in the changes")
		}
	}
	if !tomb || len(ch.Items) != 2 {
		t.Errorf("changes: %+v", ch.Items)
	}
	// Per-book paging and a bad cursor.
	apiDecode(t, e.send(e.owner, http.MethodGet, path+"?since=0", ""), &ch)
	if len(ch.Items) != 4 || ch.Next != nil { // the app's seeded bookmark, bm, a (deleted) and b
		t.Errorf("all changes: %+v", ch)
	}
	if rec := e.send(e.owner, http.MethodGet, path+"?since=0&cursor=zzz", ""); rec.Code != 400 {
		t.Errorf("cursor: %d", rec.Code)
	}
}

func TestAPIBookmarkFeed(t *testing.T) {
	e := syncSeed(t)
	ctx := context.Background()
	mk := func(tok, book string, pos int, note string) syncBookmark {
		var bm syncBookmark
		rec := e.send(tok, http.MethodPost, "/api/v1/books/"+book+"/bookmarks", fmt.Sprintf(`{"positionMs":%d,"note":%q}`, pos, note))
		apiDecode(t, rec, &bm)
		if rec.Code != 201 {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		time.Sleep(2 * time.Millisecond)
		return bm
	}
	b1 := mk(e.owner, e.book.ID, 1, "")
	b2 := mk(e.owner, e.extra[0], 2, "")
	b3 := mk(e.owner, e.extra[1], 3, "")
	mine := mk(e.member, e.book.ID, 9, "Membermark")
	if rec := e.send(e.owner, http.MethodDelete, "/api/v1/bookmarks/"+b2.ID, ""); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	// A bookmark on a book the member cannot see (stored by the service, as after a grant was withdrawn).
	hid, err := e.a.srv.progress.AddBookmark(ctx, e.uid[sweepMember], e.hidden.ID, 5, "Hiddennote")
	if err != nil {
		t.Fatal(err)
	}
	ownerHid, err := e.a.srv.progress.AddBookmark(ctx, e.uid[sweepOwner], e.hidden.ID, 5, "")
	if err != nil {
		t.Fatal(err)
	}

	page := func(tok, q string) (ids []string, tombs int) {
		cursor := ""
		for i := 0; i < 20; i++ {
			path := "/api/v1/bookmarks?" + q
			if cursor != "" {
				path += "&cursor=" + cursor
			}
			rec := e.send(tok, http.MethodGet, path, "")
			var f syncFeed[syncBookmark]
			apiDecode(t, rec, &f)
			if rec.Code != 200 {
				t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
			}
			for _, x := range f.Items {
				ids = append(ids, x.ID)
				if x.DeletedAt != nil {
					tombs++
				}
			}
			if f.Next == nil {
				return ids, tombs
			}
			cursor = *f.Next
		}
		t.Fatal("feed did not end")
		return
	}
	ids, tombs := page(e.owner, "since=0&limit=1")
	want := []string{b1.ID, b2.ID, b3.ID, ownerHid.ID}
	if len(ids) != 5 || tombs != 1 { // the four, and the owner's bookmark the app was seeded with
		t.Fatalf("owner feed %v tombs %d, want %v", ids, tombs, want)
	}
	for _, w := range want {
		found := false
		for _, id := range ids {
			found = found || id == w
		}
		if !found {
			t.Errorf("owner feed lacks %s", w)
		}
	}
	// The member sees only their own, and nothing on the book they cannot see.
	ids, _ = page(e.member, "since=0")
	if len(ids) != 2 { // their own and the one the app was seeded with
		t.Errorf("member feed %v, want %s and the seeded one, not %s", ids, mine.ID, hid.ID)
	}
	for _, id := range ids {
		if id == hid.ID {
			t.Errorf("hidden bookmark %s in the feed", id)
		}
	}
	rec := e.send(e.member, http.MethodGet, "/api/v1/bookmarks?since=0", "")
	if strings.Contains(rec.Body.String(), "Hiddennote") || strings.Contains(rec.Body.String(), e.hidden.ID) {
		t.Errorf("hidden book leaks: %s", rec.Body)
	}
	// since is exclusive; bad cursors are 400.
	var f syncFeed[syncBookmark]
	apiDecode(t, e.send(e.owner, http.MethodGet, "/api/v1/bookmarks?since=0", ""), &f)
	last := f.Items[len(f.Items)-1].UpdatedAt
	if ids, _ := page(e.owner, fmt.Sprintf("since=%d", last)); len(ids) != 0 {
		t.Errorf("after the newest change: %v", ids)
	}
	for _, q := range []string{"since=x", "limit=0", "cursor=!!", "cursor=" + base64.RawURLEncoding.EncodeToString([]byte(`{"u":-1,"i":"a"}`))} {
		if rec := e.send(e.owner, http.MethodGet, "/api/v1/bookmarks?"+q, ""); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

// TestAPISyncHiddenBooks: a book the person cannot listen to answers the same 404 as an unknown id.
func TestAPISyncHiddenBooks(t *testing.T) {
	e := syncSeed(t)
	now := time.Now().UnixMilli()
	for _, c := range []struct{ method, tail, body string }{
		{http.MethodGet, "/api/v1/progress/%s", ""},
		{http.MethodPut, "/api/v1/progress/%s", putBody(1, now, "")},
		{http.MethodGet, "/api/v1/books/%s/bookmarks", ""},
		{http.MethodPost, "/api/v1/books/%s/bookmarks", `{"positionMs":1,"clientId":"hid1"}`},
	} {
		hidden := e.send(e.member, c.method, fmt.Sprintf(c.tail, e.hidden.ID), c.body)
		unknown := e.send(e.member, c.method, fmt.Sprintf(c.tail, "bk_unknown"), c.body)
		if hidden.Code != 404 || hidden.Body.String() != unknown.Body.String() {
			t.Errorf("%s %s: %d %q vs %q", c.method, c.tail, hidden.Code, hidden.Body, unknown.Body)
		}
	}
	// Someone else's bookmark id on a visible book is as unknown as a made-up one.
	var bm syncBookmark
	apiDecode(t, e.send(e.owner, http.MethodPost, "/api/v1/books/"+e.book.ID+"/bookmarks", `{"positionMs":1}`), &bm)
	a := e.send(e.member, http.MethodPatch, "/api/v1/bookmarks/"+bm.ID, `{"note":"x"}`)
	b := e.send(e.member, http.MethodPatch, "/api/v1/bookmarks/bm_unknown", `{"note":"x"}`)
	if a.Code != 404 || a.Body.String() != b.Body.String() {
		t.Errorf("foreign bookmark: %d %q vs %q", a.Code, a.Body, b.Body)
	}
	// A refused-for-listening book (DRM) is the same 404 for everyone, the owner included.
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET refused_reason = 'drm' WHERE id = ?`, e.extra[0]); err != nil {
		t.Fatal(err)
	}
	rec := e.send(e.owner, http.MethodPut, "/api/v1/progress/"+e.extra[0], putBody(1, now, ""))
	if rec.Code != 404 {
		t.Errorf("refused book: %d", rec.Code)
	}
}

func TestAPISyncAnonymousAndErrors(t *testing.T) {
	e := syncSeed(t)
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/v1/progress"}, {"PUT", "/api/v1/progress/x"}, {"POST", "/api/v1/progress/batch"},
		{"GET", "/api/v1/bookmarks"}, {"POST", "/api/v1/books/x/bookmarks"}, {"PATCH", "/api/v1/bookmarks/x"}, {"DELETE", "/api/v1/bookmarks/x"},
	} {
		rec := e.a.do(p.method, p.path, nil, nil)
		// An unsafe request without the CSRF token is refused first (403), still in JSON.
		wantCode := 401
		if p.method != "GET" {
			wantCode = 403
		}
		if rec.Code != wantCode || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s %s: %d %s", p.method, p.path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// Bearer is refused outside /api/v1/, so the old form route stays session-only.
	rec := deviceBearerReq(e.a, http.MethodGet, "/progress/"+e.book.ID, e.owner, nil)
	if rec.Code != 401 {
		t.Errorf("bearer on /progress: %d", rec.Code)
	}
}
