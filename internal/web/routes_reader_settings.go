package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Reading settings (M5 R4 P4): the choices stored as reader.v1 and the theme (ui.theme), changed from the sheet
// of the reader.

const (
	readerSettingsRoute   = "/me/reader-settings"
	readerSettingsInvalid = "That setting is not one Tendshelf knows."
	// readerPreviewText is the sentence the sheet shows its changes on (public domain, Austen).
	readerPreviewText = "It is a truth universally acknowledged, that a single man in possession of a good fortune, must be in want of a wife."
)

func (s *server) registerReaderSettings(mux *http.ServeMux) {
	mux.HandleFunc("POST /me/reader-settings", s.postReaderSettings)
}

// rsetText reads a choice when the form has it; whether it is allowed is checked by Validate.
func rsetText(form url.Values, key string, dst *string) {
	if form.Has(key) {
		*dst = form.Get(key)
	}
}

// rsetNext is the page a plain post returns to: a book page of this server, else the home page.
func rsetNext(next string) string {
	if strings.HasPrefix(next, "/books/") && !strings.Contains(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return "/"
}

func (s *server) postReaderSettings(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	if u.ID == "" {
		s.notFound(w, r)
		return
	}
	htmx := r.Header.Get("HX-Request") == "true"
	bad := func() {
		if htmx {
			s.fragment(w, r, http.StatusBadRequest, components.ErrorToast(readerSettingsInvalid))
			return
		}
		s.errorPage(w, r, http.StatusBadRequest)
	}
	cur, err := s.users.LoadReader(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "load reader settings", err)
		return
	}
	next := cur
	form := r.PostForm
	rsetText(form, "font", &next.Font)
	rsetText(form, "leading", &next.Leading)
	if form.Has("size") {
		n, err := strconv.Atoi(form.Get("size"))
		if err != nil {
			bad()
			return
		}
		next.Size = n
	}
	if next.Validate() != nil {
		bad()
		return
	}
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "load settings", err)
		return
	}
	theme := set.Theme
	if form.Has("theme") {
		theme = form.Get("theme")
		set.Theme = theme
		if set.Validate() != nil {
			bad()
			return
		}
	}
	if next != cur {
		if err := s.users.SaveReader(ctx, u.ID, next); err != nil {
			s.serverError(w, r, "save reader settings", err)
			return
		}
	}
	if form.Has("theme") {
		if err := s.users.SaveSettings(ctx, u.ID, set); err != nil {
			s.serverError(w, r, "save settings", err)
			return
		}
		s.setThemeCookie(w, r, theme) // the sign-in pages on this browser follow the saved theme
	}
	if htmx {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, rsetNext(form.Get("next")), http.StatusSeeOther)
}

// readerLook turns the stored settings into the classes and data values of the reader root.
func readerLook(rs users.ReaderSettings) pages.ReaderLook {
	class := "ts-reader--size-" + strconv.Itoa(rs.Size) + " ts-reader--leading-" + rs.Leading
	if rs.Font != "serif" {
		class += " ts-reader--" + rs.Font // "sans" or "dyslexic"
	}
	return pages.ReaderLook{Class: class}
}

// readingSheet builds the "Reading settings" sheet over the page the person is on.
func (s *server) readingSheet(r *http.Request, rc readerCtx, ch int) (pages.ReadingSheetView, error) {
	ctx, u := r.Context(), currentUser(r)
	rs, err := s.users.LoadReader(ctx, u.ID)
	if err != nil {
		return pages.ReadingSheetView{}, err
	}
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		return pages.ReadingSheetView{}, err
	}
	back := rc.href("ch", strconv.Itoa(ch))
	return pages.ReadingSheetView{
		Title: readerSettings, CloseLabel: readerCloseSheet, CloseHref: back,
		Action: readerSettingsRoute, CSRF: s.csrf.TokenFrom(ctx), Next: back, Preview: readerPreviewText,
		Look: readerLook(rs), Theme: set.Theme, Font: rs.Font, Leading: rs.Leading,
		Size: rs.Size, Large: set.TextSize == "large",
	}, nil
}
