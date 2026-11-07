package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Sign-in links (decision 0030, plan docs/plans/sign-in-links.md). An admin makes a one-time link on
// the person's page; the person opens it, confirms, and chooses a new password. /signin/{token} is
// public like /invite/{token}: a GET only checks, the POST spends, and every failure is the same 404
// page. /me/new-password sits behind the gate and opens only for the session a link started.

func (s *server) registerSignIn(mux *http.ServeMux) {
	mux.HandleFunc("GET /signin/{token}", s.getSignIn)
	mux.HandleFunc("POST /signin/{token}", s.postSignIn)
	mux.HandleFunc("GET /me/new-password", s.getNewPassword)
	mux.HandleFunc("POST /me/new-password", s.postNewPassword)
}

// signInURL is the address of a sign-in link, as inviteURL is for an invite.
func (s *server) signInURL(r *http.Request, tok string) string {
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	return scheme + r.Host + "/signin/" + tok
}

// signInQR is the QR of a sign-in link, or nil on an address a phone cannot reach.
func (s *server) signInQR(r *http.Request, tok string) *components.QRView {
	if !reachableHost(r.Host) {
		return nil
	}
	if q, ok := qrView(s.signInURL(r, tok), signInQRLabel); ok {
		return &q
	}
	return nil
}

// signInBroken is the one answer to every link that does not work, whatever the reason.
func (s *server) signInBroken(w http.ResponseWriter, r *http.Request) {
	s.inviteState(w, r, http.StatusNotFound, pages.InviteStateView{Title: signInBrokenTitle, Body: signInBrokenBody})
}

// signInAppSession refuses a connected app's session: its WebView and its token must stay one person.
// The link stays unused.
func (s *server) signInAppSession(w http.ResponseWriter, r *http.Request) bool {
	if sess, ok := auth.SessionFrom(r.Context()); ok && sess.DeviceID != "" {
		s.inviteState(w, r, http.StatusConflict, pages.InviteStateView{Title: signInBoundNotice})
		return true
	}
	return false
}

func (s *server) getSignIn(w http.ResponseWriter, r *http.Request) {
	inviteHeaders(w)
	if s.signInAppSession(w, r) {
		return
	}
	name, err := s.auth.CheckSignInLink(r.Context(), r.PathValue("token"), s.proxies.ClientIP(r))
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
	case errors.As(err, &rl):
		s.inviteLimited(w, r, rl)
		return
	case errors.Is(err, auth.ErrSignInLinkBroken):
		s.signInBroken(w, r)
		return
	default:
		s.serverError(w, r, "check sign-in link", err)
		return
	}
	s.frame(w, r, http.StatusOK, fmt.Sprintf(signInConfirmTitle, s.serverName(r.Context())), pages.SignInConfirm(pages.SignInConfirmView{
		CSRF: s.csrf.TokenFrom(r.Context()), Token: r.PathValue("token"), ServerName: s.serverName(r.Context()),
		Title: fmt.Sprintf(signInConfirmTitle, s.serverName(r.Context())), Body: fmt.Sprintf(signInConfirmBody, name), Button: signInConfirmButton,
	}), pages.InviteStyles...)
}

func (s *server) postSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	inviteHeaders(w)
	if s.signInAppSession(w, r) {
		return
	}
	var prev *auth.Session
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		prev = &sess
	}
	l, has, err := s.auth.SpendSignInLink(r.Context(), r.PathValue("token"), s.proxies.ClientIP(r), r.UserAgent(), prev)
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
	case errors.As(err, &rl):
		s.inviteLimited(w, r, rl)
		return
	case errors.Is(err, auth.ErrSignInLinkBroken):
		s.signInBroken(w, r)
		return
	default:
		s.serverError(w, r, "spend sign-in link", err)
		return
	}
	s.startSession(w, r, l.Token)
	next := "/me/new-password"
	if !has {
		next = "/welcome"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// linkSession returns the session of the request when it is one a sign-in link started, within the
// hour; otherwise it has answered 404.
func (s *server) linkSession(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := auth.SessionFrom(r.Context())
	if ok {
		bound, err := s.auth.SignInBound(r.Context(), sess)
		if err != nil {
			s.serverError(w, r, "sign-in session", err)
			return auth.Session{}, false
		}
		ok = bound
	}
	if !ok {
		s.notFound(w, r)
		return auth.Session{}, false
	}
	return sess, true
}

func (s *server) newPasswordView(r *http.Request, errMsg string) pages.NewPasswordView {
	u := currentUser(r)
	return pages.NewPasswordView{
		CSRF: s.csrf.TokenFrom(r.Context()), Username: u.Username, Error: errMsg, Title: signInNewTitle,
		Lead: fmt.Sprintf(signInNewLead, u.Username), Label: signInNewLabel, Button: signInNewButton, Skip: signInNewSkip,
	}
}

func (s *server) getNewPassword(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.linkSession(w, r); !ok {
		return
	}
	has, err := s.users.HasPassword(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "new password", err)
		return
	}
	if !has {
		s.redirect(w, r, "/welcome")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.frame(w, r, http.StatusOK, signInNewTitle, pages.NewPassword(s.newPasswordView(r, "")), pages.InviteStyles...)
}

func (s *server) postNewPassword(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	sess, ok := s.linkSession(w, r)
	if !ok {
		return
	}
	tok, err := s.auth.SetPasswordFromLink(r.Context(), sess, r.PostFormValue("password"), s.proxies.ClientIP(r))
	var fe users.FieldErrors
	switch {
	case err == nil:
	case errors.As(err, &fe):
		w.Header().Set("Cache-Control", "no-store")
		s.frame(w, r, http.StatusUnprocessableEntity, signInNewTitle,
			pages.NewPassword(s.newPasswordView(r, fieldMessage("password", fe["password"]))), pages.InviteStyles...)
		return
	case errors.Is(err, auth.ErrNotLinkSession):
		s.notFound(w, r)
		return
	case errors.Is(err, auth.ErrNoPassword):
		s.redirect(w, r, "/welcome")
		return
	default:
		s.serverError(w, r, "set password from sign-in link", err)
		return
	}
	s.startSession(w, r, tok)
	s.redirect(w, r, "/me?changed=password")
}

// signInPersonView fills the sign-in parts of a person's page: the username line, and whether a link
// is open and until when.
func (s *server) signInPersonView(r *http.Request, t users.User, v *pages.AdminUserView) error {
	ctx := r.Context()
	has, err := s.users.HasPassword(ctx, t.ID)
	if err != nil {
		return err
	}
	v.NoPassword = !has
	v.Username = fmt.Sprintf(personUsername, t.Username)
	if !has {
		v.Username = fmt.Sprintf(personUsernameNoPassword, t.Username)
	}
	name := personName(t)
	v.SignInLabel, v.SignInHelp = signInMake, fmt.Sprintf(signInMakeHelp, name)
	exp, ok, err := s.auth.PendingSignInLink(ctx, t.ID)
	if err != nil {
		return err
	}
	if ok {
		v.SignInOpen = fmt.Sprintf(signInOpenLine, signInWhen(s.now(), exp))
	}
	return nil
}

// signInWhen is the plain time a link stops working, in the server's zone.
func signInWhen(now time.Time, ms int64) string {
	return time.UnixMilli(ms).In(now.Location()).Format("Mon 2 Jan, 15:04")
}

// postPersonSignInLink makes a sign-in link for a person the signed-in person manages and answers with
// the person's page showing it once. It never redirects: the token must stay out of Location.
func (s *server) postPersonSignInLink(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	var tok string
	var exp int64
	var target users.User
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		if t.Disabled {
			return errRefused
		}
		var err error
		tok, exp, err = s.auth.MakeSignInLink(ctx, tx, actor.ID, id)
		target = t
		return err
	})
	if !done {
		return
	}
	inviteHeaders(w)
	name := personName(target)
	s.renderPersonWith(w, r, http.StatusOK, target, "", func(v *pages.AdminUserView) {
		v.SignInOpen = ""
		v.SignInLink = &pages.InviteLink{
			Name: name, URL: s.signInURL(r, tok), Title: fmt.Sprintf(signInPanelTitle, name),
			Note: fmt.Sprintf(signInPanelNote, signInWhen(s.now(), exp), name), Label: signInFieldLabel, FieldID: "signin-link",
			QR: s.signInQR(r, tok),
		}
	})
}
