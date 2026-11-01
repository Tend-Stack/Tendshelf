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
	browseNoMatchBody    = "Try a different genre, or clear the filters to see everything."
	browseNoMatchAction  = "Clear filters"
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

func browseEmpty(kind, clearHref string) *pages.EmptyView {
	switch kind {
	case "first-run":
		return &pages.EmptyView{Kind: kind, Title: browseEmptyShelfTitle, Body: browseEmptyShelfBody,
			ActionLabel: browseEmptyShelfAction, ActionHref: "/admin/libraries/folders/new"}
	case "nothing-shared":
		return &pages.EmptyView{Kind: kind, Title: browseNothingSharedHead, Body: browseNothingSharedBody}
	case "no-match":
		return &pages.EmptyView{Kind: kind, Title: browseNoMatchTitle, Body: browseNoMatchBody,
			ActionLabel: browseNoMatchAction, ActionHref: clearHref}
	}
	return nil
}
