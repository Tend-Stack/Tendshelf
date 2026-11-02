package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Pure builders for the Share & invite page: the handler gathers rows, these write the words.

// shareScopeInput is what the "What can they listen to?" cards are built from.
type shareScopeInput struct {
	Selected       string // all, library, shelf or book; empty for none
	Library, Shelf string // the chosen ids, kept after a form error
	ActorID        string
	Libraries      []library.LibrarySummary
	Shelves        []share.ShelfTarget
	BookTitle      string // the book on a book's own page
}

func shareBooksText(n int) string { return countText(n, shareOneBook, shareBooks) }

// shareScopeChoices writes the four cards. A card with nothing to choose from (no library, no
// shelf) is left out. One library or shelf is named in its card and sent as a hidden field;
// several become a select inside the card.
func shareScopeChoices(in shareScopeInput) []pages.ShareChoice {
	total := 0
	for _, l := range in.Libraries {
		total += l.Books
	}
	out := []pages.ShareChoice{{Value: "all", Title: shareEverything, Hint: shareBooksText(total), On: in.Selected == "all"}}
	if len(in.Libraries) > 0 {
		c := pages.ShareChoice{Value: "library", Title: shareOneLibrary, Hidden: "library", Selected: in.Library, On: in.Selected == "library"}
		for _, l := range in.Libraries {
			label := l.Name
			if len(in.Libraries) > 1 {
				label += " · " + shareBooksText(l.Books)
			}
			c.Options = append(c.Options, pages.Option{Value: l.ID, Label: label})
		}
		if len(in.Libraries) == 1 {
			c.Hint = in.Libraries[0].Name + " · " + shareBooksText(in.Libraries[0].Books)
		} else {
			c.Hint = shareWhichLib
		}
		out = append(out, c)
	}
	if len(in.Shelves) > 0 {
		c := pages.ShareChoice{Value: "shelf", Hidden: "shelf", Selected: in.Shelf, On: in.Selected == "shelf"}
		for _, sh := range in.Shelves {
			label := sh.Name + " · " + sh.OwnerName
			if len(in.Shelves) == 1 {
				label = sh.Name
			}
			c.Options = append(c.Options, pages.Option{Value: sh.ID, Label: label + " · " + shareBooksText(sh.Books)})
		}
		if len(in.Shelves) == 1 {
			sh := in.Shelves[0]
			c.Title = fmt.Sprintf(shareAShelfNamed, sh.Name)
			c.Hint = fmt.Sprintf(shareShelfBy, shareBooksText(sh.Books), sh.OwnerName)
			if sh.OwnerID == in.ActorID {
				c.Hint = fmt.Sprintf(shareShelfByYou, shareBooksText(sh.Books))
			}
		} else {
			c.Title, c.Hint = shareAShelf, shareWhichShelf
		}
		out = append(out, c)
	}
	book := pages.ShareChoice{Value: "book", Title: shareJustOneBook, Hint: shareFindBook, On: in.Selected == "book"}
	if in.BookTitle != "" {
		book.Hint = in.BookTitle
	}
	return append(out, book)
}

// shareRoleChoices writes the "What can they do?" cards for the roles the signed-in person may
// invite, Listener first.
func shareRoleChoices(actor users.User, selected string) []pages.ShareChoice {
	if selected == "" {
		selected = string(users.RoleGuest)
	}
	hints := map[users.Role]string{users.RoleGuest: shareListener, users.RoleMember: shareMemberHint, users.RoleAdmin: shareAdminHint}
	may := map[users.Role]bool{}
	for _, r := range share.ManageableInviteRoles(actor) {
		may[r] = true
	}
	var out []pages.ShareChoice
	for _, r := range []users.Role{users.RoleGuest, users.RoleMember, users.RoleAdmin} {
		if may[r] {
			out = append(out, pages.ShareChoice{Value: string(r), Title: roleWord(r), Hint: hints[r], On: string(r) == selected})
		}
	}
	return out
}

// shareExpiries are the choices for how long a link works, with the hours each adds (0 is none).
var shareExpiries = []struct {
	Value, Title string
	Hours        int
}{
	{"1d", shareDay1, 24}, {"7d", shareDay7, 7 * 24}, {"30d", shareDay30, 30 * 24}, {"never", shareNever, 0},
}

const shareExpiryDefault = "7d"

func shareExpiryChoices(selected string) []pages.ShareChoice {
	if selected == "" {
		selected = shareExpiryDefault
	}
	out := make([]pages.ShareChoice, 0, len(shareExpiries))
	for _, e := range shareExpiries {
		out = append(out, pages.ShareChoice{Value: e.Value, Title: e.Title, On: e.Value == selected})
	}
	return out
}

// shareExpiry turns the form's choice into an end time in Unix milliseconds (0 for none) and the
// words the link panel uses ("7 days"; empty for none). A missing choice is the default.
func shareExpiry(choice string, now time.Time) (ms int64, words string, ok bool) {
	if choice == "" {
		choice = shareExpiryDefault
	}
	for _, e := range shareExpiries {
		if e.Value == choice {
			if e.Hours == 0 {
				return 0, "", true
			}
			return now.Add(time.Duration(e.Hours) * time.Hour).UnixMilli(), e.Title, true
		}
	}
	return 0, "", false
}

// lowerFirst lower-cases the first letter ("Listening now" gives "listening now").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// shareListenerLine is "For Mamá · listening now": what they can listen to, then when they were last
// active.
func shareListenerLine(p share.Person, now time.Time) string {
	name := p.FirstName
	if p.Everything || p.FirstScope == share.GrantAll {
		name = shareEverything
	}
	if name == "" {
		name = shareEverything
	}
	if !p.Everything && p.ActiveGrants > 1 {
		name = fmt.Sprintf("%s and %d more", name, p.ActiveGrants-1)
	}
	text, _ := lastActiveText(now, p.LastActive, p.LastListen)
	return name + " · " + lowerFirst(text)
}

// sharePendingMeta is `Link to "Summer reads" · works for 5 more days`.
func sharePendingMeta(inv share.Invite, names map[string]string, now time.Time) string {
	var what string
	switch {
	case inv.Role == users.RoleAdmin || len(inv.Grants) == 0:
		what = shareLinkAdmin
	default:
		g := inv.Grants[0]
		name := names[string(g.Scope)+":"+g.Target]
		switch {
		case g.Scope == share.GrantAll:
			what = shareLinkAll
		case len(inv.Grants) > 1:
			what = fmt.Sprintf(shareLinkMore, name, len(inv.Grants)-1)
		default:
			what = fmt.Sprintf(shareLinkOne, name)
		}
	}
	switch {
	case inv.ExpiresAt == 0:
		return what + shareMetaNoEnd
	}
	left := inv.ExpiresAt - now.UnixMilli()
	days := int((left + 24*3_600_000 - 1) / (24 * 3_600_000))
	if days <= 1 {
		return what + shareMetaDay
	}
	return what + fmt.Sprintf(shareMetaDays, days)
}
