package waveform

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// Store finds a book's saved peaks file. Adapt to however Tendshelf stores derived data.
type Store struct {
	Dir string // e.g. <data dir>/peaks; files are named <bookID>.bin
}

func (s Store) path(bookID string) string { return filepath.Join(s.Dir, filepath.Base(bookID)+".bin") }

// Save writes a book's peaks atomically.
func (s Store) Save(bookID string, peaks []byte) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp := s.path(bookID) + ".tmp"
	if err := os.WriteFile(tmp, peaks, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(bookID))
}

// Handler serves GET /books/{id}/peaks?from=<seconds>&to=<seconds>.
// 200: application/octet-stream, one byte per 1/Rate second.
// 202: not computed yet; the client shows flat bars and retries after Retry-After.
func (s Store) Handler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	from, err1 := strconv.ParseFloat(r.URL.Query().Get("from"), 64)
	to, err2 := strconv.ParseFloat(r.URL.Query().Get("to"), 64)
	if id == "" || err1 != nil || err2 != nil || to <= from {
		http.Error(w, "need id, from and to in seconds", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(s.path(id))
	if os.IsNotExist(err) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err != nil {
		http.Error(w, "peaks unavailable", http.StatusInternalServerError)
		return
	}
	etag := `"` + strconv.FormatInt(info.ModTime().Unix(), 36) + "-" + r.URL.RawQuery + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	peaks, err := os.ReadFile(s.path(id))
	if err != nil {
		http.Error(w, "peaks unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Peaks-Rate", strconv.Itoa(Rate))
	_, _ = w.Write(Slice(peaks, from, to))
}
