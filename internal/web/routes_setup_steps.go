package web

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Settings written by the setup steps.
const (
	settingServerName = "server.name"
	settingSetupDone  = "setup.done"
)

// The first run is account (/setup), server name, folder, done. Steps 2 and 3 belong to the owner
// who just signed in: every route needs share.ManageServer, so everyone else gets a 404 and a
// visitor who is not signed in is sent to sign in by the gate.
func (s *server) registerSetupSteps(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup/server", s.getSetupServer)
	mux.HandleFunc("POST /setup/server", s.postSetupServer)
	mux.HandleFunc("GET /setup/folder", s.getSetupFolder)
	mux.HandleFunc("POST /setup/folder", s.postSetupFolder)
	mux.HandleFunc("GET /setup/done", s.getSetupDone)
}

func (s *server) allowSetup(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer})
}

func (s *server) setupFinished(r *http.Request) (bool, error) {
	var done bool
	ok, err := db.GetSetting(r.Context(), s.lib.Store().Reader(), settingSetupDone, &done)
	return ok && done, err
}

// setupGuard runs the checks every step shares. It returns false once it has answered: a 404 for
// anyone but the owner, a redirect home when setup is already finished (unless keepOpen), or a 500.
func (s *server) setupGuard(w http.ResponseWriter, r *http.Request, keepOpen bool) bool {
	if !s.allowSetup(w, r) {
		return false
	}
	done, err := s.setupFinished(r)
	if err != nil {
		s.serverError(w, r, "setup state", err)
		return false
	}
	if done && !keepOpen {
		s.redirect(w, r, "/")
		return false
	}
	return true
}

// ---- step 2: server name ----

func (s *server) getSetupServer(w http.ResponseWriter, r *http.Request) {
	if !s.setupGuard(w, r, false) {
		return
	}
	var name string
	_, _ = db.GetSetting(r.Context(), s.lib.Store().Reader(), settingServerName, &name)
	s.frame(w, r, http.StatusOK, "Set up your shelf", pages.SetupServer(pages.SetupServerView{
		CSRF: s.csrf.TokenFrom(r.Context()), Name: name,
	}))
}

// validServerName trims name and checks it: 1 to 80 characters, no control characters. The second
// result is the message, empty when the name is fine.
func validServerName(name string) (string, string) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return name, msgServerNameRequired
	case utf8.RuneCountInString(name) > 80:
		return name, msgServerNameTooLong
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return name, msgServerNameRequired
		}
	}
	return name, ""
}

func (s *server) postSetupServer(w http.ResponseWriter, r *http.Request) {
	if !s.setupGuard(w, r, false) {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	name, msg := validServerName(r.PostFormValue("name"))
	if msg != "" {
		if utf8.RuneCountInString(name) > 200 {
			name = ""
		}
		s.frame(w, r, http.StatusUnprocessableEntity, "Set up your shelf", pages.SetupServer(pages.SetupServerView{
			CSRF: s.csrf.TokenFrom(ctx), Name: name, Error: msg,
		}))
		return
	}
	if err := db.PutSetting(ctx, s.lib.Store().Writer(), settingServerName, name); err != nil {
		s.serverError(w, r, "save server name", err)
		return
	}
	s.audit(ctx, currentUser(r).ID, "setup.server_named", "server", nil)
	s.redirect(w, r, "/setup/folder")
}

// ---- step 3: folder ----

func (s *server) folderStep(w http.ResponseWriter, r *http.Request, status int, v pages.SetupFolderView) {
	v.CSRF = s.csrf.TokenFrom(r.Context())
	if !v.Added {
		v.BrowseURL = browseFieldURL(setupFolderURL, v.Path)
	}
	s.frame(w, r, status, "Where are your audiobooks?", pages.SetupFolder(v), "css/pages-admin.css", "css/pages-folders.css")
}

func (s *server) getSetupFolder(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetup(w, r) {
		return
	}
	done, err := s.setupFinished(r)
	if err != nil {
		s.serverError(w, r, "setup state", err)
		return
	}
	// With ?job= the page shows the scan that setup started, even after setup is finished.
	if jobID := r.URL.Query().Get("job"); jobID != "" {
		if j, libID, ok, err := s.scanJobFor(r, jobID); err != nil {
			s.serverError(w, r, "load job", err)
			return
		} else if ok {
			sv, err := s.scanView(r.Context(), r, j, libID, true)
			if err != nil {
				s.serverError(w, r, "scan status", err)
				return
			}
			folders, err := s.lib.Folders(r.Context(), libID)
			if err != nil {
				s.serverError(w, r, "load folders", err)
				return
			}
			v := pages.SetupFolderView{Added: true, Scan: &sv}
			if len(folders) > 0 {
				v.Path = folders[0].Path
			}
			s.folderStep(w, r, http.StatusOK, v)
			return
		}
	}
	if done {
		s.redirect(w, r, "/")
		return
	}
	v := pages.SetupFolderView{Path: folderPrefill(r)}
	if fb, ok := s.folderBrowser(r, setupFolderURL); ok {
		v.Browser = &fb
	}
	s.folderStep(w, r, http.StatusOK, v)
}

func (s *server) postSetupFolder(w http.ResponseWriter, r *http.Request) {
	if !s.setupGuard(w, r, false) {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	raw := strings.TrimSpace(r.PostFormValue("folder"))
	if utf8.RuneCountInString(raw) > 4096 {
		raw = ""
	}
	invalid := func(code string) {
		s.folderStep(w, r, http.StatusUnprocessableEntity, pages.SetupFolderView{Path: raw, Error: pathMessage(code)})
	}
	path, code := s.checkFolderPath(raw)
	if code != "" {
		invalid(code)
		return
	}
	lib, folder, err := s.lib.CreateLibraryWithFolder(ctx, libraryNameFor(path), path)
	switch {
	case errors.Is(err, library.ErrFolderOverlap), errors.Is(err, library.ErrConflict):
		invalid("path_overlap")
		return
	case errors.Is(err, library.ErrInvalid):
		invalid("path_relative")
		return
	case err != nil:
		s.serverError(w, r, "add folder", err)
		return
	}
	jobID, err := s.startScan(ctx, u.ID, lib.ID)
	if err != nil {
		s.serverError(w, r, "queue scan", err)
		return
	}
	if err := db.PutSetting(ctx, s.lib.Store().Writer(), settingSetupDone, true); err != nil {
		s.serverError(w, r, "finish setup", err)
		return
	}
	s.audit(ctx, u.ID, "library.folder_added", folder.ID, map[string]any{"library_id": lib.ID, "new_library": true})
	s.redirect(w, r, "/setup/folder?job="+url.QueryEscape(jobID))
}

// ---- done ----

// setupWallSize is the most covers the finished page shows behind its headline.
const setupWallSize = 18

func (s *server) getSetupDone(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetup(w, r) {
		return
	}
	ov, err := s.lib.Overview(r.Context(), s.scope(r))
	if err != nil {
		s.serverError(w, r, "overview", err)
		return
	}
	v := pages.SetupDoneView{Empty: ov.Books == 0}
	// The cover wall: the newest books that have a cover file, drawn as decoration.
	wall, err := s.lib.ListBooks(r.Context(), s.scope(r), library.ListQuery{Sort: library.SortAdded, Limit: setupWallSize, HasCover: true})
	if err != nil {
		s.serverError(w, r, "setup wall", err)
		return
	}
	for _, b := range wall.Books {
		v.Wall = append(v.Wall, cardView(b))
	}
	if !v.Empty {
		hours := int(math.Round(float64(ov.DurationMs) / 3_600_000))
		v.Books = booksText(ov.Books) + ", all in one place"
		// Zero counts are left out: "0 series · 0 narrators" reads as a fault, not a summary.
		parts := []string{hoursText(hours) + " of listening"}
		if ov.Series > 0 {
			parts = append(parts, countText(ov.Series, "1 series", "%d series"))
		}
		if ov.Narrators > 0 {
			parts = append(parts, countText(ov.Narrators, "1 narrator", "%d narrators"))
		}
		v.Stats = strings.Join(parts, " · ") + ". Covers and details came from your files."
	}
	if ov.Problems > 0 {
		v.Problems = countText(ov.Problems, "1 book needs a look.", "%d books need a look.")
	}
	s.frame(w, r, http.StatusOK, "Your shelf is ready", pages.SetupDone(v))
}

// hoursText is "1 hour" or "1,204 hours".
func hoursText(h int) string {
	if h == 1 {
		return "1 hour"
	}
	return thousands(h) + " hours"
}
