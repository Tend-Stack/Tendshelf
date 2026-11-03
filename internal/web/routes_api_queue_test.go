package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// Up next (Q1): the queue endpoints of the JSON API.

type queueList struct {
	Items []struct {
		Book    struct{ ID, Title string } `json:"book"`
		AddedAt int64                      `json:"addedAt"`
	} `json:"items"`
	Next  *string `json:"next"`
	Total int     `json:"total"`
}

func (q queueList) titles() string {
	var out []string
	for _, it := range q.Items {
		out = append(out, it.Book.Title)
	}
	return strings.Join(out, ",")
}

func (e *syncEnv) queue(t *testing.T, tok string) queueList {
	t.Helper()
	var q queueList
	apiDecode(t, e.send(tok, http.MethodGet, "/api/v1/queue", ""), &q)
	return q
}

func queueBody(id, where string) string {
	if where == "" {
		return fmt.Sprintf(`{"bookId":%q}`, id)
	}
	return fmt.Sprintf(`{"bookId":%q,"where":%q}`, id, where)
}

func TestAPIQueueAddMoveRemove(t *testing.T) {
	e := syncSeed(t)
	book := func(id string) string {
		b, err := e.a.lib.Book(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return b.Title
	}
	if q := e.queue(t, e.owner); len(q.Items) != 0 || q.Total != 0 || q.Next != nil {
		t.Fatalf("empty queue = %+v", q)
	}
	for _, c := range []struct{ id, where, want string }{
		{e.extra[0], "", book(e.extra[0])},
		{e.extra[1], "end", book(e.extra[0]) + "," + book(e.extra[1])},
		{e.book.ID, "next", book(e.book.ID) + "," + book(e.extra[0]) + "," + book(e.extra[1])},
		{e.extra[1], "next", book(e.extra[1]) + "," + book(e.book.ID) + "," + book(e.extra[0])},
	} {
		rec := e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(c.id, c.where))
		var q queueList
		apiDecode(t, rec, &q)
		if rec.Code != 200 || q.titles() != c.want || q.Total != len(q.Items) || q.Items[0].AddedAt <= 0 {
			t.Fatalf("add %s %q: %d %s (want %s)", c.id, c.where, rec.Code, rec.Body, c.want)
		}
	}
	// Move: the first book down, then the last up; ends are no-ops.
	move := func(id, dir string) queueList {
		rec := e.send(e.owner, http.MethodPost, "/api/v1/queue/"+id+"/move", fmt.Sprintf(`{"dir":%q}`, dir))
		var q queueList
		apiDecode(t, rec, &q)
		if rec.Code != 200 {
			t.Fatalf("move %s %s: %d %s", id, dir, rec.Code, rec.Body)
		}
		return q
	}
	order := func(ids ...string) string {
		var out []string
		for _, id := range ids {
			out = append(out, book(id))
		}
		return strings.Join(out, ",")
	}
	if got := move(e.extra[1], "down").titles(); got != order(e.book.ID, e.extra[1], e.extra[0]) {
		t.Errorf("down = %s", got)
	}
	if got := move(e.extra[0], "up").titles(); got != order(e.book.ID, e.extra[0], e.extra[1]) {
		t.Errorf("up = %s", got)
	}
	if got := move(e.book.ID, "up").titles(); got != order(e.book.ID, e.extra[0], e.extra[1]) {
		t.Errorf("up at the top = %s", got)
	}
	// Remove.
	if rec := e.send(e.owner, http.MethodDelete, "/api/v1/queue/"+e.extra[0], ""); rec.Code != 204 || rec.Body.Len() != 0 {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.send(e.owner, http.MethodDelete, "/api/v1/queue/"+e.extra[0], ""); rec.Code != 404 {
		t.Errorf("delete again: %d", rec.Code)
	}
	if got := e.queue(t, e.owner).titles(); got != order(e.book.ID, e.extra[1]) {
		t.Errorf("after delete = %s", got)
	}
	// It is the owner's own list: the member's stays empty.
	if q := e.queue(t, e.member); len(q.Items) != 0 {
		t.Errorf("member sees %+v", q)
	}
}

func TestAPIQueueErrors(t *testing.T) {
	e := syncSeed(t)
	post := func(tok, body string) *httptest.ResponseRecorder {
		return e.send(tok, http.MethodPost, "/api/v1/queue", body)
	}
	for _, c := range []struct {
		name, body string
		code       int
		field      string
	}{
		{"missing bookId", `{}`, 400, "bookId"},
		{"bad where", queueBody(e.extra[0], "later"), 400, "where"},
		{"not JSON", `{`, 400, ""},
		{"unknown book", queueBody("bk_unknown", ""), 404, ""},
		{"hidden book", queueBody(e.hidden.ID, ""), 404, ""},
	} {
		rec := post(e.member, c.body)
		if rec.Code != c.code || (c.field != "" && !strings.Contains(rec.Body.String(), `"`+c.field+`"`)) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}
	// A hidden book and an unknown one are the same answer.
	a, b := post(e.member, queueBody(e.hidden.ID, "")), post(e.member, queueBody("bk_unknown", ""))
	if a.Body.String() != b.Body.String() {
		t.Errorf("hidden %q vs unknown %q", a.Body, b.Body)
	}
	// 415 for a form, 413 for a big body.
	rec := e.a.do(http.MethodPost, "/api/v1/queue", map[string][]string{"bookId": {e.extra[0]}},
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+e.owner) })
	if rec.Code != 415 {
		t.Errorf("form body: %d", rec.Code)
	}
	if rec := post(e.owner, `{"bookId":"`+strings.Repeat("x", 9<<10)+`"}`); rec.Code != 413 {
		t.Errorf("big body: %d", rec.Code)
	}
	// move and delete: bad dir 400, not queued 404, hidden 404.
	if rec := e.send(e.owner, http.MethodPost, "/api/v1/queue/"+e.extra[0]+"/move", `{"dir":"left"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"dir"`) {
		t.Errorf("bad dir: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{e.extra[0], "bk_unknown"} {
		if rec := e.send(e.owner, http.MethodPost, "/api/v1/queue/"+id+"/move", `{"dir":"up"}`); rec.Code != 404 {
			t.Errorf("move %s not queued: %d", id, rec.Code)
		}
	}
	if rec := e.send(e.member, http.MethodDelete, "/api/v1/queue/"+e.hidden.ID, ""); rec.Code != 404 {
		t.Errorf("delete hidden: %d", rec.Code)
	}
	// Refused and missing books conflict.
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET refused_reason = 'drm' WHERE id = ?`, e.extra[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET missing_since = 5 WHERE id = ?`, e.extra[1]); err != nil {
		t.Fatal(err)
	}
	for _, id := range e.extra {
		rec := post(e.owner, queueBody(id, ""))
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), queueErrUnplayable) || !strings.Contains(rec.Body.String(), `"bookId"`) {
			t.Errorf("unplayable %s: %d %s", id, rec.Code, rec.Body)
		}
	}
	// A book with no audio file conflicts too.
	if _, err := e.a.st.Writer().Exec(`DELETE FROM audio_files WHERE book_id = ?`, e.book.ID); err != nil {
		t.Fatal(err)
	}
	if rec := post(e.owner, queueBody(e.book.ID, "")); rec.Code != 409 {
		t.Errorf("no file: %d %s", rec.Code, rec.Body)
	}
}

func TestAPIQueueCap(t *testing.T) {
	e := syncSeed(t)
	folder, err := e.a.lib.Folder(context.Background(), e.folderID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < library.QueueMax+1; i++ {
		ids = append(ids, librarytest.AddBook(t, e.a.lib, folder, librarytest.BookSpec{Title: fmt.Sprintf("Cap %03d", i)}).ID)
	}
	for _, id := range ids[:library.QueueMax] {
		if rec := e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(id, "")); rec.Code != 200 {
			t.Fatalf("add %s: %d %s", id, rec.Code, rec.Body)
		}
	}
	rec := e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(ids[library.QueueMax], "next"))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), queueErrFull) || !strings.Contains(rec.Body.String(), `"bookId"`) {
		t.Fatalf("101st: %d %s", rec.Code, rec.Body)
	}
	// A queued book can still be moved to the front at the cap.
	var q queueList
	apiDecode(t, e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(ids[5], "next")), &q)
	if q.Total != library.QueueMax || q.Items[0].Book.ID != ids[5] {
		t.Errorf("re-add at the cap: total %d first %s", q.Total, q.Items[0].Book.ID)
	}
}

func TestAPIQueueNext(t *testing.T) {
	e := syncSeed(t)
	next := func(tok, body string) *httptest.ResponseRecorder {
		return e.send(tok, http.MethodPost, "/api/v1/queue/next", body)
	}
	// Empty queue: 204 with or without a body.
	if rec := next(e.owner, ""); rec.Code != 204 {
		t.Errorf("empty, no body: %d %s", rec.Code, rec.Body)
	}
	if rec := next(e.owner, `{"ended":"bk_gone"}`); rec.Code != 204 {
		t.Errorf("empty, ended: %d %s", rec.Code, rec.Body)
	}
	if rec := next(e.owner, `{"ended":"`+strings.Repeat("x", 65)+`"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"ended"`) {
		t.Errorf("long ended: %d %s", rec.Code, rec.Body)
	}
	for _, id := range e.extra {
		e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(id, ""))
	}
	// The member holds a row for a book they cannot open (access lost later): it is skipped, kept.
	if _, err := e.a.st.Writer().Exec(`INSERT INTO queue_items(user_id, book_id, position, added_at) VALUES (?, ?, -5, 1)`,
		e.uid[sweepMember], e.hidden.ID); err != nil {
		t.Fatal(err)
	}
	e.send(e.member, http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], ""))
	if q := e.queue(t, e.member); q.Total != 1 {
		t.Fatalf("member's list shows the hidden row: %+v", q)
	}
	var got struct {
		Book struct{ ID string } `json:"book"`
	}
	rec := next(e.member, `{"ended":"`+e.extra[1]+`"}`)
	apiDecode(t, rec, &got)
	if rec.Code != 200 || got.Book.ID != e.extra[0] {
		t.Fatalf("member next: %d %s", rec.Code, rec.Body)
	}
	if rec := next(e.member, ""); rec.Code != 204 {
		t.Errorf("member again: %d", rec.Code)
	}
	var n int
	if err := e.a.st.Reader().QueryRow(`SELECT count(*) FROM queue_items WHERE user_id = ?`, e.uid[sweepMember]).Scan(&n); err != nil || n != 1 {
		t.Errorf("hidden row kept: %d, %v", n, err)
	}
	// The owner: ended drops that row wherever it is, then the first plays and leaves the list.
	rec = next(e.owner, `{"ended":"`+e.extra[1]+`"}`)
	apiDecode(t, rec, &got)
	if rec.Code != 200 || got.Book.ID != e.extra[0] {
		t.Fatalf("owner next: %d %s", rec.Code, rec.Body)
	}
	if q := e.queue(t, e.owner); q.Total != 0 {
		t.Errorf("owner list = %+v", q)
	}
}

func TestAPIQueueSessionAndAnonymous(t *testing.T) {
	e := syncSeed(t)
	ck := e.ck[sweepOwner]
	tok := e.a.tokenFor(t, ck)
	send := func(method, path, body string, csrf bool) *httptest.ResponseRecorder {
		return jsonReq(e.a, method, path, body, func(r *http.Request) {
			r.AddCookie(ck)
			if csrf {
				r.Header.Set("X-CSRF-Token", tok)
			}
		})
	}
	if rec := send(http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], ""), false); rec.Code != 403 {
		t.Errorf("no CSRF: %d %s", rec.Code, rec.Body)
	}
	if rec := send(http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], ""), true); rec.Code != 200 {
		t.Errorf("session add: %d %s", rec.Code, rec.Body)
	}
	if rec := send(http.MethodGet, "/api/v1/queue", "", false); rec.Code != 200 || !strings.Contains(rec.Body.String(), e.extra[0]) {
		t.Errorf("session list: %d %s", rec.Code, rec.Body)
	}
	if rec := send(http.MethodPost, "/api/v1/queue/next", "", false); rec.Code != 403 {
		t.Errorf("next without CSRF: %d", rec.Code)
	}
	if rec := send(http.MethodDelete, "/api/v1/queue/"+e.extra[0], "", true); rec.Code != 204 {
		t.Errorf("session delete: %d", rec.Code)
	}
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/v1/queue"}, {"POST", "/api/v1/queue"}, {"POST", "/api/v1/queue/next"},
		{"POST", "/api/v1/queue/x/move"}, {"DELETE", "/api/v1/queue/x"},
	} {
		rec := e.a.do(p.method, p.path, nil, nil)
		want := 401
		if p.method != "GET" {
			want = 403
		}
		if rec.Code != want || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("anonymous %s %s: %d", p.method, p.path, rec.Code)
		}
	}
}
