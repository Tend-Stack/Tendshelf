package metadata

import (
	"reflect"
	"strings"
	"testing"
)

func flagIs(b *bool, want bool) bool { return b != nil && *b == want }

func TestFromTags(t *testing.T) {
	t.Run("tag mapping (Extractor test_tag_mapping)", func(t *testing.T) {
		d := FromTags(map[string]string{
			"title": "T", "album": "Alb", "artist": "Ann Author", "composer": "Ben Narrator & Cara Reader",
			"genre": "Romance", "date": "2022-03-04", "creation_time": "2001-01-01", "publisher": "Pub",
			"grouping": "First Series #2; Other #5", "subtitle": "Sub", "language": "eng",
			"asin": "b0015t963c", "isbn": "978-1-234-56789-7", "comment": "An Unabridged recording, full cast.",
		}, false)
		if !reflect.DeepEqual(d.Authors, []string{"Ann Author"}) || !reflect.DeepEqual(d.Narrators, []string{"Ben Narrator", "Cara Reader"}) ||
			d.Year != 2022 || !reflect.DeepEqual(d.Series, []SeriesRef{{"First Series", "2"}}) {
			t.Errorf("people/year/series = %+v", d)
		}
		if d.Subtitle != "Sub" || d.Language != "eng" || d.ASIN != "B0015T963C" || d.ISBN != "9781234567897" || d.Publisher != "Pub" {
			t.Errorf("identifiers = %+v", d)
		}
		if !flagIs(d.Abridged, false) || !flagIs(d.Dramatized, true) {
			t.Errorf("production = %v %v", d.Abridged, d.Dramatized)
		}
		if d.Title != "Alb" {
			t.Errorf("multi-file title = %q, want the album", d.Title)
		}
		if got := FromTags(map[string]string{"creation_time": "2001-01-01"}, false).Year; got != 0 {
			t.Errorf("creation_time gave year %d", got)
		}
		s := FromTags(map[string]string{"series": "A; B", "series-part": "1; 2"}, false)
		if !reflect.DeepEqual(s.Series, []SeriesRef{{"A", "1"}}) {
			t.Errorf("series = %+v", s.Series)
		}
		if got := FromTags(map[string]string{"narrator": "Eve", "composer": "Dee"}, false).Narrators; !reflect.DeepEqual(got, []string{"Eve"}) {
			t.Errorf("narrator = %v", got)
		}
	})

	t.Run("album against title", func(t *testing.T) {
		tags := map[string]string{"album": "The Book", "title": "Part 01"}
		if got := FromTags(tags, false).Title; got != "The Book" {
			t.Errorf("multi = %q", got)
		}
		if got := FromTags(tags, true).Title; got != "The Book" {
			t.Errorf("single with album = %q", got)
		}
		if got := FromTags(map[string]string{"title": "Part 01"}, false).Title; got != "" {
			t.Errorf("multi with only a track title = %q", got)
		}
		if got := FromTags(map[string]string{"title": "Whole Book"}, true).Title; got != "Whole Book" {
			t.Errorf("single with only a title = %q", got)
		}
	})

	t.Run("authors use the Audiobookshelf order", func(t *testing.T) {
		d := FromTags(map[string]string{"artist": "Artist X", "album_artist": "Album Y", "author": "Author Z"}, false)
		if !reflect.DeepEqual(d.Authors, []string{"Album Y"}) {
			t.Errorf("authors = %v", d.Authors)
		}
		d = FromTags(map[string]string{"artist": "Artist X", "author": "Author Z"}, false)
		if !reflect.DeepEqual(d.Authors, []string{"Artist X"}) {
			t.Errorf("authors = %v", d.Authors)
		}
	})

	t.Run("explicit", func(t *testing.T) {
		for _, c := range []struct {
			v    string
			want *bool
		}{{"1", boolp(true)}, {"2", boolp(false)}, {"0", nil}, {"", nil}} {
			got := FromTags(map[string]string{"itunesadvisory": c.v}, false).Explicit
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Errorf("itunesadvisory %q -> %v", c.v, got)
			}
		}
	})

	t.Run("cleaning", func(t *testing.T) {
		d := FromTags(map[string]string{
			"asin": "short", "isbn": "123", "year": "1899",
			"genre":       "Fantasy; sci-fi/Fantasy/ A/B/C/D/E/F/G/H/I/J/K",
			"description": "<p>Hello   <b>world</b></p>" + strings.Repeat("x", 11000),
		}, false)
		if d.ASIN != "" || d.ISBN != "" || d.Year != 1899 {
			t.Errorf("identifiers = %q %q %d", d.ASIN, d.ISBN, d.Year)
		}
		if len(d.Genres) != 10 || d.Genres[0] != "Fantasy" || d.Genres[1] != "sci-fi" {
			t.Errorf("genres = %q", d.Genres)
		}
		if !strings.HasPrefix(d.Description, "Hello world") || len([]rune(d.Description)) != 10000 {
			t.Errorf("description has %d runes: %.30q", len([]rune(d.Description)), d.Description)
		}
		if got := FromTags(map[string]string{"date": "3001"}, false).Year; got != 0 {
			t.Errorf("year 3001 accepted: %d", got)
		}
		if got := FromTags(map[string]string{"isbn": "080442957x"}, false).ISBN; got != "080442957X" {
			t.Errorf("isbn10 = %q", got)
		}
	})

	t.Run("narrator from the comment and series from the name", func(t *testing.T) {
		d := FromTags(map[string]string{"comment": "Narrated by: Jane Doe. A fine book.", "series": "Moon Cycle, Book 3"}, false)
		if !reflect.DeepEqual(d.Narrators, []string{"Jane Doe"}) || !reflect.DeepEqual(d.Series, []SeriesRef{{"Moon Cycle", "3"}}) {
			t.Errorf("details = %+v", d)
		}
	})
}
