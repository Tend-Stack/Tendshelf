package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// ErrBadField is returned when a field to apply is not one of ApplyFields.
var ErrBadField = errors.New("metadata: unknown field")

// ApplyFields are the fields a person may take from an online match.
var ApplyFields = []string{FieldTitle, FieldSubtitle, FieldAuthors, FieldNarrators, FieldSeries,
	FieldPublishedYear, FieldPublisher, FieldDescription, FieldLanguage, FieldGenres, FieldISBN, FieldASIN, FieldAbridged}

// SourceInfo says which source a field's value came from and when it was last seen.
type SourceInfo struct {
	Source     string
	ObservedAt int64
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// ApplyOnline writes the ticked fields of an online match. Each value becomes an "online" row,
// which gives way to any sidecar later; when the field already holds a different value, or the
// person has a value of their own there, a "user" row with the same value is written as well, so
// the person's choice wins. The book's columns, people, series and genres are then resolved by the
// same code as a local refresh. It returns the fields whose resolved value changed. A field the
// match has no value for is skipped. The caller checks who may edit, and passes a candidate that
// came from a stored lookup result, never one built from request data.
func (s *Service) ApplyOnline(ctx context.Context, bookID string, c Candidate, fields []string, userID string) ([]string, error) {
	for _, f := range fields {
		if !slices.Contains(ApplyFields, f) {
			return nil, ErrBadField
		}
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
		before, _ := Resolve(rows)
		beforeVals := before.Values()
		hasUser := map[string]bool{}
		for _, r := range rows {
			if r.Source == SourceUser {
				hasUser[r.Field] = true
			}
		}
		want := c.Details.Values()
		now := s.now().UnixMilli()
		var applied []string
		for _, f := range ApplyFields {
			if !slices.Contains(fields, f) || slices.Contains(applied, f) {
				continue
			}
			v, ok := want[f]
			if !ok {
				continue
			}
			applied = append(applied, f)
			if err := tx.PutMetadataValue(bookID, library.MetadataValue{Field: f, Source: SourceOnline, Value: v, ObservedAt: now}); err != nil {
				return err
			}
			cur, has := beforeVals[f]
			if hasUser[f] || (has && !sameJSON(cur, v)) {
				if err := tx.PutUserMetadataValue(bookID, library.MetadataValue{Field: f, Value: v, ObservedAt: now}); err != nil {
					return err
				}
			}
		}
		if rows, err = tx.MetadataValues(bookID); err != nil {
			return err
		}
		if _, err := syncResolved(tx, b, rows); err != nil {
			return err
		}
		for name, asin := range c.AuthorASINs {
			if a := cleanASIN(asin); a != "" {
				if err := tx.SetAuthorASIN(bookID, name, a); err != nil {
					return err
				}
			}
		}
		after, _ := Resolve(rows)
		afterVals := after.Values()
		for _, f := range applied {
			if !sameJSON(beforeVals[f], afterVals[f]) {
				changed = append(changed, f)
			}
		}
		return tx.Audit(db.AuditEntry{At: now, UserID: userID, Action: "book.details.apply", Target: "book:" + bookID,
			Detail: map[string]any{"provider": c.Provider, "fields": changedOrEmpty(changed)}})
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

func changedOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// ResetOnline removes the "user" and "online" values of a book and resolves it again, so the
// details come from the files (tags, sidecars and folder names) once more.
func (s *Service) ResetOnline(ctx context.Context, bookID, userID string) error {
	return s.repo.Update(ctx, func(tx *library.Tx) error {
		b, err := tx.Book(bookID)
		if err != nil {
			return err
		}
		if err := tx.DeleteChosenMetadata(bookID); err != nil {
			return err
		}
		rows, err := tx.MetadataValues(bookID)
		if err != nil {
			return err
		}
		if _, err := syncResolved(tx, b, rows); err != nil {
			return err
		}
		return tx.Audit(db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: "book.details.reset", Target: "book:" + bookID})
	})
}

// Sources returns, for every resolved field of the book, the source of its value and when that
// source last saw it. The compare sheet shows these as badges.
func (s *Service) Sources(ctx context.Context, bookID string) (map[string]SourceInfo, error) {
	if _, err := s.repo.Book(ctx, bookID); err != nil {
		return nil, err
	}
	rows, err := s.repo.MetadataValues(ctx, bookID)
	if err != nil {
		return nil, err
	}
	_, from := Resolve(rows)
	out := make(map[string]SourceInfo, len(from))
	for field, src := range from {
		info := SourceInfo{Source: src}
		for _, r := range rows {
			if r.Field == field && r.Source == src {
				info.ObservedAt = r.ObservedAt
			}
		}
		out[field] = info
	}
	return out, nil
}
