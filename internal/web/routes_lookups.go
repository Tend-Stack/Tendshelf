package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Automatic and bulk lookups (AL-4): the per-library setting, "Look up all books", its status and the
// list of books that need a look. Every route needs share.ManageLibraries on the library; a refusal,
// an unknown library or book, a book of another library and lookups being off all answer 404.

func (s *server) registerLookups(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/libraries/{id}/auto-lookup", s.postAutoLookup)
	mux.HandleFunc("POST /admin/libraries/{id}/lookups", s.postBulkStart)
	mux.HandleFunc("POST /admin/libraries/{id}/lookups/stop", s.postBulkStop)
	mux.HandleFunc("GET /admin/libraries/{id}/lookups", s.getNeedsLook)
	mux.HandleFunc("POST /admin/libraries/{id}/lookups/{book}/dismiss", s.postLookupDismiss)
}

// lookupLibrary loads the library of a lookup route: 404 unless lookups are on, the library exists
// and the person may manage it.
func (s *server) lookupLibrary(w http.ResponseWriter, r *http.Request, id string) (library.Library, bool) {
	on, err := s.lookupsOn(r)
	if err != nil {
		s.serverError(w, r, "online setting", err)
		return library.Library{}, false
	}
	if !on {
		s.notFound(w, r)
		return library.Library{}, false
	}
	l, err := s.lib.Library(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return library.Library{}, false
	}
	if err != nil {
		s.serverError(w, r, "load library", err)
		return library.Library{}, false
	}
	if !s.allow(w, r, share.ManageLibraries, share.Target{Kind: share.TargetLibrary, ID: l.ID, LibraryID: l.ID}) {
		return library.Library{}, false
	}
	return l, true
}

// lookupFailed answers 404 for an unknown library or lookups being off, and a 500 for any other failure.
func (s *server) lookupFailed(w http.ResponseWriter, r *http.Request, what string, err error) {
	if errors.Is(err, library.ErrNotFound) || errors.Is(err, metadata.ErrOnlineOff) {
		s.notFound(w, r)
		return
	}
	s.serverError(w, r, what, err)
}

func (s *server) postAutoLookup(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lookupLibrary(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	// The form sends a hidden 0 before the checkbox's 1, so the last value is the answer.
	vals := r.PostForm["on"]
	on := ""
	if len(vals) > 0 {
		on = vals[len(vals)-1]
	}
	if on != "0" && on != "1" {
		http.Error(w, detailsFailed, http.StatusUnprocessableEntity)
		return
	}
	if err := s.details.SetAutoLookup(r.Context(), l.ID, on == "1", currentUser(r).ID); err != nil {
		s.lookupFailed(w, r, "auto lookup", err)
		return
	}
	s.answerPage(w, r, "/admin/libraries")
}

func (s *server) postBulkStart(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lookupLibrary(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	if _, err := s.details.StartBulk(r.Context(), l.ID, r.PostFormValue("more") == "1", currentUser(r).ID); err != nil {
		s.lookupFailed(w, r, "start lookups", err)
		return
	}
	s.answerPage(w, r, "/admin/libraries")
}

func (s *server) postBulkStop(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lookupLibrary(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.details.StopBulk(r.Context(), l.ID, currentUser(r).ID); err != nil {
		s.lookupFailed(w, r, "stop lookups", err)
		return
	}
	s.answerPage(w, r, "/admin/libraries")
}

func needsLookHref(libraryID string) string { return "/admin/libraries/" + libraryID + "/lookups" }

func (s *server) getNeedsLook(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lookupLibrary(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	rows, total, err := s.lib.NeedsLook(r.Context(), l.ID, lookupNeedsMaxRows)
	if err != nil {
		s.serverError(w, r, "needs a look", err)
		return
	}
	v := pages.AdminLookupsPageView{CSRF: s.csrf.TokenFrom(r.Context()), Title: lookupNeedsTitle, LibraryName: l.Name,
		BackLabel: lookupNeedsBack, Empty: lookupNeedsEmpty}
	for _, row := range rows {
		outcome := lookupOutcomeMany
		if row.Outcome == library.LookupNoMatch {
			outcome = lookupOutcomeNone
		}
		v.Rows = append(v.Rows, pages.LookupBookRow{Title: row.Title, Author: row.Author, Outcome: outcome,
			CompareHref: lookupHref(row.BookID), CompareLabel: lookupCompare,
			DismissAction: needsLookHref(l.ID) + "/" + row.BookID + "/dismiss", DismissLabel: lookupLeave})
	}
	if total > len(rows) {
		v.More = lookupMoreText(total - len(rows))
	}
	s.pageStyled(w, r, http.StatusOK, lookupNeedsTitle, "admin-libraries", pages.AdminLookups(v), nil, adminStyles...)
}

func (s *server) postLookupDismiss(w http.ResponseWriter, r *http.Request) {
	l, ok := s.lookupLibrary(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	b, err := s.lib.Book(r.Context(), r.PathValue("book"))
	if errors.Is(err, library.ErrNotFound) || (err == nil && b.LibraryID != l.ID) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load book", err)
		return
	}
	if err := s.details.DismissLookup(r.Context(), b.ID, currentUser(r).ID); err != nil {
		s.lookupFailed(w, r, "dismiss lookup", err)
		return
	}
	s.answerPage(w, r, needsLookHref(l.ID))
}

// getBulkJob is the status fragment of a bulk lookup. The job's library must be one the person may
// manage; anything else is "no such job".
func (s *server) getBulkJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	var p metadata.BulkPayload
	if json.Unmarshal(j.Payload, &p) != nil || p.LibraryID == "" {
		s.notFound(w, r)
		return
	}
	l, ok := s.lookupLibrary(w, r, p.LibraryID)
	if !ok {
		return
	}
	v, err := s.bulkView(r.Context(), r, j, l.ID)
	if err != nil {
		s.serverError(w, r, "bulk lookup status", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.LookupBulkStatus(v))
}

// bulkView turns a bulk job into what the status shows.
func (s *server) bulkView(ctx context.Context, r *http.Request, j jobs.Job, libraryID string) (components.LookupBulkView, error) {
	p, err := metadata.ParseBulkProgress(j)
	if err != nil {
		return components.LookupBulkView{}, err
	}
	v := components.LookupBulkView{JobID: j.ID}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
		v.Active = true
		v.Progress = lookupProgressText(p)
		if p.Total > 0 {
			v.Percent = min(100, p.Done*100/p.Total)
		}
		if p.PausedUntil > time.Now().UnixMilli() {
			v.Status = lookupPauseText(p.PauseReason, time.UnixMilli(p.PausedUntil))
		}
		v.StopAction = needsLookHref(libraryID) + "/stop"
		v.StopLabel = lookupStop
		v.CSRF = s.csrf.TokenFrom(ctx)
	case jobs.StateDone:
		v.Status = lookupDoneText(p)
	case jobs.StateCancelled:
		v.Status = lookupStoppedText(p)
	default:
		v.Failed = true
		if j.ErrorCode == "provider_refusing" {
			v.Status = lookupRefusing
		} else if v.Status = j.ErrorText; v.Status == "" {
			v.Status = lookupStopped
		}
	}
	return v, nil
}

// lookupsView builds the library rows of "Book details from the internet", or nil when lookups
// are off.
func (s *server) lookupsView(ctx context.Context, r *http.Request, libs []library.Library) (*pages.AdminLookupsView, error) {
	on, err := s.lookupsOn(r)
	if err != nil || !on {
		return nil, err
	}
	v := &pages.AdminLookupsView{AutoLabel: lookupAutoLabel, AutoHint: lookupAutoHint, Save: lookupAutoSave,
		StartLabel: lookupStart, MoreLabel: detailsMoreLabel}
	for _, l := range libs {
		row := pages.AdminLookupLibrary{ID: l.ID, Name: l.Name, Auto: l.AutoLookupAt > 0}
		j, found, err := s.latestBulk(ctx, l.ID)
		if err != nil {
			return nil, err
		}
		if found {
			sv, err := s.bulkView(ctx, r, j, l.ID)
			if err != nil {
				return nil, err
			}
			row.Status = &sv
		}
		if _, total, err := s.lib.NeedsLook(ctx, l.ID, 0); err != nil {
			return nil, err
		} else if total > 0 {
			row.NeedsHref, row.NeedsLabel = needsLookHref(l.ID), lookupNeedsLabel(total)
		}
		v.Libraries = append(v.Libraries, row)
	}
	return v, nil
}

func (s *server) latestBulk(ctx context.Context, libraryID string) (jobs.Job, bool, error) {
	return s.jobs.LatestFor(ctx, metadata.KindOnlineBulk, "library_id", libraryID)
}

// autoLookupField is the checkbox of Add a folder and the setup step, or nil when lookups are off.
func (s *server) autoLookupField(r *http.Request, id string, on bool) (*components.AutoLookupView, error) {
	ok, err := s.lookupsOn(r)
	if err != nil || !ok {
		return nil, err
	}
	return &components.AutoLookupView{ID: id, Label: lookupAutoLabel, Hint: lookupAutoHint, On: on}, nil
}

// applyAutoLookup turns "look up details online for new books" on for a new folder's library when the
// box was ticked. An unticked box never turns a library's setting off. It is called before the scan
// starts, so the first books are already counted as new.
func (s *server) applyAutoLookup(r *http.Request, libraryID string) error {
	if r.PostFormValue("auto_lookup") != "1" {
		return nil
	}
	on, err := s.lookupsOn(r)
	if err != nil || !on {
		return err
	}
	return s.details.SetAutoLookup(r.Context(), libraryID, true, currentUser(r).ID)
}
