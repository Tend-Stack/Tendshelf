package library_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var ahlSeq int

// ahlAdd stores a highlight; deleted marks it removed.
func ahlAdd(t *testing.T, r *library.Repo, user, book, quote, note, colour string, at int64, deleted bool) string {
	t.Helper()
	ahlSeq++
	id := fmt.Sprintf("hl_%03d", ahlSeq)
	var del any
	if deleted {
		del = at
	}
	if _, err := r.Store().Writer().Exec(`INSERT INTO highlights(id, user_id, book_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, note, colour, created_at, updated_at, deleted_at)
VALUES (?, ?, ?, 1, 'c0001s0001', 0, 'c0001s0001', 5, ?, ?, ?, ?, ?, ?)`, id, user, book, quote, note, colour, at, at, del); err != nil {
		t.Fatal(err)
	}
	return id
}

func ahlIDs(rows []library.HighlightRow) []string {
	var out []string
	for _, x := range rows {
		out = append(out, x.ID)
	}
	return out
}

func ahlOwnerQuery(q library.HighlightQuery) library.HighlightQuery {
	q.Read = share.Filter{SQL: "1=1"}
	return q
}

func TestAllHighlightsScopeDeletedAndOrder(t *testing.T) {
	r, _, _, f := listenSeed(t)
	a := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A"})
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "B"})
	h1 := ahlAdd(t, r, listenOwner, a.ID, "one", "", "yellow", 10, false)
	h2 := ahlAdd(t, r, listenOwner, b.ID, "two", "", "green", 20, false)
	ahlAdd(t, r, listenOwner, a.ID, "gone", "", "yellow", 30, true)
	ahlAdd(t, r, listenMember, a.ID, "theirs", "", "yellow", 40, false)

	rows, total, err := r.AllHighlights(ctx, allScope, listenOwner, ahlOwnerQuery(library.HighlightQuery{}))
	if err != nil || total != 2 || !reflect.DeepEqual(ahlIDs(rows), []string{h2, h1}) {
		t.Fatalf("rows %v total %d err %v", ahlIDs(rows), total, err)
	}
	if rows[0].BookTitle != "B" {
		t.Errorf("title %q", rows[0].BookTitle)
	}
	rows, _, _ = r.AllHighlights(ctx, allScope, listenOwner, ahlOwnerQuery(library.HighlightQuery{BookID: a.ID}))
	if !reflect.DeepEqual(ahlIDs(rows), []string{h1}) {
		t.Errorf("book filter: %v", ahlIDs(rows))
	}
	// No Read filter at all matches nothing.
	if rows, total, _ = r.AllHighlights(ctx, allScope, listenOwner, library.HighlightQuery{}); len(rows) != 0 || total != 0 {
		t.Errorf("zero Read listed %v", ahlIDs(rows))
	}
}

func TestAllHighlightsRevokedAndCanRead(t *testing.T) {
	r, st, _, f := listenSeed(t)
	open := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Open Book"})
	listen := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Listen Only Book"})
	lost := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lost Book"})
	for _, g := range []struct {
		id, book string
		read     int
	}{{"gr_o", open.ID, 1}, {"gr_l", listen.ID, 0}} {
		if _, err := st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, can_read, created_at, updated_at) VALUES (?, ?, 'book', ?, ?, 1, 1)`, g.id, listenMember, g.book, g.read); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []library.Book{open, listen, lost} {
		ahlAdd(t, r, listenMember, b.ID, "q "+b.Title, "", "blue", 5, false)
	}
	c := share.New(st)
	u := users.User{ID: listenMember, Role: users.RoleMember}
	sc := library.Scope{Books: c.BookFilter(u, "b"), Libraries: c.LibraryFilter(u, "l"), Shared: c.SharedBooks(u)}
	rows, total, err := r.AllHighlights(ctx, sc, listenMember, library.HighlightQuery{Read: c.BookFilterFor(u, "b", share.ReadText)})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].BookTitle != "Open Book" {
		t.Fatalf("a revoked book and a listen-only book must not show: %+v", rows)
	}
}

func TestAllHighlightsSearchColourAndPaging(t *testing.T) {
	r, _, _, f := listenSeed(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A"})
	pct := ahlAdd(t, r, listenOwner, b.ID, "100% sure", "", "yellow", 1, false)
	und := ahlAdd(t, r, listenOwner, b.ID, "a_b", "", "green", 2, false)
	ahlAdd(t, r, listenOwner, b.ID, "axb", "", "green", 3, false)
	note := ahlAdd(t, r, listenOwner, b.ID, "plain", "mind the gap", "pink", 4, false)
	q := func(q library.HighlightQuery) []string {
		rows, _, err := r.AllHighlights(ctx, allScope, listenOwner, ahlOwnerQuery(q))
		if err != nil {
			t.Fatal(err)
		}
		return ahlIDs(rows)
	}
	if got := q(library.HighlightQuery{Text: "%"}); !reflect.DeepEqual(got, []string{pct}) {
		t.Errorf("%% matched %v", got)
	}
	if got := q(library.HighlightQuery{Text: "a_b"}); !reflect.DeepEqual(got, []string{und}) {
		t.Errorf("_ matched %v", got)
	}
	if got := q(library.HighlightQuery{Text: "GAP"}); !reflect.DeepEqual(got, []string{note}) {
		t.Errorf("note search %v", got)
	}
	if got := q(library.HighlightQuery{Colour: "pink"}); !reflect.DeepEqual(got, []string{note}) {
		t.Errorf("colour %v", got)
	}
	if got := q(library.HighlightQuery{Colour: "purple"}); len(got) != 4 {
		t.Errorf("an unknown colour is ignored: %v", got)
	}
	if got := q(library.HighlightQuery{Limit: 2, Offset: 3}); len(got) != 1 {
		t.Errorf("paging: %v", got)
	}
	if _, total, _ := r.AllHighlights(ctx, allScope, listenOwner, ahlOwnerQuery(library.HighlightQuery{Limit: 1})); total != 4 {
		t.Errorf("total = %d", total)
	}
}
