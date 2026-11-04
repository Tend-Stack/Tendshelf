package ebook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// UploadError is a refused upload: an HTTP status, a short code and a plain sentence.
type UploadError struct {
	Status        int
	Code, Message string
}

func (e *UploadError) Error() string { return "ebook upload: " + e.Code }

func upErr(status int, code, msg string) *UploadError { return &UploadError{status, code, msg} }

// ErrNotFound is returned by Dismiss for a file that is not a dismissable upload of the book.
var ErrNotFound = errors.New("ebook: not found")

const uploadSpaceMargin = 64 << 20

// AddUpload stores an uploaded ebook byte-identical at <data>/ebooks/<sha256>.<ext>, records it as
// paired with the book and queues its read. Nothing is written to a library folder.
func (s *Service) AddUpload(ctx context.Context, book library.Book, userID, name string, body io.Reader) (File, error) {
	ext := ExtOf(name)
	switch ext {
	case "":
		return File{}, upErr(http.StatusUnprocessableEntity, "unsupported", "Tendshelf cannot read this kind of file.")
	case "kfx", "kfx-zip":
		return File{}, upErr(http.StatusUnprocessableEntity, "copy_protected", "This ebook is copy-protected, so Tendshelf will not open it.")
	}
	if err := os.MkdirAll(s.incomingDir(), 0o700); err != nil {
		return File{}, err
	}
	free, err := media.FreeBytes(s.ebooksDir())
	if err != nil && !errors.Is(err, media.ErrNoSpaceInfo) {
		return File{}, err
	}
	if err == nil && free < uint64(s.limits.MaxFileBytes)+uploadSpaceMargin {
		return File{}, upErr(http.StatusInsufficientStorage, "no_space", "The server's data folder is full.")
	}

	id := db.NewID("ebf")
	part := filepath.Join(s.incomingDir(), id+".part")
	sha, n, err := s.stream(part, body)
	if err != nil {
		_ = os.Remove(part)
		return File{}, err
	}
	switch {
	case n > s.limits.MaxFileBytes:
		_ = os.Remove(part)
		return File{}, upErr(http.StatusRequestEntityTooLarge, "too_large", "This file is larger than 200 MB.")
	case n == 0:
		_ = os.Remove(part)
		return File{}, upErr(http.StatusUnprocessableEntity, "empty", "That file is empty.")
	}

	var dup int
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM ebook_files
WHERE origin='upload' AND book_id=? AND sha256=?`, book.ID, sha).Scan(&dup); err != nil {
		_ = os.Remove(part)
		return File{}, err
	}
	if dup > 0 {
		_ = os.Remove(part)
		return File{}, upErr(http.StatusConflict, "duplicate", "This ebook is already on this book.")
	}

	final := s.storedPath(sha, ext)
	if _, err := os.Stat(final); err == nil {
		_ = os.Remove(part)
	} else if err := os.Rename(part, final); err != nil {
		_ = os.Remove(part)
		return File{}, err
	}
	qh, size, err := media.QuickHash(final)
	if err != nil {
		return File{}, err
	}
	now := s.now().UnixMilli()
	_, err = s.db.Writer().ExecContext(ctx, `INSERT INTO ebook_files(id, origin, library_id, book_id, paired_by, ext, size,
mtime_ns, quick_hash, sha256, state, original_name, uploaded_by, created_at, updated_at)
VALUES(?, 'upload', ?, ?, 'upload', ?, ?, 0, ?, ?, 'pending', ?, NULLIF(?, ''), ?, ?)`,
		id, book.LibraryID, book.ID, ext, size, qh, sha, cutName(name), userID, now, now)
	if err != nil {
		s.dropBytes(ctx, sha, ext, "")
		if db.IsUniqueViolation(err) {
			return File{}, upErr(http.StatusConflict, "duplicate", "This ebook is already on this book.")
		}
		return File{}, err
	}
	if err := s.enqueue(ctx, id, userID); err != nil {
		s.log.Warn("ebooks: could not queue the read; it is retried later", "err", err)
	}
	f, _, err := s.store.File(ctx, id)
	return f, err
}

// stream copies body to path while hashing it, reading at most MaxFileBytes+1 bytes.
func (s *Service) stream(path string, body io.Reader) (sha string, n int64, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(body, s.limits.MaxFileBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// cutName keeps the base name of an upload, at most 255 bytes, on a rune boundary.
func cutName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if len(name) > 255 {
		name = name[:255]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}

// dropBytes deletes an upload's stored bytes unless another non-refused upload row (other than
// exceptID) still uses them.
func (s *Service) dropBytes(ctx context.Context, sha, ext, exceptID string) {
	var n int
	err := s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM ebook_files
WHERE origin='upload' AND sha256=? AND ext=? AND id<>? AND state<>'refused'`, sha, ext, exceptID).Scan(&n)
	if err != nil || n > 0 {
		return
	}
	_ = os.Remove(s.storedPath(sha, ext))
}

// Dismiss deletes an upload of the book that was refused or failed, and its bytes when nothing else
// uses them. Anything else is ErrNotFound.
func (s *Service) Dismiss(ctx context.Context, bookID, fileID string) error {
	f, ok, err := s.store.File(ctx, fileID)
	if err != nil {
		return err
	}
	if !ok || f.Origin != "upload" || f.BookID != bookID || (f.State != "refused" && f.State != "failed") {
		return ErrNotFound
	}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE id=? AND book_id=? AND origin='upload'
AND state IN ('refused','failed')`, fileID, bookID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		// The last file of a text book was its only reason to exist.
		_, err = tx.ExecContext(ctx, `DELETE FROM books WHERE id=? AND kind='text'
AND NOT EXISTS (SELECT 1 FROM ebook_files WHERE book_id = books.id)`, bookID)
		return err
	})
	if err != nil {
		return err
	}
	s.dropBytes(ctx, f.SHA256, f.Ext, fileID)
	return nil
}
