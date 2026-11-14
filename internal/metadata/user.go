package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Editing details by hand (decisions 0015 and the details plan, "Edit details"). A person's values are rows of
// source "user", which outrank every other source, so a rescan, a sidecar or an online lookup never overwrites
// them. The book's columns, people, series and genres are then resolved by syncResolved, the code a lookup
// uses, so the two never drift apart.

// Bounds of the hand-edit form. Titles and names are short; a description may be long.
const (
	maxEditTitle       = 300
	maxEditShort       = 200
	maxEditLanguage    = 35
	maxEditDescription = 10000
	maxEditNames       = 20
	maxEditGenres      = 20
	maxEditSeriesNum   = 20
)

// UserDetails is the hand-edit form as text, the way a person types it: lists are separated by commas or new
// lines, the year is four digits, and the series is a name and a number.
type UserDetails struct {
	Title, Subtitle, Authors, Narrators, SeriesName, SeriesNumber string
	Year, Publisher, Language, Genres, ISBN, ASIN, Description    string
}

// FieldProblem is one plain sentence about a field of the form that was refused.
type FieldProblem struct {
	Field   string // the form field name, e.g. "title"
	Message string
}

// Problems is returned when the form has fields that cannot be saved; nothing was written.
type Problems []FieldProblem

func (p Problems) Error() string {
	if len(p) == 0 {
		return "metadata: invalid details"
	}
	return "metadata: invalid details: " + p[0].Message
}

// splitList splits "A, B\nC" into trimmed, distinct (case-insensitive) names in order.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ';' }) {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" || slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, part) }) {
			continue
		}
		out = append(out, part)
	}
	return out
}

func joinList(l []string) string { return strings.Join(l, ", ") }

// effective is what the book shows now: the strongest value of each field, and, for a field no row says
// anything about, what the book's own columns, people and series hold (a book put there without rows).
func effective(d Details, b library.Book, people []library.BookPerson, series []library.BookSeriesLink) Details {
	fill := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	fill(&d.Title, b.Title)
	fill(&d.Subtitle, b.Subtitle)
	fill(&d.Description, b.Description)
	fill(&d.Publisher, b.Publisher)
	fill(&d.Language, b.Language)
	fill(&d.ISBN, b.ISBN)
	fill(&d.ASIN, b.ASIN)
	if d.Year == 0 {
		d.Year = b.PublishedYear
	}
	if len(d.Authors) == 0 || len(d.Narrators) == 0 {
		var authors, narrators []string
		for _, p := range people {
			switch p.Role {
			case library.RoleAuthor:
				authors = append(authors, p.Name)
			case library.RoleNarrator:
				narrators = append(narrators, p.Name)
			}
		}
		if len(d.Authors) == 0 {
			d.Authors = authors
		}
		if len(d.Narrators) == 0 {
			d.Narrators = narrators
		}
	}
	if len(d.Series) == 0 {
		for _, l := range series {
			d.Series = append(d.Series, SeriesRef{Name: l.Name, Sequence: l.Sequence})
		}
	}
	return d
}

// formOf is the effective details as form text.
func formOf(d Details, title string) UserDetails {
	f := UserDetails{
		Title: d.Title, Subtitle: d.Subtitle, Authors: joinList(d.Authors), Narrators: joinList(d.Narrators),
		Publisher: d.Publisher, Language: d.Language, Genres: joinList(d.Genres), ISBN: d.ISBN, ASIN: d.ASIN, Description: d.Description,
	}
	if f.Title == "" {
		f.Title = title
	}
	if d.Year > 0 {
		f.Year = strconv.Itoa(d.Year)
	}
	if len(d.Series) > 0 {
		f.SeriesName, f.SeriesNumber = d.Series[0].Name, d.Series[0].Sequence
	}
	return f
}

// DetailsForm returns the book's effective details as form text, for prefilling the edit form.
func (s *Service) DetailsForm(ctx context.Context, bookID string) (UserDetails, error) {
	b, err := s.repo.Book(ctx, bookID)
	if err != nil {
		return UserDetails{}, err
	}
	rows, err := s.repo.MetadataValues(ctx, bookID)
	if err != nil {
		return UserDetails{}, err
	}
	people, err := s.repo.BookPeople(ctx, bookID)
	if err != nil {
		return UserDetails{}, err
	}
	series, err := s.repo.BookSeries(ctx, bookID)
	if err != nil {
		return UserDetails{}, err
	}
	d, _ := Resolve(rows)
	return formOf(effective(d, b, people, series), b.Title), nil
}

func tooLong(field, label string, s string, max int, p *Problems) {
	if utf8.RuneCountInString(s) > max {
		*p = append(*p, FieldProblem{field, fmt.Sprintf("%s can have at most %d characters.", label, max)})
	}
}

// typed is one submitted field, ready to compare and store: its JSON, or empty when the person cleared it.
type typed struct {
	field string
	form  string
	value json.RawMessage // nil when cleared
}

// parseUserDetails checks the form and returns the fields in the order of ApplyFields.
func parseUserDetails(in UserDetails) ([]typed, Problems) {
	var p Problems
	enc := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			panic("metadata: cannot encode: " + err.Error())
		}
		return b
	}
	str := func(field, form, label string, s string, max int) typed {
		s = strings.TrimSpace(s)
		tooLong(form, label, s, max, &p)
		t := typed{field: field, form: form}
		if s != "" {
			t.value = enc(s)
		}
		return t
	}
	list := func(field, form, label, s string, max int) typed {
		l := splitList(s)
		if len(l) > max {
			p = append(p, FieldProblem{form, fmt.Sprintf("%s can have at most %d entries.", label, max)})
		}
		for _, x := range l {
			tooLong(form, label, x, maxEditShort, &p)
		}
		t := typed{field: field, form: form}
		if len(l) > 0 {
			t.value = enc(l)
		}
		return t
	}
	out := []typed{
		str(FieldTitle, "title", "The title", in.Title, maxEditTitle),
		str(FieldSubtitle, "subtitle", "The subtitle", in.Subtitle, maxEditTitle),
		list(FieldAuthors, "authors", "Authors", in.Authors, maxEditNames),
		list(FieldNarrators, "narrators", "Narrators", in.Narrators, maxEditNames),
	}
	series := typed{field: FieldSeries, form: "series"}
	name, num := strings.TrimSpace(in.SeriesName), strings.TrimSpace(in.SeriesNumber)
	tooLong("series", "The series name", name, maxEditShort, &p)
	tooLong("series_number", "The book number", num, maxEditSeriesNum, &p)
	switch {
	case name != "":
		series.value = enc([]SeriesRef{{Name: name, Sequence: num}})
	case num != "":
		p = append(p, FieldProblem{"series", "Add the series name too, or clear the book number."})
	}
	out = append(out, series)
	year := typed{field: FieldPublishedYear, form: "year"}
	if y := strings.TrimSpace(in.Year); y != "" {
		if n, err := strconv.Atoi(y); err != nil || len(y) != 4 || n < 1000 || n > 2999 {
			p = append(p, FieldProblem{"year", "The year is four digits, like 1999."})
		} else {
			year.value = enc(n)
		}
	}
	out = append(out, year,
		str(FieldPublisher, "publisher", "The publisher", in.Publisher, maxEditShort),
		str(FieldDescription, "description", "The description", in.Description, maxEditDescription),
		str(FieldLanguage, "language", "The language", in.Language, maxEditLanguage),
		list(FieldGenres, "genres", "Genres", in.Genres, maxEditGenres))
	isbn := typed{field: FieldISBN, form: "isbn"}
	if v := strings.TrimSpace(in.ISBN); v != "" {
		if c := cleanISBN(v); c == "" {
			p = append(p, FieldProblem{"isbn", "The ISBN is 10 or 13 digits."})
		} else {
			isbn.value = enc(c)
		}
	}
	asin := typed{field: FieldASIN, form: "asin"}
	if v := strings.TrimSpace(in.ASIN); v != "" {
		if c := cleanASIN(v); c == "" {
			p = append(p, FieldProblem{"asin", "The ASIN is 10 letters and digits."})
		} else {
			asin.value = enc(c)
		}
	}
	out = append(out, isbn, asin)
	return out, p
}

// SaveUserDetails stores what a person typed on the edit form. A field whose submitted value equals what the
// book resolves to now writes nothing; a changed field becomes a "user" row; a cleared field removes its "user"
// row, so the value from the files or a lookup returns. Everything is then resolved by the code lookups use.
// It returns the fields that were saved or cleared, in form order. A refused form is Problems and writes nothing.
// The caller checks who may edit.
func (s *Service) SaveUserDetails(ctx context.Context, bookID string, in UserDetails, userID string) ([]string, error) {
	fields, problems := parseUserDetails(in)
	if len(problems) > 0 {
		return nil, problems
	}
	var changed []string
	err := s.repo.Update(ctx, func(tx *library.Tx) error {
		b, err := tx.Book(bookID)
		if err != nil {
			return err
		}
		rows, err := tx.MetadataValues(bookID)
		if err != nil {
			return err
		}
		people, err := tx.BookPeople(bookID)
		if err != nil {
			return err
		}
		series, err := tx.BookSeries(bookID)
		if err != nil {
			return err
		}
		before, _ := Resolve(rows)
		eff := effective(before, b, people, series)
		shown := eff.Values()
		prefilled := formOf(eff, b.Title)
		hasUser := map[string]bool{}
		for _, r := range rows {
			if r.Source == SourceUser {
				hasUser[r.Field] = true
			}
		}
		now := s.now().UnixMilli()
		for _, f := range fields {
			if untouched(f.form, in, prefilled) {
				continue // the person left what the form showed: it is not rewritten, not split, not pinned
			}
			cur, has := shown[f.field]
			if f.field == FieldSeries && len(eff.Series) > 1 {
				// The form edits the first series only; the others stay as they are.
				rest := eff.Series[1:]
				var refs []SeriesRef
				if f.value != nil {
					if err := json.Unmarshal(f.value, &refs); err != nil {
						return err
					}
				}
				if refs = append(refs, rest...); len(refs) > 0 {
					raw, err := json.Marshal(refs)
					if err != nil {
						return err
					}
					f.value = raw
				}
			}
			switch {
			case f.value == nil && !hasUser[f.field]:
				continue // nothing to clear
			case f.value == nil:
				if err := tx.DeleteUserMetadataValue(bookID, f.field); err != nil {
					return err
				}
			case has && sameJSON(cur, f.value):
				continue // unchanged
			default:
				if err := tx.PutUserMetadataValue(bookID, library.MetadataValue{Field: f.field, Value: f.value, ObservedAt: now}); err != nil {
					return err
				}
			}
			changed = append(changed, f.form)
		}
		if len(changed) == 0 {
			return nil
		}
		if rows, err = tx.MetadataValues(bookID); err != nil {
			return err
		}
		if _, err := syncResolved(tx, b, rows); err != nil {
			return err
		}
		return tx.Audit(db.AuditEntry{At: now, UserID: userID, Action: "book.details.edit", Target: "book:" + bookID,
			Detail: map[string]any{"fields": changed}})
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

// formText is a submitted or prefilled value as compared: trimmed, with the line ends a browser sends (CRLF)
// turned into LF.
func formText(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s))
}

// untouched reports whether the submitted form field says the same text the form was prefilled with, so saving
// leaves that field alone (an author such as "Downey, Jr." is not split into two, a multi-line description is not
// pinned as a "user" value just because the form was sent).
func untouched(form string, in, prefilled UserDetails) bool {
	same := func(a, b string) bool { return formText(a) == formText(b) }
	switch form {
	case "title":
		return same(in.Title, prefilled.Title)
	case "subtitle":
		return same(in.Subtitle, prefilled.Subtitle)
	case "authors":
		return same(in.Authors, prefilled.Authors)
	case "narrators":
		return same(in.Narrators, prefilled.Narrators)
	case "series":
		return same(in.SeriesName, prefilled.SeriesName) && same(in.SeriesNumber, prefilled.SeriesNumber)
	case "year":
		return same(in.Year, prefilled.Year)
	case "publisher":
		return same(in.Publisher, prefilled.Publisher)
	case "description":
		return same(in.Description, prefilled.Description)
	case "language":
		return same(in.Language, prefilled.Language)
	case "genres":
		return same(in.Genres, prefilled.Genres)
	case "isbn":
		return same(in.ISBN, prefilled.ISBN)
	case "asin":
		return same(in.ASIN, prefilled.ASIN)
	}
	return false
}
