package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Invites. /invite/{token} and /invite/{token}/cover are the only public routes here (see gate.go);
// everything else needs a session. A link that is unknown, revoked, replaced, or whose inviter may
// no longer invite that role all give one and the same answer, so a link says nothing about why it
// fails.

func (s *server) registerInvites(mux *http.ServeMux) {
	mux.HandleFunc("GET /invite/{token}", s.getInvite)
	mux.HandleFunc("POST /invite/{token}", s.postInvite)
	mux.HandleFunc("GET /invite/{token}/cover", s.getInviteCover)
	mux.HandleFunc("GET /welcome", s.getWelcome)
	mux.HandleFunc("POST /welcome", s.postWelcome)
	mux.HandleFunc("POST /admin/invites/{id}/link", s.postInviteLink)
	mux.HandleFunc("POST /admin/invites/{id}/cancel", s.postInviteCancel)
}

// inviteHeaders keeps the token out of referrers, search indexes and caches.
func inviteHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Cache-Control", "no-store")
}

// inviteState renders a page that says the link does not work.
func (s *server) inviteState(w http.ResponseWriter, r *http.Request, status int, v pages.InviteStateView) {
	inviteHeaders(w)
	s.frame(w, r, status, v.Title, pages.InviteState(v), pages.InviteStyles...)
}

// inviteBroken is the one answer to every link that does not work, whatever the reason.
func (s *server) inviteBroken(w http.ResponseWriter, r *http.Request) {
	s.inviteState(w, r, http.StatusNotFound, pages.InviteStateView{Title: inviteBrokenTitle, Body: inviteBrokenBody})
}

func (s *server) inviteLimited(w http.ResponseWriter, r *http.Request, rl *auth.RateLimitedError) {
	body := tooMany(w, rl)
	s.inviteState(w, r, http.StatusTooManyRequests, pages.InviteStateView{Title: "Please wait a little", Body: body})
}

func (s *server) inviteEnded(w http.ResponseWriter, r *http.Request, inv share.Invite, at int64) {
	if inv.State(at) == share.InviteUsedUp {
		s.inviteState(w, r, http.StatusGone, pages.InviteStateView{Title: inviteUsedTitle, Body: inviteUsedBody, Link: "/login", LinkText: inviteSignIn})
		return
	}
	name := inv.InviterName
	if name == "" {
		name = "the person who sent it"
	}
	s.inviteState(w, r, http.StatusGone, pages.InviteStateView{Title: inviteEndedTitle, Body: fmt.Sprintf(inviteEndedBody, name)})
}

// inviteFeature is the book an invite page shows, seen with the inviter's own rights.
type inviteFeature struct {
	Card   library.BookCard
	Others int
	Ok     bool
}

// featuredBook finds the book an invite page shows. Ok is false when the invite covers no book the
// inviter can still see.
func (s *server) featuredBook(ctx context.Context, inv share.Invite) (inviteFeature, error) {
	id, others, err := s.invites.Featured(ctx, inv)
	if err != nil || id == "" {
		return inviteFeature{}, err
	}
	inviter, err := s.users.ByID(ctx, inv.CreatedBy)
	if err != nil {
		return inviteFeature{}, err
	}
	sc := library.Scope{Books: s.can.BookFilter(inviter, "b"), Libraries: s.can.LibraryFilter(inviter, "l")}
	cards, err := s.lib.CardsByID(ctx, sc, []string{id})
	if err != nil || len(cards) == 0 {
		return inviteFeature{}, err
	}
	return inviteFeature{Card: cards[0], Others: others, Ok: true}, nil
}

func (s *server) serverName(ctx context.Context) string {
	var name string
	if _, err := db.GetSetting(ctx, s.lib.Store().Reader(), settingServerName, &name); err != nil || strings.TrimSpace(name) == "" {
		return inviteServerFall
	}
	return name
}

// inviteSentence is the headline in two parts: the words before the title (the title is set in
// italics between them) and the words after it. An invite that opens no book has the first part
// only. The board's sentence has no full stop.
func inviteSentence(first, server, title string, others int) (lead, tail string) {
	switch {
	case title == "":
		return fmt.Sprintf(inviteLineServer, first, server), ""
	case others <= 0:
		return fmt.Sprintf(inviteLead, first), ""
	case others == 1:
		return fmt.Sprintf(inviteLead, first), inviteTailOther
	}
	return fmt.Sprintf(inviteLead, first), fmt.Sprintf(inviteTailOthers, others)
}

// firstWord is the first word of a display name ("Daniel Reyes" gives "Daniel").
func firstWord(name string) string {
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}
	return name
}

// lookupInvite finds the invite a request names, or has answered. It checks the limiter first and
// charges one failure for every link that does not work. A page for a member or guest invite whose
// books are all gone is broken like any other.
func (s *server) lookupInvite(w http.ResponseWriter, r *http.Request) (share.Invite, inviteFeature, bool) {
	ctx := r.Context()
	ip := s.proxies.ClientIP(r)
	if err := s.auth.InviteAllowed(ip); err != nil {
		var rl *auth.RateLimitedError
		if errors.As(err, &rl) {
			s.inviteLimited(w, r, rl)
			return share.Invite{}, inviteFeature{}, false
		}
	}
	inv, err := s.invites.Lookup(ctx, r.PathValue("token"))
	switch {
	case errors.Is(err, share.ErrInviteUnknown):
		s.auth.InviteFailed(ctx, ip, "unknown")
		s.inviteBroken(w, r)
		return share.Invite{}, inviteFeature{}, false
	case errors.Is(err, share.ErrInviteRefused):
		s.auth.InviteFailed(ctx, ip, "refused")
		s.inviteBroken(w, r)
		return share.Invite{}, inviteFeature{}, false
	case err != nil:
		s.serverError(w, r, "invite lookup", err)
		return share.Invite{}, inviteFeature{}, false
	}
	if st := inv.State(s.now().UnixMilli()); st != share.InvitePending {
		s.inviteEnded(w, r, inv, s.now().UnixMilli())
		return share.Invite{}, inviteFeature{}, false
	}
	feat, err := s.featuredBook(ctx, inv)
	if err != nil {
		s.serverError(w, r, "invite book", err)
		return share.Invite{}, inviteFeature{}, false
	}
	if inv.Role != users.RoleAdmin && !feat.Ok {
		s.auth.InviteFailed(ctx, ip, "no_access")
		s.inviteBroken(w, r)
		return share.Invite{}, inviteFeature{}, false
	}
	return inv, feat, true
}

func (s *server) getInvite(w http.ResponseWriter, r *http.Request) {
	inv, feat, ok := s.lookupInvite(w, r)
	if !ok {
		return
	}
	tok := r.PathValue("token")
	v := pages.InviteView{
		CSRF: s.csrf.TokenFrom(r.Context()), Token: tok, ServerName: s.serverName(r.Context()),
		InviterName: inv.InviterName, InviterID: inv.CreatedBy, InviterFirst: firstWord(inv.InviterName), Note: inv.Note,
	}
	if feat.Ok {
		cv := cardView(feat.Card)
		if cv.CoverSrc != "" {
			cv.CoverSrc = "/invite/" + tok + "/cover?w=640"
		}
		cv.CoverSrcset = ""
		v.Cover, v.Title = cv, cv.Title
		v.BandTint, v.BandClass = bandClasses(cv.Tint)
	}
	v.Lead, v.Tail = inviteSentence(v.InviterFirst, v.ServerName, v.Title, feat.Others)
	if u, signedIn := auth.UserFrom(r.Context()); signedIn {
		v.SignedInAs = personName(u)
	}
	inviteHeaders(w)
	s.frame(w, r, http.StatusOK, "You're invited", pages.Invite(v), pages.InviteStyles...)
}

func (s *server) postInvite(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	var prev *auth.Session
	if sess, ok := auth.SessionFrom(ctx); ok {
		prev = &sess
	}
	l, inv, err := s.auth.AcceptInvite(ctx, r.PathValue("token"), s.proxies.ClientIP(r), r.UserAgent(), prev)
	var rl *auth.RateLimitedError
	switch {
	case err == nil:
	case errors.As(err, &rl):
		s.inviteLimited(w, r, rl)
		return
	case errors.Is(err, share.ErrInviteExpired), errors.Is(err, share.ErrInviteUsedUp):
		// The invite exists; read it again for the inviter's name.
		if got, lerr := s.invites.Lookup(ctx, r.PathValue("token")); lerr == nil {
			s.inviteEnded(w, r, got, s.now().UnixMilli())
		} else {
			s.inviteBroken(w, r)
		}
		return
	case errors.Is(err, share.ErrInviteUnknown), errors.Is(err, share.ErrInviteRefused), errors.Is(err, share.ErrInviteNoAccess):
		s.inviteBroken(w, r)
		return
	default:
		s.serverError(w, r, "accept invite", err)
		return
	}
	s.startSession(w, r, l.Token)
	next := "/welcome"
	if feat, ferr := s.featuredBook(ctx, inv); ferr == nil && feat.Ok {
		next += "?book=" + url.QueryEscape(feat.Card.ID)
	}
	inviteHeaders(w)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *server) getInviteCover(w http.ResponseWriter, r *http.Request) {
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCoverWidth {
			http.Error(w, "Bad width.", http.StatusBadRequest)
			return
		}
		width = n
	}
	ip := s.proxies.ClientIP(r)
	if err := s.auth.InviteAllowed(ip); err != nil {
		var rl *auth.RateLimitedError
		if errors.As(err, &rl) {
			tooMany(w, rl)
			http.Error(w, "Too many tries.", http.StatusTooManyRequests)
			return
		}
	}
	notFound := func(reason string) {
		if reason != "" {
			s.auth.InviteFailed(r.Context(), ip, reason)
		}
		inviteHeaders(w)
		http.Error(w, "Not found.", http.StatusNotFound)
	}
	inv, err := s.invites.Lookup(r.Context(), r.PathValue("token"))
	if errors.Is(err, share.ErrInviteUnknown) || errors.Is(err, share.ErrInviteRefused) {
		notFound("unknown")
		return
	}
	if err != nil {
		s.serverError(w, r, "invite cover", err)
		return
	}
	if inv.State(s.now().UnixMilli()) != share.InvitePending {
		notFound("")
		return
	}
	feat, err := s.featuredBook(r.Context(), inv)
	if err != nil {
		s.serverError(w, r, "invite cover", err)
		return
	}
	if !feat.Ok {
		notFound("")
		return
	}
	book, err := s.lib.Book(r.Context(), feat.Card.ID)
	if err != nil {
		notFound("")
		return
	}
	s.serveCoverFile(w, r, book.CoverID, width, "private, no-store")
}

// ---- Welcome: "Keep your place" ----

// welcomeBook returns the book id for next when the signed-in person may view it, else "".
func (s *server) welcomeBook(r *http.Request, id string) string {
	if id == "" || len(id) > browseMaxIDLen {
		return ""
	}
	b, err := s.lib.Book(r.Context(), id)
	if err != nil {
		return ""
	}
	if !s.can.Can(r.Context(), currentUser(r), share.ViewBook, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) {
		return ""
	}
	return b.ID
}

func welcomeNext(book string) string {
	if book == "" {
		return "/"
	}
	return "/books/" + book
}

func (s *server) welcomeView(r *http.Request, book, keep, username string, errs map[string]string) pages.WelcomeView {
	u := currentUser(r)
	return pages.WelcomeView{CSRF: s.csrf.TokenFrom(r.Context()), Name: personName(u), Book: book, Username: username, Keep: keep, Errors: errs}
}

func (s *server) getWelcome(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	has, err := s.users.HasPassword(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, "welcome", err)
		return
	}
	book := s.welcomeBook(r, r.URL.Query().Get("book"))
	if has {
		s.redirect(w, r, welcomeNext(book))
		return
	}
	s.frame(w, r, http.StatusOK, "Keep your place", pages.Welcome(s.welcomeView(r, book, "phone", u.Username, nil)), pages.InviteStyles...)
}

func (s *server) postWelcome(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	has, err := s.users.HasPassword(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "welcome", err)
		return
	}
	if has {
		s.notFound(w, r)
		return
	}
	book := s.welcomeBook(r, r.PostFormValue("book"))
	if r.PostFormValue("keep") != "password" {
		s.redirect(w, r, welcomeNext(book))
		return
	}
	nu := users.Normalize(users.NewUser{Username: r.PostFormValue("username"), DisplayName: u.DisplayName, Email: u.Email, Password: r.PostFormValue("password")})
	again := func(errs map[string]string) {
		s.frame(w, r, http.StatusUnprocessableEntity, "Keep your place", pages.Welcome(s.welcomeView(r, book, "password", nu.Username, errs)), pages.InviteStyles...)
	}
	if fe := users.Validate(nu); fe != nil {
		errs := map[string]string{}
		for _, field := range []string{"username", "password"} {
			if c, bad := fe[field]; bad {
				errs[field] = fieldMessage(field, c)
			}
		}
		if len(errs) > 0 {
			again(errs)
			return
		}
	}
	hash, err := s.users.HashPassword(ctx, nu.Password) // slow; never inside the write transaction
	if err != nil {
		s.serverError(w, r, "welcome", err)
		return
	}
	err = s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.users.SetFirstCredentials(ctx, tx, u.ID, nu.Username, hash); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: u.ID, Action: "user.credentials", Target: "user:" + u.ID})
	})
	var fe users.FieldErrors
	switch {
	case err == nil:
		s.redirect(w, r, welcomeNext(book))
	case errors.Is(err, users.ErrHasPassword), errors.Is(err, users.ErrNotFound):
		s.notFound(w, r)
	case errors.As(err, &fe):
		again(map[string]string{"username": fieldMessage("username", fe["username"])})
	default:
		s.serverError(w, r, "welcome", err)
	}
}

// ---- Users page: copy link again, cancel invite ----

// inviteRows turns the pending invites the actor may manage into rows.
func (s *server) inviteRows(list []share.Invite) []pages.InviteRow {
	loc := s.now().Location()
	rows := make([]pages.InviteRow, 0, len(list))
	for _, inv := range list {
		meta := fmt.Sprintf(inviteNoEnd, roleWord(inv.Role))
		if inv.ExpiresAt != 0 {
			meta = fmt.Sprintf(inviteUntil, roleWord(inv.Role), time.UnixMilli(inv.ExpiresAt-1).In(loc).Format("2 Jan"))
		}
		rows = append(rows, pages.InviteRow{ID: inv.ID, Name: inv.Name, Line: fmt.Sprintf(inviteWaiting, inv.Name), Meta: meta})
	}
	return rows
}

// inviteAdminAction starts both admin routes: only people who manage users get past, then the form.
func (s *server) inviteAdminAction(w http.ResponseWriter, r *http.Request) bool {
	if !s.allow(w, r, share.ManageUsers, share.Target{Kind: share.TargetServer}) {
		return false
	}
	return s.parseForm(w, r)
}

// inviteActionError answers a failed rotate or cancel: 404 for anything the person may not touch.
func (s *server) inviteActionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, share.ErrInviteUnknown) || errors.Is(err, share.ErrInviteRefused) {
		s.notFound(w, r)
		return
	}
	s.serverError(w, r, "invite change", err)
}

// postInviteLink replaces an invite's link and answers with the Users page and the new address in
// a panel. It does not redirect: the token must not enter a Location header or the history.
func (s *server) postInviteLink(w http.ResponseWriter, r *http.Request) {
	if !s.inviteAdminAction(w, r) {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	var tok string
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		tok, err = s.invites.Rotate(ctx, tx, currentUser(r), id)
		return err
	})
	if err != nil {
		s.inviteActionError(w, r, err)
		return
	}
	v, err := s.usersPageView(r)
	if err != nil {
		s.serverError(w, r, "users page", err)
		return
	}
	for _, row := range v.Invites {
		if row.ID == id {
			scheme := "http://"
			if s.proxies.Secure(r) {
				scheme = "https://"
			}
			v.NewLink = &pages.InviteLink{Name: row.Name, URL: scheme + r.Host + "/invite/" + tok}
		}
	}
	s.pageStyled(w, r, http.StatusOK, usersTitle, "admin-users", pages.AdminUsers(v), nil, adminStyles...)
}

func (s *server) postInviteCancel(w http.ResponseWriter, r *http.Request) {
	if !s.inviteAdminAction(w, r) {
		return
	}
	ctx := r.Context()
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		return s.invites.Revoke(ctx, tx, currentUser(r), r.PathValue("id"))
	})
	if err != nil {
		s.inviteActionError(w, r, err)
		return
	}
	s.redirect(w, r, "/admin/users")
}
