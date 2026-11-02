package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Share & invite: one page to invite someone and to see who is listening. /share is the general
// form; /books/{id}/share is the same page with that book chosen and the lists narrowed to it.
// Only people who manage users get past (the owner and admins); everyone else gets a 404. The
// invite link is shown once, in the answer to the POST, and never in an address.

func (s *server) registerShare(mux *http.ServeMux) {
	mux.HandleFunc("GET /share", s.getShare)
	mux.HandleFunc("POST /share", s.postShare)
	mux.HandleFunc("GET /books/{id}/share", s.getBookShare)
	mux.HandleFunc("POST /books/{id}/share", s.postBookShare)
}

var shareBackRe = regexp.MustCompile(`^(?:/share|/books/([A-Za-z0-9_-]{1,80})/share)$`)

// validShareBack accepts only the two Share & invite addresses as a place to return to, so a
// forged "back" can never be an open redirect. bookID is set for a book's page.
func validShareBack(v string) (path, bookID string, ok bool) {
	m := shareBackRe.FindStringSubmatch(v)
	if m == nil {
		return "", "", false
	}
	return v, m[1], true
}

// shareFormError is a form the person must fix, in plain words; it answers 422.
type shareFormError string

func (e shareFormError) Error() string { return string(e) }

func (s *server) shareGate(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageUsers, share.Target{Kind: share.TargetServer})
}

func (s *server) getShare(w http.ResponseWriter, r *http.Request) { s.showShare(w, r, "") }

func (s *server) getBookShare(w http.ResponseWriter, r *http.Request) {
	s.showShare(w, r, r.PathValue("id"))
}

func (s *server) postShare(w http.ResponseWriter, r *http.Request) { s.createShare(w, r, "") }

func (s *server) postBookShare(w http.ResponseWriter, r *http.Request) {
	s.createShare(w, r, r.PathValue("id"))
}

func (s *server) showShare(w http.ResponseWriter, r *http.Request, bookID string) {
	if !s.shareGate(w, r) {
		return
	}
	v, err := s.sharePageView(r, bookID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "share page", err)
		return
	}
	s.renderShare(w, r, http.StatusOK, v)
}

func (s *server) renderShare(w http.ResponseWriter, r *http.Request, status int, v pages.ShareView) {
	inviteHeaders(w) // the page may carry a link: keep it out of referrers and caches
	s.pageStyled(w, r, status, shareTitle, "admin-users", pages.SharePage(v), nil, pages.ShareStyles...)
}

// formValue reads a field the person just sent, so a form that comes back with an error keeps it.
func formValue(r *http.Request, key string) string {
	if r.Method == http.MethodPost {
		return r.PostForm.Get(key)
	}
	return ""
}

// sharePageView builds the whole page for the signed-in person. bookID is set for a book's own
// page (an unknown book is library.ErrNotFound). The lists come from fixed queries however many
// people there are.
func (s *server) sharePageView(r *http.Request, bookID string) (pages.ShareView, error) {
	ctx := r.Context()
	actor := currentUser(r)
	_ = r.ParseForm()
	v := pages.ShareView{
		CSRF: s.csrf.TokenFrom(ctx), Action: "/share", FindAction: "/share", Back: "/share", BackHref: "/admin/users",
		Name: formValue(r, "name"), Note: formValue(r, "note"), Read: r.Method != http.MethodPost || formValue(r, "read") == "1",
	}
	sc := s.scope(r)
	in := shareScopeInput{Selected: formValue(r, "scope"), Library: formValue(r, "library"), Shelf: formValue(r, "shelf"), ActorID: actor.ID}
	if bookID != "" {
		book, err := s.lib.Book(ctx, bookID)
		if err != nil {
			return v, err
		}
		v.Action, v.Back, v.BackHref, v.BookID, v.BookTitle = "/books/"+book.ID+"/share", "/books/"+book.ID+"/share", "/books/"+book.ID, book.ID, book.Title
		in.BookTitle = book.Title
		if in.Selected == "" {
			in.Selected = "book"
		}
	} else if q := strings.TrimSpace(r.URL.Query().Get("q")); r.Method == http.MethodGet && q != "" {
		v.Q = q
		res, err := s.lib.Search(ctx, sc, q, 20)
		if err != nil {
			return v, err
		}
		cards, err := s.lib.CardsByID(ctx, sc, res.IDs)
		if err != nil {
			return v, err
		}
		for _, c := range cards {
			v.Found = append(v.Found, pages.Option{Value: c.ID, Label: c.Title})
		}
		if len(v.Found) > 0 && in.Selected == "" {
			in.Selected = "book"
		}
	} else if b := formValue(r, "book"); b != "" && in.Selected == "book" {
		// A form that came back with an error keeps the book that was chosen.
		if book, err := s.lib.Book(ctx, b); err == nil {
			v.Found = []pages.Option{{Value: book.ID, Label: book.Title}}
			v.BookChosen = book.ID
		}
	}
	var err error
	if in.Libraries, err = s.lib.VisibleLibraries(ctx, sc); err != nil {
		return v, err
	}
	if in.Shelves, err = s.grants.ShelfTargets(ctx); err != nil {
		return v, err
	}
	v.Scopes = shareScopeChoices(in)
	v.Roles = shareRoleChoices(actor, formValue(r, "role"))
	v.Expires = shareExpiryChoices(formValue(r, "expires"))
	return v, s.shareLists(r, &v, bookID)
}

// shareLists fills "Who's listening": people with access (for a book, the ones whose access opens
// it) and the invites still waiting.
func (s *server) shareLists(r *http.Request, v *pages.ShareView, bookID string) error {
	ctx := r.Context()
	now := s.now()
	nowMs := now.UnixMilli()
	actor := currentUser(r)
	people, err := s.grants.People(ctx, nowMs, monthStart(now).UnixMilli())
	if err != nil {
		return err
	}
	covers := map[string][]share.BookCover{}
	if bookID != "" {
		list, err := s.grants.CoveringGrants(ctx, bookID, nowMs)
		if err != nil {
			return err
		}
		for _, c := range list {
			covers[c.UserID] = append(covers[c.UserID], c)
		}
	}
	for _, p := range people {
		if (p.User.Role != users.RoleMember && p.User.Role != users.RoleGuest) || p.User.Disabled || p.ActiveGrants < 1 {
			continue
		}
		row := pages.ShareListener{ID: p.User.ID, Name: personName(p.User), RolePill: roleWord(p.User.Role), RoleKind: string(p.User.Role),
			Line: shareListenerLine(p, now)}
		if bookID == "" {
			row.StopHref = "/admin/users/" + p.User.ID + "/stop?back=" + url.QueryEscape(v.Back)
		} else {
			cs := covers[p.User.ID]
			if len(cs) == 0 {
				continue
			}
			row.StopHref = "/admin/users/" + p.User.ID + "/stop?back=" + url.QueryEscape(v.Back)
			if !hasBookGrant(cs) {
				row.StopHref, row.ViaHref, row.Via = "", "/admin/users/"+p.User.ID, shareVia(cs[0])
			}
		}
		v.People = append(v.People, row)
	}
	roles := share.ManageableInviteRoles(actor)
	var pending []share.Invite
	if bookID == "" {
		pending, err = s.invites.Pending(ctx, nowMs, roles)
	} else {
		pending, err = s.invites.PendingCovering(ctx, nowMs, roles, bookID)
	}
	if err != nil {
		return err
	}
	var gs []share.InviteGrant
	for _, inv := range pending {
		gs = append(gs, inv.Grants...)
	}
	names, err := s.grants.TargetNames(ctx, gs)
	if err != nil {
		return err
	}
	for _, inv := range pending {
		v.Pending = append(v.Pending, pages.SharePending{ID: inv.ID, Name: inv.Name, Line: fmt.Sprintf(sharePendingLine, inv.Name),
			Meta: sharePendingMeta(inv, names, now)})
	}
	v.Listening = subtitleText(len(v.People), len(v.Pending), 0)
	return nil
}

// shareWithNewLink answers a replaced link asked for from a Share & invite page: that page again,
// with the new address in a panel. link writes the panel for the invite's name.
func (s *server) shareWithNewLink(w http.ResponseWriter, r *http.Request, bookID, inviteID string, link func(name string) *pages.InviteLink) {
	v, err := s.sharePageView(r, bookID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "share page", err)
		return
	}
	pending, err := s.invites.Pending(r.Context(), s.now().UnixMilli(), share.ManageableInviteRoles(currentUser(r)))
	if err != nil {
		s.serverError(w, r, "share page", err)
		return
	}
	for _, inv := range pending {
		if inv.ID == inviteID {
			v.NewLink = link(inv.Name)
		}
	}
	s.renderShare(w, r, http.StatusOK, v)
}

func hasBookGrant(cs []share.BookCover) bool {
	for _, c := range cs {
		if c.Scope == share.GrantBook {
			return true
		}
	}
	return false
}

// shareVia says where a wider share gives someone the book.
func shareVia(c share.BookCover) string {
	switch c.Scope {
	case share.GrantLibrary:
		return fmt.Sprintf(shareViaLibrary, c.Name)
	case share.GrantShelf:
		return fmt.Sprintf(shareViaShelf, c.Name)
	}
	return shareViaAll
}

// inviteSpec reads the form. Problems the person must fix are shareFormError.
func (s *server) inviteSpec(r *http.Request, bookID string) (share.InviteSpec, string, error) {
	f := r.PostForm
	spec := share.InviteSpec{Name: f.Get("name"), Note: f.Get("note")}
	switch role := users.Role(f.Get("role")); role {
	case "", users.RoleGuest:
		spec.Role = users.RoleGuest
	case users.RoleMember, users.RoleAdmin:
		spec.Role = role
	default:
		return spec, "", shareFormError(shareErrRole)
	}
	end, words, ok := shareExpiry(f.Get("expires"), s.now())
	if !ok {
		return spec, "", shareFormError(shareErrExpires)
	}
	spec.ExpiresAt = end
	if spec.Role == users.RoleAdmin {
		return spec, words, nil // an admin invite opens everything by role and carries no grants
	}
	g := share.InviteGrant{Scope: share.GrantScope(f.Get("scope")), Download: true, Read: f.Get("read") == "1"}
	switch f.Get("scope") {
	case "all":
	case "library", "shelf", "book":
		g.Target = f.Get(f.Get("scope"))
		if g.Scope == share.GrantBook && bookID != "" {
			g.Target = bookID
		}
		if g.Target == "" {
			return spec, "", shareFormError(shareErrScope)
		}
	default:
		return spec, "", shareFormError(shareErrScope)
	}
	spec.Grants = []share.InviteGrant{g}
	return spec, words, nil
}

func (s *server) inviteURL(r *http.Request, tok string) string {
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	return scheme + r.Host + "/invite/" + tok
}

func (s *server) createShare(w http.ResponseWriter, r *http.Request, bookID string) {
	if !s.shareGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	spec, words, err := s.inviteSpec(r, bookID)
	var inv share.Invite
	var tok string
	if err == nil {
		err = s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
			var cerr error
			inv, tok, cerr = s.invites.Create(ctx, tx, currentUser(r), spec)
			return cerr
		})
	}
	if err != nil {
		s.shareError(w, r, bookID, err)
		return
	}
	v, err := s.sharePageView(r, bookID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "share page", err)
		return
	}
	note := fmt.Sprintf(shareLinkNoteEnd, inv.Name)
	if words != "" {
		note = fmt.Sprintf(shareLinkNoteFor, inv.Name, words)
	}
	v.Created = &pages.InviteLink{Name: inv.Name, URL: s.inviteURL(r, tok), Title: fmt.Sprintf(shareLinkTitle, inv.Name), Note: note}
	s.renderShare(w, r, http.StatusOK, v)
}

// shareError answers a failed create: 404 for anything the person may not do, 422 with plain words
// for a form to fix (a toast for htmx, the page again otherwise), 500 for the rest.
func (s *server) shareError(w http.ResponseWriter, r *http.Request, bookID string, err error) {
	var msg string
	var fe users.FieldErrors
	var sf shareFormError
	switch {
	case errors.Is(err, share.ErrInviteRefused):
		s.notFound(w, r)
		return
	case errors.As(err, &fe):
		msg = shareFieldMessage(fe)
	case errors.As(err, &sf):
		msg = string(sf)
	case errors.Is(err, share.ErrInviteGrants), errors.Is(err, share.ErrGrantTarget), errors.Is(err, share.ErrGrantScope):
		msg = shareErrScope
	default:
		s.serverError(w, r, "create invite", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msg))
		return
	}
	v, verr := s.sharePageView(r, bookID)
	if errors.Is(verr, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if verr != nil {
		s.serverError(w, r, "share page", verr)
		return
	}
	v.Error = msg
	s.renderShare(w, r, http.StatusUnprocessableEntity, v)
}

func shareFieldMessage(fe users.FieldErrors) string {
	switch {
	case fe["name"] == "too_long":
		return shareErrNameLen
	case fe["name"] != "":
		return shareErrName
	case fe["note"] != "":
		return shareErrNoteLen
	}
	return shareErrExpires
}
