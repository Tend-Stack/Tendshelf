package library

import (
	"context"
	"database/sql"
	"path/filepath"
)

// This file holds the queries and writes behind the Libraries admin page and the setup steps.
// Every read takes a Scope: books are joined as b and libraries as l, and both filters apply.

// FolderSummary is a folder with its library name and what it holds.
type FolderSummary struct {
	Folder
	LibraryName string
	Books       int   // listable books
	Bytes       int64 // bytes of present files of listable books
}

// scopeWhere is the shared condition for a query over books b and libraries l.
func scopeWhere(sc Scope) (string, []any) {
	args := append(append([]any{}, sc.Books.Args...), sc.Libraries.Args...)
	return "(" + sc.Books.SQL + ") AND (" + sc.Libraries.SQL + ")", args
}

// FolderSummaries lists the folders of the libraries the scope may see, by library name
// (case-insensitive) then path.
func (r *Repo) FolderSummaries(ctx context.Context, sc Scope) ([]FolderSummary, error) {
	q := `SELECT f.id, f.library_id, f.path, f.created_at, l.name,
  (SELECT count(*) FROM books b WHERE b.folder_id = f.id AND ` + Listable("b") + ` AND ` + sc.Books.SQL + `),
  (SELECT COALESCE(SUM(a.size), 0) FROM audio_files a JOIN books b ON b.id = a.book_id
    WHERE b.folder_id = f.id AND a.missing_since IS NULL AND ` + Listable("b") + ` AND ` + sc.Books.SQL + `)
FROM library_folders f JOIN libraries l ON l.id = f.library_id
WHERE ` + sc.Libraries.SQL + ` ORDER BY l.name COLLATE NOCASE, f.path`
	args := append(append(append([]any{}, sc.Books.Args...), sc.Books.Args...), sc.Libraries.Args...)
	return many(ctx, r.st.Reader(), func(s scanner) (FolderSummary, error) {
		var fs FolderSummary
		err := s.Scan(&fs.ID, &fs.LibraryID, &fs.Path, &fs.CreatedAt, &fs.LibraryName, &fs.Books, &fs.Bytes)
		return fs, err
	}, q, args...)
}

// ProblemBook is one book listed under a problem group.
type ProblemBook struct{ ID, Title, FolderID string }

// Problems are the books that need a look, grouped. Lists are capped; counts are exact.
type Problems struct {
	Refused      map[string][]ProblemBook // by refused reason
	RefusedCount map[string]int
	Missing      []ProblemBook
	MissingCount int
	NoCover      []ProblemBook
	NoCoverCount int
}

// noUnsettled is true for a book none of whose present files still wait for a probe.
const noUnsettled = `NOT EXISTS (SELECT 1 FROM audio_files a WHERE a.book_id = b.id AND a.missing_since IS NULL
  AND a.probe_json IS NULL AND a.probe_error IS NULL)`

func scanProblemBook(s scanner) (ProblemBook, error) {
	var (
		p           ProblemBook
		title, root string
	)
	err := s.Scan(&p.ID, &title, &root, &p.FolderID)
	if title == "" {
		title = filepath.Base(root)
	}
	p.Title = title
	return p, err
}

// Problems returns the refused, missing and cover-less books the scope may see. Each list holds at
// most perGroup books (0 means 20), ordered by title; the counts are exact. A missing book counts
// only as missing, even when it was also refused.
func (r *Repo) Problems(ctx context.Context, sc Scope, perGroup int) (Problems, error) {
	if perGroup <= 0 {
		perGroup = 20
	}
	where, wargs := scopeWhere(sc)
	from := ` FROM books b JOIN libraries l ON l.id = b.library_id WHERE ` + where
	cols := `SELECT b.id, b.title, b.root_path, b.folder_id` + from
	order := ` ORDER BY b.sort_title COLLATE NOCASE, b.root_path LIMIT ?`
	p := Problems{Refused: map[string][]ProblemBook{}, RefusedCount: map[string]int{}}
	rd := r.st.Reader()

	rows, err := rd.QueryContext(ctx, `SELECT b.refused_reason, count(*)`+from+
		` AND b.refused_reason IS NOT NULL AND b.missing_since IS NULL GROUP BY b.refused_reason`, wargs...)
	if err != nil {
		return Problems{}, err
	}
	defer rows.Close()
	var reasons []string
	for rows.Next() {
		var reason string
		var n int
		if err := rows.Scan(&reason, &n); err != nil {
			return Problems{}, err
		}
		p.RefusedCount[reason] = n
		reasons = append(reasons, reason)
	}
	if err := rows.Err(); err != nil {
		return Problems{}, err
	}
	rows.Close()
	for _, reason := range reasons {
		list, err := many(ctx, rd, scanProblemBook,
			cols+` AND b.refused_reason = ? AND b.missing_since IS NULL`+order, append(append([]any{}, wargs...), reason, perGroup)...)
		if err != nil {
			return Problems{}, err
		}
		p.Refused[reason] = list
	}

	if err := rd.QueryRowContext(ctx, `SELECT count(*)`+from+` AND b.missing_since IS NOT NULL`, wargs...).Scan(&p.MissingCount); err != nil {
		return Problems{}, err
	}
	if p.MissingCount > 0 {
		if p.Missing, err = many(ctx, rd, scanProblemBook, cols+` AND b.missing_since IS NOT NULL`+order,
			append(append([]any{}, wargs...), perGroup)...); err != nil {
			return Problems{}, err
		}
	}

	noCover := ` AND ` + Listable("b") + ` AND b.cover_id IS NULL AND ` + noUnsettled
	if err := rd.QueryRowContext(ctx, `SELECT count(*)`+from+noCover, wargs...).Scan(&p.NoCoverCount); err != nil {
		return Problems{}, err
	}
	if p.NoCoverCount > 0 {
		if p.NoCover, err = many(ctx, rd, scanProblemBook, cols+noCover+order,
			append(append([]any{}, wargs...), perGroup)...); err != nil {
			return Problems{}, err
		}
	}
	return p, nil
}

// Overview is the totals shown when setup finishes.
type Overview struct {
	Books                       int
	DurationMs                  int64
	Series, Narrators, Problems int // Problems = refused plus missing books
}

// Overview counts what the scope may see: listable books, their total duration, the series and
// narrators they belong to, and the books that need a look.
func (r *Repo) Overview(ctx context.Context, sc Scope) (Overview, error) {
	where, wargs := scopeWhere(sc)
	from := ` FROM books b JOIN libraries l ON l.id = b.library_id WHERE ` + where
	rd := r.st.Reader()
	var o Overview
	if err := rd.QueryRowContext(ctx, `SELECT count(*), COALESCE(SUM(b.duration_ms), 0)`+from+` AND `+Listable("b"), wargs...).
		Scan(&o.Books, &o.DurationMs); err != nil {
		return Overview{}, err
	}
	if err := rd.QueryRowContext(ctx, `SELECT count(DISTINCT bs.series_id) FROM book_series bs
  JOIN books b ON b.id = bs.book_id JOIN libraries l ON l.id = b.library_id WHERE `+where+` AND `+Listable("b"), wargs...).
		Scan(&o.Series); err != nil {
		return Overview{}, err
	}
	if err := rd.QueryRowContext(ctx, `SELECT count(DISTINCT bp.person_id) FROM book_people bp
  JOIN books b ON b.id = bp.book_id JOIN libraries l ON l.id = b.library_id WHERE `+where+` AND bp.role = 'narrator' AND `+Listable("b"), wargs...).
		Scan(&o.Narrators); err != nil {
		return Overview{}, err
	}
	if err := rd.QueryRowContext(ctx, `SELECT count(*)`+from+` AND (b.missing_since IS NOT NULL OR b.refused_reason IS NOT NULL)`, wargs...).
		Scan(&o.Problems); err != nil {
		return Overview{}, err
	}
	return o, nil
}

// ScanProgress is how far a library's streaming-in has come.
type ScanProgress struct {
	Books        int // listable books
	Files        int // present files of present books
	FilesWaiting int // present files with neither probe data nor a probe error
}

// ScanProgress counts one library's books and files for the scan status line.
func (r *Repo) ScanProgress(ctx context.Context, sc Scope, libraryID string) (ScanProgress, error) {
	where, wargs := scopeWhere(sc)
	args := append(append([]any{}, wargs...), libraryID)
	var p ScanProgress
	err := r.st.Reader().QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM books b JOIN libraries l ON l.id = b.library_id WHERE `+where+` AND b.library_id = ? AND `+Listable("b")+`)`,
		args...).Scan(&p.Books)
	if err != nil {
		return ScanProgress{}, err
	}
	err = r.st.Reader().QueryRowContext(ctx, `SELECT count(*),
  COALESCE(SUM(a.probe_json IS NULL AND a.probe_error IS NULL), 0)
FROM audio_files a JOIN books b ON b.id = a.book_id JOIN libraries l ON l.id = b.library_id
WHERE `+where+` AND b.library_id = ? AND b.missing_since IS NULL AND a.missing_since IS NULL`, args...).Scan(&p.Files, &p.FilesWaiting)
	return p, err
}

// CreateLibraryWithFolder creates a library and its first folder in one transaction, with the rules
// of CreateLibrary and AddFolder. When the folder is refused, no library is left behind.
func (r *Repo) CreateLibraryWithFolder(ctx context.Context, name, path string) (Library, Folder, error) {
	l, err := r.newLibrary(name, 0)
	if err != nil {
		return Library{}, Folder{}, err
	}
	f, err := r.newFolder(l.ID, path)
	if err != nil {
		return Library{}, Folder{}, err
	}
	err = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := insertLibrary(ctx, tx, l); err != nil {
			return err
		}
		return r.insertFolder(ctx, tx, f)
	})
	if err != nil {
		return Library{}, Folder{}, err
	}
	return l, f, nil
}

// PresentBooksInFolder counts the folder's books that are not marked missing.
func (r *Repo) PresentBooksInFolder(ctx context.Context, folderID string) (int, error) {
	var n int
	err := r.st.Reader().QueryRowContext(ctx,
		`SELECT count(*) FROM books WHERE folder_id = ? AND missing_since IS NULL`, folderID).Scan(&n)
	return n, err
}

// MarkFolderBooksMissing marks every present book of the folder, and its present files, missing
// since the given time. It returns the number of books marked. Nothing is deleted and no file on
// disk is touched; a later scan that finds the files again restores the books.
func (t *Tx) MarkFolderBooksMissing(folderID string, since int64) (int, error) {
	if _, err := t.exec(`UPDATE audio_files SET missing_since = ? WHERE missing_since IS NULL
  AND book_id IN (SELECT id FROM books WHERE folder_id = ?)`, since, folderID); err != nil {
		return 0, err
	}
	res, err := t.exec(`UPDATE books SET missing_since = ? WHERE folder_id = ? AND missing_since IS NULL`, since, folderID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
