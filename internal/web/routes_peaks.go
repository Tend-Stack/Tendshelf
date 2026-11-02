package web

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// peaksRetryAfter is what a 404 for peaks that are being made tells the client, in seconds.
const peaksRetryAfter = "30"

func (s *server) registerPeaks(mux *http.ServeMux) {
	mux.HandleFunc("GET /peaks/{audioFileID}", s.getPeaks)
	// Without this the catch-all answers every other method with 404.
	mux.HandleFunc("/peaks/{audioFileID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
	})
}

// getPeaks serves the waveform peaks of one audio file: GET /peaks/{audioFileID}, the binary file
// media.Peaks.Encode wrote. It is authorised exactly like /stream: the person must be allowed to
// listen to the file's book, and an unknown file, a hidden book, a refused or copy-protected file
// and a service that is off all answer 404. Peaks that are not built yet answer 404 with
// Retry-After and make sure a job is on its way (queued at the front). The response never waits
// for the decoding.
func (s *server) getPeaks(w http.ResponseWriter, r *http.Request) {
	if !s.peaks.Enabled() {
		s.peaksNotFound(w, "")
		return
	}
	f, err := s.lib.AudioFile(r.Context(), r.PathValue("audioFileID"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load audio file", err)
		return
	}
	book, ok := s.loadBook(w, r, f.BookID, share.ListenBook)
	if !ok {
		return
	}
	if !streamable(book, f) {
		s.notFound(w, r)
		return
	}
	s.servePeaks(w, r, f, "private, no-cache")
}

// servePeaks sends the peaks of a file the caller has already authorized and checked with
// streamable, building them in the background when they are not there yet. It is shared by /peaks
// and /s/{token}/peaks.
func (s *server) servePeaks(w http.ResponseWriter, r *http.Request, f library.AudioFile, cacheControl string) {
	path, ready, err := s.peaks.Lookup(f)
	if err != nil {
		slog.Warn("peaks lookup", "file", f.ID, "err", err)
		s.peaksNotFound(w, "")
		return
	}
	if !ready {
		wait, err := s.peaks.Request(r.Context(), f)
		if err != nil {
			var me *media.Error
			if !errors.As(err, &me) {
				slog.Warn("peaks request", "file", f.ID, "err", err)
			}
			s.peaksNotFound(w, "")
			return
		}
		if wait {
			s.peaksNotFound(w, peaksRetryAfter)
			return
		}
		// Built between the lookup and the request.
		if path, ready, err = s.peaks.Lookup(f); err != nil || !ready {
			s.peaksNotFound(w, "")
			return
		}
	}
	if !within(mediajobs.PeaksDir(s.dataDir), path) {
		s.peaksNotFound(w, "")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("peaks: cannot open file", "file", f.ID, "err", err)
		}
		s.peaksNotFound(w, "")
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		s.peaksNotFound(w, "")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	// The cache name carries the quick hash and the format, so the tag changes with either.
	h.Set("ETag", `"pk`+strconv.Itoa(media.PeaksFormat)+`-`+f.QuickHash+`"`)
	h.Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, "", fi.ModTime(), file)
}

// peaksNotFound is the bare 404 of the peaks route. retryAfter, when set, says the peaks are on
// their way.
func (s *server) peaksNotFound(w http.ResponseWriter, retryAfter string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	http.Error(w, "Not found.", http.StatusNotFound)
}
