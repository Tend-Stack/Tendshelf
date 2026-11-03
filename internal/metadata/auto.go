package metadata

import (
	"context"
	"database/sql"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// candidateKey names the book a candidate describes: its ASIN, else its provider and title, so the
// same book found by two sites counts once only when the site gave an ASIN.
func candidateKey(c Candidate) string {
	if c.Details.ASIN != "" {
		return c.Details.ASIN
	}
	return c.Provider + "/" + c.Details.Title
}

// pickAuto chooses the candidate to apply without a person looking, or false. Rule A1: the book's
// own ASIN or ISBN equals the candidate's (one such book), or the query has a title and an author
// and exactly one distinct book among the candidates is confident (same title, matching author).
// Two plausible editions, or none, mean a person must look.
func pickAuto(q online.Query, cands []Candidate) (Candidate, bool) {
	if c, ok := onlyBook(cands, func(c Candidate) bool { return c.Exact }); ok {
		return c, true
	}
	if q.Title == "" || q.Author == "" {
		return Candidate{}, false
	}
	return onlyBook(cands, func(c Candidate) bool { return c.Confident })
}

// onlyBook returns the first candidate that passes keep when they all describe one book.
func onlyBook(cands []Candidate, keep func(Candidate) bool) (Candidate, bool) {
	var first Candidate
	seen := map[string]bool{}
	for _, c := range cands {
		if !keep(c) {
			continue
		}
		if len(seen) == 0 {
			first = c
		}
		seen[candidateKey(c)] = true
	}
	if len(seen) != 1 {
		return Candidate{}, false
	}
	return first, true
}

// applyAuto writes a confident match as "online" rows, which any tag, sidecar or person's value
// beats. It never writes a "user" row, and does nothing (outcome "kept") when the book already has
// an online or user row. Author ASINs are set only where empty. It returns whether it applied the
// match. guard (a job's fence check) runs first in the same transaction; it may be nil.
func (s *Service) applyAuto(ctx context.Context, guard func(context.Context, *sql.Tx) error, bookID, jobID string, c Candidate) (bool, error) {
	applied := false
	err := s.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
		b, err := tx.Book(bookID)
		if err != nil {
			return err
		}
		rows, err := tx.MetadataValues(bookID)
		if err != nil {
			return err
		}
		now := s.now().UnixMilli()
		for _, r := range rows {
			if r.Source == SourceOnline || r.Source == SourceUser {
				return tx.PutBookLookup(bookID, library.LookupKept, c.Provider, jobID, now)
			}
		}
		want := c.Details.Values()
		var fields []string
		for _, f := range ApplyFields {
			v, ok := want[f]
			if !ok {
				continue
			}
			if err := tx.PutMetadataValue(bookID, library.MetadataValue{Field: f, Source: SourceOnline, Value: v, ObservedAt: now}); err != nil {
				return err
			}
			fields = append(fields, f)
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
		if err := tx.PutBookLookup(bookID, library.LookupMatched, c.Provider, jobID, now); err != nil {
			return err
		}
		applied = true
		return tx.Audit(db.AuditEntry{At: now, Action: "book.details.auto", Target: "book:" + bookID,
			Detail: map[string]any{"provider": c.Provider, "fields": changedOrEmpty(fields), "job": jobID}})
	})
	return applied, err
}

// DismissLookup marks a book "leave it": automatic and bulk lookups never ask about it again. The
// caller checks who may manage the library.
func (s *Service) DismissLookup(ctx context.Context, bookID, userID string) error {
	return s.repo.Update(ctx, func(tx *library.Tx) error {
		if _, err := tx.Book(bookID); err != nil {
			return err
		}
		now := s.now().UnixMilli()
		if err := tx.PutBookLookup(bookID, library.LookupDismissed, "", "", now); err != nil {
			return err
		}
		return tx.Audit(db.AuditEntry{At: now, UserID: userID, Action: "book.details.dismiss", Target: "book:" + bookID})
	})
}
