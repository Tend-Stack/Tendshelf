package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// MetadataSourceUser is the source of values the owner typed. This package refuses to write or
// delete them: user edits belong to the editing feature, and no automatic job may touch them.
const MetadataSourceUser = "user"

// Roles of people on a book, and kinds of labels.
const (
	RoleAuthor   = "author"
	RoleNarrator = "narrator"
	LabelGenre   = "genre"
	LabelTag     = "tag"
)

// MetadataValue is one field's value as seen by one source. Value is JSON: a string, a number or a
// list, depending on the field.
type MetadataValue struct {
	Field, Source string
	Value         json.RawMessage
	ObservedAt    int64
}

// BookPerson is a person linked to a book as an author or a narrator, in order.
type BookPerson struct {
	PersonID, Name, Role string
	Idx                  int
}

// BookSeriesLink is a book's place in a series. Sequence is text because it can be "0.5" or "1a".
type BookSeriesLink struct {
	SeriesID, Name, Sequence string
	Idx                      int
}

func scanMetadataValue(s scanner) (MetadataValue, error) {
	var v MetadataValue
	var raw string
	err := s.Scan(&v.Field, &v.Source, &raw, &v.ObservedAt)
	v.Value = json.RawMessage(raw)
	return v, err
}

const metadataQuery = `SELECT field, source, value_json, observed_at FROM metadata_values WHERE book_id = ? ORDER BY field, source`

// MetadataValues returns every stored value of a book, ordered by field, then source.
func (t *Tx) MetadataValues(bookID string) ([]MetadataValue, error) {
	return many(t.ctx, t.tx, scanMetadataValue, metadataQuery, bookID)
}

// MetadataValues returns every stored value of a book, ordered by field, then source.
func (r *Repo) MetadataValues(ctx context.Context, bookID string) ([]MetadataValue, error) {
	return many(ctx, r.st.Reader(), scanMetadataValue, metadataQuery, bookID)
}

func checkMetadataKey(field, source string) error {
	if field == "" || source == "" {
		return invalid("metadata field and source are required")
	}
	if source == MetadataSourceUser {
		return invalid("user values are never written by this call")
	}
	return nil
}

// PutMetadataValue stores or replaces the value of one (field, source) of a book. It refuses the
// user source and values that are not valid JSON (ErrInvalid), and an unknown book (ErrNotFound).
func (t *Tx) PutMetadataValue(bookID string, v MetadataValue) error {
	if err := checkMetadataKey(v.Field, v.Source); err != nil {
		return err
	}
	if !json.Valid(v.Value) {
		return invalid("metadata value is not valid JSON")
	}
	_, err := t.exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?,?,?,?,?)
  ON CONFLICT(book_id, field, source) DO UPDATE SET value_json = excluded.value_json, observed_at = excluded.observed_at`,
		bookID, v.Field, v.Source, string(v.Value), v.ObservedAt)
	return mapErr(err, "book")
}

// DeleteMetadataValue removes one (field, source) value. A missing row is not an error. The user
// source is refused (ErrInvalid).
func (t *Tx) DeleteMetadataValue(bookID, field, source string) error {
	if err := checkMetadataKey(field, source); err != nil {
		return err
	}
	_, err := t.exec(`DELETE FROM metadata_values WHERE book_id = ? AND field = ? AND source = ?`, bookID, field, source)
	return err
}

// ensureNamed returns the id of the row in table with this name (case-insensitive, lowest id), or
// inserts one. table is one of two constants, never user input.
func (t *Tx) ensureNamed(table, prefix, name, sortName string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("name is required")
	}
	var id string
	err := t.tx.QueryRowContext(t.ctx, `SELECT id FROM `+table+` WHERE name = ? COLLATE NOCASE ORDER BY id LIMIT 1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = db.NewID(prefix)
	_, err = t.exec(`INSERT INTO `+table+`(id, name, sort_name, created_at) VALUES (?,?,?,?)`,
		id, name, strings.TrimSpace(sortName), t.r.nowMs())
	if err != nil {
		return "", err
	}
	return id, nil
}

// EnsurePerson returns the id of the person with this name (case-insensitive), creating it when
// there is none.
func (t *Tx) EnsurePerson(name, sortName string) (string, error) {
	return t.ensureNamed("people", "ps", name, sortName)
}

// EnsureSeries returns the id of the series with this name (case-insensitive), creating it when
// there is none.
func (t *Tx) EnsureSeries(name, sortName string) (string, error) {
	return t.ensureNamed("series", "sr", name, sortName)
}

func scanBookPerson(s scanner) (BookPerson, error) {
	var p BookPerson
	return p, s.Scan(&p.PersonID, &p.Name, &p.Role, &p.Idx)
}

const bookPeopleQuery = `SELECT bp.person_id, p.name, bp.role, bp.idx FROM book_people bp
  JOIN people p ON p.id = bp.person_id WHERE bp.book_id = ? ORDER BY bp.role, bp.idx, bp.person_id`

// BookPeople returns a book's authors and narrators ordered by role, then position.
func (t *Tx) BookPeople(bookID string) ([]BookPerson, error) {
	return many(t.ctx, t.tx, scanBookPerson, bookPeopleQuery, bookID)
}

// BookPeople returns a book's authors and narrators ordered by role, then position.
func (r *Repo) BookPeople(ctx context.Context, bookID string) ([]BookPerson, error) {
	return many(ctx, r.st.Reader(), scanBookPerson, bookPeopleQuery, bookID)
}

// SetBookPeople replaces the people of one role on a book; the position is the index in personIDs.
// Repeated ids are kept once. An unknown role is ErrInvalid; an unknown book or person ErrNotFound.
func (t *Tx) SetBookPeople(bookID, role string, personIDs []string) error {
	if role != RoleAuthor && role != RoleNarrator {
		return invalid("unknown role %q", role)
	}
	if _, err := t.exec(`DELETE FROM book_people WHERE book_id = ? AND role = ?`, bookID, role); err != nil {
		return err
	}
	seen := map[string]bool{}
	idx := 0
	for _, id := range personIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := t.exec(`INSERT INTO book_people(book_id, person_id, role, idx) VALUES (?,?,?,?)`, bookID, id, role, idx); err != nil {
			return mapErr(err, "book or person")
		}
		idx++
	}
	return nil
}

func scanBookSeries(s scanner) (BookSeriesLink, error) {
	var l BookSeriesLink
	return l, s.Scan(&l.SeriesID, &l.Name, &l.Sequence, &l.Idx)
}

const bookSeriesQuery = `SELECT bs.series_id, s.name, bs.sequence, bs.idx FROM book_series bs
  JOIN series s ON s.id = bs.series_id WHERE bs.book_id = ? ORDER BY bs.idx, bs.series_id`

// BookSeries returns a book's series links in order.
func (t *Tx) BookSeries(bookID string) ([]BookSeriesLink, error) {
	return many(t.ctx, t.tx, scanBookSeries, bookSeriesQuery, bookID)
}

// BookSeries returns a book's series links in order.
func (r *Repo) BookSeries(ctx context.Context, bookID string) ([]BookSeriesLink, error) {
	return many(ctx, r.st.Reader(), scanBookSeries, bookSeriesQuery, bookID)
}

// SetBookSeries replaces a book's series links; SeriesID and Sequence are used, and the position is
// the index in links. A series repeated in links is kept once (the first).
func (t *Tx) SetBookSeries(bookID string, links []BookSeriesLink) error {
	if _, err := t.exec(`DELETE FROM book_series WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	seen := map[string]bool{}
	idx := 0
	for _, l := range links {
		if seen[l.SeriesID] {
			continue
		}
		seen[l.SeriesID] = true
		if _, err := t.exec(`INSERT INTO book_series(book_id, series_id, sequence, idx) VALUES (?,?,?,?)`,
			bookID, l.SeriesID, l.Sequence, idx); err != nil {
			return mapErr(err, "book or series")
		}
		idx++
	}
	return nil
}

func labelsOf(rows *sql.Rows) (map[string][]string, error) {
	out := map[string][]string{}
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, err
		}
		out[kind] = append(out[kind], value)
	}
	return out, rows.Err()
}

const bookLabelsQuery = `SELECT kind, value FROM book_labels WHERE book_id = ? ORDER BY kind, value`

// BookLabels returns a book's labels by kind, each list ordered by value.
func (t *Tx) BookLabels(bookID string) (map[string][]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, bookLabelsQuery, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return labelsOf(rows)
}

// BookLabels returns a book's labels by kind, each list ordered by value.
func (r *Repo) BookLabels(ctx context.Context, bookID string) (map[string][]string, error) {
	rows, err := r.st.Reader().QueryContext(ctx, bookLabelsQuery, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return labelsOf(rows)
}

// SetBookLabels replaces the labels of one kind on a book. Blank values are skipped and
// case-insensitive duplicates are kept once.
func (t *Tx) SetBookLabels(bookID, kind string, values []string) error {
	if kind != LabelGenre && kind != LabelTag {
		return invalid("unknown label kind %q", kind)
	}
	if _, err := t.exec(`DELETE FROM book_labels WHERE book_id = ? AND kind = ?`, bookID, kind); err != nil {
		return err
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, err := t.exec(`INSERT OR IGNORE INTO book_labels(book_id, kind, value) VALUES (?,?,?)`, bookID, kind, v); err != nil {
			return mapErr(err, "book")
		}
	}
	return nil
}
