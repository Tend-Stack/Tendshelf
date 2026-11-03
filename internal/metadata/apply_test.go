package metadata_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// newAppliedBook is a book with a metadata.json year of 2001 and no narrator, plus the "The Test
// Match" candidate (year 2023, two narrators) from a lookup.
func newAppliedBook(t *testing.T) (*lookupEnv, string, metadata.Candidate) {
	e, id := newLookupBook(t)
	e.writeSidecar(lookupBookDir+"/metadata.json", `{"publishedYear":"2001"}`)
	e.local(id)
	if d, from := e.resolved(id); d.Year != 2001 || from["published_year"] != "json" {
		t.Fatalf("setup: %+v %v", d, from)
	}
	res := e.lookup(id, online.Query{Title: "The Test Match"})
	return e, id, candidateTitled(t, res, "The Test Match")
}

func (e *lookupEnv) rowCount(bookID, field, source string) int {
	return e.count(`SELECT COUNT(*) FROM metadata_values WHERE book_id = ? AND field = ? AND source = ?`, bookID, field, source)
}

func TestLookupApplyRules(t *testing.T) {
	e, id, c := newAppliedBook(t)
	if _, err := e.svc.ApplyOnline(bg, id, c, []string{"title", "no_such_field"}, ""); !errors.Is(err, metadata.ErrBadField) {
		t.Fatalf("unknown field: %v", err)
	}
	if e.rowCount(id, "title", "online") != 0 {
		t.Error("a bad field list writes nothing")
	}

	changed, err := e.svc.ApplyOnline(bg, id, c, []string{"narrators", "published_year", "authors"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changed, []string{"narrators", "published_year"}) {
		t.Errorf("changed: %v", changed)
	}
	// An empty local narrator and an online value: an online row only.
	if e.rowCount(id, "narrators", "online") != 1 || e.rowCount(id, "narrators", "user") != 0 {
		t.Error("narrators rows")
	}
	// A local year and a different ticked online year: both rows, and the online year wins.
	if e.rowCount(id, "published_year", "online") != 1 || e.rowCount(id, "published_year", "user") != 1 {
		t.Error("year rows")
	}
	// Same author: an online row, no user row.
	if e.rowCount(id, "authors", "user") != 0 {
		t.Error("an equal value needs no user row")
	}
	b, err := e.repo.Book(bg, id)
	if err != nil || b.PublishedYear != 2023 || b.Title != "Plain Title" {
		t.Errorf("book: %+v %v", b, err)
	}
	people, _ := e.repo.BookPeople(bg, id)
	if got := names(people, "narrator"); !slices.Equal(got, []string{"Ben Narrator", "Cara Reader"}) {
		t.Errorf("narrators: %v", got)
	}
	// An unticked field is unchanged.
	if e.rowCount(id, "title", "online") != 0 || e.rowCount(id, "publisher", "online") != 0 {
		t.Error("unticked fields were written")
	}
	// The author's ASIN is set from the match.
	if e.count(`SELECT COUNT(*) FROM people WHERE name = 'Ann Author' AND asin = 'B0AUTHOR01'`) != 1 {
		t.Error("author asin")
	}
	if e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'book.details.apply' AND target = ?`, "book:"+id) != 1 {
		t.Error("audit row")
	}
	// Applying again changes nothing.
	again, err := e.svc.ApplyOnline(bg, id, c, []string{"narrators", "published_year"}, "")
	if err != nil || len(again) != 0 {
		t.Errorf("second apply: %v %v", again, err)
	}
	src, err := e.svc.Sources(bg, id)
	if err != nil || src["published_year"].Source != "user" || src["narrators"].Source != "online" || src["title"].Source != "tags" {
		t.Errorf("sources: %+v %v", src, err)
	}
	if _, err := e.svc.ApplyOnline(bg, "bk_missing", c, []string{"title"}, ""); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown book: %v", err)
	}
}

func TestLookupSidecarBeatsOnlineFill(t *testing.T) {
	e, id, c := newAppliedBook(t)
	if _, err := e.svc.ApplyOnline(bg, id, c, []string{"narrators"}, ""); err != nil {
		t.Fatal(err)
	}
	e.writeSidecar(lookupBookDir+"/metadata.json", `{"publishedYear":"2001","narrators":["Side Narr"]}`)
	e.local(id)
	people, _ := e.repo.BookPeople(bg, id)
	if got := names(people, "narrator"); !slices.Equal(got, []string{"Side Narr"}) {
		t.Errorf("narrators after the sidecar: %v", got)
	}
	if _, from := e.resolved(id); from["narrators"] != "json" {
		t.Errorf("source: %v", from)
	}
	if e.rowCount(id, "narrators", "online") != 1 {
		t.Error("the online row stays for later")
	}
}

func TestLookupReset(t *testing.T) {
	e, id, c := newAppliedBook(t)
	if _, err := e.svc.ApplyOnline(bg, id, c, []string{"narrators", "published_year", "title"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ResetOnline(bg, id, ""); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT COUNT(*) FROM metadata_values WHERE book_id = ? AND source IN ('user','online')`, id) != 0 {
		t.Error("user and online rows remain")
	}
	b, _ := e.repo.Book(bg, id)
	if b.PublishedYear != 2001 || b.Title != "Plain Title" {
		t.Errorf("book after reset: %+v", b)
	}
	people, _ := e.repo.BookPeople(bg, id)
	if got := names(people, "narrator"); len(got) != 0 {
		t.Errorf("narrators after reset: %v", got)
	}
	if e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'book.details.reset'`) != 1 {
		t.Error("audit row")
	}
}
