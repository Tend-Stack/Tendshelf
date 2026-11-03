package library

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// UpdatePersonDetails stores what an author lookup found: the description, the Audible ASIN and the
// image id (the stored file's name). An unknown person is ErrNotFound.
func (t *Tx) UpdatePersonDetails(id, description, asin, imageID string) error {
	res, err := t.exec(`UPDATE people SET description = ?, asin = ?, image_id = ? WHERE id = ?`, description, asin, nullStr(imageID), id)
	if err != nil {
		return err
	}
	return needRow(res, "person")
}

// SetAuthorASIN sets the ASIN of the book's author called name (case-insensitive) when that
// author has none yet. No matching author is not an error.
func (t *Tx) SetAuthorASIN(bookID, name, asin string) error {
	_, err := t.exec(`UPDATE people SET asin = ? WHERE asin = '' AND name = ? COLLATE NOCASE
  AND id IN (SELECT person_id FROM book_people WHERE book_id = ? AND role = 'author')`, asin, strings.TrimSpace(name), bookID)
	return err
}

// PutUserMetadataValue stores a value the person chose (source "user"). PutMetadataValue refuses
// that source; this call is for the details features that act on a person's click.
func (t *Tx) PutUserMetadataValue(bookID string, v MetadataValue) error {
	if v.Field == "" || !json.Valid(v.Value) {
		return invalid("metadata field and a valid JSON value are required")
	}
	_, err := t.exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?,?,?,?,?)
  ON CONFLICT(book_id, field, source) DO UPDATE SET value_json = excluded.value_json, observed_at = excluded.observed_at`,
		bookID, v.Field, MetadataSourceUser, string(v.Value), v.ObservedAt)
	return mapErr(err, "book")
}

// DeleteChosenMetadata removes every "user" and "online" value of a book, so the next resolve
// reads the files again.
func (t *Tx) DeleteChosenMetadata(bookID string) error {
	_, err := t.exec(`DELETE FROM metadata_values WHERE book_id = ? AND source IN (?, 'online')`, bookID, MetadataSourceUser)
	return err
}

// PersonByID returns a person with their ASIN and image id, whatever the viewer may see. It is
// for jobs only; pages use Person, which is scoped.
func (r *Repo) PersonByID(ctx context.Context, id string) (Person, error) {
	row := r.st.Reader().QueryRowContext(ctx, `SELECT id, name, description, asin, COALESCE(image_id, '') FROM people WHERE id = ?`, id)
	return one(row, func(s scanner) (Person, error) {
		var p Person
		return p, s.Scan(&p.ID, &p.Name, &p.Description, &p.ASIN, &p.ImageID)
	}, "person")
}

// PersonImageInUse reports whether any person still points at the image id.
func (r *Repo) PersonImageInUse(ctx context.Context, imageID string) (bool, error) {
	var n int
	err := r.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE image_id = ?`, imageID).Scan(&n)
	return n > 0, err
}

// Audit appends an audit row in this transaction.
func (t *Tx) Audit(e db.AuditEntry) error { return db.Audit(t.ctx, t.tx, e) }
