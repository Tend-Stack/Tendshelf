package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Editing details by hand: the "Edit details" form on the Details tab. It needs share.EditBook and works with
// online lookups off. A person's values are rows of source "user", which outrank every other source, so a
// rescan or a later lookup keeps them. A refusal is a refused form: the page answers 422 with the form open,
// the typed values kept and each problem under its field. A role that may not edit, an unknown book and a
// server without details all answer 404.

func (s *server) registerDetailsEdit(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/details/edit", s.postDetailsEdit)
}

// editFormKey carries a refused form from the handler to the page that renders it again.
type editFormKey struct{}

type editedForm struct {
	in       metadata.UserDetails
	problems metadata.Problems
}

// detailsEditHref opens the Details tab with the edit form open and focused.
func detailsEditHref(id string) string { return detailsTabHref(id) + "?edit=1#edit-details" }

// postDetailsEdit saves the form.
func (s *server) postDetailsEdit(w http.ResponseWriter, r *http.Request) {
	if s.details == nil {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.EditBook)
	if !ok {
		return
	}
	in := metadata.UserDetails{
		Title: r.PostFormValue("title"), Subtitle: r.PostFormValue("subtitle"), Authors: r.PostFormValue("authors"),
		Narrators: r.PostFormValue("narrators"), SeriesName: r.PostFormValue("series"), SeriesNumber: r.PostFormValue("series_number"),
		Year: r.PostFormValue("year"), Publisher: r.PostFormValue("publisher"), Language: r.PostFormValue("language"),
		Genres: r.PostFormValue("genres"), ISBN: r.PostFormValue("isbn"), ASIN: r.PostFormValue("asin"), Description: r.PostFormValue("description"),
	}
	changed, err := s.details.SaveUserDetails(r.Context(), book.ID, in, currentUser(r).ID)
	var problems metadata.Problems
	switch {
	case errors.As(err, &problems):
		r = r.WithContext(context.WithValue(r.Context(), editFormKey{}, &editedForm{in: in, problems: problems}))
		s.renderBookWith(w, r, book.ID, "details", http.StatusUnprocessableEntity, nil, nil)
		return
	case errors.Is(err, library.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "save details", err)
		return
	}
	n := "0"
	if len(changed) > 0 {
		n = "1"
	}
	s.answerPage(w, r, detailsTabHref(book.ID)+"?edited="+n)
}

// detailsFormView is the edit form: prefilled with what the book shows now, or with the refused form.
func (s *server) detailsFormView(r *http.Request, id string) (*pages.DetailsFormView, error) {
	var in metadata.UserDetails
	var problems metadata.Problems
	open := r.URL.Query().Get("edit") == "1"
	if ef, ok := r.Context().Value(editFormKey{}).(*editedForm); ok {
		in, problems, open = ef.in, ef.problems, true
	} else {
		var err error
		if in, err = s.details.DetailsForm(r.Context(), id); err != nil {
			return nil, err
		}
	}
	msg := map[string]string{}
	for _, p := range problems {
		if msg[p.Field] == "" {
			msg[p.Field] = p.Message
		}
	}
	line := func(name, label, value, hint string, plain bool) pages.DetailsFormField {
		return pages.DetailsFormField{Field: components.Field{ID: "edit-" + name, Name: name, Label: label, Value: value, Hint: hint, Error: msg[name], Plain: plain, Autocomplete: "off"}}
	}
	v := &pages.DetailsFormView{
		Action: "/books/" + id + "/details/edit", CSRF: s.csrf.TokenFrom(r.Context()), Title: detailsEditTitle, Intro: detailsEditIntro,
		Note: detailsEditNote, Submit: detailsEditSave, Open: open, Problem: detailsEditProblem, HasProblem: len(problems) > 0,
		Fields: []pages.DetailsFormField{
			line("title", detailsLabelTitle, in.Title, "", false),
			line("subtitle", detailsLabelSubtitle, in.Subtitle, "", false),
			line("authors", detailsLabelAuthors, in.Authors, detailsEditListHint, false),
			line("narrators", detailsLabelNarrators, in.Narrators, detailsEditListHint, false),
			line("series", detailsLabelSeries, in.SeriesName, "", false),
			line("series_number", detailsEditSeriesNumber, in.SeriesNumber, "", true),
			line("year", detailsLabelYear, in.Year, "", true),
			line("publisher", detailsLabelPublisher, in.Publisher, "", false),
			line("language", detailsLabelLanguage, in.Language, "", true),
			line("genres", detailsLabelGenres, in.Genres, detailsEditListHint, false),
			line("isbn", "ISBN", in.ISBN, "", true),
			line("asin", "ASIN", in.ASIN, "", true),
		},
	}
	if p := msg["series"]; p != "" {
		v.Fields[4].Error = p
	}
	v.Description = pages.DetailsFormField{Field: components.Field{ID: "edit-description", Name: "description", Label: detailsLabelDescription, Value: in.Description, Error: msg["description"]}, Multiline: true}
	return v, nil
}

// editedNotice is the line after a save.
func editedNotice(q string) string {
	switch q {
	case "1":
		return detailsEditSaved
	case "0":
		return detailsEditNothing
	}
	return ""
}
