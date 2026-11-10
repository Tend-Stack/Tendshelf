package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Merging two copies of one audiobook (S4). The owner picks the copy to keep and the copy to drop; everything
// anyone said about the dropped copy moves onto the kept one, the dropped row is deleted, and its folder is
// remembered so a scan never brings it back while the merge stands. Rows only: no library file is opened,
// written, renamed or deleted, and the dropped copy's folder stays on disk.

var (
	// ErrMergeLibrary means the two books are in different libraries; a merge never crosses libraries.
	ErrMergeLibrary = errors.New("library: books are in different libraries")
	// ErrMergeBusy means a file of one of the books is being written to ("write details into the file").
	ErrMergeBusy = errors.New("library: a book is being written to")
	// ErrMergeSame means the same book was named twice.
	ErrMergeSame = errors.New("library: a book cannot be merged into itself")
	// ErrBookNotFound means a book is unknown, is not an audiobook, or the kept one cannot be listed.
	ErrBookNotFound = fmt.Errorf("%w: book", ErrNotFound)
)

// mergeBookRows are the statements that are only for an audiobook copy, run before MergeRowsSQL. Each has a
// reason: progress keeps the newest place for a person (capped at the kept book's length); the rows with a
// unique key per person or target move only where the kept book has none (OR IGNORE), and what is left behind
// goes with the dropped row's cascade.
var mergeBookRows = []string{
	`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
  SELECT user_id, :to, CASE WHEN :dur > 0 THEN MIN(position_ms, :dur) ELSE position_ms END, CASE WHEN :dur > 0 THEN :dur ELSE duration_ms END,
    finished_at, client_updated_at, :now, device_id, device_label FROM progress WHERE book_id = :from AND true
  ON CONFLICT(user_id, book_id) DO UPDATE SET position_ms = excluded.position_ms, duration_ms = excluded.duration_ms,
    finished_at = excluded.finished_at, client_updated_at = excluded.client_updated_at, server_updated_at = :now,
    device_id = excluded.device_id, device_label = excluded.device_label
  WHERE excluded.client_updated_at > progress.client_updated_at`,
	`UPDATE listening_sessions SET book_id = :to WHERE book_id = :from`,
	`UPDATE OR IGNORE queue_items SET book_id = :to, item_id = :to WHERE book_id = :from`,
	`UPDATE OR IGNORE book_settings SET book_id = :to WHERE book_id = :from`,
	`UPDATE OR IGNORE grants SET book_id = :to WHERE book_id = :from`,
	`UPDATE share_links SET book_id = :to WHERE book_id = :from`,
	// An uploaded ebook the kept book already has (same bytes) would break ebook_files_upload_once: drop the dropped
	// copy's row and keep the kept book's. Upload rows have no library path and the bytes stay, shared by the kept row.
	`DELETE FROM ebook_files WHERE book_id = :from AND origin = 'upload' AND sha256 IN
    (SELECT sha256 FROM ebook_files k WHERE k.book_id = :to AND k.origin = 'upload')`,
	// Ebook files attached to the dropped copy become editions of the kept one; the one-default-per-book rule holds.
	`UPDATE ebook_files SET book_id = :to, merged_from = COALESCE(merged_from, :from), updated_at = :now,
    book_default = CASE WHEN book_default = 1 AND NOT EXISTS (SELECT 1 FROM ebook_files k WHERE k.book_id = :to AND k.book_default = 1)
      THEN 1 ELSE 0 END WHERE book_id = :from`,
	`UPDATE OR IGNORE ebook_not_same SET book_id = :to WHERE book_id = :from`,
}

// MergeBooks merges the audiobook drop into the audiobook keep, for the person userID (empty for the engine).
// It is one transaction. It refuses when the books are one book, in different libraries, not audiobooks, when
// keep cannot be listed, or when either has an open file write.
func (r *Repo) MergeBooks(ctx context.Context, keep, drop, userID string) error {
	if keep == drop {
		return ErrMergeSame
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		type row struct {
			lib, kind, root string
			dur             int64
			listable        bool
			cover           sql.NullString
		}
		load := func(id string) (x row, err error) {
			var refused sql.NullString
			var missing sql.NullInt64
			err = tx.QueryRowContext(ctx, `SELECT library_id, kind, root_path, duration_ms, refused_reason, missing_since, cover_id FROM books WHERE id = ?`, id).
				Scan(&x.lib, &x.kind, &x.root, &x.dur, &refused, &missing, &x.cover)
			if errors.Is(err, sql.ErrNoRows) {
				return x, ErrBookNotFound
			}
			x.listable = !refused.Valid && !missing.Valid
			if err == nil && x.kind != BookAudio {
				err = ErrBookNotFound
			}
			return x, err
		}
		k, err := load(keep)
		if err != nil {
			return err
		}
		d, err := load(drop)
		if err != nil {
			return err
		}
		if !k.listable {
			return ErrBookNotFound
		}
		if k.lib != d.lib {
			return ErrMergeLibrary
		}
		var busy int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM file_writes WHERE book_id IN (?, ?) AND state NOT IN ('confirmed','undone','failed') LIMIT 1`, keep, drop).Scan(&busy)
		if err == nil {
			return ErrMergeBusy
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		now := r.nowMs()
		count := func(q string) (n int) {
			_ = tx.QueryRowContext(ctx, q, sql.Named("from", drop)).Scan(&n)
			return n
		}
		detail := map[string]any{"from": drop, "to": keep, "root": d.root,
			"progress":  count(`SELECT count(*) FROM progress WHERE book_id = :from`),
			"bookmarks": count(`SELECT count(*) FROM bookmarks WHERE book_id = :from AND deleted_at IS NULL`),
			"shelves":   count(`SELECT count(*) FROM shelf_items WHERE book_id = :from`),
			"grants":    count(`SELECT count(*) FROM grants WHERE book_id = :from`),
			"links":     count(`SELECT count(*) FROM share_links WHERE book_id = :from`)}

		var by any
		if userID != "" {
			by = userID
		}
		all := []any{sql.Named("from", drop), sql.Named("to", keep), sql.Named("now", now), sql.Named("dur", k.dur)}
		stmts := append(append([]string{}, mergeBookRows...), MergeRowsSQL...)
		stmts = append(stmts, `UPDATE merged_folders SET to_id = :to WHERE to_id = :from`)
		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q, BindFor(q, all)...); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO merged_folders(root_path, library_id, from_id, to_id, merged_by, merged_at) VALUES (?,?,?,?,?,?)`,
			d.root, d.lib, drop, keep, by, now); err != nil {
			return err
		}
		// The kept copy takes the dropped copy's cover when it has none of its own.
		if !k.cover.Valid && d.cover.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE covers SET book_id = ? WHERE id = ?`, keep, d.cover.String); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE books SET cover_id = ?, updated_at = ? WHERE id = ?`, d.cover.String, now, keep); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE books SET cover_id = NULL WHERE id = ?`, drop); err != nil {
				return err
			}
		}
		// Cascades: files, chapters, details, people, series, labels, leftover rows the OR IGNORE moves skipped.
		if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE id = ? AND kind = 'audio'`, drop); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: userID, Action: "book.merge", Target: "book:" + keep, Detail: detail})
	})
}

// RestoreMergedFolder forgets a merged-away folder, so the next scan adds it again as a new book. What moved to
// the kept book when the copies were merged stays there. It returns the folder's library, or "" when the folder
// was not remembered.
func (r *Repo) RestoreMergedFolder(ctx context.Context, rootPath, userID string) (libraryID string, err error) {
	err = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		qerr := tx.QueryRowContext(ctx, `DELETE FROM merged_folders WHERE root_path = ? RETURNING library_id`, rootPath).Scan(&libraryID)
		if errors.Is(qerr, sql.ErrNoRows) {
			return nil
		}
		if qerr != nil {
			return qerr
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: r.nowMs(), UserID: userID, Action: "book.merge_undo", Target: "library:" + libraryID, Detail: map[string]any{"root": rootPath}})
	})
	return libraryID, err
}

// MergedFolders lists the folders merged away in a library, as root path to the kept book's id. A scan reads
// it to keep those folders out.
func (r *Repo) MergedFolders(ctx context.Context, libraryID string) (map[string]string, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT root_path, to_id FROM merged_folders WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, to string
		if err := rows.Scan(&p, &to); err != nil {
			return nil, err
		}
		out[p] = to
	}
	return out, rows.Err()
}

// MergedFolders lists the merged-away folders of a library inside the transaction, as root path to the kept
// book's id. The scan applies its diff with it, so a merge made after the walk still keeps its folder out.
func (t *Tx) MergedFolders(libraryID string) (map[string]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT root_path, to_id FROM merged_folders WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, to string
		if err := rows.Scan(&p, &to); err != nil {
			return nil, err
		}
		out[p] = to
	}
	return out, rows.Err()
}

// NotDuplicates records "Not the same" for a pair of books, so the Duplicates page stops offering it.
func (r *Repo) NotDuplicates(ctx context.Context, a, b, userID string) error {
	if a == b {
		return ErrMergeSame
	}
	if a > b {
		a, b = b, a
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO book_not_duplicates(a_id, b_id, created_at) VALUES (?,?,?)`, a, b, r.nowMs())
		if db.IsForeignKeyViolation(err) {
			return ErrBookNotFound
		}
		return err
	})
}
