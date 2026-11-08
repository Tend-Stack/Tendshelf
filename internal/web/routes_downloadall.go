package web

import (
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// "Download all": a series or a shelf saved with one tap. The page script (js/downloads-all.js) asks the list route
// below, subtracts what the device already holds, confirms, then feeds the existing download queue one book at a
// time. These routes change nothing, sit behind the normal page session (a device token never reaches them) and are
// never cached by the service worker (it answers none of /series/ or /shelves/).

func (s *server) registerDownloadAll(mux *http.ServeMux) {
	mux.HandleFunc("GET /series/{id}/download-list", s.getSeriesDownloadList)
	mux.HandleFunc("GET /shelves/{id}/download-list", s.getShelfDownloadList)
}

// downloadListJSON is the body of the two list routes: the books a "Download all" would save, in page order, with
// the bytes of their audio files.
type downloadListJSON struct {
	Books      []downloadListBook `json:"books"`
	TotalBytes int64              `json:"totalBytes"`
}

type downloadListBook struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Bytes int64  `json:"bytes"`
}

func downloadListBody(items []library.DownloadItem) downloadListJSON {
	out := downloadListJSON{Books: make([]downloadListBook, len(items))}
	for i, it := range items {
		out.Books[i] = downloadListBook{ID: it.ID, Title: it.Title, Bytes: it.Bytes}
		out.TotalBytes += it.Bytes
	}
	return out
}

// downloadAllView is the "Download all" control of a page whose list is at listHref; nil when nothing in it can be saved.
func downloadAllView(listHref string, n int) *pages.DownloadAllView {
	if n == 0 {
		return nil
	}
	return &pages.DownloadAllView{ListHref: listHref, Messages: downloadAllMessages}
}

// getSeriesDownloadList lists a series' downloadable books for this person. The scope is the download right in SQL
// (the same as the podcast feed), so a person who may not download any book of the series gets the plain 404.
func (s *server) getSeriesDownloadList(w http.ResponseWriter, r *http.Request) {
	items, err := s.lib.SeriesDownloadItems(r.Context(), s.feedScope(currentUser(r)), r.PathValue("id"))
	if err != nil {
		s.serverError(w, r, "series download list", err)
		return
	}
	if len(items) == 0 {
		s.notFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, downloadListBody(items))
}

// getShelfDownloadList lists every downloadable book on a shelf the person may see, all of it in one query (the page
// shows 48 at a time). A shelf the person cannot see is the plain 404 of viewShelf.
func (s *server) getShelfDownloadList(w http.ResponseWriter, r *http.Request) {
	u, sh, _, ok := s.viewShelf(w, r)
	if !ok {
		return
	}
	items, err := s.lib.ShelfDownloadItems(r.Context(), s.feedScope(u), sh.OwnerID, sh.ID)
	if err != nil {
		s.serverError(w, r, "shelf download list", err)
		return
	}
	writeJSON(w, http.StatusOK, downloadListBody(items))
}
