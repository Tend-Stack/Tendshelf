package web

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// The original ebook file, as a download (decision 0018, research mistake 10: a reader must be able to take
// its own book away). The bytes are served as they are; nothing is written. Anything not allowed answers 404.

func (s *server) registerEbookDownload(mux *http.ServeMux) {
	mux.HandleFunc("GET /books/{id}/text/{fileID}/file", s.getEbookFile)
}

// mayDownloadText is the right to read the book's text and to download it. Both are needed: a person who may
// only read has no copy to take.
func (s *server) mayDownloadText(r *http.Request, book library.Book) bool {
	t := share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}
	u := currentUser(r)
	return s.can.Can(r.Context(), u, share.ReadText, t) && s.can.Can(r.Context(), u, share.DownloadBook, t)
}

func ebookFileHref(bookID, fileID string) string {
	return "/books/" + bookID + "/text/" + fileID + "/file"
}

// ebookMime is the content type of an ebook extension.
func ebookMime(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	case "mobi":
		return "application/x-mobipocket-ebook"
	case "azw3":
		return "application/vnd.amazon.ebook"
	case "fb2":
		return "application/x-fictionbook+xml"
	case "txt":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

func (s *server) getEbookFile(w http.ResponseWriter, r *http.Request) {
	if s.ebooks == nil {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.ViewBook)
	if !ok {
		return
	}
	if !s.mayDownloadText(r, book) {
		s.notFound(w, r)
		return
	}
	s.serveEbookFile(w, r, book, r.PathValue("fileID"), func() { s.notFound(w, r) })
}

// serveEbookFile sends one ready ebook file of the book, after the caller has decided the person may have it.
// A refused, failed, pending, missing or foreign file, or one that is not a plain regular file, calls miss.
// The OPDS route shares it (decision 0025) and passes a plain 404.
func (s *server) serveEbookFile(w http.ResponseWriter, r *http.Request, book library.Book, fileID string, miss func()) {
	if s.ebooks == nil {
		miss()
		return
	}
	f, found, err := s.ebooks.Store().File(r.Context(), fileID)
	if err != nil {
		s.serverError(w, r, "ebook file", err)
		return
	}
	// A ready file of this book only: a refused, failed, pending, missing or foreign file is never served.
	if !found || f.BookID != book.ID || f.State != "ready" || f.RefusedReason != "" || f.MissingSince != 0 {
		miss()
		return
	}
	path := s.ebooks.FilePath(f) // an upload is read from our own copy, a library file from its folder
	lfi, err := os.Lstat(path)
	if err != nil || !lfi.Mode().IsRegular() {
		miss()
		return
	}
	file, err := os.Open(path)
	if err != nil {
		miss()
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(lfi, fi) {
		miss()
		return
	}
	name := filepath.Base(path)
	if f.OriginalName != "" {
		name = filepath.Base(f.OriginalName)
	}
	h := w.Header()
	h.Set("Content-Type", ebookMime(f.Ext))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", fi.ModTime(), file)
}
