package desktopshell

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/web"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// StateKind is what the window is doing about the person's choice.
type StateKind int

const (
	Idle   StateKind = iota // waiting for a choice
	Busy                    // starting the library or opening the server
	Failed                  // the last attempt failed; Words says why
)

// State is the Starter's progress. Words is set only when Kind is Failed.
type State struct {
	Kind  StateKind
	Words string
}

// Starter is what the desktop module provides: it acts on the person's choice. The start page
// never touches a window itself, which keeps this package free of cgo.
type Starter interface {
	// StartPersonal starts the library on this computer and opens it in the window.
	StartPersonal() error
	// Connect opens the already-checked server origin in the window.
	Connect(origin string) error
	State() State
}

// Options configures the start page.
type Options struct {
	Starter Starter
	Assets  *static.Set
}

// New returns the handler of the start page. The Wails asset handler serves it on wails://, the
// only place that origin is used (decision 0020). It is not a route of the web server.
//
//	GET  /               the picker (a saved mode is the caller's concern: it loads the page
//	                     only when the person has to choose, and with ?change=1 to choose again)
//	POST /start/personal this computer
//	POST /start/connect  a server (checked with CheckServer first)
//	GET  /static/        the same hashed files the web server sends
func New(o Options) http.Handler {
	s := &start{o: o, token: randomToken()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.picker)
	mux.HandleFunc("POST /start/personal", s.personal)
	mux.HandleFunc("POST /start/connect", s.connect)
	mux.Handle("GET /static/", o.Assets.Handler())
	return web.SecurityHeaders(mux)
}

type start struct {
	o     Options
	token string // random per process; forms carry it so a web page cannot post to the picker
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("desktopshell: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// tokenOK compares the form's token in constant time.
func (s *start) tokenOK(r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.PostFormValue("_token")), []byte(s.token)) == 1
}

func (s *start) picker(w http.ResponseWriter, r *http.Request) {
	st := s.o.Starter.State()
	v := pages.DesktopStartView{Token: s.token}
	switch st.Kind {
	case Busy:
		v.Busy = true
	case Failed:
		v.Error = st.Words
	}
	s.render(w, r, http.StatusOK, v)
}

func (s *start) personal(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.o.Starter.State().Kind == Busy {
		s.render(w, r, http.StatusOK, pages.DesktopStartView{Token: s.token, Busy: true})
		return
	}
	if err := s.o.Starter.StartPersonal(); err != nil {
		slog.Error("start personal mode", "err", err)
		s.render(w, r, http.StatusBadGateway, pages.DesktopStartView{Token: s.token, Error: Words(err)})
		return
	}
	s.render(w, r, http.StatusOK, pages.DesktopStartView{Token: s.token, Busy: true})
}

func (s *start) connect(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	typed := r.PostFormValue("server")
	v := pages.DesktopStartView{Token: s.token, Server: typed}
	if s.o.Starter.State().Kind == Busy {
		v.Busy = true
		s.render(w, r, http.StatusOK, v)
		return
	}
	origin, err := NormalizeServer(typed)
	if err != nil {
		v.Error = Words(err)
		s.render(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	v.Server = origin
	// A password sent over plain http outside the person's own network is readable by anyone on
	// the way. Ask once more before going on.
	if origin[:5] == "http:" && !IsPrivateHost(origin) && r.PostFormValue("confirm") != "1" {
		v.Insecure = true
		s.render(w, r, http.StatusOK, v)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := CheckServer(ctx, origin); err != nil {
		v.Error = Words(err)
		s.render(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if err := s.o.Starter.Connect(origin); err != nil {
		slog.Error("open server", "err", err)
		v.Error = Words(err)
		s.render(w, r, http.StatusBadGateway, v)
		return
	}
	s.render(w, r, http.StatusOK, pages.DesktopStartView{Token: s.token, Busy: true})
}

// render writes the page in the sign-in frame, marked as the desktop window (no install links, no
// offline script).
func (s *start) render(w http.ResponseWriter, r *http.Request, status int, v pages.DesktopStartView) {
	var buf bytes.Buffer
	err := components.Frame(components.Page{
		Title:     "Welcome",
		Theme:     themeFrom(r),
		Assets:    s.o.Assets,
		Styles:    pages.DesktopStartStyles,
		Shell:     "desktop",
		NoInstall: true,
		NoOffline: true,
	}, pages.DesktopStart(v)).Render(r.Context(), &buf)
	if err != nil {
		slog.Error("render the start page", "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// themeFrom reads the ts_theme cookie when the window kept one; otherwise the page follows the system.
func themeFrom(r *http.Request) string {
	ck, err := r.Cookie("ts_theme")
	if err != nil {
		return ""
	}
	switch ck.Value {
	case "dark", "light", "sepia", "contrast":
		return ck.Value
	}
	return ""
}
