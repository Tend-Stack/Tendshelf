package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Sign in with an OpenID Connect provider (decision 0037, plan docs/plans/oidc.md). The flow itself
// is internal/auth (OIDCStart, OIDCFinish, the link methods); this file is the HTTP side: the start
// link, the callback, the Account page and the owner's settings. No code, state or token is ever
// logged, rendered or put in a Location other than the provider's authorize address.

const (
	oidcStartPath    = "/login/oidc"
	oidcOriginsPath  = "/login/oidc/origins"
	oidcCookie       = "ts_oidc"
	oidcCookieMaxAge = 600 // seconds; the sign-in row lives 10 minutes
)

func (s *server) registerOIDC(mux *http.ServeMux) {
	mux.HandleFunc("GET /login/oidc", s.getOIDCStart)
	mux.HandleFunc("GET /login/oidc/callback", s.getOIDCCallback)
	mux.HandleFunc("GET /login/oidc/origins", s.getOIDCOrigins)
	mux.HandleFunc("GET /me/sso", s.getMeSSO)
	mux.HandleFunc("GET /me/sso/link", s.getMeSSOLink)
	mux.HandleFunc("GET /me/sso/confirm", s.getMeSSOConfirm)
	mux.HandleFunc("POST /me/sso/confirm", s.postMeSSOConfirm)
	mux.HandleFunc("POST /me/sso/unlink", s.postMeSSOUnlink)
	mux.HandleFunc("GET /admin/sso", s.getAdminSSO)
	mux.HandleFunc("POST /admin/sso", s.postAdminSSO)
	mux.HandleFunc("POST /admin/sso/off", s.postAdminSSOOff)
}

// isOIDCPublicPath reports whether p is one of the three sign-in routes a signed-out browser may
// reach (exact paths).
func isOIDCPublicPath(p string) bool {
	return p == oidcStartPath || p == oidc.CallbackPath || p == oidcOriginsPath
}

// requestOrigin is the origin the browser used for r, in the normal form of ParseServerAddress.
func (s *server) requestOrigin(r *http.Request) string {
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	o, _ := oidc.ParseServerAddress(scheme + r.Host)
	return o
}

// oidcCookieFor builds the ts_oidc cookie: HttpOnly, SameSite=Lax, Secure where the session cookie
// is, limited to the callback path. maxAge -1 clears it.
func (s *server) oidcCookieFor(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: oidcCookie, Value: value, Path: oidc.CallbackPath, HttpOnly: true,
		Secure: s.proxies.Secure(r), SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

// ssoLoginView is the sign-in page with the provider button when it is on.
func (s *server) ssoLoginView(r *http.Request, next string) pages.LoginView {
	v := pages.LoginView{CSRF: s.csrf.TokenFrom(r.Context()), Next: next, NoPassword: loginNoPassword}
	if cfg, on := s.auth.OIDCEnabled(r.Context()); on {
		href := oidcStartPath
		if next != "" && next != "/" {
			href += "?next=" + url.QueryEscape(next)
		}
		v.SSOLabel, v.SSOHref, v.SSOOr = fmt.Sprintf(ssoButton, cfg.Name), href, ssoOr
	}
	return v
}

// ssoFail answers a sign-in or link step that did not work with plain words: the sign-in page for a
// person who is signed out, the Account page for one who is signed in. It never redirects, so no
// reason travels in a Location.
func (s *server) ssoFail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if _, ok := auth.UserFrom(r.Context()); ok {
		s.meSSOPage(w, r, status, pages.MeSSOView{Error: msg})
		return
	}
	v := s.ssoLoginView(r, "")
	v.FormError = msg
	s.frame(w, r, status, "Sign in", pages.Login(v))
}

// ssoProviderName is the provider's name for messages; "" when sign-in with a provider is off.
func (s *server) ssoProviderName(r *http.Request) string {
	cfg, _ := s.auth.OIDCEnabled(r.Context())
	return cfg.Name
}

// ssoErrorStatus maps an error of the start or the callback to a status and a message. ok is false
// for an error that is the server's own (a 500).
func (s *server) ssoErrorStatus(w http.ResponseWriter, r *http.Request, err error) (status int, msg string, ok bool) {
	name := s.ssoProviderName(r)
	var (
		rl *auth.RateLimitedError
		im *oidc.IssuerMismatchError
		ae *oidc.AlgError
	)
	switch {
	case errors.As(err, &rl):
		return http.StatusTooManyRequests, tooMany(w, rl), true
	case errors.Is(err, auth.ErrOIDCOff):
		return http.StatusNotFound, "", true
	case errors.Is(err, auth.ErrOIDCBusy):
		return http.StatusServiceUnavailable, ssoBusy, true
	case errors.Is(err, auth.ErrOIDCCancelled):
		return http.StatusOK, fmt.Sprintf(ssoCancelled, name), true
	case errors.Is(err, auth.ErrOIDCFailed):
		return http.StatusForbidden, fmt.Sprintf(ssoFailed, name), true
	case errors.Is(err, auth.ErrOIDCNoAccount):
		return http.StatusForbidden, fmt.Sprintf(ssoNoAccount, name, name), true
	case errors.Is(err, auth.ErrOIDCEmailTaken):
		return http.StatusConflict, fmt.Sprintf(ssoEmailTaken, name), true
	case errors.Is(err, oidc.ErrProviderDown), errors.Is(err, oidc.ErrProviderConfig), errors.Is(err, oidc.ErrProviderRefused),
		errors.As(err, &im), errors.As(err, &ae):
		return http.StatusServiceUnavailable, fmt.Sprintf(ssoDown, name), true
	}
	return 0, "", false
}

// getOIDCStart begins a sign-in. It is a GET link on purpose (decision 0037 point 4): the redirect
// leaves the origin, which form-action 'self' would block for a form.
func (s *server) getOIDCStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	next := auth.SafeNext(r.URL.Query().Get("next"))
	if _, ok := auth.UserFrom(r.Context()); ok {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	cfg, on := s.auth.OIDCEnabled(r.Context())
	if !on {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	if s.requestOrigin(r) != cfg.ServerAddress {
		// The state cookie and the session must land on the origin the provider returns to. The Android
		// window keeps its own origin (the app's /app/connect/done), so it says where to connect instead.
		if shellFrom(r) == "android" || r.URL.Query().Get("ro") == "1" {
			v := s.ssoLoginView(r, next)
			v.FormError = fmt.Sprintf(ssoWrongAddr, cfg.Name, cfg.ServerAddress)
			s.frame(w, r, http.StatusOK, "Sign in", pages.Login(v))
			return
		}
		q := url.Values{"ro": {"1"}}
		if next != "/" {
			q.Set("next", next)
		}
		http.Redirect(w, r, cfg.ServerAddress+oidcStartPath+"?"+q.Encode(), http.StatusSeeOther)
		return
	}
	s.beginOIDC(w, r, "signin", next, nil)
}

// beginOIDC starts a sign-in or a link step and sends the browser to the provider.
func (s *server) beginOIDC(w http.ResponseWriter, r *http.Request, purpose, next string, sess *auth.Session) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	authURL, state, err := s.auth.OIDCStart(r.Context(), purpose, next, s.proxies.ClientIP(r), sess)
	if err != nil {
		status, msg, ok := s.ssoErrorStatus(w, r, err)
		switch {
		case !ok:
			s.serverError(w, r, "oidc start", err)
		case status == http.StatusNotFound:
			s.errorPage(w, r, status)
		default:
			s.ssoFail(w, r, status, msg)
		}
		return
	}
	http.SetCookie(w, s.oidcCookieFor(r, state, oidcCookieMaxAge))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// getOIDCCallback is where the provider sends the browser back. Whatever happens, the ts_oidc cookie
// is cleared and the answer carries Referrer-Policy: no-referrer.
func (s *server) getOIDCCallback(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	var cookie string
	if c, err := r.Cookie(oidcCookie); err == nil {
		cookie = c.Value
	}
	http.SetCookie(w, s.oidcCookieFor(r, "", -1))
	q := r.URL.Query()
	var prev *auth.Session
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		prev = &sess
	}
	res, err := s.auth.OIDCFinish(r.Context(), auth.OIDCCallback{
		State: q.Get("state"), Cookie: cookie, Code: q.Get("code"), Error: q.Get("error"),
	}, s.proxies.ClientIP(r), r.UserAgent(), prev)
	if err != nil {
		status, msg, ok := s.ssoErrorStatus(w, r, err)
		switch {
		case !ok:
			s.serverError(w, r, "oidc callback", err)
		case status == http.StatusNotFound:
			s.errorPage(w, r, status)
		default:
			s.ssoFail(w, r, status, msg)
		}
		return
	}
	if res.LinkPending != "" {
		http.Redirect(w, r, "/me/sso/confirm?id="+url.QueryEscape(res.LinkPending), http.StatusSeeOther)
		return
	}
	s.startSession(w, r, res.Login.Token)
	http.Redirect(w, r, res.Next, http.StatusSeeOther)
}

// getOIDCOrigins lists the origins a sign-in visits, for the Android window. Public, JSON, empty
// when sign-in with a provider is off or the provider does not answer.
func (s *server) getOIDCOrigins(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	origins, err := s.auth.OIDCOrigins(r.Context(), s.proxies.ClientIP(r))
	var rl *auth.RateLimitedError
	if errors.As(err, &rl) {
		tooMany(w, rl)
		h.Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"origins":[]}`))
		return
	}
	if origins == nil {
		origins = []string{}
	}
	h.Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Origins []string `json:"origins"`
	}{origins})
}

// ---- The Account page ----

// meSSOPage renders /me/sso. The view is completed here: title, links and the linked line.
func (s *server) meSSOPage(w http.ResponseWriter, r *http.Request, status int, v pages.MeSSOView) {
	ctx := r.Context()
	cfg, on := s.auth.OIDCEnabled(ctx)
	if !on {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	u := currentUser(r)
	id, linked, err := s.auth.OIDCIdentity(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "sso page", err)
		return
	}
	has, err := s.users.HasPassword(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "sso page", err)
		return
	}
	v.CSRF, v.Title = s.csrf.TokenFrom(ctx), fmt.Sprintf(ssoTitle, cfg.Name)
	v.Linked, v.NoPassword = linked && id.Issuer == cfg.Issuer, !has
	v.LinkLabel, v.LinkHref, v.UnlinkLabel = fmt.Sprintf(ssoLinkButton, cfg.Name), "/me/sso/link", ssoUnlinkButton
	v.NoPasswordNote, v.SignOutNote = ssoUnlinkNoPassword, fmt.Sprintf(ssoSignOutNote, cfg.Name)
	if v.Linked {
		who := id.Email
		if who == "" {
			who = fmt.Sprintf(ssoLinkedFallback, cfg.Name)
		}
		v.Line = fmt.Sprintf(ssoLinkedLine, who, time.UnixMilli(id.CreatedAt).In(s.now().Location()).Format("2 Jan 2006"))
	} else {
		v.Line = ssoNotLinked
	}
	s.prefsPage(w, r, status, v.Title, "profile", "account", false, pages.MeSSO(v), nil, pages.SSOStyles...)
}

func (s *server) getMeSSO(w http.ResponseWriter, r *http.Request) {
	v := pages.MeSSOView{}
	switch r.URL.Query().Get("done") {
	case "linked":
		v.Banner = fmt.Sprintf(ssoLinked, s.ssoProviderName(r))
	case "unlinked":
		v.Banner = ssoUnlinked
	}
	s.meSSOPage(w, r, http.StatusOK, v)
}

func (s *server) getMeSSOLink(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		s.redirect(w, r, "/login")
		return
	}
	cfg, on := s.auth.OIDCEnabled(r.Context())
	if !on {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	if s.requestOrigin(r) != cfg.ServerAddress && shellFrom(r) != "android" && r.URL.Query().Get("ro") != "1" {
		http.Redirect(w, r, cfg.ServerAddress+"/me/sso/link?ro=1", http.StatusSeeOther)
		return
	}
	s.beginOIDC(w, r, "link", "/me/sso", &sess)
}

func (s *server) getMeSSOConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		s.redirect(w, r, "/login")
		return
	}
	name := s.ssoProviderName(r)
	who, err := s.auth.OIDCLinkView(r.Context(), r.URL.Query().Get("id"), sess)
	switch {
	case errors.Is(err, auth.ErrOIDCLinkGone):
		s.meSSOPage(w, r, http.StatusConflict, pages.MeSSOView{Error: ssoLinkGone})
	case err != nil:
		s.serverError(w, r, "sso confirm", err)
	default:
		s.meSSOPage(w, r, http.StatusOK, pages.MeSSOView{Confirm: &pages.MeSSOConfirm{
			ID: r.URL.Query().Get("id"), Title: fmt.Sprintf(ssoConfirmTitle, name), Body: fmt.Sprintf(ssoConfirmBody, name, who),
			Button: ssoConfirmButton, Cancel: ssoConfirmCancel,
		}})
	}
}

func (s *server) postMeSSOConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		s.redirect(w, r, "/login")
		return
	}
	name := s.ssoProviderName(r)
	err := s.auth.OIDCConfirmLink(r.Context(), r.PostFormValue("id"), sess)
	switch {
	case err == nil:
		s.answerPage(w, r, "/me/sso?done=linked")
	case errors.Is(err, auth.ErrOIDCLinkGone):
		s.meSSOPage(w, r, http.StatusConflict, pages.MeSSOView{Error: ssoLinkGone})
	case errors.Is(err, auth.ErrOIDCTaken):
		s.meSSOPage(w, r, http.StatusConflict, pages.MeSSOView{Error: fmt.Sprintf(ssoTaken, name)})
	case errors.Is(err, auth.ErrOIDCAlready):
		s.meSSOPage(w, r, http.StatusConflict, pages.MeSSOView{Error: fmt.Sprintf(ssoAlready, name)})
	default:
		s.serverError(w, r, "sso confirm", err)
	}
}

func (s *server) postMeSSOUnlink(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	err := s.auth.OIDCUnlink(r.Context(), currentUser(r).ID)
	switch {
	case err == nil:
		s.answerPage(w, r, "/me/sso?done=unlinked")
	case errors.Is(err, auth.ErrOIDCNotLinked):
		s.answerPage(w, r, "/me/sso")
	case errors.Is(err, auth.ErrOIDCLastWayIn):
		s.meSSOPage(w, r, http.StatusConflict, pages.MeSSOView{Error: ssoUnlinkNoPassword})
	default:
		s.serverError(w, r, "sso unlink", err)
	}
}

// ---- The owner's settings ----

func (s *server) ssoGate(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer})
}

var ssoRoleChoices = []pages.Option{
	{Value: string(users.RoleMember), Label: ssoRoleMember},
	{Value: string(users.RoleGuest), Label: ssoRoleGuest},
}

// ssoAdminView builds the settings page from what is saved. The client secret never enters it. A
// server address not yet saved is prefilled with the origin this request came on.
func (s *server) ssoAdminView(r *http.Request) (pages.AdminSSOView, error) {
	ctx := r.Context()
	cfg, err := oidc.LoadConfig(ctx, s.lib.Store().Reader())
	if err != nil {
		return pages.AdminSSOView{}, err
	}
	addr := cfg.ServerAddress
	if addr == "" {
		addr = s.requestOrigin(r)
	}
	v := pages.AdminSSOView{
		CSRF: s.csrf.TokenFrom(ctx), Name: cfg.Name, Issuer: cfg.Issuer, ClientID: cfg.ClientID, ServerAddress: addr,
		SecretSaved: cfg.ClientSecret != "", Enabled: cfg.Enabled, MatchEmail: cfg.MatchEmail, CreateAccounts: cfg.CreateAccounts,
		NewRole: string(cfg.NewRole), Roles: ssoRoleChoices, Errors: map[string]string{}, Intro: ssoAdminIntro,
		RedirectHint: fmt.Sprintf(ssoHintRedirect, addr+oidc.CallbackPath),
		SavedNotice:  ssoAdminSaved, OffNotice: ssoAdminOffDone,
	}
	if cfg.Ready() {
		v.StatusLine = fmt.Sprintf(ssoStatusOn, cfg.Name)
	} else {
		v.StatusLine = ssoAdminOff
	}
	return v, nil
}

func (s *server) getAdminSSO(w http.ResponseWriter, r *http.Request) {
	if !s.ssoGate(w, r) {
		return
	}
	v, err := s.ssoAdminView(r)
	if err != nil {
		s.serverError(w, r, "sso settings", err)
		return
	}
	v.Saved, v.TurnedOff = r.URL.Query().Get("saved") == "1", r.URL.Query().Get("off") == "1"
	s.renderAdminSSO(w, r, http.StatusOK, v)
}

func (s *server) renderAdminSSO(w http.ResponseWriter, r *http.Request, status int, v pages.AdminSSOView) {
	s.prefsPage(w, r, status, ssoAdminTitle, "admin-sso", "sso", false, pages.AdminSSO(v), nil, pages.SSOStyles...)
}

// ssoFieldMessages turns the codes of oidc.SaveConfig into plain words.
func ssoFieldMessages(fe users.FieldErrors) map[string]string {
	out := map[string]string{}
	for field, code := range fe {
		switch {
		case code == "required":
			out[field] = ssoErrRequired
		case code == "too_long":
			out[field] = ssoErrTooLong
		case field == "issuer":
			out[field] = ssoErrIssuerFormat
		case field == "server_address":
			out[field] = ssoErrServerFormat
		default:
			out[field] = ssoErrInvalid
		}
	}
	return out
}

// ssoDiscoveryMessage explains a provider that cannot be turned on, in plain words.
func ssoDiscoveryMessage(issuer string, err error) string {
	var (
		im *oidc.IssuerMismatchError
		ae *oidc.AlgError
	)
	switch {
	case errors.As(err, &im):
		return fmt.Sprintf(ssoErrIssuer, im.Got)
	case errors.As(err, &ae):
		return fmt.Sprintf(ssoErrAlg, strings.Join(ae.Offered, ", "))
	}
	return fmt.Sprintf(ssoErrDiscovery, issuer)
}

func (s *server) postAdminSSO(w http.ResponseWriter, r *http.Request) {
	if !s.ssoGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	f := r.PostForm
	in := oidc.ConfigInput{
		Enabled: f.Get("enabled") == "1", Name: f.Get("name"), Issuer: f.Get("issuer"), ClientID: f.Get("client_id"),
		ServerAddress: f.Get("server_address"), MatchEmail: f.Get("match_email") == "1", CreateAccounts: f.Get("create_accounts") == "1",
		NewRole: users.Role(f.Get("new_role")),
	}
	secret := f.Get("client_secret")
	refuse := func(errs map[string]string, formErr string) {
		v, err := s.ssoAdminView(r)
		if err != nil {
			s.serverError(w, r, "sso settings", err)
			return
		}
		// Show what was typed (never the secret).
		v.Name, v.Issuer, v.ClientID, v.ServerAddress = strings.TrimSpace(in.Name), strings.TrimSpace(in.Issuer), strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ServerAddress)
		v.Enabled, v.MatchEmail, v.CreateAccounts = in.Enabled, in.MatchEmail, in.CreateAccounts
		if in.NewRole != "" {
			v.NewRole = string(in.NewRole)
		}
		v.Errors, v.FormError = errs, formErr
		s.renderAdminSSO(w, r, http.StatusUnprocessableEntity, v)
	}
	// Turning it on reads the provider's settings once, so a wrong address or an unusable signing key
	// is said now and not at the first sign-in. The check runs only for an issuer that parses.
	if issuer := strings.TrimSpace(in.Issuer); in.Enabled && oidc.ParseIssuer(issuer) {
		if _, err := s.auth.OIDCDiscover(ctx, issuer); err != nil {
			refuse(map[string]string{"issuer": ssoDiscoveryMessage(issuer, err)}, "")
			return
		}
	}
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := oidc.SaveConfig(ctx, tx, tx, currentUser(r), in, secret, s.now())
		return err
	})
	var fe users.FieldErrors
	if errors.As(err, &fe) {
		refuse(ssoFieldMessages(fe), "")
		return
	}
	if err != nil {
		s.serverError(w, r, "save sso settings", err)
		return
	}
	s.answerPage(w, r, "/admin/sso?saved=1")
}

func (s *server) postAdminSSOOff(w http.ResponseWriter, r *http.Request) {
	if !s.ssoGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		return oidc.ClearConfig(ctx, tx, currentUser(r), s.now())
	})
	if err != nil {
		s.serverError(w, r, "turn off sso", err)
		return
	}
	s.answerPage(w, r, "/admin/sso?off=1")
}
