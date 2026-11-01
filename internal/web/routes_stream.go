package web

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// openAudio opens a library audio file or a compatibility copy. It is a variable so tests can count
// the file system calls and prove that a refused request never reaches the disk.
var openAudio = os.Open

// compatWaitText is the body of the 503 that answers ?compat=1 while the copy is being made.
const compatWaitText = "Getting this book ready for your browser."

func (s *server) registerStream(mux *http.ServeMux) {
	mux.HandleFunc("GET /stream/{audioFileID}", s.getStream)
	// Without this the catch-all answers every other method with 404.
	mux.HandleFunc("/stream/{audioFileID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
	})
}

// getStream serves one audio file with byte ranges: GET /stream/{audioFileID}, or with ?compat=1 the
// browser-friendly copy of it. The person must be allowed to listen to the file's book before any
// file is opened; an unknown file, an unknown or hidden book and a refused file all answer the same
// 404. The path comes only from the database row. Library files are only read.
func (s *server) getStream(w http.ResponseWriter, r *http.Request) {
	compat := false
	if q := r.URL.Query(); q.Has("compat") {
		if q.Get("compat") != "1" {
			http.Error(w, "Bad compat value.", http.StatusBadRequest)
			return
		}
		compat = true
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
	if book.RefusedReason != "" || mediajobs.Refusal(&f) != nil || media.Classify(f.Path) == media.KindProtected ||
		!(f.Path == book.RootPath || within(book.RootPath, f.Path)) {
		s.notFound(w, r)
		return
	}
	if compat {
		s.streamCompat(w, r, f)
		return
	}
	// The scanner never records symlinks, but a file can be swapped for one after a scan, and a
	// link may point anywhere on the server. Refuse anything that is not a regular file before
	// opening, then make sure the opened file is the one checked (no swap in between).
	lfi, err := os.Lstat(f.Path)
	if err != nil {
		s.streamOpenFailed(w, r, f.ID, err)
		return
	}
	if !lfi.Mode().IsRegular() {
		s.notFound(w, r)
		return
	}
	file, err := openAudio(f.Path)
	if err != nil {
		s.streamOpenFailed(w, r, f.ID, err)
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(lfi, fi) {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", audioContentType(f.Container, f.Path))
	if streamHashOK(f.QuickHash) && fi.Size() == f.Size && fi.ModTime().UnixNano() == f.MtimeNs {
		h.Set("ETag", `"`+f.QuickHash+`"`)
	} else {
		// The file changed since the scan: a tag that cannot match an old copy of its bytes.
		h.Set("ETag", `"m-`+strconv.FormatInt(fi.Size(), 10)+"-"+strconv.FormatInt(fi.ModTime().UnixNano(), 10)+`"`)
	}
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", fi.ModTime(), file)
}

// streamCompat answers ?compat=1: a ready copy is served; otherwise one job is requested and the
// answer is 503 with Retry-After. It never waits for the conversion.
func (s *server) streamCompat(w http.ResponseWriter, r *http.Request, f library.AudioFile) {
	if s.compat == nil {
		s.notFound(w, r)
		return
	}
	ctx := r.Context()
	p, ready, err := s.compat.Lookup(ctx, f.ID)
	if err != nil {
		slog.Warn("compat copy lookup", "file", f.ID, "err", err)
		s.notFound(w, r)
		return
	}
	if ready {
		if !within(mediajobs.CompatDir(s.dataDir), p) {
			s.notFound(w, r)
			return
		}
		file, err := openAudio(p)
		if err != nil {
			s.streamOpenFailed(w, r, f.ID, err)
			return
		}
		defer file.Close()
		fi, err := file.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			s.notFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "audio/mp4")
		h.Set("ETag", `"`+f.QuickHash+"-c"+strconv.Itoa(media.CompatRecipe)+`"`)
		h.Set("Cache-Control", "private, no-cache")
		http.ServeContent(w, r, "", fi.ModTime(), file)
		return
	}
	if _, err := s.compat.Request(ctx, f.ID); err != nil {
		var me *media.Error
		if errors.As(err, &me) || errors.Is(err, library.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, "request compat copy", err)
		return
	}
	h := w.Header()
	h.Set("Retry-After", "3")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(compatWaitText))
	}
}

// streamOpenFailed logs why a file could not be opened and answers 404, never 500: a file that
// vanished is an ordinary state between scans.
func (s *server) streamOpenFailed(w http.ResponseWriter, r *http.Request, id string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		slog.Debug("stream: file is not on disk", "file", id)
	} else {
		slog.Warn("stream: cannot open file", "file", id, "err", err)
	}
	s.notFound(w, r)
}

// streamHashOK reports whether a quick hash is safe to put in an ETag.
func streamHashOK(h string) bool {
	if h == "" {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// audioContentType picks the type by container, then by file extension.
func audioContentType(container, path string) string {
	if t := audioTypes[strings.ToLower(container)]; t != "" {
		return t
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		if t := audioTypes[strings.ToLower(path[i+1:])]; t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

var audioTypes = map[string]string{
	"mp4": "audio/mp4", "m4a": "audio/mp4", "m4b": "audio/mp4",
	"mp3":  "audio/mpeg",
	"flac": "audio/flac",
	"ogg":  "audio/ogg", "opus": "audio/ogg",
	"wav":  "audio/wav",
	"aac":  "audio/aac",
	"webm": "audio/webm",
}
