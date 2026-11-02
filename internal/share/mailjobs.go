package share

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Job kinds of the email relay.
const (
	KindInviteMail  = "invite.mail"
	KindSMTPTest    = "smtp.test"
	inviteMailTries = 4

	settingServerName = "server.name" // the same row the setup steps write
	fallbackServer    = "Tendshelf"
)

// Mail sends email through the owner's relay as background jobs. Requests never wait on a mail
// server: they queue a job and the page reads its state.
type Mail struct {
	st     *db.Store
	runner *jobs.Runner
	outbox *mailOutbox
	opts   MailOptions
}

// NewMail returns the mailer. Register adds its job kinds to the runner before the runner starts.
func NewMail(st *db.Store, runner *jobs.Runner, o MailOptions) *Mail {
	return &Mail{st: st, runner: runner, outbox: newOutbox(), opts: o}
}

// Register adds the job kinds: invite.mail (up to four tries) and smtp.test (one).
func (m *Mail) Register() error {
	if err := m.runner.Register(jobs.Kind{Name: KindInviteMail, Concurrency: 1, MaxAttempts: inviteMailTries, Handler: m.handleInvite}); err != nil {
		return err
	}
	return m.runner.Register(jobs.Kind{Name: KindSMTPTest, Concurrency: 1, MaxAttempts: 1, Handler: m.handleTest})
}

func newRef() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// QueueInvite queues the email for a pending invite (inv.Email must be set). The link is kept in
// memory under a random reference; the job holds the invite id and that reference only.
func (m *Mail) QueueInvite(ctx context.Context, by users.User, inv Invite, url string) (string, error) {
	if inv.Email == "" {
		return "", errors.New("share: the invite has no email address")
	}
	ref, err := newRef()
	if err != nil {
		return "", err
	}
	m.outbox.put(ref, outboxEntry{URL: url, At: m.now()})
	id, err := m.runner.Enqueue(ctx, KindInviteMail, map[string]string{"invite_id": inv.ID, "ref": ref}, jobs.EnqueueOptions{CreatedBy: by.ID})
	if err != nil {
		m.outbox.drop(ref)
		return "", err
	}
	err = m.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET mail_job_id = ? WHERE id = ?`, id, inv.ID); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: m.now().UnixMilli(), UserID: by.ID, Action: "invite.mail",
			Target: "invite:" + inv.ID, Detail: map[string]any{"job": id}})
	})
	return id, err
}

// QueueTest queues a test email to the given address.
func (m *Mail) QueueTest(ctx context.Context, by users.User, to string) (string, error) {
	if !ValidMailAddress(to) {
		return "", errHeader
	}
	ref, err := newRef()
	if err != nil {
		return "", err
	}
	m.outbox.put(ref, outboxEntry{To: to, At: m.now()})
	id, err := m.runner.Enqueue(ctx, KindSMTPTest, map[string]string{"ref": ref}, jobs.EnqueueOptions{CreatedBy: by.ID})
	if err != nil {
		m.outbox.drop(ref)
		return "", err
	}
	err = db.Audit(ctx, m.st.Writer(), db.AuditEntry{At: m.now().UnixMilli(), UserID: by.ID, Action: "smtp.test",
		Target: "server", Detail: map[string]any{"job": id}})
	return id, err
}

func (m *Mail) serverName(ctx context.Context) string {
	var name string
	if _, err := db.GetSetting(ctx, m.st.Reader(), settingServerName, &name); err != nil || strings.TrimSpace(name) == "" {
		return fallbackServer
	}
	return name
}

// handleInvite sends one invitation. It returns a *jobs.Error only when something went wrong.
func (m *Mail) handleInvite(ctx context.Context, t *jobs.Task) (any, error) {
	var p struct {
		InviteID string `json:"invite_id"`
		Ref      string `json:"ref"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, mailError("mail_link_lost", true)
	}
	inv, err := scanInvite(m.st.Reader().QueryRowContext(ctx, `SELECT `+inviteColumns+inviteFrom+` WHERE i.id = ?`, p.InviteID))
	if errors.Is(err, sql.ErrNoRows) {
		m.outbox.drop(p.Ref)
		return map[string]bool{"skipped": true}, nil
	}
	if err != nil {
		return nil, err
	}
	now := m.now()
	if inv.State(now.UnixMilli()) != InvitePending {
		m.outbox.drop(p.Ref)
		return map[string]bool{"skipped": true}, nil
	}
	cfg, err := LoadMailConfig(ctx, m.st.Reader())
	if err != nil {
		return nil, err
	}
	if !cfg.Ready() {
		m.outbox.drop(p.Ref)
		return nil, m.classify(errNotReady)
	}
	entry, ok := m.outbox.get(p.Ref, now)
	if !ok {
		return nil, mailError("mail_link_lost", true)
	}
	msg := InviteMessage{Inviter: inv.InviterName, Invitee: inv.Name, ServerName: m.serverName(ctx), Note: inv.Note, URL: entry.URL}
	if inv.ExpiresAt != 0 {
		msg.Days = max(1, int(math.Ceil(float64(inv.ExpiresAt-now.UnixMilli())/float64(24*time.Hour/time.Millisecond))))
	}
	for _, part := range msg.HeaderParts() {
		if hasLineBreak(part) {
			m.outbox.drop(p.Ref)
			return nil, m.classify(errHeader)
		}
	}
	if err := m.send(ctx, cfg, Message{To: inv.Email, Subject: msg.Subject(), Body: msg.Body()}); err != nil {
		je := m.classify(err)
		if je.Permanent || t.Attempt >= inviteMailTries {
			m.outbox.drop(p.Ref)
		}
		return nil, je
	}
	m.outbox.drop(p.Ref)
	return map[string]bool{"sent": true}, nil
}

// handleTest sends the test email.
func (m *Mail) handleTest(ctx context.Context, t *jobs.Task) (any, error) {
	var p struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, mailError("mail_link_lost", true)
	}
	entry, ok := m.outbox.get(p.Ref, m.now())
	m.outbox.drop(p.Ref)
	if !ok {
		return nil, mailError("mail_link_lost", true)
	}
	cfg, err := LoadMailConfig(ctx, m.st.Reader())
	if err != nil {
		return nil, err
	}
	subject, body := TestMessage(m.serverName(ctx))
	if hasLineBreak(subject) {
		return nil, m.classify(errHeader)
	}
	if err := m.send(ctx, cfg, Message{To: entry.To, Subject: subject, Body: body}); err != nil {
		return nil, m.classify(err)
	}
	return map[string]bool{"sent": true}, nil
}
