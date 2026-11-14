package upload

import (
	"testing"
)

// A text-only book placed by Add books (ledger ebook, no audio) is removable whole through RemoveWhole,
// and a changed file is kept with the book.
func TestRemoveWholeTextOnlyLedger(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxReceiving = 20 })
	sc := eadmIngest(t, e)
	v := e.send("x", book{"a.epub": eadmEPUB(t, "Placed Text", "Ann Author")})
	e.mustPlace(v.ID)
	eadmScanLast(t, e, sc)
	eadmWaitBooks(t, e, 1)
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PartsOf(bg, id, "")
	if err != nil || !p.Whole || p.HasAudio || len(p.Text) != 1 || p.Combined {
		t.Fatalf("parts = %+v, %v", p, err)
	}
	if _, ok := p.EditionRemovable(p.Text[0].ID); ok {
		t.Error("the only file of a text book is offered as an edition")
	}
	res, err := e.svc.RemoveWhole(bg, me, id, "", nil)
	if err != nil || len(res.Deleted) != 1 {
		t.Fatalf("remove: %+v %v", res, err)
	}
	if n := booksCount(t, e); n != 0 {
		t.Errorf("books = %d", n)
	}
	if n := countRows(t, e, "ebook_files"); n != 0 {
		t.Errorf("ebook_files = %d", n)
	}
}
