package web

import (
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Copy for Home and the Library (P1b-5). Strings from the copy deck and the design boards are used
// word for word; the rest follow the voice rules in BRAND.md and are listed in the package report.
// Fixed labels that no logic touches ("New on the shelf", "See all", "Filter and sort", "Show
// books", "Show more") sit in the templates beside their markup.
const (
	// Board: HomePhone ("Welcome back, Maya").
	browseGreeting = "Welcome back"
	// Copy deck: empty states.
	browseEmptyShelfTitle   = "Your shelf is empty"
	browseEmptyShelfBody    = "Point Tendshelf at a folder of audiobooks and they'll appear here."
	browseEmptyShelfAction  = "Add a folder"
	browseNothingSharedHead = "Nothing shared yet"
	browseNothingSharedBody = "When someone shares a book with you, it shows up here."
	// New.
	browseNoMatchTitle   = "No books match these filters"
	browseNoMatchBody    = "Try different words, or clear the filters to see everything."
	browseLibraryTitle   = "Library"
	browseBadCursorToast = "This list changed. Reload the page to see it."
)

// browseGreetingFor is "Welcome back, Ana": the first word of the display name, or just "Welcome
// back" when the person has no name.
func browseGreetingFor(displayName string) string {
	if f := strings.Fields(displayName); len(f) > 0 {
		return browseGreeting + ", " + f[0]
	}
	return browseGreeting
}

// browseCount is the line under the Library title.
func browseCount(n int) string {
	if n == 1 {
		return "1 book"
	}
	return thousands(n) + " books"
}

var browseSortLabels = []struct {
	Sort  library.Sort
	Label string
}{
	{library.SortAdded, "Recently added"},
	{library.SortTitle, "Title"},
	{library.SortAuthor, "Author"},
	{library.SortDuration, "Length"},
}

// browseSortLabel is the sort chip's text.
func browseSortLabel(s library.Sort) string {
	for _, o := range browseSortLabels {
		if o.Sort == s {
			return o.Label
		}
	}
	return "Recently added"
}

// browseHeaderLine is the line under the Library title, from the board: "912 books · 3 folders ·
// 8,240 hours". Only a person who manages libraries sees the folders.
func browseHeaderLine(t library.Totals, folders bool) string {
	parts := []string{browseCount(t.Books)}
	if folders {
		if t.Folders == 1 {
			parts = append(parts, "1 folder")
		} else {
			parts = append(parts, thousands(t.Folders)+" folders")
		}
	}
	h := int((t.DurationMs + 1_800_000) / 3_600_000)
	if h == 1 {
		parts = append(parts, "1 hour")
	} else {
		parts = append(parts, thousands(h)+" hours")
	}
	return strings.Join(parts, " · ")
}

// browseMatch is the count of a filtered list, from the board: "16 books match".
func browseMatch(n int) string {
	if n == 1 {
		return "1 book matches"
	}
	return thousands(n) + " books match"
}

// browseLengthOptions are the length buckets in the order the menu lists them.
var browseLengthOptions = []struct{ Value, Label string }{
	{"short", "Under 5 hours"}, {"medium", "5 to 15 hours"}, {"long", "Over 15 hours"},
}

func browseEmpty(kind, clearHref string) *pages.EmptyView {
	switch kind {
	case "first-run":
		return &pages.EmptyView{Kind: kind, Title: browseEmptyShelfTitle, Body: browseEmptyShelfBody,
			ActionLabel: browseEmptyShelfAction, ActionHref: "/admin/libraries/folders/new"}
	case "nothing-shared":
		return &pages.EmptyView{Kind: kind, Title: browseNothingSharedHead, Body: browseNothingSharedBody}
	case "no-match":
		// "Clear filters" sits right above, in the match line, so the empty state does not repeat it.
		return &pages.EmptyView{Kind: kind, Title: browseNoMatchTitle, Body: browseNoMatchBody}
	}
	return nil
}
