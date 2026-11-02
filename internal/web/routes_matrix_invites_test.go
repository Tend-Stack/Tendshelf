package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// inviteSeed makes the invites the matrix rows act on, as the owner, and gives the admin, member
// and guest of the sweep a password: the test helper makes them without one, and /welcome is only
// for accounts that have none.
func inviteSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.a.st.Writer().Exec(`UPDATE users SET password_hash = 'x' WHERE password_hash IS NULL`); err != nil {
		t.Fatal(err)
	}
	owner, err := e.a.srv.users.ByID(ctx, e.uid[sweepOwner])
	if err != nil {
		t.Fatal(err)
	}
	create := func(s share.InviteSpec) (share.Invite, string) {
		var inv share.Invite
		var tok string
		err := e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			var err error
			inv, tok, err = e.a.srv.invites.Create(ctx, tx, owner, s)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return inv, tok
	}
	member := func(name string) share.InviteSpec {
		return share.InviteSpec{Name: name, Role: users.RoleMember, Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.libID}}}
	}
	_, e.inviteTok = create(share.InviteSpec{Name: "Inviteone", Role: users.RoleAdmin})
	inv, _ := create(member("Inviterot"))
	e.inviteRot = inv.ID
	inv, _ = create(member("Invitecancel"))
	e.inviteCancel = inv.ID
	inv, _ = create(share.InviteSpec{Name: "Inviteadmin", Role: users.RoleAdmin})
	e.inviteAdmin = inv.ID
}

// invitesSweepRows are the route matrix rows of the invite routes (P3-3a). The public routes answer
// every role and the visitor the same; the two admin routes are for people who manage users, and an
// admin may not touch an admin invite.
func invitesSweepRows() []sweepRow {
	unknown := "/invite/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	tok := func(suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/invite/" + e.inviteTok + suffix }
	}
	admin := func(id func(*sweepEnv) string, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/admin/invites/" + id(e) + suffix }
	}
	rot := func(e *sweepEnv) string { return e.inviteRot }
	can := func(e *sweepEnv) string { return e.inviteCancel }
	adm := func(e *sweepEnv) string { return e.inviteAdmin }

	valid := sweepGet("GET /invite/{token}", tok(""), sweepSame(http.StatusOK))
	valid.Label, valid.Anon = "valid", http.StatusOK
	broken := sweepGet("GET /invite/{token}", sweepStatic(unknown), sweepSame(http.StatusNotFound))
	broken.Label, broken.Anon = "unknown", http.StatusNotFound
	post := sweepPost("POST /invite/{token}", sweepStatic(unknown), sweepFormOf(url.Values{}), sweepSame(http.StatusNotFound), "")
	post.Label, post.Anon = "unknown", http.StatusNotFound
	cover := sweepGet("GET /invite/{token}/cover", sweepStatic(unknown+"/cover"), sweepSame(http.StatusNotFound))
	cover.Anon = http.StatusNotFound
	welcome := sweepGet("GET /welcome", sweepStatic("/welcome"), sweepSame(http.StatusSeeOther))
	welcome.Loc = "/"
	welcomePost := sweepPost("POST /welcome", sweepStatic("/welcome"), sweepFormOf(url.Values{"keep": {"phone"}}), sweepSame(http.StatusNotFound), "")

	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	staffPost := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	onlyOwnerPage := [4]int{http.StatusOK, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}
	onlyOwnerPost := [4]int{http.StatusSeeOther, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}
	link := sweepPost("POST /admin/invites/{id}/link", admin(rot, "/link"), nil, staff, "")
	linkAdmin := sweepPost("POST /admin/invites/{id}/link", admin(adm, "/link"), nil, onlyOwnerPage, "")
	linkAdmin.Label = "admin invite"
	cancel := sweepPost("POST /admin/invites/{id}/cancel", admin(can, "/cancel"), nil, staffPost, "/admin/users")
	cancelAdmin := sweepPost("POST /admin/invites/{id}/cancel", admin(adm, "/cancel"), nil, onlyOwnerPost, "/admin/users")
	cancelAdmin.Label = "admin invite"
	return []sweepRow{valid, broken, post, cover, welcome, welcomePost, link, linkAdmin, cancel, cancelAdmin}
}
