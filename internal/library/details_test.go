package library_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestMetadataValues(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b1")

	err := r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.PutMetadataValue(b.ID, library.MetadataValue{Field: "title", Source: "tags", Value: json.RawMessage(`"A"`), ObservedAt: 5}); err != nil {
			return err
		}
		if err := tx.PutMetadataValue(b.ID, library.MetadataValue{Field: "title", Source: "folder", Value: json.RawMessage(`"B"`), ObservedAt: 6}); err != nil {
			return err
		}
		// upsert on the primary key
		return tx.PutMetadataValue(b.ID, library.MetadataValue{Field: "title", Source: "tags", Value: json.RawMessage(`"C"`), ObservedAt: 7})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.MetadataValues(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.MetadataValue{
		{Field: "title", Source: "folder", Value: json.RawMessage(`"B"`), ObservedAt: 6},
		{Field: "title", Source: "tags", Value: json.RawMessage(`"C"`), ObservedAt: 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %+v, want %+v", got, want)
	}
	// the Tx read sees the same, and delete works (a missing row is fine)
	err = r.Update(ctx, func(tx *library.Tx) error {
		in, err := tx.MetadataValues(b.ID)
		if err != nil || len(in) != 2 {
			t.Fatalf("tx values = %v, %v", in, err)
		}
		if err := tx.DeleteMetadataValue(b.ID, "title", "folder"); err != nil {
			return err
		}
		return tx.DeleteMetadataValue(b.ID, "title", "folder")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.MetadataValues(ctx, b.ID); len(got) != 1 || got[0].Source != "tags" {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestMetadataValueRefusals(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b1")
	ok := json.RawMessage(`"x"`)
	cases := []struct {
		name string
		v    library.MetadataValue
		want error
	}{
		{"user source", library.MetadataValue{Field: "title", Source: "user", Value: ok}, library.ErrInvalid},
		{"no field", library.MetadataValue{Source: "tags", Value: ok}, library.ErrInvalid},
		{"no source", library.MetadataValue{Field: "title", Value: ok}, library.ErrInvalid},
		{"bad json", library.MetadataValue{Field: "title", Source: "tags", Value: json.RawMessage(`{nope`)}, library.ErrInvalid},
		{"empty json", library.MetadataValue{Field: "title", Source: "tags"}, library.ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.Update(ctx, func(tx *library.Tx) error { return tx.PutMetadataValue(b.ID, c.v) })
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	err := r.Update(ctx, func(tx *library.Tx) error { return tx.DeleteMetadataValue(b.ID, "title", "user") })
	if !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("delete user err = %v", err)
	}
	err = r.Update(ctx, func(tx *library.Tx) error {
		return tx.PutMetadataValue("bk_nope", library.MetadataValue{Field: "title", Source: "tags", Value: ok})
	})
	if !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("unknown book err = %v", err)
	}
}

func TestEnsurePersonAndSeries(t *testing.T) {
	r, _ := newRepo(t)
	var a, b, c, s1, s2 string
	err := r.Update(ctx, func(tx *library.Tx) error {
		var err error
		if a, err = tx.EnsurePerson("  Ann Lee ", "Lee, Ann"); err != nil {
			return err
		}
		if b, err = tx.EnsurePerson("ann LEE", "x"); err != nil {
			return err
		}
		if c, err = tx.EnsurePerson("Bob Ray", "Ray, Bob"); err != nil {
			return err
		}
		if s1, err = tx.EnsureSeries("Moon", "Moon"); err != nil {
			return err
		}
		s2, err = tx.EnsureSeries("moon", "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a == c || !strings.HasPrefix(a, "ps_") || s1 != s2 || !strings.HasPrefix(s1, "sr_") {
		t.Fatalf("ids = %s %s %s %s %s", a, b, c, s1, s2)
	}
	err = r.Update(ctx, func(tx *library.Tx) error {
		_, err := tx.EnsurePerson("   ", "")
		return err
	})
	if !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("blank person err = %v", err)
	}
	err = r.Update(ctx, func(tx *library.Tx) error {
		_, err := tx.EnsureSeries("", "")
		return err
	})
	if !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("blank series err = %v", err)
	}
}

func TestBookPeopleSeriesLabels(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)
	b := addBook(t, r, lib, f, "/audio/b1")
	var p1, p2, p3, s1, s2 string
	err := r.Update(ctx, func(tx *library.Tx) error {
		p1, _ = tx.EnsurePerson("Ann Lee", "Lee, Ann")
		p2, _ = tx.EnsurePerson("Bob Ray", "Ray, Bob")
		p3, _ = tx.EnsurePerson("Cy Poe", "Poe, Cy")
		s1, _ = tx.EnsureSeries("Moon", "Moon")
		s2, _ = tx.EnsureSeries("Sun", "Sun")
		if err := tx.SetBookPeople(b.ID, "author", []string{p2, p1, p2}); err != nil {
			return err
		}
		if err := tx.SetBookPeople(b.ID, "narrator", []string{p3}); err != nil {
			return err
		}
		if err := tx.SetBookSeries(b.ID, []library.BookSeriesLink{{SeriesID: s2, Sequence: "2"}, {SeriesID: s1, Sequence: "0.5"}, {SeriesID: s2, Sequence: "9"}}); err != nil {
			return err
		}
		return tx.SetBookLabels(b.ID, "genre", []string{"Drama", "drama", " ", "Fantasy"})
	})
	if err != nil {
		t.Fatal(err)
	}
	people, err := r.BookPeople(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantP := []library.BookPerson{{p2, "Bob Ray", "author", 0}, {p1, "Ann Lee", "author", 1}, {p3, "Cy Poe", "narrator", 0}}
	if !reflect.DeepEqual(people, wantP) {
		t.Fatalf("people = %+v, want %+v", people, wantP)
	}
	series, err := r.BookSeries(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantS := []library.BookSeriesLink{{s2, "Sun", "2", 0}, {s1, "Moon", "0.5", 1}}
	if !reflect.DeepEqual(series, wantS) {
		t.Fatalf("series = %+v, want %+v", series, wantS)
	}
	labels, err := r.BookLabels(ctx, b.ID)
	if err != nil || !reflect.DeepEqual(labels, map[string][]string{"genre": {"Drama", "Fantasy"}}) {
		t.Fatalf("labels = %v, %v", labels, err)
	}

	// replace one role, leave the other; Tx reads agree with Repo reads
	err = r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.SetBookPeople(b.ID, "author", []string{p1}); err != nil {
			return err
		}
		in, err := tx.BookPeople(b.ID)
		if err != nil || len(in) != 2 || in[0].PersonID != p1 || in[1].Role != "narrator" {
			t.Fatalf("tx people = %+v, %v", in, err)
		}
		if ls, err := tx.BookSeries(b.ID); err != nil || len(ls) != 2 {
			t.Fatalf("tx series = %+v, %v", ls, err)
		}
		if lb, err := tx.BookLabels(b.ID); err != nil || len(lb["genre"]) != 2 {
			t.Fatalf("tx labels = %+v, %v", lb, err)
		}
		return tx.SetBookLabels(b.ID, "genre", nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if labels, _ := r.BookLabels(ctx, b.ID); len(labels) != 0 {
		t.Fatalf("labels after clear = %v", labels)
	}

	// validation
	for _, fn := range []func(*library.Tx) error{
		func(tx *library.Tx) error { return tx.SetBookPeople(b.ID, "editor", nil) },
		func(tx *library.Tx) error { return tx.SetBookLabels(b.ID, "mood", nil) },
	} {
		if err := r.Update(ctx, fn); !errors.Is(err, library.ErrInvalid) {
			t.Fatalf("validation err = %v", err)
		}
	}
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.SetBookPeople(b.ID, "author", []string{"ps_nope"}) }); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("unknown person err = %v", err)
	}

	// cascades: deleting the book removes its links but keeps the people and series rows
	if _, err := st.Writer().ExecContext(ctx, `DELETE FROM books WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"book_people", "book_series", "book_labels", "metadata_values"} {
		var n int
		if err := st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows after book delete = %d, %v", table, n, err)
		}
	}
	var n int
	if err := st.Reader().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM people) + (SELECT count(*) FROM series)`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("people+series = %d, %v", n, err)
	}
}
