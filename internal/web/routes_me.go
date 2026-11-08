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
	mux.HandleFunc("GET /me/devices", s.getDevices)
	mux.HandleFunc("GET /me/settings", s.getSettings)
	mux.HandleFunc("GET /me/settings/{section}", s.getSettingsSection)
	mux.HandleFunc("POST /me/settings", s.postSettings)
	mux.HandleFunc("GET /me/password", s.getPassword)
	mux.HandleFunc("POST /me/password", s.postPassword)
	mux.HandleFunc("POST /me/sessions/{sid}/signout", s.postSessionSignOut)
	mux.HandleFunc("POST /me/sessions/signout-others", s.postSignOutOthers)
	mux.HandleFunc("POST /me/devices/{did}/signout", s.postDeviceSignOut)
	mux.HandleFunc("POST /me/sessions/{sid}/name", s.postSessionName)
	mux.HandleFunc("POST /me/devices/{did}/name", s.postDeviceName)
	s.registerNav(mux)
}

// ---- Profile ----

func (s *server) getMe(w http.ResponseWriter, r *http.Request) {
	v, err := s.profileView(r)
	if err != nil {
		s.serverError(w, r, "profile", err)
		return
	}
	s.pageStyled(w, r, http.StatusOK, "Profile", "profile", pages.Profile(v), nil, pages.ProfileStyles...)
}

// ownDeviceRows reads the signed-in person's own browsers and apps; no id comes from the request.
func (s *server) ownDeviceRows(r *http.Request) ([]pages.ProfileDevice, int, error) {
	ctx := r.Context()
	u := currentUser(r)
	sess, _ := auth.SessionFrom(ctx)
	list, err := s.sessions.ListForUser(ctx, u.ID, sess.Hash)
	if err != nil {
		return nil, 0, err
	}
	apps, err := s.devices.ListForUser(ctx, u.ID)
	if err != nil {
		return nil, 0, err
	}
	rows, others := deviceRows(s.now(), list, apps, "/me", false, sess.DeviceID)
	return rows, others, nil
}

// getDevices is "Your devices": the person's sign-ins, nearly identical ones folded.
func (s *server) getDevices(w http.ResponseWriter, r *http.Request) {
	s.devicesPage(w, r, http.StatusOK, r.URL.Query().Get("name"), "", "")
}

// devicesPage renders "Your devices". A refused Name save comes back as 422 with renameID's form open,
// the typed value kept and msg beside the field.
func (s *server) devicesPage(w http.ResponseWriter, r *http.Request, status int, renameID, renameValue, msg string) {
	rows, others, err := s.ownDeviceRows(r)
	if err != nil {
		s.serverError(w, r, "devices", err)
		return
	}
	if renameID != "" && renameValue == "" && msg == "" {
		// The Name link opened a form: it starts with the row's current name.
		for _, d := range rows {
			if d.ID == renameID {
				renameValue = d.Name
			}
		}
	}
	keys, err := s.appKeysView(r)
	if err != nil {
		s.serverError(w, r, "app keys", err)
		return
	}
	feeds, err := s.feedsView(r)
	if err != nil {
		s.serverError(w, r, "feeds", err)
		return
	}
	v := pages.DevicesView{CSRF: s.csrf.TokenFrom(r.Context()), Lines: groupDevices(rows), Others: others, AppKeys: keys, Feeds: feeds,
		RenameID: renameID, RenameValue: renameValue, RenameErr: msg,
		NameButton: devNameButton, NameLabel: devNameLabel, HintWeb: devNameHintWeb, HintApp: devNameHintApp}
	s.prefsPage(w, r, status, devTitle, "profile", "devices", false, pages.Devices(v), nil, append(pages.MeStyles, pages.AppKeyStyles...)...)
}

// deviceNameInput cleans a posted device name and checks its length: control characters dropped, white
// space collapsed, at most 64 characters. msg is the plain-words refusal, "" when the name is fine.
func deviceNameInput(raw string) (name, msg string) {
	name = auth.CleanName(raw)
	if len([]rune(name)) > auth.MaxDeviceNameRunes {
		return name, devNameTooLong
	}
	return name, ""
}

func (s *server) profileView(r *http.Request) (pages.ProfileView, error) {
	ctx := r.Context()
	u := currentUser(r)
	v := pages.ProfileView{
		CSRF: s.csrf.TokenFrom(ctx), UserID: u.ID, Name: u.DisplayName, Role: roleLabel(string(u.Kind())),
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

	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		return v, err
	}
	st, err := s.progress.Stats(ctx, u.ID, statsLocation(set.TimeZone), s.now())
	if err != nil {
		return v, err
	}
	reads, err := s.progress.HasReading(ctx, u.ID)
	if err != nil {
		return v, err
	}
	v.Stats = statsView(st, false, reads)

	_, others, err := s.ownDeviceRows(r)
	if err != nil {
		return v, err
	}
	v.DevicesLine = devicesLine(others)

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
	if s.can.Can(ctx, u, share.ManageServer, share.Target{Kind: share.TargetServer}) {
		v.Email = true
		if v.Backups, err = s.backupsLastLine(); err != nil {
			return v, err
		}
	}
	if v.Books, err = s.profileBooksView(r, set.Speed); err != nil {
		return v, err
	}
	if v.CanShelve = s.canShelve(r); v.CanShelve {
		if v.ShelfRows, err = s.profileShelvesView(r); err != nil {
			return v, err
		}
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

// settingsSections are the personal categories of /me/settings/{section} and their titles.
var settingsSections = map[string]string{
	"playback": "Playback", "appearance": "Appearance", "accessibility": "Easier to use", "privacy": "Privacy",
}

// getSettings is the category list; beside it (when there is room) the Playback pane.
func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsPage(w, r, http.StatusOK, "playback", true, "")
}

func (s *server) getSettingsSection(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	if _, ok := settingsSections[section]; !ok {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	s.settingsPage(w, r, http.StatusOK, section, false, "")
}

// settingsPage renders one category of Settings; refusal is the line a failed plain post shows.
func (s *server) settingsPage(w http.ResponseWriter, r *http.Request, status int, section string, index bool, refusal string) {
	cur, err := s.users.LoadSettings(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "load settings", err)
		return
	}
	v := settingsView(s.csrf.TokenFrom(r.Context()), cur)
	v.InApp = shellFrom(r) == "android"
	v.Section, v.Index, v.Error = section, index, refusal
	title := "Settings"
	if !index {
		title = settingsSections[section]
	}
	s.prefsPage(w, r, status, title, "profile", section, index, pages.SettingsPane(v), nil, pages.MeStyles...)
}

func (s *server) postSettings(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	if zone, only := timeZoneOnly(r.PostForm); only {
		// shell.js posts the browser's zone by itself: store that key and nothing else.
		if !users.ValidTimeZone(zone) {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msgSettingInvalid))
			return
		}
		if err := s.users.SaveTimeZone(ctx, u.ID, zone); err != nil {
			s.serverError(w, r, "save time zone", err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cur, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "load settings", err)
		return
	}
	// section says which category the form came from, so a plain post goes back to it. Anything
	// that is not one of the four names is ignored, never echoed into the redirect.
	section := r.PostFormValue("section")
	if _, known := settingsSections[section]; !known {
		section = ""
	}
	next, ok := settingsFromForm(r.PostForm, cur)
	if !ok {
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msgSettingInvalid))
			return
		}
		if section == "" {
			s.settingsPage(w, r, http.StatusUnprocessableEntity, "playback", true, msgSettingInvalid)
			return
		}
		s.settingsPage(w, r, http.StatusUnprocessableEntity, section, false, msgSettingInvalid)
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
	back := "/me/settings"
	if section != "" {
		back += "/" + section
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---- Change password ----

func (s *server) getPassword(w http.ResponseWriter, r *http.Request) {
	s.passwordPage(w, r, http.StatusOK, pages.PasswordView{})
}

func (s *server) passwordPage(w http.ResponseWriter, r *http.Request, status int, v pages.PasswordView) {
	v.CSRF = s.csrf.TokenFrom(r.Context())
	s.prefsPage(w, r, status, "Change password", "profile", "account", false, pages.Password(v), nil, pages.PasswordStyles...)
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
		http.Redirect(w, r, "/me/devices", http.StatusSeeOther)
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
		http.Redirect(w, r, "/me/devices", http.StatusSeeOther)
	}
}

// postSessionName names one of the signed-in person's own browser sign-ins; an empty name clears it and
// the row goes back to the label worked out from the browser. Someone else's id is a 404 page.
func (s *server) postSessionName(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u := currentUser(r)
	sid := r.PathValue("sid")
	name, msg := deviceNameInput(r.PostFormValue("name"))
	if msg != "" {
		s.devicesPage(w, r, http.StatusUnprocessableEntity, sid, name, msg)
		return
	}
	ok, err := s.sessions.Rename(r.Context(), u.ID, sid, name)
	switch {
	case err != nil:
		s.serverError(w, r, "name device", err)
	case !ok:
		s.notFound(w, r)
	default:
		http.Redirect(w, r, "/me/devices", http.StatusSeeOther)
	}
}

// postDeviceName names one of the signed-in person's own phones or desktop apps. A name is required
// there (the table keeps it), so an empty one is refused in plain words. An app key, a revoked device
// and someone else's id are a 404 page.
func (s *server) postDeviceName(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u := currentUser(r)
	did := r.PathValue("did")
	name, msg := deviceNameInput(r.PostFormValue("name"))
	if name == "" {
		msg = devNameEmpty
	}
	if msg != "" {
		s.devicesPage(w, r, http.StatusUnprocessableEntity, did, name, msg)
		return
	}
	ok, err := s.auth.RenameDevice(r.Context(), u.ID, did, name)
	switch {
	case err != nil:
		s.serverError(w, r, "name app", err)
	case !ok:
		s.notFound(w, r)
	default:
		http.Redirect(w, r, "/me/devices", http.StatusSeeOther)
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
	http.Redirect(w, r, "/me/devices", http.StatusSeeOther)
}
