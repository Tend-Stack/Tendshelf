package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// "Remove all their books" (decision 0029): a confirm page, a job that goes through upload.Service.Remove
// with placedBy set, and a status page that polls /hx/jobs/{id}. Every step needs users.manage on that
// person, so an admin cannot remove an admin's or the owner's books.

func (s *server) registerUsersBooks(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/users/{id}/books/remove", s.getPersonRemoveBooks)
	mux.HandleFunc("POST /admin/users/{id}/books/remove", s.postPersonRemoveBooks)
}

// addedBooksView is the "Books they added" section of a person's page.
func (s *server) addedBooksView(r *http.Request, t users.User) (pages.AddedBooksView, error) {
	ctx := r.Context()
	n, err := s.lib.CountAddedBy(ctx, t.ID)
	if err != nil {
		return pages.AddedBooksView{}, err
	}
	if n == 0 && !t.Contributor() {
		return pages.AddedBooksView{}, nil
	}
	folders, err := s.lib.ContributorFolders(ctx, t.ID)
	if err != nil {
		return pages.AddedBooksView{}, err
	}
	v := pages.AddedBooksView{Show: true, Title: usersAddedTitle, Count: n, SeeLabel: usersAddedSee, RemoveLabel: usersAddedRemove,
		SeeHref: "/libraries?added=" + url.QueryEscape(t.ID), RemoveHref: "/admin/users/" + t.ID + "/books/remove"}
	switch n {
	case 0:
		v.CountWords = usersAddedNone
	default:
		v.CountWords = usersBooksText(n)
	}
	for _, f := range folders {
		v.Folders = append(v.Folders, fmt.Sprintf(usersAddedFolder, f))
	}
	if t.Contributor() {
		v.Note = fmt.Sprintf(usersAddedNote, personName(t))
	}
	return v, nil
}

func (s *server) getPersonRemoveBooks(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadPerson(w, r)
	if !ok {
		return
	}
	if jobID := r.URL.Query().Get("job"); jobID != "" {
		j, err := s.jobs.Get(r.Context(), jobID)
		p, perr := removeAllPayload(j)
		if errors.Is(err, jobs.ErrNotFound) || (err == nil && (perr != nil || p.UserID != t.ID)) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, "remove all status", err)
			return
		}
		title := fmt.Sprintf(usersRemoveStatusTitle, personName(t))
		s.prefsPage(w, r, http.StatusOK, title, "admin-users", "users", false,
			pages.AdminUserRemoveAll(title, "/admin/users/"+t.ID, personName(t), removeAllView(j, "/admin/users/"+t.ID+"/books/remove?job="+j.ID)), nil, adminStyles...)
		return
	}
	n, err := s.lib.CountAddedBy(r.Context(), t.ID)
	if err != nil {
		s.serverError(w, r, "remove all count", err)
		return
	}
	if n == 0 {
		s.backToPerson(w, r, t.ID)
		return
	}
	name := personName(t)
	body, confirm := fmt.Sprintf(usersRemoveBody, n, name), fmt.Sprintf(usersRemoveOK, n)
	if n == 1 {
		body, confirm = fmt.Sprintf(usersRemoveBody1, name), usersRemoveOK1
	}
	s.confirmPage(w, r, t.ID, fmt.Sprintf(usersRemoveTitle, name), body, "/admin/users/"+t.ID+"/books/remove", confirm, usersRemoveKeep, "")
}

func (s *server) postPersonRemoveBooks(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	ctx, actor := r.Context(), currentUser(r)
	t, err := s.users.ByID(ctx, id)
	if errors.Is(err, users.ErrNotFound) || (err == nil && !s.can.Can(ctx, actor, share.ManageUsers, share.Target{Kind: share.TargetUser, ID: t.ID, Role: t.Role})) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "remove all person", err)
		return
	}
	n, err := s.lib.CountAddedBy(ctx, id)
	if err != nil {
		s.serverError(w, r, "remove all count", err)
		return
	}
	if n == 0 {
		s.backToPerson(w, r, id)
		return
	}
	jobID, err := s.jobs.Enqueue(ctx, upload.KindRemoveAll, upload.RemoveAllPayload{UserID: id, By: actor.ID}, jobs.EnqueueOptions{CreatedBy: actor.ID})
	if err != nil {
		s.serverError(w, r, "remove all enqueue", err)
		return
	}
	s.audit(ctx, actor.ID, "user.books_remove_all", "user:"+id, map[string]any{"books": n})
	s.answerPage(w, r, "/admin/users/"+id+"/books/remove?job="+jobID)
}

func removeAllPayload(j jobs.Job) (upload.RemoveAllPayload, error) {
	var p upload.RemoveAllPayload
	if j.Kind != upload.KindRemoveAll {
		return p, errors.New("not a remove-all job")
	}
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.UserID == "" {
		return p, errors.New("bad payload")
	}
	return p, nil
}

// getRemoveAllJob is the status fragment of "Remove all their books". It needs users.manage on the
// person named in the payload; anything else is "no such job".
func (s *server) getRemoveAllJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	p, err := removeAllPayload(j)
	if err != nil {
		s.notFound(w, r)
		return
	}
	t, err := s.users.ByID(r.Context(), p.UserID)
	if err != nil || t.ID == currentUser(r).ID ||
		!s.can.Can(r.Context(), currentUser(r), share.ManageUsers, share.Target{Kind: share.TargetUser, ID: t.ID, Role: t.Role}) {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, components.RemoveAllStatus(removeAllView(j, "/admin/users/"+t.ID+"/books/remove?job="+j.ID)))
}

var keptReasons = map[string]string{
	upload.KeptChanged:      usersKeptChanged,
	upload.KeptNotRemovable: usersKeptNotRemovable,
	upload.KeptNotTheirs:    usersKeptNotTheirs,
}

// removeAllView turns a remove-all job into what the status shows.
func removeAllView(j jobs.Job, refresh string) components.RemoveAllView {
	v := components.RemoveAllView{JobID: j.ID, Refresh: refresh, RefreshLabel: usersRemoveReload}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
		var pr struct{ Done, Total int }
		_ = json.Unmarshal(j.Progress, &pr)
		v.Active = true
		v.Progress = fmt.Sprintf(usersRemoveBusy, pr.Done, pr.Total)
		if pr.Total > 0 {
			v.Percent = min(100, pr.Done*100/pr.Total)
		}
	case jobs.StateDone:
		var res upload.RemoveAllResult
		_ = json.Unmarshal(j.Result, &res)
		v.Done = fmt.Sprintf(usersRemoveDone, res.Removed)
		if res.Removed == 1 {
			v.Done = usersRemoveDone1
		}
		if k := len(res.Kept); k > 0 {
			v.KeptHeading = fmt.Sprintf(usersRemoveKept, k)
			if k == 1 {
				v.KeptHeading = usersRemoveKept1
			}
			for _, b := range res.Kept {
				v.Kept = append(v.Kept, components.RemoveAllKept{Title: b.Title, Reason: keptReasons[b.Reason]})
			}
		}
	default:
		v.Failed = usersRemoveFailed
	}
	return v
}
