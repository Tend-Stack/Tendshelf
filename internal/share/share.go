package share

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Action names one thing a person can try to do. Handlers pass an Action and a
// Target to Checker.Can and nothing else decides access.
type Action string

// The actions. Names are stable: they appear in docs and in the audit log.
const (
	ViewLibrary     Action = "library.view"
	ViewBook        Action = "book.view" // details and covers
	ListenBook      Action = "book.listen"
	DownloadBook    Action = "book.download"
	EditBook        Action = "book.edit"
	ReadText        Action = "text.read"
	DownloadText    Action = "text.download"
	ManageLibraries Action = "libraries.manage"
	ManageJobs      Action = "jobs.manage"
	ManageUsers     Action = "users.manage"
	ManageServer    Action = "server.manage"
	UseShelves      Action = "shelves.use"
	UseResearch     Action = "research.use"
	ViewShelf       Action = "shelf.view" // reading a shelf that is not one's own
)

var knownActions = map[Action]bool{
	ViewLibrary: true, ViewBook: true, ListenBook: true, DownloadBook: true, EditBook: true,
	ReadText: true, DownloadText: true, ManageLibraries: true, ManageJobs: true,
	ManageUsers: true, ManageServer: true, UseShelves: true, UseResearch: true, ViewShelf: true,
}

// TargetKind is what an action is aimed at.
type TargetKind string

// The kinds of target.
const (
	TargetServer  TargetKind = "server"
	TargetLibrary TargetKind = "library"
	TargetBook    TargetKind = "book"
	TargetShelf   TargetKind = "shelf"
	TargetUser    TargetKind = "user"
)

// Target says what an action is aimed at. LibraryID is the library a book
// belongs to (Can never trusts it for grants and reads the real one from the
// database); OwnerID is the person a shelf belongs to; Shared says a shelf is
// shared; Role is the role of the person a TargetUser stands for.
type Target struct {
	Kind      TargetKind
	ID        string
	LibraryID string
	OwnerID   string
	Role      users.Role
	Shared    bool
}

// Checker makes the authorization decision. Owners and admins pass by role;
// members and guests pass only through an active grant read from the store.
type Checker struct {
	st  *db.Store
	now func() time.Time
}

// New returns a Checker reading grants from st. A nil store denies members
// and guests everything.
func New(st *db.Store) *Checker { return &Checker{st: st, now: time.Now} }

// innerPrefix starts every SQL alias this package uses inside a fragment;
// callers may not use it for theirs.
const innerPrefix = "share_"

// activeSQL is true for a grant (aliased share_g) of the user that has not
// ended. Args: user id, now in Unix milliseconds. A grant ending exactly now is over.
const activeSQL = `share_g.user_id = ? AND (share_g.expires_at IS NULL OR share_g.expires_at > ?)`

// sharerSQL is true when the user aliased o may share: an enabled owner or
// admin. A shelf grant or a shared flag counts only while its owner passes.
func sharerSQL(o string) string {
	return o + ".disabled = 0 AND " + o + ".role IN ('owner','admin')"
}

// SharerSQL is sharerSQL for other packages (the shared-shelf flag is read in
// the library package's queries); the alias is checked like a Filter alias.
func SharerSQL(alias string) string {
	checkAlias(alias)
	return sharerSQL(alias)
}

// coversSQL is true when the grant aliased g gives access to the book aliased
// b. A shelf grant counts only while the shelf's owner is an enabled owner or admin.
func coversSQL(g, b string) string {
	return strings.NewReplacer("$g", g, "$b", b).Replace(`($g.scope = 'all'
	OR ($g.scope = 'library' AND $g.library_id = $b.library_id)
	OR ($g.scope = 'book' AND $g.book_id = $b.id)
	OR ($g.scope = 'shelf' AND EXISTS (SELECT 1 FROM shelf_items share_si
		JOIN shelves share_s ON share_s.id = share_si.shelf_id
		JOIN users share_o ON share_o.id = share_s.owner_id
		WHERE share_si.shelf_id = $g.shelf_id AND share_si.book_id = $b.id
		AND ` + sharerSQL("share_o") + `)))`)
}

// libraryCoversSQL is true when grant share_g gives access to at least one
// book of the library aliased a.
func libraryCoversSQL(a string) string {
	return strings.ReplaceAll(`(share_g.scope = 'all'
	OR (share_g.scope = 'library' AND share_g.library_id = $a.id)
	OR (share_g.scope = 'book' AND EXISTS (SELECT 1 FROM books share_b
		WHERE share_b.id = share_g.book_id AND share_b.library_id = $a.id))
	OR (share_g.scope = 'shelf' AND EXISTS (SELECT 1 FROM shelf_items share_si
		JOIN books share_b ON share_b.id = share_si.book_id
		JOIN shelves share_s ON share_s.id = share_si.shelf_id
		JOIN users share_o ON share_o.id = share_s.owner_id
		WHERE share_si.shelf_id = share_g.shelf_id AND share_b.library_id = $a.id
		AND `+sharerSQL("share_o")+`)))`, "$a", a)
}

// Can reports whether u may perform a on t. Any database error is a refusal.
func (c *Checker) Can(ctx context.Context, u users.User, a Action, t Target) bool {
	if u.Disabled || !knownActions[a] {
		return false
	}
	switch t.Kind {
	case TargetUser:
		if a != ManageUsers {
			return false
		}
		switch u.Role {
		case users.RoleOwner:
			return true
		case users.RoleAdmin:
			return t.Role == users.RoleMember || t.Role == users.RoleGuest
		}
		return false
	case TargetShelf:
		return canShelf(u, a, t)
	}
	switch u.Role {
	case users.RoleOwner:
		return true
	case users.RoleAdmin:
		return a != ManageServer
	case users.RoleMember, users.RoleGuest:
		return c.canByGrant(ctx, u, a, t)
	}
	return false
}

// canShelf decides actions on a shelf: it is its owner's; others may only look
// at it, and only when it is shared and they are not a guest.
func canShelf(u users.User, a Action, t Target) bool {
	if a == ViewShelf {
		switch u.Role {
		case users.RoleOwner, users.RoleAdmin, users.RoleMember:
			return t.OwnerID == u.ID || t.Shared
		}
		return false
	}
	if t.OwnerID != u.ID {
		return false
	}
	switch u.Role {
	case users.RoleOwner:
		return true
	case users.RoleAdmin:
		return a != ManageServer
	case users.RoleMember:
		return a == UseShelves
	}
	return false
}

// canByGrant decides for a member or guest from their active grants.
func (c *Checker) canByGrant(ctx context.Context, u users.User, a Action, t Target) bool {
	if c.st == nil || t.ID == "" {
		return false
	}
	switch {
	case t.Kind == TargetLibrary && a == ViewLibrary:
		f := c.LibraryFilter(u, "l")
		var one int
		err := c.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM libraries l WHERE l.id = ? AND `+f.SQL,
			append([]any{t.ID}, f.Args...)...).Scan(&one)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("share: grant lookup failed", "action", a, "err", err)
		}
		return err == nil
	case t.Kind == TargetBook:
		var n, download, read int
		// The book's library comes from the books table, never from the caller.
		err := c.st.Reader().QueryRowContext(ctx, `SELECT count(*), coalesce(max(share_g.can_download), 0), coalesce(max(share_g.can_read), 0)
			FROM books b JOIN grants share_g ON `+activeSQL+` AND `+coversSQL("share_g", "b")+` WHERE b.id = ?`,
			u.ID, c.now().UnixMilli(), t.ID).Scan(&n, &download, &read)
		if err != nil {
			slog.Error("share: grant lookup failed", "action", a, "err", err)
			return false
		}
		switch a {
		case ViewBook, ListenBook:
			return n > 0
		case DownloadBook:
			return n > 0 && download == 1
		case ReadText:
			return n > 0 && read == 1
		case DownloadText:
			return n > 0 && download == 1 && read == 1
		}
	}
	return false
}

// Filter is a SQL fragment for a listing or search query, so people never see
// titles they could not open. Args are positional parameters for SQL.
type Filter struct {
	SQL  string
	Args []any
}

var aliasRE = regexp.MustCompile(`^[a-z_]{1,16}$`)

func checkAlias(alias string) {
	if !aliasRE.MatchString(alias) || strings.HasPrefix(alias, innerPrefix) {
		panic("share: unsafe SQL alias " + alias)
	}
}

// filter builds the fragment: everything for an enabled owner or admin, the
// grant subquery built by covers for a member or guest, nothing otherwise.
func (c *Checker) filter(u users.User, alias string, covers func(alias string) string) Filter {
	checkAlias(alias)
	if u.Disabled {
		return Filter{SQL: "0=1"}
	}
	switch u.Role {
	case users.RoleOwner, users.RoleAdmin:
		return Filter{SQL: "1=1"}
	case users.RoleMember, users.RoleGuest:
		return Filter{
			SQL:  `EXISTS (SELECT 1 FROM grants share_g WHERE ` + activeSQL + ` AND ` + covers(alias) + `)`,
			Args: []any{u.ID, c.now().UnixMilli()},
		}
	}
	return Filter{SQL: "0=1"}
}

// BookFilter limits a query on books (aliased alias) to the books u may see.
func (c *Checker) BookFilter(u users.User, alias string) Filter {
	return c.filter(u, alias, func(b string) string { return coversSQL("share_g", b) })
}

// LibraryFilter limits a query on libraries (aliased alias) to those u may see.
func (c *Checker) LibraryFilter(u users.User, alias string) Filter {
	return c.filter(u, alias, libraryCoversSQL)
}
