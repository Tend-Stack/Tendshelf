package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The Users & invites area. Every route asks share.Can(ManageUsers, ...) and answers 404 on a
// refusal. Everything about one person runs in one write transaction that first re-reads the
// signed-in person and the target and asks again (recheckManage), so a role change made after the
// page loaded cannot be used.

func (s *server) registerUsers(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/users", s.getUsers)
	mux.HandleFunc("GET /hx/admin/users", s.getUsersFragment)
	mux.HandleFunc("GET /admin/users/{id}", s.getUserPage)
	mux.HandleFunc("POST /admin/users/{id}/role", s.postPersonRole)
	mux.HandleFunc("GET /admin/users/{id}/disable", s.getPersonDisable)
	mux.HandleFunc("POST /admin/users/{id}/disable", s.postPersonDisable)
	mux.HandleFunc("POST /admin/users/{id}/enable", s.postPersonEnable)
	mux.HandleFunc("POST /admin/users/{id}/devices/{did}/signout", s.postPersonDeviceSignOut)
	mux.HandleFunc("POST /admin/users/{id}/grants", s.postGrantAdd)
	mux.HandleFunc("POST /admin/users/{id}/grants/{gid}", s.postGrantUpdate)
	mux.HandleFunc("POST /admin/users/{id}/grants/{gid}/renew", s.postGrantRenew)
	mux.HandleFunc("POST /admin/users/{id}/grants/{gid}/remove", s.postGrantRemove)
	mux.HandleFunc("GET /admin/users/{id}/stop", s.getPersonStop)
	mux.HandleFunc("POST /admin/users/{id}/stop", s.postPersonStop)
}

// errRefused is a change the signed-in person may not make, as seen inside the transaction.
var errRefused = errors.New("web: change refused")

// formError is a refused form, in plain words; it answers 422.
type formError string

func (e formError) Error() string { return string(e) }

func (s *server) canManageServerUsers(r *http.Request) bool {
	return s.can.Can(r.Context(), currentUser(r), share.ManageUsers, share.Target{Kind: share.TargetServer})
}

// usersLink fills the sidebar: whether to show "Users & invites" and its "{n} waiting" count.
func (s *server) usersLink(r *http.Request) (show bool, waiting string) {
	if !s.canManageServerUsers(r) {
		return false, ""
	}
	n, err := share.WaitingInvites(r.Context(), s.lib.Store(), s.now().UnixMilli(), share.ManageableInviteRoles(currentUser(r)))
	if err == nil && n > 0 {
		waiting = fmt.Sprintf("%d waiting", n)
	}
	return true, waiting
}

// usersRow is the profile row: "{n} people · {k} invite(s) waiting".
func (s *server) usersRow(ctx context.Context, actor users.User) (string, error) {
	var people int
	if err := s.lib.Store().Reader().QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&people); err != nil {
		return "", err
	}
	n, err := share.WaitingInvites(ctx, s.lib.Store(), s.now().UnixMilli(), share.ManageableInviteRoles(actor))
	if err != nil {
		return "", err
	}
	return subtitleText(people, n, 0), nil
}

func monthStart(now time.Time) time.Time {
	y, m, _ := now.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
}

func (s *server) usersPageView(r *http.Request) (pages.UsersView, error) {
	ctx := r.Context()
	now := s.now()
	list, err := s.grants.People(ctx, now.UnixMilli(), monthStart(now).UnixMilli())
	if err != nil {
		return pages.UsersView{}, err
	}
	actor := currentUser(r)
	pending, err := s.invites.Pending(ctx, now.UnixMilli(), share.ManageableInviteRoles(actor))
	if err != nil {
		return pages.UsersView{}, err
	}
	manage := func(u users.User) bool {
		return s.can.Can(ctx, actor, share.ManageUsers, share.Target{Kind: share.TargetUser, ID: u.ID, Role: u.Role})
	}
	v := usersView(s.csrf.TokenFrom(ctx), now, actor, list, len(pending), manage)
	if len(pending) > 0 {
		v.TabInvites = fmt.Sprintf(inviteTabLabel, len(pending))
		v.Invites = s.inviteRows(pending)
	}
	if err := s.addDevices(ctx, &v, list, actor, manage); err != nil {
		return pages.UsersView{}, err
	}
	return v, nil
}

func (s *server) getUsers(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, share.ManageUsers, share.Target{Kind: share.TargetServer}) {
		return
	}
	v, err := s.usersPageView(r)
	if err != nil {
		s.serverError(w, r, "users page", err)
		return
	}
	s.pageStyled(w, r, http.StatusOK, usersTitle, "admin-users", pages.AdminUsers(v), nil, adminStyles...)
}

func (s *server) getUsersFragment(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, share.ManageUsers, share.Target{Kind: share.TargetServer}) {
		return
	}
	v, err := s.usersPageView(r)
	if err != nil {
		s.serverError(w, r, "users list", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.UsersList(v))
}

// loadPerson loads the person named in the path for a page. Unknown, the signed-in person
// themselves, and anyone share.Can refuses all answer 404.
func (s *server) loadPerson(w http.ResponseWriter, r *http.Request) (users.User, bool) {
	id := r.PathValue("id")
	if id == currentUser(r).ID {
		s.notFound(w, r)
		return users.User{}, false
	}
	u, err := s.users.ByID(r.Context(), id)
	if errors.Is(err, users.ErrNotFound) {
		s.notFound(w, r)
		return users.User{}, false
	}
	if err != nil {
		s.serverError(w, r, "load person", err)
		return users.User{}, false
	}
	if !s.allow(w, r, share.ManageUsers, share.Target{Kind: share.TargetUser, ID: u.ID, Role: u.Role}) {
		return users.User{}, false
	}
	return u, true
}

func rolesFor(s *server, r *http.Request, current users.Role) []pages.RoleChoice {
	actor := currentUser(r)
	all := []pages.RoleChoice{
		{Value: string(users.RoleAdmin), Label: usersRoleAdmin, Hint: usersHintAdmin},
		{Value: string(users.RoleMember), Label: usersRoleMember, Hint: usersHintMember},
		{Value: string(users.RoleGuest), Label: usersRoleGuest, Hint: usersHintGuest},
	}
	var out []pages.RoleChoice
	for _, c := range all {
		role := users.Role(c.Value)
		if role != current && !s.can.Can(r.Context(), actor, share.ManageUsers, share.Target{Kind: share.TargetUser, Role: role}) {
			continue
		}
		c.On = role == current
		out = append(out, c)
	}
	return out
}

// personView builds the page of one person; errMsg is a refused form's message.
func (s *server) personView(r *http.Request, t users.User, errMsg string) (pages.AdminUserView, error) {
	ctx := r.Context()
	now := s.now()
	v := pages.AdminUserView{
		CSRF: s.csrf.TokenFrom(ctx), ID: t.ID, Name: personName(t), RolePill: roleWord(t.Role), RoleKind: string(t.Role),
		Error: errMsg, Roles: rolesFor(s, r, t.Role), Off: t.Disabled, Today: now.Format("2006-01-02"),
		CanGrant: t.Role == users.RoleMember || t.Role == users.RoleGuest,
	}
	people, err := s.grants.People(ctx, now.UnixMilli(), monthStart(now).UnixMilli())
	if err != nil {
		return v, err
	}
	for _, p := range people {
		if p.User.ID == t.ID {
			text, _ := lastActiveText(now, p.LastActive, p.LastListen)
			v.Meta = "Last active " + strings.ToLower(text[:1]) + text[1:] + " · " + monthText(p.MonthMs) + " this month"
			if text == usersNever {
				v.Meta = "Never signed in"
			}
		}
	}
	if !v.CanGrant {
		return v, nil
	}
	list, err := s.grants.ForUserNamed(ctx, t.ID)
	if err != nil {
		return v, err
	}
	v.Grants = grantRows(list, now)
	libs, err := s.lib.Libraries(ctx)
	if err != nil {
		return v, err
	}
	for _, l := range libs {
		v.Libraries = append(v.Libraries, pages.Option{Value: l.ID, Label: l.Name})
	}
	shelves, err := s.grants.ShelfTargets(ctx)
	if err != nil {
		return v, err
	}
	for _, sh := range shelves {
		v.Shelves = append(v.Shelves, pages.Option{Value: sh.ID, Label: sh.Name + " · " + sh.OwnerName + " · " + usersBooksText(sh.Books)})
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		v.Q = q
		sc := s.scope(r)
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
	}
	return v, nil
}

// renderPerson answers with the person's page and the given status.
func (s *server) renderPerson(w http.ResponseWriter, r *http.Request, status int, t users.User, errMsg string) {
	v, err := s.personView(r, t, errMsg)
	if err != nil {
		s.serverError(w, r, "person page", err)
		return
	}
	s.pageStyled(w, r, status, v.Name, "admin-users", pages.AdminUser(v), nil, adminStyles...)
}

func (s *server) getUserPage(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadPerson(w, r)
	if !ok {
		return
	}
	s.renderPerson(w, r, http.StatusOK, t, "")
}

// personForm starts every unsafe person route: refuse people who manage nobody and the person
// themselves, then read the form. The full decision is made again inside the transaction.
func (s *server) personForm(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.allow(w, r, share.ManageUsers, share.Target{Kind: share.TargetServer}) {
		return "", false
	}
	id := r.PathValue("id")
	if id == currentUser(r).ID {
		s.notFound(w, r)
		return "", false
	}
	if !s.parseForm(w, r) {
		return "", false
	}
	return id, true
}

// recheckManage re-reads the signed-in person and the target inside tx and asks share.Can again on
// the fresh rows. It returns the fresh target, or errRefused (or users.ErrNotFound for an unknown
// target). newRole, when set, is the role the change would give the target.
func (s *server) recheckManage(ctx context.Context, tx *sql.Tx, actor users.User, targetID string, newRole users.Role) (users.User, error) {
	a, err := s.users.ByIDTx(ctx, tx, actor.ID)
	if errors.Is(err, users.ErrNotFound) {
		return users.User{}, errRefused
	}
	if err != nil {
		return users.User{}, err
	}
	if a.Disabled || a.Role != actor.Role || a.ID == targetID {
		return users.User{}, errRefused
	}
	t, err := s.users.ByIDTx(ctx, tx, targetID)
	if err != nil {
		return users.User{}, err
	}
	if !s.can.Can(ctx, a, share.ManageUsers, share.Target{Kind: share.TargetUser, ID: t.ID, Role: t.Role}) {
		return users.User{}, errRefused
	}
	if newRole != "" && !s.can.Can(ctx, a, share.ManageUsers, share.Target{Kind: share.TargetUser, ID: t.ID, Role: newRole}) {
		return users.User{}, errRefused
	}
	return t, nil
}

// changePerson runs fn on the fresh target inside one write transaction. On success it returns
// true; otherwise it has answered.
func (s *server) changePerson(w http.ResponseWriter, r *http.Request, id string, newRole users.Role,
	fn func(ctx context.Context, tx *sql.Tx, actor, target users.User) error) bool {
	ctx := r.Context()
	actor := currentUser(r)
	err := s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		t, err := s.recheckManage(ctx, tx, actor, id, newRole)
		if err != nil {
			return err
		}
		return fn(ctx, tx, actor, t)
	})
	if err == nil {
		return true
	}
	s.personError(w, r, id, err)
	return false
}

// personError answers a failed change: 404 for anything the person may not touch, 422 with plain
// words for a bad form (a toast for htmx, the page again otherwise), 500 for the rest.
func (s *server) personError(w http.ResponseWriter, r *http.Request, id string, err error) {
	var msg string
	var fe formError
	switch {
	case errors.As(err, &fe):
		msg = string(fe)
	case errors.Is(err, share.ErrGrantScope), errors.Is(err, share.ErrGrantTarget):
		msg = usersErrTarget
	case errors.Is(err, share.ErrGrantExpiry), errors.Is(err, errUntilDay):
		msg = usersErrDay
	case errors.Is(err, errRefused), errors.Is(err, users.ErrNotFound), errors.Is(err, users.ErrSelf),
		errors.Is(err, users.ErrOwnerFixed), errors.Is(err, share.ErrGrantRole), errors.Is(err, share.ErrGrantNotFound):
		s.notFound(w, r)
		return
	default:
		s.serverError(w, r, "change person", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msg))
		return
	}
	t, uerr := s.users.ByID(r.Context(), id)
	if uerr != nil {
		s.notFound(w, r)
		return
	}
	s.renderPerson(w, r, http.StatusUnprocessableEntity, t, msg)
}

func (s *server) backToPerson(w http.ResponseWriter, r *http.Request, id string) {
	s.redirect(w, r, "/admin/users/"+id)
}

func (s *server) postPersonRole(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	to := users.Role(r.PostForm.Get("role"))
	valid := to == users.RoleAdmin || to == users.RoleMember || to == users.RoleGuest
	newRole := to
	if !valid {
		newRole = ""
	}
	done := s.changePerson(w, r, id, newRole, func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		if !valid {
			return formError(usersErrRole)
		}
		from, err := s.users.SetRole(ctx, tx, actor.ID, id, to)
		if err != nil {
			return err
		}
		if to == users.RoleAdmin && from != users.RoleAdmin {
			// Admins see everything by role; grants left behind would come back on a demotion.
			_, err = s.grants.RevokeAll(ctx, tx, actor, id)
		}
		return err
	})
	if done {
		s.backToPerson(w, r, id)
	}
}

func (s *server) getPersonDisable(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadPerson(w, r)
	if !ok {
		return
	}
	name := personName(t)
	s.confirmPage(w, r, t.ID, fmt.Sprintf(usersOffTitle, name), fmt.Sprintf(usersOffBody, name),
		"/admin/users/"+t.ID+"/disable", usersOffConfirm, usersOffKeep, "")
}

// confirmPage shows a confirmation. ret, when it is a valid Share & invite address, is where "keep"
// goes and where the action returns to afterwards; otherwise both use the person's page.
func (s *server) confirmPage(w http.ResponseWriter, r *http.Request, id, title, body, action, confirm, keep, ret string) {
	v := pages.ConfirmView{CSRF: s.csrf.TokenFrom(r.Context()), Title: title, Body: body, Action: action,
		Confirm: confirm, Keep: keep, Back: "/admin/users/" + id}
	if ret != "" {
		v.Back, v.Return = ret, ret
	}
	s.pageStyled(w, r, http.StatusOK, title, "admin-users", pages.AdminUserConfirm(v), nil, adminStyles...)
}

func (s *server) postPersonDisable(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		if err := s.users.SetDisabled(ctx, tx, actor.ID, id, true); err != nil {
			return err
		}
		n, err := s.sessions.EndAllForUser(ctx, tx, id, s.now().UnixMilli())
		if err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: actor.ID, Action: "user.signout",
			Target: "user:" + id, Detail: map[string]any{"sessions": n}})
	})
	if done {
		s.backToPerson(w, r, id)
	}
}

// postPersonDeviceSignOut ends one signed-in browser or app of another person: did is a session
// public id (16 hex) or a device id. The same re-check as every change to a person runs inside the
// transaction, so self, the owner (for an admin) and members all answer 404.
func (s *server) postPersonDeviceSignOut(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	did := r.PathValue("did")
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		now := s.now().UnixMilli()
		kind := "device"
		if auth.IsSessionPublicID(did) {
			kind = "session"
			res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND substr(lower(hex(token_hash)), 1, ?) = ?`,
				id, auth.PublicIDLen, did)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				return users.ErrNotFound
			}
		} else {
			found, err := s.devices.Revoke(ctx, tx, id, did, now)
			if err != nil {
				return err
			}
			if !found {
				return users.ErrNotFound
			}
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: actor.ID, Action: "user.device_signout",
			Target: "user:" + id, Detail: map[string]string{"kind": kind}})
	})
	if done {
		s.redirect(w, r, "/admin/users#users-devices")
	}
}

func (s *server) postPersonEnable(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		return s.users.SetDisabled(ctx, tx, actor.ID, id, false)
	})
	if done {
		s.backToPerson(w, r, id)
	}
}

// grantSpecFromForm reads the "Share more" form.
func (s *server) grantSpecFromForm(r *http.Request, userID string) (share.GrantSpec, error) {
	f := r.PostForm
	spec := share.GrantSpec{UserID: userID, Scope: share.GrantScope(f.Get("scope")),
		CanDownload: f.Get("download") == "1", CanRead: f.Get("read") == "1"}
	switch spec.Scope {
	case share.GrantAll:
	case share.GrantLibrary:
		spec.TargetID = f.Get("library")
	case share.GrantShelf:
		spec.TargetID = f.Get("shelf")
	case share.GrantBook:
		spec.TargetID = f.Get("book")
	default:
		return spec, formError(usersErrTarget)
	}
	if spec.Scope != share.GrantAll && spec.TargetID == "" {
		return spec, formError(usersErrTarget)
	}
	end, err := parseUntil(f.Get("until"), s.now())
	spec.ExpiresAt = end
	return spec, err
}

func (s *server) postGrantAdd(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		spec, err := s.grantSpecFromForm(r, id)
		if err != nil {
			return err
		}
		_, err = s.grants.Put(ctx, tx, actor, spec)
		return err
	})
	if done {
		s.backToPerson(w, r, id)
	}
}

// ownGrant loads grant gid and checks that it belongs to the person in the path.
func (s *server) ownGrant(ctx context.Context, id, gid string) (share.Grant, error) {
	g, err := s.grants.Get(ctx, gid)
	if err != nil {
		return g, err
	}
	if g.UserID != id {
		return g, share.ErrGrantNotFound
	}
	return g, nil
}

func (s *server) postGrantUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		g, err := s.ownGrant(ctx, id, r.PathValue("gid"))
		if err != nil {
			return err
		}
		end, err := parseUntil(r.PostForm.Get("until"), s.now())
		if err != nil {
			return err
		}
		_, err = s.grants.Put(ctx, tx, actor, share.GrantSpec{UserID: id, Scope: g.Scope, TargetID: g.TargetID,
			CanDownload: r.PostForm.Get("download") == "1", CanRead: r.PostForm.Get("read") == "1", ExpiresAt: end})
		return err
	})
	if done {
		s.backToPerson(w, r, id)
	}
}

func (s *server) postGrantRenew(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		g, err := s.ownGrant(ctx, id, r.PathValue("gid"))
		if err != nil {
			return err
		}
		_, err = s.grants.Put(ctx, tx, actor, share.GrantSpec{UserID: id, Scope: g.Scope, TargetID: g.TargetID,
			CanDownload: g.CanDownload, CanRead: g.CanRead, ExpiresAt: renewEnd(s.now())})
		return err
	})
	if done {
		s.backToPerson(w, r, id)
	}
}

func (s *server) postGrantRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		g, err := s.ownGrant(ctx, id, r.PathValue("gid"))
		if err != nil {
			return err
		}
		return s.grants.Revoke(ctx, tx, actor, g.ID)
	})
	if done {
		s.afterChange(w, r, id)
	}
}

func (s *server) getPersonStop(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadPerson(w, r)
	if !ok {
		return
	}
	name := personName(t)
	ret, bookID, ok := validShareBack(r.URL.Query().Get("back"))
	if !ok {
		ret = ""
	}
	if bookID != "" {
		// From a book's page, stopping removes only the share that gives that book.
		if gid, title := s.bookGrantOf(r, t.ID, bookID); gid != "" {
			s.confirmPage(w, r, t.ID, fmt.Sprintf(shareStopBookTitle, title, name), fmt.Sprintf(shareStopBookBody, name),
				"/admin/users/"+t.ID+"/grants/"+gid+"/remove", usersStopConfirm, usersStopKeep, ret)
			return
		}
	}
	s.confirmPage(w, r, t.ID, fmt.Sprintf(usersStopTitle, name), fmt.Sprintf(usersStopBody, name),
		"/admin/users/"+t.ID+"/stop", usersStopConfirm, usersStopKeep, ret)
}

// bookGrantOf finds the grant that gives userID the book itself, and the book's title; the id is
// empty when there is none.
func (s *server) bookGrantOf(r *http.Request, userID, bookID string) (gid, title string) {
	list, err := s.grants.ForUserNamed(r.Context(), userID)
	if err != nil {
		return "", ""
	}
	for _, g := range list {
		if g.Scope == share.GrantBook && g.TargetID == bookID {
			return g.ID, g.Name
		}
	}
	return "", ""
}

// afterChange answers a finished change: back to the Share & invite page it came from (a valid
// "back"), else the person's page.
func (s *server) afterChange(w http.ResponseWriter, r *http.Request, id string) {
	if back, _, ok := validShareBack(r.PostForm.Get("back")); ok {
		s.redirect(w, r, back)
		return
	}
	s.backToPerson(w, r, id)
}

func (s *server) postPersonStop(w http.ResponseWriter, r *http.Request) {
	id, ok := s.personForm(w, r)
	if !ok {
		return
	}
	done := s.changePerson(w, r, id, "", func(ctx context.Context, tx *sql.Tx, actor, t users.User) error {
		n, err := s.grants.RevokeAll(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: actor.ID, Action: "user.stop_sharing",
			Target: "user:" + id, Detail: map[string]any{"grants": n}})
	})
	if done {
		s.afterChange(w, r, id)
	}
}
