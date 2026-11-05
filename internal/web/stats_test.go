package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// statsNow is Wednesday 2026-11-04 10:00 UTC: the week began Sunday 2026-11-01.
var statsNow = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

const statsSecretTitle = "Zzyzx Secret Title"

// statsApp is a test app whose clock stands at statsNow, with one book.
func statsApp(t *testing.T) (*testApp, string) {
	t.Helper()
	a := newTestApp(t, func(o *Options) { o.Now = func() time.Time { return statsNow } })
	a.owner(t)
	_, f := librarytest.NewLibrary(t, a.lib, "Books", adminAudioDir(t, false))
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: statsSecretTitle, DurationMs: 3_600_000})
	return a, b.ID
}

var statsSeq int

// statsListen records a finished session for a person: started at `at`, `mins` minutes long.
func statsListen(t *testing.T, a *testApp, user, book string, at time.Time, mins int64) {
	t.Helper()
	statsSeq++
	ms := mins * 60_000
	if _, err := a.st.Writer().Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, 100)`, fmt.Sprintf("ses_w%d", statsSeq), user, book, at.UnixMilli(), at.UnixMilli()+ms, ms, ms); err != nil {
		t.Fatal(err)
	}
}

func statsFinish(t *testing.T, a *testApp, user, book string, at time.Time) {
	t.Helper()
	if _, err := a.st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, 3600000, 3600000, ?, ?, ?, 'dev')`, user, book, at.UnixMilli(), at.UnixMilli(), at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

// statsZone stores a person's zone the way the browser's post does.
func statsZone(t *testing.T, a *testApp, user, zone string) {
	t.Helper()
	s, err := a.srv.users.LoadSettings(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	s.TimeZone = zone
	if err := a.srv.users.SaveSettings(context.Background(), user, s); err != nil {
		t.Fatal(err)
	}
}

var statsPanelRE = regexp.MustCompile(`(?s)<section aria-labelledby="pf-stats".*?</section>`)

func statsPanel(t *testing.T, page string) string {
	t.Helper()
	p := statsPanelRE.FindString(page)
	if p == "" {
		t.Fatal("no listening panel on the page")
	}
	return p
}

func TestStatsProfilePanel(t *testing.T) {
	a, book := statsApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	member := a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, member)
	statsZone(t, a, ownerID, "UTC")

	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	statsListen(t, a, ownerID, book, day(1, 9), 120) // Sunday
	statsListen(t, a, ownerID, book, day(2, 9), 72)  // Monday
	statsListen(t, a, ownerID, book, day(3, 9), 30)  // Tuesday
	statsFinish(t, a, ownerID, book, day(2, 12))
	// Another person's listening must not reach this panel.
	statsListen(t, a, memberID, book, day(4, 8), 600)
	statsFinish(t, a, memberID, book, day(3, 12))

	page := a.do(http.MethodGet, "/me", nil, nil, owner).Body.String()
	panel := statsPanel(t, page)
	for _, want := range []string{
		"Your listening", "This week", "3 h 42 m", "In a row", "3 days", "Finished in 2026",
		`<dd class="time ts-stats__value">1</dd>`, "Only you see these.",
		`aria-label="Listened on Sunday to Tuesday this week; not yet today, Wednesday"`,
		"ts-stats__day--today",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel lacks %q:\n%s", want, panel)
		}
	}
	if n := strings.Count(panel, "ts-stats__day--listened"); n != 3 {
		t.Errorf("%d listened marks, want 3", n)
	}
	if !strings.Contains(page, `data-ts-tz="UTC"`) {
		t.Error("the body does not carry the stored zone")
	}
	// It comes before the account card.
	if i, j := strings.Index(page, `aria-labelledby="pf-stats"`), strings.Index(page, `aria-labelledby="pf-account"`); i < 0 || j < 0 || i > j {
		t.Errorf("the panel is not first: %d, %d", i, j)
	}
	// The member sees only their own numbers.
	mp := statsPanel(t, a.do(http.MethodGet, "/me", nil, nil, member).Body.String())
	if !strings.Contains(mp, "10 h 0 m") || strings.Contains(mp, "3 h 42 m") {
		t.Errorf("member panel:\n%s", mp)
	}
}

func TestStatsTimeZonePost(t *testing.T) {
	a, _ := statsApp(t)
	ck := a.owner(t)
	if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"time_zone": {"Asia/Tokyo"}}, true); rec.Code != http.StatusNoContent {
		t.Fatalf("valid zone = %d", rec.Code)
	}
	if got := settingsOf(t, a).TimeZone; got != "Asia/Tokyo" {
		t.Fatalf("stored %q", got)
	}
	// The zone alone stores no theme, so the theme cookie still decides for someone who never chose one.
	if theme, err := a.srv.users.Theme(context.Background(), sweepUserID(t, a, ck)); err != nil || theme != "" {
		t.Fatalf("a zone post stored theme %q (%v)", theme, err)
	}
	for _, bad := range []string{"Mars/Base", "", "Local"} {
		if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"time_zone": {bad}}, true); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("zone %q = %d, want 422", bad, rec.Code)
		}
	}
	if got := settingsOf(t, a).TimeZone; got != "Asia/Tokyo" {
		t.Fatalf("a refused post changed the zone to %q", got)
	}
	// A later post of another setting keeps the zone.
	if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"speed": {"1.25"}}, true); rec.Code != http.StatusNoContent {
		t.Fatalf("speed = %d", rec.Code)
	}
	if s := settingsOf(t, a); s.TimeZone != "Asia/Tokyo" || s.Speed != 1.25 {
		t.Fatalf("stored %+v", s)
	}
	page := a.do(http.MethodGet, "/me", nil, nil, ck).Body.String()
	if !strings.Contains(page, `data-ts-tz="Asia/Tokyo"`) {
		t.Error("the page does not carry the stored zone")
	}
}

func TestStatsAdminView(t *testing.T) {
	a, book := statsApp(t)
	owner := a.owner(t)
	admin := a.as(t, users.RoleAdmin)
	admin2 := a.as(t, users.RoleAdmin)
	member := a.as(t, users.RoleMember)
	adminID, admin2ID, memberID := sweepUserID(t, a, admin), sweepUserID(t, a, admin2), sweepUserID(t, a, member)
	statsListen(t, a, adminID, book, time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), 125)
	statsListen(t, a, memberID, book, time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), 45)
	statsListen(t, a, memberID, book, time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC), 5)
	statsFinish(t, a, memberID, book, time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	statsZone(t, a, memberID, "UTC")
	_ = admin2

	// The owner sees an admin's numbers; an admin sees a member's. Three numbers, no marks, no title.
	page := a.do(http.MethodGet, "/admin/users/"+adminID, nil, nil, owner)
	if page.Code != http.StatusOK {
		t.Fatalf("owner on admin = %d", page.Code)
	}
	for _, want := range []string{"Listening", "2 h 5 m"} {
		if !strings.Contains(statsPanel(t, page.Body.String()), want) {
			t.Errorf("owner's view of the admin lacks %q", want)
		}
	}
	rec := a.do(http.MethodGet, "/admin/users/"+memberID, nil, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin on member = %d", rec.Code)
	}
	body := rec.Body.String()
	panel := statsPanel(t, body)
	for _, want := range []string{"This week", "50 m", "In a row", "2 days", "Finished in 2026", `<dd class="time ts-stats__value">1</dd>`} {
		if !strings.Contains(panel, want) {
			t.Errorf("admin's view of the member lacks %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "ts-stats__days") || strings.Contains(panel, "Only you see these.") {
		t.Error("the person page shows the day marks")
	}
	if strings.Contains(body, statsSecretTitle) {
		t.Error("the person page names a book")
	}
	// Unchanged from today: an admin on another admin, and a member on anyone.
	for _, c := range []struct {
		who  string
		ck   *http.Cookie
		path string
	}{
		{"admin on admin", admin, "/admin/users/" + admin2ID},
		{"member on member", member, "/admin/users/" + memberID},
		{"member on admin", member, "/admin/users/" + adminID},
	} {
		if r := a.do(http.MethodGet, c.path, nil, nil, c.ck); r.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", c.who, r.Code)
		}
	}
}

func TestStatsNoTitles(t *testing.T) {
	a, book := statsApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	statsZone(t, a, ownerID, "UTC")
	statsListen(t, a, ownerID, book, time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC), 30)
	statsFinish(t, a, ownerID, book, time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC))
	page := a.do(http.MethodGet, "/me", nil, nil, owner).Body.String()
	if panel := statsPanel(t, page); strings.Contains(panel, statsSecretTitle) || strings.Contains(panel, book) {
		t.Errorf("the panel names the book:\n%s", panel)
	}
}

func TestStatsLabels(t *testing.T) {
	for _, c := range []struct {
		marks string // l listened, t today, m missed, a ahead
		want  string
	}{
		{"lllllta", "Listened on Sunday to Thursday this week; not yet today, Friday"},
		{"mmmtaaa", "No listening yet this week; not yet today, Wednesday"},
		{"mlmlaaa", "Listened on Monday and Wednesday this week"},
		{"llmlmtm", "Listened on Sunday, Monday and Wednesday this week; not yet today, Friday"},
		{"mmmmmmm", "No listening yet this week"},
		{"llllllt", "Listened on Sunday to Friday this week; not yet today, Saturday"},
	} {
		var days [7]progress.DayMark
		for i, ch := range c.marks {
			days[i] = progress.DayMark{Weekday: time.Weekday(i), State: map[rune]string{'l': "listened", 't': "today", 'm': "missed", 'a': "ahead"}[ch]}
		}
		if got := statsLabel(days); got != c.want {
			t.Errorf("%s: %q, want %q", c.marks, got, c.want)
		}
	}
}
