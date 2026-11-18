package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Your own lookup sites (decision 0046): the owner's list of sites Tendshelf may ask for a book's title
// and author. Owner only (share.ManageServer); everyone else gets a 404. The authorization header is
// written but never read back into a page, a log or a job.

func (s *server) registerProviders(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/providers", s.getProviders)
	mux.HandleFunc("POST /admin/providers", s.postProvider)
	mux.HandleFunc("POST /admin/providers/{id}/remove", s.postProviderRemove)
	mux.HandleFunc("POST /admin/providers/{id}/test", s.postProviderTest)
	mux.HandleFunc("GET /hx/admin/providers/test/{job}", s.getProviderTest)
}

// providersGate allows the owner only and answers 404 when the lookup service is not built in.
func (s *server) providersGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer}) {
		return false
	}
	if s.details == nil {
		s.notFound(w, r)
		return false
	}
	return true
}

// providersView builds the page. The test job in ?test= (a plain-form test) shows under its site.
func (s *server) providersView(r *http.Request) (pages.AdminProvidersView, error) {
	ctx := r.Context()
	list, err := s.details.List(ctx)
	if err != nil {
		return pages.AdminProvidersView{}, err
	}
	on, err := metadata.OnlineEnabled(ctx, s.lib.Store().Reader())
	if err != nil {
		return pages.AdminProvidersView{}, err
	}
	q := r.URL.Query()
	v := pages.AdminProvidersView{
		CSRF: s.csrf.TokenFrom(ctx), Title: providersTitle, Intro: providersIntro, OffNote: providersOff, Empty: providersEmpty,
		NameLabel: providersNameLabel, NameHint: providersNameHint, AddressLabel: providersAddressLabel, AddressHint: providersAddressHint,
		AuthLabel: providersAuthLabel, AuthHint: providersAuthHint, AuthSaved: providersAuthSaved,
		AddHeading: providersAddHeading, AddLabel: providersAdd, ListLabel: providersListLabel,
		RemoveLabel: providersRemove, TestLabel: providersTest, CheckLabel: providersCheck,
		AddedNotice: providersAdded, RemovedNotice: providersRemoved,
		Off: !on, Added: q.Get("added") == "1", Removed: q.Get("removed") == "1", Errors: map[string]string{},
	}
	var tv *pages.ProviderTestView
	if id := q.Get("test"); id != "" {
		if t, ok := s.providerTestView(r, id, list); ok {
			tv = &t
		}
	}
	for _, p := range list {
		row := pages.ProviderRow{ID: p.ID, Name: p.Name, Host: p.Host, HasAuth: p.HasAuth}
		if tv != nil && tv.ProviderID == p.ID {
			row.Test = tv
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

func (s *server) renderProviders(w http.ResponseWriter, r *http.Request, status int, v pages.AdminProvidersView) {
	s.prefsPage(w, r, status, providersTitle, "admin-libraries", "libraries", false, pages.AdminProviders(v), nil, pages.EmailStyles...)
}

func (s *server) getProviders(w http.ResponseWriter, r *http.Request) {
	if !s.providersGate(w, r) {
		return
	}
	v, err := s.providersView(r)
	if err != nil {
		s.serverError(w, r, "providers page", err)
		return
	}
	s.renderProviders(w, r, http.StatusOK, v)
}

// providerFieldMessage is the words for a refused field. Codes that name a refused address reuse the
// online texts.
func providerFieldMessage(fe *metadata.ProviderFieldError) string {
	switch fe.Code {
	case "name":
		return providersErrName
	case "name_taken":
		return providersErrNameTaken
	case "address":
		return providersErrAddress
	case "address_parts":
		return providersErrParts
	case "port":
		return providersErrPort
	case "blocked", "insecure":
		return (&online.Error{Code: fe.Code}).Text()
	case "auth":
		return providersErrAuth
	case "too_many":
		return providersErrTooMany
	}
	return providersErrOther
}

func (s *server) postProvider(w http.ResponseWriter, r *http.Request) {
	if !s.providersGate(w, r) || !s.parseForm(w, r) {
		return
	}
	f := r.PostForm
	in := metadata.CustomProviderInput{Name: f.Get("name"), Address: f.Get("address"), Auth: f.Get("auth")}
	_, err := s.details.AddCustomProvider(r.Context(), in, currentUser(r).ID)
	var fe *metadata.ProviderFieldError
	if errors.As(err, &fe) {
		v, verr := s.providersView(r)
		if verr != nil {
			s.serverError(w, r, "providers page", verr)
			return
		}
		field := fe.Field
		if field == "" {
			field = "form"
		}
		v.Errors[field] = providerFieldMessage(fe)
		v.Name, v.Address = in.Name, in.Address // the header is never sent back
		s.renderProviders(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if err != nil {
		s.serverError(w, r, "add lookup site", err)
		return
	}
	s.answerPage(w, r, "/admin/providers?added=1")
}

func (s *server) postProviderRemove(w http.ResponseWriter, r *http.Request) {
	if !s.providersGate(w, r) || !s.parseForm(w, r) {
		return
	}
	err := s.details.RemoveCustomProvider(r.Context(), r.PathValue("id"), currentUser(r).ID)
	if errors.Is(err, metadata.ErrProviderNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "remove lookup site", err)
		return
	}
	s.answerPage(w, r, "/admin/providers?removed=1")
}

func (s *server) postProviderTest(w http.ResponseWriter, r *http.Request) {
	if !s.providersGate(w, r) || !s.parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	job, err := s.details.StartProviderTest(r.Context(), id, currentUser(r).ID)
	if errors.Is(err, metadata.ErrProviderNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "test lookup site", err)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		s.redirect(w, r, "/admin/providers?test="+url.QueryEscape(job))
		return
	}
	list, err := s.details.List(r.Context())
	if err != nil {
		s.serverError(w, r, "test lookup site", err)
		return
	}
	tv, ok := s.providerTestView(r, job, list)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.ProviderTestPanel(tv, providersCheck))
}

// providerTestView reads a test job. ok is false for a job that is not a site test or whose site is gone,
// so the page and the fragment route answer as if it did not exist.
func (s *server) providerTestView(r *http.Request, id string, list []metadata.CustomProviderView) (pages.ProviderTestView, bool) {
	j, err := s.jobs.Get(r.Context(), id)
	if err != nil || j.Kind != metadata.KindProviderTest {
		return pages.ProviderTestView{}, false
	}
	var p metadata.ProviderTestPayload
	if json.Unmarshal(j.Payload, &p) != nil {
		return pages.ProviderTestView{}, false
	}
	name := ""
	for _, c := range list {
		if c.ID == p.ProviderID {
			name = c.Name
		}
	}
	if name == "" {
		return pages.ProviderTestView{}, false
	}
	v := pages.ProviderTestView{ProviderID: p.ProviderID, JobID: id}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning:
		v.Pending, v.Line = true, fmt.Sprintf(providersAsking, name)
	case jobs.StateDone:
		var res metadata.ProviderTestResult
		if json.Unmarshal(j.Result, &res) != nil {
			v.Failed, v.Line = true, providersTestOther
			break
		}
		switch {
		case res.OK && res.Found > 0:
			v.Line = fmt.Sprintf(providersFound, name, res.Found)
		case res.OK:
			v.Line = fmt.Sprintf(providersNothing, name)
		default:
			v.Failed, v.Line = true, (&online.Error{Code: res.Code, Site: name}).Text()
		}
	default:
		v.Failed, v.Line = true, providersTestOther
	}
	return v, true
}

func (s *server) getProviderTest(w http.ResponseWriter, r *http.Request) {
	if !s.providersGate(w, r) {
		return
	}
	list, err := s.details.List(r.Context())
	if err != nil {
		s.serverError(w, r, "lookup site test", err)
		return
	}
	v, ok := s.providerTestView(r, r.PathValue("job"), list)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.ProviderTestPanel(v, providersCheck))
}
