package share

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Errors the callers match on. The web layer answers ErrInviteUnknown, ErrInviteRefused and
// ErrInviteNoAccess with one and the same page, so a link says nothing about why it fails.
var (
	ErrInviteUnknown  = errors.New("share: no such invite")
	ErrInviteExpired  = errors.New("share: the invite has ended")
	ErrInviteUsedUp   = errors.New("share: the invite was already used")
	ErrInviteRefused  = errors.New("share: the person who made the invite may no longer invite this role")
	ErrInviteGrants   = errors.New("share: the invite's access list is not valid")
	ErrInviteNoAccess = errors.New("share: nothing the invite offered is still there")
)

// Limits on what an invite carries.
const (
	maxInviteGrants = 20
	maxInviteUses   = 50
)

// InviteGrant is one thing an invite opens, stored in invites.grants_json.
type InviteGrant struct {
	Scope    GrantScope `json:"scope"`
	Target   string     `json:"target,omitempty"`
	Download bool       `json:"download"`
	Read     bool       `json:"read"`
}

// InviteSpec is what Create needs.
type InviteSpec struct {
	Name, Email, Note string
	Role              users.Role
	Grants            []InviteGrant
	ExpiresAt         int64 // Unix milliseconds; 0 means until the inviter stops it
	MaxUses           int   // 0 means 1
}

// Invite is one row of the invites table with the inviter's name.
type Invite struct {
	ID, CreatedBy, InviterName, Name, Email, Note string
	Role                                          users.Role
	Grants                                        []InviteGrant
	ExpiresAt                                     int64
	MaxUses, UseCount                             int
	RevokedAt, CreatedAt, UpdatedAt               int64
	// MailJobID is the job that emails this invite ("" if none); MailState and MailCode are that job's
	// state and error code, read with the invite so a page shows Sending, Sent or Couldn't send.
	MailJobID, MailState, MailCode string
}

// InviteState is where an invite stands at a moment.
type InviteState int

// The states. A revoked invite wins over the others.
const (
	InvitePending InviteState = iota
	InviteExpired
	InviteUsedUp
	InviteRevoked
)

// State reports the state at nowMs. An invite that ends exactly now is over.
func (i Invite) State(nowMs int64) InviteState {
	switch {
	case i.RevokedAt != 0:
		return InviteRevoked
	case i.ExpiresAt != 0 && i.ExpiresAt <= nowMs:
		return InviteExpired
	case i.UseCount >= i.MaxUses:
		return InviteUsedUp
	}
	return InvitePending
}

// pendingSQL is the one definition of "can still be used", with one placeholder: now.
const pendingSQL = `revoked_at IS NULL AND use_count < max_uses AND (expires_at IS NULL OR expires_at > ?)`

// Invites reads and writes the invites table. It authorizes by asking the Checker about the
// inviter's fresh row, inside the write transaction, every time.
type Invites struct {
	st     *db.Store
	can    *Checker
	grants *Grants
	users  *users.Repo
	now    func() time.Time
}

// NewInvites returns the invites repository; a nil now means time.Now.
func NewInvites(st *db.Store, c *Checker, g *Grants, u *users.Repo, now func() time.Time) *Invites {
	if now == nil {
		now = time.Now
	}
	return &Invites{st: st, can: c, grants: g, users: u, now: now}
}

// ManageableInviteRoles lists the roles u may invite: the owner admins, members and guests, an
// admin members and guests, anyone else (or a disabled account) nobody.
func ManageableInviteRoles(u users.User) []users.Role {
	if u.Disabled {
		return nil
	}
	switch u.Role {
	case users.RoleOwner:
		return []users.Role{users.RoleAdmin, users.RoleMember, users.RoleGuest}
	case users.RoleAdmin:
		return []users.Role{users.RoleMember, users.RoleGuest}
	}
	return nil
}

func inviteRole(role users.Role) bool {
	return role == users.RoleAdmin || role == users.RoleMember || role == users.RoleGuest
}

// mayInvite re-reads the inviter inside tx and asks Can about the role.
func (r *Invites) mayInvite(ctx context.Context, tx *sql.Tx, inviterID string, role users.Role) (users.User, error) {
	u, err := r.users.ByIDTx(ctx, tx, inviterID)
	if errors.Is(err, users.ErrNotFound) {
		return users.User{}, ErrInviteRefused
	}
	if err != nil {
		return users.User{}, err
	}
	if !inviteRole(role) || !r.can.Can(ctx, u, ManageUsers, Target{Kind: TargetUser, Role: role}) {
		return users.User{}, ErrInviteRefused
	}
	return u, nil
}

const inviteColumns = `i.id, i.created_by, coalesce(nullif(u.display_name, ''), u.username), i.name, coalesce(i.email, ''), i.note, i.role,
	i.grants_json, coalesce(i.expires_at, 0), i.max_uses, i.use_count, coalesce(i.revoked_at, 0), i.created_at, i.updated_at,
	coalesce(i.mail_job_id, ''), coalesce(j.state, ''), coalesce(j.error_code, '')`

const inviteFrom = ` FROM invites i JOIN users u ON u.id = i.created_by LEFT JOIN jobs j ON j.id = i.mail_job_id`

func scanInvite(row rowScanner) (Invite, error) {
	var inv Invite
	var role, grantsJSON string
	err := row.Scan(&inv.ID, &inv.CreatedBy, &inv.InviterName, &inv.Name, &inv.Email, &inv.Note, &role, &grantsJSON,
		&inv.ExpiresAt, &inv.MaxUses, &inv.UseCount, &inv.RevokedAt, &inv.CreatedAt, &inv.UpdatedAt,
		&inv.MailJobID, &inv.MailState, &inv.MailCode)
	if err != nil {
		return Invite{}, err
	}
	inv.Role = users.Role(role)
	if err := json.Unmarshal([]byte(grantsJSON), &inv.Grants); err != nil {
		return Invite{}, err
	}
	return inv, nil
}

// checkSpec validates the person-facing fields of a spec and returns them normalised.
func checkSpec(s InviteSpec, now int64) (InviteSpec, error) {
	s.Name, s.Email, s.Note = strings.TrimSpace(s.Name), strings.TrimSpace(s.Email), strings.TrimSpace(s.Note)
	errs := users.FieldErrors{}
	switch {
	case strings.IndexFunc(s.Name, unicode.IsControl) >= 0:
		errs["name"] = "invalid"
	case s.Name == "":
		errs["name"] = "required"
	case utf8.RuneCountInString(s.Name) > 80:
		errs["name"] = "too_long"
	}
	if s.Email != "" && !users.ValidEmail(s.Email) {
		errs["email"] = "invalid"
	}
	if utf8.RuneCountInString(s.Note) > 500 {
		errs["note"] = "too_long"
	}
	if s.ExpiresAt != 0 && s.ExpiresAt <= now {
		errs["expires"] = "past"
	}
	if len(errs) > 0 {
		return s, errs
	}
	if s.MaxUses == 0 {
		s.MaxUses = 1
	}
	if s.MaxUses < 1 || s.MaxUses > maxInviteUses {
		return s, ErrInviteGrants
	}
	return s, nil
}

// checkGrants applies the count, duplicate and target rules of an invite's access list.
func checkGrants(ctx context.Context, tx *sql.Tx, role users.Role, gs []InviteGrant) error {
	if role == users.RoleAdmin {
		if len(gs) != 0 {
			return ErrInviteGrants
		}
		return nil
	}
	if len(gs) < 1 || len(gs) > maxInviteGrants {
		return ErrInviteGrants
	}
	seen := map[[2]string]bool{}
	for _, g := range gs {
		k := [2]string{string(g.Scope), g.Target}
		if seen[k] {
			return ErrInviteGrants
		}
		seen[k] = true
		if err := validateTarget(ctx, tx, g.Scope, g.Target); err != nil {
			if errors.Is(err, ErrGrantScope) {
				return ErrInviteGrants
			}
			return err
		}
	}
	return nil
}

// Create writes an invite inside tx and returns it with the raw token, which exists nowhere else.
// by must be allowed to invite the role (ErrInviteRefused), judged on a fresh read of its row.
// Field problems come back as users.FieldErrors, a bad access list as ErrInviteGrants or
// ErrGrantTarget.
func (r *Invites) Create(ctx context.Context, tx *sql.Tx, by users.User, s InviteSpec) (Invite, string, error) {
	inviter, err := r.mayInvite(ctx, tx, by.ID, s.Role)
	if err != nil {
		return Invite{}, "", err
	}
	now := r.now().UnixMilli()
	s, err = checkSpec(s, now)
	if err != nil {
		return Invite{}, "", err
	}
	if err := checkGrants(ctx, tx, s.Role, s.Grants); err != nil {
		return Invite{}, "", err
	}
	gj := s.Grants
	if gj == nil {
		gj = []InviteGrant{}
	}
	raw, err := json.Marshal(gj)
	if err != nil {
		return Invite{}, "", err
	}
	tok, hash := token.New()
	id := db.NewID("inv")
	var email, exp any
	if s.Email != "" {
		email = s.Email
	}
	if s.ExpiresAt != 0 {
		exp = s.ExpiresAt
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invites(id, token_hash, created_by, name, email, note, role, grants_json, expires_at,
		max_uses, use_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		id, hash[:], inviter.ID, s.Name, email, s.Note, string(s.Role), string(raw), exp, s.MaxUses, now, now); err != nil {
		return Invite{}, "", err
	}
	if err := db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: inviter.ID, Action: "invite.create", Target: "invite:" + id,
		Detail: map[string]any{"role": s.Role, "grants": len(s.Grants), "expires_at": s.ExpiresAt, "max_uses": s.MaxUses}}); err != nil {
		return Invite{}, "", err
	}
	inv, err := scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.id = ?`, id))
	return inv, tok, err
}

// Lookup finds the invite a link names, for showing it. A malformed, unknown or revoked token is
// ErrInviteUnknown; an invite whose inviter may no longer invite that role is ErrInviteRefused.
// An expired or used-up invite is returned with its State, so the page can say so.
func (r *Invites) Lookup(ctx context.Context, tok string) (Invite, error) {
	hash, ok := token.Hash(tok)
	if !ok {
		return Invite{}, ErrInviteUnknown
	}
	inv, err := scanInvite(r.st.Reader().QueryRowContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.token_hash = ?`, hash[:]))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && inv.RevokedAt != 0) {
		return Invite{}, ErrInviteUnknown
	}
	if err != nil {
		return Invite{}, err
	}
	inviter, err := r.users.ByID(ctx, inv.CreatedBy)
	if err != nil || !inviteRole(inv.Role) || !r.can.Can(ctx, inviter, ManageUsers, Target{Kind: TargetUser, Role: inv.Role}) {
		return Invite{}, ErrInviteRefused
	}
	return inv, nil
}

// Get reads an invite by id. ErrInviteUnknown when there is none.
func (r *Invites) Get(ctx context.Context, id string) (Invite, error) {
	inv, err := scanInvite(r.st.Reader().QueryRowContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrInviteUnknown
	}
	return inv, err
}

// get reads an invite by id inside tx.
func (r *Invites) get(ctx context.Context, tx *sql.Tx, id string) (Invite, error) {
	inv, err := scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrInviteUnknown
	}
	return inv, err
}

// Rotate gives a pending invite a new link and returns the new raw token; the old link stops
// working. ErrInviteUnknown when the invite is not pending, ErrInviteRefused when by may not
// manage its role.
func (r *Invites) Rotate(ctx context.Context, tx *sql.Tx, by users.User, id string) (string, error) {
	inv, err := r.get(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if _, err := r.mayInvite(ctx, tx, by.ID, inv.Role); err != nil {
		return "", err
	}
	now := r.now().UnixMilli()
	tok, hash := token.New()
	res, err := tx.ExecContext(ctx, `UPDATE invites SET token_hash = ?, mail_job_id = NULL, updated_at = ? WHERE id = ? AND `+pendingSQL, hash[:], now, id, now)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", ErrInviteUnknown
	}
	return tok, db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: by.ID, Action: "invite.rotate", Target: "invite:" + id})
}

// Revoke ends an invite. It is idempotent: an invite already revoked is left alone and writes no
// second audit row. ErrInviteRefused when by may not manage the invite's role.
func (r *Invites) Revoke(ctx context.Context, tx *sql.Tx, by users.User, id string) error {
	inv, err := r.get(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := r.mayInvite(ctx, tx, by.ID, inv.Role); err != nil {
		return err
	}
	now := r.now().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE invites SET revoked_at = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return err
	}
	return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: by.ID, Action: "invite.revoke", Target: "invite:" + id})
}

func roleMarks(roles []users.Role) (string, []any) {
	marks := make([]string, len(roles))
	args := make([]any, len(roles))
	for i, r := range roles {
		marks[i], args[i] = "?", string(r)
	}
	return strings.Join(marks, ","), args
}

// Pending lists the invites that can still be used at nowMs and whose role is in roles, oldest
// first. No roles, no invites.
func (r *Invites) Pending(ctx context.Context, nowMs int64, roles []users.Role) ([]Invite, error) {
	if len(roles) == 0 {
		return nil, nil
	}
	marks, args := roleMarks(roles)
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.role IN (`+marks+`)
		AND i.revoked_at IS NULL AND i.use_count < i.max_uses AND (i.expires_at IS NULL OR i.expires_at > ?)
		ORDER BY i.created_at, i.id`, append(args, nowMs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// PendingCovering lists the pending invites (as Pending) whose access list opens bookID: an "all"
// grant, the book's library, a shelf holding it, or the book itself. Admin invites carry no grants
// and never match. Two small queries find the book's library and shelves; the match is in Go.
func (r *Invites) PendingCovering(ctx context.Context, nowMs int64, roles []users.Role, bookID string) ([]Invite, error) {
	list, err := r.Pending(ctx, nowMs, roles)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	var lib string
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT library_id FROM books WHERE id = ?`, bookID).Scan(&lib); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT shelf_id FROM shelf_items WHERE book_id = ?`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shelves := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		shelves[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Invite
	for _, inv := range list {
		for _, g := range inv.Grants {
			if g.Scope == GrantAll || (g.Scope == GrantLibrary && g.Target == lib) ||
				(g.Scope == GrantShelf && shelves[g.Target]) || (g.Scope == GrantBook && g.Target == bookID) {
				out = append(out, inv)
				break
			}
		}
	}
	return out, nil
}

// WaitingInvites counts the invites that can still be used at nowMs and whose role is in roles.
// No roles, 0.
func WaitingInvites(ctx context.Context, st *db.Store, nowMs int64, roles []users.Role) (int, error) {
	if len(roles) == 0 {
		return 0, nil
	}
	marks, args := roleMarks(roles)
	var n int
	err := st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM invites WHERE role IN (`+marks+`) AND `+pendingSQL,
		append(args, nowMs)...).Scan(&n)
	return n, err
}

// Accept turns the invite named by hash into an account, inside tx: it re-checks the inviter and
// every target, claims one use, creates the person, writes the grants and one audit row. All or
// nothing: any error rolls the transaction back, so two parallel accepts of a one-use invite give
// exactly one account (the second is ErrInviteUsedUp). passwordHash "" creates the account without
// a password. Unknown and revoked links are ErrInviteUnknown; ErrInviteRefused and ErrInviteNoAccess
// mean the offer no longer stands.
func (r *Invites) Accept(ctx context.Context, tx *sql.Tx, hash [32]byte, passwordHash string) (users.User, Invite, error) {
	inv, err := scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.token_hash = ?`, hash[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return users.User{}, Invite{}, ErrInviteUnknown
	}
	if err != nil {
		return users.User{}, Invite{}, err
	}
	now := r.now().UnixMilli()
	switch inv.State(now) {
	case InviteRevoked:
		return users.User{}, Invite{}, ErrInviteUnknown
	case InviteExpired:
		return users.User{}, Invite{}, ErrInviteExpired
	case InviteUsedUp:
		return users.User{}, Invite{}, ErrInviteUsedUp
	}
	inviter, err := r.mayInvite(ctx, tx, inv.CreatedBy, inv.Role)
	if err != nil {
		return users.User{}, Invite{}, err
	}
	var kept []InviteGrant
	skipped := 0
	if inv.Role != users.RoleAdmin {
		for _, g := range inv.Grants {
			switch err := validateTarget(ctx, tx, g.Scope, g.Target); {
			case err == nil:
				kept = append(kept, g)
			case errors.Is(err, ErrGrantTarget), errors.Is(err, ErrGrantScope):
				skipped++
			default:
				return users.User{}, Invite{}, err
			}
		}
		if len(kept) == 0 {
			return users.User{}, Invite{}, ErrInviteNoAccess
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE invites SET use_count = use_count + 1, updated_at = ? WHERE id = ? AND `+pendingSQL, now, inv.ID, now)
	if err != nil {
		return users.User{}, Invite{}, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return users.User{}, Invite{}, ErrInviteUsedUp
	}
	u, err := r.users.CreateInvited(ctx, tx, users.NewUser{DisplayName: inv.Name, Email: inv.Email, Role: inv.Role}, passwordHash)
	if err != nil {
		return users.User{}, Invite{}, err
	}
	for _, g := range kept {
		if _, err := r.grants.Put(ctx, tx, inviter, GrantSpec{UserID: u.ID, Scope: g.Scope, TargetID: g.Target, CanDownload: g.Download, CanRead: g.Read}); err != nil {
			return users.User{}, Invite{}, err
		}
	}
	inv.UseCount++
	inv.UpdatedAt = now
	err = db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "invite.accept", Target: "invite:" + inv.ID,
		Detail: map[string]any{"invite": inv.ID, "user": u.ID, "role": u.Role, "grants": len(kept), "skipped": skipped}})
	return u, inv, err
}

// bookOK keeps refused and missing files out of what an invite shows or counts.
const bookOK = `b.refused_reason IS NULL AND b.missing_since IS NULL`

// grantBooksSQL is a query selecting the ids of the books one invite grant covers.
func grantBooksSQL(g InviteGrant) (string, []any) {
	switch g.Scope {
	case GrantAll:
		return `SELECT id FROM books`, nil
	case GrantLibrary:
		return `SELECT id FROM books WHERE library_id = ?`, []any{g.Target}
	case GrantShelf:
		return `SELECT si.book_id AS id FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id JOIN users o ON o.id = s.owner_id
			WHERE si.shelf_id = ? AND o.disabled = 0 AND o.role IN ('owner','admin')`, []any{g.Target}
	case GrantBook:
		return `SELECT id FROM books WHERE id = ?`, []any{g.Target}
	}
	return "", nil
}

// Featured picks the book an invite page shows and counts the other books it covers. The first
// grant that still covers a book decides: a book grant is that book, a shelf its first book, a
// library or everything its newest. Refused and missing files never count. The second value is the
// number of other books (0 or more).
func (r *Invites) Featured(ctx context.Context, inv Invite) (string, int, error) {
	var parts []string
	var args []any
	for _, g := range inv.Grants {
		q, a := grantBooksSQL(g)
		if q == "" {
			continue
		}
		parts = append(parts, q)
		args = append(args, a...)
	}
	if len(parts) == 0 {
		return "", 0, nil
	}
	var n int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(DISTINCT x.id) FROM (`+strings.Join(parts, " UNION ALL ")+`) x
		JOIN books b ON b.id = x.id WHERE `+bookOK, args...).Scan(&n); err != nil {
		return "", 0, err
	}
	if n == 0 {
		return "", 0, nil
	}
	for _, g := range inv.Grants {
		var q string
		var a []any
		switch g.Scope {
		case GrantAll:
			q = `SELECT b.id FROM books b WHERE ` + bookOK + ` ORDER BY b.added_at DESC, b.id LIMIT 1`
		case GrantLibrary:
			q, a = `SELECT b.id FROM books b WHERE b.library_id = ? AND `+bookOK+` ORDER BY b.added_at DESC, b.id LIMIT 1`, []any{g.Target}
		case GrantShelf:
			q, a = `SELECT b.id FROM shelf_items si JOIN shelves s ON s.id = si.shelf_id JOIN users o ON o.id = s.owner_id
				JOIN books b ON b.id = si.book_id WHERE si.shelf_id = ? AND o.disabled = 0 AND o.role IN ('owner','admin')
				AND `+bookOK+` ORDER BY si.idx, b.id LIMIT 1`, []any{g.Target}
		case GrantBook:
			q, a = `SELECT b.id FROM books b WHERE b.id = ? AND `+bookOK, []any{g.Target}
		default:
			continue
		}
		var id string
		err := r.st.Reader().QueryRowContext(ctx, q, a...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		return id, n - 1, nil
	}
	return "", 0, nil
}
