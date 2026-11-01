package library

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Personal shelves (P1b-8). A shelf belongs to one person: every read and write names the owner and
// the SQL filters on it, so another person's shelf is ErrNotFound exactly like an unknown id. Lists,
// counts and cover previews only cover books the caller's scope may open; rows of books that became
// hidden or missing stay in the table and reappear if the book returns.

// ErrLimit means a shelf limit was reached: too many shelves for a person, or too many books on a shelf.
var ErrLimit = errors.New("library: limit reached")

// Limits are variables so tests can lower them.
var (
	maxShelves    = 200
	maxShelfBooks = 5000
)

const (
	shelfNameMax = 80
	shelfCovers  = 3
)

// Shelf is one shelf row.
type Shelf struct {
	ID, OwnerID, Name    string
	CreatedAt, UpdatedAt int64
}

// ShelfCover is the cover of one book on a shelf. The book id and title are there so a page can draw
// the cover (the image address is built from the book id). A book without a cover file has an
// empty SHA and is drawn from its title.
type ShelfCover struct {
	BookID, Title string
	CoverRef
}

// ShelfSummary is a shelf with the number of books the scope may open on it and up to three of
// their covers, in shelf order.
type ShelfSummary struct {
	Shelf
	Books  int
	Covers []ShelfCover
}

// cleanShelfName trims name and checks it: 1 to 80 characters, no control characters.
func cleanShelfName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	switch {
	case n < 1:
		return "", invalid("shelf name is empty")
	case n > shelfNameMax:
		return "", invalid("shelf name is longer than %d characters", shelfNameMax)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return "", invalid("shelf name has control characters")
	}
	return name, nil
}

// ownShelf checks, inside tx, that the shelf exists and belongs to ownerID.
func ownShelf(ctx context.Context, tx *sql.Tx, ownerID, id string) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM shelves WHERE id = ? AND owner_id = ?`, id, ownerID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: shelf", ErrNotFound)
	}
	return err
}

func touchShelf(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE shelves SET updated_at = ? WHERE id = ?`, now, id)
	return err
}

// CreateShelf adds an empty shelf. The name is trimmed; a name the person already uses (ignoring
// case) is ErrConflict, a bad name ErrInvalid, and a person with the maximum number of shelves
// gets ErrLimit.
func (r *Repo) CreateShelf(ctx context.Context, ownerID, name string) (Shelf, error) {
	name, err := cleanShelfName(name)
	if err != nil {
		return Shelf{}, err
	}
	now := r.nowMs()
	s := Shelf{ID: db.NewID("shf"), OwnerID: ownerID, Name: name, CreatedAt: now, UpdatedAt: now}
	err = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shelves WHERE owner_id = ?`, ownerID).Scan(&n); err != nil {
			return err
		}
		if n >= maxShelves {
			return fmt.Errorf("%w: shelves", ErrLimit)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES (?,?,?,?,?)`,
			s.ID, s.OwnerID, s.Name, s.CreatedAt, s.UpdatedAt)
		return mapErr(err, "shelf name")
	})
	if err != nil {
		return Shelf{}, err
	}
	return s, nil
}

// RenameShelf renames a shelf of ownerID.
func (r *Repo) RenameShelf(ctx context.Context, ownerID, id, name string) error {
	name, err := cleanShelfName(name)
	if err != nil {
		return err
	}
	now := r.nowMs()
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE shelves SET name = ?, updated_at = ? WHERE id = ? AND owner_id = ?`,
			name, now, id, ownerID)
		if err != nil {
			return mapErr(err, "shelf name")
		}
		return needRow(res, "shelf")
	})
}

// DeleteShelf deletes a shelf of ownerID and, by cascade, its items. The books stay.
func (r *Repo) DeleteShelf(ctx context.Context, ownerID, id string) error {
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM shelves WHERE id = ? AND owner_id = ?`, id, ownerID)
		if err != nil {
			return err
		}
		return needRow(res, "shelf")
	})
}

// Shelf returns a shelf of ownerID, or ErrNotFound (also for another person's shelf).
func (r *Repo) Shelf(ctx context.Context, ownerID, id string) (Shelf, error) {
	return one(r.st.Reader().QueryRowContext(ctx,
		`SELECT id, owner_id, name, created_at, updated_at FROM shelves WHERE id = ? AND owner_id = ?`, id, ownerID),
		func(s scanner) (Shelf, error) {
			var x Shelf
			return x, s.Scan(&x.ID, &x.OwnerID, &x.Name, &x.CreatedAt, &x.UpdatedAt)
		}, "shelf")
}

// Shelves returns every shelf of ownerID by name (ignoring case), then id, with the count of
// visible books and up to three covers.
func (r *Repo) Shelves(ctx context.Context, sc Scope, ownerID string) ([]ShelfSummary, error) {
	out, err := r.ShelfCounts(ctx, sc, ownerID)
	if err != nil || len(out) == 0 {
		return out, err
	}
	w := visible(sc, true)
	args := append([]any{ownerID}, w.args...)
	type pick struct{ shelf, book string }
	picks, err := many(ctx, r.st.Reader(), func(s scanner) (pick, error) {
		var p pick
		return p, s.Scan(&p.shelf, &p.book)
	}, `SELECT shelf_id, book_id FROM (
  SELECT si.shelf_id AS shelf_id, si.book_id AS book_id,
    ROW_NUMBER() OVER (PARTITION BY si.shelf_id ORDER BY si.idx, si.book_id) AS rn
  FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id AND s.owner_id = ?
  JOIN books b ON b.id = si.book_id JOIN libraries l ON l.id = b.library_id WHERE `+w.sql()+`)
  WHERE rn <= `+fmt.Sprint(shelfCovers)+` ORDER BY shelf_id, rn`, args...)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(picks))
	for _, p := range picks {
		ids = append(ids, p.book)
	}
	cards, err := r.cards(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	at := make(map[string]int, len(out))
	for i, s := range out {
		at[s.ID] = i
	}
	for _, p := range picks {
		if c, ok := byID[p.book]; ok {
			sc := ShelfCover{BookID: c.ID, Title: c.Title}
			if c.Cover != nil {
				sc.CoverRef = *c.Cover
			}
			i := at[p.shelf]
			out[i].Covers = append(out[i].Covers, sc)
		}
	}
	return out, nil
}

// ShelfCounts is Shelves without the covers: the sidebar needs names and counts on every page.
func (r *Repo) ShelfCounts(ctx context.Context, sc Scope, ownerID string) ([]ShelfSummary, error) {
	out, err := many(ctx, r.st.Reader(), func(s scanner) (ShelfSummary, error) {
		var x ShelfSummary
		return x, s.Scan(&x.ID, &x.OwnerID, &x.Name, &x.CreatedAt, &x.UpdatedAt)
	}, `SELECT id, owner_id, name, created_at, updated_at FROM shelves WHERE owner_id = ? ORDER BY name COLLATE NOCASE, id`, ownerID)
	if err != nil || len(out) == 0 {
		return out, err
	}
	w := visible(sc, true)
	args := append([]any{ownerID}, w.args...)
	type count struct {
		shelf string
		n     int
	}
	counts, err := many(ctx, r.st.Reader(), func(s scanner) (count, error) {
		var c count
		return c, s.Scan(&c.shelf, &c.n)
	}, `SELECT si.shelf_id, COUNT(*) FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id AND s.owner_id = ?
  JOIN books b ON b.id = si.book_id JOIN libraries l ON l.id = b.library_id WHERE `+w.sql()+` GROUP BY si.shelf_id`, args...)
	if err != nil {
		return nil, err
	}
	n := make(map[string]int, len(counts))
	for _, c := range counts {
		n[c.shelf] = c.n
	}
	for i := range out {
		out[i].Books = n[out[i].ID]
	}
	return out, nil
}

// shelfCursor is the position after the last book of a page: its index and id.
type shelfCursor struct {
	I int64  `json:"i"`
	B string `json:"b"`
}

func decodeShelfCursor(s string) (shelfCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	var c shelfCursor
	if err != nil || json.Unmarshal(raw, &c) != nil || c.B == "" || c.I < 0 {
		return shelfCursor{}, invalid("cursor is not valid")
	}
	return c, nil
}

// ShelfBooks returns one page of the visible books on a shelf of ownerID, in shelf order. limit 0
// means 48; the most is 120. A bad cursor is ErrInvalid. Total counts every visible book on the shelf.
func (r *Repo) ShelfBooks(ctx context.Context, sc Scope, ownerID, id, cursor string, limit int) (BookPage, error) {
	switch {
	case limit == 0:
		limit = defaultLimit
	case limit < 1:
		limit = 1
	case limit > maxLimit:
		limit = maxLimit
	}
	var cur *shelfCursor
	if cursor != "" {
		c, err := decodeShelfCursor(cursor)
		if err != nil {
			return BookPage{}, err
		}
		cur = &c
	}
	if _, err := r.Shelf(ctx, ownerID, id); err != nil {
		return BookPage{}, err
	}
	w := visible(sc, true)
	from := ` FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id AND s.owner_id = ?
  JOIN books b ON b.id = si.book_id JOIN libraries l ON l.id = b.library_id WHERE si.shelf_id = ? AND ` + w.sql()
	base := append([]any{ownerID, id}, w.args...)

	var total int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*)`+from, base...).Scan(&total); err != nil {
		return BookPage{}, err
	}
	q := `SELECT si.book_id, si.idx` + from
	args := append([]any(nil), base...)
	if cur != nil {
		q += ` AND (si.idx > ? OR (si.idx = ? AND si.book_id > ?))`
		args = append(args, cur.I, cur.I, cur.B)
	}
	q += ` ORDER BY si.idx, si.book_id LIMIT ?`
	args = append(args, limit+1)
	type row struct {
		id  string
		idx int64
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.id, &x.idx)
	}, q, args...)
	if err != nil {
		return BookPage{}, err
	}
	page := BookPage{Total: total}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		raw, _ := json.Marshal(shelfCursor{I: last.idx, B: last.id})
		page.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	ids := make([]string, len(rows))
	for i, x := range rows {
		ids[i] = x.id
	}
	page.Books, err = r.cards(ctx, ids)
	return page, err
}

// ShelvesWithBook reports which of ownerID's shelves hold the book (by shelf id). It does not
// check that the book is visible: the caller loads the book through its own authorization first.
func (r *Repo) ShelvesWithBook(ctx context.Context, ownerID, bookID string) (map[string]bool, error) {
	ids, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var id string
		return id, s.Scan(&id)
	}, `SELECT si.shelf_id FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id
  WHERE s.owner_id = ? AND si.book_id = ?`, ownerID, bookID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// AddToShelf puts a book at the end of a shelf of ownerID. Adding a book already there changes
// nothing. An unknown book is ErrNotFound; a full shelf ErrLimit. Whether the person may open the
// book is the caller's check.
func (r *Repo) AddToShelf(ctx context.Context, ownerID, id, bookID string) error {
	now := r.nowMs()
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ownShelf(ctx, tx, ownerID, id); err != nil {
			return err
		}
		var have int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM shelf_items WHERE shelf_id = ? AND book_id = ?`, id, bookID).Scan(&have)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var n int
		var top sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), MAX(idx) FROM shelf_items WHERE shelf_id = ?`, id).Scan(&n, &top); err != nil {
			return err
		}
		if n >= maxShelfBooks {
			return fmt.Errorf("%w: shelf books", ErrLimit)
		}
		idx := int64(0)
		if top.Valid {
			idx = top.Int64 + 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES (?,?,?,?)`,
			id, bookID, idx, now); err != nil {
			return mapErr(err, "shelf book")
		}
		return touchShelf(ctx, tx, id, now)
	})
}

// RemoveFromShelf takes a book off a shelf of ownerID. A book that is not on it is not an error.
func (r *Repo) RemoveFromShelf(ctx context.Context, ownerID, id, bookID string) error {
	now := r.nowMs()
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ownShelf(ctx, tx, ownerID, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM shelf_items WHERE shelf_id = ? AND book_id = ?`, id, bookID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return touchShelf(ctx, tx, id, now)
		}
		return nil
	})
}

// MoveInShelf moves a visible book one place up (delta -1) or down (+1) by swapping places with
// the nearest neighbour the scope may open, so a hidden book is stepped over. At either end it does
// nothing. Any other delta is ErrInvalid; a book that is not on the shelf or not visible is ErrNotFound.
func (r *Repo) MoveInShelf(ctx context.Context, sc Scope, ownerID, id, bookID string, delta int) error {
	if delta != -1 && delta != 1 {
		return invalid("a book moves one place at a time")
	}
	now := r.nowMs()
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ownShelf(ctx, tx, ownerID, id); err != nil {
			return err
		}
		w := visible(sc, true)
		from := ` FROM shelf_items si JOIN books b ON b.id = si.book_id JOIN libraries l ON l.id = b.library_id
  WHERE si.shelf_id = ? AND ` + w.sql()
		var idx int64
		err := tx.QueryRowContext(ctx, `SELECT si.idx`+from+` AND si.book_id = ?`,
			append(append([]any{id}, w.args...), bookID)...).Scan(&idx)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: shelf book", ErrNotFound)
		}
		if err != nil {
			return err
		}
		cond := ` AND (si.idx < ? OR (si.idx = ? AND si.book_id < ?)) ORDER BY si.idx DESC, si.book_id DESC LIMIT 1`
		if delta > 0 {
			cond = ` AND (si.idx > ? OR (si.idx = ? AND si.book_id > ?)) ORDER BY si.idx, si.book_id LIMIT 1`
		}
		var nb string
		var nidx int64
		err = tx.QueryRowContext(ctx, `SELECT si.book_id, si.idx`+from+cond,
			append(append([]any{id}, w.args...), idx, idx, bookID)...).Scan(&nb, &nidx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // already at that end
		}
		if err != nil {
			return err
		}
		for _, u := range []struct {
			idx  int64
			book string
		}{{nidx, bookID}, {idx, nb}} {
			if _, err := tx.ExecContext(ctx, `UPDATE shelf_items SET idx = ? WHERE shelf_id = ? AND book_id = ?`, u.idx, id, u.book); err != nil {
				return err
			}
		}
		return touchShelf(ctx, tx, id, now)
	})
}
