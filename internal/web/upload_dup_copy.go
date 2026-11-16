package web

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// Words of the "already in the library" warning on the Add books page (decision 0045, plan section 6).
// The sentences with {tokens} are filled by js/upload.js before anything is sent; the %s forms are the
// server's, for an upload held after its files arrived. The plan has no sentence for a held upload
// whose book the person can no longer list (uploadAlreadyMaybe, the service's own message), and no word
// for the book page notice's second sentence; both are listed in docs/copy-additions.md.
const (
	uploadAlreadySame = "This book is already in {library}: {title}."
	uploadAlreadySome = "Some of these files are already in {library}."
	uploadAlreadyLike = "This looks like {title}, already in {library}."
	uploadAlreadyAsk  = "Ask an admin if you need a second copy."
	uploadOpenIt      = "Open it"
	uploadAddAnyway   = "Add anyway"
	uploadDoNotAdd    = "Do not add"

	uploadAlreadyMaybe = "This book may already be in the library."
	// New: a match that is the person's own upload from a moment ago, not yet seen by the scan.
	uploadAlreadyRecent    = "You added this a moment ago. It will show up once the library scan has seen it."
	uploadAlreadyRecentFmt = uploadAlreadyRecent

	uploadAlreadySameFmt = "This book is already in %s: %s."
	uploadAlreadySomeFmt = "Some of these files are already in %s."
	uploadAlreadyLikeFmt = "This looks like %s, already in %s."

	// The notice on a book page when the scan found another copy with the same audio.
	bookDupNotice = "Another copy of this book is in the library."
	bookDupLink   = "Compare them"
)

// alreadyLine is the sentence of the warning for a match, in the server's own form.
func alreadyLine(m *upload.Match, libName string) string {
	var line string
	switch {
	case m.BookID == "":
		// The person's own upload that the scan has not listed yet: there is no book to name or open.
		line = uploadAlreadyRecentFmt
	case m.Tier == "same_files":
		line = fmt.Sprintf(uploadAlreadySameFmt, libName, m.Title)
	case m.Tier == "some_files":
		line = fmt.Sprintf(uploadAlreadySomeFmt, libName)
	default:
		line = fmt.Sprintf(uploadAlreadyLikeFmt, m.Title, libName)
	}
	if !m.MayAddAnyway {
		line += " " + uploadAlreadyAsk
	}
	return line
}

// alreadyRow turns an upload held as already_in_library into the "already" state. Without a match (the
// person can no longer list the book, so its title stays unsaid) the row says it may be there and offers
// only "Do not add".
func (s *server) alreadyRow(v *components.UploadRowView, u upload.View, libName string) {
	v.State = "already"
	if u.Match == nil {
		v.Line = uploadAlreadyMaybe
		return
	}
	v.Line = alreadyLine(u.Match, libName)
	v.OpenHref = u.Match.Href // empty for an upload the scan has not listed yet: no link, no bare /books/
	v.AddAnyway = u.Match.MayAddAnyway
	v.Listing = u.Match.BookID == ""
}

// uploadRowFor is uploadRow for a request: an "already" row also gets the CSRF token of its forms and,
// for a person who may edit the matched book, the link to its details.
func (s *server) uploadRowFor(r *http.Request, u upload.View, libName, folderPath string) components.UploadRowView {
	ctx := r.Context()
	v := s.uploadRow(ctx, u, libName, folderPath)
	if v.State != "already" {
		return v
	}
	v.CSRF = s.csrf.TokenFrom(ctx)
	if u.Match != nil && u.Match.BookID != "" && s.details != nil &&
		s.can.Can(ctx, currentUser(r), share.EditBook, share.Target{Kind: share.TargetBook, ID: u.Match.BookID, LibraryID: u.LibraryID}) {
		v.EditHref = detailsEditHref(u.Match.BookID)
	}
	return v
}

// sameAudioNotice is the line on a book page for the owner and admins when the scan found another copy of the
// book's audio in the same library (a folder copied by hand next to a book that was added by upload, or the
// other way round). The copy is one the person can list, so no title is named; the link opens the existing merge
// preview with the older book kept (decision 0045, D11). Empty for everyone else.
func (s *server) sameAudioNotice(r *http.Request, b library.Book) (notice, label, href string) {
	u := currentUser(r)
	if (u.Role != users.RoleOwner && u.Role != users.RoleAdmin) || b.Kind != library.BookAudio {
		return "", "", ""
	}
	ids, err := s.lib.SameAudioCopies(r.Context(), s.scope(r), b.ID)
	if err != nil || len(ids) == 0 {
		return "", "", ""
	}
	other, err := s.lib.Book(r.Context(), ids[0])
	if err != nil {
		return "", "", ""
	}
	keep, drop := other.ID, b.ID
	if b.AddedAt < other.AddedAt || (b.AddedAt == other.AddedAt && b.ID < other.ID) {
		keep, drop = b.ID, other.ID
	}
	return bookDupNotice, bookDupLink, "/admin/duplicates/merge?keep=" + url.QueryEscape(keep) + "&drop=" + url.QueryEscape(drop)
}
