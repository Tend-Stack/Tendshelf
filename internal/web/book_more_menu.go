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
	moreRemoveText  = "Remove the text book"
	moreRemoveAudio = "Remove the audiobook only"
	moreRemoveWhole = "Remove the whole book"
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
		items = append(items, components.MoreMenuItem{Label: moreEditDetails, Href: detailsEditHref(book.ID)})
	}
	items = append(items, s.removeMenuItems(r, book, target)...)
	return components.MoreMenuView{Items: items}
}

// removeMenuItems are the quick removal actions of the menu. They open the same confirm page as the buttons on the
// book page (GET /books/{id}/remove with a part), and only for what is removable by the same rule.
func (s *server) removeMenuItems(r *http.Request, book library.Book, target share.Target) []components.MoreMenuItem {
	if s.uploads == nil || !s.can.Can(r.Context(), currentUser(r), share.RemoveBook, target) {
		return nil
	}
	parts, err := s.uploads.PartsOf(r.Context(), book.ID, s.removePlacedBy(currentUser(r)))
	if err != nil {
		return nil
	}
	base := "/books/" + book.ID + "/remove"
	var items []components.MoreMenuItem
	if parts.Combined && parts.AnyText() {
		href, n := base+"?part=text", 0
		for _, f := range parts.Text {
			if f.Removable {
				n++
				href = base + "?file=" + f.ID
			}
		}
		if n > 1 {
			href = base + "?part=text"
		}
		items = append(items, components.MoreMenuItem{Label: moreRemoveText, Href: href})
	}
	if parts.Combined && parts.Audio {
		items = append(items, components.MoreMenuItem{Label: moreRemoveAudio, Href: base + "?part=audio"})
	}
	if parts.Whole {
		items = append(items, components.MoreMenuItem{Label: moreRemoveWhole, Href: base + "?part=whole"})
	}
	if !parts.Whole && !(parts.Combined && (parts.Audio || parts.AnyText())) &&
		s.can.Can(r.Context(), currentUser(r), share.RemoveLibraryFiles, target) {
		// A book put in the library folder by hand: the owner and admins remove it after a warning (decision 0045).
		items = append(items, components.MoreMenuItem{Label: bookRemoveLabel, Href: base})
	}
	return items
}

// bookSeriesHref is the address of the book's first series page, or "" (for the play region read on its own).
func (s *server) bookSeriesHref(r *http.Request, id string) string {
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), id)
	if err != nil || len(d.Series) == 0 {
		return ""
	}
	return "/series/" + d.Series[0].ID
}
