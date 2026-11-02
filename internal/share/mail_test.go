package share

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/testutil/smtpfake"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const mailTestPassword = "s3cret-pw-zq"

type mailEnv struct {
	*inviteEnv
	runner *jobs.Runner
	mail   *Mail
	logs   *bytes.Buffer
}

// newMailEnv builds the fixture, a runner with tiny back-off (not started yet) and a Mail that trusts
// the fake server's certificate.
func newMailEnv(t *testing.T, fake *smtpfake.Server) *mailEnv {
	t.Helper()
	e := &mailEnv{inviteEnv: newInviteEnv(t), logs: &bytes.Buffer{}}
	var err error
	e.runner, err = jobs.New(e.f.st, jobs.Options{PollEvery: 20 * time.Millisecond, Backoff: func(int) time.Duration { return 10 * time.Millisecond }})
	if err != nil {
		t.Fatal(err)
	}
	opts := MailOptions{Logger: slog.New(slog.NewTextHandler(e.logs, nil))}
	if fake != nil {
		opts.RootCAs = fake.Pool()
	}
	e.mail = NewMail(e.f.st, e.runner, opts)
	if err := e.mail.Register(); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *mailEnv) start(t *testing.T) {
	t.Helper()
	if err := e.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.runner.Stop(context.Background()) })
}

// configure stores the relay settings directly (the save path is tested in mailconfig_test.go).
func (e *mailEnv) configure(t *testing.T, c MailConfig) {
	t.Helper()
	for k, v := range map[string]any{settingSMTPHost: c.Host, settingSMTPPort: c.Port, settingSMTPTLS: c.TLS,
		settingSMTPUsername: c.Username, settingSMTPPassword: c.Password, settingSMTPFromName: c.FromName, settingSMTPFromAddr: c.FromAddress} {
		if err := db.PutSetting(context.Background(), e.f.st.Writer(), k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func fakeConfig(f *smtpfake.Server, tlsMode string) MailConfig {
	return MailConfig{Host: f.Host(), Port: f.Port(), TLS: tlsMode, Username: "mailer", Password: mailTestPassword,
		FromName: "Home", FromAddress: "noreply@example.org"}
}

func (e *mailEnv) wait(t *testing.T, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := e.runner.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == jobs.StateDone || j.State == jobs.StateFailed {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return jobs.Job{}
}

func (e *mailEnv) queue(t *testing.T) (Invite, string) {
	t.Helper()
	inv, tok := e.mustCreate(t, "u_own", validSpec())
	id, err := e.mail.QueueInvite(context.Background(), e.f.users["u_own"], inv, "http://tend.example/invite/"+tok)
	if err != nil {
		t.Fatal(err)
	}
	return inv, id
}

func validSpec() InviteSpec {
	return InviteSpec{Name: "Rosa", Email: "rosa@example.org", Note: "See you there", Role: users.RoleAdmin}
}

func codeOf(err error, m *Mail) string {
	if err == nil {
		return ""
	}
	return m.classify(err).Code
}

func TestMailSendTable(t *testing.T) {
	msg := Message{To: "rosa@example.org", Subject: "Hello", Body: "Line one\nLine two\n"}
	cases := []struct {
		name     string
		opts     smtpfake.Options
		tlsMode  string
		badPool  bool
		host     string // overrides the config host
		wantCode string
		sent     int
		sessions int // -1 = do not check
	}{
		{name: "starttls ok", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword}, tlsMode: TLSStartTLS, sent: 1, sessions: -1},
		{name: "starttls missing", opts: smtpfake.Options{NoStartTLS: true, User: "mailer", Pass: mailTestPassword}, tlsMode: TLSStartTLS, wantCode: "mail_no_tls", sessions: -1},
		{name: "implicit tls ok", opts: smtpfake.Options{ImplicitTLS: true, User: "mailer", Pass: mailTestPassword}, tlsMode: TLSImplicit, sent: 1, sessions: -1},
		{name: "unknown certificate", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword}, tlsMode: TLSStartTLS, badPool: true, wantCode: "mail_tls", sessions: -1},
		{name: "unknown certificate implicit", opts: smtpfake.Options{ImplicitTLS: true, User: "mailer", Pass: mailTestPassword}, tlsMode: TLSImplicit, badPool: true, wantCode: "mail_tls", sessions: -1},
		{name: "none to loopback", opts: smtpfake.Options{NoStartTLS: true}, tlsMode: TLSNone, sent: 1, sessions: -1},
		{name: "none to a remote name", opts: smtpfake.Options{NoStartTLS: true}, tlsMode: TLSNone, host: "example.com", wantCode: "mail_no_tls", sessions: 0},
		{name: "wrong password", opts: smtpfake.Options{User: "mailer", Pass: "other"}, tlsMode: TLSStartTLS, wantCode: "mail_auth", sessions: -1},
		{name: "sender refused", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"mail": 554}}, tlsMode: TLSStartTLS, wantCode: "mail_sender", sessions: -1},
		{name: "recipient refused", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"rcpt": 550}}, tlsMode: TLSStartTLS, wantCode: "mail_recipient", sessions: -1},
		{name: "message refused", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"data": 554}}, tlsMode: TLSStartTLS, wantCode: "mail_rejected", sessions: -1},
		{name: "busy", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"rcpt": 451}}, tlsMode: TLSStartTLS, wantCode: "mail_busy", sessions: -1},
		{name: "dropped after data", opts: smtpfake.Options{User: "mailer", Pass: mailTestPassword, DropAfterData: true}, tlsMode: TLSStartTLS, wantCode: "mail_unconfirmed", sent: 1, sessions: -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := smtpfake.Start(t, c.opts)
			e := newMailEnv(t, fake)
			if c.badPool {
				e.mail.opts.RootCAs = nil
			}
			cfg := fakeConfig(fake, c.tlsMode)
			if c.opts.User == "" {
				cfg.Username, cfg.Password = "", ""
			}
			if c.host != "" {
				cfg.Host = c.host
			}
			err := e.mail.send(context.Background(), cfg, msg)
			if got := codeOf(err, e.mail); got != c.wantCode {
				t.Fatalf("code = %q (err %v), want %q", got, err, c.wantCode)
			}
			if n := len(fake.Messages()); n != c.sent {
				t.Fatalf("messages = %d, want %d", n, c.sent)
			}
			if c.sessions >= 0 && fake.Sessions() != c.sessions {
				t.Fatalf("sessions = %d, want %d", fake.Sessions(), c.sessions)
			}
			if c.sent == 1 && c.wantCode == "" {
				m := fake.Messages()[0]
				if c.tlsMode != TLSNone && !m.TLS {
					t.Error("message arrived without TLS")
				}
				if m.From != cfg.FromAddress || m.To != msg.To {
					t.Errorf("envelope = %q -> %q", m.From, m.To)
				}
				if c.opts.User != "" && m.AuthUser != "mailer" {
					t.Errorf("auth user = %q", m.AuthUser)
				}
			}
		})
	}
}

func TestMailPermanentAndRetriedByJob(t *testing.T) {
	t.Run("retry then sent", func(t *testing.T) {
		fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"rcpt": 451}, FailTimes: 1})
		e := newMailEnv(t, fake)
		e.configure(t, fakeConfig(fake, TLSStartTLS))
		_, id := e.queue(t)
		e.start(t)
		j := e.wait(t, id)
		if j.State != jobs.StateDone || j.Attempts != 2 || len(fake.Messages()) != 1 {
			t.Fatalf("state %s attempts %d messages %d (code %s)", j.State, j.Attempts, len(fake.Messages()), j.ErrorCode)
		}
	})
	t.Run("four busy answers fail", func(t *testing.T) {
		fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"rcpt": 451}})
		e := newMailEnv(t, fake)
		e.configure(t, fakeConfig(fake, TLSStartTLS))
		_, id := e.queue(t)
		e.start(t)
		j := e.wait(t, id)
		if j.State != jobs.StateFailed || j.ErrorCode != "mail_busy" || j.Attempts != 4 || len(fake.Messages()) != 0 {
			t.Fatalf("state %s code %s attempts %d", j.State, j.ErrorCode, j.Attempts)
		}
	})
	t.Run("dropped connection is not sent twice", func(t *testing.T) {
		fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword, DropAfterData: true})
		e := newMailEnv(t, fake)
		e.configure(t, fakeConfig(fake, TLSStartTLS))
		_, id := e.queue(t)
		e.start(t)
		j := e.wait(t, id)
		time.Sleep(150 * time.Millisecond) // a wrongly scheduled retry would show up by now
		if j.State != jobs.StateFailed || j.ErrorCode != "mail_unconfirmed" || j.Attempts != 1 || len(fake.Messages()) != 1 || fake.Sessions() != 1 {
			t.Fatalf("state %s code %s attempts %d messages %d sessions %d", j.State, j.ErrorCode, j.Attempts, len(fake.Messages()), fake.Sessions())
		}
	})
	t.Run("recipient refused is permanent", func(t *testing.T) {
		fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"rcpt": 550}})
		e := newMailEnv(t, fake)
		e.configure(t, fakeConfig(fake, TLSStartTLS))
		_, id := e.queue(t)
		e.start(t)
		if j := e.wait(t, id); j.State != jobs.StateFailed || j.ErrorCode != "mail_recipient" || j.Attempts != 1 {
			t.Fatalf("state %s code %s attempts %d", j.State, j.ErrorCode, j.Attempts)
		}
	})
}

func TestMailInviteContent(t *testing.T) {
	fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e := newMailEnv(t, fake)
	e.configure(t, fakeConfig(fake, TLSStartTLS))
	if err := db.PutSetting(context.Background(), e.f.st.Writer(), settingServerName, "Casa Niño"); err != nil {
		t.Fatal(err)
	}
	e.f.exec(t, `UPDATE users SET display_name = 'José Álvarez' WHERE id = 'u_own'`)
	spec := validSpec()
	spec.Name = "Zoë"
	spec.Note = "Ünï ✓ café & more"
	inv, tok := e.mustCreate(t, "u_own", spec)
	link := "http://tend.example/invite/" + tok
	id, err := e.mail.QueueInvite(context.Background(), e.f.users["u_own"], inv, link)
	if err != nil {
		t.Fatal(err)
	}
	e.start(t)
	if j := e.wait(t, id); j.State != jobs.StateDone {
		t.Fatalf("job %s %s", j.State, j.ErrorCode)
	}
	got := fake.Messages()
	if len(got) != 1 {
		t.Fatalf("messages = %d", len(got))
	}
	m, err := mail.ReadMessage(strings.NewReader(got[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(m.Header.Get("Subject"))
	if err != nil || subj != "José Álvarez invited you to Casa Niño" {
		t.Fatalf("subject = %q, %v", subj, err)
	}
	if raw := m.Header.Get("Subject"); strings.ContainsAny(raw, "éÁñ") {
		t.Errorf("subject header is not encoded: %q", raw)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	for _, want := range []string{"Hi Zoë,", "José Álvarez invited you to listen to audiobooks on Casa Niño.", "Their note: Ünï ✓ café & more", link,
		"The link works once, until José Álvarez stops it.", "If you weren't expecting this, you can ignore this email."} {
		if !strings.Contains(text, want) {
			t.Errorf("body lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "<img") || strings.Count(text, "http") != 1 {
		t.Errorf("body has markup or another address:\n%s", text)
	}
	if ct := m.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; charset=utf-8") || m.Header.Get("To") != "rosa@example.org" {
		t.Errorf("headers: %q, to %q", ct, m.Header.Get("To"))
	}
	if !strings.HasSuffix(m.Header.Get("Message-ID"), "@example.org>") || m.Header.Get("Date") == "" {
		t.Errorf("Message-ID %q Date %q", m.Header.Get("Message-ID"), m.Header.Get("Date"))
	}
	if from := m.Header.Get("From"); !strings.Contains(from, "noreply@example.org") {
		t.Errorf("From = %q", from)
	}
}

func TestMailHeaderInjection(t *testing.T) {
	fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e := newMailEnv(t, fake)
	cfg := fakeConfig(fake, TLSStartTLS)
	for _, c := range []struct {
		name string
		cfg  func(*MailConfig)
		msg  Message
	}{
		{"to", nil, Message{To: "a@example.org\r\nBcc: evil@example.org", Subject: "s", Body: "b"}},
		{"to lf only", nil, Message{To: "a@example.org\nBcc: evil@example.org", Subject: "s", Body: "b"}},
		{"to with a display name", nil, Message{To: "A <a@example.org>", Subject: "s", Body: "b"}},
		{"subject", nil, Message{To: "a@example.org", Subject: "s\r\nBcc: evil@example.org", Body: "b"}},
		{"from name", func(c *MailConfig) { c.FromName = "Home\r\nBcc: evil@example.org" }, Message{To: "a@example.org", Subject: "s", Body: "b"}},
		{"from address", func(c *MailConfig) { c.FromAddress = "a@example.org\r\nBcc: x@example.org" }, Message{To: "a@example.org", Subject: "s", Body: "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cc := cfg
			if c.cfg != nil {
				c.cfg(&cc)
			}
			err := e.mail.send(context.Background(), cc, c.msg)
			if got := codeOf(err, e.mail); got != "mail_header" {
				t.Fatalf("code = %q (err %v)", got, err)
			}
		})
	}
	if fake.Sessions() != 0 || len(fake.Messages()) != 0 {
		t.Fatalf("a refused message reached the server: sessions %d", fake.Sessions())
	}
}

// A line break in a name or the server name, set behind the forms' back, still cannot reach a header.
func TestMailInviteNamesWithLineBreaks(t *testing.T) {
	for _, c := range []struct{ name, sql string }{
		{"invitee", `UPDATE invites SET name = 'Rosa' || char(13) || char(10) || 'Bcc: x@example.org'`},
		{"inviter", `UPDATE users SET display_name = 'Ann' || char(13) || char(10) || 'Bcc: x@example.org' WHERE id = 'u_own'`},
		{"server name", `INSERT INTO settings(key, value_json) VALUES ('server.name', json_quote('Home' || char(13) || char(10) || 'Bcc: x@example.org'))`},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
			e := newMailEnv(t, fake)
			e.configure(t, fakeConfig(fake, TLSStartTLS))
			_, id := e.queue(t)
			e.f.exec(t, c.sql)
			e.start(t)
			j := e.wait(t, id)
			if j.State != jobs.StateFailed || j.ErrorCode != "mail_header" || len(fake.Messages()) != 0 || fake.Sessions() != 0 {
				t.Fatalf("state %s code %s messages %d", j.State, j.ErrorCode, len(fake.Messages()))
			}
		})
	}
}

func TestMailPasswordNeverLeaks(t *testing.T) {
	fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: "different"})
	e := newMailEnv(t, fake)
	var saved MailConfig
	err := e.f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		saved, _, err = SaveMailConfig(context.Background(), tx, tx, e.f.users["u_own"],
			MailInput{Host: fake.Host(), Port: fake.Port(), TLS: TLSStartTLS, Username: "mailer", FromName: "Home", FromAddress: "noreply@example.org"},
			mailTestPassword, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.mail.log().Info("config", "cfg", saved)
	_, id := e.queue(t)
	e.start(t)
	if j := e.wait(t, id); j.ErrorCode != "mail_auth" {
		t.Fatalf("code = %s", j.ErrorCode)
	}
	testID, err := e.mail.QueueTest(context.Background(), e.f.users["u_own"], "me@example.org")
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, testID)
	if strings.Contains(e.logs.String(), mailTestPassword) {
		t.Errorf("the password is in the log:\n%s", e.logs.String())
	}
	for _, q := range []string{
		`SELECT coalesce(group_concat(coalesce(payload_json,'') || coalesce(result_json,'') || coalesce(error_text,'') || coalesce(error_code,'')), '') FROM jobs`,
		`SELECT coalesce(group_concat(detail_json || action || target), '') FROM audit_log`,
	} {
		var dump string
		if err := e.f.st.Reader().QueryRow(q).Scan(&dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump, mailTestPassword) {
			t.Errorf("the password is in %q", q[:40])
		}
		if strings.Contains(dump, "rosa@example.org") || strings.Contains(dump, "me@example.org") {
			t.Errorf("an address is in %q", q[:40])
		}
	}
}

func TestMailLinkLostAfterRestart(t *testing.T) {
	fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e := newMailEnv(t, fake)
	e.configure(t, fakeConfig(fake, TLSStartTLS))
	_, id := e.queue(t)
	e.mail.outbox = newOutbox() // what a restart does to memory
	e.start(t)
	j := e.wait(t, id)
	if j.State != jobs.StateFailed || j.ErrorCode != "mail_link_lost" || len(fake.Messages()) != 0 {
		t.Fatalf("state %s code %s", j.State, j.ErrorCode)
	}
}

func TestMailRevokedBeforeSend(t *testing.T) {
	fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e := newMailEnv(t, fake)
	e.configure(t, fakeConfig(fake, TLSStartTLS))
	inv, id := e.queue(t)
	if err := e.write(func(tx *sql.Tx) error { return e.inv.Revoke(context.Background(), tx, e.f.users["u_own"], inv.ID) }); err != nil {
		t.Fatal(err)
	}
	e.start(t)
	j := e.wait(t, id)
	if j.State != jobs.StateDone || len(fake.Messages()) != 0 || fake.Sessions() != 0 {
		t.Fatalf("state %s messages %d", j.State, len(fake.Messages()))
	}
}

func TestMailNotSetUp(t *testing.T) {
	e := newMailEnv(t, nil)
	_, id := e.queue(t)
	e.start(t)
	if j := e.wait(t, id); j.ErrorCode != "mail_not_set_up" || j.State != jobs.StateFailed {
		t.Fatalf("state %s code %s", j.State, j.ErrorCode)
	}
}

func TestMailInviteStateAndRotate(t *testing.T) {
	fake := smtpfake.Start(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e := newMailEnv(t, fake)
	e.configure(t, fakeConfig(fake, TLSStartTLS))
	inv, id := e.queue(t)
	e.start(t)
	e.wait(t, id)
	got, err := e.inv.Get(context.Background(), inv.ID)
	if err != nil || got.MailJobID != id || got.MailState != "done" {
		t.Fatalf("invite mail state = %+v, %v", got, err)
	}
	if err := e.write(func(tx *sql.Tx) error {
		_, err := e.inv.Rotate(context.Background(), tx, e.f.users["u_own"], inv.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.inv.Get(context.Background(), inv.ID); got.MailJobID != "" || got.MailState != "" {
		t.Fatalf("after Rotate = %+v", got)
	}
}

// A server that sends more than 64 KiB is cut off.
func TestMailLimitConn(t *testing.T) {
	e := newMailEnv(t, nil)
	e.mail.opts.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			_, _ = s.Write([]byte("220 " + strings.Repeat("a", 70<<10) + "\r\n"))
		}()
		return c, nil
	}
	cfg := MailConfig{Host: "mail.example.org", Port: 587, TLS: TLSStartTLS, FromAddress: "a@example.org"}
	err := e.mail.send(context.Background(), cfg, Message{To: "b@example.org", Subject: "s", Body: "b"})
	if err == nil || codeOf(err, e.mail) != "mail_unreachable" {
		t.Fatalf("err = %v", err)
	}
}

// A server that never answers does not hold the send past its deadline.
func TestMailDeadline(t *testing.T) {
	e := newMailEnv(t, nil)
	cfg := MailConfig{Host: "mail.example.org", Port: 587, TLS: TLSStartTLS, FromAddress: "a@example.org"}
	for name, dial := range map[string]func(ctx context.Context, addr string) (net.Conn, error){
		"silent server": func(ctx context.Context, addr string) (net.Conn, error) { c, _ := net.Pipe(); return c, nil },
		"slow dial":     func(ctx context.Context, addr string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() },
	} {
		t.Run(name, func(t *testing.T) {
			e.mail.opts.Dial = dial
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- e.mail.send(ctx, cfg, Message{To: "b@example.org", Subject: "s", Body: "b"}) }()
			select {
			case err := <-done:
				if codeOf(err, e.mail) != "mail_unreachable" {
					t.Fatalf("err = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("send did not stop at its deadline")
			}
		})
	}
}

func TestEncodeSubjectFolds(t *testing.T) {
	long := strings.Repeat("ñandú ", 40)
	enc := encodeSubject(long)
	if !strings.Contains(enc, "\r\n ") {
		t.Fatalf("a long non-ASCII subject is not folded: %q", enc)
	}
	got, err := new(mime.WordDecoder).DecodeHeader(strings.ReplaceAll(enc, "\r\n ", " "))
	if err != nil || strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(long, " ", "") {
		t.Fatalf("round trip = %q, %v", got, err)
	}
}
