// Command soundspike is the S0 spike of docs/plans/sound-modes.md: a throwaway test page the
// operator opens on real phones to learn whether Web Audio on the player's <audio> element survives
// a locked screen. It is not part of the server binary, serves one audio file the operator names,
// and writes what the page reports to .local/tests/soundspike/<run>/log.jsonl. S1 deletes it.
//
//	go run ./internal/testutil/soundspike -file <audio> [-addr 127.0.0.1:8096]
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/web"
)

//go:embed page.html spike.js spike.css manifest.webmanifest
var assets embed.FS

// maxLogBody caps one beacon; a real line is a few hundred bytes.
const maxLogBody = 8 << 10

func main() {
	file := flag.String("file", "", "the one audio file to serve (never committed)")
	addr := flag.String("addr", "127.0.0.1:8096", "listen address")
	out := flag.String("out", filepath.Join(".local", "tests", "soundspike"), "folder for run logs")
	flag.Parse()
	if *file == "" {
		log.Fatal("soundspike: -file <audio> is required")
	}
	if st, err := os.Stat(*file); err != nil || !st.Mode().IsRegular() {
		log.Fatalf("soundspike: %s is not a readable file", *file)
	}
	run := time.Now().UTC().Format("20060102-150405")
	dir := filepath.Join(*out, run)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	logPath := filepath.Join(dir, "log.jsonl")
	fmt.Printf("soundspike: serving %s on http://%s\nsoundspike: log %s\n", filepath.Base(*file), *addr, logPath)
	srv := &http.Server{Addr: *addr, Handler: newHandler(*file, logPath), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// newHandler is the whole site: four asset routes, two audio routes (the same file, so Range works),
// and the log beacon. Wrapped in the app's real security headers so the CSP is the app's.
func newHandler(file, logPath string) http.Handler {
	mux := http.NewServeMux()
	asset := func(name, contentType string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, err := assets.ReadFile(name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b)
		}
	}
	mux.HandleFunc("GET /{$}", asset("page.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /spike.js", asset("spike.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /spike.css", asset("spike.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /manifest.webmanifest", asset("manifest.webmanifest", "application/manifest+json"))
	serveAudio := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, file)
	}
	mux.HandleFunc("GET /audio/a", serveAudio)
	mux.HandleFunc("GET /audio/b", serveAudio)

	var mu sync.Mutex
	mux.HandleFunc("POST /log", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogBody))
		if err != nil || !json.Valid(body) {
			http.Error(w, "bad log line", http.StatusBadRequest)
			return
		}
		line, _ := json.Marshal(struct {
			Server int64           `json:"server"`
			Page   json.RawMessage `json:"page"`
		}{time.Now().UnixMilli(), body})
		mu.Lock()
		defer mu.Unlock()
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			http.Error(w, "cannot write", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		_, _ = f.Write(append(line, '\n'))
		w.WriteHeader(http.StatusNoContent)
	})
	return web.SecurityHeaders(mux)
}
