// Package web serves Tendshelf's server-rendered pages, HTMX fragments and static assets.
//
// Pages are templ components (internal/web/pages) inside one layout (internal/web/components).
// Static files are embedded and content-hashed (internal/web/static). Unsafe requests pass
// through the CSRF guard in csrf.go before any handler runs.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// Options configures the handler.
type Options struct {
	Version string
	// Store is the database; required. Accounts, sessions and the CSRF key live in it.
	Store *db.Store
	// CSRFKey signs CSRF tokens. Nil means the key kept in the settings table (key web.csrf_key),
	// created on first start, so forms survive a restart.
	CSRFKey []byte
	// SetupCode is the first-run code the owner types on /setup; required.
	SetupCode string
	// TrustedProxies are the reverse proxies whose forwarded headers are believed.
	TrustedProxies auth.Proxies
	// Hash sets the password hashing cost. The zero value is users.DefaultHash.
	Hash users.HashParams
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Library is the library repository; required.
	Library *library.Repo
	// DataDir is the data directory (covers and thumbnails live under it); required.
	DataDir string
	// Jobs is the job runner (scans and job status); required.
	Jobs *jobs.Runner
	// P1b-2..: add fields below this line.

	// Compat makes and finds compatibility copies; nil means ?compat=1 answers 404.
	Compat *mediajobs.Compat
}

type server struct {
	assets   *static.Set
	csrf     *CSRF
	auth     *auth.Service
	sessions *auth.Sessions
	users    *users.Repo
	can      *share.Checker
	proxies  auth.Proxies
	lib      *library.Repo
	dataDir  string
	jobs     *jobs.Runner
	version  string
	compat   *mediajobs.Compat
}

// settingCSRFKey is the settings row that keeps the CSRF key between runs.
const settingCSRFKey = "web.csrf_key"

// csrfKey returns the key from the options, or the stored one, creating it on first start.
func csrfKey(ctx context.Context, o Options) ([]byte, error) {
	if o.CSRFKey != nil {
		return o.CSRFKey, nil
	}
	var enc string
	ok, err := db.GetSetting(ctx, o.Store.Reader(), settingCSRFKey, &enc)
	if err != nil {
		return nil, err
	}
	if ok {
		if key, err := base64.RawURLEncoding.DecodeString(enc); err == nil && len(key) == 32 {
			return key, nil
		}
		// A damaged value is replaced: the only cost is that open forms expire.
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := db.PutSetting(ctx, o.Store.Writer(), settingCSRFKey, base64.RawURLEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}

// NewHandler returns the root HTTP handler. Everything except /static/, /healthz, /setup and /login
// needs a signed-in person (see gate.go). It returns an error for a missing store or setup code, or
// when the CSRF key can not be loaded; it panics if the embedded static tree is invalid, which is a
// build defect, not a runtime condition.
func NewHandler(ctx context.Context, opts Options) (http.Handler, error) {
	_, h, err := newServer(ctx, opts)
	return h, err
}

// newServer is NewHandler with the *server returned too, so tests can reach it.
func newServer(ctx context.Context, opts Options) (*server, http.Handler, error) {
	switch {
	case opts.Store == nil:
		return nil, nil, errors.New("web: Options.Store is required")
	case opts.SetupCode == "":
		return nil, nil, errors.New("web: Options.SetupCode is required")
	case opts.Library == nil:
		return nil, nil, errors.New("web: Options.Library is required")
	case opts.DataDir == "":
		return nil, nil, errors.New("web: Options.DataDir is required")
	case opts.Jobs == nil:
		return nil, nil, errors.New("web: Options.Jobs is required")
	}
	assets, err := static.Load()
	if err != nil {
		panic("web: " + err.Error())
	}
	key, err := csrfKey(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("web: CSRF key: %w", err)
	}
	ur := users.New(opts.Store, users.Options{Hash: opts.Hash, Now: opts.Now})
	ss := auth.NewSessions(opts.Store, opts.Now)
	s := &server{
		assets:   assets,
		auth:     auth.NewService(opts.Store, ur, ss, auth.ServiceOptions{SetupCode: opts.SetupCode, Now: opts.Now}),
		sessions: ss,
		users:    ur,
		can:      share.New(opts.Store),
		proxies:  opts.TrustedProxies,
		lib:      opts.Library,
		dataDir:  opts.DataDir,
		jobs:     opts.Jobs,
		version:  opts.Version,
		compat:   opts.Compat,
	}
	s.csrf = NewCSRF(key, sessionOrCookieBinding)
	s.csrf.Deny = http.HandlerFunc(s.forbidden)
	s.csrf.Secure = s.proxies.Secure

	mux := http.NewServeMux()
	registerDev(mux, s)
	// register areas
	s.registerAuth(mux)
	s.registerCovers(mux)
	s.registerBrowse(mux)
	s.registerBook(mux)
	//
	s.registerAdmin(mux)
	//
	s.registerSetupSteps(mux)
	s.registerMe(mux)
	s.registerShelves(mux)
	s.registerStream(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.errorPage(w, r, http.StatusNotFound)
	})

	guarded := auth.Middleware(ss, s.proxies)(s.csrf.Middleware(s.gate(mux)))
	outer := http.NewServeMux()
	outer.Handle("GET /static/", assets.Handler())
	outer.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": opts.Version,
		})
	})
	outer.Handle("/", guarded)
	return s, securityHeaders(outer), nil
}

// sessionOrCookieBinding binds a CSRF token to the signed-in session, or to the ts_csrf cookie when
// nobody is signed in. A token for one can never pass as the other (the values carry a prefix).
func sessionOrCookieBinding(r *http.Request) (string, bool) {
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		return "s:" + hex.EncodeToString(sess.Hash[:]), true
	}
	return cookieBinding(r)
}

// page renders body inside the layout. Pages are per person and per theme, so never cached.
func (s *server) page(w http.ResponseWriter, r *http.Request, status int, title, current string, body templ.Component) {
	s.pageSheet(w, r, status, title, current, body, nil)
}

// pageSheet is page with a sheet (or nil) rendered into #sheet, outside #main.
func (s *server) pageSheet(w http.ResponseWriter, r *http.Request, status int, title, current string, body, sheet templ.Component) {
	s.pageStyled(w, r, status, title, current, body, sheet)
}

// pageStyled is pageSheet with extra stylesheets (static paths) linked in the head. The shell shows
// the "Your server" group to people who may manage libraries.
func (s *server) pageStyled(w http.ResponseWriter, r *http.Request, status int, title, current string, body, sheet templ.Component, styles ...string) {
	var buf bytes.Buffer
	err := components.Layout(components.Page{
		Title:   title,
		Current: current,
		Theme:   s.themeFor(r),
		CSRF:    s.csrf.TokenFrom(r.Context()),
		Assets:  s.assets,
		Sheet:   sheet,
		Styles:  styles,
		Admin:   s.can.Can(r.Context(), currentUser(r), share.ManageLibraries, share.Target{Kind: share.TargetServer}),
		Shelves: s.sidebarShelves(r),
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

// frame renders body in the sign-in frame (no navigation, no player). Pages are per person, so
// never cached.
func (s *server) frame(w http.ResponseWriter, r *http.Request, status int, title string, body templ.Component, styles ...string) {
	var buf bytes.Buffer
	err := components.Frame(components.Page{
		Title:  title,
		Theme:  s.themeFor(r),
		CSRF:   s.csrf.TokenFrom(r.Context()),
		Assets: s.assets,
		Styles: styles,
	}, body).Render(r.Context(), &buf)
	if err != nil {
		slog.Error("render frame", "path", r.URL.Path, "err", err)
		http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// errorPage renders the plain-words error page with the given status: inside the app shell for a
// signed-in person, inside the plain frame for anyone else.
func (s *server) errorPage(w http.ResponseWriter, r *http.Request, status int) {
	if _, ok := auth.UserFrom(r.Context()); !ok {
		s.frame(w, r, status, pages.ErrorTitle(status), pages.ErrorPage(status))
		return
	}
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
