package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// ctShelfRows counts the "To read" shelves and their rows for the book, for the person.
func ctShelfRows(t *testing.T, l *rdrLab, userID, bookID string) (shelves, items int) {
	t.Helper()
	row := l.a.st.Reader().QueryRow(`SELECT count(DISTINCT s.id), count(si.book_id) FROM shelves s
		LEFT JOIN shelf_items si ON si.shelf_id = s.id AND si.book_id = ? WHERE s.owner_id = ? AND s.name = 'To read' COLLATE NOCASE`, bookID, userID)
	if err := row.Scan(&shelves, &items); err != nil {
		t.Fatal(err)
	}
	return
}

func TestToReadShelf(t *testing.T) {
	l := rdrSeed(t)
	path := "/books/" + l.book.ID + "/to-read"
	if strings.Count(l.get(l.owner, "/books/"+l.book.ID+"?sheet=shelves", false).Body.String(), "To read") != 1 {
		t.Error("the sheet does not offer To read once, before the shelf exists")
	}
	if n, _ := ctShelfRows(t, l, l.ownerID, l.book.ID); n != 0 {
		t.Fatal("opening the sheet made the shelf")
	}
	if c := ctPost(t, l, l.owner, path, true); c != http.StatusSeeOther {
		t.Fatalf("to-read: %d", c)
	}
	if s, i := ctShelfRows(t, l, l.ownerID, l.book.ID); s != 1 || i != 1 {
		t.Fatalf("after one tap: %d shelves, %d rows", s, i)
	}
	if c := ctPost(t, l, l.owner, path, true); c != http.StatusSeeOther {
		t.Fatalf("to-read twice: %d", c)
	}
	if s, i := ctShelfRows(t, l, l.ownerID, l.book.ID); s != 1 || i != 1 {
		t.Errorf("after two taps: %d shelves, %d rows", s, i)
	}
	sheet := l.get(l.owner, "/books/"+l.book.ID+"?sheet=shelves", false).Body.String()
	sheet = sheet[strings.Index(sheet, "ts-shelf-sheet__list"):]
	if strings.Count(sheet, "To read") != 1 || !strings.Contains(sheet, `aria-pressed="true"`) {
		t.Errorf("the sheet does not show To read ticked, once: %.900s", sheet)
	}
	// Another person's shelf is untouched, and they get their own.
	if s, _ := ctShelfRows(t, l, sweepUserID(t, l.a, l.admin), l.book.ID); s != 0 {
		t.Error("the admin has a To read shelf nobody made")
	}
	if c := ctPost(t, l, l.admin, path, true); c != http.StatusSeeOther {
		t.Fatalf("admin to-read: %d", c)
	}
	if s, i := ctShelfRows(t, l, l.ownerID, l.book.ID); s != 1 || i != 1 {
		t.Errorf("the owner's shelf changed: %d, %d", s, i)
	}
}

func TestToReadRights(t *testing.T) {
	l := rdrSeed(t)
	path := "/books/" + l.book.ID + "/to-read"
	if c := ctPost(t, l, l.owner, path, false); c != http.StatusForbidden {
		t.Errorf("no token: %d", c)
	}
	if s, _ := ctShelfRows(t, l, l.ownerID, l.book.ID); s != 0 {
		t.Error("a refused post made the shelf")
	}
	if c := ctPost(t, l, l.guest, path, true); c != http.StatusNotFound {
		t.Errorf("guest: %d", c)
	}
	if c := ctPost(t, l, l.owner, "/books/bk_unknown/to-read", true); c != http.StatusNotFound {
		t.Errorf("unknown book: %d", c)
	}
	// Someone who cannot see the book gets 404 and no shelf.
	stranger := l.a.as(t, users.RoleMember)
	if c := ctPost(t, l, stranger, path, true); c != http.StatusNotFound {
		t.Errorf("a person without the book: %d", c)
	}
	if s, _ := ctShelfRows(t, l, sweepUserID(t, l.a, stranger), l.book.ID); s != 0 {
		t.Error("a refused post made a shelf")
	}
	if c := ctPost(t, l, l.reader, path, true); c != http.StatusSeeOther {
		t.Errorf("member with the book: %d", c)
	}
}

func TestToReadNamed(t *testing.T) {
	l := rdrSeed(t)
	// A shelf the person already has under the name (any case) is reused, never doubled.
	if _, err := l.a.lib.CreateShelf(t.Context(), l.ownerID, "TO READ"); err != nil {
		t.Fatal(err)
	}
	if c := ctPost(t, l, l.owner, "/books/"+l.book.ID+"/to-read", true); c != http.StatusSeeOther {
		t.Fatalf("to-read: %d", c)
	}
	if s, i := ctShelfRows(t, l, l.ownerID, l.book.ID); s != 1 || i != 1 {
		t.Errorf("%d shelves, %d rows", s, i)
	}
}
