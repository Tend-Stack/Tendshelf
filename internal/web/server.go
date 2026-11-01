// Package web serves Tendshelf's server-rendered pages, HTMX fragments and static assets.
//
// Pages are templ components (internal/web/pages) inside one layout (internal/web/components).
// Static files are embedded and content-hashed (internal/web/static). Unsafe requests pass
// through the CSRF guard in csrf.go before any handler runs.
package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// Options configures the handler.
type Options struct {
	Version string
	// CSRFKey signs CSRF tokens. Nil means a random key per process (P1b passes a stored key).
	CSRFKey []byte
}

type server struct {
	assets *static.Set
	csrf   *CSRF
}

// NewHandler returns the root HTTP handler. It panics if the embedded static tree is invalid,
// which is a build defect, not a runtime condition.
func NewHandler(opts Options) http.Handler {
	assets, err := static.Load()
	if err != nil {
		panic("web: " + err.Error())
	}
	s := &server{assets: assets, csrf: NewCSRF(opts.CSRFKey)}
	s.csrf.Deny = http.HandlerFunc(s.forbidden)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", assets.Handler())
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		s.page(w, r, http.StatusOK, "Home", "home", pages.HomePage())
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": opts.Version,
		})
	})
	// P0a follow-up: mux.Handle("GET /hx/jobs/{id}", jobsFragment(jobs.Get)) — polls every 2s; not in P0b.
	registerDev(mux, s)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.errorPage(w, r, http.StatusNotFound)
	})
	return securityHeaders(s.csrf.Middleware(mux))
}

// page renders body inside the layout. Pages are per person and per theme, so never cached.
func (s *server) page(w http.ResponseWriter, r *http.Request, status int, title, current string, body templ.Component) {
	s.pageSheet(w, r, status, title, current, body, nil)
}

// pageSheet is page with a sheet (or nil) rendered into #sheet, outside #main.
func (s *server) pageSheet(w http.ResponseWriter, r *http.Request, status int, title, current string, body, sheet templ.Component) {
	var buf bytes.Buffer
	err := components.Layout(components.Page{
		Title:   title,
		Current: current,
		Theme:   themeFrom(r),
		CSRF:    s.csrf.TokenFrom(r.Context()),
		Assets:  s.assets,
		Sheet:   sheet,
	}, body).Render(r.Context(), &buf)
	if err != nil {
		slog.Error("render page", "path", r.URL.Path, "err", err)
		http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// errorPage renders the plain-words error page with the given status.
func (s *server) errorPage(w http.ResponseWriter, r *http.Request, status int) {
	s.page(w, r, status, pages.ErrorTitle(status), "", pages.ErrorPage(status))
}

// fragment renders an HTMX fragment (never a full page) with the given status. Fragments are per
// person, so never cached.
func (s *server) fragment(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		slog.Error("render fragment", "path", r.URL.Path, "err", err)
		http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// forbidden answers a refused unsafe request (bad or missing CSRF token, or cross-site origin).
// An htmx request gets the error toast fragment, which htmx 4 swaps even on a 4xx; anything else
// gets the full page.
func (s *server) forbidden(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, http.StatusForbidden, components.ErrorToast("This form expired. Reload the page and try again."))
		return
	}
	s.errorPage(w, r, http.StatusForbidden)
}

// contentSecurityPolicy matches docs/architecture.md ("Auth and sharing").
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; " +
	"script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
