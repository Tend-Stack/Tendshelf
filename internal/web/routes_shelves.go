package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Personal shelves (P1b-8). A shelf is its owner's alone: every route checks share.UseShelves for
// the signed-in person's own shelf (a guest, another person's shelf and an unknown id all answer
// 404) and every library call names the owner, so the SQL filters on it too. Lists and counts only
// cover books the person can open.

const (
	shelfPageSize   = 48
	shelfSideLimit  = 12 // shelves listed in the sidebar before "All shelves"
	shelfSheetQuery = "shelves"
)

func (s *server) registerShelves(mux *http.ServeMux) {
	mux.HandleFunc("GET /shelves", s.getShelves)
	mux.HandleFunc("POST /shelves", s.postShelf)
	mux.HandleFunc("GET /shelves/{id}", s.getShelf)
	mux.HandleFunc("POST /shelves/{id}/rename", s.postShelfRename)
	mux.HandleFunc("GET /shelves/{id}/delete", s.getShelfDelete)
	mux.HandleFunc("POST /shelves/{id}/delete", s.postShelfDelete)
	mux.HandleFunc("POST /shelves/{id}/items", s.postShelfAdd)
	mux.HandleFunc("POST /shelves/{id}/items/{bookID}/remove", s.postShelfRemove)
	mux.HandleFunc("POST /shelves/{id}/items/{bookID}/move", s.postShelfMove)
}

// shelfForm is a refused shelf form on the book page's sheet: what was typed and why it failed.
type shelfForm struct{ name, err string }

// canShelve reports whether the signed-in person may use shelves (never a guest).
func (s *server) canShelve(r *http.Request) bool {
	u := currentUser(r)
	return s.can.Can(r.Context(), u, share.UseShelves, share.Target{Kind: share.TargetShelf, OwnerID: u.ID})
}

// shelfUser authorizes a shelf route and returns the person. When not allowed it has answered 404.
func (s *server) shelfUser(w http.ResponseWriter, r *http.Request) (users.User, bool) {
	u := currentUser(r)
	return u, s.allow(w, r, share.UseShelves, share.Target{Kind: share.TargetShelf, ID: r.PathValue("id"), OwnerID: u.ID})
}

// loadShelf authorizes the route and loads the person's shelf; anything else is 404.
func (s *server) loadShelf(w http.ResponseWriter, r *http.Request) (users.User, library.Shelf, bool) {
	u, ok := s.shelfUser(w, r)
	if !ok {
		return u, library.Shelf{}, false
	}
	sh, err := s.lib.Shelf(r.Context(), u.ID, r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return u, sh, false
	}
	if err != nil {
		s.serverError(w, r, "load shelf", err)
		return u, sh, false
	}
	return u, sh, true
}

// shelfNext is where a form returns to: the same-origin address in next, else def.
func shelfNext(r *http.Request, def string) string {
	if n := auth.SafeNext(r.PostFormValue("next")); n != "/" {
		return n
	}
	return def
}

func shelfSheetHref(bookID string) string { return "/books/" + bookID + "?sheet=" + shelfSheetQuery }

// shelfNameMessage turns a refused shelf name into plain words.
func shelfNameMessage(name string, err error) string {
	switch {
	case errors.Is(err, library.ErrConflict):
		return shelfErrTaken
	case errors.Is(err, library.ErrLimit):
		return shelfErrShelves
	case errors.Is(err, library.ErrInvalid):
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return shelfErrEmpty
		case utf8.RuneCountInString(name) > 80:
			return shelfErrLong
		case strings.IndexFunc(name, unicode.IsControl) >= 0:
			return shelfErrControls
		}
	}
	return ""
}

// sidebarShelves is the "Your shelves" group of the desktop sidebar: the first twelve with their
// counts, then "All shelves" with the total; one "Shelves" entry for a person who has none yet.
// It is nil for people who may not use shelves, and on a failed read (logged).
func (s *server) sidebarShelves(r *http.Request) []components.ShelfLink {
	if !s.canShelve(r) {
		return nil
	}
	u := currentUser(r)
	list, err := s.lib.ShelfCounts(r.Context(), s.scope(r), u.ID)
	if err != nil {
		slog.Error("sidebar shelves", "err", err)
		return nil
	}
	if len(list) == 0 {
		return []components.ShelfLink{{Name: shelfSideNone, Count: "0"}}
	}
	var out []components.ShelfLink
	for i, sh := range list {
		if i == shelfSideLimit {
			out = append(out, components.ShelfLink{Name: shelfSideAll, Count: strconv.Itoa(len(list))})
			break
		}
		out = append(out, components.ShelfLink{ID: sh.ID, Name: sh.Name, Count: strconv.Itoa(sh.Books)})
	}
	return out
}

// ---- The list ----

func (s *server) getShelves(w http.ResponseWriter, r *http.Request) {
	s.shelvesPage(w, r, http.StatusOK, "", "")
}

func (s *server) shelvesPage(w http.ResponseWriter, r *http.Request, status int, name, nameErr string) {
	u, ok := s.shelfUser(w, r)
	if !ok {
		return
	}
	list, err := s.lib.Shelves(r.Context(), s.scope(r), u.ID)
	if err != nil {
		s.serverError(w, r, "list shelves", err)
		return
	}
	v := pages.ShelvesView{CSRF: s.csrf.TokenFrom(r.Context()), NameValue: name, NameErr: nameErr}
	for _, sh := range list {
		row := pages.ShelfRowView{Href: "/shelves/" + sh.ID, Name: sh.Name, Count: countText(sh.Books, "1 book", "%d books")}
		for _, c := range sh.Covers {
			bc := library.BookCard{ID: c.BookID, Title: c.Title}
			if c.SHA != "" {
				ref := c.CoverRef
				bc.Cover = &ref
			}
			row.Covers = append(row.Covers, cardView(bc))
		}
		v.Rows = append(v.Rows, row)
	}
	s.pageStyled(w, r, status, shelfTitle, "shelves", pages.Shelves(v), nil, pages.ShelfStyles...)
}

func (s *server) postShelf(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u, ok := s.shelfUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	name, bookID := r.PostFormValue("name"), r.PostFormValue("book_id")
	if bookID != "" { // a hidden or unknown book is 404 before anything is created
		if _, ok := s.loadBook(w, r, bookID, share.ViewBook); !ok {
			return
		}
	}
	sh, err := s.lib.CreateShelf(ctx, u.ID, name)
	if msg := shelfNameMessage(name, err); msg != "" {
		if bookID != "" {
			s.renderBook(w, r, bookID, "about", http.StatusUnprocessableEntity, &shelfForm{name: name, err: msg})
			return
		}
		s.shelvesPage(w, r, http.StatusUnprocessableEntity, name, msg)
		return
	}
	if err != nil {
		s.serverError(w, r, "create shelf", err)
		return
	}
	if bookID != "" {
		if err := s.lib.AddToShelf(ctx, u.ID, sh.ID, bookID); err != nil {
			s.serverError(w, r, "add to new shelf", err)
			return
		}
	}
	s.shelfSeeOther(w, r, shelfNext(r, "/shelves/"+sh.ID))
}

// shelfSeeOther answers a finished form with a plain 303; htmx follows it with a fetch.
func (s *server) shelfSeeOther(w http.ResponseWriter, r *http.Request, loc string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

// ---- One shelf ----

func (s *server) getShelf(w http.ResponseWriter, r *http.Request) {
	s.shelfPage(w, r, http.StatusOK, "", "")
}

func (s *server) shelfPage(w http.ResponseWriter, r *http.Request, status int, renameValue, renameErr string) {
	u, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	page, err := s.lib.ShelfBooks(r.Context(), s.scope(r), u.ID, sh.ID, cursor, shelfPageSize)
	if errors.Is(err, library.ErrInvalid) {
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, "shelf books", err)
		return
	}
	v := pages.ShelfView{
		CSRF: s.csrf.TokenFrom(r.Context()), ID: sh.ID, Name: sh.Name,
		Count:       countText(page.Total, "1 book", "%d books"),
		RenameValue: sh.Name, RenameErr: renameErr,
	}
	if renameErr != "" {
		v.RenameValue = renameValue
	}
	for i, b := range page.Books {
		c := cardView(b)
		v.Items = append(v.Items, pages.ShelfItemView{
			Card: c, First: cursor == "" && i == 0, Last: page.Next == "" && i == len(page.Books)-1,
			UpLabel: fmt.Sprintf(shelfMoveUp, b.Title), DownLabel: fmt.Sprintf(shelfMoveDown, b.Title),
			RemoveLabel: fmt.Sprintf(shelfRemove, b.Title),
		})
	}
	path := "/shelves/" + sh.ID
	if page.Next != "" {
		v.MoreHref = path + "?cursor=" + url.QueryEscape(page.Next)
	}
	if cursor != "" {
		v.StartHref = path
	}
	s.pageStyled(w, r, status, sh.Name, "shelf-"+sh.ID, pages.Shelf(v), nil, pages.ShelfStyles...)
}

func (s *server) postShelfRename(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	name := r.PostFormValue("name")
	err := s.lib.RenameShelf(r.Context(), u.ID, sh.ID, name)
	if msg := shelfNameMessage(name, err); msg != "" {
		s.shelfPage(w, r, http.StatusUnprocessableEntity, name, msg)
		return
	}
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "rename shelf", err)
		return
	}
	s.shelfSeeOther(w, r, "/shelves/"+sh.ID)
}

func (s *server) getShelfDelete(w http.ResponseWriter, r *http.Request) {
	_, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	v := pages.ShelfDeleteView{CSRF: s.csrf.TokenFrom(r.Context()), ID: sh.ID, Title: fmt.Sprintf(shelfDeleteHead, sh.Name), Body: shelfDeleteBody}
	s.pageStyled(w, r, http.StatusOK, sh.Name, "shelf-"+sh.ID, pages.ShelfDelete(v), nil, pages.ShelfStyles...)
}

func (s *server) postShelfDelete(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	err := s.lib.DeleteShelf(r.Context(), u.ID, sh.ID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "delete shelf", err)
		return
	}
	s.shelfSeeOther(w, r, "/shelves")
}

// ---- Books on a shelf ----

func (s *server) postShelfAdd(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	bookID := r.PostFormValue("book_id")
	b, ok := s.loadBook(w, r, bookID, share.ViewBook)
	if !ok {
		return
	}
	err := s.lib.AddToShelf(r.Context(), u.ID, sh.ID, b.ID)
	switch {
	case errors.Is(err, library.ErrLimit):
		s.renderBook(w, r, b.ID, "about", http.StatusUnprocessableEntity, &shelfForm{err: shelfErrFull})
		return
	case errors.Is(err, library.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "add to shelf", err)
		return
	}
	s.shelfSeeOther(w, r, shelfNext(r, "/shelves/"+sh.ID))
}

func (s *server) postShelfRemove(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	err := s.lib.RemoveFromShelf(r.Context(), u.ID, sh.ID, r.PathValue("bookID"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "remove from shelf", err)
		return
	}
	s.shelfSeeOther(w, r, shelfNext(r, "/shelves/"+sh.ID))
}

func (s *server) postShelfMove(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u, sh, ok := s.loadShelf(w, r)
	if !ok {
		return
	}
	delta := 0
	switch r.PostFormValue("dir") {
	case "up":
		delta = -1
	case "down":
		delta = 1
	default:
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	bookID := r.PathValue("bookID")
	err := s.lib.MoveInShelf(r.Context(), s.scope(r), u.ID, sh.ID, bookID, delta)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "move in shelf", err)
		return
	}
	s.shelfSeeOther(w, r, "/shelves/"+sh.ID+"#item-"+url.PathEscape(bookID))
}

// ---- The sheet on the book page ----

// shelfSheet builds the "Add to shelf" sheet for a book: one toggle per shelf of the signed-in
// person, then the "New shelf" form. f carries a refused form back, or is nil.
func (s *server) shelfSheet(r *http.Request, bookID string, f *shelfForm) (templ.Component, error) {
	ctx := r.Context()
	u := currentUser(r)
	list, err := s.lib.ShelfCounts(ctx, s.scope(r), u.ID)
	if err != nil {
		return nil, err
	}
	on, err := s.lib.ShelvesWithBook(ctx, u.ID, bookID)
	if err != nil {
		return nil, err
	}
	back := "/books/" + bookID
	v := components.ShelfSheetView{
		CSRF: s.csrf.TokenFrom(ctx), BookID: bookID, Back: shelfSheetHref(bookID), CloseHref: back,
		NewAction: "/shelves", Title: shelfSheetTitle, NewLabel: shelfNewTitle, NewButton: shelfSheetNew,
		CloseLabel: shelfSheetClose, NameLabel: shelfNameLabel,
		Styles: s.sheetStyles(pages.ShelfStyles[2]),
	}
	if f != nil {
		v.NewName, v.Error = f.name, f.err
	}
	for _, sh := range list {
		row := components.ShelfSheetRow{ID: sh.ID, Name: sh.Name, On: on[sh.ID], Action: "/shelves/" + sh.ID + "/items"}
		if row.On {
			row.Action = "/shelves/" + sh.ID + "/items/" + bookID + "/remove"
		}
		v.Shelves = append(v.Shelves, row)
	}
	return components.ShelfSheet(v), nil
}
