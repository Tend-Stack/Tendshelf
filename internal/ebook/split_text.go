package ebook

import (
	"context"
	"database/sql"
)

// SplitText takes every ebook file off an audiobook into text books of their own, in one transaction, so the
// audiobook can be removed while the text survives ("Remove the audiobook only"). Files merged in together stay
// together (splitBook); each other file becomes its own text book. Reading places, highlights and bookmarks made in
// those files go with them. Only rows change. It returns the new books' ids; a book with no ebook files returns none.
func (s *Service) SplitText(ctx context.Context, bookID, userID string) ([]string, error) {
	var made []string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		made = nil
		rs, err := tx.QueryContext(ctx, `SELECT id, COALESCE(merged_from,'') FROM ebook_files WHERE book_id = ? ORDER BY created_at, id`, bookID)
		if err != nil {
			return err
		}
		var groups [][]string
		byMerge := map[string]int{}
		for rs.Next() {
			var id, from string
			if err := rs.Scan(&id, &from); err != nil {
				rs.Close()
				return err
			}
			if gi, ok := byMerge[from]; ok && from != "" {
				groups[gi] = append(groups[gi], id)
				continue
			}
			if from != "" {
				byMerge[from] = len(groups)
			}
			groups = append(groups, []string{id})
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		now := s.now().UnixMilli()
		for _, ids := range groups {
			id, err := s.splitBook(ctx, tx, ids, bookID, userID, now)
			if err != nil {
				return err
			}
			made = append(made, id)
		}
		if len(groups) == 0 {
			return nil
		}
		return s.audit(ctx, tx, userID, "ebook.split_all", groups[0][0], map[string]any{"book": bookID, "new_books": len(made)})
	})
	if err != nil {
		return nil, err
	}
	for _, id := range made {
		s.notifyCreated(ctx, id)
	}
	return made, nil
}
