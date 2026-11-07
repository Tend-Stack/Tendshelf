package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// ErrHasPassword means the account already has a password, so SetFirstCredentials refuses: a
// password is only ever set on an account that has none, so a stolen session cannot change
// credentials.
var ErrHasPassword = errors.New("users: the account already has a password")

// CreateInvited inserts an account made by accepting an invite, inside tx. The role must be
// admin, member or guest (ErrRole). A hash of "" leaves the account without a password; it is
// reached only through the session the invite started, until someone adds one. The username is
// suggested from the display name and retried a few times on a clash.
func (r *Repo) CreateInvited(ctx context.Context, tx *sql.Tx, n NewUser, hash string) (User, error) {
	switch n.Role {
	case RoleAdmin, RoleMember, RoleGuest:
	default:
		return User{}, ErrRole
	}
	if n.AddsBooks && n.Role != RoleMember {
		return User{}, ErrRole
	}
	u := User{ID: db.NewID("usr"), DisplayName: n.DisplayName, Email: n.Email, Role: n.Role, AddsBooks: n.AddsBooks, CreatedAt: r.nowMs()}
	adds := 0
	if n.AddsBooks {
		adds = 1
	}
	var email, pw any
	if u.Email != "" {
		email = u.Email
	}
	if hash != "" {
		pw = hash
	}
	for range 5 {
		u.Username = SuggestUsername(n.DisplayName)
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, display_name, email, role, password_hash, disabled, created_at, adds_books)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`, u.ID, u.Username, u.DisplayName, email, string(u.Role), pw, u.CreatedAt, adds)
		if err == nil {
			return u, nil
		}
		if !db.IsUniqueViolation(err) || !strings.Contains(err.Error(), "users.username") {
			return User{}, err
		}
	}
	return User{}, errors.New("users: could not find a free username")
}

// HasPassword reports whether the account has a password.
func (r *Repo) HasPassword(ctx context.Context, id string) (bool, error) {
	var has bool
	err := r.st.Reader().QueryRowContext(ctx, `SELECT password_hash IS NOT NULL FROM users WHERE id = ?`, id).Scan(&has)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return has, err
}

// SetFirstCredentials gives a password-less account its username and password hash, inside tx.
// It changes nothing on an account that already has a password (ErrHasPassword) or does not exist
// (ErrNotFound); a username in use is FieldErrors{"username": "taken"}. The caller validates the
// username and hashes the password first.
func (r *Repo) SetFirstCredentials(ctx context.Context, tx *sql.Tx, id, username, hash string) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, password_hash = ? WHERE id = ? AND password_hash IS NULL`, username, hash, id)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return FieldErrors{"username": "taken"}
		}
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, id).Scan(&one); err != nil {
			return ErrNotFound
		}
		return ErrHasPassword
	}
	return nil
}

// foldTable turns the accented letters a family name is likely to carry into plain ones.
var foldTable = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'å': 'a', 'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i', 'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ñ': 'n', 'ç': 'c',
}

// suffixAlphabet is base32 without digits 0, 1, 8, 9: lowercase letters and 2-7.
const suffixAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// SuggestUsername makes a username from a display name: lowercase ASCII letters and digits (accents
// folded), at most 20 of them ("listener" when fewer than 3), then "-" and four random characters
// so two people called Rosa do not clash. The result always matches the username rule.
func SuggestUsername(name string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		if f, ok := foldTable[c]; ok {
			c = f
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
		if b.Len() == 20 {
			break
		}
	}
	base := b.String()
	if len(base) < 3 {
		base = "listener"
	}
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	suffix := make([]byte, 4)
	for i, v := range raw {
		suffix[i] = suffixAlphabet[int(v)%len(suffixAlphabet)]
	}
	return base + "-" + string(suffix)
}
