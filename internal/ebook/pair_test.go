package ebook

import (
	"reflect"
	"testing"
)

var pairTBooks = []BookKey{
	{ID: "b1", Title: "Dune", Authors: []string{"Frank Herbert"}, ISBN: "080442957X"},
	{ID: "b2", Title: "Dune Messiah", Authors: []string{"Frank Herbert"}},
	{ID: "b3", Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"}, ASIN: "B007978NPG"},
	{ID: "b4", Title: "The Hobbit", Authors: []string{"Someone Else"}},
	{ID: "b5", Title: "Emma", Authors: []string{"Jane Austen"}},
	{ID: "b6", Title: "Emma", Authors: []string{"Jane Austen"}},
	{ID: "b7", Title: "Same ISBN A", ISBN: "0306406152"},
	{ID: "b8", Title: "Same ISBN B", ISBN: "978-0-306-40615-7"},
}

func TestPairMatch(t *testing.T) {
	for _, c := range []struct {
		name    string
		m       PairMeta
		notSame map[string]bool
		want    []Candidate // first entries, in order
	}{
		{"isbn beats title elsewhere", PairMeta{Title: "The Hobbit", Authors: []string{"Tolkien, J. R. R."}, ISBN: "0306406152"},
			nil, []Candidate{{"b7", ReasonISBN, 1}, {"b8", ReasonISBN, 1}}},
		{"asin", PairMeta{Title: "zzz", ASIN: " b007978npg "}, nil, []Candidate{{"b3", ReasonASIN, 1}}},
		{"title and author, one book", PairMeta{Title: "Dune: A Novel", Authors: []string{"Herbert, Frank"}}, nil,
			[]Candidate{{"b1", ReasonTitle, 0.9}}},
		{"title equal author differs", PairMeta{Title: "Emma", Authors: []string{"Somebody Other"}}, nil,
			[]Candidate{{"b5", ReasonSimilar, 0.6}, {"b6", ReasonSimilar, 0.6}}},
		{"two titles, same author", PairMeta{Title: "Emma", Authors: []string{"Jane Austen"}}, nil,
			[]Candidate{{"b5", ReasonTitle, 0.9}, {"b6", ReasonTitle, 0.9}}},
		{"author only", PairMeta{Title: "Persuasion", Authors: []string{"Jane Austen"}}, nil, nil},
		{"not same excluded", PairMeta{Title: "Dune", Authors: []string{"Frank Herbert"}}, map[string]bool{"b1": true},
			[]Candidate{{"b2", ReasonAuthor, 0}}},
		{"no title, no signal", PairMeta{}, nil, []Candidate{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Match(c.m, pairTBooks, c.notSame, 0)
			if c.name == "author only" {
				for _, g := range got {
					if g.Reason == ReasonTitle || g.Reason == ReasonISBN {
						t.Errorf("unexpected %+v", g)
					}
				}
				return
			}
			if len(got) < len(c.want) {
				t.Fatalf("got %+v, want at least %d", got, len(c.want))
			}
			for i, w := range c.want {
				if got[i].BookID != w.BookID || got[i].Reason != w.Reason || (w.Score != 0 && got[i].Score != w.Score) {
					t.Errorf("candidate %d = %+v, want %+v", i, got[i], w)
				}
			}
			for _, g := range got {
				if c.notSame[g.BookID] {
					t.Errorf("not-same book %s offered", g.BookID)
				}
			}
		})
	}
	if got := Match(PairMeta{Title: "Emma", Authors: []string{"Jane Austen"}}, pairTBooks, nil, 1); len(got) != 1 {
		t.Errorf("limit 1 gave %d", len(got))
	}
}

func TestPairDecide(t *testing.T) {
	for _, c := range []struct {
		name   string
		m      PairMeta
		books  []BookKey
		wantID string
		wantBy string
	}{
		{"isbn, one book", PairMeta{Title: "x", ISBN: "9780804429573"}, pairTBooks[:2], "b1", "isbn"},
		{"isbn, two books", PairMeta{ISBN: "0306406152"}, pairTBooks[6:], "", ""},
		{"asin", PairMeta{ASIN: "B007978NPG"}, pairTBooks, "b3", "isbn"},
		{"title and author, one", PairMeta{Title: "Dune", Authors: []string{"Frank Herbert"}}, pairTBooks, "b1", "title"},
		{"two candidates, suggestion only", PairMeta{Title: "Emma", Authors: []string{"Jane Austen"}}, pairTBooks, "", ""},
		{"title equal author differs", PairMeta{Title: "The Hobbit", Authors: []string{"Nobody"}}, pairTBooks[2:4], "", ""},
		{"isbn beats a title match on another book", PairMeta{Title: "Dune", Authors: []string{"Frank Herbert"}, ISBN: "0306406152"},
			[]BookKey{pairTBooks[0], {ID: "bx", Title: "Other", ISBN: "9780306406157"}}, "bx", "isbn"},
		{"nothing", PairMeta{Title: "Unknown"}, pairTBooks, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, by, ok := Decide(Match(c.m, c.books, nil, 0))
			if id != c.wantID || by != c.wantBy || ok != (c.wantID != "") {
				t.Errorf("Decide = %q %q %v, want %q %q", id, by, ok, c.wantID, c.wantBy)
			}
		})
	}
}

func TestPairChecks(t *testing.T) {
	m := PairMeta{Title: "Dune (Unabridged)", Authors: []string{"Herbert, Frank"}, ISBN: "0306406152"}
	b := BookKey{Title: "Dune", Authors: []string{"Frank Herbert"}, ISBN: "9780306406157"}
	ch := []ChapterRow{{Title: "Cover", Front: true}, {Title: "One"}, {Title: "Two"}}
	for _, c := range []struct {
		name  string
		m     PairMeta
		b     BookKey
		ch    []ChapterRow
		bt    []string
		kinds []string
		first Check
	}{
		{"all agree", m, b, ch, []string{"one", "TWO"}, []string{"same_title", "same_isbn", "chapters_same"},
			Check{Kind: "same_title", OK: true, Author: "Frank Herbert"}},
		{"same count only", m, b, ch, []string{"1", "2"}, []string{"same_title", "same_isbn", "chapters_count"}, Check{}},
		{"differ", PairMeta{Title: "Emma"}, b, ch, []string{"a"}, []string{"different_title", "chapters_differ"},
			Check{Kind: "different_title"}},
		{"no chapter list", m, b, ch, nil, []string{"same_title", "same_isbn"}, Check{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Checks(c.m, c.b, c.ch, c.bt)
			var kinds []string
			for _, k := range got {
				kinds = append(kinds, k.Kind)
			}
			if !reflect.DeepEqual(kinds, c.kinds) {
				t.Fatalf("kinds = %v, want %v", kinds, c.kinds)
			}
			if c.first.Kind != "" && got[0] != c.first {
				t.Errorf("first = %+v, want %+v", got[0], c.first)
			}
		})
	}
}
