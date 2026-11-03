package library_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

func queueTitles(t *testing.T, r *library.Repo, sc library.Scope, user string) string {
	t.Helper()
	list, err := r.Queue(ctx, sc, user)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range list {
		out = append(out, e.Book.Title)
	}
	return strings.Join(out, ",")
}

func queueAdd(t *testing.T, r *library.Repo, user string, b library.Book, front bool) bool {
	t.Helper()
	added, err := r.QueueAdd(ctx, user, b.ID, front)
	if err != nil {
		t.Fatalf("QueueAdd(%s, front=%v) = %v", b.Title, front, err)
	}
	return added
}

func TestQueueAddOrder(t *testing.T) {
	r, _, b, _ := shelfSeed(t, 5)
	steps := []struct {
		book      int
		front     bool
		wantAdded bool
		want      string
	}{
		{0, false, true, "B0"},
		{1, false, true, "B0,B1"},
		{2, true, true, "B2,B0,B1"},
		{3, false, true, "B2,B0,B1,B3"},
		{4, true, true, "B4,B2,B0,B1,B3"},
		{1, false, false, "B4,B2,B0,B1,B3"}, // re-add at the end keeps its place
		{1, true, false, "B1,B4,B2,B0,B3"},  // Play next on a queued book moves it first
		{2, true, false, "B2,B1,B4,B0,B3"},
	}
	for i, s := range steps {
		if got := queueAdd(t, r, "us_a", b[s.book], s.front); got != s.wantAdded {
			t.Errorf("step %d: added = %v", i, got)
		}
		if got := queueTitles(t, r, allScope, "us_a"); got != s.want {
			t.Errorf("step %d: queue = %s, want %s", i, got, s.want)
		}
	}
	if n, err := r.QueueCount(ctx, allScope, "us_a"); err != nil || n != 5 {
		t.Errorf("count = %d, %v", n, err)
	}
	if got := queueTitles(t, r, allScope, "us_b"); got != "" {
		t.Errorf("another person's queue = %q", got)
	}
	list, _ := r.Queue(ctx, allScope, "us_a")
	if list[0].AddedAt <= 0 {
		t.Errorf("addedAt = %d", list[0].AddedAt)
	}
	if _, err := r.QueueAdd(ctx, "us_a", "bk_nope", false); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown book = %v", err)
	}
}

func TestQueueCap(t *testing.T) {
	r, _, _, f := shelfSeed(t, 0)
	var books []library.Book
	for i := 0; i < library.QueueMax+1; i++ {
		books = append(books, librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("C%03d", i)}))
	}
	for i := 0; i < library.QueueMax; i++ {
		queueAdd(t, r, "us_a", books[i], i%2 == 0)
	}
	for _, front := range []bool{false, true} {
		if _, err := r.QueueAdd(ctx, "us_a", books[library.QueueMax].ID, front); !errors.Is(err, library.ErrQueueFull) {
			t.Fatalf("101st (front=%v) = %v", front, err)
		}
	}
	// A queued book can still be re-added or moved to the front at the cap.
	if added := queueAdd(t, r, "us_a", books[1], false); added {
		t.Error("re-adding reported added")
	}
	queueAdd(t, r, "us_a", books[1], true)
	if first, _ := r.Queue(ctx, allScope, "us_a"); first[0].Book.ID != books[1].ID {
		t.Errorf("first = %s", first[0].Book.Title)
	}
	// The cap counts rows the person cannot see too, and is per person.
	queueAdd(t, r, "us_b", books[library.QueueMax], false)
	if _, err := r.QueueAdd(ctx, "us_a", books[library.QueueMax].ID, false); !errors.Is(err, library.ErrQueueFull) {
		t.Errorf("full with hidden rows = %v", err)
	}
}

func TestQueueMoveAndRemove(t *testing.T) {
	r, _, b, f := shelfSeed(t, 4)
	hidden := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Hidden"})
	for _, x := range []library.Book{b[0], hidden, b[1], b[2], b[3]} {
		queueAdd(t, r, "us_a", x, false)
	}
	sc := library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{hidden.ID}}, Libraries: share.Filter{SQL: "1=1"}}
	if got := queueTitles(t, r, sc, "us_a"); got != "B0,B1,B2,B3" {
		t.Fatalf("scoped = %s", got)
	}
	move := func(bk library.Book, d int) error { return r.QueueMove(ctx, sc, "us_a", bk.ID, d) }
	// B1 moving up steps over the hidden book and swaps with B0.
	if err := move(b[1], -1); err != nil {
		t.Fatal(err)
	}
	if got := queueTitles(t, r, sc, "us_a"); got != "B1,B0,B2,B3" {
		t.Errorf("after up = %s", got)
	}
	if got := queueTitles(t, r, allScope, "us_a"); got != "B1,Hidden,B0,B2,B3" && got != "B1,B0,Hidden,B2,B3" {
		// The swap exchanges positions with the visible neighbour; the hidden row keeps its own.
		t.Errorf("all = %s", got)
	}
	if err := move(b[2], 1); err != nil {
		t.Fatal(err)
	}
	if got := queueTitles(t, r, sc, "us_a"); got != "B1,B0,B3,B2" {
		t.Errorf("after down = %s", got)
	}
	for _, c := range []struct {
		bk library.Book
		d  int
	}{{b[1], -1}, {b[2], 1}} {
		if err := move(c.bk, c.d); err != nil {
			t.Errorf("end move = %v", err)
		}
	}
	if got := queueTitles(t, r, sc, "us_a"); got != "B1,B0,B3,B2" {
		t.Errorf("end moves changed the order: %s", got)
	}
	if err := move(b[0], 2); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("delta 2 = %v", err)
	}
	if err := move(hidden, 1); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("hidden = %v", err)
	}
	if err := r.QueueMove(ctx, sc, "us_b", b[0].ID, 1); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("another person = %v", err)
	}
	if err := r.QueueRemove(ctx, "us_b", b[0].ID); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("remove as another person = %v", err)
	}
	if err := r.QueueRemove(ctx, "us_a", b[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueRemove(ctx, "us_a", b[0].ID); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("remove missing = %v", err)
	}
	if got := queueTitles(t, r, sc, "us_a"); got != "B1,B3,B2" {
		t.Errorf("after remove = %s", got)
	}
}

func TestQueuePop(t *testing.T) {
	r, _, b, f := shelfSeed(t, 4)
	hidden := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Hidden"})
	refused := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Refused", Refused: library.RefusedDRM})
	missing := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", MissingSince: 5})
	for _, x := range []library.Book{hidden, refused, missing, b[0], b[1], b[2]} {
		queueAdd(t, r, "us_a", x, false)
	}
	sc := library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{hidden.ID}}, Libraries: share.Filter{SQL: "1=1"}}
	if n, _ := r.QueueCount(ctx, sc, "us_a"); n != 3 {
		t.Errorf("count = %d, want 3 (hidden, refused and missing do not count)", n)
	}
	// Ended deletes that row wherever it is, then the first playable is popped.
	id, err := r.QueuePop(ctx, sc, "us_a", b[2].ID)
	if err != nil || id != b[0].ID {
		t.Fatalf("pop = %q, %v", id, err)
	}
	if got := queueTitles(t, r, sc, "us_a"); got != "B1" {
		t.Errorf("queue = %s", got)
	}
	// Skipped rows were kept: with wide access the hidden one is still there.
	if n, _ := r.QueueCount(ctx, allScope, "us_a"); n != 2 {
		t.Errorf("all-scope count = %d, want 2 (hidden and B1; refused and missing are never listed)", n)
	}
	if id, err = r.QueuePop(ctx, sc, "us_a", ""); err != nil || id != b[1].ID {
		t.Fatalf("pop 2 = %q, %v", id, err)
	}
	if _, err = r.QueuePop(ctx, sc, "us_a", ""); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("empty pop = %v", err)
	}
	// With full access the earlier hidden row plays now.
	if id, err = r.QueuePop(ctx, allScope, "us_a", ""); err != nil || id != hidden.ID {
		t.Fatalf("pop after access returns = %q, %v", id, err)
	}
	// Ending a book on an empty queue still reports nothing to play.
	if _, err = r.QueuePop(ctx, allScope, "us_a", b[3].ID); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("pop with ended on empty = %v", err)
	}
}

func TestQueueScopedByGrant(t *testing.T) {
	r, _, b, _ := shelfSeed(t, 2)
	queueAdd(t, r, "us_a", b[0], false)
	queueAdd(t, r, "us_a", b[1], false)
	if got := queueTitles(t, r, allScope, "us_a"); got != "B0,B1" {
		t.Fatalf("owner = %s", got)
	}
	if n, err := r.QueueCount(ctx, allScope, "us_a"); err != nil || n != 2 {
		t.Errorf("owner count = %d, %v", n, err)
	}
	if got := queueTitles(t, r, noneScope, "us_a"); got != "" {
		t.Errorf("member without a grant sees %q", got)
	}
	if n, err := r.QueueCount(ctx, noneScope, "us_a"); err != nil || n != 0 {
		t.Errorf("member count = %d, %v (must not leak)", n, err)
	}
	if _, err := r.QueuePop(ctx, noneScope, "us_a", ""); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("pop without access = %v", err)
	}
	if n, _ := r.QueueCount(ctx, allScope, "us_a"); n != 2 {
		t.Errorf("rows were dropped: %d", n)
	}
}
