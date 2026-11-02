package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	mux.HandleFunc("POST /books/{id}/share/link", s.postBookShareLink)
	mux.HandleFunc("POST /share/links/{id}/stop", s.postShareLinkStop)
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
		Email: formValue(r, "email"),
	}
	if (r.Method == http.MethodPost && r.PostForm.Get("via") == "email") || (r.Method == http.MethodGet && r.URL.Query().Get("via") == "email") {
		v.Via = "email"
	}
	v.EmailRelay = s.emailRelayReady(r)
	if s.can.Can(ctx, actor, share.ManageServer, share.Target{Kind: share.TargetServer}) {
		v.EmailSetup = "/admin/email"
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
	v.LinkTab, v.EmailTab = v.Back, v.Back+"?via=email"
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
	typed := url.Values{}
	if r.Method == http.MethodPost {
		typed = r.PostForm
	}
	if bookID != "" {
		v.LinkForm = linkFormFor(bookID, typed)
	}
	if v.Links, err = s.shareLinkRows(ctx, bookID); err != nil {
		return v, err
	}
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
		row := pages.SharePending{ID: inv.ID, Name: inv.Name, Line: fmt.Sprintf(sharePendingLine, inv.Name),
			Meta: sharePendingMeta(inv, names, now)}
		row.Mail, row.MailFailed = sharePendingMail(inv)
		row.Resend, row.ResendLabel = sharePendingResend(inv, v.EmailRelay)
		v.Pending = append(v.Pending, row)
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
	if f.Get("via") == "email" {
		spec.Email = strings.TrimSpace(f.Get("email"))
		switch {
		case spec.Email == "":
			return spec, "", shareFormError(shareErrEmail)
		case !users.ValidEmail(spec.Email):
			return spec, "", shareFormError(shareErrEmailBad)
		}
	}
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
	relay := r.PostForm.Get("via") == "email" && r.PostForm.Get("how") == "relay"
	if err == nil && relay {
		err = s.relayChecks(r, spec.Email)
	}
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
	if r.PostForm.Get("via") == "email" {
		s.renderShareMail(w, r, bookID, inv, tok, relay)
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
	status := http.StatusUnprocessableEntity
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
		if sf == shareErrMailBusy || sf == shareErrMailAgain {
			w.Header().Set("Retry-After", "3600")
			status = http.StatusTooManyRequests
		}
	case errors.Is(err, share.ErrInviteGrants), errors.Is(err, share.ErrGrantTarget), errors.Is(err, share.ErrGrantScope):
		msg = shareErrScope
	default:
		s.serverError(w, r, "create invite", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
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
	s.renderShare(w, r, status, v)
}

func shareFieldMessage(fe users.FieldErrors) string {
	switch {
	case fe["name"] == "too_long":
		return shareErrNameLen
	case fe["name"] != "":
		return shareErrName
	case fe["note"] != "":
		return shareErrNoteLen
	case fe["email"] != "":
		return shareErrEmailBad
	}
	return shareErrExpires
}

// ---- Share links: made and stopped from the Share & invite pages ----

const linkMinPW = 8

// linkForm is the "Make a link" form of a book's page. typed is what the person last sent (label
// and length); the password is never carried back.
func linkFormFor(bookID string, typed url.Values) *pages.ShareLinkForm {
	days := typed.Get("link_days")
	if days == "" {
		days = strconv.Itoa(share.LinkDefaultDays)
	}
	f := &pages.ShareLinkForm{Action: "/books/" + bookID + "/share/link", Label: typed.Get("link_label")}
	for _, d := range share.LinkDays {
		title := fmt.Sprintf(linkDaysN, d)
		if d == 1 {
			title = linkDay1
		}
		f.Days = append(f.Days, pages.ShareChoice{Value: strconv.Itoa(d), Title: title, On: strconv.Itoa(d) == days})
	}
	return f
}

// shareLinkRows lists the links that still work: the book's own on its page, every book's on /share
// (where each line starts with the title).
func (s *server) shareLinkRows(ctx context.Context, bookID string) ([]pages.ShareLinkRow, error) {
	rows, err := s.links.Active(ctx, s.now().UnixMilli(), bookID, 50)
	if err != nil {
		return nil, err
	}
	loc := s.now().Location()
	var out []pages.ShareLinkRow
	for _, l := range rows {
		name := l.Label
		line := name
		if line == "" {
			line = fmt.Sprintf(linkMade, time.UnixMilli(l.CreatedAt).In(loc).Format("2 Jan"))
		}
		if name == "" {
			name = l.BookTitle
		}
		if bookID == "" {
			line = l.BookTitle + " · " + line
		}
		ends := time.UnixMilli(l.ExpiresAt).In(loc).Format("2 Jan")
		meta := fmt.Sprintf(linkEndsShort, ends)
		if l.HasPassword() {
			meta = fmt.Sprintf(linkEndsPassword, ends)
		}
		out = append(out, pages.ShareLinkRow{ID: l.ID, Line: line, Meta: meta, StopLabel: fmt.Sprintf(linkStopFor, name)})
	}
	return out, nil
}

// linkSpec reads the share-link form. The password is returned apart from the spec because it is
// hashed before the write transaction opens. Problems the person must fix are shareFormError.
func linkSpec(r *http.Request, bookID string) (share.LinkSpec, string, error) {
	f := r.PostForm
	spec := share.LinkSpec{BookID: bookID, Label: strings.TrimSpace(f.Get("link_label"))}
	days, err := strconv.Atoi(f.Get("link_days"))
	if f.Get("link_days") == "" {
		days, err = share.LinkDefaultDays, nil
	}
	if err != nil || !slices.Contains(share.LinkDays, days) {
		return spec, "", shareFormError(linkErrDays)
	}
	spec.Days = days
	if utf8.RuneCountInString(spec.Label) > 80 {
		return spec, "", shareFormError(linkErrLabel)
	}
	pw := f.Get("link_password")
	switch {
	case pw == "":
	case utf8.RuneCountInString(pw) < linkMinPW:
		return spec, "", shareFormError(linkErrPassword)
	case len(pw) > linkMaxPW:
		return spec, "", shareFormError(linkErrPasswordLong)
	}
	return spec, pw, nil
}

func (s *server) linkURL(r *http.Request, tok string) string {
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	return scheme + r.Host + linkPrefix + tok
}

// freshShareView is the page again with the invite form in its starting state: the link form's own
// fields are not the invite form's.
func (s *server) freshShareView(r *http.Request, bookID string) (pages.ShareView, error) {
	page := r.Clone(r.Context())
	page.Method = http.MethodGet
	return s.sharePageView(page, bookID)
}

func (s *server) postBookShareLink(w http.ResponseWriter, r *http.Request) {
	if !s.shareGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	bookID := r.PathValue("id")
	spec, pw, err := linkSpec(r, bookID)
	hash := ""
	if err == nil && pw != "" {
		hash, err = s.users.HashPassword(ctx, pw) // before the transaction: hashing is slow
	}
	var l share.Link
	var tok string
	if err == nil {
		err = s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
			var cerr error
			l, tok, cerr = s.links.Create(ctx, tx, currentUser(r), spec, hash)
			return cerr
		})
	}
	if err != nil {
		s.shareLinkError(w, r, bookID, err)
		return
	}
	v, err := s.freshShareView(r, bookID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "share page", err)
		return
	}
	title := ""
	if b, berr := s.lib.Book(ctx, bookID); berr == nil {
		title = b.Title
	}
	end := time.UnixMilli(l.ExpiresAt).In(s.now().Location()).Format("2 January")
	v.LinkCreated = &pages.InviteLink{URL: s.linkURL(r, tok), Title: linkReadyTitle, Label: linkFieldLabel, FieldID: "share-link",
		Note: fmt.Sprintf(linkReadyNote, title, end)}
	s.renderShare(w, r, http.StatusOK, v)
}

// shareLinkError answers a failed link create: 404 for anything the person may not do or a book
// that is not there, 422 with plain words for a form to fix, 500 for the rest.
func (s *server) shareLinkError(w http.ResponseWriter, r *http.Request, bookID string, err error) {
	var msg string
	var sf shareFormError
	switch {
	case errors.As(err, &sf):
		msg = string(sf)
	case errors.Is(err, share.ErrLinkSpec):
		msg = linkErrDays
	case errors.Is(err, share.ErrLinkRefused):
		if _, berr := s.lib.Book(r.Context(), bookID); berr == nil && s.can.Can(r.Context(), currentUser(r), share.ManageUsers, share.Target{Kind: share.TargetServer}) {
			msg = linkErrRefused // staff, a real book that cannot be shared (refused, missing)
			break
		}
		s.notFound(w, r)
		return
	default:
		s.serverError(w, r, "create link", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msg))
		return
	}
	v, verr := s.freshShareView(r, bookID)
	if errors.Is(verr, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if verr != nil {
		s.serverError(w, r, "share page", verr)
		return
	}
	v.LinkForm = linkFormFor(bookID, r.PostForm)
	v.LinkError = msg
	s.renderShare(w, r, http.StatusUnprocessableEntity, v)
}

func (s *server) postShareLinkStop(w http.ResponseWriter, r *http.Request) {
	if !s.shareGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		return s.links.Revoke(ctx, tx, currentUser(r), r.PathValue("id"))
	})
	switch {
	case errors.Is(err, share.ErrLinkUnknown), errors.Is(err, share.ErrLinkRefused):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "stop link", err)
		return
	}
	back := "/share"
	if b, _, ok := validShareBack(r.PostForm.Get("back")); ok {
		back = b
	}
	s.redirect(w, r, back)
}
