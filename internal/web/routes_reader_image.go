package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// getReaderImage answers GET /books/{id}/read/images/{blockID}?edition=: the picture of one image block of an
// EPUB, read out of the original file through the bounded zip view. The address names a block of the stored
// document, never a path in the zip; the zip entry comes from the document. Only JPEG, PNG, GIF and WebP are
// sent (sniffed from the bytes); SVG and anything else is 404, so a scripted picture cannot reach the browser.
func (s *server) getReaderImage(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	c, bi, ok := rsBlockPos(rc.doc, r.PathValue("blockID"))
	if !ok {
		s.notFound(w, r)
		return
	}
	b := rc.doc.Chapters[c-1].Blocks[bi]
	if b.Kind != ebook.KindImg || b.Img == "" || rc.doc.Format != "epub" {
		s.notFound(w, r)
		return
	}
	data, kind, err := ebook.OpenImage(s.ebooks.FilePath(rc.file), b.Img, s.ebooks.Limits())
	if err != nil {
		if !errors.Is(err, ebook.ErrNoImage) {
			// A moved or unreadable file is a quiet 404 here; the page itself says what is wrong.
			slog.Debug("reader image", "err", err)
		}
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/"+kind)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
