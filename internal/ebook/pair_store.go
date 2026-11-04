package ebook

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// pairBatch is how many unpaired files the engine reads at a time.
const pairBatch = 500

// suggestScan bounds how many unpaired files SuggestionsForBook looks at.
const suggestScan = 5000

func (e Edition) pairMeta() PairMeta {
	var authors []string
	_ = json.Unmarshal([]byte(e.AuthorsJSON), &authors)
	return PairMeta{Title: e.Title, Authors: authors, ISBN: e.ISBN, ASIN: e.ASIN}
}

// EditionMeta is what an edition says about itself, for matching.
func EditionMeta(e Edition) PairMeta { return e.pairMeta() }

// bookKeys lists live books with their authors. f limits the books (empty for the engine, which has no person).
func (s *Store) bookKeys(ctx context.Context, f share.Filter) ([]BookKey, error) {
	return s.bookKeysKind(ctx, f, "")
}

// bookKeysKind is bookKeys limited to one kind of book ('audio' or 'text'); an empty kind lists both.
func (s *Store) bookKeysKind(ctx context.Context, f share.Filter, kind string) ([]BookKey, error) {
	where := `b.missing_since IS NULL`
	if kind != "" {
		where += ` AND b.kind = '` + kind + `'` // kind is one of two constants, never input
	}
	if f.SQL != "" {
		where += ` AND (` + f.SQL + `)`
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT b.id, b.title, b.isbn, b.asin FROM books b WHERE `+where+` ORDER BY b.id`, f.Args...)
	if err != nil {
		return nil, err
	}
	var out []BookKey
	idx := map[string]int{}
	for rs.Next() {
		var k BookKey
		if err := rs.Scan(&k.ID, &k.Title, &k.ISBN, &k.ASIN); err != nil {
			rs.Close()
			return nil, err
		}
		idx[k.ID] = len(out)
		out = append(out, k)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}
	as, err := s.db.Reader().QueryContext(ctx, `SELECT bp.book_id, p.name FROM book_people bp
JOIN people p ON p.id = bp.person_id JOIN books b ON b.id = bp.book_id
WHERE bp.role = 'author' AND `+where+` ORDER BY bp.book_id, bp.idx`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer as.Close()
	for as.Next() {
		var id, name string
		if err := as.Scan(&id, &name); err != nil {
			return nil, err
		}
		if i, ok := idx[id]; ok {
			out[i].Authors = append(out[i].Authors, name)
		}
	}
	if err := as.Err(); err != nil {
		return nil, err
	}
	if kind == library.BookText {
		return out, s.fillFromEditions(ctx, out)
	}
	return out, nil
}

// fillFromEditions gives a text book what its default file says where the book row says nothing yet: the
// details job fills the row a moment after the book is made, and a second format arriving in that moment
// must still match (ISBN, ASIN, authors).
func (s *Store) fillFromEditions(ctx context.Context, keys []BookKey) error {
	if len(keys) == 0 {
		return nil
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT f.book_id, e.isbn, e.asin, e.authors_json FROM ebook_files f
JOIN editions e ON e.ebook_file_id = f.id AND e.source_sha256 = f.sha256
JOIN books b ON b.id = f.book_id AND b.kind = 'text'
WHERE f.book_default = 1 AND f.state = 'ready' ORDER BY e.created_at, e.id`)
	if err != nil {
		return err
	}
	defer rs.Close()
	byID := map[string]int{}
	for i, k := range keys {
		byID[k.ID] = i
	}
	for rs.Next() {
		var id, isbn, asin, authors string
		if err := rs.Scan(&id, &isbn, &asin, &authors); err != nil {
			return err
		}
		i, ok := byID[id]
		if !ok {
			continue
		}
		k := &keys[i]
		if k.ISBN == "" {
			k.ISBN = isbn
		}
		if k.ASIN == "" {
			k.ASIN = asin
		}
		if len(k.Authors) == 0 {
			var as []string
			_ = json.Unmarshal([]byte(authors), &as)
			k.Authors = as
		}
	}
	return rs.Err()
}

// notSameFor returns, per file, the books a person said it is not.
func (s *Store) notSameFor(ctx context.Context, fileIDs []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if len(fileIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(fileIDs))
	for i, id := range fileIDs {
		args[i] = id
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT ebook_file_id, book_id FROM ebook_not_same WHERE ebook_file_id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(fileIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var f, b string
		if err := rs.Scan(&f, &b); err != nil {
			return nil, err
		}
		if out[f] == nil {
			out[f] = map[string]bool{}
		}
		out[f][b] = true
	}
	return out, rs.Err()
}

// UnpairedFile is an ebook file with no book, with its current edition when it has one.
type UnpairedFile struct {
	File
	Edition *Edition
}

// Unpaired lists files with no book, oldest first, and how many there are in all.
func (s *Store) Unpaired(ctx context.Context, limit int) (rows []UnpairedFile, total int, err error) {
	if err = s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM ebook_files WHERE book_id IS NULL`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files WHERE book_id IS NULL ORDER BY created_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	var files []File
	for rs.Next() {
		f, err := scanFile(rs)
		if err != nil {
			rs.Close()
			return nil, 0, err
		}
		files = append(files, f)
	}
	rs.Close()
	if err = rs.Err(); err != nil {
		return nil, 0, err
	}
	for _, f := range files {
		u := UnpairedFile{File: f}
		if ed, ok, err := s.CurrentEdition(ctx, f.ID); err != nil {
			return nil, 0, err
		} else if ok {
			u.Edition = &ed
		}
		rows = append(rows, u)
	}
	return rows, total, nil
}

// BookDefault returns the file marked as the book's edition.
func (s *Store) BookDefault(ctx context.Context, bookID string) (File, bool, error) {
	f, err := scanFile(s.db.Reader().QueryRowContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE book_id = ? AND book_default = 1 AND `+readableState, bookID))
	if err == sql.ErrNoRows {
		return File{}, false, nil
	}
	return f, err == nil, err
}

// Choice returns the file a person chose for a book, or "".
func (s *Store) Choice(ctx context.Context, userID, bookID string) (string, error) {
	var id string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT ebook_file_id FROM text_choices WHERE user_id = ? AND book_id = ?`, userID, bookID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// EditionFor picks the text a person reads for a book: their choice if that file is still on the book and ready,
// else the book's default, else the oldest ready file. ok is false when the book has no ready file.
func (s *Store) EditionFor(ctx context.Context, userID, bookID string) (File, Edition, bool, error) {
	var f File
	found := false
	if userID != "" {
		id, err := s.Choice(ctx, userID, bookID)
		if err != nil {
			return File{}, Edition{}, false, err
		}
		if id != "" {
			c, ok, err := s.File(ctx, id)
			if err != nil {
				return File{}, Edition{}, false, err
			}
			if ok && c.BookID == bookID && c.State == "pending" {
				// Waiting for a new reading: still readable while an older edition exists.
				if _, has, err := s.NewestEdition(ctx, c.ID); err != nil {
					return File{}, Edition{}, false, err
				} else if !has {
					ok = false
				}
			}
			if ok && c.BookID == bookID && (c.State == "ready" || c.State == "pending") {
				f, found = c, true
			}
		}
	}
	if !found {
		d, ok, err := s.BookDefault(ctx, bookID)
		if err != nil {
			return File{}, Edition{}, false, err
		}
		f, found = d, ok
	}
	if !found {
		c, err := scanFile(s.db.Reader().QueryRowContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE book_id = ? AND `+readableState+` ORDER BY created_at, id LIMIT 1`, bookID))
		if err == sql.ErrNoRows {
			return File{}, Edition{}, false, nil
		}
		if err != nil {
			return File{}, Edition{}, false, err
		}
		f, found = c, true
	}
	ed, ok, err := s.CurrentEdition(ctx, f.ID)
	if err != nil {
		return File{}, Edition{}, false, err
	}
	if !ok {
		// A file being read again keeps serving its newest edition until the new one lands.
		if ed, ok, err = s.NewestEdition(ctx, f.ID); err != nil {
			return File{}, Edition{}, false, err
		}
		if !ok && f.State != "ready" {
			return File{}, Edition{}, false, nil // a pending file nobody has read yet
		}
	}
	return f, ed, true, nil
}

// readableState selects the files a person can read: ready ones, and ones waiting for a new reading
// that still have an older edition to serve.
const readableState = `(state = 'ready' OR (state = 'pending' AND EXISTS (SELECT 1 FROM editions e WHERE e.ebook_file_id = ebook_files.id)))`

// Candidates rates the books one unpaired file might belong to, among the books f lets the caller see.
func (s *Service) Candidates(ctx context.Context, fileID string, f share.Filter, limit int) ([]Candidate, error) {
	ed, ok, err := s.store.CurrentEdition(ctx, fileID)
	if err != nil || !ok {
		return nil, err
	}
	keys, err := s.store.bookKeysKind(ctx, f, library.BookAudio)
	if err != nil {
		return nil, err
	}
	ns, err := s.store.notSameFor(ctx, []string{fileID})
	if err != nil {
		return nil, err
	}
	return Match(ed.pairMeta(), keys, ns[fileID], limit), nil
}

// SuggestionsForBook lists unpaired, ready files (not kept on their own) that could be this book's text.
func (s *Service) SuggestionsForBook(ctx context.Context, bookID string, limit int) ([]File, error) {
	keys, err := s.store.bookKeys(ctx, share.Filter{SQL: "b.id = ?", Args: []any{bookID}})
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE book_id IS NULL AND state = 'ready' AND (hold IS NULL OR hold <> 'kept') ORDER BY created_at DESC, id DESC LIMIT ?`, suggestScan)
	if err != nil {
		return nil, err
	}
	var files []File
	for rs.Next() {
		f, err := scanFile(rs)
		if err != nil {
			rs.Close()
			return nil, err
		}
		files = append(files, f)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	ns, err := s.store.notSameFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, f := range files {
		if limit > 0 && len(out) >= limit {
			break
		}
		ed, ok, err := s.store.CurrentEdition(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		if ok && len(Match(ed.pairMeta(), keys, ns[f.ID], 1)) > 0 {
			out = append(out, f)
		}
	}
	return out, nil
}
