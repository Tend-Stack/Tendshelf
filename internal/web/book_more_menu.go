package web

import (
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

const (
	moreSeries      = "Go to series"
	moreShare       = "Share"
	moreFeed        = "Podcast feed"
	moreEditDetails = "Edit details"
)

// bookMoreMenu is the three-dot menu of the book page. Each item is there only when the person may use it,
// by the same checks the pages behind it make: the series page, Share (managing users), the Podcast feed
// (withFeed: the download right and audio, decided by the caller), Change cover and Edit details (editing
// the book). It is built with the play region so the region re-read after a save keeps it.
func (s *server) bookMoreMenu(r *http.Request, book library.Book, seriesHref string, withFeed bool) components.MoreMenuView {
	var items []components.MoreMenuItem
	if seriesHref != "" {
		items = append(items, components.MoreMenuItem{Label: moreSeries, Href: seriesHref})
	}
	if s.canManageServerUsers(r) {
		items = append(items, components.MoreMenuItem{Label: moreShare, Href: "/books/" + book.ID + "/share"})
	}
	if withFeed {
		items = append(items, components.MoreMenuItem{Label: moreFeed, Href: "/books/" + book.ID + "/feed"})
	}
	target := share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}
	canEdit := s.can.Can(r.Context(), currentUser(r), share.EditBook, target)
	if canEdit && s.covers != nil {
		items = append(items, components.MoreMenuItem{Label: coverChangeLink, Href: coverSheetHref(book.ID, ""), Sheet: true})
	}
	if canEdit && s.details != nil {
		items = append(items, components.MoreMenuItem{Label: moreEditDetails, Href: detailsTabHref(book.ID)})
	}
	return components.MoreMenuView{Items: items}
}

// bookSeriesHref is the address of the book's first series page, or "" (for the play region read on its own).
func (s *server) bookSeriesHref(r *http.Request, id string) string {
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), id)
	if err != nil || len(d.Series) == 0 {
		return ""
	}
	return "/series/" + d.Series[0].ID
}
