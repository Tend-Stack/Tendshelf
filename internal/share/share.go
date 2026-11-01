package share

import (
	"context"
	"regexp"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Action names one thing a person can try to do. Handlers pass an Action and a
// Target to Checker.Can and nothing else decides access.
type Action string

// The actions. Names are stable: they appear in docs and, later, in grants.
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
)

var knownActions = map[Action]bool{
	ViewLibrary: true, ViewBook: true, ListenBook: true, DownloadBook: true, EditBook: true,
	ReadText: true, DownloadText: true, ManageLibraries: true, ManageJobs: true,
	ManageUsers: true, ManageServer: true, UseShelves: true, UseResearch: true,
}

// TargetKind is what an action is aimed at.
type TargetKind string

// The kinds of target.
const (
	TargetServer  TargetKind = "server"
	TargetLibrary TargetKind = "library"
	TargetBook    TargetKind = "book"
	TargetShelf   TargetKind = "shelf"
)

// Target says what an action is aimed at. LibraryID is the library a book
// belongs to; OwnerID is the person a shelf belongs to.
type Target struct {
	Kind      TargetKind
	ID        string
	LibraryID string
	OwnerID   string
}

// Checker makes the authorization decision.
type Checker struct{ st *db.Store }

// New returns a Checker. The store is where grants will be read from; until
// grants exist (P3) it is unused.
func New(st *db.Store) *Checker { return &Checker{st: st} }

// Can reports whether u may perform a on t. P3 replaces the member and guest
// branch with a grants subquery; callers do not change.
func (c *Checker) Can(_ context.Context, u users.User, a Action, t Target) bool {
	if u.Disabled || !knownActions[a] {
		return false
	}
	if t.Kind == TargetShelf && t.OwnerID != u.ID {
		return false // a shelf is its owner's, whatever the role
	}
	switch u.Role {
	case users.RoleOwner:
		return true
	case users.RoleAdmin:
		return a != ManageServer
	case users.RoleMember:
		return a == UseShelves && t.Kind == TargetShelf
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
	if !aliasRE.MatchString(alias) {
		panic("share: unsafe SQL alias " + alias)
	}
}

func (c *Checker) filter(u users.User, alias string) Filter {
	checkAlias(alias)
	if !u.Disabled && (u.Role == users.RoleOwner || u.Role == users.RoleAdmin) {
		return Filter{SQL: "1=1"}
	}
	return Filter{SQL: "0=1"}
}

// BookFilter limits a query on books (aliased alias) to the books u may see.
func (c *Checker) BookFilter(u users.User, alias string) Filter { return c.filter(u, alias) }

// LibraryFilter limits a query on libraries (aliased alias) to those u may see.
func (c *Checker) LibraryFilter(u users.User, alias string) Filter { return c.filter(u, alias) }
