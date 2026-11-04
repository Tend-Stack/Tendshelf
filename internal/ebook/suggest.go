package ebook

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Suggested merges for /admin/ebooks (decision 0018, B3). A text book is a suggestion when its default file may
// still be decided by a person (no hold, never paired) and an audiobook looks like the same book, but the engine
// would not take it: the match is not confident, or the guard refused. Rows are read only.

// suggestionsScanned bounds how many text books Suggestions looks at.
const suggestionsScanned = 5000

// Suggestion is a text book that may be the same book as one audiobook.
type Suggestion struct {
	// File is the text book's default file (its BookID is the text book) and Edition what it says about itself.
	File    File
	Edition Edition
	// Match is the audiobook and why it was offered.
	Match Candidate
	// Guard is true when the match is confident but merging it would change who can reach what; a person decides.
	Guard bool
}

// Suggestions lists up to limit suggested merges among the text books and audiobooks f lets the caller see.
func (s *Service) Suggestions(ctx context.Context, f share.Filter, limit int) ([]Suggestion, error) {
	audio, err := s.store.bookKeysKind(ctx, f, library.BookAudio)
	if err != nil || len(audio) == 0 {
		return nil, err
	}
	visible := make(map[string]bool, len(audio))
	for _, k := range audio {
		visible[k.ID] = true
	}
	where := ``
	if f.SQL != "" {
		where = ` AND (` + f.SQL + `)`
	}
	args := append([]any{}, f.Args...)
	args = append(args, suggestionsScanned)
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE book_default = 1 AND hold IS NULL AND paired_by IS NULL AND state = 'ready' AND missing_since IS NULL
AND book_id IN (SELECT b.id FROM books b WHERE b.kind = 'text' AND b.missing_since IS NULL`+where+`) ORDER BY created_at, id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var files []File
	for rs.Next() {
		file, err := scanFile(rs)
		if err != nil {
			rs.Close()
			return nil, err
		}
		files = append(files, file)
	}
	rs.Close()
	if err := rs.Err(); err != nil || len(files) == 0 {
		return nil, err
	}
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.ID
	}
	ns, err := s.store.notSameFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	folders := map[string]map[string][]string{}
	var out []Suggestion
	for _, file := range files {
		if limit > 0 && len(out) >= limit {
			break
		}
		ed, ok, err := s.store.CurrentEdition(ctx, file.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		cands := Match(ed.pairMeta(), audio, ns[file.ID], 0)
		to, _, auto, err := s.autoTarget(ctx, file, ed, audio, ns[file.ID], folders)
		if err != nil {
			return nil, err
		}
		if auto {
			if !visible[to] {
				continue
			}
			allowed, err := s.guardAllows(ctx, file.BookID, to)
			if err != nil {
				return nil, err
			}
			if allowed {
				continue // the engine takes this one
			}
			m := Candidate{BookID: to, Reason: ReasonFolder}
			for _, c := range cands {
				if c.BookID == to {
					m = c
				}
			}
			out = append(out, Suggestion{File: file, Edition: ed, Match: m, Guard: true})
			continue
		}
		if len(cands) > 0 {
			out = append(out, Suggestion{File: file, Edition: ed, Match: cands[0]})
		}
	}
	return out, nil
}

// OnlyAudio keeps, in order, the ids that are live audiobooks.
func (s *Store) OnlyAudio(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT id FROM books WHERE kind = 'audio' AND id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	ok := map[string]bool{}
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		ok[id] = true
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if ok[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// TextBookOf says whether the file is on a text book, and which.
func (s *Store) TextBookOf(ctx context.Context, fileID string) (bookID string, ok bool, err error) {
	err = s.db.Reader().QueryRowContext(ctx, `SELECT b.id FROM ebook_files f JOIN books b ON b.id = f.book_id
WHERE f.id = ? AND b.kind = 'text'`, fileID).Scan(&bookID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return bookID, err == nil, err
}
