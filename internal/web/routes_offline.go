package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// Offline listening, server side (decision 0012). Three things live here: the installable-app files
// (/sw.js and /manifest.webmanifest, public, on the outer mux beside /static/), and /offline, the page
// the service worker shows when the server cannot be reached. The worker's own source is
// static/js/sw.js; this file only prepends the build id and the asset list it needs.

// defaultDownloadChunk is the size of one offline download request: small enough to resume cheaply,
// large enough that a book is not thousands of requests.
const defaultDownloadChunk = 4 << 20

// maxOfflineBooks is how many ids /offline?books= accepts.
const maxOfflineBooks = 200

// offlineOthers is how many recent unfinished books that are not on the phone the shell lists.
const offlineOthers = 5

var offlineBookID = regexp.MustCompile(`^[0-9A-Za-z_-]{1,64}$`)

// registerInstall adds the public install routes to the outer mux, outside the sign-in gate and the
// CSRF guard: the browser fetches both without a session cookie when it checks for a new worker.
func (s *server) registerInstall(outer *http.ServeMux) {
	var src []byte
	if a, ok := s.assets.Asset("js/sw.js"); ok {
		src = a.Body
	}
	outer.Handle("/sw.js", noWorkerInApp(newServiceWorker(s.swBuild, s.assets.Precache(), src)))
	outer.Handle("/manifest.webmanifest", s.manifestHandler())
}

// noWorkerInApp answers 404 to /sw.js inside the Android app's window (decision 0026, D10). The page
// already leaves offline.js out there; this keeps a page that lost its script choice from
// registering a worker anyway. Browsers and the desktop shell are not changed.
func noWorkerInApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shellFrom(r) == "android" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serviceWorker is the /sw.js answer: the build and asset list, then the worker source. It is built
// once at start, with its gzip body.
type serviceWorker struct {
	body, gz   []byte
	etag, gzET string
}

// newServiceWorker joins the header line and the worker source. An empty src (the script is not
// embedded yet) makes the route answer 404.
func newServiceWorker(build string, assets []string, src []byte) http.Handler {
	if len(src) == 0 {
		return http.NotFoundHandler()
	}
	head, err := json.Marshal(struct {
		Build  string   `json:"build"`
		Assets []string `json:"assets"`
	}{build, assets})
	if err != nil { // strings only, cannot happen
		panic("web: service worker header: " + err.Error())
	}
	body := append(append([]byte("self.TS_BUILD = "), head...), []byte(";\n")...)
	body = append(body, src...)
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(body)
	_ = zw.Close()
	sw := &serviceWorker{body: body, etag: `"sw-` + build + `"`, gzET: `"sw-` + build + `-gz"`}
	if buf.Len() < len(body) {
		sw.gz = buf.Bytes()
	}
	return sw
}

func (sw *serviceWorker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	body, etag := sw.body, sw.etag
	h := w.Header()
	if sw.gz != nil {
		h.Set("Vary", "Accept-Encoding")
		if static.AcceptsGzip(r.Header.Get("Accept-Encoding")) {
			body, etag = sw.gz, sw.gzET
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	// no-cache: the browser always revalidates, so a new build is seen at the next check.
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && static.MatchesETag(match, etag) {
		h.Del("Content-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// manifestIcon is one icon of the web app manifest.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes,omitempty"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// manifestHandler serves /manifest.webmanifest. The document is the same for everyone; the icon
// addresses are the hashed ones. The colours are the dark theme's ground, the one the splash shows.
func (s *server) manifestHandler() http.Handler {
	ground := components.ThemeColor("dark")
	body, err := json.Marshal(struct {
		ID              string            `json:"id"`
		Name            string            `json:"name"`
		ShortName       string            `json:"short_name"`
		StartURL        string            `json:"start_url"`
		Scope           string            `json:"scope"`
		Display         string            `json:"display"`
		BackgroundColor string            `json:"background_color"`
		ThemeColor      string            `json:"theme_color"`
		LaunchHandler   map[string]string `json:"launch_handler"`
		Icons           []manifestIcon    `json:"icons"`
	}{
		ID: "/", Name: "Tendshelf", ShortName: "Tendshelf", StartURL: "/", Scope: "/", Display: "standalone",
		BackgroundColor: ground, ThemeColor: ground,
		LaunchHandler: map[string]string{"client_mode": "focus-existing"},
		Icons: []manifestIcon{
			{Src: s.assets.URL("img/icon-192.png"), Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: s.assets.URL("img/icon-512.png"), Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: s.assets.URL("img/icon-maskable-512.png"), Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
			{Src: s.assets.URL("img/tendshelf-app-icon.svg"), Sizes: "any", Type: "image/svg+xml", Purpose: "any"},
		},
	})
	if err != nil { // strings only, cannot happen
		panic("web: manifest: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/manifest+json")
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}

func (s *server) registerOffline(mux *http.ServeMux) {
	mux.HandleFunc("GET /offline", s.getOffline)
	s.registerOfflineRead(mux)
	// Without this the catch-all answers every other method with 404.
	mux.HandleFunc("/offline", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		s.plainError(w, r, http.StatusMethodNotAllowed, "Method not allowed.")
	})
}

// offlineIDs reads ?books=a,b,c: up to 200 book ids in the usual id alphabet. ok is false for a
// bad id, an empty item or too many.
func offlineIDs(raw string) (ids []string, ok bool) {
	if raw == "" {
		return nil, true
	}
	ids = strings.Split(raw, ",")
	if len(ids) > maxOfflineBooks {
		return nil, false
	}
	for _, id := range ids {
		if !offlineBookID.MatchString(id) {
			return nil, false
		}
	}
	return ids, true
}

// getOffline is the page the worker shows when it cannot reach the server. ?books= names the books the
// phone holds; each one is checked again here, so an id the person may not play, a refused or missing
// book and an unknown id are left out without a word. The worker keeps the answer in the person's cache
// and the page re-seeds it whenever the set changes.
func (s *server) getOffline(w http.ResponseWriter, r *http.Request) {
	ids, ok := offlineIDs(r.URL.Query().Get("books"))
	if !ok {
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	textIDs, ok := offlineIDs(r.URL.Query().Get("text"))
	if !ok {
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	v, err := s.offlineView(r, ids, textIDs)
	if err != nil {
		s.serverError(w, r, "offline shell", err)
		return
	}
	// pages-home.css holds the shell's own rules (.ts-offline-home), which the shell never linked before.
	s.pageCached(w, r, http.StatusOK, "private, no-store", offlineTitle, "home", pages.Offline(v), nil, "css/pages-home.css")
}

// offlineView builds the shell: the ready books first, then up to five recent unfinished ones that are
// not on the phone, each group by last listened.
func (s *server) offlineView(r *http.Request, ids, textIDs []string) (pages.OfflineView, error) {
	ctx, u := r.Context(), currentUser(r)
	v := pages.OfflineView{
		Greeting: browseGreetingFor(u.DisplayName), Caption: offlineCaption, Section: offlineSection,
		Note: offlineNote, EmptyTitle: offlineEmpty, EmptyBody: offlineEmptyBy,
	}
	// A book that has text on the phone is listed too, so its card is looked up with the rest.
	cards, err := s.lib.CardsByID(ctx, s.scope(r), slices.Concat(ids, textIDs))
	if err != nil {
		return v, err
	}
	type ready struct {
		row pages.OfflineRow
		at  int64
	}
	var have []ready
	for _, c := range cards {
		book, err := s.lib.Book(ctx, c.ID)
		if err != nil {
			slog.Warn("offline shell: load book", "book", c.ID, "err", err)
			continue
		}
		files, p, can, err := s.bookPlayData(r, book)
		if err != nil {
			return v, err
		}
		row := pages.OfflineRow{Card: cardView(c), Ready: true}
		at := int64(0)
		if can {
			if link := playLinkIn(book, files, p); link != nil { // else no file this server will stream
				row.Play = link.button(fmt.Sprintf(offlinePlayAria, c.Title), true)
				row.Meta, err = s.offlineMeta(r, book, p)
				if err != nil {
					return v, err
				}
				if p != nil {
					at = p.ClientUpdatedAt
				}
			}
		}
		// The text on the phone: a Read button for a book the person may read, beside the play button if there is one.
		if slices.Contains(textIDs, c.ID) {
			mayRead, err := s.mayReadBook(ctx, u, book)
			if err != nil {
				return v, err
			}
			if mayRead {
				rp, err := s.readingOf(ctx, u.ID, book.ID)
				if err != nil {
					return v, err
				}
				row.Read = &components.ReadButtonView{Href: readHref(book.ID), Label: readOther, AriaLabel: fmt.Sprintf(offlineReadAria, c.Title), Secondary: true}
				if row.Play == nil {
					row.Meta = offlineReady
					if rp != nil && !rp.Finished() && rp.PercentBP > 0 {
						row.Meta = fmt.Sprintf(offlineReadyPct, rp.PercentBP/100)
					}
					if rp != nil {
						at = rp.ClientUpdatedAt
					}
				}
			}
		}
		if row.Play == nil && row.Read == nil {
			continue
		}
		have = append(have, ready{row, at})
	}
	slices.SortStableFunc(have, func(a, b ready) int { return int(b.at - a.at) })
	for _, h := range have {
		v.Rows = append(v.Rows, h.row)
	}
	recent, err := s.lib.ContinueListening(ctx, s.scope(r), u.ID, offlineOthers+len(ids))
	if err != nil {
		return v, err
	}
	held := map[string]bool{}
	for _, id := range slices.Concat(ids, textIDs) {
		held[id] = true
	}
	n := 0
	for _, l := range recent {
		if held[l.Book.ID] || n == offlineOthers {
			continue
		}
		n++
		v.Rows = append(v.Rows, pages.OfflineRow{Card: cardView(l.Book), Meta: offlineNeedsNet})
	}
	return v, nil
}

// offlineMeta is the second line of a ready row: the chapter the person is in, else the percent
// listened for a book without chapters, else just "Ready offline" (not started, or finished).
func (s *server) offlineMeta(r *http.Request, book library.Book, p *progress.Progress) (string, error) {
	if p == nil || p.Finished() || p.PositionMs <= 0 {
		return offlineReady, nil
	}
	chs, err := s.lib.Chapters(r.Context(), book.ID)
	if err != nil {
		return "", err
	}
	if cur := currentChapter(chs, p.PositionMs); cur >= 0 {
		return fmt.Sprintf(offlineReadyChap, cur+1), nil
	}
	dur := book.DurationMs
	if dur <= 0 {
		dur = p.DurationMs
	}
	if dur <= 0 {
		return offlineReady, nil
	}
	pct := int(math.Round(float64(min(p.PositionMs, dur)) * 100 / float64(dur)))
	return fmt.Sprintf(offlineReadyPct, pct), nil
}
