package metadata_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

func TestSaveUserDetailsWritesChangedFieldsOnly(t *testing.T) {
	e, id := newLookupBook(t)
	form, err := e.svc.DetailsForm(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if form.Title != "Plain Title" || form.Authors != "Ann Author" {
		t.Fatalf("prefill: %+v", form)
	}
	// The untouched form writes nothing.
	changed, err := e.svc.SaveUserDetails(bg, id, form, "")
	if err != nil || len(changed) != 0 {
		t.Fatalf("unchanged form: %v %v", changed, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM metadata_values WHERE book_id = ? AND source = 'user'`, id); n != 0 {
		t.Errorf("user rows after an unchanged save = %d", n)
	}
	form.Title, form.Subtitle, form.Authors, form.Narrators = "My Title", "A Sub", "Ann Author, Bo Second", "Cy Voice"
	form.SeriesName, form.SeriesNumber, form.Year, form.Publisher = "The Saga", "2", "1999", "Home Press"
	form.Genres, form.ISBN, form.ASIN, form.Description, form.Language = "Fantasy; Drama\nFantasy", "978-1-234-56789-7", "b0testbook", "About it.", "English"
	changed, err = e.svc.SaveUserDetails(bg, id, form, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changed, []string{"title", "subtitle", "authors", "narrators", "series", "year", "publisher", "description", "language", "genres", "isbn", "asin"}) {
		t.Errorf("changed = %v", changed)
	}
	b, err := e.repo.Book(bg, id)
	if err != nil || b.Title != "My Title" || b.Subtitle != "A Sub" || b.PublishedYear != 1999 || b.Publisher != "Home Press" || b.Description != "About it." {
		t.Fatalf("book: %+v %v", b, err)
	}
	d, from := e.resolved(id)
	if !slices.Equal(d.Authors, []string{"Ann Author", "Bo Second"}) || !slices.Equal(d.Genres, []string{"Fantasy", "Drama"}) ||
		len(d.Series) != 1 || d.Series[0].Name != "The Saga" || d.Series[0].Sequence != "2" || d.ISBN != "9781234567897" || d.ASIN != "B0TESTBOOK" {
		t.Errorf("resolved: %+v", d)
	}
	if from["title"] != "user" || from["authors"] != "user" {
		t.Errorf("sources: %v", from)
	}
	// Saving the same form again writes nothing more.
	again, err := e.svc.DetailsForm(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := e.svc.SaveUserDetails(bg, id, again, ""); err != nil || len(changed) != 0 {
		t.Errorf("second save: %v %v", changed, err)
	}
}

func TestSaveUserDetailsClearingRestoresDerived(t *testing.T) {
	e, id := newLookupBook(t)
	form, _ := e.svc.DetailsForm(bg, id)
	form.Title, form.Publisher = "Mine", "Home Press"
	if _, err := e.svc.SaveUserDetails(bg, id, form, ""); err != nil {
		t.Fatal(err)
	}
	form.Title, form.Publisher = "", ""
	changed, err := e.svc.SaveUserDetails(bg, id, form, "")
	if err != nil || !slices.Equal(changed, []string{"title", "publisher"}) {
		t.Fatalf("clear: %v %v", changed, err)
	}
	b, _ := e.repo.Book(bg, id)
	if b.Title != "Plain Title" || b.Publisher != "" {
		t.Errorf("after clearing: %+v", b)
	}
	if n := e.count(`SELECT COUNT(*) FROM metadata_values WHERE book_id = ? AND source = 'user'`, id); n != 0 {
		t.Errorf("user rows = %d", n)
	}
}

func TestSaveUserDetailsRefusals(t *testing.T) {
	e, id := newLookupBook(t)
	form, _ := e.svc.DetailsForm(bg, id)
	form.Title, form.Year, form.ISBN, form.ASIN, form.SeriesNumber = "Changed", "99", "12", "x", "3"
	_, err := e.svc.SaveUserDetails(bg, id, form, "")
	var p metadata.Problems
	if !errors.As(err, &p) {
		t.Fatalf("err = %v", err)
	}
	var fields []string
	for _, f := range p {
		fields = append(fields, f.Field)
	}
	slices.Sort(fields)
	if !slices.Equal(fields, []string{"asin", "isbn", "series", "year"}) {
		t.Errorf("fields = %v", fields)
	}
	if b, _ := e.repo.Book(bg, id); b.Title != "Plain Title" {
		t.Error("a refused form saved something")
	}
}

// A book whose details a lookup already fetched keeps a hand edit through a rescan and a later lookup.
func TestHandEditSurvivesLookupAndRescan(t *testing.T) {
	e, id, c := newAppliedBook(t)
	if _, err := e.svc.ApplyOnline(bg, id, c, []string{"title", "published_year", "narrators"}, ""); err != nil {
		t.Fatal(err)
	}
	form, _ := e.svc.DetailsForm(bg, id)
	if form.Year != "2023" {
		t.Fatalf("the fetched year is not prefilled: %q", form.Year)
	}
	form.Year, form.Title = "1990", "By Hand"
	if _, err := e.svc.SaveUserDetails(bg, id, form, ""); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		b, err := e.repo.Book(bg, id)
		if err != nil || b.PublishedYear != 1990 || b.Title != "By Hand" {
			t.Errorf("%s: %+v %v", when, b, err)
		}
	}
	check("after saving")
	e.local(id) // a rescan
	check("after a rescan")
	// A new lookup, applying a different field, leaves the hand-edited ones alone.
	res := e.lookup(id, online.Query{Title: "The Test Match"})
	c2 := candidateTitled(t, res, "The Test Match")
	if _, err := e.svc.ApplyOnline(bg, id, c2, []string{"publisher"}, ""); err != nil {
		t.Fatal(err)
	}
	check("after another lookup")
	// Applying the year again is the person's choice and does replace it.
	if _, err := e.svc.ApplyOnline(bg, id, c2, []string{"published_year"}, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.repo.Book(bg, id); b.PublishedYear != 2023 {
		t.Errorf("an applied year: %d", b.PublishedYear)
	}
	// Reset clears every hand edit and every fetched value.
	if err := e.svc.ResetOnline(bg, id, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.repo.Book(bg, id); b.Title != "Plain Title" {
		t.Errorf("after a reset: %q", b.Title)
	}
}
