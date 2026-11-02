package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// emailSeed sets up the email relay as a finished, tested one (settings saved, a test job done) and makes
// the invites the email rows act on. Nothing is sent: the sweep's job runner is never started.
func emailSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	ctx := context.Background()
	for k, v := range map[string]any{"smtp.host": "mail.example.org", "smtp.port": 587, "smtp.tls": "starttls",
		"smtp.from_address": "noreply@example.org"} {
		if err := db.PutSetting(ctx, e.a.st.Writer(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	e.emailJob = "jb_emailtest"
	if _, err := e.a.st.Writer().Exec(`INSERT INTO jobs(id, kind, state, created_by, created_at, updated_at) VALUES (?, 'smtp.test', 'done', ?, 1, 1)`,
		e.emailJob, e.uid[sweepOwner]); err != nil {
		t.Fatal(err)
	}
	owner, err := e.a.srv.users.ByID(ctx, e.uid[sweepOwner])
	if err != nil {
		t.Fatal(err)
	}
	create := func(s share.InviteSpec) share.Invite {
		var inv share.Invite
		err := e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			var err error
			inv, _, err = e.a.srv.invites.Create(ctx, tx, owner, s)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	e.inviteMail = create(share.InviteSpec{Name: "Mailone", Email: "mailone@example.org", Role: users.RoleMember,
		Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.libID}}}).ID
	e.inviteMailAdmin = create(share.InviteSpec{Name: "Mailadmin", Email: "mailadmin@example.org", Role: users.RoleAdmin}).ID
}

// emailSweepRows are the route matrix rows of the email routes (P3-8a). The settings are the owner's
// alone (the server manage action): admins, members and guests get a 404. "Send it for me" is for
// people who manage users, and an admin may not touch an admin invite.
func emailSweepRows() []sweepRow {
	ownerOnlyPage := sweepOwnerOnly(http.StatusOK, http.StatusNotFound)
	ownerOnlyPost := sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound)
	save := sweepPost("POST /admin/email", sweepStatic("/admin/email"), sweepFormOf(url.Values{
		"host": {"mail.example.org"}, "port": {"587"}, "tls": {"starttls"}, "username": {"mailer"}, "password": {"pw"},
		"from_name": {"Home"}, "from_address": {"noreply@example.org"}}), ownerOnlyPost, "/admin/email?saved=1")
	test := sweepPost("POST /admin/email/test", sweepStatic("/admin/email/test"), sweepFormOf(url.Values{"to": {"me@example.org"}}), ownerOnlyPage, "")
	off := sweepPost("POST /admin/email/off", sweepStatic("/admin/email/off"), nil, ownerOnlyPost, "/admin/email")
	poll := sweepGet("GET /hx/admin/email/test/{id}", func(e *sweepEnv, _ int) string { return "/hx/admin/email/test/" + e.emailJob }, ownerOnlyPage)
	invite := sweepPost("POST /admin/invites/{id}/email", func(e *sweepEnv, _ int) string { return "/admin/invites/" + e.inviteMail + "/email" },
		nil, sweepStaff(http.StatusSeeOther, http.StatusNotFound), "/admin/users")
	adminInvite := sweepPost("POST /admin/invites/{id}/email", func(e *sweepEnv, _ int) string { return "/admin/invites/" + e.inviteMailAdmin + "/email" },
		nil, ownerOnlyPost, "/admin/users")
	adminInvite.Label = "admin invite"
	return []sweepRow{sweepGet("GET /admin/email", sweepStatic("/admin/email"), ownerOnlyPage), save, test, poll, off, invite, adminInvite}
}
