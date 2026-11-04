package library

import "context"

// SidecarBook is a book a scan may re-read sidecar files for: where it lives and the signature of
// the sidecars when they were last read.
type SidecarBook struct {
	ID, RootPath string
	Kind         string // BookAudio or BookText
	Single       bool   // one audio file whose path is the root path
	Signature    string // as stored; meaningful only when Checked
	Checked      bool   // a signature has been stored for this book
}

// PutSidecarSignature records the signature of a book's sidecar files. A row that already holds the
// same signature is left untouched, so repeating the call writes nothing; checked_at is when the
// signature last changed.
func (t *Tx) PutSidecarSignature(bookID, sig string, at int64) error {
	_, err := t.exec(`INSERT INTO book_sidecars(book_id, signature, checked_at) VALUES (?,?,?)
  ON CONFLICT(book_id) DO UPDATE SET signature = excluded.signature, checked_at = excluded.checked_at
  WHERE book_sidecars.signature <> excluded.signature`, bookID, sig, at)
	return mapErr(err, "book")
}

// SidecarCandidates lists the books of a library that are present and not refused, with the sidecar
// signature stored for each.
func (r *Repo) SidecarCandidates(ctx context.Context, libraryID string) ([]SidecarBook, error) {
	return many(ctx, r.st.Reader(), func(s scanner) (SidecarBook, error) {
		var b SidecarBook
		var sig *string
		var single int
		if err := s.Scan(&b.ID, &b.RootPath, &b.Kind, &single, &sig); err != nil {
			return b, err
		}
		b.Single = single != 0
		if sig != nil {
			b.Signature, b.Checked = *sig, true
		}
		return b, nil
	}, `SELECT b.id, b.root_path, b.kind,
    EXISTS(SELECT 1 FROM audio_files a WHERE a.book_id = b.id AND a.path = b.root_path AND a.missing_since IS NULL),
    s.signature
  FROM books b LEFT JOIN book_sidecars s ON s.book_id = b.id
  WHERE b.library_id = ? AND b.missing_since IS NULL AND b.refused_reason IS NULL
  ORDER BY b.root_path`, libraryID)
}
