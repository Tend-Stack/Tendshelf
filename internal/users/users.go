package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Role is an account's standing on the server.
type Role string

// The four roles. Exactly one account is the owner.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleGuest  Role = "guest"
)

// User is an account without its password hash.
type User struct {
	ID          string
	Username    string
	DisplayName string
	Email       string
	Role        Role
	AddsBooks   bool // a member who also adds books of their own (decision 0029)
	Disabled    bool
	CreatedAt   int64 // Unix milliseconds UTC
	LastSeenAt  int64 // 0 when never seen
}

// Errors the callers match on.
var (
	ErrNotFound    = errors.New("users: not found")
	ErrOwnerExists = errors.New("users: the server already has an owner")
	ErrOwnerFixed  = errors.New("users: the owner's role and account cannot change")
	ErrSelf        = errors.New("users: nobody changes their own role or account here")
	ErrRole        = errors.New("users: a role must be admin, contributor, member or guest")
)

// Options configures a Repo. The zero value is production-ready.
type Options struct {
	Hash       HashParams       // zero: DefaultHash
	Now        func() time.Time // nil: time.Now
	MaxHashers int              // concurrent argon2 runs; <= 0: 4
}

// Repo reads and writes the users table and hashes passwords.
type Repo struct {
	st   *db.Store
	opts Options
	sem  chan struct{}

	dummyOnce sync.Once
	dummy     string
}

// New returns a Repo on st.
func New(st *db.Store, o Options) *Repo {
	if o.Hash == (HashParams{}) {
		o.Hash = DefaultHash
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxHashers <= 0 {
		o.MaxHashers = 4
	}
	return &Repo{st: st, opts: o, sem: make(chan struct{}, o.MaxHashers)}
}

func (r *Repo) nowMs() int64 { return r.opts.Now().UnixMilli() }

// CreateOwner inserts the one owner inside tx. The caller hashes the password
// first (never inside a write transaction). It returns ErrOwnerExists when an
// owner is already there, and FieldErrors{"username": "taken"} when the
// username is in use.
func (r *Repo) CreateOwner(ctx context.Context, tx *sql.Tx, n NewUser, hash string) (User, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE role = 'owner')`).Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrOwnerExists
	}
	u := User{
		ID: db.NewID("usr"), Username: n.Username, DisplayName: n.DisplayName,
		Email: n.Email, Role: RoleOwner, CreatedAt: r.nowMs(),
	}
	var email any
	if u.Email != "" {
		email = u.Email
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, display_name, email, role, password_hash, disabled, created_at)
		VALUES (?, ?, ?, ?, 'owner', ?, 0, ?)`, u.ID, u.Username, u.DisplayName, email, hash, u.CreatedAt)
	if err != nil {
		if db.IsUniqueViolation(err) {
			if strings.Contains(err.Error(), "users.username") {
				return User{}, FieldErrors{"username": "taken"}
			}
			return User{}, ErrOwnerExists
		}
		return User{}, err
	}
	return u, nil
}

// OwnerExists reports whether the server has an owner yet.
func (r *Repo) OwnerExists(ctx context.Context) (bool, error) {
	var exists bool
	err := r.st.Reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE role = 'owner')`).Scan(&exists)
	return exists, err
}

const userColumns = `id, username, display_name, COALESCE(email, ''), role, disabled, created_at, COALESCE(last_seen_at, 0), adds_books`

func scanUser(row interface{ Scan(...any) error }, extra ...any) (User, error) {
	var u User
	var role string
	var disabled, adds int
	dst := append([]any{&u.ID, &u.Username, &u.DisplayName, &u.Email, &role, &disabled, &u.CreatedAt, &u.LastSeenAt, &adds}, extra...)
	if err := row.Scan(dst...); err != nil {
		return User{}, err
	}
	u.Role = Role(role)
	u.Disabled = disabled != 0
	u.AddsBooks = adds != 0 && u.Role == RoleMember
	return u, nil
}

// Contributor reports whether the account is a member who adds books and is switched on. It is the only
// test of "contributor" in Go (decision 0029).
func (u User) Contributor() bool {
	return u.Role == RoleMember && u.AddsBooks && !u.Disabled
}

// Kind is what pages and forms call a person: the role, with a contributor told apart from a member.
type Kind string

// The five kinds.
const (
	KindOwner       Kind = "owner"
	KindAdmin       Kind = "admin"
	KindContributor Kind = "contributor"
	KindMember      Kind = "member"
	KindGuest       Kind = "guest"
)

// Kind derives the kind from the role and the flag. A disabled contributor is still a contributor here;
// only Contributor() looks at disabled.
func (u User) Kind() Kind {
	if u.Role == RoleMember && u.AddsBooks {
		return KindContributor
	}
	return Kind(u.Role)
}

// ParseKind turns a form value into the role and flag it stands for: admin, contributor, member or guest.
// Anything else, the owner included, is ErrRole.
func ParseKind(s string) (Role, bool, error) {
	switch Kind(s) {
	case KindAdmin:
		return RoleAdmin, false, nil
	case KindContributor:
		return RoleMember, true, nil
	case KindMember:
		return RoleMember, false, nil
	case KindGuest:
		return RoleGuest, false, nil
	}
	return "", false, ErrRole
}

// ByUsername finds an account by username (case-insensitive) and returns its
// password hash too ("" when the account has none).
func (r *Repo) ByUsername(ctx context.Context, name string) (User, string, error) {
	var hash string
	u, err := scanUser(r.st.Reader().QueryRowContext(ctx,
		`SELECT `+userColumns+`, COALESCE(password_hash, '') FROM users WHERE username = ?`, name), &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return u, hash, err
}

// ByID finds an account by id.
func (r *Repo) ByID(ctx context.Context, id string) (User, error) {
	u, err := scanUser(r.st.Reader().QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// TouchLastSeen records the time (Unix milliseconds) the account was last seen.
func (r *Repo) TouchLastSeen(ctx context.Context, ex db.Execer, id string, at int64) error {
	_, err := ex.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`, at, id)
	return err
}

// SetPassword stores a new password hash for the account, inside the caller's write transaction.
// The caller hashes first: hashing never happens inside a write transaction. ErrNotFound when no
// such account exists.
func (r *Repo) SetPassword(ctx context.Context, ex db.Execer, userID, hash string) error {
	res, err := ex.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// ByIDTx finds an account by id inside the caller's transaction, so a decision made on the
// row cannot be overtaken by another write.
func (r *Repo) ByIDTx(ctx context.Context, tx *sql.Tx, id string) (User, error) {
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// SetRole moves the account id to the role to, inside tx, and writes a "user.role" audit row.
// to must be admin, member or guest (ErrRole); the owner is never changed and never made
// (ErrOwnerFixed); byID cannot change itself (ErrSelf). An unchanged role writes nothing.
// It returns the role the account had. Authorization is the caller's.
func (r *Repo) SetRole(ctx context.Context, tx *sql.Tx, byID, id string, to Role) (Role, error) {
	switch to {
	case RoleAdmin, RoleMember, RoleGuest:
	case RoleOwner:
		return "", ErrOwnerFixed
	default:
		return "", ErrRole
	}
	if id == byID {
		return "", ErrSelf
	}
	u, err := r.ByIDTx(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if u.Role == RoleOwner {
		return "", ErrOwnerFixed
	}
	if u.Role == to {
		return u.Role, nil
	}
	now := r.nowMs()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?1, adds_books = CASE WHEN ?1 = 'member' THEN adds_books ELSE 0 END WHERE id = ?2`, string(to), id); err != nil {
		return "", err
	}
	err = db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: byID, Action: "user.role", Target: "user:" + id,
		Detail: map[string]any{"from": u.Role, "to": to}})
	return u.Role, err
}

// SetKind is SetRole that also knows the contributor: to is admin, contributor, member or guest. The same
// guards hold (ErrOwnerFixed, ErrSelf, ErrRole); role and flag change in one UPDATE, an unchanged kind writes
// nothing, and the audit row "user.role" carries the kinds. It returns the kind the account had.
func (r *Repo) SetKind(ctx context.Context, tx *sql.Tx, byID, id string, to Kind) (Kind, error) {
	if to == Kind(RoleOwner) {
		return "", ErrOwnerFixed
	}
	role, adds, err := ParseKind(string(to))
	if err != nil {
		return "", err
	}
	if id == byID {
		return "", ErrSelf
	}
	u, err := r.ByIDTx(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if u.Role == RoleOwner {
		return "", ErrOwnerFixed
	}
	from := u.Kind()
	if from == to {
		return from, nil
	}
	flag := 0
	if adds {
		flag = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, adds_books = ? WHERE id = ?`, string(role), flag, id); err != nil {
		return "", err
	}
	err = db.Audit(ctx, tx, db.AuditEntry{At: r.nowMs(), UserID: byID, Action: "user.role", Target: "user:" + id,
		Detail: map[string]any{"from": from, "to": to}})
	return from, err
}

// SetDisabled turns the account id off or back on inside tx and writes a "user.disable" or
// "user.enable" audit row. The owner cannot be turned off (ErrOwnerFixed) and byID cannot
// change itself (ErrSelf). An unchanged flag writes nothing. Ending the person's sessions is
// the caller's job (auth.Sessions.EndAllForUser) in the same transaction.
func (r *Repo) SetDisabled(ctx context.Context, tx *sql.Tx, byID, id string, off bool) error {
	if id == byID {
		return ErrSelf
	}
	u, err := r.ByIDTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if u.Role == RoleOwner {
		return ErrOwnerFixed
	}
	if u.Disabled == off {
		return nil
	}
	v, action := 0, "user.enable"
	if off {
		v, action = 1, "user.disable"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE id = ?`, v, id); err != nil {
		return err
	}
	return db.Audit(ctx, tx, db.AuditEntry{At: r.nowMs(), UserID: byID, Action: action, Target: "user:" + id})
}
