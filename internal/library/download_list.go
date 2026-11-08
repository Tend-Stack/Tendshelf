package library

import (
	"context"
)

// DownloadItem is one book a "Download all" would save: its id, title and the bytes of its downloadable audio files.
type DownloadItem struct {
	ID    string
	Title string
	Bytes int64
}

// downloadable is the SQL test, on an audio_files row aliased af, for a file a download can keep: present, probed
// without error, not encrypted and not a copy-protected format (the rule of the stream route, in SQL).
const downloadable = `af.missing_since IS NULL AND af.encrypted = 0 AND af.probe_error IS NULL AND af.probe_json IS NOT NULL
  AND af.path NOT LIKE '%.aax' AND af.path NOT LIKE '%.aaxc' AND af.path NOT LIKE '%.aa' AND af.path NOT LIKE '%.m4p'`

// downloadItems is the shared query: listable books with at least one downloadable file. from carries the
// FROM and JOIN clauses ending in a WHERE that already holds the scope; order is the ORDER BY with its LIMIT.
func (r *Repo) downloadItems(ctx context.Context, from, order string, args []any) ([]DownloadItem, error) {
	return many(ctx, r.st.Reader(), func(s scanner) (DownloadItem, error) {
		var d DownloadItem
		return d, s.Scan(&d.ID, &d.Title, &d.Bytes)
	}, `SELECT b.id, b.title, (SELECT COALESCE(SUM(af.size), 0) FROM audio_files af WHERE af.book_id = b.id AND `+downloadable+`)`+
		from+` AND EXISTS (SELECT 1 FROM audio_files af WHERE af.book_id = b.id AND `+downloadable+`) `+order, args...)
}

// SeriesDownloadItems lists the books of a series that sc allows and that have audio, in the series page's order,
// at most 500. The caller builds sc with the download right, so the filter runs in SQL.
func (r *Repo) SeriesDownloadItems(ctx context.Context, sc Scope, seriesID string) ([]DownloadItem, error) {
	w := visible(sc, true)
	w.add("bs.series_id = ?", seriesID)
	return r.downloadItems(ctx, ` FROM books b JOIN libraries l ON l.id = b.library_id JOIN book_series bs ON bs.book_id = b.id WHERE `+w.sql(),
		`ORDER BY CASE WHEN bs.sequence GLOB '[0-9]*' THEN CAST(bs.sequence AS REAL) END ASC NULLS LAST, `+titleKey+` COLLATE NOCASE, b.id LIMIT ?`,
		append(append([]any(nil), w.args...), maxSeriesList))
}

// ShelfDownloadItems lists every book on a shelf of ownerID that sc allows and that has audio, in shelf order, in one
// query (a shelf holds at most 5,000). A shelf that is not ownerID's is ErrNotFound.
func (r *Repo) ShelfDownloadItems(ctx context.Context, sc Scope, ownerID, id string) ([]DownloadItem, error) {
	if _, err := r.Shelf(ctx, ownerID, id); err != nil {
		return nil, err
	}
	w := visible(sc, true)
	args := append([]any{ownerID, id}, w.args...)
	return r.downloadItems(ctx, ` FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id AND s.owner_id = ?
  JOIN books b ON b.id = si.book_id JOIN libraries l ON l.id = b.library_id WHERE si.shelf_id = ? AND `+w.sql(),
		`ORDER BY si.idx, si.book_id LIMIT ?`, append(args, maxShelfBooks))
}
