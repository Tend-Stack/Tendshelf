package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// adminStyles are the stylesheets the admin pages add to the shell.
var adminStyles = []string{"css/pages-onboard.css", "css/pages-admin.css"}

func (s *server) registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /hx/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /admin/libraries", s.getAdminLibraries)
	mux.HandleFunc("GET /admin/libraries/folders/new", s.getAddFolder)
	mux.HandleFunc("POST /admin/libraries/folders", s.postAddFolder)
	mux.HandleFunc("POST /admin/libraries/scan", s.postScanAll)
	mux.HandleFunc("POST /admin/libraries/{id}/scan", s.postScanLibrary)
	mux.HandleFunc("GET /admin/folders/{id}/emptied", s.getFolderEmptied)
	mux.HandleFunc("POST /admin/folders/{id}/emptied", s.postFolderEmptied)
	s.registerEbookInbox(mux)
}

// allowLibraries answers 404 unless the signed-in person may manage libraries.
func (s *server) allowLibraries(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageLibraries, share.Target{Kind: share.TargetServer})
}

// audit records one action of the signed-in person. A failure is logged, never shown: the action
// already happened.
func (s *server) audit(ctx context.Context, userID, action, target string, detail any) {
	e := db.AuditEntry{At: time.Now().UnixMilli(), UserID: userID, Action: action, Target: target, Detail: detail}
	if err := db.Audit(ctx, s.lib.Store().Writer(), e); err != nil {
		slog.Error("audit "+action, "err", err)
	}
}

// startScan queues a scan of the library, or returns the one already waiting or running.
func (s *server) startScan(ctx context.Context, userID, libraryID string) (string, error) {
	return scan.Enqueue(ctx, s.jobs, userID, libraryID)
}

// scanView turns a scan job into what the status fragment shows. Active means the job is queued,
// running or held, or it is done but files are still being read ("streaming in").
func (s *server) scanView(ctx context.Context, r *http.Request, j jobs.Job, libraryID string, setup bool) (components.ScanStatusView, error) {
	p, err := s.lib.ScanProgress(ctx, s.scope(r), libraryID)
	if err != nil {
		return components.ScanStatusView{}, err
	}
	v := components.ScanStatusView{JobID: j.ID, Books: p.Books, Setup: setup}
	if p.Files > 0 {
		v.Percent = (p.Files - p.FilesWaiting) * 100 / p.Files
	}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
		v.Active = true
	case jobs.StateDone:
		v.Active = p.FilesWaiting > 0
	default: // failed, cancelled
		v.Failed = true
		v.ErrorText = j.ErrorText
	}
	return v, nil
}

// jobLibrary reads the library id a scan job works on.
func jobLibrary(j jobs.Job) string {
	var p scan.ScanPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return ""
	}
	return p.LibraryID
}

// lastScanText is the line under "books ready to play".
func (s *server) lastScanText(ctx context.Context, libs []library.Library) (string, error) {
	var (
		newest jobs.Job
		found  bool
	)
	for _, l := range libs {
		j, ok, err := s.jobs.LatestFor(ctx, scan.KindScan, "library_id", l.ID)
		if err != nil {
			return "", err
		}
		if ok && (j.State == jobs.StateQueued || j.State == jobs.StateRunning) {
			// A scan in progress says so, instead of "No scan yet." or the previous scan's summary.
			return "Scanning your audiobook folder.", nil
		}
		if ok && j.State == jobs.StateDone && (!found || j.UpdatedAt.After(newest.UpdatedAt)) {
			newest, found = j, true
		}
	}
	if !found {
		return "No scan yet.", nil
	}
	var res scan.Result
	_ = json.Unmarshal(newest.Result, &res)
	added := "no new books"
	if res.BooksAdded > 0 {
		added = countText(res.BooksAdded, "1 new book", "%d new books")
	}
	return "Last scan " + agoText(time.Now(), newest.UpdatedAt) + " found " + added + ".", nil
}

func (s *server) getAdminLibraries(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	s.renderAdminLibraries(w, r, http.StatusOK, "")
}

// renderAdminLibraries draws the Libraries page with a status and, when banner is not empty, that
// line in place of the "marked" one. The caller has already checked access.
func (s *server) renderAdminLibraries(w http.ResponseWriter, r *http.Request, status int, banner string) {
	s.renderAdminLibrariesFor(w, r, status, banner, "", "")
}

// renderAdminLibrariesFor is renderAdminLibraries with a refusal shown under one library's upload
// choice (errLib is its id).
func (s *server) renderAdminLibrariesFor(w http.ResponseWriter, r *http.Request, status int, banner, errLib, errText string) {
	ctx := r.Context()
	sc := s.scope(r)
	view, err := s.librariesView(ctx, r, sc)
	if err != nil {
		s.serverError(w, r, "libraries page", err)
		return
	}
	libs, err := s.lib.Libraries(ctx)
	if err != nil {
		s.serverError(w, r, "libraries", err)
		return
	}
	if view.Lookups, err = s.lookupsView(ctx, r, libs); err != nil {
		s.serverError(w, r, "lookups", err)
		return
	}
	if view.Lookups != nil {
		view.OnlineTitle, view.OnlineBody = adminOnlineTitle, adminOnlineBody
	}
	if s.details != nil && s.can.Can(ctx, currentUser(r), share.ManageServer, share.Target{Kind: share.TargetServer}) {
		view.OnlineTitle, view.OnlineBody = adminOnlineTitle, adminOnlineBody
		on, err := metadata.OnlineEnabled(ctx, s.lib.Store().Reader())
		if err != nil {
			s.serverError(w, r, "online setting", err)
			return
		}
		view.Online = &pages.AdminOnlineView{Title: adminOnlineTitle, Body: adminOnlineBody,
			OnLabel: adminOnlineOn, OffLabel: adminOnlineOff, Save: adminOnlineSave, On: on}
	}
	if view.Watch, err = s.adminWatchView(r); err != nil {
		s.serverError(w, r, "watch setting", err)
		return
	}
	if view.Uploads, err = s.adminUploadsView(ctx, r, errLib, errText); err != nil {
		s.serverError(w, r, "uploads setting", err)
		return
	}
	if m := r.URL.Query().Get("marked"); m != "" {
		var n int
		if _, err := fmt.Sscanf(m, "%d", &n); err == nil && n >= 0 && n < 10_000_000 {
			view.Banner = countText(n, "Marked 1 book as missing.", "Marked %d books as missing.")
		}
	}
	if banner != "" {
		view.Banner = banner
	}
	if s.ebooks != nil {
		view.EbooksHref, view.EbooksLabel = "/admin/ebooks", inboxTitle
	}
	view.DuplicatesHref, view.DuplicatesLabel = "/admin/duplicates", dupTitle
	s.prefsPage(w, r, status, "Libraries & scanning", "admin-libraries", "libraries", false, pages.AdminLibraries(view), nil, adminStyles...)
}

func (s *server) librariesView(ctx context.Context, r *http.Request, sc library.Scope) (pages.AdminLibrariesView, error) {
	v := pages.AdminLibrariesView{CSRF: s.csrf.TokenFrom(ctx)}
	ov, err := s.lib.Overview(ctx, sc)
	if err != nil {
		return v, err
	}
	v.Ready = booksText(ov.Books) + " ready to play"
	libs, err := s.lib.Libraries(ctx)
	if err != nil {
		return v, err
	}
	if v.LastScan, err = s.lastScanText(ctx, libs); err != nil {
		return v, err
	}
	folders, err := s.lib.FolderSummaries(ctx, sc)
	if err != nil {
		return v, err
	}
	views := map[string]*components.ScanStatusView{}
	for _, f := range folders {
		af := pages.AdminFolder{
			ID: f.ID, LibraryID: f.LibraryID, LibraryName: f.LibraryName, Path: f.Path,
			Summary: booksText(f.Books) + " · " + sizeText(f.Bytes),
		}
		if _, ok := views[f.LibraryID]; !ok {
			views[f.LibraryID] = nil
			if j, ok, err := s.jobs.ActiveFor(ctx, scan.KindScan, "library_id", f.LibraryID); err != nil {
				return v, err
			} else if ok {
				sv, err := s.scanView(ctx, r, j, f.LibraryID, false)
				if err != nil {
					return v, err
				}
				views[f.LibraryID] = &sv
			}
		}
		af.Scan = views[f.LibraryID]
		v.Folders = append(v.Folders, af)
	}
	pr, err := s.lib.Problems(ctx, sc, 0)
	if err != nil {
		return v, err
	}
	v.Problems = problemCards(pr)
	if len(v.Problems) > 0 {
		v.ProblemsTitle = problemsTitle(len(v.Problems))
	}
	return v, nil
}

// ---- add a folder ----

// checkFolderPath validates a typed path. It returns the cleaned path and "" when the folder can
// be used, or a message code (path_relative, path_missing, path_unreadable, path_file, path_data).
// Overlap with an existing folder is decided by the repository, in the same transaction as the
// insert.
func (s *server) checkFolderPath(raw string) (string, string) {
	p := strings.TrimSpace(raw)
	if p == "" || !filepath.IsAbs(p) {
		return p, "path_relative"
	}
	p = filepath.Clean(p)
	fi, err := os.Stat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return p, "path_missing"
	case err != nil:
		return p, "path_unreadable"
	case !fi.IsDir():
		return p, "path_file"
	}
	d, err := os.Open(p)
	if err != nil {
		return p, "path_unreadable"
	}
	_, err = d.Readdirnames(1)
	_ = d.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return p, "path_unreadable"
	}
	if overlapsDataDir(p, s.dataDir) {
		return p, "path_data"
	}
	return p, ""
}

// overlapsDataDir reports whether p equals, lies inside or contains the data directory, after
// resolving symbolic links on both.
func overlapsDataDir(p, data string) bool {
	resolve := func(x string) string {
		if r, err := filepath.EvalSymlinks(x); err == nil {
			x = r
		}
		if a, err := filepath.Abs(x); err == nil {
			x = a
		}
		return filepath.Clean(x)
	}
	a, b := resolve(p), resolve(data)
	sep := string(filepath.Separator)
	within := func(child, parent string) bool {
		return child == parent || strings.HasPrefix(child, strings.TrimSuffix(parent, sep)+sep)
	}
	return within(a, b) || within(b, a)
}

func (s *server) addFolderPage(w http.ResponseWriter, r *http.Request, status int, v pages.AdminAddFolderView) {
	s.addFolderPageSheet(w, r, status, v, nil)
}

// addFolderPageSheet is addFolderPage with the folder browser sheet (or nil) in #sheet.
func (s *server) addFolderPageSheet(w http.ResponseWriter, r *http.Request, status int, v pages.AdminAddFolderView, sheet templ.Component) {
	v.BrowseURL = browseFieldURL(addFolderURL, v.Path)
	v.CSRF = s.csrf.TokenFrom(r.Context())
	libs, err := s.lib.Libraries(r.Context())
	if err != nil {
		s.serverError(w, r, "libraries", err)
		return
	}
	for _, l := range libs {
		v.Libraries = append(v.Libraries, pages.AdminLibraryOption{ID: l.ID, Name: l.Name})
	}
	if v.Auto, err = s.autoLookupField(r, "auto-lookup", v.AutoOn); err != nil {
		s.serverError(w, r, "online setting", err)
		return
	}
	s.prefsPage(w, r, status, "Add a folder", "admin-libraries", "libraries", false, pages.AdminAddFolder(v), sheet, folderStyles...)
}

func (s *server) getAddFolder(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	v := pages.AdminAddFolderView{}
	if n, err := s.lib.Libraries(r.Context()); err == nil && len(n) > 0 {
		v.Library = n[0].ID
	}
	if folder := folderPrefill(r); folder != "" {
		v.Path, v.NewName = folder, libraryNameFor(folder)
	}
	if fb, ok := s.folderBrowser(r, addFolderURL); ok {
		fb.Sheet = true
		fb.Styles = s.sheetStyles(folderSheetStyles...)
		s.addFolderPageSheet(w, r, http.StatusOK, v, components.FolderBrowserSheet(fb))
		return
	}
	s.addFolderPage(w, r, http.StatusOK, v)
}

// libraryNameFor is the name of a library created from a folder: the folder's own name, or
// "Audiobooks" for the file system root.
func libraryNameFor(path string) string {
	name := filepath.Base(path)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "Audiobooks"
	}
	if utf8.RuneCountInString(name) > 200 {
		name = string([]rune(name)[:200])
	}
	return name
}

func (s *server) postAddFolder(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	v := pages.AdminAddFolderView{
		Path: strings.TrimSpace(r.PostFormValue("folder")), Library: r.PostFormValue("library"),
		NewName: strings.TrimSpace(r.PostFormValue("new_name")),
		AutoOn:  r.PostFormValue("auto_lookup") == "1",
	}
	invalid := func(code string) {
		if code == "name_required" {
			v.NameError = pathMessage(code)
		} else {
			v.PathError = pathMessage(code)
		}
		s.addFolderPage(w, r, http.StatusUnprocessableEntity, v)
	}
	path, code := s.checkFolderPath(v.Path)
	if code != "" {
		invalid(code)
		return
	}
	var (
		lib        library.Library
		folder     library.Folder
		err        error
		newLibrary = v.Library == ""
	)
	if newLibrary {
		name := v.NewName
		if name == "" {
			name = libraryNameFor(path)
		}
		if utf8.RuneCountInString(name) > 200 {
			invalid("name_required")
			return
		}
		lib, folder, err = s.lib.CreateLibraryWithFolder(ctx, name, path)
	} else {
		if lib, err = s.lib.Library(ctx, v.Library); errors.Is(err, library.ErrNotFound) {
			s.notFound(w, r)
			return
		} else if err == nil {
			folder, err = s.lib.AddFolder(ctx, lib.ID, path)
		}
	}
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
	s.audit(ctx, u.ID, "library.folder_added", folder.ID, map[string]any{"library_id": lib.ID, "new_library": newLibrary})
	if err := s.applyAutoLookup(r, lib.ID); err != nil {
		s.serverError(w, r, "auto lookup", err)
		return
	}
	if _, err := s.startScan(ctx, u.ID, lib.ID); err != nil {
		s.serverError(w, r, "queue scan", err)
		return
	}
	s.answerPage(w, r, "/admin/libraries")
}

// ---- scanning ----

func (s *server) postScanAll(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	libs, err := s.lib.Libraries(r.Context())
	if err != nil {
		s.serverError(w, r, "libraries", err)
		return
	}
	for _, l := range libs {
		if _, err := s.startScan(r.Context(), currentUser(r).ID, l.ID); err != nil {
			s.serverError(w, r, "queue scan", err)
			return
		}
	}
	s.answerPage(w, r, "/admin/libraries")
}

func (s *server) postScanLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	l, err := s.lib.Library(r.Context(), r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load library", err)
		return
	}
	if _, err := s.startScan(r.Context(), currentUser(r).ID, l.ID); err != nil {
		s.serverError(w, r, "queue scan", err)
		return
	}
	s.answerPage(w, r, "/admin/libraries")
}

// ---- folder emptied on purpose ----

// Folder states for the "emptied on purpose" page.
const (
	emptiedReady       = "ready"
	emptiedUnavailable = "unavailable"
	emptiedAudio       = "audio"
	emptiedNothing     = "nothing"
)

// emptiedState decides what the page can do: mark the books only when the whole folder was read and
// no audio or protected file is left.
func (s *server) emptiedState(ctx context.Context, f library.Folder) (string, int, error) {
	n, err := s.lib.PresentBooksInFolder(ctx, f.ID)
	if err != nil {
		return "", 0, err
	}
	if n == 0 {
		return emptiedNothing, 0, nil
	}
	has, err := scan.FolderHasAudio(ctx, f.Path)
	switch {
	case errors.Is(err, scan.ErrFolderUnavailable):
		return emptiedUnavailable, n, nil
	case err != nil:
		return "", 0, err
	case has:
		return emptiedAudio, n, nil
	}
	return emptiedReady, n, nil
}

func (s *server) emptiedPage(w http.ResponseWriter, r *http.Request, status int, f library.Folder, state string, n int) {
	v := pages.AdminFolderEmptiedView{CSRF: s.csrf.TokenFrom(r.Context()), ID: f.ID, Path: f.Path, State: state, Books: n}
	switch state {
	case emptiedReady:
		v.Title = "Was this folder emptied on purpose?"
		v.Body = "Tendshelf found no audiobooks in " + f.Path + ". If you moved or deleted them on purpose, mark their " +
			countText(n, "1 book", "%d books") + " as missing. Listening history is kept, and the books come back if their files return."
		v.Action = countText(n, "Mark 1 book missing", "Mark %d books missing")
	case emptiedUnavailable:
		v.Title = "Tendshelf can't open this folder"
		v.Body = "Tendshelf can't open " + f.Path + " right now, so it won't mark anything. " +
			"If it's on a drive that isn't connected, connect it and scan again."
	case emptiedAudio:
		v.Title = "This folder still has audiobooks"
		v.Body = f.Path + " still has audiobooks in it, so nothing needs marking. Use Scan now to pick up changes."
	default:
		v.Title = "Nothing to mark"
		v.Body = "No books in this folder need marking."
	}
	s.prefsPage(w, r, status, v.Title, "admin-libraries", "libraries", false, pages.AdminFolderEmptied(v), nil, adminStyles...)
}

func (s *server) loadFolder(w http.ResponseWriter, r *http.Request) (library.Folder, bool) {
	f, err := s.lib.Folder(r.Context(), r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return library.Folder{}, false
	}
	if err != nil {
		s.serverError(w, r, "load folder", err)
		return library.Folder{}, false
	}
	return f, true
}

func (s *server) getFolderEmptied(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	f, ok := s.loadFolder(w, r)
	if !ok {
		return
	}
	state, n, err := s.emptiedState(r.Context(), f)
	if err != nil {
		s.serverError(w, r, "check folder", err)
		return
	}
	s.emptiedPage(w, r, http.StatusOK, f, state, n)
}

func (s *server) postFolderEmptied(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) {
		return
	}
	f, ok := s.loadFolder(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	state, n, err := s.emptiedState(ctx, f)
	if err != nil {
		s.serverError(w, r, "check folder", err)
		return
	}
	if state != emptiedReady {
		s.emptiedPage(w, r, http.StatusConflict, f, state, n)
		return
	}
	var marked int
	err = s.lib.Update(ctx, func(tx *library.Tx) error {
		var err error
		marked, err = tx.MarkFolderBooksMissing(f.ID, time.Now().UnixMilli())
		return err
	})
	if err != nil {
		s.serverError(w, r, "mark books missing", err)
		return
	}
	s.audit(ctx, currentUser(r).ID, "library.folder_emptied", f.ID, map[string]int{"books": marked})
	s.answerPage(w, r, fmt.Sprintf("/admin/libraries?marked=%d", marked))
}

// booksText is "1 book" or "1,204 books".
func booksText(n int) string {
	if n == 1 {
		return "1 book"
	}
	return thousands(n) + " books"
}
