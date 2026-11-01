// Package metadata turns what a library already holds (folder names and file tags) into a book's
// details, field by field, and keeps every value's source. Online providers and sidecar files are
// later work.
//
// A source writes only its own rows in metadata_values (folder, tags). The book's columns, people,
// series and genres are then resolved from all rows by the fixed Precedence list, so a value the
// owner typed (source "user") always wins and is never written here. The code imports library,
// media and jobs, never scan: scan reaches this package only through a follower function.
package metadata

import (
	"encoding/json"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Where a value came from.
const (
	SourceUser   = library.MetadataSourceUser // typed by the owner; never written by this package
	SourceOPF    = "opf"
	SourceTxt    = "txt"
	SourceNFO    = "nfo"
	SourceTags   = "tags"
	SourceFolder = "folder"
	SourceOnline = "online"
)

// Precedence ranks sources, highest first. Online is last, so it only fills gaps.
var Precedence = []string{SourceUser, SourceOPF, SourceTxt, SourceNFO, SourceTags, SourceFolder, SourceOnline}

// Fields of metadata_values.
const (
	FieldTitle         = "title"
	FieldSubtitle      = "subtitle"
	FieldAuthors       = "authors"
	FieldNarrators     = "narrators"
	FieldSeries        = "series"
	FieldPublishedYear = "published_year"
	FieldPublisher     = "publisher"
	FieldDescription   = "description"
	FieldLanguage      = "language"
	FieldISBN          = "isbn"
	FieldASIN          = "asin"
	FieldGenres        = "genres"
	FieldAbridged      = "abridged"
	FieldDramatized    = "dramatized"
	FieldExplicit      = "explicit"
)

// SeriesRef is one series a book belongs to. Sequence is text: "2", "0.5", "1-4".
type SeriesRef struct {
	Name     string `json:"name"`
	Sequence string `json:"sequence"`
}

// Details is what one source says about a book. Empty means the source says nothing; a nil bool
// means the same.
type Details struct {
	Title, Subtitle, Description, Publisher, Language, ISBN, ASIN string
	Year                                                          int
	Authors, Narrators, Genres                                    []string
	Series                                                        []SeriesRef
	Abridged, Dramatized, Explicit                                *bool
}

// Values returns the fields that have content, as the JSON stored in metadata_values.
func (d Details) Values() map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	put := func(field string, v any) {
		b, err := json.Marshal(v)
		if err != nil { // only strings, ints, bools and plain structs go in
			panic("metadata: cannot encode " + field + ": " + err.Error())
		}
		out[field] = b
	}
	for _, s := range []struct{ field, v string }{
		{FieldTitle, d.Title}, {FieldSubtitle, d.Subtitle}, {FieldPublisher, d.Publisher},
		{FieldDescription, d.Description}, {FieldLanguage, d.Language}, {FieldISBN, d.ISBN}, {FieldASIN, d.ASIN},
	} {
		if s.v != "" {
			put(s.field, s.v)
		}
	}
	if d.Year > 0 {
		put(FieldPublishedYear, d.Year)
	}
	for _, l := range []struct {
		field string
		v     []string
	}{{FieldAuthors, d.Authors}, {FieldNarrators, d.Narrators}, {FieldGenres, d.Genres}} {
		if len(l.v) > 0 {
			put(l.field, l.v)
		}
	}
	if len(d.Series) > 0 {
		put(FieldSeries, d.Series)
	}
	for _, b := range []struct {
		field string
		v     *bool
	}{{FieldAbridged, d.Abridged}, {FieldDramatized, d.Dramatized}, {FieldExplicit, d.Explicit}} {
		if b.v != nil {
			put(b.field, *b.v)
		}
	}
	return out
}

func boolp(b bool) *bool { return &b }
