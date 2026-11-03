package metadata

import (
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

// TestFileWritesBookTagsRoundTrip proves the tag writer is the inverse of the tag reader: the tags
// media.BookTags makes for a book read back, through FromTags, as the details the book came from.
func TestFileWritesBookTagsRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		d      Details
		single bool
	}{
		{"full", Details{
			Title: "Brontë's Book", Subtitle: "A Sub", Publisher: "Pub House", Description: "Line one; with = signs and a # mark.",
			Language: "eng", ASIN: "B000TEST01", ISBN: "9780000000002", Year: 2021,
			Authors: []string{"Ann Author", "Bo Writer"}, Narrators: []string{"Nia Voice", "Omar Reader"},
			Genres: []string{"Fantasy", "Epic"}, Series: []SeriesRef{{Name: "The Series", Sequence: "2"}},
		}, true},
		{"multi-file part keeps the album title", Details{
			Title: "Plain Book", Authors: []string{"Ann Author"}, Narrators: []string{"Nia Voice"}, Year: 1999,
		}, false},
		{"minimal", Details{Title: "Only A Title"}, true},
		{"series without a number", Details{Title: "Book", Series: []SeriesRef{{Name: "Saga"}}}, true},
		{"non-Latin", Details{Title: "Война и мир", Authors: []string{"Лев Толстой"}, Narrators: []string{"Иван Иванов"}, Language: "rus"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := media.BookInfo{
				Title: c.d.Title, Subtitle: c.d.Subtitle, Publisher: c.d.Publisher, Description: c.d.Description,
				Language: c.d.Language, ASIN: c.d.ASIN, ISBN: c.d.ISBN, Year: c.d.Year,
				Authors: c.d.Authors, Narrators: c.d.Narrators, Genres: c.d.Genres,
			}
			if len(c.d.Series) > 0 {
				info.Series, info.Sequence = c.d.Series[0].Name, c.d.Series[0].Sequence
			}
			got := FromTags(media.BookTags(info, !c.single), c.single)
			if !reflect.DeepEqual(got, c.d) {
				t.Errorf("read back\n%+v\nwant\n%+v", got, c.d)
			}
		})
	}
}
