package share

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Errors the callers match on. The web layer answers every failed lookup of a link with one page.
var (
	ErrLinkUnknown = errors.New("share: no such link")
	ErrLinkSpec    = errors.New("share: the link settings are not valid")
	ErrLinkRefused = errors.New("share: this book cannot be shared with a link")
)

// LinkDays are the lengths of life a link may have, in days.
var LinkDays = []int{1, 7, 30, 90}

// LinkDefaultDays is the length offered first.
const LinkDefaultDays = 7

const (
	maxLinkLabel = 80
	msPerDay     = 86400000
	maxActive    = 50
)

// Link is one row of share_links. CreatorActive is filled by Lookup and Active: the person who made
// the link is still an enabled owner or admin.
type Link struct {
	ID, BookID, CreatedBy, Label, PasswordHash string
	ExpiresAt, RevokedAt, CreatedAt            int64
	CreatorActive                              bool
}

// HasPassword reports whether the link asks for a password.
func (l Link) HasPassword() bool { return l.PasswordHash != "" }

// LinkSpec is what a person chooses when making a link.
type LinkSpec struct {
	BookID, Label string
	Days          int
}

// LinkRow is a link with the title of its book, for lists.
type LinkRow struct {
	Link
	BookTitle string
}

// Links reads and writes the share_links table. It never decides access to a request: that is
// Checker.CanLink, a pure function of the row.
type Links struct {
	st  *db.Store
	can *Checker
	now func() time.Time
}

// NewLinks returns the links repository; a nil now means time.Now.
func NewLinks(st *db.Store, c *Checker, now func() time.Time) *Links {
	if now == nil {
		now = time.Now
	}
	return &Links{st: st, can: c, now: now}
}

// CanLink reports whether the link l allows a on t. A link reaches one book: it may view and listen
// to it, only while it is neither revoked nor expired and its maker is still an enabled owner or
// admin. Everything else is refused. It reads no database.
func (c *Checker) CanLink(l Link, a Action, t Target) bool {
	if a != ViewBook && a != ListenBook {
		return false
	}
	if t.Kind != TargetBook || t.ID == "" || t.ID != l.BookID {
		return false
	}
	return l.RevokedAt == 0 && l.ExpiresAt > c.now().UnixMilli() && l.CreatorActive
}

// LinkScope returns the filters that limit a book query (aliased b) and a library query (aliased l)
// to the one book of l, in the same shape as the filters people get.
func LinkScope(l Link) (books, libraries Filter) {
	return Filter{SQL: "b.id = ?", Args: []any{l.BookID}},
		Filter{SQL: "l.id = (SELECT library_id FROM books WHERE id = ?)", Args: []any{l.BookID}}
}

const linkColumns = `s.id, s.book_id, s.created_by, s.label, coalesce(s.password_hash, ''), s.expires_at, coalesce(s.revoked_at, 0),
	s.created_at, (u.disabled = 0 AND u.role IN ('owner','admin'))`
const linkFrom = ` FROM share_links s JOIN users u ON u.id = s.created_by`

func scanLink(row rowScanner) (Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.BookID, &l.CreatedBy, &l.Label, &l.PasswordHash, &l.ExpiresAt, &l.RevokedAt, &l.CreatedAt, &l.CreatorActive)
	return l, err
}

// staff reads the person's row inside tx and reports an enabled owner or admin, the only people who
// make or stop links. The decision goes through Can on that fresh row.
func (r *Links) staff(ctx context.Context, tx *sql.Tx, id string) (users.User, bool, error) {
	var u users.User
	var role string
	var disabled int
	err := tx.QueryRowContext(ctx, `SELECT id, role, disabled FROM users WHERE id = ?`, id).Scan(&u.ID, &role, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	u.Role, u.Disabled = users.Role(role), disabled == 1
	return u, r.can.Can(ctx, u, ManageUsers, Target{Kind: TargetUser, Role: users.RoleMember}), nil
}

// Create writes a link inside tx and returns it with the raw token, which exists nowhere else.
// passwordHash is "" for a link without a password (the caller hashes before the transaction).
// ErrLinkSpec for a bad length or label; ErrLinkRefused when by is not an enabled owner or admin or
// the book is missing or refused.
func (r *Links) Create(ctx context.Context, tx *sql.Tx, by users.User, s LinkSpec, passwordHash string) (Link, string, error) {
	maker, ok, err := r.staff(ctx, tx, by.ID)
	if err != nil {
		return Link{}, "", err
	}
	if !ok {
		return Link{}, "", ErrLinkRefused
	}
	days := false
	for _, d := range LinkDays {
		days = days || d == s.Days
	}
	s.Label = strings.TrimSpace(s.Label)
	if !days || utf8.RuneCountInString(s.Label) > maxLinkLabel {
		return Link{}, "", ErrLinkSpec
	}
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM books WHERE id = ? AND refused_reason IS NULL AND missing_since IS NULL`, s.BookID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, "", ErrLinkRefused
	}
	if err != nil {
		return Link{}, "", err
	}
	now := r.now().UnixMilli()
	tok, hash := token.New()
	id := db.NewID("lnk")
	var pw any
	if passwordHash != "" {
		pw = passwordHash
	}
	expires := now + int64(s.Days)*msPerDay
	if _, err := tx.ExecContext(ctx, `INSERT INTO share_links(id, token_hash, book_id, created_by, label, password_hash, can_read, expires_at, revoked_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, NULL, ?)`, id, hash[:], s.BookID, maker.ID, s.Label, pw, expires, now); err != nil {
		return Link{}, "", err
	}
	if err := db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: maker.ID, Action: "link.create", Target: "link:" + id,
		Detail: map[string]any{"book": s.BookID, "expires_at": expires, "password": passwordHash != ""}}); err != nil {
		return Link{}, "", err
	}
	l, err := scanLink(tx.QueryRowContext(ctx, `SELECT `+linkColumns+linkFrom+` WHERE s.id = ?`, id))
	return l, tok, err
}

// Lookup finds the link a token names. A malformed or unknown token is ErrLinkUnknown, the malformed
// one without a query. Revoked and expired rows are returned; CanLink judges them.
func (r *Links) Lookup(ctx context.Context, tok string) (Link, error) {
	hash, ok := token.Hash(tok)
	if !ok {
		return Link{}, ErrLinkUnknown
	}
	l, err := scanLink(r.st.Reader().QueryRowContext(ctx, `SELECT `+linkColumns+linkFrom+` WHERE s.token_hash = ?`, hash[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrLinkUnknown
	}
	return l, err
}

// Revoke ends a link. Idempotent: a link already revoked writes nothing more. ErrLinkUnknown for an
// unknown id, ErrLinkRefused when by is not an enabled owner or admin.
func (r *Links) Revoke(ctx context.Context, tx *sql.Tx, by users.User, id string) error {
	maker, ok, err := r.staff(ctx, tx, by.ID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLinkRefused
	}
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM share_links WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrLinkUnknown
	} else if err != nil {
		return err
	}
	now := r.now().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE share_links SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return err
	}
	return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: maker.ID, Action: "link.revoke", Target: "link:" + id})
}

// Active lists the links that still work (not revoked, not expired, maker still staff), newest
// first, at most 50. bookID "" lists every book's.
func (r *Links) Active(ctx context.Context, nowMs int64, bookID string, limit int) ([]LinkRow, error) {
	if limit <= 0 || limit > maxActive {
		limit = maxActive
	}
	q := `SELECT ` + linkColumns + `, b.title` + linkFrom + ` JOIN books b ON b.id = s.book_id
		WHERE s.revoked_at IS NULL AND s.expires_at > ? AND u.disabled = 0 AND u.role IN ('owner','admin')`
	args := []any{nowMs}
	if bookID != "" {
		q += ` AND s.book_id = ?`
		args = append(args, bookID)
	}
	q += ` ORDER BY s.created_at DESC, s.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LinkRow
	for rows.Next() {
		var lr LinkRow
		if err := rows.Scan(&lr.ID, &lr.BookID, &lr.CreatedBy, &lr.Label, &lr.PasswordHash, &lr.ExpiresAt, &lr.RevokedAt,
			&lr.CreatedAt, &lr.CreatorActive, &lr.BookTitle); err != nil {
			return nil, err
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}
