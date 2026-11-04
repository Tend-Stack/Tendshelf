package ebook

import (
	"context"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Reads for the Ebooks to pair inbox (M5 R2 P3). Rows only; no file is touched.

// CandidatesFor is Candidates for many files at once: the books are loaded once. A file with no
// current edition has no entry.
func (s *Service) CandidatesFor(ctx context.Context, fileIDs []string, f share.Filter, limit int) (map[string][]Candidate, error) {
	out := make(map[string][]Candidate, len(fileIDs))
	if len(fileIDs) == 0 {
		return out, nil
	}
	keys, err := s.store.bookKeysKind(ctx, f, library.BookAudio)
	if err != nil {
		return nil, err
	}
	ns, err := s.store.notSameFor(ctx, fileIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range fileIDs {
		ed, ok, err := s.store.CurrentEdition(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = Match(ed.pairMeta(), keys, ns[id], limit)
		}
	}
	return out, nil
}

// CountToPair is how many ready files wait to be paired: no book, not kept on their own.
func (s *Store) CountToPair(ctx context.Context) (n int, err error) {
	err = s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM ebook_files
WHERE book_id IS NULL AND state = 'ready' AND (hold IS NULL OR hold <> 'kept')`).Scan(&n)
	return n, err
}
