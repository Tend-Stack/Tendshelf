package metadata

import (
	"reflect"
	"testing"
)

func TestFolderNames(t *testing.T) {
	t.Run("parse_folder_name (Extractor)", func(t *testing.T) {
		if got := ParseFolderName("Book 2 - Title - Subtitle", true).Sequence; got != "2" {
			t.Errorf("sequence = %q", got)
		}
		f := ParseFolderName("(2019) - Foo {A B & C D} [B0015T963C]", false)
		if f.Title != "Foo" || f.Year != 2019 || f.ASIN != "B0015T963C" || !reflect.DeepEqual(f.Narrators, []string{"A B", "C D"}) {
			t.Errorf("folder = %+v", f)
		}
		if got := ParseFolderName("Vol. 3 Title Here", true).Sequence; got != "3" {
			t.Errorf("sequence = %q", got)
		}
		if got := ParseFolderName("101 Dalmatians", true).Title; got != "101 Dalmatians" {
			t.Errorf("title = %q", got)
		}
		if got := ParseFolderName("6. Title", true).Sequence; got != "6" {
			t.Errorf("sequence = %q", got)
		}
	})

	t.Run("parse_book_name (Extractor)", func(t *testing.T) {
		b := ParseBookName("Bridesmaid for Hire (Bridesmaid for Hire #1) (Unabridged)")
		if b.Title != "Bridesmaid for Hire" || b.Series != "Bridesmaid for Hire" || b.Sequence != "1" || b.Abridged == nil || *b.Abridged {
			t.Errorf("book = %+v", b)
		}
		if got := ParseBookName("Box (Campus #1-4) (Unabridged)").Sequence; got != "1-4" {
			t.Errorf("sequence = %q", got)
		}
		if got := ParseBookName("Just for the Cameras by Meghan Quinn"); got.Author != "Meghan Quinn" || got.Title != "Just for the Cameras" {
			t.Errorf("by-author = %+v", got)
		}
		k := ParseBookName("Kennedy Ryan - Skyland 02 - This Could Be Us")
		if k.Author != "Kennedy Ryan" || k.Series != "Skyland" || k.Sequence != "2" || k.Title != "This Could Be Us" {
			t.Errorf("calibre = %+v", k)
		}
	})

	t.Run("getSequence examples (ABS)", func(t *testing.T) {
		cases := []struct {
			in, seq string
			year    int
		}{
			{"Book 2 - Title - Subtitle", "2", 0},
			{"Title - Subtitle - Vol 12", "12", 0},
			{"Title - volume 9 - Subtitle", "9", 0},
			{"Vol. 3 Title Here - Subtitle", "3", 0},
			{"1980 - Book 2 - Title", "2", 1980},
			{"Volume 12. Title - Subtitle", "12", 0},
			{"100 - Book Title", "100", 0},
			{"6. Title", "6", 0},
			{"0.5 - Book Title", "0.5", 0},
		}
		for _, c := range cases {
			f := ParseFolderName(c.in, true)
			if f.Sequence != c.seq || f.Year != c.year {
				t.Errorf("%q: sequence %q year %d, want %q %d", c.in, f.Sequence, f.Year, c.seq, c.year)
			}
		}
		if got := ParseFolderName("1980 - Book 2 - Title", true).Title; got != "Title" {
			t.Errorf("title = %q", got)
		}
		if got := ParseFolderName("Book 2 - Title - Subtitle", false).Sequence; got != "" {
			t.Errorf("no series dir still found sequence %q", got)
		}
	})

	t.Run("a bare Book 4 keeps its name", func(t *testing.T) {
		d := FromPath("/lib", "/lib/Au/Ser/Book 4", false)
		if d.Title != "Book 4" || len(d.Series) != 1 || d.Series[0] != (SeriesRef{"Ser", "4"}) || !reflect.DeepEqual(d.Authors, []string{"Au"}) {
			t.Errorf("details = %+v", d)
		}
	})

	t.Run("layouts", func(t *testing.T) {
		cases := []struct {
			name      string
			bookRoot  string
			single    bool
			title     string
			authors   []string
			series    []SeriesRef
			narrators []string
		}{
			{name: "root single", bookRoot: "/lib/S01.m4b", single: true, title: "S01"},
			{name: "Au/Book", bookRoot: "/lib/Ann Author/The Tide", title: "The Tide", authors: []string{"Ann Author"}},
			{name: "Au/Ser/Book", bookRoot: "/lib/Ann Author/Moon Series/Book 3 - The Tide {Dee Narr}", title: "The Tide",
				authors: []string{"Ann Author"}, series: []SeriesRef{{"Moon Series", "3"}}, narrators: []string{"Dee Narr"}},
			{name: "Au/loose.m4b", bookRoot: "/lib/Ann Author/Loose Book.m4b", single: true, title: "Loose Book", authors: []string{"Ann Author"}},
			{name: "Au/Ser/loose.m4b", bookRoot: "/lib/Ann Author/Moon Series/2 - Loose Book.m4b", single: true, title: "Loose Book",
				authors: []string{"Ann Author"}, series: []SeriesRef{{"Moon Series", "2"}}},
			{name: "deeper than three levels uses the last two dirs", bookRoot: "/lib/Genre/Ann Author/Moon Series/Book 1", title: "Book 1",
				authors: []string{"Ann Author"}, series: []SeriesRef{{"Moon Series", "1"}}},
			{name: "name only", bookRoot: "/lib/Bridesmaid for Hire (Bridesmaid for Hire #1) (Unabridged).m4b", single: true,
				title: "Bridesmaid for Hire", series: []SeriesRef{{"Bridesmaid for Hire", "1"}}},
			{name: "by author does not cut a title when the folders name the author", bookRoot: "/lib/Ann Author/Surrounded by Idiots",
				title: "Surrounded by Idiots", authors: []string{"Ann Author"}},
			{name: "the upload placeholder folder is no author", bookRoot: "/lib/" + UnknownAuthorFolder + "/Dracula", title: "Dracula"},
			{name: "the placeholder is no author above a series", bookRoot: "/lib/" + UnknownAuthorFolder + "/Moon Series/Book 3 - The Tide",
				title: "The Tide", series: []SeriesRef{{"Moon Series", "3"}}},
			{name: "a real author whose name only starts like the placeholder", bookRoot: "/lib/Unknown Author Jr/Dracula",
				title: "Dracula", authors: []string{"Unknown Author Jr"}},
			{name: "book root equals the library folder", bookRoot: "/lib", title: "lib"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				d := FromPath("/lib", c.bookRoot, c.single)
				if d.Title != c.title || !reflect.DeepEqual(d.Authors, c.authors) || !reflect.DeepEqual(d.Series, c.series) ||
					!reflect.DeepEqual(d.Narrators, c.narrators) {
					t.Errorf("details = %+v", d)
				}
			})
		}
	})
}
