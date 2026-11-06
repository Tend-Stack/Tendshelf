package web

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const tlPath = "/me/listening/history"

func tlAdd(t *testing.T, w *listeningWorld, id, user, book string, ended time.Time, startMs, endMs int64) {
	t.Helper()
	if _, err := w.a.st.Writer().Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, ?, ?, 60000, 100)`, id, user, book, ended.UnixMilli()-60_000, ended.UnixMilli(), startMs, endMs); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineRoles(t *testing.T) {
	w := newListeningWorld(t)
	if rec := w.a.do(http.MethodGet, tlPath, nil, nil); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("anonymous = %d %q, want a redirect to sign in", rec.Code, rec.Header().Get("Location"))
	}
	for _, role := range sweepRoles {
		ck := w.a.as(t, role)
		if code, body := w.get(tlPath, ck); code != http.StatusOK || !strings.Contains(body, "Nothing here yet") {
			t.Errorf("%s: %d, want the empty page", role, code)
		}
	}
	// A POST is not a route.
	if rec := w.a.do(http.MethodPost, tlPath, nil, nil, w.owner); rec.Code == http.StatusOK {
		t.Errorf("POST answered 200")
	}
}

func TestTimelineOnlyOwnRowsAndRevoked(t *testing.T) {
	w := newListeningWorld(t)
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	tlAdd(t, w, "tl_o", w.ownerID, w.ownerBook, at, 3_723_000, 6_010_000)
	tlAdd(t, w, "tl_m", w.memberID, w.memBook, at.Add(time.Hour), 0, 60_000)

	_, body := w.get(tlPath, w.owner)
	mustContain(t, "owner", body, listenOwnerTitle, "from 1:02:03 to 1:40:10", "Go back here", `data-ts-play-at="6010000"`, "Thursday 5 November 2026")
	mustNotContain(t, "owner", body, listenMemberTitle)
	_, body = w.get(tlPath, w.member)
	mustContain(t, "member", body, listenMemberTitle)
	mustNotContain(t, "member", body, listenOwnerTitle)

	// A named book that is another person's data is the same as an unknown id: nothing.
	_, body = w.get(tlPath+"?book="+w.memBook, w.owner)
	mustNotContain(t, "owner asking for the member's book", body, listenMemberTitle, listenOwnerTitle)
	_, body = w.get(tlPath+"?book=nope", w.owner)
	mustContain(t, "unknown book", body, "Nothing here yet")
	_, body = w.get(tlPath+"?book="+w.ownerBook, w.owner)
	mustContain(t, "one book", body, "Only "+listenOwnerTitle+".", "Show every book")

	// The member loses the library: the rows stay stored but nothing lists.
	if _, err := w.a.st.Writer().Exec(`DELETE FROM grants WHERE user_id = ?`, w.memberID); err != nil {
		t.Fatal(err)
	}
	_, body = w.get(tlPath, w.member)
	mustNotContain(t, "revoked", body, listenMemberTitle)
}

func TestTimelineGroupsByDayInTheZone(t *testing.T) {
	w := newListeningWorld(t)
	statsZone(t, w.a, w.ownerID, "America/New_York")
	tlAdd(t, w, "tl_1", w.ownerID, w.ownerBook, time.Date(2026, 11, 7, 3, 30, 0, 0, time.UTC), 0, 1000) // Nov 6, 22:30
	tlAdd(t, w, "tl_2", w.ownerID, w.ownerBook, time.Date(2026, 11, 7, 5, 30, 0, 0, time.UTC), 0, 1000) // Nov 7, 00:30
	_, body := w.get(tlPath, w.owner)
	mustContain(t, "days", body, "Saturday 7 November 2026", "Friday 6 November 2026", "00:29 to 00:30", "22:29 to 22:30")
	if strings.Index(body, "Saturday 7 November 2026") > strings.Index(body, "Friday 6 November 2026") {
		t.Error("newest day is not first")
	}
}

func TestTimelinePagesWithACursor(t *testing.T) {
	w := newListeningWorld(t)
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for i := range 51 {
		tlAdd(t, w, "tl_p"+string(rune('a'+i/26))+string(rune('a'+i%26)), w.ownerID, w.ownerBook, at.Add(time.Duration(i)*time.Minute), 0, 1000)
	}
	_, body := w.get(tlPath, w.owner)
	if strings.Count(body, "Go back here") != 50 || !strings.Contains(body, "Show older") {
		t.Fatalf("first page: %d rows, older link %v", strings.Count(body, "Go back here"), strings.Contains(body, "Show older"))
	}
	i := strings.Index(body, "/me/listening/history?cursor=")
	j := strings.Index(body[i:], `"`)
	href := strings.ReplaceAll(body[i:i+j], "&amp;", "&")
	_, body = w.get(href, w.owner)
	if strings.Count(body, "Go back here") != 1 || strings.Contains(body, "Show older") {
		t.Fatalf("second page: %d rows", strings.Count(body, "Go back here"))
	}
}

func TestTimelineLinks(t *testing.T) {
	w := newListeningWorld(t)
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 2), 120)
	_, body := w.get("/me/listening", w.owner)
	mustContain(t, "dashboard", body, "Where was I?", tlPath)
}
