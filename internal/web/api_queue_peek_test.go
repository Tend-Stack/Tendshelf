package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// L8 (decision 0035): the read-only peek at the next book, GET /api/v1/queue/next.

func (e *syncEnv) l8Peek(t *testing.T, tok, ended string) (int, map[string]any) {
	t.Helper()
	path := "/api/v1/queue/next"
	if ended != "" {
		path += "?ended=" + ended
	}
	rec := e.send(tok, http.MethodGet, path, "")
	var out map[string]any
	if rec.Code == http.StatusOK {
		apiDecode(t, rec, &out)
	}
	return rec.Code, out
}

func l8Book(out map[string]any) string {
	b, _ := out["book"].(map[string]any)
	s, _ := b["id"].(string)
	return s
}

func TestL8PeekQueue(t *testing.T) {
	e := syncSeed(t)
	if code, _ := e.l8Peek(t, e.owner, ""); code != http.StatusNoContent {
		t.Errorf("empty queue = %d", code)
	}
	if rec := e.send(e.owner, http.MethodGet, "/api/v1/queue/next", ""); rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	for _, id := range e.extra {
		e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(id, ""))
	}
	before := e.send(e.owner, http.MethodGet, "/api/v1/queue", "").Body.String()
	code, out := e.l8Peek(t, e.owner, "")
	if code != http.StatusOK || out["source"] != "queue" || l8Book(out) != e.extra[0] {
		t.Fatalf("head = %d %v", code, out)
	}
	// It skips the book that just ended, and pops nothing.
	if code, out := e.l8Peek(t, e.owner, e.extra[0]); code != http.StatusOK || l8Book(out) == e.extra[0] {
		t.Errorf("ended skipped = %d %v", code, out)
	}
	for range 3 {
		e.l8Peek(t, e.owner, "")
	}
	if after := e.send(e.owner, http.MethodGet, "/api/v1/queue", "").Body.String(); after != before {
		t.Errorf("the queue changed:\n%s\n%s", before, after)
	}
	rec := e.send(e.owner, http.MethodGet, "/api/v1/queue/next?ended="+strings.Repeat("x", 65), "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"ended"`) {
		t.Errorf("long ended = %d %s", rec.Code, rec.Body)
	}
}

func TestL8PeekSkipsKeeps(t *testing.T) {
	e := syncSeed(t)
	// The member holds a row for a book they cannot open, then a book they can.
	if _, err := e.a.st.Writer().Exec(`INSERT INTO queue_items(user_id, book_id, position, added_at) VALUES (?, ?, -5, 1)`,
		e.uid[sweepMember], e.hidden.ID); err != nil {
		t.Fatal(err)
	}
	e.send(e.member, http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], ""))
	code, out := e.l8Peek(t, e.member, "")
	if code != http.StatusOK || l8Book(out) != e.extra[0] || out["source"] != "queue" {
		t.Fatalf("member peek = %d %v", code, out)
	}
	if strings.Contains(e.send(e.member, http.MethodGet, "/api/v1/queue/next", "").Body.String(), e.hidden.ID) {
		t.Error("the hidden book showed")
	}
	// A text head is skipped and kept.
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET kind = 'text' WHERE id = ?`, e.extra[0]); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.l8Peek(t, e.member, ""); code != http.StatusNoContent {
		t.Errorf("text head = %d", code)
	}
	var n int
	if err := e.a.st.Reader().QueryRow(`SELECT count(*) FROM queue_items WHERE user_id = ?`, e.uid[sweepMember]).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows kept = %d, %v", n, err)
	}
	if code, _ := e.l8Peek(t, "", ""); code != http.StatusUnauthorized {
		t.Errorf("no token = %d", code)
	}
}

func TestL8PeekSeries(t *testing.T) {
	e := syncSeed(t)
	first, hidden, last := e.a1Series(t)
	code, out := e.l8Peek(t, e.owner, first.ID)
	if code != http.StatusOK || out["source"] != "series" || l8Book(out) != hidden.ID {
		t.Errorf("owner = %d %v", code, out)
	}
	// The member never learns of the closed book.
	if code, out := e.l8Peek(t, e.member, first.ID); code != http.StatusOK || l8Book(out) != last.ID {
		t.Errorf("member = %d %v", code, out)
	}
	if code, _ := e.l8Peek(t, e.owner, last.ID); code != http.StatusNoContent {
		t.Errorf("last in series = %d", code)
	}
	// A finished next book is not offered.
	now := int64(1_800_000_000_000)
	if _, err := e.a.st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
VALUES (?,?,?,?,?,?,?,?,?)`, e.uid[sweepOwner], hidden.ID, 1000, 1000, now, now, now, "devicealpha1", "x"); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.l8Peek(t, e.owner, first.ID); code != http.StatusNoContent {
		t.Errorf("finished next = %d", code)
	}
	// The series setting off is a 204.
	if rec := e.send(e.member, http.MethodPatch, "/api/v1/settings", `{"nextInSeries":false}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if code, _ := e.l8Peek(t, e.member, first.ID); code != http.StatusNoContent {
		t.Errorf("setting off = %d", code)
	}
}

func TestL8AutoDownloadSetting(t *testing.T) {
	e := syncSeed(t)
	on := func() bool {
		var m struct {
			AutoDownload bool `json:"autoDownload"`
		}
		apiDecode(t, e.send(e.owner, http.MethodGet, "/api/v1/settings", ""), &m)
		return m.AutoDownload
	}
	if on() {
		t.Fatal("autoDownload on by default")
	}
	if rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"autoDownload":true}`); rec.Code != http.StatusOK || !on() {
		t.Fatalf("patch true = %d", rec.Code)
	}
	rec := e.send(e.owner, http.MethodPatch, "/api/v1/settings", `{"autoDownload":"on"}`)
	if rec.Code != http.StatusBadRequest || a1Field(t, rec) != "autoDownload" {
		t.Errorf("patch \"on\" = %d %s", rec.Code, rec.Body)
	}
	if !on() {
		t.Error("a bad patch changed it")
	}
}

func TestL8SettingsSwitch(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	page := func(extra ...*http.Cookie) string {
		return a.do(http.MethodGet, "/me/settings/playback", nil, nil, append([]*http.Cookie{ck}, extra...)...).Body.String()
	}
	if b := page(a1Android); !strings.Contains(b, `name="auto_download"`) || !strings.Contains(b, "Download the next book on its own") {
		t.Error("the app's playback settings lack the switch")
	}
	for name, extra := range map[string][]*http.Cookie{"browser": nil, "desktop": {{Name: shellCookie, Value: "desktop"}}} {
		if b := page(extra...); strings.Contains(b, `name="auto_download"`) {
			t.Errorf("%s shows the switch", name)
		}
	}
	form := url.Values{"theme": {"system"}, "theme_auto": {"1"}, "speed": {"1"}, "skip_back": {"15"}, "skip_forward": {"30"},
		"auto_rewind": {"0", "1"}, "next_in_series": {"0", "1"}, "auto_download": {"0", "1"}}
	if rec := settingsPost(t, a, ck, "/me/settings", form, true); rec.Code != http.StatusNoContent || !settingsOf(t, a).AutoDownload {
		t.Fatalf("save on = %d", rec.Code)
	}
	form.Del("auto_download")
	if settingsPost(t, a, ck, "/me/settings", form, true); !settingsOf(t, a).AutoDownload {
		t.Error("a post without the key turned it off")
	}
}
