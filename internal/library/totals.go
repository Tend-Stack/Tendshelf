package library

import "context"

// Totals is the line under the Library title: the listable books the scope may see, their length,
// and the folders of the libraries it may see.
type Totals struct {
	Books      int
	DurationMs int64
	Folders    int
}

// Totals counts the visible listable books, their summed duration and the folders of the visible
// libraries. libraryID "" covers every visible library. Both queries carry the scope in SQL, so a
// scope that sees nothing gets zeros.
func (r *Repo) Totals(ctx context.Context, sc Scope, libraryID string) (Totals, error) {
	var t Totals
	w := where(sc, ListQuery{LibraryID: libraryID})
	err := r.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(b.duration_ms), 0)`+fromVisible+` WHERE `+w.sql(), w.args...).
		Scan(&t.Books, &t.DurationMs)
	if err != nil {
		return Totals{}, err
	}
	q := `SELECT COUNT(*) FROM library_folders f JOIN libraries l ON l.id = f.library_id WHERE (` + sc.Libraries.SQL + `)`
	args := append([]any(nil), sc.Libraries.Args...)
	if libraryID != "" {
		q += ` AND f.library_id = ?`
		args = append(args, libraryID)
	}
	err = r.st.Reader().QueryRowContext(ctx, q, args...).Scan(&t.Folders)
	return t, err
}
