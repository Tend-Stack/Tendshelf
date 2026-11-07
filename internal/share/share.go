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
	AddBooks        Action = "books.add"        // upload books into a library folder (decisions 0015, 0029); owner and admin, and a contributor with an active whole-library or whole-server grant
	RemoveBook      Action = "book.remove"      // "Remove this book": owner and admin, and a contributor for their own books (decision 0029)
	WriteFiles      Action = "book.write_files" // "Write details into the file": owner and admin only (decision 0029)
	UseShelves      Action = "shelves.use"
	ViewShelf       Action = "shelf.view" // reading a shelf that is not one's own
)

var knownActions = map[Action]bool{
	ViewLibrary: true, ViewBook: true, ListenBook: true, DownloadBook: true, EditBook: true,
	ReadText: true, DownloadText: true, ManageLibraries: true, ManageJobs: true,
	ManageUsers: true, ManageServer: true, UseShelves: true, ViewShelf: true, AddBooks: true, RemoveBook: true, WriteFiles: true,
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

// AddedBySQL is true when the book aliased a lies at or inside the root of a book that one of the person's
// uploads published (upload_books, decision 0029). One positional parameter: the person's id. The alias is
// checked like a Filter alias. Ownership is always read from the database, never from a request.
func AddedBySQL(alias string) string {
	checkAlias(alias)
	return addedBySQL(alias)
}

func addedBySQL(a string) string {
	return strings.ReplaceAll(`EXISTS (SELECT 1 FROM upload_books share_ub WHERE share_ub.added_by = ? AND ($a.root_path = share_ub.root_path
	OR ($a.root_path > share_ub.root_path || '/' AND $a.root_path < share_ub.root_path || '0')))`, "$a", a)
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
		if u.Contributor() {
			if ok, decided := c.canContributor(ctx, u, a, t); decided {
				return ok
			}
		}
		return c.canByGrant(ctx, u, a, t)
	}
	return false
}

// canContributor decides what a contributor has beyond a member (decision 0029). decided is false when the
// member rules (grants) apply. Ownership of a book is read from upload_books by the book's id.
func (c *Checker) canContributor(ctx context.Context, u users.User, a Action, t Target) (ok, decided bool) {
	if c.st == nil {
		return false, false
	}
	switch {
	case a == AddBooks && t.Kind == TargetServer:
		return true, true
	case a == AddBooks && t.Kind == TargetLibrary:
		if t.ID == "" {
			return false, true
		}
		var one int
		err := c.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM grants share_g WHERE `+activeSQL+`
			AND (share_g.scope = 'all' OR (share_g.scope = 'library' AND share_g.library_id = ?)) LIMIT 1`,
			u.ID, c.now().UnixMilli(), t.ID).Scan(&one)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("share: grant lookup failed", "action", a, "err", err)
		}
		return err == nil, true
	case t.Kind == TargetBook && t.ID != "":
		switch a {
		case ViewBook, ListenBook, DownloadBook, ReadText, DownloadText, EditBook, RemoveBook:
		default:
			return false, false
		}
		var one int
		err := c.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM books b WHERE b.id = ? AND `+addedBySQL("b"), t.ID, u.ID).Scan(&one)
		if err == nil {
			return true, true
		}
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("share: ownership lookup failed", "action", a, "err", err)
			return false, true
		}
		return false, a == EditBook || a == RemoveBook
	}
	// ViewLibrary on a library holding an own book is answered by LibraryFilter, which canByGrant uses.
	return false, false
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
func (c *Checker) filter(u users.User, alias string, covers, own func(alias string) string) Filter {
	checkAlias(alias)
	if u.Disabled {
		return Filter{SQL: "0=1"}
	}
	switch u.Role {
	case users.RoleOwner, users.RoleAdmin:
		return Filter{SQL: "1=1"}
	case users.RoleMember, users.RoleGuest:
		f := Filter{
			SQL:  `EXISTS (SELECT 1 FROM grants share_g WHERE ` + activeSQL + ` AND ` + covers(alias) + `)`,
			Args: []any{u.ID, c.now().UnixMilli()},
		}
		if u.Contributor() {
			f.SQL = `(` + f.SQL + ` OR ` + own(alias) + `)`
			f.Args = append(f.Args, u.ID)
		}
		return f
	}
	return Filter{SQL: "0=1"}
}

// BookFilter limits a query on books (aliased alias) to the books u may see.
func (c *Checker) BookFilter(u users.User, alias string) Filter {
	return c.filter(u, alias, func(b string) string { return coversSQL("share_g", b) }, addedBySQL)
}

// LibraryFilter limits a query on libraries (aliased alias) to those u may see.
func (c *Checker) LibraryFilter(u users.User, alias string) Filter {
	return c.filter(u, alias, libraryCoversSQL, func(l string) string {
		return `EXISTS (SELECT 1 FROM books share_lb WHERE share_lb.library_id = ` + l + `.id AND ` + addedBySQL("share_lb") + `)`
	})
}

// BookFilterFor limits a query on books (aliased alias) to the books u may do a to, agreeing with Can for
// ViewBook, ReadText and DownloadText. Can takes the maximum of each right across all covering grants, so
// the filter needs one EXISTS per right instead of one grant that has them all. Any other action matches nothing.
func (c *Checker) BookFilterFor(u users.User, alias string, a Action) Filter {
	checkAlias(alias)
	var rights []string
	switch a {
	case ViewBook:
		rights = []string{"1=1"}
	case ReadText:
		rights = []string{"share_g.can_read = 1"}
	case DownloadText:
		rights = []string{"share_g.can_download = 1", "share_g.can_read = 1"}
	default:
		return Filter{SQL: "0=1"}
	}
	if u.Disabled {
		return Filter{SQL: "0=1"}
	}
	switch u.Role {
	case users.RoleOwner, users.RoleAdmin:
		return Filter{SQL: "1=1"}
	case users.RoleMember, users.RoleGuest:
	default:
		return Filter{SQL: "0=1"}
	}
	nowMs := c.now().UnixMilli()
	parts := make([]string, 0, 2)
	var args []any
	for _, r := range rights {
		parts = append(parts, `EXISTS (SELECT 1 FROM grants share_g WHERE `+activeSQL+` AND `+coversSQL("share_g", alias)+` AND `+r+`)`)
		args = append(args, u.ID, nowMs)
	}
	f := Filter{SQL: strings.Join(parts, " AND "), Args: args}
	if u.Contributor() {
		f.SQL = `(` + f.SQL + ` OR ` + addedBySQL(alias) + `)`
		f.Args = append(f.Args, u.ID)
	}
	return f
}
