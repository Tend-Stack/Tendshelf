package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// setupNextURL is where a new owner lands after /setup: the next setup step.
const setupNextURL = "/setup/server"

// formLimit bounds the body of the sign-in and setup forms.
const formLimit = 16 << 10

func (s *server) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", s.getSetup)
	mux.HandleFunc("POST /setup", s.postSetup)
	mux.HandleFunc("GET /login", s.getLogin)
	mux.HandleFunc("POST /login", s.postLogin)
	mux.HandleFunc("POST /logout", s.postLogout)
}

// parseForm reads a small urlencoded form. A body that is too large or malformed is a 400.
func (s *server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, formLimit)
	if err := r.ParseForm(); err != nil {
		s.errorPage(w, r, http.StatusBadRequest)
		return false
	}
	return true
}

func (s *server) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	slog.Error(what, "err", err)
	s.errorPage(w, r, http.StatusInternalServerError)
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, token string) {
	auth.SetCookie(w, token, s.proxies.Secure(r))
}

// tooMany answers a rate-limited attempt: 429 with Retry-After (whole seconds, rounded up).
func tooMany(w http.ResponseWriter, rl *auth.RateLimitedError) string {
	secs := int((rl.RetryAfter + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	return limitedMessage(rl.RetryAfter)
}

func (s *server) getSetup(w http.ResponseWriter, r *http.Request) {
	exists, err := s.auth.OwnerExists(r.Context())
	if err != nil {
		s.serverError(w, r, "owner check", err)
		return
	}
	if exists {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if len(code) > 32 {
		code = ""
	}
	s.frame(w, r, http.StatusOK, "Create your account", pages.SetupAccount(pages.SetupView{
		CSRF: s.csrf.TokenFrom(r.Context()), Code: code,
	}))
}

func (s *server) postSetup(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	exists, err := s.auth.OwnerExists(r.Context())
	if err != nil {
		s.serverError(w, r, "owner check", err)
		return
	}
	if exists {
		s.setupTaken(w, r)
		return
	}
	nu := users.NewUser{
		Username:    r.PostFormValue("username"),
		DisplayName: r.PostFormValue("display_name"),
		Email:       r.PostFormValue("email"),
		Password:    r.PostFormValue("password"),
	}
	code := r.PostFormValue("code")
	l, err := s.auth.Setup(r.Context(), code, nu, s.proxies.ClientIP(r), r.UserAgent())

	// The view never carries the password.
	nn := users.Normalize(nu)
	view := pages.SetupView{
		CSRF: s.csrf.TokenFrom(r.Context()), Code: strings.TrimSpace(code),
		DisplayName: nn.DisplayName, Email: nn.Email, Username: nn.Username,
	}
	if len(view.Code) > 32 {
		view.Code = ""
	}
	var (
		rl *auth.RateLimitedError
		fe users.FieldErrors
	)
	switch {
	case err == nil:
		s.startSession(w, r, l.Token)
		http.Redirect(w, r, setupNextURL, http.StatusSeeOther)
	case errors.Is(err, users.ErrOwnerExists):
		s.setupTaken(w, r)
	case errors.As(err, &rl):
		view.FormError = tooMany(w, rl)
		s.frame(w, r, http.StatusTooManyRequests, "Create your account", pages.SetupAccount(view))
	case errors.Is(err, auth.ErrSetupCode):
		view.FieldErrors = map[string]string{"code": msgBadCode}
		s.frame(w, r, http.StatusUnprocessableEntity, "Create your account", pages.SetupAccount(view))
	case errors.As(err, &fe):
		view.FieldErrors = make(map[string]string, len(fe))
		for field, c := range fe {
			view.FieldErrors[field] = fieldMessage(field, c)
		}
		s.frame(w, r, http.StatusUnprocessableEntity, "Create your account", pages.SetupAccount(view))
	default:
		s.serverError(w, r, "setup", err)
	}
}

func (s *server) setupTaken(w http.ResponseWriter, r *http.Request) {
	s.frame(w, r, http.StatusConflict, "Already set up", pages.SetupTaken())
}

func (s *server) getLogin(w http.ResponseWriter, r *http.Request) {
	next := auth.SafeNext(r.URL.Query().Get("next"))
	if _, ok := auth.UserFrom(r.Context()); ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	exists, err := s.auth.OwnerExists(r.Context())
	if err != nil {
		s.serverError(w, r, "owner check", err)
		return
	}
	if !exists {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.frame(w, r, http.StatusOK, "Sign in", pages.Login(s.ssoLoginView(r, next)))
}

func (s *server) postLogin(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	exists, err := s.auth.OwnerExists(r.Context())
	if err != nil {
		s.serverError(w, r, "owner check", err)
		return
	}
	if !exists {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	next := auth.SafeNext(r.PostFormValue("next"))
	username := r.PostFormValue("username")
	var prev *auth.Session
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		prev = &sess
	}
	l, err := s.auth.Login(r.Context(), username, r.PostFormValue("password"), s.proxies.ClientIP(r), r.UserAgent(), prev)

	view := s.ssoLoginView(r, next)
	view.Username = strings.TrimSpace(username)
	if len(view.Username) > 64 {
		view.Username = ""
	}
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
		s.startSession(w, r, l.Token)
		http.Redirect(w, r, next, http.StatusSeeOther)
	case errors.Is(err, auth.ErrBadCredentials):
		view.FormError = msgBadCredentials
		s.frame(w, r, http.StatusUnprocessableEntity, "Sign in", pages.Login(view))
	case errors.As(err, &rl):
		view.FormError = tooMany(w, rl)
		s.frame(w, r, http.StatusTooManyRequests, "Sign in", pages.Login(view))
	default:
		s.serverError(w, r, "login", err)
	}
}

func (s *server) postLogout(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFrom(r.Context())
	if !ok { // the gate already sent anonymous visitors away; this is defence in depth
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := s.auth.Logout(r.Context(), sess); err != nil {
		s.serverError(w, r, "logout", err)
		return
	}
	auth.ClearCookie(w, s.proxies.Secure(r))
	// Clear-Site-Data wipes what the browser holds for this site (downloads, queued saves, the
	// worker); the sign-in page's signed_out=1 makes offline.js do the same where the header is
	// ignored (an insecure address, an older browser).
	w.Header().Set("Clear-Site-Data", `"storage"`)
	http.Redirect(w, r, "/login?signed_out=1", http.StatusSeeOther)
}
