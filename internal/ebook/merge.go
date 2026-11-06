package ebook

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Merging a text book into an audiobook (decision 0018). The ebook's files become editions of the audiobook,
// and the person's reading data moves with them. Rows only: no library file is opened, written or renamed.

// mergeGuard holds when merging the text book :from into the audiobook :to cannot widen who reaches anything.
// A book grant would start to cover the audio; a shelf item moved onto the audiobook would extend that shelf's
// grant; across libraries a person may see one side only, so data is moved only where it stays within one library.
// A person's own merge skips the guard: they see both books and the page says what goes with the text book.
const mergeGuard = `
NOT EXISTS (SELECT 1 FROM grants WHERE book_id = :from)
AND NOT EXISTS (SELECT 1 FROM share_links WHERE book_id = :from)
AND NOT EXISTS (SELECT 1 FROM grants g JOIN shelf_items s ON s.shelf_id = g.shelf_id WHERE s.book_id = :from)
AND NOT EXISTS (SELECT 1 FROM shelf_items i JOIN shelves v ON v.id = i.shelf_id WHERE i.book_id = :from AND v.shared = 1)
AND ( (SELECT library_id FROM books WHERE id = :from) = (SELECT library_id FROM books WHERE id = :to)
      OR NOT (EXISTS (SELECT 1 FROM reading_positions WHERE book_id = :from)
           OR EXISTS (SELECT 1 FROM highlights WHERE book_id = :from)
           OR EXISTS (SELECT 1 FROM bookmarks WHERE book_id = :from)) )`

// mergeOpts says how a merge was decided.
type mergeOpts struct {
	userID string // the person; empty for the engine
	reason string // paired_by for an automatic merge ('isbn', 'title', 'folder'); empty for a person
	auto   bool   // check the guard and that no person has said anything about these files
}

// MergeBook is a person's merge: the text book `from` becomes editions of the audiobook `to`. It skips the
// automatic guard. Grants and share links on `from` go with it.
func (s *Service) MergeBook(ctx context.Context, from, to, userID string) error {
	done, err := s.mergeBook(ctx, from, to, mergeOpts{userID: userID})
	if err != nil {
		return err
	}
	if !done {
		return ErrBookNotFound
	}
	return nil
}

// mergeBook moves a text book into an audiobook in one transaction. It reports false, with nothing
// written, when an automatic merge is not allowed.
func (s *Service) mergeBook(ctx context.Context, from, to string, o mergeOpts) (bool, error) {
	merged := false
	needCover := false
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var fromKind, toKind string
		var toCover sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT kind FROM books WHERE id = ?`, from).Scan(&fromKind)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && fromKind != library.BookText) {
			return ErrBookNotFound
		}
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT kind, cover_id FROM books WHERE id = ? AND missing_since IS NULL AND refused_reason IS NULL`, to).Scan(&toKind, &toCover)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && toKind != library.BookAudio) {
			return ErrBookNotFound
		}
		if err != nil {
			return err
		}
		args := []any{sql.Named("from", from), sql.Named("to", to)}
		if o.auto {
			var ok int
			// Nothing a person said: the default file is still unheld and unpaired, and no file was refused this book.
			err := tx.QueryRowContext(ctx, `SELECT 1 WHERE EXISTS (SELECT 1 FROM ebook_files WHERE book_id = :from AND book_default = 1
AND hold IS NULL AND paired_by IS NULL)
AND NOT EXISTS (SELECT 1 FROM ebook_not_same n JOIN ebook_files f ON f.id = n.ebook_file_id WHERE f.book_id = :from AND n.book_id = :to)
AND `+mergeGuard, args...).Scan(&ok)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		count := func(q string) (n int) {
			_ = tx.QueryRowContext(ctx, q, sql.Named("from", from)).Scan(&n)
			return n
		}
		detail := map[string]any{"from": from, "to": to, "by": "person",
			"positions":  count(`SELECT count(*) FROM reading_positions WHERE book_id = :from`),
			"highlights": count(`SELECT count(*) FROM highlights WHERE book_id = :from`),
			"bookmarks":  count(`SELECT count(*) FROM bookmarks WHERE book_id = :from`),
			"shelves":    count(`SELECT count(*) FROM shelf_items WHERE book_id = :from`)}
		if o.auto {
			detail["by"] = "auto"
		}
		var reason any
		if o.reason != "" {
			reason = o.reason
		}
		all := append(args, sql.Named("now", s.now().UnixMilli()), sql.Named("reason", reason))
		stmts := append([]string{
			`UPDATE ebook_files SET book_id=:to, merged_from=:from, paired_by=COALESCE(:reason,'person'), book_default=0, updated_at=:now WHERE book_id=:from`,
		}, library.MergeRowsSQL...)
		stmts = append(stmts,
			// Cascades: choices, covers, details, labels, leftover shelf rows, grants, links.
			`DELETE FROM books WHERE id=:from AND kind='text'`,
		)
		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q, library.BindFor(q, all)...); err != nil {
				return err
			}
		}
		needCover = !toCover.Valid
		merged = true
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: o.userID, Action: "ebook.merge", Target: "book:" + to, Detail: detail})
	})
	if err != nil {
		return false, err
	}
	if merged && needCover {
		s.notifyCreated(ctx, to) // the audiobook has no cover: the cover job may take the ebook's
	}
	return merged, nil
}

// PairBook runs the automatic merge for one book after its details were read: an audiobook's title is only
// right once its tags are in, and a text book's ISBN and authors only once its details are. For an audiobook
// it looks for a text book to take in; for a text book, for an audiobook to join.
func (s *Service) PairBook(ctx context.Context, bookID string) error {
	var kind string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT kind FROM books WHERE id = ?`, bookID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == library.BookText {
		return s.mergePending(ctx, "", bookID)
	}
	return s.mergePending(ctx, bookID, "")
}

// mergePending merges every text book whose default file confidently matches one audiobook. onlyTo limits the
// audiobook and onlyFrom the text book ("" for all). A match the guard refuses stays a suggestion for a person.
func (s *Service) mergePending(ctx context.Context, onlyTo, onlyFrom string) error {
	audio, err := s.store.bookKeysKind(ctx, share.Filter{}, library.BookAudio)
	if err != nil || len(audio) == 0 {
		return err
	}
	if onlyTo != "" {
		found := false
		for _, k := range audio {
			found = found || k.ID == onlyTo
		}
		if !found {
			return nil
		}
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE book_default = 1 AND hold IS NULL AND paired_by IS NULL AND state = 'ready' AND missing_since IS NULL
AND book_id IN (SELECT id FROM books WHERE kind = 'text' AND missing_since IS NULL) ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	var files []File
	for rs.Next() {
		f, err := scanFile(rs)
		if err != nil {
			rs.Close()
			return err
		}
		files = append(files, f)
	}
	rs.Close()
	if err := rs.Err(); err != nil || len(files) == 0 {
		return err
	}
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	ns, err := s.store.notSameFor(ctx, ids)
	if err != nil {
		return err
	}
	folders := map[string]map[string][]string{} // library -> directory -> audiobooks, read once
	for _, f := range files {
		ed, ok, err := s.store.CurrentEdition(ctx, f.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		to, by, ok, err := s.autoTarget(ctx, f, ed, audio, ns[f.ID], folders)
		if err != nil {
			return err
		}
		if !ok || (onlyTo != "" && to != onlyTo) || (onlyFrom != "" && f.BookID != onlyFrom) {
			continue
		}
		if _, err := s.mergeBook(ctx, f.BookID, to, mergeOpts{reason: by, auto: true}); err != nil && !errors.Is(err, ErrBookNotFound) {
			return err
		}
	}
	return nil
}

// autoTarget is the audiobook the engine would merge this text book's default file into, and how it is
// recorded: the deciding match, else the same-folder rule. folders caches the folder reads per library.
func (s *Service) autoTarget(ctx context.Context, f File, ed Edition, audio []BookKey, ns map[string]bool,
	folders map[string]map[string][]string) (to, by string, ok bool, err error) {
	to, by, ok = Decide(Match(ed.pairMeta(), audio, ns, 0))
	if ok || f.Origin != "library" {
		return to, by, ok, nil
	}
	byDir, seen := folders[f.LibraryID]
	if !seen {
		if byDir, err = s.folderBooks(ctx, f.LibraryID); err != nil {
			return "", "", false, err
		}
		folders[f.LibraryID] = byDir
	}
	if id, found := folderBook(byDir, f.Path, f.Ext); found && !ns[id] {
		return id, "folder", true, nil
	}
	return "", "", false, nil
}

// GuardAllows says whether the automatic guard would let the text book `from` merge into `to`. A person's own
// merge skips it; the page uses it to say first that grants on the text book go along.
func (s *Service) GuardAllows(ctx context.Context, from, to string) (bool, error) {
	var one int
	err := s.db.Reader().QueryRowContext(ctx, `SELECT 1 WHERE `+mergeGuard, sql.Named("from", from), sql.Named("to", to)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// splitBook takes the files `ids` (all of one book, `book`) off it into a text book of their own, inside tx.
// The new book has the id the files were merged from when that id is free, so old links work again. Each file
// gets a "not the same book" row and is held, so no automatic merge or suggestion brings it back.
// It returns the new book's id.
func (s *Service) splitBook(ctx context.Context, tx *sql.Tx, ids []string, book, userID string, now int64) (string, error) {
	var files []File
	for _, id := range ids {
		f, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileCols+` FROM ebook_files WHERE id = ?`, id))
		if err != nil {
			return "", err
		}
		files = append(files, f)
	}
	first := files[0]
	for _, f := range files {
		if f.CreatedAt < first.CreatedAt || (f.CreatedAt == first.CreatedAt && f.ID < first.ID) {
			first = f
		}
	}
	root, folder, ok, err := s.textRoot(ctx, tx, first)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrFileNotFound
	}
	title := ""
	if first.SHA256 != "" {
		_ = tx.QueryRowContext(ctx, `SELECT title FROM editions WHERE ebook_file_id = ? AND source_sha256 = ? ORDER BY created_at DESC LIMIT 1`,
			first.ID, first.SHA256).Scan(&title)
	}
	if title = strings.TrimSpace(title); title == "" {
		if title = fileStem(first); title == "" {
			title = "Untitled"
		}
	}
	id, err := s.freeBookID(ctx, tx, first.MergedFrom)
	if err != nil {
		return "", err
	}
	if _, err := s.lib.TxOn(ctx, tx).CreateTextBook(library.TextBookIn{LibraryID: first.LibraryID, FolderID: folder, RootPath: root, Title: title, ID: id}); err != nil {
		return "", err
	}
	if id == first.MergedFrom {
		if _, err := tx.ExecContext(ctx, `DELETE FROM merged_books WHERE from_id = ?`, id); err != nil {
			return "", err
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, x := range ids {
		args[i] = x
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_id=?, paired_by=NULL, hold='person', book_default=(id = ?), updated_at=?
WHERE id IN (`+marks+`)`, append([]any{id, first.ID, now}, args...)...); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM text_choices WHERE ebook_file_id IN (`+marks+`)`, args...); err != nil {
		return "", err
	}
	for _, fid := range ids {
		if err := insertNotSame(ctx, tx, fid, book, userID, now); err != nil {
			return "", err
		}
	}
	// What was made in these files' editions goes back with them. Shelf rows and sessions stay.
	back := `book_id = ? AND edition_id IN (SELECT id FROM editions WHERE ebook_file_id IN (` + marks + `))`
	backArgs := append([]any{id, book}, args...)
	for _, q := range []string{
		`UPDATE OR REPLACE reading_positions SET book_id = ? WHERE ` + back,
		`UPDATE highlights SET book_id = ? WHERE ` + back,
		`UPDATE bookmarks SET book_id = ? WHERE ` + back,
		`UPDATE OR IGNORE word_list SET book_id = ? WHERE ` + back,
	} {
		if _, err := tx.ExecContext(ctx, q, backArgs...); err != nil {
			return "", err
		}
	}
	return id, nil
}
