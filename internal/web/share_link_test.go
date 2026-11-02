package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

var linkURLRe = regexp.MustCompile(`value="https?://[^"/]+(/s/[A-Za-z0-9_-]{43})"`)

// linkPost sends a share-link form as the person with the cookie.
func (e *linkEnv) post(path string, form url.Values, ck *http.Cookie) *httptest.ResponseRecorder {
	form.Set("_csrf", e.a.tokenFor(e.t, ck))
	return e.a.do(http.MethodPost, path, form, nil, ck)
}

func TestShareLinkCreate(t *testing.T) {
	e := linkNewEnv(t)
	path := "/books/" + e.book.ID + "/share/link"
	rec := e.post(path, url.Values{"link_label": {"Tia Marta"}, "link_days": {"30"}}, e.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d", rec.Code)
	}
	linkAssertHeaders(t, rec, "no-store")
	body := rec.Body.String()
	m := linkURLRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no link in the answer")
	}
	for _, want := range []string{"Your link is ready", "Linkword Lantern", "it won&#39;t be shown again", `id="share-link"`} {
		if !strings.Contains(body, want) && !strings.Contains(body, strings.ReplaceAll(want, "&#39;", "'")) {
			t.Errorf("the panel lacks %q", want)
		}
	}
	if rec := e.get(m[1]); rec.Code != http.StatusOK {
		t.Errorf("the new link: %d", rec.Code)
	}
	// Shown once: the page again lists the link, with Stop, but not its address.
	again := e.get("/books/"+e.book.ID+"/share", e.owner).Body.String()
	if strings.Contains(again, m[1][3:]) || !strings.Contains(again, "Tia Marta") || !strings.Contains(again, "Stop the link for Tia Marta") {
		t.Errorf("the page after: token shown or link not listed")
	}
	// On /share the list names the book, and the form is replaced by a hint.
	all := e.get("/share", e.owner).Body.String()
	if !strings.Contains(all, "Linkword Lantern · Tia Marta") || !strings.Contains(all, "To make a link, open a book and choose Share.") || strings.Contains(all, `name="link_days"`) {
		t.Errorf("/share: list or hint wrong")
	}
	// Not for members, guests or the unknown.
	for name, ck := range map[string]*http.Cookie{"member": e.a.as(t, users.RoleMember), "guest": e.a.as(t, users.RoleGuest)} {
		if rec := e.post(path, url.Values{"link_days": {"7"}}, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := e.post("/books/nope/share/link", url.Values{"link_days": {"7"}}, e.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown book: %d", rec.Code)
	}
	if n := e.a.count(t, `SELECT count(*) FROM share_links`); n != 1 {
		t.Errorf("%d links, want 1", n)
	}
}

func TestShareLinkCreateRefusals(t *testing.T) {
	e := linkNewEnv(t)
	path := "/books/" + e.book.ID + "/share/link"
	for name, c := range map[string]struct {
		form url.Values
		want string
	}{
		"days 0":          {url.Values{"link_days": {"0"}}, "Choose how long the link works."},
		"days 91":         {url.Values{"link_days": {"91"}}, "Choose how long the link works."},
		"days word":       {url.Values{"link_days": {"soon"}}, "Choose how long the link works."},
		"short password":  {url.Values{"link_days": {"7"}, "link_password": {"1234567"}}, "Use at least 8 characters for the password."},
		"label too long":  {url.Values{"link_days": {"7"}, "link_label": {strings.Repeat("é", 81)}}, "Keep the name under 80 characters."},
		"password length": {url.Values{"link_days": {"7"}, "link_password": {strings.Repeat("a", 1025)}}, "Use a shorter password."},
	} {
		rec := e.post(path, c.form, e.owner)
		body := rec.Body.String()
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if pw := c.form.Get("link_password"); pw != "" && strings.Contains(body, pw) {
			t.Errorf("%s: the password came back", name)
		}
	}
	// A refused book is a form error for staff, not a link.
	streamExec(t, e.a, `UPDATE books SET refused_reason = 'drm' WHERE id = ?`, e.book.ID)
	rec := e.post(path, url.Values{"link_days": {"7"}}, e.owner)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "can&#39;t be shared with a link") && !strings.Contains(rec.Body.String(), "can't be shared with a link") {
		t.Errorf("refused book: %d", rec.Code)
	}
	if n := e.a.count(t, `SELECT count(*) FROM share_links`); n != 0 {
		t.Errorf("%d links written by refused forms", n)
	}
}

func TestShareLinkWithPassword(t *testing.T) {
	e := linkNewEnv(t)
	rec := e.post("/books/"+e.book.ID+"/share/link", url.Values{"link_days": {"1"}, "link_password": {"long enough pass"}}, e.owner)
	m := linkURLRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		t.Fatalf("create: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "long enough pass") {
		t.Errorf("the password came back")
	}
	if page := e.get(m[1]); page.Code != http.StatusOK || strings.Contains(page.Body.String(), "Linkword") {
		t.Errorf("the link is not locked: %d", page.Code)
	}
	if list := e.get("/share", e.owner).Body.String(); !strings.Contains(list, "· Password") {
		t.Errorf("the list does not say the link has a password")
	}
}

func TestShareLinkStop(t *testing.T) {
	e := linkNewEnv(t)
	l, tok := e.make(e.book.ID, 7, "")
	stop := "/share/links/" + l.ID + "/stop"
	if rec := e.post(stop, url.Values{}, e.a.as(t, users.RoleMember)); rec.Code != http.StatusNotFound {
		t.Errorf("member stops: %d", rec.Code)
	}
	if rec := e.get("/s/" + tok); rec.Code != http.StatusOK {
		t.Fatalf("link after a refused stop: %d", rec.Code)
	}
	if rec := e.post("/share/links/nope/stop", url.Values{}, e.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown link: %d", rec.Code)
	}
	rec := e.post(stop, url.Values{"back": {"https://evil.example/"}}, e.owner)
	if rec.Code != http.StatusSeeOther || location(rec) != "/share" {
		t.Errorf("stop: %d %q", rec.Code, location(rec))
	}
	if rec := e.get("/s/" + tok); rec.Code != http.StatusNotFound {
		t.Errorf("link after stop: %d", rec.Code)
	}
	// Stopping twice is fine and goes back where the form said.
	rec = e.post(stop, url.Values{"back": {"/books/" + e.book.ID + "/share"}}, e.owner)
	if rec.Code != http.StatusSeeOther || location(rec) != "/books/"+e.book.ID+"/share" {
		t.Errorf("second stop: %d %q", rec.Code, location(rec))
	}
	if n := e.a.count(t, `SELECT count(*) FROM audit_log WHERE action = 'link.revoke'`); n != 1 {
		t.Errorf("%d revoke audit rows", n)
	}
	if list := e.get("/share", e.owner).Body.String(); strings.Contains(list, "Stop the link") {
		t.Errorf("a stopped link is still listed")
	}
}
