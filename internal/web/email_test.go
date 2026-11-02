package web

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/smtpfake"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// mail tests: the Email settings page and the emailed invites (P3-8a). Helpers start with "mail".

const mailTestPassword = "s3cret-pw-zq"

// mailWeb is an app whose mailer trusts a fake SMTP server, with the job runner started so queued mail
// is really sent.
type mailWeb struct {
	*inviteWeb
	fake *smtpfake.Server
}

func mailNewWeb(t *testing.T, o smtpfake.Options) *mailWeb {
	t.Helper()
	fake := smtpfake.Start(t, o)
	a := newTestApp(t, func(opt *Options) { opt.MailOptions = share.MailOptions{RootCAs: fake.Pool()} })
	e := &usersEnv{t: t, a: a}
	e.owner = a.as(t, users.RoleOwner)
	e.admin = a.as(t, users.RoleAdmin)
	e.ownerID = sweepUserID(t, a, e.owner)
	e.adminID = sweepUserID(t, a, e.admin)
	l, f := librarytest.NewLibrary(t, a.lib, "Family library", adminAudioDir(t, true))
	e.lib = l
	e.books = append(e.books, librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Mailword One", Files: 1, DurationMs: sweepHourMs, AddedAt: 5}))
	if err := a.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.runner.Stop(context.Background()) })
	return &mailWeb{inviteWeb: &inviteWeb{e}, fake: fake}
}

func (e *mailWeb) settings(extra url.Values) url.Values {
	f := url.Values{"host": {e.fake.Host()}, "port": {strconv.Itoa(e.fake.Port())}, "tls": {"starttls"}, "username": {"mailer"},
		"password": {mailTestPassword}, "from_name": {"Home"}, "from_address": {"noreply@example.org"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

// jobState waits for the newest job of a kind to finish and returns its state and code.
func (e *mailWeb) jobEnd(kind string) (state, code string) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var c *string
		err := e.a.st.Reader().QueryRow(`SELECT state, error_code FROM jobs WHERE kind = ? ORDER BY created_at DESC, id DESC LIMIT 1`, kind).Scan(&state, &c)
		if err == nil && (state == "done" || state == "failed") {
			code = ""
			if c != nil {
				code = *c
			}
			return state, code
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("no finished %s job", kind)
	return "", ""
}

// ready saves the settings and sends a test email that works, so "Send it for me" is on.
func (e *mailWeb) ready() {
	e.t.Helper()
	usersWant(e.t, e.post(e.owner, "/admin/email", e.settings(nil), false), http.StatusSeeOther, "save settings")
	usersWant(e.t, e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false), http.StatusOK, "test")
	if st, code := e.jobEnd(share.KindSMTPTest); st != "done" {
		e.t.Fatalf("test email: %s %s", st, code)
	}
}

func TestMailSettingsAccess(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{})
	_, member := e.person(users.RoleMember)
	_, guest := e.person(users.RoleGuest)
	for name, ck := range map[string]*http.Cookie{"admin": e.admin, "member": member, "guest": guest} {
		for _, p := range []string{"/admin/email", "/hx/admin/email/test/x"} {
			usersWant(t, e.get(ck, p), http.StatusNotFound, name+" GET "+p)
		}
		for _, p := range []string{"/admin/email", "/admin/email/test", "/admin/email/off"} {
			usersWant(t, e.post(ck, p, e.settings(nil), false), http.StatusNotFound, name+" POST "+p)
		}
	}
	rec := e.get(e.owner, "/admin/email")
	usersWant(t, rec, http.StatusOK, "owner page")
	for _, want := range []string{"<h1", "Email", "Amazon SES", "Using Amazon SES?", "Email is not set up"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(rec.Body.String(), "Send a test email") {
		t.Error("the test form is offered before anything is saved")
	}
	// no CSRF token, no change
	rec = e.a.do(http.MethodPost, "/admin/email", e.settings(nil), nil, e.owner)
	usersWant(t, rec, http.StatusForbidden, "no token")
}

func TestMailSettingsPasswordNeverShown(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{})
	rec := e.post(e.owner, "/admin/email", e.settings(nil), false)
	usersWant(t, rec, http.StatusSeeOther, "save")
	if location(rec) != "/admin/email?saved=1" || strings.Contains(rec.Body.String(), mailTestPassword) {
		t.Errorf("save: %q", location(rec))
	}
	page := e.get(e.owner, location(rec)).Body.String()
	if strings.Contains(page, mailTestPassword) || !strings.Contains(page, "A password is saved. Leave this empty to keep it.") || !strings.Contains(page, "Email settings saved.") {
		t.Error("saved page")
	}
	// A refused form comes back with what was typed, except the password.
	bad := e.post(e.owner, "/admin/email", e.settings(url.Values{"host": {"https://x.example"}}), false)
	usersWant(t, bad, http.StatusUnprocessableEntity, "bad host")
	if strings.Contains(bad.Body.String(), mailTestPassword) || !strings.Contains(bad.Body.String(), "Enter the server name, like email-smtp.eu-west-1.amazonaws.com.") {
		t.Error("422 page")
	}
	for _, c := range []struct {
		field, value, msg string
	}{
		{"port", "99999", "Enter a port from 1 to 65535."},
		{"from_address", "nobody@", "That email looks incomplete."},
		{"tls", "none", "Tendshelf only sends without encryption to a server on this computer."},
		{"username", "a\nb", "Remove line breaks and keep it shorter."},
	} {
		f := e.settings(url.Values{c.field: {c.value}})
		if c.field == "tls" {
			f.Set("host", "mail.example.org")
		}
		rec := e.post(e.owner, "/admin/email", f, false)
		usersWant(t, rec, http.StatusUnprocessableEntity, c.field)
		if !strings.Contains(html.UnescapeString(rec.Body.String()), c.msg) || strings.Contains(rec.Body.String(), mailTestPassword) {
			t.Errorf("%s: message or password", c.field)
		}
	}
	// A new host without a new password forgets the stored one.
	rec = e.post(e.owner, "/admin/email", e.settings(url.Values{"host": {"localhost"}, "password": {""}}), false)
	usersWant(t, rec, http.StatusSeeOther, "host change")
	if location(rec) != "/admin/email?saved=1&dropped=1" {
		t.Errorf("redirect = %q", location(rec))
	}
	if e.count(`SELECT count(*) FROM settings WHERE key = 'smtp.password' AND value_json <> '""'`) != 0 {
		t.Error("the stored password survived a host change")
	}
	if !strings.Contains(e.get(e.owner, location(rec)).Body.String(), "The server or username changed, so enter the password again.") {
		t.Error("no notice about the dropped password")
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE instr(detail_json, ?1) OR instr(target, ?1)`, mailTestPassword) != 0 {
		t.Error("the password is in the audit log")
	}
}

func TestMailSettingsPresets(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{})
	for _, c := range []struct{ query, want string }{
		{"preset=ses&region=eu-west-1", `value="email-smtp.eu-west-1.amazonaws.com"`},
		{"preset=gmail", `value="smtp.gmail.com"`},
		{"preset=fastmail", `value="465"`},
		{"preset=sendgrid", `value="apikey"`},
		{"preset=own", `value="587"`},
	} {
		body := e.get(e.owner, "/admin/email?"+c.query).Body.String()
		if !strings.Contains(body, c.want) {
			t.Errorf("%s lacks %s", c.query, c.want)
		}
	}
	// An unknown region never reaches the host field.
	if body := e.get(e.owner, "/admin/email?preset=ses&region=evil.example/x").Body.String(); strings.Contains(body, "evil.example") {
		t.Error("an unknown region was echoed")
	}
}

func TestMailSettingsTestEmail(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	// Not saved yet: refused with words.
	usersWant(t, e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false), http.StatusUnprocessableEntity, "test before save")
	usersWant(t, e.post(e.owner, "/admin/email", e.settings(nil), false), http.StatusSeeOther, "save")
	page := e.get(e.owner, "/admin/email").Body.String()
	if !strings.Contains(page, "Send a test email") || !strings.Contains(page, "not tested yet") {
		t.Error("saved page offers the test")
	}
	usersWant(t, e.post(e.owner, "/admin/email/test", url.Values{"to": {"not an address"}}, false), http.StatusUnprocessableEntity, "bad address")
	rec := e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false)
	usersWant(t, rec, http.StatusOK, "test")
	body := html.UnescapeString(rec.Body.String())
	if !strings.Contains(body, "Sending a test email to me@example.org…") || !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "Check the result") {
		t.Errorf("test panel: %s", body)
	}
	if st, code := e.jobEnd(share.KindSMTPTest); st != "done" {
		t.Fatalf("job %s %s", st, code)
	}
	msgs := e.fake.Messages()
	if len(msgs) != 1 || msgs[0].To != "me@example.org" || msgs[0].AuthUser != "mailer" || !msgs[0].TLS {
		t.Fatalf("messages = %+v", msgs)
	}
	var id string
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'smtp.test'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	frag := e.get(e.owner, "/hx/admin/email/test/"+id+"?to=me%40example.org")
	usersWant(t, frag, http.StatusOK, "fragment")
	if !strings.Contains(frag.Body.String(), "Sent. Check the inbox of me@example.org.") || strings.Contains(frag.Body.String(), "every 2s") || strings.Contains(frag.Body.String(), "<html") {
		t.Errorf("fragment: %s", frag.Body.String())
	}
	if !strings.Contains(e.get(e.owner, "/admin/email").Body.String(), "set up and tested") {
		t.Error("status after a good test")
	}
	// The job is the owner's: someone else's job, or another kind, is a 404.
	e.exec(`INSERT INTO jobs(id, kind, state, created_by, created_at, updated_at) VALUES ('jb_other', 'smtp.test', 'done', ?, 1, 1)`, e.adminID)
	usersWant(t, e.get(e.owner, "/hx/admin/email/test/jb_other"), http.StatusNotFound, "another person's test")
	e.exec(`INSERT INTO jobs(id, kind, state, created_by, created_at, updated_at) VALUES ('jb_scan', 'scan', 'done', ?, 1, 1)`, e.ownerID)
	usersWant(t, e.get(e.owner, "/hx/admin/email/test/jb_scan"), http.StatusNotFound, "another kind")
	usersWant(t, e.get(e.owner, "/hx/admin/email/test/nope"), http.StatusNotFound, "unknown")
	// Turning it off forgets the settings.
	usersWant(t, e.post(e.owner, "/admin/email/off", nil, false), http.StatusSeeOther, "off")
	if e.count(`SELECT count(*) FROM settings WHERE key LIKE 'smtp.%'`) != 0 {
		t.Error("settings left after Turn off email")
	}
}

func TestMailSettingsTestFailureLines(t *testing.T) {
	for _, c := range []struct {
		name string
		opts smtpfake.Options
		code string
		want string
	}{
		{"wrong password", smtpfake.Options{User: "mailer", Pass: "other"}, "mail_auth", "The email server didn't accept the username or password. With SES, use the SMTP credentials, not your AWS sign-in."},
		{"sender refused", smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"mail": 554}}, "mail_sender", "The email server won't send from noreply@example.org. With Amazon SES, verify that address first."},
		{"recipient refused", smtpfake.Options{User: "mailer", Pass: mailTestPassword, Fail: map[string]int{"rcpt": 550}}, "mail_recipient", "The email server refused me@example.org. In the SES sandbox, it only sends to verified addresses."},
		{"no starttls", smtpfake.Options{NoStartTLS: true, User: "mailer", Pass: mailTestPassword}, "mail_no_tls", "That server doesn't offer a secure connection, so Tendshelf won't send through it."},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := mailNewWeb(t, c.opts)
			usersWant(t, e.post(e.owner, "/admin/email", e.settings(nil), false), http.StatusSeeOther, "save")
			usersWant(t, e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false), http.StatusOK, "test")
			if st, code := e.jobEnd(share.KindSMTPTest); st != "failed" || code != c.code {
				t.Fatalf("job %s %s", st, code)
			}
			var id string
			if err := e.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'smtp.test'`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			body := html.UnescapeString(e.get(e.owner, "/hx/admin/email/test/"+id+"?to=me%40example.org").Body.String())
			if !strings.Contains(body, c.want) {
				t.Errorf("fragment lacks %q:\n%s", c.want, body)
			}
			if strings.Contains(e.get(e.owner, "/admin/email").Body.String(), "set up and tested") {
				t.Error("a failed test turned the relay on")
			}
			if strings.Contains(e.get(e.owner, "/share?via=email").Body.String(), "Send it for me") {
				t.Error("Send it for me is offered after a failed test")
			}
		})
	}
}

func TestMailSettingsTestIsRateLimited(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	usersWant(t, e.post(e.owner, "/admin/email", e.settings(nil), false), http.StatusSeeOther, "save")
	for i := range 5 {
		usersWant(t, e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false), http.StatusOK, "test "+strconv.Itoa(i))
	}
	rec := e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false)
	usersWant(t, rec, http.StatusTooManyRequests, "sixth test")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}
