package web

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Emailed invitations. The default path needs no server setup: "Send invitation" creates the invite and
// hands the person a mailto: address that opens their own email app with the message ready. When the
// optional relay is set up and tested, "Send it for me" queues a job that Tendshelf sends itself.

// relayChecks refuses a "Send it for me" the relay can't or shouldn't do, and counts one send against
// the inviter's and the address's limits.
func (s *server) relayChecks(r *http.Request, email string) error {
	if !s.emailRelayReady(r) {
		return shareFormError(shareErrRelayOff)
	}
	if !share.ValidMailAddress(email) {
		return shareFormError(shareErrEmailBad)
	}
	user := "u:" + currentUser(r).ID
	addr := "a:" + strings.ToLower(email)
	if ok, _ := s.mailLim.inviter.Allow(user); !ok {
		return shareFormError(shareErrMailBusy)
	}
	if ok, _ := s.mailLim.address.Allow(addr); !ok {
		return shareFormError(shareErrMailAgain)
	}
	s.mailLim.inviter.Fail(user)
	s.mailLim.address.Fail(addr)
	return nil
}

// daysLeft is how many more days a link works, counted up, at least 1; 0 when it has no end.
func daysLeft(endMs int64, now time.Time) int {
	if endMs == 0 {
		return 0
	}
	left := time.UnixMilli(endMs).Sub(now)
	return max(1, int((left+24*time.Hour-1)/(24*time.Hour)))
}

// inviteMessage is the words of the email for an invite whose link is url.
func (s *server) inviteMessage(r *http.Request, inv share.Invite, url string) share.InviteMessage {
	return share.InviteMessage{Inviter: inv.InviterName, Invitee: inv.Name, ServerName: s.serverName(r.Context()),
		Note: inv.Note, URL: url, Days: daysLeft(inv.ExpiresAt, s.now())}
}

// renderShareMail answers a created emailed invitation: the page with the panel that shows the link
// once. With relay, the email is queued first so the pending row already reads Sending.
func (s *server) renderShareMail(w http.ResponseWriter, r *http.Request, bookID string, inv share.Invite, tok string, relay bool) {
	url := s.inviteURL(r, tok)
	msg := s.inviteMessage(r, inv, url)
	panel := &pages.ShareMail{URL: url, Message: msg.Body()}
	queueFailed := false
	if relay {
		if _, err := s.mail.QueueInvite(r.Context(), currentUser(r), inv, url); err != nil {
			slog.Error("queue invite email", "err", err)
			queueFailed = true
		}
	}
	if relay && !queueFailed {
		panel.Title, panel.Note = fmt.Sprintf(shareMailSentTitle, inv.Email), fmt.Sprintf(shareMailSentNote, inv.Email)
	} else {
		panel.Title, panel.Note = fmt.Sprintf(shareMailReadyTitle, inv.Name), fmt.Sprintf(shareMailReadyNote, inv.Email)
		panel.Mailto = msg.Mailto(inv.Email)
	}
	v, err := s.sharePageView(r, bookID)
	if err == library.ErrNotFound {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "share page", err)
		return
	}
	if queueFailed {
		v.Error = shareErrQueue
	}
	v.Mail = panel
	s.renderShare(w, r, http.StatusOK, v)
}

// sharePendingMail is the pending row's line about the email, and whether it went wrong.
func sharePendingMail(inv share.Invite) (string, bool) {
	if inv.Email == "" {
		return "", false
	}
	if inv.MailJobID == "" {
		return fmt.Sprintf(shareMailCreated, inv.Email), false
	}
	switch inv.MailState {
	case "queued", "running":
		return fmt.Sprintf(shareMailSending, inv.Email), false
	case "done":
		return fmt.Sprintf(shareMailSent, inv.Email), false
	case "failed":
		switch inv.MailCode {
		case "mail_recipient":
			return fmt.Sprintf(shareMailBadAddress, inv.Email), true
		case "mail_link_lost":
			return fmt.Sprintf(shareMailLinkLost, inv.Email), true
		case "mail_unconfirmed":
			return fmt.Sprintf(shareMailUnconfirmed, inv.Email), true
		}
		return shareMailSettings, true
	}
	return fmt.Sprintf(shareMailCreated, inv.Email), false
}

// sharePendingResend is the action that has Tendshelf send the invite, and its label: offered when the
// relay is on, the invite has an address, and no send is in progress.
func sharePendingResend(inv share.Invite, relayOn bool) (action, label string) {
	if inv.Email == "" || !relayOn || inv.MailState == "queued" || inv.MailState == "running" {
		return "", ""
	}
	action = "/admin/invites/" + inv.ID + "/email"
	if inv.MailJobID == "" {
		return action, shareMailSendForMe
	}
	return action, shareMailSendAgain
}

// postInviteEmail has Tendshelf email an invite's link: it gives the invite a new link (the old one
// stops working, as "Copy link" does) and queues the mail. 404 for anything the person may not touch.
func (s *server) postInviteEmail(w http.ResponseWriter, r *http.Request) {
	if !s.inviteAdminAction(w, r) {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	user := currentUser(r)
	inv, err := s.invites.Get(ctx, id)
	if err != nil {
		s.inviteActionError(w, r, err)
		return
	}
	if !slices.Contains(share.ManageableInviteRoles(user), inv.Role) || inv.State(s.now().UnixMilli()) != share.InvitePending {
		s.notFound(w, r)
		return
	}
	switch {
	case inv.Email == "":
		s.inviteMailRefuse(w, r, http.StatusUnprocessableEntity, shareErrNoEmail)
		return
	case !s.emailRelayReady(r):
		s.inviteMailRefuse(w, r, http.StatusUnprocessableEntity, shareErrRelayOff)
		return
	case inv.MailState == "queued" || inv.MailState == "running":
		s.inviteMailRefuse(w, r, http.StatusConflict, shareErrStillSending)
		return
	}
	if err := s.relayChecks(r, inv.Email); err != nil {
		status := http.StatusUnprocessableEntity
		if sf, ok := err.(shareFormError); ok && (sf == shareErrMailBusy || sf == shareErrMailAgain) {
			status = http.StatusTooManyRequests
			w.Header().Set("Retry-After", "3600")
		}
		s.inviteMailRefuse(w, r, status, err.Error())
		return
	}
	var tok string
	err = s.lib.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		tok, err = s.invites.Rotate(ctx, tx, user, id)
		return err
	})
	if err != nil {
		s.inviteActionError(w, r, err)
		return
	}
	if _, err := s.mail.QueueInvite(ctx, user, inv, s.inviteURL(r, tok)); err != nil {
		s.serverError(w, r, "queue invite email", err)
		return
	}
	if back, _, ok := validShareBack(r.PostForm.Get("back")); ok {
		s.answerPage(w, r, back)
		return
	}
	s.answerPage(w, r, "/admin/users")
}

// inviteMailRefuse answers a refused send in plain words: a toast for htmx, else the Share page again.
func (s *server) inviteMailRefuse(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
		return
	}
	_, bookID, ok := validShareBack(r.PostForm.Get("back"))
	if !ok {
		bookID = ""
	}
	v, err := s.sharePageView(r, bookID)
	if err != nil {
		s.errorPage(w, r, status)
		return
	}
	v.Error = msg
	s.renderShare(w, r, status, v)
}
