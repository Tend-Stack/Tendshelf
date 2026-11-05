package web

import (
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

func (s *server) registerDownloads(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/downloads", s.getDownloads)
}

// getDownloads is the Downloads page: the server draws the empty state and the DOM that
// js/downloads.js fills with the downloads kept on this device.
func (s *server) getDownloads(w http.ResponseWriter, r *http.Request) {
	s.pageStyled(w, r, http.StatusOK, downloadsTitle, "downloads", pages.Downloads(downloadsView()), nil, "css/pages-me.css")
}

// bookDownloadView is the Download pair of the book page: only for a person who may download the
// book (share.DownloadBook) and only when it has something to keep: a playable file or readable text (hasPlay,
// the play button's test or the right to read).
func (s *server) bookDownloadView(r *http.Request, book library.Book, hasPlay bool) *pages.BookDownloadView {
	target := share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}
	if !hasPlay || !s.can.Can(r.Context(), currentUser(r), share.DownloadBook, target) {
		return nil
	}
	return &pages.BookDownloadView{
		ID: book.ID, Label: bookDownload, Href: "/me/downloads",
		Downloading: "Downloading · {pct}%", Waiting: "Waiting",
		Ready: "Ready offline", Paused: "Paused", Failed: "Try again",
	}
}
