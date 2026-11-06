package web

import (
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// OPDS catalogue for reading apps (decision 0025). GET only; every list is filtered by grants in SQL
// (share.BookFilterFor with DownloadText), so a person sees exactly the books they may download as text.
// Audio is not listed. Nothing here sets or reads a cookie.

func (s *server) registerOPDS(outer *http.ServeMux) {
	m := http.NewServeMux()
	m.HandleFunc("GET /opds", s.getOPDSRoot)
	m.HandleFunc("GET /opds/{$}", s.getOPDSRoot)
	m.HandleFunc("GET /opds/all", s.getOPDSAll)
	m.HandleFunc("GET /opds/new", s.getOPDSNew)
	m.HandleFunc("GET /opds/search", s.getOPDSSearch)
	m.HandleFunc("GET /opds/search.xml", s.getOPDSSearchDescription)
	m.HandleFunc("GET /opds/books/{id}/file/{fileID}", s.getOPDSFile)
	m.HandleFunc("GET /opds/covers/{id}", s.getOPDSCover)
	guarded := s.opdsAuth(m)
	outer.Handle("/opds", guarded)
	outer.Handle("/opds/", guarded)
}

// opdsScope is the scope of a key's owner: what they may view and download as text.
func (s *server) opdsScope(r *http.Request) library.Scope {
	u := currentUser(r)
	return library.Scope{Books: s.can.BookFilterFor(u, "b", share.DownloadText), Libraries: s.can.LibraryFilter(u, "l")}
}

func writeOPDS(w http.ResponseWriter, status int, contentType string, v any) {
	body, err := xml.MarshalIndent(v, "", " ")
	if err != nil {
		keyText(w, http.StatusInternalServerError, "Something went wrong on our side.")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

func (s *server) getOPDSRoot(w http.ResponseWriter, r *http.Request) {
	writeOPDS(w, http.StatusOK, opdsNav, opdsNavFeed(s.now()))
}

// opdsPage reads ?page=, 1-based; anything else is page 1.
func opdsPage(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 || n > 100000 {
		return 1
	}
	return n
}

func (s *server) opdsList(w http.ResponseWriter, r *http.Request, id, title, base string, q library.CatalogueQuery) {
	page := opdsPage(r)
	q.Offset, q.Limit = (page-1)*opdsPageSize, opdsPageSize
	books, total, err := s.lib.TextCatalogue(r.Context(), s.opdsScope(r), q)
	if err != nil {
		s.opdsFail(w, "opds catalogue", err)
		return
	}
	writeOPDS(w, http.StatusOK, opdsAcq, opdsBooksFeed(id, title, base, page, total, books, s.now()))
}

func (s *server) getOPDSAll(w http.ResponseWriter, r *http.Request) {
	s.opdsList(w, r, "urn:tendshelf:opds:all", "All books", "/opds/all", library.CatalogueQuery{Sort: "title"})
}

func (s *server) getOPDSNew(w http.ResponseWriter, r *http.Request) {
	s.opdsList(w, r, "urn:tendshelf:opds:new", "Newest", "/opds/new", library.CatalogueQuery{Sort: "new"})
}

func (s *server) getOPDSSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	terms := library.SearchTerms(q)
	base := "/opds/search?q=" + queryEscape(q)
	if len(terms) == 0 {
		writeOPDS(w, http.StatusOK, opdsAcq, opdsBooksFeed("urn:tendshelf:opds:search", "Search", base, 1, 0, nil, s.now()))
		return
	}
	s.opdsList(w, r, "urn:tendshelf:opds:search", "Search", base, library.CatalogueQuery{Terms: terms})
}

func (s *server) getOPDSSearchDescription(w http.ResponseWriter, r *http.Request) {
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	writeOPDS(w, http.StatusOK, "application/opensearchdescription+xml; charset=utf-8", openSearchDescription{
		NS: openSearchNS, ShortName: "Tendshelf", Description: "Search your books",
		URL: openSearchURL{Type: opdsAcq, Template: scheme + r.Host + "/opds/search?q={searchTerms}"},
	})
}

// opdsBook loads a book the key's owner may download as text, or answers a plain 404 and reports false.
func (s *server) opdsBook(w http.ResponseWriter, r *http.Request) (library.Book, bool) {
	book, err := s.lib.Book(r.Context(), r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		keyText(w, http.StatusNotFound, "Not found.")
		return library.Book{}, false
	}
	if err != nil {
		s.opdsFail(w, "opds book", err)
		return library.Book{}, false
	}
	if !s.mayDownloadText(r, book) {
		keyText(w, http.StatusNotFound, "Not found.")
		return library.Book{}, false
	}
	return book, true
}

func (s *server) getOPDSFile(w http.ResponseWriter, r *http.Request) {
	book, ok := s.opdsBook(w, r)
	if !ok {
		return
	}
	s.serveEbookFile(w, r, book, r.PathValue("fileID"), func() { keyText(w, http.StatusNotFound, "Not found.") })
}

func (s *server) getOPDSCover(w http.ResponseWriter, r *http.Request) {
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCoverWidth {
			keyText(w, http.StatusBadRequest, "Bad width.")
			return
		}
		width = n
	}
	book, ok := s.opdsBook(w, r)
	if !ok {
		return
	}
	s.serveCoverFile(w, r, book.CoverID, width, "private, no-cache")
}

// opdsFail logs a server-side failure and answers plain words.
func (s *server) opdsFail(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	keyText(w, http.StatusInternalServerError, "Something went wrong on our side. Try again in a moment.")
}

func queryEscape(q string) string { return url.QueryEscape(q) }
