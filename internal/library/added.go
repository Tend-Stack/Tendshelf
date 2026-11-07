package library

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Who added a book (decision 0029). upload_books has one row per published upload, matched to books by path,
// so a book is a person's when its root is, or lies inside, a root they published.

// addedByBook is true for the book aliased b when ?1 published it. share.AddedBySQL takes a positional "?".
func addedByBook() string { return share.AddedBySQL("b") }

// BooksAddedBy returns every book the person's uploads published, by root path, including books marked missing.
func (r *Repo) BooksAddedBy(ctx context.Context, userID string) ([]Book, error) {
	return many(ctx, r.st.Reader(), scanBook,
		`SELECT `+bookCols+` FROM books b WHERE `+addedByBook()+` ORDER BY root_path`, userID)
}

// CountAddedBy counts the books the person's uploads published.
func (r *Repo) CountAddedBy(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM books b WHERE `+addedByBook(), userID).Scan(&n)
	return n, err
}

// AddedBy returns the person whose upload published the book, or "" when none is on record (the newest
// row wins when the path was published more than once).
func (r *Repo) AddedBy(ctx context.Context, bookID string) (string, error) {
	var id string
	err := r.st.Reader().QueryRowContext(ctx, `SELECT ub.added_by FROM books b JOIN upload_books ub
  ON b.root_path = ub.root_path OR (b.root_path > ub.root_path || '/' AND b.root_path < ub.root_path || '0')
  WHERE b.id = ? ORDER BY length(ub.root_path) DESC, ub.created_at DESC LIMIT 1`, bookID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ContributorFolderOf returns the contributor folder (contributor_folders.path) of the library folder that
// holds bookRoot, or "" when the book does not lie inside one. metadata.FromPath starts there, so the
// "Added by <name>" folder never becomes an author.
func (r *Repo) ContributorFolderOf(ctx context.Context, folderID, bookRoot string) (string, error) {
	var p string
	err := r.st.Reader().QueryRowContext(ctx, `SELECT path FROM contributor_folders
  WHERE folder_id = ?1 AND ?2 > path || '/' AND ?2 < path || '0' ORDER BY length(path) DESC LIMIT 1`, folderID, bookRoot).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return p, err
}

// ContributorFolders lists the folders created for the person, one per upload folder.
func (r *Repo) ContributorFolders(ctx context.Context, userID string) ([]string, error) {
	return many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var p string
		return p, s.Scan(&p)
	}, `SELECT path FROM contributor_folders WHERE user_id = ? ORDER BY path`, userID)
}

// AddedByFacet lists the people with at least one visible book they added, most books first, under every
// other filter of q (it ignores q.AddedBy). limit 0 is 30. The scope decides which books count.
func (r *Repo) AddedByFacet(ctx context.Context, sc Scope, q ListQuery, limit int) ([]NamedFacet, error) {
	if limit <= 0 {
		limit = defaultFacetLimit
	}
	if err := checkShow(q); err != nil {
		return nil, err
	}
	w := whereExcept(sc, q, "added")
	return many(ctx, r.st.Reader(), scanFacet, `SELECT u.id, coalesce(nullif(u.display_name, ''), u.username), COUNT(DISTINCT b.id)
  FROM books b JOIN libraries l ON l.id = b.library_id
  JOIN upload_books ub ON b.root_path = ub.root_path OR (b.root_path > ub.root_path || '/' AND b.root_path < ub.root_path || '0')
  JOIN users u ON u.id = ub.added_by WHERE `+w.sql()+`
  GROUP BY u.id ORDER BY COUNT(DISTINCT b.id) DESC, 2 COLLATE NOCASE, u.id LIMIT ?`, append(w.args, limit)...)
}
