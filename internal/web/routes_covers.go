package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// openCover opens a cover or thumbnail file. It is a variable so tests can count the file system
// calls and prove that a refused request never reaches the disk.
var openCover = os.Open

// maxCoverWidth bounds the w query value.
const maxCoverWidth = 4096

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *server) registerCovers(mux *http.ServeMux) {
	mux.HandleFunc("GET /covers/{bookID}", s.getCover)
	// Without this the catch-all answers every other method with 404.
	mux.HandleFunc("/covers/{bookID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		s.plainError(w, r, http.StatusMethodNotAllowed, "Method not allowed.")
	})
}

// coverCandidate is one file that may answer a cover request.
type coverCandidate struct {
	path, mime, tag string
}

// getCover serves a book's cover: GET /covers/{bookID}?w=<px>&v=<sha[:8]>. The person must be allowed
// to view the book before any file is touched; a refusal and an absent cover answer the same 404.
func (s *server) getCover(w http.ResponseWriter, r *http.Request) {
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCoverWidth {
			s.plainError(w, r, http.StatusBadRequest, "Bad width.")
			return
		}
		width = n
	}
	book, ok := s.loadBook(w, r, r.PathValue("bookID"), share.ViewBook)
	if !ok {
		return
	}
	s.serveCoverFile(w, r, book.CoverID, width, "")
}

// serveCoverFile writes the cover image coverID (a width of 0 means the original) after the caller
// has decided the person may see it. An empty id, a missing row and a missing file all answer the
// same 404. A non-empty cacheControl replaces the default caching headers.
func (s *server) serveCoverFile(w http.ResponseWriter, r *http.Request, coverID string, width int, cacheControl string) {
	if coverID == "" {
		s.plainError(w, r, http.StatusNotFound, "Not found.")
		return
	}
	cover, err := s.lib.Cover(r.Context(), coverID)
	if errors.Is(err, library.ErrNotFound) {
		s.plainError(w, r, http.StatusNotFound, "Not found.")
		return
	}
	if err != nil {
		s.serverError(w, r, "load cover", err)
		return
	}
	if !sha256Hex.MatchString(cover.SHA256) {
		s.plainError(w, r, http.StatusNotFound, "Not found.")
		return
	}
	for _, c := range s.coverCandidates(cover, width) {
		f, err := openCover(c.path)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			f.Close()
			continue
		}
		h := w.Header()
		h.Set("Content-Type", c.mime)
		h.Set("ETag", `"`+cover.SHA256+"-"+c.tag+`"`)
		if cacheControl != "" {
			h.Set("Cache-Control", cacheControl)
		} else if r.URL.Query().Get("v") == cover.SHA256[:8] {
			h.Set("Cache-Control", "private, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "private, no-cache")
		}
		http.ServeContent(w, r, "", time.Time{}, f)
		f.Close()
		return
	}
	s.plainError(w, r, http.StatusNotFound, "Not found.")
}

// coverCandidates lists the files that may answer, best first: for a width, the smallest thumbnail
// box that covers it, then each larger box, then the original; with no width, the original. Every
// path is checked to sit under the covers or thumbnails directory.
func (s *server) coverCandidates(c library.Cover, width int) []coverCandidate {
	var out []coverCandidate
	if width > 0 {
		thumbs := mediajobs.ThumbsDir(s.dataDir)
		for _, box := range mediajobs.ThumbSizes {
			if box < width {
				continue
			}
			p := mediajobs.ThumbPath(s.dataDir, c.SHA256, box)
			if within(thumbs, p) {
				out = append(out, coverCandidate{p, "image/jpeg", strconv.Itoa(box)})
			}
		}
	}
	orig := filepath.Join(s.dataDir, c.Path)
	if within(mediajobs.CoversDir(s.dataDir), orig) {
		out = append(out, coverCandidate{orig, c.Mime, "orig"})
	}
	return out
}

// within reports whether path lies strictly inside dir, lexically (dir itself is not inside).
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}
