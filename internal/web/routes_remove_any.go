package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Removing any book (decision 0045): the owner and admins may remove a book whose files were put in the library
// folder by hand. The files move to a holding folder and stay there for 30 days; "Put back" returns them. The
// confirm page is GET /books/{id}/remove (routes_upload_pages.go) when the upload removal is not offered. Every
// route here checks share.RemoveLibraryFiles and answers 404 to everyone else. The file work is all in
// internal/upload; nothing here touches a file.

func (s *server) registerRemoveAny(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/remove-files", s.postBookRemoveFiles)
	mux.HandleFunc("GET /removals/{rid}", s.getRemoval)
	mux.HandleFunc("GET /hx/removals/{rid}", s.getRemovalFragment)
	mux.HandleFunc("POST /removals/{rid}/restore", s.postRemovalRestore)
	mux.HandleFunc("POST /removals/{rid}/purge", s.postRemovalPurge)
	mux.HandleFunc("GET /admin/libraries/{id}/removed", s.getRemovedBooks)
}

func libraryTarget(id string) share.Target {
	return share.Target{Kind: share.TargetLibrary, ID: id, LibraryID: id}
}

// renderRemoveAny is the confirm page of a book: the exact files, with sizes, and the words around them. notice is
// a refusal shown above the fresh list; the status is the one the refusal had.
func (s *server) renderRemoveAny(w http.ResponseWriter, r *http.Request, b library.Book, status int, notice string) {
	plan, err := s.uploads.PlanRemoval(r.Context(), b.ID)
	v := pages.BookRemoveAnyView{
		Title: removeAnyTitle, Cancel: removeAnyCancel, CancelHref: "/books/" + b.ID, Notice: notice,
	}
	var ue *upload.Error
	switch {
	case errors.As(err, &ue) && ue.Status == http.StatusNotFound:
		s.notFound(w, r)
		return
	case errors.As(err, &ue):
		// The disk does not let the book be removed (a link in the way, no file recorded, a changed file).
		v.Error, v.Notice, status = ue.Message, "", ue.Status
	case err != nil:
		s.serverError(w, r, "remove book", err)
		return
	default:
		v.Body, v.Loss, v.Check = removeAnyBody, removeAnyLoss, removeAnyCheck
		v.FilesButton, v.KeepButton, v.KeepHelp = removeAnyButton, removeAnyKeep, removeAnyKeepHelp
		v.Action, v.CSRF, v.Hash = "/books/"+b.ID+"/remove-files", s.csrf.TokenFrom(r.Context()), plan.Hash
		for _, f := range append(append([]upload.PlanFile{}, plan.Files...), plan.Extras...) {
			v.Files = append(v.Files, pages.RemoveAnyFile{Line: fmt.Sprintf(removeAnyFileLine, f.Rel, sizeText(f.Size))})
		}
		n := len(plan.Files) + len(plan.Extras)
		v.Total = fmt.Sprintf(removeAnyTotal, n, sizeText(plan.Bytes))
		if n == 1 {
			v.Total = fmt.Sprintf(removeAnyTotalOne, sizeText(plan.Bytes))
		}
		if plan.Staying > 0 {
			v.Staying = countText(plan.Staying, removeAnyStayingOne, removeAnyStaying)
		}
	}
	s.pageStyled(w, r, status, removeAnyTitle, "library", pages.BookRemoveAny(v), nil, uploadStyles...)
}

// removeAnyFailed answers a refusal. An htmx request gets a toast (htmx 4 swaps 4xx answers); a plain post gets
// the confirm page again with the fresh list under the message.
func (s *server) removeAnyFailed(w http.ResponseWriter, r *http.Request, b library.Book, err error) {
	var ue *upload.Error
	if !errors.As(err, &ue) || ue.Status == http.StatusNotFound || r.Header.Get("HX-Request") == "true" {
		s.removeFailed(w, r, err)
		return
	}
	s.renderRemoveAny(w, r, b, ue.Status, ue.Message)
}

// postBookRemoveFiles starts the removal the person confirmed: the plan's hash, the box, and the mode.
func (s *server) postBookRemoveFiles(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		s.notFound(w, r)
		return
	}
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.RemoveLibraryFiles)
	if !ok {
		return
	}
	mode := r.FormValue("mode")
	if mode == upload.ModeFiles && r.FormValue("understand") != "1" {
		s.removeAnyFailed(w, r, b, &upload.Error{Status: http.StatusUnprocessableEntity, Code: upload.CodeNotConfirmed, Message: removeAnyNotConfirmed})
		return
	}
	rid, err := s.uploads.StartRemoval(r.Context(), currentUser(r).ID, b.ID, r.FormValue("plan"), mode)
	if err != nil {
		s.removeAnyFailed(w, r, b, err)
		return
	}
	s.answerPage(w, r, "/removals/"+rid)
}

// loadRemoval loads a removal the person may manage; anything else answers 404.
func (s *server) loadRemoval(w http.ResponseWriter, r *http.Request) (upload.Removal, bool) {
	if s.uploads == nil {
		s.notFound(w, r)
		return upload.Removal{}, false
	}
	rm, err := s.uploads.Removal(r.Context(), r.PathValue("rid"))
	var ue *upload.Error
	if errors.As(err, &ue) && ue.Status == http.StatusNotFound {
		s.notFound(w, r)
		return rm, false
	} else if err != nil {
		s.serverError(w, r, "removal", err)
		return rm, false
	}
	if !s.allow(w, r, share.RemoveLibraryFiles, libraryTarget(rm.LibraryID)) {
		return rm, false
	}
	return rm, true
}

// removalView is what a removal's status shows, in plain words.
func (s *server) removalView(r *http.Request, rm upload.Removal) pages.RemovalView {
	v := pages.RemovalView{
		ID: rm.ID, State: rm.State, Title: rm.Title, CSRF: s.csrf.TokenFrom(r.Context()),
		PutBack: removeAnyPutBack, PutBackAria: fmt.Sprintf(removeAnyPutBackAria, rm.Title),
		Delete: removeAnyDeleteNow, DeleteAria: fmt.Sprintf(removeAnyDeleteAria, rm.Title), DeleteCheck: removeAnyDeleteCheck,
		ShowAgain: removeAnyShowAgain, ShowAgainAria: fmt.Sprintf(removeAnyShowAgainAria, rm.Title),
		BackHref: "/libraries/" + rm.LibraryID, Back: removeAnyBack,
	}
	size := fmt.Sprintf(removeAnyTotal, rm.Files, sizeText(rm.Bytes))
	if rm.Files == 1 {
		size = fmt.Sprintf(removeAnyTotalOne, sizeText(rm.Bytes))
	}
	switch rm.State {
	case upload.RemovalPlanned, upload.RemovalMoving:
		v.State, v.Line, v.Poll = "moving", removeAnyMoving, "/hx/removals/"+rm.ID
	case upload.RemovalHeld:
		if rm.Mode == upload.ModeKeepFiles {
			v.State, v.Line = "kept_out", removeAnyKeptOut
			break
		}
		v.CanDelete, v.Meta = true, size
		until := time.Now().Add(upload.RemovedKeepDays * 24 * time.Hour)
		if rm.PurgeAfter > 0 {
			until = time.UnixMilli(rm.PurgeAfter)
		}
		v.Line = fmt.Sprintf(removeAnyHeld, until.Format("2 Jan 2006"))
	case upload.RemovalRestored:
		v.Line = removeAnyRestored
	case upload.RemovalPurged:
		v.Line = removeAnyGone
	default:
		v.State, v.Title, v.Line = "failed", rm.Title, removeAnyMessage(rm.ErrorCode)
		v.Detail = removeAnyFailedTitle
	}
	return v
}

func (s *server) getRemoval(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.loadRemoval(w, r)
	if !ok {
		return
	}
	v := s.removalView(r, rm)
	if r.URL.Query().Get("done") == "restored" {
		v.Banner = removeAnyRestored
		if n, err := strconv.Atoi(r.URL.Query().Get("kept")); err == nil && n > 0 && n < 100000 {
			v.Banner = countText(n, removeAnyRestoredKeptOne, removeAnyRestoredKept)
		}
	}
	s.pageStyled(w, r, http.StatusOK, removeAnyPageTitle, "library", pages.RemovalPage(v), nil, uploadStyles...)
}

// getRemovalFragment is the status the page polls while the files move.
func (s *server) getRemovalFragment(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.loadRemoval(w, r)
	if !ok {
		return
	}
	s.fragment(w, r, http.StatusOK, pages.RemovalStatus(s.removalView(r, rm)))
}

// removalDone is where a restore or a purge ends: the removed books list when the action came from there, the
// removal's own page otherwise.
func removalDone(r *http.Request, rm upload.Removal, done string, kept int) string {
	q := url.Values{"done": {done}}
	if kept > 0 {
		q.Set("kept", strconv.Itoa(kept))
	}
	if r.FormValue("next") == "list" {
		return "/admin/libraries/" + rm.LibraryID + "/removed?" + q.Encode()
	}
	return "/removals/" + rm.ID + "?" + q.Encode()
}

func (s *server) postRemovalRestore(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.loadRemoval(w, r)
	if !ok {
		return
	}
	res, err := s.uploads.RestoreRemoval(r.Context(), currentUser(r).ID, rm.ID)
	if err != nil {
		s.removeFailed(w, r, err)
		return
	}
	s.answerPage(w, r, removalDone(r, rm, "restored", len(res.Kept)))
}

func (s *server) postRemovalPurge(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.loadRemoval(w, r)
	if !ok {
		return
	}
	if r.FormValue("understand") != "1" {
		s.removeFailed(w, r, &upload.Error{Status: http.StatusUnprocessableEntity, Code: upload.CodeNotConfirmed, Message: removeAnyNotConfirmed})
		return
	}
	if err := s.uploads.PurgeRemoval(r.Context(), currentUser(r).ID, rm.ID); err != nil {
		s.removeFailed(w, r, err)
		return
	}
	s.answerPage(w, r, removalDone(r, rm, "purged", 0))
}

// getRemovedBooks lists the removals of one library that are waiting: files in the holding folder, and books
// whose files were left where they were.
func (s *server) getRemovedBooks(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		s.notFound(w, r)
		return
	}
	lib, err := s.lib.Library(r.Context(), r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, "removed books", err)
		return
	}
	if !s.allowLibraries(w, r) || !s.allow(w, r, share.RemoveLibraryFiles, libraryTarget(lib.ID)) {
		return
	}
	held, err := s.uploads.HeldRemovals(r.Context(), lib.ID)
	if err != nil {
		s.serverError(w, r, "removed books", err)
		return
	}
	v := pages.AdminRemovedView{CSRF: s.csrf.TokenFrom(r.Context()), Title: removeAnyListTitle,
		Lead: fmt.Sprintf(removeAnyListLead, lib.Name), Empty: removeAnyListEmpty,
		Back: removeAnyListBack, BackHref: "/admin/libraries", Next: "list"}
	switch r.URL.Query().Get("done") {
	case "restored":
		v.Banner = removeAnyRestored
		if n, err := strconv.Atoi(r.URL.Query().Get("kept")); err == nil && n > 0 && n < 100000 {
			v.Banner = countText(n, removeAnyRestoredKeptOne, removeAnyRestoredKept)
		}
	case "purged":
		v.Banner = removeAnyPurged
	}
	now := s.now()
	for _, rm := range held {
		row := pages.RemovedRow{ID: rm.ID, Title: rm.Title, Path: rm.RootPath, Href: "/removals/" + rm.ID,
			Delete: removeAnyDeleteNow, DeleteAria: fmt.Sprintf(removeAnyDeleteAria, rm.Title), DeleteCheck: removeAnyDeleteCheck}
		if rm.Mode == upload.ModeKeepFiles {
			row.Meta, row.Restore, row.RestoreAria = removeAnyKeptMeta, removeAnyShowAgain, fmt.Sprintf(removeAnyShowAgainAria, rm.Title)
		} else {
			size := fmt.Sprintf(removeAnyTotal, rm.Files, sizeText(rm.Bytes))
			if rm.Files == 1 {
				size = fmt.Sprintf(removeAnyTotalOne, sizeText(rm.Bytes))
			}
			row.Meta, row.CanDelete = size, true
			row.Restore, row.RestoreAria = removeAnyPutBack, fmt.Sprintf(removeAnyPutBackAria, rm.Title)
			row.Left = daysLeftText(now, rm.PurgeAfter)
		}
		v.Rows = append(v.Rows, row)
	}
	s.prefsPage(w, r, http.StatusOK, removeAnyListTitle, "admin-libraries", "libraries", false, pages.AdminRemoved(v), nil, adminStyles...)
}

// daysLeftText says how long a held removal's files are kept: whole days, rounded up.
func daysLeftText(now time.Time, purgeAfterMs int64) string {
	if purgeAfterMs <= 0 {
		return ""
	}
	left := time.UnixMilli(purgeAfterMs).Sub(now)
	if left < 24*time.Hour {
		return removeAnyDaysLess
	}
	return countText(int((left+24*time.Hour-1)/(24*time.Hour)), removeAnyDaysOne, removeAnyDaysMany)
}
