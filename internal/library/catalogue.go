package library

import (
	"context"
	"strings"
)

// The text catalogue is the list an OPDS feed shows: books with at least one ebook file a reading app
// can take. It is the same Listable and scope rules as browse lists, plus a ready ebook file.

// ebookReadySQL is true for a file of the ebook_files table (aliased e) that may be listed or served:
// ready, not refused, not missing. Pending, failed, refused (copy-protected too) and missing never pass.
const ebookReadySQL = `e.state = 'ready' AND e.refused_reason IS NULL AND e.missing_since IS NULL`

// CatalogueQuery asks for one page of the catalogue. Terms empty means no search. Sort is "title"
// (the default) or "new"; with Terms the order is best match first.
type CatalogueQuery struct {
	Terms         []string
	Sort          string
	Offset, Limit int
}

// CatalogueFile is one listable ebook file of a book.
type CatalogueFile struct {
	ID, Ext, OriginalName string
	Size                  int64
}

// CatalogueBook is one entry of an OPDS feed.
type CatalogueBook struct {
	ID, Title, Subtitle, Description string
	Authors                          []string
	CoverID                          string
	UpdatedAt, AddedAt               int64 // Unix milliseconds UTC
	Files                            []CatalogueFile
}

// TextCatalogue returns one page of the books the scope allows that have a ready ebook file, and the
// total number of such books. The scope's Books filter is applied in SQL; a zero Scope shows nothing.
func (r *Repo) TextCatalogue(ctx context.Context, sc Scope, q CatalogueQuery) ([]CatalogueBook, int, error) {
	cond, sargs := scopeSQL(sc)
	where := Listable("b") + ` AND ` + cond + ` AND EXISTS (SELECT 1 FROM ebook_files e WHERE e.book_id = b.id AND ` + ebookReadySQL + `)`
	from := ` FROM books b JOIN libraries l ON l.id = b.library_id`
	var args []any
	order := `b.sort_title COLLATE NOCASE, b.id`
	if q.Sort == "new" {
		order = `b.added_at DESC, b.id`
	}
	if len(q.Terms) > 0 {
		from = ` FROM books_fts JOIN books_fts_ids i ON i.rid = books_fts.rowid JOIN books b ON b.id = i.book_id JOIN libraries l ON l.id = b.library_id`
		where = `books_fts MATCH ? AND ` + where
		args = append(args, matchExpr(q.Terms))
		order = bm25Order + `, b.id`
	}
	args = append(args, sargs...)
	rd := r.st.Reader()

	var total int
	if err := rd.QueryRowContext(ctx, `SELECT count(*)`+from+` WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := clampLimit(q.Limit, 50, 200)
	rows, err := rd.QueryContext(ctx, `SELECT b.id, b.title, b.subtitle, b.description, COALESCE(b.cover_id, ''), b.updated_at, b.added_at`+
		from+` WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append([]any{}, args...), limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []CatalogueBook
	idx := map[string]int{}
	var ids []any
	for rows.Next() {
		var b CatalogueBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Subtitle, &b.Description, &b.CoverID, &b.UpdatedAt, &b.AddedAt); err != nil {
			return nil, 0, err
		}
		idx[b.ID] = len(out)
		ids = append(ids, b.ID)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return out, total, nil
	}
	in := `(` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`

	frows, err := rd.QueryContext(ctx, `SELECT e.book_id, e.id, e.ext, e.original_name, e.size FROM ebook_files e
		WHERE e.book_id IN `+in+` AND `+ebookReadySQL+` ORDER BY e.book_default DESC, e.created_at, e.id`, ids...)
	if err != nil {
		return nil, 0, err
	}
	defer frows.Close()
	for frows.Next() {
		var bid string
		var f CatalogueFile
		if err := frows.Scan(&bid, &f.ID, &f.Ext, &f.OriginalName, &f.Size); err != nil {
			return nil, 0, err
		}
		out[idx[bid]].Files = append(out[idx[bid]].Files, f)
	}
	if err := frows.Err(); err != nil {
		return nil, 0, err
	}

	arows, err := rd.QueryContext(ctx, `SELECT bp.book_id, p.name FROM book_people bp JOIN people p ON p.id = bp.person_id
		WHERE bp.role = 'author' AND bp.book_id IN `+in+` ORDER BY bp.book_id, bp.idx, p.name`, ids...)
	if err != nil {
		return nil, 0, err
	}
	defer arows.Close()
	for arows.Next() {
		var bid, name string
		if err := arows.Scan(&bid, &name); err != nil {
			return nil, 0, err
		}
		out[idx[bid]].Authors = append(out[idx[bid]].Authors, name)
	}
	return out, total, arows.Err()
}
