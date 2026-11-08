package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var feedAddrRE = regexp.MustCompile(`https?://[^"\s<]+/feeds/([A-Za-z0-9_-]+)/feed\.xml`)

// feedForm posts a form as a lab person.
func (l *feedLab) post(t *testing.T, who, path string, form url.Values) (int, string, http.Header) {
	t.Helper()
	ck := l.cookies[who]
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", l.a.tokenFor(t, ck))
	rec := l.a.do(http.MethodPost, path, form, proxyDefault, ck)
	return rec.Code, rec.Body.String(), rec.Header()
}

func (l *feedLab) page(who, path string) (int, string) {
	rec := l.a.do(http.MethodGet, path, nil, proxyDefault, l.cookies[who])
	return rec.Code, rec.Body.String()
}

// TestFeedMakeAndUse: the POST shows the address once, with a QR code; a later GET does not; the database holds only
// the hash; the audit row names the feed and target and never the address; the address works.
func TestFeedMakeAndUse(t *testing.T) {
	l := newFeedLab(t)
	path := "/books/" + l.single.ID + "/feed"
	code, body := l.page("m_dl", path)
	if code != http.StatusOK || !strings.Contains(body, "Make a feed address") || !strings.Contains(body, "Anyone with the address") {
		t.Fatalf("GET = %d, the make page is missing words", code)
	}
	code, body, h := l.post(t, "m_dl", path, url.Values{"name": {"Pocket Casts"}})
	if code != http.StatusOK || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("POST = %d, Cache-Control %q", code, h.Get("Cache-Control"))
	}
	m := feedAddrRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no address in the answer:\n%s", body)
	}
	tok := m[1]
	if !strings.HasPrefix(m[0], "https://podcasts.example/feeds/") {
		t.Errorf("address = %q", m[0])
	}
	for _, want := range []string{"Your feed address", "Copy it now", "readonly", "data-copy", "<svg", "Back to the book"} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer lacks %q", want)
		}
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds WHERE name = 'Pocket Casts' AND book_id = ? AND user_id = ?`, l.single.ID, l.ids["m_dl"]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("feed rows = %d, %v", n, err)
	}
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds WHERE token_hash IS NOT NULL AND CAST(token_hash AS TEXT) LIKE '%'||?||'%'`, tok).Scan(&n); err != nil || n != 0 {
		t.Errorf("the raw token is stored: %d, %v", n, err)
	}
	var detail, target string
	if err := l.a.st.Reader().QueryRow(`SELECT detail_json, target FROM audit_log WHERE action = 'feed.create'`).Scan(&detail, &target); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail, tok) || target != l.ids["m_dl"] || !strings.Contains(detail, `"target":"book:`+l.single.ID+`"`) || !strings.Contains(detail, `"feed":"`) {
		t.Errorf("audit target %q detail %s", target, detail)
	}
	for _, p := range []string{path, "/me/devices"} {
		if _, again := l.page("m_dl", p); strings.Contains(again, tok) {
			t.Errorf("GET %s shows the address again", p)
		}
	}
	if rec := l.get("/feeds/" + tok + "/feed.xml"); rec.Code != http.StatusOK {
		t.Errorf("the new address = %d", rec.Code)
	}
}

// TestFeedMakeForbidden: who may not download a book gets a plain 404 on the page and the POST, and no row.
func TestFeedMakeForbidden(t *testing.T) {
	l := newFeedLab(t)
	for _, who := range []string{"m_nodl"} {
		for _, p := range []string{"/books/" + l.single.ID + "/feed", "/series/" + l.seriesID + "/feed"} {
			if code, _ := l.page(who, p); code != http.StatusNotFound {
				t.Errorf("%s GET %s = %d, want 404", who, p, code)
			}
			if code, _, _ := l.post(t, who, p, nil); code != http.StatusNotFound {
				t.Errorf("%s POST %s = %d, want 404", who, p, code)
			}
		}
	}
	// A person with a grant on another library only: lib2 holds Saga Three, so the book of lib1 is hidden.
	if code, _ := l.page("m_part", "/books/"+l.sb.ID+"/feed"); code != http.StatusNotFound {
		t.Errorf("a book of another library = %d, want 404", code)
	}
	// Not an audio book, a protected file, refused, unknown.
	for name, id := range map[string]string{"text-only": l.tx.ID, "protected": l.protected.ID, "refused": l.refused.ID, "unknown": "bk_none"} {
		if code, _ := l.page("owner", "/books/"+id+"/feed"); code != http.StatusNotFound {
			t.Errorf("%s book GET = %d, want 404", name, code)
		}
		if code, _, _ := l.post(t, "owner", "/books/"+id+"/feed", nil); code != http.StatusNotFound {
			t.Errorf("%s book POST = %d, want 404", name, code)
		}
	}
	if code, _ := l.page("owner", "/series/ser_none/feed"); code != http.StatusNotFound {
		t.Errorf("unknown series = %d, want 404", code)
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds`).Scan(&n); err != nil || n != 0 {
		t.Errorf("feeds made by refused requests: %d, %v", n, err)
	}
	// A guest with the download right may make one; a series feed shows for a person who may download part of it.
	if code, _, _ := l.post(t, "g_dl", "/books/"+l.single.ID+"/feed", nil); code != http.StatusOK {
		t.Errorf("guest with download POST = %d, want 200", code)
	}
	if code, _, _ := l.post(t, "m_part", "/series/"+l.seriesID+"/feed", nil); code != http.StatusOK {
		t.Errorf("series POST = %d, want 200", code)
	}
}

// TestFeedMakeContributor: a contributor may make a feed for a book their own upload placed, and not for another.
func TestFeedMakeContributor(t *testing.T) {
	l := newFeedLab(t)
	l.exec(t, `UPDATE users SET adds_books = 1 WHERE id = ?`, l.ids["m_nodl"])
	l.exec(t, `DELETE FROM grants WHERE user_id = ?`, l.ids["m_nodl"])
	l.exec(t, `INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, 'up1', ?, ?, 1)`, l.single.RootPath, l.lib1.ID, l.ids["m_nodl"])
	if code, _, _ := l.post(t, "m_nodl", "/books/"+l.single.ID+"/feed", nil); code != http.StatusOK {
		t.Errorf("contributor on their own upload = %d, want 200", code)
	}
	if code, _, _ := l.post(t, "m_nodl", "/books/"+l.multi.ID+"/feed", nil); code != http.StatusNotFound {
		t.Errorf("contributor on another book = %d, want 404", code)
	}
}

// TestFeedMakeRefusals: no CSRF token is 403, the 51st live feed is 409 (a toast for htmx), and nothing is made.
func TestFeedMakeRefusals(t *testing.T) {
	l := newFeedLab(t)
	path := "/books/" + l.single.ID + "/feed"
	rec := l.a.do(http.MethodPost, path, url.Values{"name": {"x"}}, proxyDefault, l.cookies["m_dl"])
	if rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token = %d, want 403", rec.Code)
	}
	for i := 0; i < 50; i++ {
		l.mk(t, "m_dl", l.single.ID, "")
	}
	code, body, _ := l.post(t, "m_dl", path, nil)
	if code != http.StatusConflict || !strings.Contains(body, "as many feed addresses") {
		t.Errorf("51st feed = %d %q", code, body)
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds WHERE user_id = ?`, l.ids["m_dl"]).Scan(&n); err != nil || n != 50 {
		t.Errorf("feeds = %d, %v", n, err)
	}
}

// TestFeedRevoke: the owner turns off their feed (303, audited, the feed then 404); another person cannot, and the
// feed keeps working; an unknown id is 404; no CSRF token is 403.
func TestFeedRevoke(t *testing.T) {
	l := newFeedLab(t)
	tok, f := l.mk(t, "m_dl", l.single.ID, "")
	path := "/me/feeds/" + f.ID + "/revoke"
	if code, _, _ := l.post(t, "m_part", path, nil); code != http.StatusNotFound {
		t.Errorf("another person's feed = %d, want 404", code)
	}
	if rec := l.get("/feeds/" + tok + "/feed.xml"); rec.Code != http.StatusOK {
		t.Fatalf("the feed after a foreign revoke = %d", rec.Code)
	}
	if rec := l.a.do(http.MethodPost, path, url.Values{}, proxyDefault, l.cookies["m_dl"]); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token = %d, want 403", rec.Code)
	}
	if code, _, _ := l.post(t, "m_dl", "/me/feeds/zz-none/revoke", nil); code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", code)
	}
	code, _, h := l.post(t, "m_dl", path, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/me/devices" {
		t.Fatalf("revoke = %d %q", code, h.Get("Location"))
	}
	if rec := l.get("/feeds/" + tok + "/feed.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("the feed after revoke = %d, want 404", rec.Code)
	}
	var detail string
	if err := l.a.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'feed.revoke' AND target = ?`, l.ids["m_dl"]).Scan(&detail); err != nil || !strings.Contains(detail, f.ID) || strings.Contains(detail, tok) {
		t.Errorf("audit detail %q, %v", detail, err)
	}
	if code, _, _ := l.post(t, "m_dl", path, nil); code != http.StatusNotFound {
		t.Errorf("a second revoke = %d, want 404", code)
	}
}

// TestFeedDevicesList: Your devices lists each live feed with its target name, kind, app name and last use, and a gone
// target as the plain sentence.
func TestFeedDevicesList(t *testing.T) {
	l := newFeedLab(t)
	_, none := l.mk(t, "m_dl", l.single.ID, "")
	tok, _ := l.mk(t, "m_dl", "", l.seriesID)
	l.exec(t, `UPDATE podcast_feeds SET name = 'Overcast' WHERE id <> ?`, none.ID)
	_, gone := l.mk(t, "m_dl", l.multi.ID, "")
	l.exec(t, `DELETE FROM books WHERE id = ?`, l.multi.ID)
	if rec := l.get("/feeds/" + tok + "/feed.xml"); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	_, body := l.page("m_dl", "/me/devices")
	for _, want := range []string{"Podcast feeds", "Single Tale", "Saga", "Overcast", "Series", "Book", "Not used yet", "just now", "A book or series that is no longer here", "Turn off Single Tale", "/me/feeds/" + gone.ID + "/revoke"} {
		if !strings.Contains(body, want) {
			t.Errorf("Your devices lacks %q", want)
		}
	}
	if _, other := l.page("m_part", "/me/devices"); strings.Contains(other, "Single Tale") || !strings.Contains(other, "No feeds yet.") {
		t.Error("another person sees the feeds, or lacks the empty line")
	}
}

// TestFeedActionOnPages: the book and series pages offer "Podcast feed" only to who may download.
func TestFeedActionOnPages(t *testing.T) {
	l := newFeedLab(t)
	for _, c := range []struct {
		who, path string
		want      bool
	}{
		{"m_dl", "/books/" + l.single.ID, true},
		{"owner", "/books/" + l.single.ID, true},
		{"m_nodl", "/books/" + l.single.ID, false},
		{"m_dl", "/series/" + l.seriesID, true},
		{"m_nodl", "/series/" + l.seriesID, false},
	} {
		code, body := l.page(c.who, c.path)
		if code != http.StatusOK {
			t.Errorf("%s GET %s = %d", c.who, c.path, code)
			continue
		}
		if got := strings.Contains(body, "Podcast feed"); got != c.want {
			t.Errorf("%s on %s: action shown = %v, want %v", c.who, c.path, got, c.want)
		}
	}
}

// TestFeedEndedWithPerson: disabling a person turns off their feeds in the database, not only at the request.
func TestFeedEndedWithPerson(t *testing.T) {
	l := newFeedLab(t)
	tok, _ := l.mk(t, "m_dl", l.single.ID, "")
	if code, _, _ := l.post(t, "owner", "/admin/users/"+l.ids["m_dl"]+"/disable", nil); code != http.StatusSeeOther && code != http.StatusOK {
		t.Fatalf("disable = %d", code)
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds WHERE user_id = ? AND revoked_at IS NULL`, l.ids["m_dl"]).Scan(&n); err != nil || n != 0 {
		t.Errorf("live feeds after sign-out = %d, %v", n, err)
	}
	if rec := l.get("/feeds/" + tok + "/feed.xml"); rec.Code != http.StatusNotFound {
		t.Errorf("feed = %d, want 404", rec.Code)
	}
}
