package web

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/smtpfake"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The emailed-invite flows of the Share page (P3-8a).

var (
	mailtoRe   = regexp.MustCompile(`href="(mailto:[^"]*)"`)
	mailLinkRe = regexp.MustCompile(`id="share-link"[^>]*value="(http://example\.com/invite/[A-Za-z0-9_-]{43})"`)
)

func mailForm(e *inviteWeb, extra url.Values) url.Values {
	f := sharepageForm(e, url.Values{"via": {"email"}, "email": {"rosa@example.org"}, "how": {"app"}, "note": {"Come & listen? #1"}})
	for k, v := range extra {
		f[k] = v
	}
	return f
}

// mailToParts splits the panel's mailto: address into its address, subject and body.
func mailToParts(t *testing.T, body string) (to, subject, text string) {
	t.Helper()
	m := mailtoRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no mailto: link in the page")
	}
	raw := html.UnescapeString(m[1])
	rest := strings.TrimPrefix(raw, "mailto:")
	addr, query, _ := strings.Cut(rest, "?")
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	to, _ = url.PathUnescape(addr)
	return to, q.Get("subject"), q.Get("body")
}

func TestShareEmailTabs(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{})
	body := e.get(e.owner, "/share").Body.String()
	for _, want := range []string{`href="/share" aria-current="true"`, `href="/share?via=email" aria-current="false"`, "Send a link", "Email it"} {
		if !strings.Contains(body, want) {
			t.Errorf("link tab lacks %q", body[:0]+want)
		}
	}
	if strings.Contains(body, "Email isn't set up on this server yet") || strings.Contains(body, `name="email"`) {
		t.Error("the link tab shows the old blocker or the email field")
	}
	body = e.get(e.owner, "/share?via=email").Body.String()
	for _, want := range []string{`href="/share?via=email" aria-current="true"`, `id="share-email"`, `type="email"`, `name="via" value="email"`,
		"Their email", "Send invitation", `name="how" value="app"`, `To have Tendshelf send it, <a href="/admin/email">set up email</a>.`} {
		if !strings.Contains(body, want) {
			t.Errorf("email tab lacks %q", want)
		}
	}
	if strings.Contains(body, "Send it for me") || strings.Contains(body, "Create invite link") {
		t.Error("email tab offers the relay with nothing set up, or the link button")
	}
	// An admin may invite but does not manage the server: no setup line for them.
	if admin := e.get(e.admin, "/share?via=email").Body.String(); strings.Contains(admin, "set up email") || !strings.Contains(admin, "Send invitation") {
		t.Error("admin view of the email tab")
	}
	// A book's page keeps its path in both tabs.
	book := "/books/" + e.books[0].ID + "/share"
	if b := e.get(e.owner, book+"?via=email").Body.String(); !strings.Contains(b, `href="`+book+`?via=email"`) || !strings.Contains(b, `href="`+book+`"`) {
		t.Error("tab links on a book's page")
	}
}

func TestShareEmailWithOwnEmailApp(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{})
	rec := e.post(e.owner, "/share", mailForm(e.inviteWeb, nil), false)
	usersWant(t, rec, http.StatusOK, "send invitation")
	body := rec.Body.String()
	m := mailLinkRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no link in the panel")
	}
	link := m[1]
	for _, want := range []string{"Invitation for Rosa is ready", "Open in my email app", "data-mailto-auto", "Copy link", "Copy message", `id="share-message"`, "readonly"} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	to, subject, text := mailToParts(t, body)
	if to != "rosa@example.org" || !strings.HasSuffix(subject, " invited you to Tendshelf") {
		t.Errorf("mailto = %q, %q", to, subject)
	}
	for _, want := range []string{"Hi Rosa,", link + "\r\n", "Their note: Come & listen? #1", "The link works once, for 7 more days.", "If you weren't expecting this"} {
		if !strings.Contains(text, want) {
			t.Errorf("mailto body lacks %q:\n%q", want, text)
		}
	}
	// The same text is in the message box.
	if box := html.UnescapeString(body); !strings.Contains(box, "Hi Rosa,\n\nIt works") && !strings.Contains(box, "Hi Rosa,") {
		t.Error("message box")
	}
	if rec.Header().Get("Referrer-Policy") != "same-origin" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("headers")
	}
	// Nothing went through a relay, the address is kept for the row, and the link is shown once.
	if e.count(`SELECT count(*) FROM jobs WHERE kind = 'invite.mail'`) != 0 || len(e.fake.Messages()) != 0 {
		t.Error("a mail job ran with no relay")
	}
	again := e.get(e.owner, "/share").Body.String()
	if strings.Contains(again, link) || !strings.Contains(again, "Link created for rosa@example.org") || strings.Contains(again, "Sent to") {
		t.Error("pending row after the own-app flow")
	}
	if e.count(`SELECT count(*) FROM invites WHERE email = 'rosa@example.org' AND mail_job_id IS NULL`) != 1 {
		t.Error("invite row")
	}
	if e.count(`SELECT count(*) FROM invites WHERE instr(note, ?1) OR instr(name, ?1)`, link[strings.LastIndex(link, "/")+1:]) != 0 {
		t.Error("token stored")
	}
}

func TestShareEmailFormErrors(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{})
	for _, c := range []struct {
		name  string
		extra url.Values
		want  string
	}{
		{"no email", url.Values{"email": {""}}, "Enter their email."},
		{"incomplete email", url.Values{"email": {"rosa@"}}, "That email looks incomplete."},
		{"relay not set up", url.Values{"how": {"relay"}}, "Sending from Tendshelf isn't set up yet."},
	} {
		rec := e.post(e.owner, "/share", mailForm(e.inviteWeb, c.extra), false)
		usersWant(t, rec, http.StatusUnprocessableEntity, c.name)
		if !strings.Contains(html.UnescapeString(rec.Body.String()), c.want) {
			t.Errorf("%s: no %q", c.name, c.want)
		}
		if !strings.Contains(rec.Body.String(), `name="email"`) || !strings.Contains(rec.Body.String(), `aria-current="true"`) {
			t.Errorf("%s: the email tab is not kept", c.name)
		}
		toast := e.post(e.owner, "/share", mailForm(e.inviteWeb, c.extra), true)
		if toast.Code != http.StatusUnprocessableEntity || strings.Contains(toast.Body.String(), "<html") {
			t.Errorf("%s: htmx answer %d", c.name, toast.Code)
		}
	}
	if e.count(`SELECT count(*) FROM invites`) != 0 {
		t.Error("a refused form made an invite")
	}
	// The link tab ignores an email field.
	f := sharepageForm(e.inviteWeb, url.Values{"email": {"rosa@example.org"}})
	usersWant(t, e.post(e.owner, "/share", f, false), http.StatusOK, "link tab")
	if e.count(`SELECT count(*) FROM invites WHERE email IS NOT NULL`) != 0 {
		t.Error("the link tab stored an address")
	}
}

func TestShareEmailRelay(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e.ready()
	body := e.get(e.owner, "/share?via=email").Body.String()
	if !strings.Contains(body, `name="how" value="relay"`) || !strings.Contains(body, "Send it for me") || strings.Contains(body, "set up email") {
		t.Error("Send it for me is not offered once set up and tested")
	}
	e.fake.Messages() // the test email is the first message
	rec := e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"relay"}}), false)
	usersWant(t, rec, http.StatusOK, "send it for me")
	panel := rec.Body.String()
	link := mailLinkRe.FindStringSubmatch(panel)
	if link == nil || !strings.Contains(panel, "Invite on its way to rosa@example.org") || strings.Contains(panel, "Open in my email app") {
		t.Fatalf("relay panel")
	}
	// The job may already have finished by the time the page reads it: either line is right, "Link created" is not.
	if !strings.Contains(panel, "Sending to rosa@example.org") && !strings.Contains(panel, "Sent to rosa@example.org") {
		t.Error("pending row does not follow the queued send")
	}
	if st, code := e.jobEnd("invite.mail"); st != "done" {
		t.Fatalf("invite job %s %s", st, code)
	}
	msgs := e.fake.Messages()
	if len(msgs) != 2 || msgs[1].To != "rosa@example.org" || !strings.Contains(msgs[1].Data, "Subject: ") {
		t.Fatalf("messages = %d", len(msgs))
	}
	data := strings.ReplaceAll(msgs[1].Data, "=\r\n", "")
	if !strings.Contains(data, link[1]) {
		t.Error("the emailed link is not the one in the panel")
	}
	if !strings.Contains(e.get(e.owner, "/share").Body.String(), "Sent to rosa@example.org") {
		t.Error("row after the send")
	}
	var id, jobID string
	if err := e.a.st.Reader().QueryRow(`SELECT id, mail_job_id FROM invites WHERE email = 'rosa@example.org'`).Scan(&id, &jobID); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM jobs WHERE instr(payload_json, ?1) OR instr(coalesce(result_json, ''), ?1) OR instr(coalesce(error_text, ''), ?1)`, "rosa@example.org") != 0 {
		t.Error("the address is in a job row")
	}
	// A new link ("Copy link") kills the emailed one, so "Sent" goes.
	usersWant(t, e.post(e.owner, "/admin/invites/"+id+"/link", url.Values{"back": {"/share"}}, false), http.StatusOK, "copy link")
	after := e.get(e.owner, "/share").Body.String()
	if strings.Contains(after, "Sent to") || !strings.Contains(after, "Link created for rosa@example.org") {
		t.Error("row after Copy link still says Sent")
	}
	// Send it again: a new link, a second message.
	rec = e.post(e.owner, "/admin/invites/"+id+"/email", url.Values{"back": {"/share"}}, false)
	usersWant(t, rec, http.StatusSeeOther, "send again")
	if location(rec) != "/share" {
		t.Errorf("redirect = %q", location(rec))
	}
	if st, _ := e.jobEnd("invite.mail"); st != "done" {
		t.Fatal("resend did not finish")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(e.fake.Messages()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := e.fake.Messages(); len(got) != 3 || strings.Contains(strings.ReplaceAll(got[2].Data, "=\r\n", ""), link[1]) {
		t.Fatalf("resend: %d messages, or it carries the old link", len(got))
	}
	if !strings.Contains(e.get(e.owner, "/share").Body.String(), "Send again") {
		t.Error("no Send again button")
	}
}

func TestShareEmailResendRefusals(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	// An invite with an address, made with the own-app flow.
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, nil), false), http.StatusOK, "create")
	var id string
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM invites`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	path := "/admin/invites/" + id + "/email"
	// Relay not set up: 422, nothing queued.
	usersWant(t, e.post(e.owner, path, url.Values{"back": {"/share"}}, false), http.StatusUnprocessableEntity, "not set up")
	toast := e.post(e.owner, path, url.Values{"back": {"/share"}}, true)
	if toast.Code != http.StatusUnprocessableEntity || strings.Contains(toast.Body.String(), "<html") || !strings.Contains(toast.Body.String(), "set up yet") {
		t.Errorf("htmx refusal: %d %s", toast.Code, toast.Body.String())
	}
	e.ready()
	// Still sending: 409.
	// not_before far ahead, so the running job runner never claims and finishes it mid-test (it did under load).
	e.exec(`INSERT INTO jobs(id, kind, state, created_by, created_at, updated_at, not_before) VALUES ('jb_run', 'invite.mail', 'queued', ?, 1, 1, 9999999999999)`, e.ownerID)
	e.exec(`UPDATE invites SET mail_job_id = 'jb_run'`)
	rec := e.post(e.owner, path, url.Values{"back": {"/share"}}, false)
	usersWant(t, rec, http.StatusConflict, "still sending")
	if !strings.Contains(rec.Body.String(), "It&#39;s still sending.") && !strings.Contains(rec.Body.String(), "It's still sending.") {
		t.Error("409 words")
	}
	if e.count(`SELECT count(*) FROM jobs WHERE kind = 'invite.mail'`) != 1 {
		t.Error("a send was queued while one was running")
	}
	e.exec(`UPDATE invites SET mail_job_id = NULL`)
	// An invite with no address: 422.
	usersWant(t, e.post(e.owner, "/share", sharepageForm(e.inviteWeb, url.Values{"name": {"Ines"}}), false), http.StatusOK, "link invite")
	var noMail string
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM invites WHERE email IS NULL`).Scan(&noMail); err != nil {
		t.Fatal(err)
	}
	usersWant(t, e.post(e.owner, "/admin/invites/"+noMail+"/email", nil, false), http.StatusUnprocessableEntity, "no address")
	// Unknown invite, and the member without rights: 404.
	usersWant(t, e.post(e.owner, "/admin/invites/inv_nope/email", nil, false), http.StatusNotFound, "unknown")
	_, member := e.person(users.RoleMember)
	usersWant(t, e.post(member, path, nil, false), http.StatusNotFound, "member")
	// An admin invite is the owner's alone.
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"role": {"admin"}, "name": {"Boss"}, "email": {"boss@example.org"}}), false), http.StatusOK, "admin invite")
	var adminInv string
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM invites WHERE role = 'admin'`).Scan(&adminInv); err != nil {
		t.Fatal(err)
	}
	usersWant(t, e.post(e.admin, "/admin/invites/"+adminInv+"/email", nil, false), http.StatusNotFound, "admin on an admin invite")
}

func TestShareEmailLimits(t *testing.T) {
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e.ready()
	for i := range 3 {
		usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"relay"}, "name": {"Rosa" + string(rune('A'+i))}}), false), http.StatusOK, "send")
	}
	rec := e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"relay"}}), false)
	usersWant(t, rec, http.StatusTooManyRequests, "fourth to the same address")
	if rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "emailed that address three times today") {
		t.Error("429 words")
	}
	// Another address is fine, and the own-app flow is never limited.
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"relay"}, "email": {"ines@example.org"}}), false), http.StatusOK, "other address")
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, nil), false), http.StatusOK, "own app, same address")
}
