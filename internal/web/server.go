// Package web serves Tendshelf's server-rendered pages, HTMX fragments and static assets.
//
// Pages are templ components (internal/web/pages) inside one layout (internal/web/components).
// Static files are embedded and content-hashed (internal/web/static). Unsafe requests pass
// through the CSRF guard in csrf.go before any handler runs.
package web

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/align"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/backups"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
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
	// Peaks makes and finds waveform peaks; nil means GET /peaks/{id} answers 404.
	Peaks *mediajobs.Peaks
	// Details runs online lookups and applies their results; nil means the M4 details routes answer 404.
	Details *metadata.Service
	// Covers adopts, chooses and uploads cover pictures; nil means the M4 cover routes answer 404.
	Covers *mediajobs.Covers
	// Writes writes details into the audio files and keeps the backups; nil means the M4 write
	// routes answer 404.
	Writes *mediajobs.Writes
	// MailOptions changes how the optional email relay dials (tests give it the fake server's
	// certificate); the zero value is production. The handler builds the mailer and registers its
	// job kinds on Jobs, so Jobs must not be started yet.
	MailOptions share.MailOptions
	// Backups makes and lists scheduled database copies; nil means the handler makes its own
	// (tests) and registers the job kind on Jobs.
	Backups *backups.Service
	// DownloadChunk is the size in bytes of one offline download request; zero means 4 MiB. Tests
	// shrink it so a small fixture spans several chunks.
	DownloadChunk int
	// Uploads adds books by upload into a library folder (decision 0015); nil means the upload
	// routes answer 404.
	Uploads *upload.Service
	// Ebooks holds the ebook files, editions and uploads of the Text tab (M5 R1); nil means the Text
	// tab and its routes answer 404.
	Ebooks *ebook.Service
	// Align starts, watches and ends read-along alignments (M5 R3); nil means the alignment routes answer 404.
	Align *align.Service
	// Runtimes is the word-timing runtime the /admin/runtimes page reports on and checks; nil means that
	// page answers 404.
	Runtimes *alignrt.Provider
}

type server struct {
	routes     http.Handler // the route mux itself, for answerPage to render a page a POST points at
	assets     *static.Set
	csrf       *CSRF
	auth       *auth.Service
	sessions   *auth.Sessions
	devices    *auth.Devices
	apiLimiter *auth.Limiter // per device id: every bearer request counts, see bearer
	users      *users.Repo
	can        *share.Checker
	grants     *share.Grants
	invites    *share.Invites
	links      *share.Links
	linkKey    []byte // signs the share-link unlock cookie; derived from the CSRF key, never the same bytes
	proxies    auth.Proxies
	lib        *library.Repo
	dataDir    string
	jobs       *jobs.Runner
	version    string
	now        func() time.Time // the clock; never nil
	compat     *mediajobs.Compat
	peaks      *mediajobs.Peaks
	details    *metadata.Service
	covers     *mediajobs.Covers
	writes     *mediajobs.Writes
	uploads    *upload.Service
	ebooks     *ebook.Service
	align      *align.Service
	runtimes   *alignrt.Provider
	backups    *backups.Service
	progress   *progress.Service
	mail       *share.Mail
	mailLim    mailLimits
	chunkBytes int    // size of one offline download request, written on <html data-ts-chunk>
	swBuild    string // the id of this build of the assets, see static.Set.Build
}

// mailLimits are the in-memory rate limits of the email relay. A restart resets them (only the owner
// can restart). Each Allow is followed by Fail, so a limiter counts one send.
type mailLimits struct {
	inviter, address, test *auth.Limiter
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
	dv := auth.NewDevices(opts.Store, opts.Now)
	checker := share.New(opts.Store)
	grants := share.NewGrants(opts.Store)
	invites := share.NewInvites(opts.Store, checker, grants, ur, opts.Now)
	mailer := share.NewMail(opts.Store, opts.Jobs, opts.MailOptions)
	if err := mailer.Register(); err != nil && !errors.Is(err, jobs.ErrDuplicateKind) { // tests build several handlers on one runner
		return nil, nil, fmt.Errorf("web: email jobs: %w", err)
	}
	bk := opts.Backups
	if bk == nil {
		bk = backups.New(opts.Store, opts.Jobs, opts.DataDir, opts.Now, nil)
		if err := bk.Register(); err != nil && !errors.Is(err, jobs.ErrDuplicateKind) {
			return nil, nil, fmt.Errorf("web: backup job: %w", err)
		}
	}
	lk := hmac.New(sha256.New, key)
	lk.Write([]byte("tendshelf share-link unlock v1"))
	s := &server{
		assets:     assets,
		auth:       auth.NewService(opts.Store, ur, ss, auth.ServiceOptions{SetupCode: opts.SetupCode, Now: opts.Now, Invites: invites, Devices: dv}),
		sessions:   ss,
		devices:    dv,
		apiLimiter: auth.NewLimiter(apiRateMax, apiRateWindow, apiRateKeys, opts.Now),
		users:      ur,
		can:        checker,
		grants:     grants,
		invites:    invites,
		links:      share.NewLinks(opts.Store, checker, opts.Now),
		linkKey:    lk.Sum(nil),
		proxies:    opts.TrustedProxies,
		lib:        opts.Library,
		dataDir:    opts.DataDir,
		jobs:       opts.Jobs,
		version:    opts.Version,
		now:        nowOrReal(opts.Now),
		compat:     opts.Compat,
		peaks:      opts.Peaks,
		details:    opts.Details,
		covers:     opts.Covers,
		writes:     opts.Writes,
		uploads:    opts.Uploads,
		ebooks:     opts.Ebooks,
		align:      opts.Align,
		runtimes:   opts.Runtimes,
		backups:    bk,
		progress:   progress.New(opts.Store, opts.Now),
		mail:       mailer,
		chunkBytes: cmp.Or(opts.DownloadChunk, defaultDownloadChunk),
		swBuild:    assets.Build(opts.Version),
		mailLim: mailLimits{
			inviter: auth.NewLimiter(20, time.Hour, 1024, opts.Now),
			address: auth.NewLimiter(3, 24*time.Hour, 1024, opts.Now),
			test:    auth.NewLimiter(5, 10*time.Minute, 64, opts.Now),
		},
	}
	s.csrf = NewCSRF(key, sessionOrCookieBinding)
	s.csrf.Deny = http.HandlerFunc(s.forbidden)
	s.csrf.Secure = s.proxies.Secure
	s.csrf.Exempt = func(r *http.Request) bool {
		if _, ok := auth.DeviceFrom(r.Context()); ok {
			return true
		}
		return r.Method == http.MethodPost && r.URL.Path == apiTokenPath
	}

	mux := http.NewServeMux()
	registerDev(mux, s)
	// register areas
	s.registerAuth(mux)
	s.registerCovers(mux)
	s.registerBrowse(mux)
	s.registerBook(mux)
	s.registerBookmarks(mux)
	s.registerAdmin(mux)
	s.registerUsers(mux)
	s.registerInvites(mux)
	s.registerShare(mux)
	s.registerLinks(mux)
	s.registerEmail(mux)
	s.registerRuntimes(mux)
	//
	s.registerSetupSteps(mux)
	s.registerMe(mux)
	s.registerShelves(mux)
	s.registerQueue(mux)
	s.registerDownloads(mux)
	s.registerListening(mux)
	s.registerBackups(mux)
	s.registerOffline(mux)
	s.registerStream(mux)
	s.registerPeaks(mux)
	s.registerProgress(mux)
	s.registerPlayer(mux)
	s.registerAPI(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.errorPage(w, r, http.StatusNotFound)
	})

	s.routes = mux
	guarded := s.refuseMixed(s.bearer(auth.Middleware(ss, s.proxies)(s.csrf.Middleware(s.gate(mux)))))
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
	s.registerInstall(outer)
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
	s.pageCached(w, r, status, "no-store", title, current, body, sheet, styles...)
}

// pageCached is pageStyled with the Cache-Control value given. Only the offline shell differs
// ("private, no-store"): the service worker keeps that one answer itself.
func (s *server) pageCached(w http.ResponseWriter, r *http.Request, status int, cacheControl, title, current string, body, sheet templ.Component, styles ...string) {
	var buf bytes.Buffer
	showUsers, usersWaiting := s.usersLink(r)
	theme, textSize, navRail, timeZone := s.lookFor(r)
	err := components.Layout(components.Page{
		Title:        title,
		Current:      current,
		Theme:        theme,
		TextSize:     textSize,
		NavRail:      navRail,
		TimeZone:     timeZone,
		CSRF:         s.csrf.TokenFrom(r.Context()),
		Assets:       s.assets,
		Sheet:        sheet,
		Styles:       styles,
		Admin:        s.can.Can(r.Context(), currentUser(r), share.ManageLibraries, share.Target{Kind: share.TargetServer}),
		Users:        showUsers,
		UsersWaiting: usersWaiting,
		Shelves:      s.sidebarShelves(r),
		Player:       s.withEyesFree(playerMsgs()),
		Person:       currentUser(r).ID,
		ChunkBytes:   s.chunkBytes,
		Offline:      offlineMsgs(),
	}, body).Render(r.Context(), &buf)
	if err != nil {
		slog.Error("render page", "path", logPath(r), "err", err)
		http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", cacheControl)
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
		slog.Error("render frame", "path", logPath(r), "err", err)
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
	if isAPIPath(r.URL.Path) {
		api.StatusError(w, status)
		return
	}
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
		slog.Error("render fragment", "path", logPath(r), "err", err)
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
	if isAPIPath(r.URL.Path) {
		api.Error(w, http.StatusForbidden, api.CodeForbidden, "This request was refused.")
		return
	}
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

// sheetStyles resolves stylesheet source names to the hashed addresses a sheet links inside itself.
func (s *server) sheetStyles(names ...string) []string {
	hrefs := make([]string, 0, len(names))
	for _, n := range names {
		hrefs = append(hrefs, s.assets.URL(n))
	}
	return hrefs
}
