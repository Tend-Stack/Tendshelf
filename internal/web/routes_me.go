package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// registerMe wires Profile, Settings and Change password. Everything here acts on the signed-in
// person only, whatever their role: nothing takes a person id from the request.
func (s *server) registerMe(mux *http.ServeMux) {
	mux.HandleFunc("GET /me", s.getMe)
	mux.HandleFunc("GET /me/settings", s.getSettings)
	mux.HandleFunc("POST /me/settings", s.postSettings)
	mux.HandleFunc("GET /me/password", s.getPassword)
	mux.HandleFunc("POST /me/password", s.postPassword)
	mux.HandleFunc("POST /me/sessions/{sid}/signout", s.postSessionSignOut)
	mux.HandleFunc("POST /me/sessions/signout-others", s.postSignOutOthers)
	mux.HandleFunc("POST /me/devices/{did}/signout", s.postDeviceSignOut)
}

// ---- Profile ----

func (s *server) getMe(w http.ResponseWriter, r *http.Request) {
	v, err := s.profileView(r)
	if err != nil {
		s.serverError(w, r, "profile", err)
		return
	}
	s.pageStyled(w, r, http.StatusOK, "Profile", "profile", pages.Profile(v), nil, pages.MeStyles...)
}

func (s *server) profileView(r *http.Request) (pages.ProfileView, error) {
	ctx := r.Context()
	u := currentUser(r)
	sess, _ := auth.SessionFrom(ctx)
	v := pages.ProfileView{
		CSRF: s.csrf.TokenFrom(ctx), UserID: u.ID, Name: u.DisplayName, Role: roleLabel(string(u.Role)),
		Version: s.version,
	}
	if r.URL.Query().Get("changed") == "password" {
		v.Banner = msgPasswordChanged
	}
	var name string
	if _, err := db.GetSetting(ctx, s.lib.Store().Reader(), settingServerName, &name); err != nil {
		return v, err
	}
	v.ServerName = name
	has, err := s.users.HasPassword(ctx, u.ID)
	if err != nil {
		return v, err
	}
	v.NoPassword = !has

	list, err := s.sessions.ListForUser(ctx, u.ID, sess.Hash)
	if err != nil {
		return v, err
	}
	apps, err := s.devices.ListForUser(ctx, u.ID)
	if err != nil {
		return v, err
	}
	v.Devices, v.Others = deviceRows(s.now(), list, apps, "/me", false)

	if s.can.Can(ctx, u, share.ManageLibraries, share.Target{Kind: share.TargetServer}) {
		if v.Libraries, err = s.librariesRow(ctx, r); err != nil {
			return v, err
		}
	}
	if s.can.Can(ctx, u, share.ManageUsers, share.Target{Kind: share.TargetServer}) {
		if v.UsersRow, err = s.usersRow(ctx, u); err != nil {
			return v, err
		}
	}
	if s.canShelve(r) {
		list, err := s.lib.ShelfCounts(ctx, s.scope(r), u.ID)
		if err != nil {
			return v, err
		}
		v.Shelves = countText(len(list), "1 shelf", "%d shelves")
	}
	return v, nil
}

// librariesRow is "{n} folders · {last scan}", the phone and tablet way into the admin pages.
func (s *server) librariesRow(ctx context.Context, r *http.Request) (string, error) {
	folders, err := s.lib.FolderSummaries(ctx, s.scope(r))
	if err != nil {
		return "", err
	}
	libs, err := s.lib.Libraries(ctx)
	if err != nil {
		return "", err
	}
	last, err := s.lastScanText(ctx, libs)
	if err != nil {
		return "", err
	}
	return countText(len(folders), "1 folder", "%d folders") + " · " + last, nil
}

// ---- Settings ----

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	cur, err := s.users.LoadSettings(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "load settings", err)
		return
	}
	s.pageStyled(w, r, http.StatusOK, "Settings", "profile",
		pages.Settings(settingsView(s.csrf.TokenFrom(r.Context()), cur)), nil, pages.MeStyles...)
}

func (s *server) postSettings(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	cur, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "load settings", err)
		return
	}
	next, ok := settingsFromForm(r.PostForm, cur)
	if !ok {
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msgSettingInvalid))
			return
		}
		v := settingsView(s.csrf.TokenFrom(ctx), cur)
		v.Error = msgSettingInvalid
		s.pageStyled(w, r, http.StatusUnprocessableEntity, "Settings", "profile", pages.Settings(v), nil, pages.MeStyles...)
		return
	}
	if err := s.users.SaveSettings(ctx, u.ID, next); err != nil {
		s.serverError(w, r, "save settings", err)
		return
	}
	// The cookie follows the saved theme, so the sign-in pages on this browser match it.
	s.setThemeCookie(w, r, next.Theme)
	if r.Header.Get("HX-Request") == "true" {
		if next.Theme != cur.Theme {
			w.Header().Set("HX-Refresh", "true") // the page re-renders in the new theme
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/me/settings", http.StatusSeeOther)
}

// ---- Change password ----

func (s *server) getPassword(w http.ResponseWriter, r *http.Request) {
	s.passwordPage(w, r, http.StatusOK, pages.PasswordView{})
}

func (s *server) passwordPage(w http.ResponseWriter, r *http.Request, status int, v pages.PasswordView) {
	v.CSRF = s.csrf.TokenFrom(r.Context())
	s.pageStyled(w, r, status, "Change password", "profile", pages.Password(v), nil, pages.PasswordStyles...)
}

func (s *server) postPassword(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	sess, ok := auth.SessionFrom(r.Context())
	if !ok { // the gate already sent anonymous visitors away; this is defence in depth
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tok, err := s.auth.ChangePassword(r.Context(), sess, r.PostFormValue("current"), r.PostFormValue("password"), s.proxies.ClientIP(r))
	var (
		rl *auth.RateLimitedError
		fe users.FieldErrors
	)
	switch {
	case err == nil:
		s.startSession(w, r, tok) // the old token is dead; this browser carries on with a new one
		http.Redirect(w, r, "/me?changed=password", http.StatusSeeOther)
	case errors.Is(err, auth.ErrBadCredentials):
		s.passwordPage(w, r, http.StatusUnprocessableEntity, pages.PasswordView{CurrentError: msgNotCurrentPassword})
	case errors.As(err, &fe):
		s.passwordPage(w, r, http.StatusUnprocessableEntity, pages.PasswordView{NewError: fieldMessage("password", fe["password"])})
	case errors.As(err, &rl):
		s.passwordPage(w, r, http.StatusTooManyRequests, pages.PasswordView{FormError: tooMany(w, rl)})
	default:
		s.serverError(w, r, "change password", err)
	}
}

// ---- Devices ----

func (s *server) postSessionSignOut(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	sid := r.PathValue("sid")
	if sid == auth.PublicID(sess.Hash) {
		s.postLogout(w, r) // signing out this browser is signing out
		return
	}
	_, found, err := s.sessions.DeleteByPublicID(r.Context(), sess.UserID, sid)
	switch {
	case err != nil:
		s.serverError(w, r, "sign out device", err)
	case !found: // unknown, malformed, or someone else's
		s.notFound(w, r)
	default:
		http.Redirect(w, r, "/me", http.StatusSeeOther)
	}
}

// postDeviceSignOut revokes one of the signed-in person's own device tokens. An unknown, revoked or
// someone else's id is a 404 page, so ids reveal nothing.
func (s *server) postDeviceSignOut(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ok, err := s.auth.RevokeDevice(r.Context(), u.ID, r.PathValue("did"), u.ID)
	switch {
	case err != nil:
		s.serverError(w, r, "sign out app", err)
	case !ok:
		s.notFound(w, r)
	default:
		http.Redirect(w, r, "/me", http.StatusSeeOther)
	}
}

func (s *server) postSignOutOthers(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if _, err := s.auth.SignOutOthers(r.Context(), sess); err != nil {
		s.serverError(w, r, "sign out others", err)
		return
	}
	http.Redirect(w, r, "/me", http.StatusSeeOther)
}
