package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Author details (M4-8): the photo, the bio and the "Look up author online" button. The lookup needs
// the same right as the library pages' other server-wide actions (s.canManage); the photo follows
// the page's own rule, that a person is visible only when they have a book the viewer may open.

// personImageName is the only shape of image id the photo route will turn into a file name:
// the SHA-256 of the picture and its extension.
var personImageName = regexp.MustCompile(`^[0-9a-f]{64}\.(jpg|png|webp)$`)

func (s *server) registerAuthors(mux *http.ServeMux) {
	mux.HandleFunc("GET /people/{id}/photo", s.getPersonPhoto)
	mux.HandleFunc("POST /people/{id}/lookup", s.postPersonLookup)
}

// visiblePerson loads a person the viewer may see (at least one visible book as author or narrator)
// together with the image id, which only PersonByID reads.
func (s *server) visiblePerson(r *http.Request, id string) (library.Person, bool, error) {
	p, err := s.lib.Person(r.Context(), s.scope(r), id)
	if errors.Is(err, library.ErrNotFound) || (err == nil && p.AuthorOf+p.NarratorOf == 0) {
		return library.Person{}, false, nil
	}
	if err != nil {
		return library.Person{}, false, err
	}
	full, err := s.lib.PersonByID(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		return library.Person{}, false, nil
	}
	if err != nil {
		return library.Person{}, false, err
	}
	p.ImageID, p.ASIN = full.ImageID, full.ASIN
	return p, true, nil
}

// getPersonPhoto serves the 320 px picture of a person. The file is found by the person's own image
// id, which must have the shape of a stored photo; nothing the client sends reaches the disk.
func (s *server) getPersonPhoto(w http.ResponseWriter, r *http.Request) {
	p, ok, err := s.visiblePerson(r, r.PathValue("id"))
	if err != nil {
		s.serverError(w, r, "person photo", err)
		return
	}
	if !ok || !personImageName.MatchString(p.ImageID) {
		s.notFound(w, r)
		return
	}
	sha := strings.TrimSuffix(p.ImageID, filepath.Ext(p.ImageID))
	dir := metadata.PeopleDir(s.dataDir)
	path := filepath.Join(dir, sha+"-320.jpg")
	if !within(dir, path) {
		s.notFound(w, r)
		return
	}
	f, err := openCover(path)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("ETag", `"`+p.ImageID+`"`)
	h.Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// authorLookupsOn reports whether the author lookup exists for this person: a service is set, online
// lookups are on and the viewer manages the library.
func (s *server) authorLookupsOn(r *http.Request) (bool, error) {
	if !s.canManage(r) {
		return false, nil
	}
	return s.lookupsOn(r)
}

// postPersonLookup starts the author lookup and goes back to the author's page, which shows its state.
func (s *server) postPersonLookup(w http.ResponseWriter, r *http.Request) {
	on, err := s.authorLookupsOn(r)
	if err != nil {
		s.serverError(w, r, "online setting", err)
		return
	}
	if !on {
		s.notFound(w, r)
		return
	}
	p, ok, err := s.visiblePerson(r, r.PathValue("id"))
	if err != nil {
		s.serverError(w, r, "person", err)
		return
	}
	if !ok || p.AuthorOf == 0 {
		s.notFound(w, r)
		return
	}
	jobID, err := s.details.StartAuthorLookup(r.Context(), p.ID, currentUser(r).ID)
	if errors.Is(err, metadata.ErrOnlineOff) || errors.Is(err, metadata.ErrNotFound) || errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "start author lookup", err)
		return
	}
	s.answerPage(w, r, authorHref(p.ID, jobID))
}

func authorHref(personID, jobID string) string {
	h := "/authors/" + personID
	if jobID != "" {
		h += "?job=" + url.QueryEscape(jobID)
	}
	return h
}

// authorJobFor loads an author lookup job of this person. ok is false for any other job.
func (s *server) authorJobFor(r *http.Request, jobID, personID string) (jobs.Job, bool, error) {
	j, err := s.jobs.Get(r.Context(), jobID)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, false, nil
	}
	if err != nil {
		return jobs.Job{}, false, err
	}
	var p metadata.AuthorPayload
	if j.Kind != metadata.KindAuthor || json.Unmarshal(j.Payload, &p) != nil || p.PersonID != personID {
		return jobs.Job{}, false, nil
	}
	return j, true, nil
}

// authorStatus is the status line for an author lookup job.
func authorStatus(j jobs.Job) pages.PersonStatusView {
	personID := ""
	var p metadata.AuthorPayload
	if json.Unmarshal(j.Payload, &p) == nil {
		personID = p.PersonID
	}
	v := pages.PersonStatusView{CheckHref: authorHref(personID, j.ID), CheckLabel: authorCheckAgain, PollHref: "/hx/jobs/" + url.PathEscape(j.ID)}
	switch j.State {
	case jobs.StateDone:
		var res struct {
			Found bool `json:"found"`
		}
		_ = json.Unmarshal(j.Result, &res)
		v.State, v.Message = "done", authorDone
		if !res.Found {
			v.Message = authorNothing
		} else {
			v.Link, v.LinkLabel = "/authors/"+personID, authorShow
		}
	case jobs.StateFailed, jobs.StateCancelled:
		v.State, v.Message = "failed", j.ErrorText
		if v.Message == "" {
			v.Message = authorFailed
		}
	default:
		v.State, v.Message = "wait", authorWaiting
	}
	return v
}

// getAuthorJob is GET /hx/jobs/{id} for an author lookup: the status line again, waiting or over.
func (s *server) getAuthorJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	on, err := s.authorLookupsOn(r)
	if err != nil {
		s.serverError(w, r, "online setting", err)
		return
	}
	if !on {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.PersonStatus(authorStatus(j)))
}

// personExtras fills the photo, the bio, the lookup button and the lookup status of a person's page.
func (s *server) personExtras(r *http.Request, v *pages.PersonView, p library.Person, role string) error {
	if personImageName.MatchString(p.ImageID) {
		v.PhotoSrc = "/people/" + p.ID + "/photo?v=" + p.ImageID[:8]
	}
	for _, para := range strings.Split(strings.ReplaceAll(p.Description, "\r\n", "\n"), "\n") {
		if para = strings.TrimSpace(para); para != "" {
			v.Bio = append(v.Bio, para)
		}
	}
	if role != library.RoleAuthor {
		return nil
	}
	on, err := s.authorLookupsOn(r)
	if err != nil || !on {
		return err
	}
	v.Lookup = &pages.PersonLookupView{Action: "/people/" + p.ID + "/lookup", CSRF: s.csrf.TokenFrom(r.Context()), Label: authorLookupButton}
	if id := r.URL.Query().Get("job"); id != "" {
		j, ok, err := s.authorJobFor(r, id, p.ID)
		if err != nil {
			return err
		}
		if ok {
			st := authorStatus(j)
			v.Status = &st
		}
	}
	return nil
}
