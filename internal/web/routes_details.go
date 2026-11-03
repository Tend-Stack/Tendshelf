package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Look up details online (M4-6): the lookup sheet, the compare sheet, applying a match, going back to
// the files' details, and the owner's switch. Everything on a book needs share.EditBook on it; a
// refusal, an unknown book, a job of another book and online being off all answer 404. The sheets
// are the book page's own address with ?sheet=, so they work without JavaScript.

func (s *server) registerDetails(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/match", s.postMatch)
	mux.HandleFunc("POST /books/{id}/match/apply", s.postMatchApply)
	mux.HandleFunc("GET /books/{id}/match/{job}/cover/{n}", s.getMatchCover)
	mux.HandleFunc("POST /books/{id}/details/reset", s.postDetailsReset)
	mux.HandleFunc("POST /admin/online", s.postAdminOnline)
}

// lookupFieldRunes caps each query field, so a form cannot send a book's worth of text to a site.
const lookupFieldRunes = 300

// lookupForm is a lookup form as the person filled it, with its refusal.
type lookupForm struct {
	title, author, asin, isbn string
	more                      bool
	err                       string
}

func capRunes(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > lookupFieldRunes {
		return string(r[:lookupFieldRunes])
	}
	return s
}

// lookupsOn reports whether the lookup routes exist: a service is set and the owner has not turned
// lookups off.
func (s *server) lookupsOn(r *http.Request) (bool, error) {
	if s.details == nil {
		return false, nil
	}
	return metadata.OnlineEnabled(r.Context(), s.lib.Store().Reader())
}

// lookupBook loads the book of a lookup route: 404 unless lookups are on and the person may edit it.
func (s *server) lookupBook(w http.ResponseWriter, r *http.Request, id string) (library.Book, bool) {
	on, err := s.lookupsOn(r)
	if err != nil {
		s.serverError(w, r, "online setting", err)
		return library.Book{}, false
	}
	if !on {
		s.notFound(w, r)
		return library.Book{}, false
	}
	return s.loadBook(w, r, id, share.EditBook)
}

// detailsRefuse answers a refused form in plain words: a toast for htmx, text otherwise.
func (s *server) detailsRefuse(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
		return
	}
	http.Error(w, msg, status)
}

// detailsEditView fills the lookup button, the "Used n changes" line and the reset form of the
// Details tab.
func (s *server) detailsEditView(r *http.Request, id string, lookups bool) (pages.DetailsEditView, error) {
	v := pages.DetailsEditView{}
	if lookups {
		v.LookupHref, v.LookupLabel = lookupHref(id), detailsLookupButton
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("applied")); err == nil && n > 0 && n < 100 {
		v.Applied = detailsUsed(n)
	}
	rows, err := s.lib.MetadataValues(r.Context(), id)
	if err != nil {
		return v, err
	}
	if slices.ContainsFunc(rows, func(m library.MetadataValue) bool {
		return m.Source == metadata.SourceUser || m.Source == metadata.SourceOnline
	}) {
		v.ResetAction, v.CSRF = detailsTabHref(id)+"/reset", s.csrf.TokenFrom(r.Context())
		v.ResetTitle, v.ResetBody, v.ResetButton = detailsResetTitle, detailsResetBody, detailsResetButton
	}
	return v, nil
}

// detailsSheet builds the sheet the address names: the lookup form (also after a refusal) or the
// compare sheet. ok is false for a job that is not this book's or a candidate that is not there.
func (s *server) detailsSheet(r *http.Request, d library.BookDetail, look *lookupForm) (templ.Component, bool, error) {
	ctx, q := r.Context(), r.URL.Query()
	id := d.Book.ID
	if look != nil || q.Get("sheet") == "lookup" {
		if look == nil {
			prefill, err := s.details.LookupQuery(ctx, id)
			if err != nil {
				return nil, false, err
			}
			look = &lookupForm{title: prefill.Title, author: prefill.Author, asin: prefill.ASIN, isbn: prefill.ISBN}
		}
		return pages.LookupSheet(s.lookupSheetView(r, id, look)), true, nil
	}
	j, res, err := s.details.LookupResultFor(ctx, q.Get("job"), id)
	if errors.Is(err, metadata.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	c, hasC := 0, q.Get("c") != ""
	if hasC {
		if c, err = strconv.Atoi(q.Get("c")); err != nil {
			return nil, false, nil
		}
	}
	v, ok, err := s.compareSheetView(r, d, j, res, c, hasC, useSet(q["use"]), q.Get("picked") == "1")
	if err != nil || !ok {
		return nil, false, err
	}
	return pages.CompareSheet(v), true, nil
}

func (s *server) lookupSheetView(r *http.Request, id string, f *lookupForm) pages.LookupSheetView {
	return pages.LookupSheetView{
		CSRF: s.csrf.TokenFrom(r.Context()), Action: "/books/" + id + "/match", CloseHref: detailsTabHref(id),
		Title: detailsSheetTitle, CloseLabel: detailsSheetClose, Intro: detailsQueryIntro, Submit: detailsQueryGo,
		TitleLabel: detailsQueryTitle, AuthorLabel: detailsQueryAuthor, ASINLabel: detailsQueryASIN, ISBNLabel: detailsQueryISBN,
		QTitle: f.title, QAuthor: f.author, QASIN: f.asin, QISBN: f.isbn, Error: f.err,
		More: f.more, MoreTitle: detailsMoreTitle, MoreLabel: detailsMoreLabel,
		Styles: s.sheetStyles(pages.DetailsStyles...),
	}
}

// postMatch starts a lookup. A plain form is sent on to the compare sheet; htmx gets the waiting
// sheet at once, which polls /hx/jobs/{id}.
func (s *server) postMatch(w http.ResponseWriter, r *http.Request) {
	book, ok := s.lookupBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	f := &lookupForm{
		title: capRunes(r.PostFormValue("title")), author: capRunes(r.PostFormValue("author")),
		asin: capRunes(r.PostFormValue("asin")), isbn: capRunes(r.PostFormValue("isbn")),
		more: r.PostFormValue("more") == "1",
	}
	q := online.Query{Title: f.title, Author: f.author, ASIN: f.asin, ISBN: f.isbn}
	if pre, err := s.details.LookupQuery(r.Context(), book.ID); err == nil {
		q.Language = pre.Language
	}
	jobID, err := s.details.StartLookup(r.Context(), book.ID, q, f.more, currentUser(r).ID)
	switch {
	case errors.Is(err, online.ErrEmptyQuery):
		f.err = detailsErrEmpty
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, pages.LookupSheet(s.lookupSheetView(r, book.ID, f)))
			return
		}
		s.renderBookWith(w, r, book.ID, "details", http.StatusUnprocessableEntity, nil, f)
		return
	case errors.Is(err, metadata.ErrOnlineOff), errors.Is(err, metadata.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "start lookup", err)
		return
	}
	loc := "/books/" + book.ID + "/details?sheet=compare&job=" + url.QueryEscape(jobID)
	if r.Header.Get("HX-Request") != "true" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, loc, http.StatusSeeOther)
		return
	}
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), book.ID)
	if err != nil {
		s.serverError(w, r, "book detail", err)
		return
	}
	j, res, err := s.details.LookupResultFor(r.Context(), jobID, book.ID)
	if err != nil {
		s.serverError(w, r, "lookup job", err)
		return
	}
	v, _, err := s.compareSheetView(r, d, j, res, 0, false, nil, false)
	if err != nil {
		s.serverError(w, r, "compare sheet", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.CompareSheet(v))
}

// getLookupJob is GET /hx/jobs/{id} for a lookup: the compare sheet's body, waiting or finished.
// The page that polls says which book it is for (?book=); the job must be that book's.
func (s *server) getLookupJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	book, ok := s.lookupBook(w, r, r.URL.Query().Get("book"))
	if !ok {
		return
	}
	j, res, err := s.details.LookupResultFor(r.Context(), j.ID, book.ID)
	if errors.Is(err, metadata.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "lookup job", err)
		return
	}
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), book.ID)
	if err != nil {
		s.serverError(w, r, "book detail", err)
		return
	}
	v, _, err := s.compareSheetView(r, d, j, res, 0, false, nil, false)
	if err != nil {
		s.serverError(w, r, "compare sheet", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.LookupJobFragment(v))
}

// postMatchApply writes the ticked values of one candidate of a finished lookup. The values and the
// cover address come from the stored result; the form names only the job, the candidate and the fields.
func (s *server) postMatchApply(w http.ResponseWriter, r *http.Request) {
	book, ok := s.lookupBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	j, res, err := s.details.LookupResultFor(r.Context(), r.PostFormValue("job"), book.ID)
	if errors.Is(err, metadata.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "lookup job", err)
		return
	}
	if j.State != jobs.StateDone {
		s.detailsRefuse(w, r, http.StatusConflict, detailsStillRuns)
		return
	}
	c, err := strconv.Atoi(r.PostFormValue("c"))
	if err != nil || c < 0 || c >= min(len(res.Candidates), compareMaxCandidates) {
		s.detailsRefuse(w, r, http.StatusUnprocessableEntity, detailsFailed)
		return
	}
	var fields []string
	cover := false
	for _, f := range r.PostForm["use"] {
		switch {
		case f == fieldCover:
			cover = true
		case slices.Contains(metadata.ApplyFields, f):
			if !slices.Contains(fields, f) {
				fields = append(fields, f)
			}
		default:
			s.detailsRefuse(w, r, http.StatusUnprocessableEntity, detailsFailed)
			return
		}
	}
	cand := res.Candidates[c]
	user := currentUser(r).ID
	changed, err := s.details.ApplyOnline(r.Context(), book.ID, cand, fields, user)
	if errors.Is(err, metadata.ErrBadField) {
		s.detailsRefuse(w, r, http.StatusUnprocessableEntity, detailsFailed)
		return
	}
	if err != nil {
		s.serverError(w, r, "apply details", err)
		return
	}
	n := len(changed)
	if cover && cand.CoverURL != "" {
		payload := mediajobs.AdoptPayload{BookID: book.ID, Source: "online", URL: cand.CoverURL}
		if _, err := s.jobs.Enqueue(r.Context(), mediajobs.KindCoverAdopt, payload, jobs.EnqueueOptions{CreatedBy: user}); err != nil {
			s.serverError(w, r, "adopt cover", err)
			return
		}
		n++
	}
	loc := detailsTabHref(book.ID)
	if n > 0 {
		loc += "?applied=" + strconv.Itoa(n)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

// coverTypes maps the picture kinds the online client reports to their media types.
var coverTypes = map[string]string{"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}

// getMatchCover serves the cover picture of one candidate from the cache the lookup job filled. It
// never fetches anything.
func (s *server) getMatchCover(w http.ResponseWriter, r *http.Request) {
	book, ok := s.lookupBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	_, res, err := s.details.LookupResultFor(r.Context(), r.PathValue("job"), book.ID)
	if errors.Is(err, metadata.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "lookup job", err)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= min(len(res.Candidates), compareMaxCandidates) || res.Candidates[n].CoverURL == "" {
		s.notFound(w, r)
		return
	}
	b, kind, ok := s.details.CachedCover(res.Candidates[n].CoverURL)
	if !ok || coverTypes[kind] == "" {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", coverTypes[kind])
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// postDetailsReset removes the details a person chose or took from the internet, so the files'
// own details show again.
func (s *server) postDetailsReset(w http.ResponseWriter, r *http.Request) {
	if s.details == nil {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.EditBook)
	if !ok {
		return
	}
	if err := s.details.ResetOnline(r.Context(), book.ID, currentUser(r).ID); err != nil {
		s.serverError(w, r, "reset details", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, detailsTabHref(book.ID), http.StatusSeeOther)
}

// postAdminOnline is the owner's switch for online lookups.
func (s *server) postAdminOnline(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer}) {
		return
	}
	on := r.PostFormValue("on")
	if on != "0" && on != "1" {
		http.Error(w, detailsFailed, http.StatusUnprocessableEntity)
		return
	}
	if err := metadata.SetOnlineEnabled(r.Context(), s.lib.Store(), on == "1", currentUser(r).ID); err != nil {
		s.serverError(w, r, "online setting", err)
		return
	}
	s.redirect(w, r, "/admin/libraries")
}
