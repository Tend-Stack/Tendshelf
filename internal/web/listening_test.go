package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// listeningNow is Saturday 2026-11-07 10:00 UTC, the clock of the dashboard board.
var listeningNow = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

const (
	listenOwnerTitle  = "Zzyzx Owner Book"
	listenMemberTitle = "Qwerty Member Book"
)

// listeningWorld is an app with an owner and a member who each listen to their own book. The member is
// granted the library, so both see both titles; only their own listening differs.
type listeningWorld struct {
	a                  *testApp
	owner, member      *http.Cookie
	ownerID, memberID  string
	ownerBook, memBook string
}

func newListeningWorld(t *testing.T) *listeningWorld {
	t.Helper()
	a := newTestApp(t, func(o *Options) { o.Now = func() time.Time { return listeningNow } })
	w := &listeningWorld{a: a, owner: a.owner(t), member: a.as(t, users.RoleMember)}
	w.ownerID, w.memberID = sweepUserID(t, a, w.owner), sweepUserID(t, a, w.member)
	statsZone(t, a, w.ownerID, "UTC")
	statsZone(t, a, w.memberID, "UTC")
	lib, f := librarytest.NewLibrary(t, a.lib, "Books", adminAudioDir(t, false))
	w.ownerBook = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: listenOwnerTitle, DurationMs: 3_600_000, Authors: []string{"Olive Owner"}}).ID
	w.memBook = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: listenMemberTitle, DurationMs: 3_600_000, Authors: []string{"Mia Member"}}).ID
	if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gl_m', ?, 'library', ?, 1, 1)`,
		w.memberID, lib.ID); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *listeningWorld) get(path string, ck *http.Cookie) (int, string) {
	rec := w.a.do(http.MethodGet, path, nil, nil, ck)
	return rec.Code, rec.Body.String()
}

// listenAt is a time on a day of 2026 at 09:00 UTC.
func listenAt(month time.Month, day int) time.Time {
	return time.Date(2026, month, day, 9, 0, 0, 0, time.UTC)
}

func mustContain(t *testing.T, where, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s lacks %q", where, want)
		}
	}
}

func mustNotContain(t *testing.T, where, body string, bans ...string) {
	t.Helper()
	for _, ban := range bans {
		if strings.Contains(body, ban) {
			t.Errorf("%s contains %q", where, ban)
		}
	}
}

func TestListeningPage(t *testing.T) {
	w := newListeningWorld(t)
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 2), 120) // Monday
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 3), 60)
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(10, 20), 30) // October
	statsFinish(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 3))

	// The default is the month: totals, numbers, sentences, the nav, no Later on the current period.
	code, month := w.get("/me/listening", w.owner)
	if code != http.StatusOK {
		t.Fatalf("month = %d", code)
	}
	mustContain(t, "month page", month,
		"Your listening", "Only you see these.", "November 2026", `aria-label="Show"`,
		`<a class="ts-seg__item" href="/me/listening?range=month" aria-current="page">Month</a>`,
		`aria-label="Show October 2026"`, "ts-dash__navgap",
		"3 h 0 m", "2 of 7", "Usual speed", "1×",
		"3 h 0 m in November 2026, about 1 h 30 m on the days you listened.",
		"Listening time", "Days of the week", "You listen most on Mondays.", "Time of day", "You listen most in the morning.",
		"Most listened", listenOwnerTitle, "Olive Owner", "Finished",
		`class="ts-chart ts-chart--edge"`, "Listening by hour of the day")
	mustNotContain(t, "month page", month, `aria-label="Show December 2026"`, "Day by day", "Year by year", `style=`)
	if n := strings.Count(month, `<svg class="ts-chart__plot"`); n != 3 {
		t.Errorf("%d bar charts on the month page, want 3", n)
	}

	// Week: Listening time spans the row; there is no Days of the week card.
	code, week := w.get("/me/listening?range=week", w.owner)
	if code != http.StatusOK {
		t.Fatalf("week = %d", code)
	}
	mustContain(t, "week page", week, "1–7 Nov 2026", `class="ts-dash__card ts-dash__card--wide" aria-labelledby="ld1"`,
		"3 h 0 m in 1–7 Nov 2026")
	mustNotContain(t, "week page", week, "Days of the week")

	// Year: the heat grid, both layouts, the key and the month table.
	code, year := w.get("/me/listening?range=year", w.owner)
	if code != http.StatusOK {
		t.Fatalf("year = %d", code)
	}
	mustContain(t, "year page", year, "Day by day", "You listened on 3 days in 2026.", "ts-heat--weeks", "ts-heat--months",
		`viewBox="0 0 636 84"`, `viewBox="0 0 372 192"`, "No listening", "Under 15 m", "1 h 30 m or more",
		"3 h 30 m in 2026", "Listening by month", "Listening time by month")
	mustNotContain(t, "year page", year, "Year by year") // one year only
	if n := strings.Count(year, "ts-heat__c0\" x="); n == 0 {
		t.Error("no empty heat cells")
	}

	// One month back shows Later, linked to the current month without a date.
	code, back := w.get("/me/listening?range=month&at=2026-10-15", w.owner)
	if code != http.StatusOK {
		t.Fatalf("back = %d", code)
	}
	mustContain(t, "October", back, "October 2026", `aria-label="Show November 2026"`, `href="/me/listening?range=month"`,
		`aria-label="Show September 2026"`, `href="/me/listening?range=month&amp;at=2026-09-01"`, "30 m in October 2026, about 30 m")
	mustNotContain(t, "October", back, "ts-dash__navgap")
}

func TestListeningBadParams(t *testing.T) {
	w := newListeningWorld(t)
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 3), 30)
	for _, c := range []struct {
		path string
		code int
	}{
		{"/me/listening?range=decade", 404},
		{"/me/listening?range=", 200},
		{"/me/listening?at=2026-13-01", 404},
		{"/me/listening?at=nonsense", 404},
		{"/me/listening?at=1999-12-31", 404},
		{"/me/listening?at=2000-01-01", 200},
		{"/me/listening?range=year&at=2000-06-01", 200},
		{"/me/listening?at=2030-01-01", 200},
	} {
		if code, _ := w.get(c.path, w.owner); code != c.code {
			t.Errorf("GET %s = %d, want %d", c.path, code, c.code)
		}
	}
	// A future date shows the current period.
	_, body := w.get("/me/listening?at=2030-01-01", w.owner)
	mustContain(t, "future at", body, "November 2026")
	// An htmx request gets the toast, not a page.
	rec := w.a.do(http.MethodGet, "/me/listening?range=decade", nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, w.owner)
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("htmx 404 = %d %q", rec.Code, rec.Body.String())
	}
}

func TestListeningOnlyOwnData(t *testing.T) {
	w := newListeningWorld(t)
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 3), 120)
	statsListen(t, w.a, w.memberID, w.memBook, listenAt(11, 4), 45)
	statsFinish(t, w.a, w.memberID, w.memBook, listenAt(11, 4))

	_, o := w.get("/me/listening", w.owner)
	mustContain(t, "owner", o, "2 h 0 m in November 2026", listenOwnerTitle)
	mustNotContain(t, "owner", o, listenMemberTitle, "Mia Member", "45 m in")
	_, m := w.get("/me/listening?user="+w.ownerID+"&person="+w.ownerID, w.member)
	mustContain(t, "member", m, "45 m in November 2026", listenMemberTitle, "Mia Member")
	mustNotContain(t, "member", m, listenOwnerTitle, "Olive Owner", "2 h 0 m")
}

func TestListeningHiddenTitle(t *testing.T) {
	w := newListeningWorld(t)
	statsListen(t, w.a, w.memberID, w.memBook, listenAt(11, 3), 60)
	statsListen(t, w.a, w.memberID, w.ownerBook, listenAt(11, 4), 60)
	// Revoke the member's library grant: both books are hidden, but the time still counts.
	if _, err := w.a.st.Writer().Exec(`DELETE FROM grants WHERE user_id = ?`, w.memberID); err != nil {
		t.Fatal(err)
	}
	code, body := w.get("/me/listening", w.member)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mustContain(t, "member without a grant", body, "2 h 0 m in November 2026", "2 of 7", "Time of day")
	mustNotContain(t, "member without a grant", body, listenOwnerTitle, listenMemberTitle, "Olive Owner", "Mia Member", "Most listened")
}

func TestListeningEmpty(t *testing.T) {
	w := newListeningWorld(t)
	// Never listened: the empty state, no controls, a link to the library.
	_, body := w.get("/me/listening", w.owner)
	mustContain(t, "never", body, "Nothing to show yet", "Listen to a book and your time will show here, by day and by hour.",
		"Go to your library", `href="/libraries"`)
	mustNotContain(t, "never", body, "ts-dash__range", "Listening time")
	// Listened, but not in this period: the controls stay and a line replaces the charts.
	statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(9, 3), 60)
	_, body = w.get("/me/listening", w.owner)
	mustContain(t, "empty period", body, "No listening in November 2026.", "ts-dash__range", `aria-label="Show October 2026"`)
	mustNotContain(t, "empty period", body, "Listening time", "ts-dash__nums", "Nothing to show yet")
}

func TestListeningProfileLink(t *testing.T) {
	w := newListeningWorld(t)
	_, me := w.get("/me", w.owner)
	mustContain(t, "profile", me, `<a class="label ts-dash__more" href="/me/listening">See your listening over time</a>`)
	_, person := w.get("/admin/users/"+w.memberID, w.owner)
	mustNotContain(t, "person page", person, "See your listening over time", "/me/listening")
}

func TestListeningRoleAccess(t *testing.T) {
	w := newListeningWorld(t)
	for _, role := range []users.Role{users.RoleOwner, users.RoleAdmin, users.RoleMember} {
		if code, _ := w.get("/me/listening", w.a.as(t, role)); code != http.StatusOK {
			t.Errorf("%s = %d, want 200", role, code)
		}
	}
	rec := w.a.do(http.MethodGet, "/me/listening", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("anonymous = %d %q, want a redirect to sign in", rec.Code, rec.Header().Get("Location"))
	}
	// No API, no fragment route.
	for _, p := range []string{"/api/v1/listening", "/hx/listening"} {
		if code, _ := w.get(p, w.owner); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
	// Unsafe methods are not routed.
	if rec := w.a.do(http.MethodPost, "/me/listening", nil, nil, w.owner); rec.Code == http.StatusOK {
		t.Errorf("POST = %d", rec.Code)
	}
}

var styleAttrRE = regexp.MustCompile(`\sstyle\s*=`)

func TestListeningNoStyleAttr(t *testing.T) {
	w := newListeningWorld(t)
	for d := 1; d <= 6; d++ {
		statsListen(t, w.a, w.ownerID, w.ownerBook, listenAt(11, d), int64(10*d))
		statsListen(t, w.a, w.ownerID, w.memBook, listenAt(10, d), 70)
	}
	statsFinish(t, w.a, w.ownerID, w.ownerBook, listenAt(11, 3))
	for _, path := range []string{"/me/listening", "/me/listening?range=week", "/me/listening?range=year"} {
		_, body := w.get(path, w.owner)
		if styleAttrRE.MatchString(body) {
			t.Errorf("%s has a style attribute", path)
		}
		if strings.Contains(body, "<text") {
			t.Errorf("%s has SVG text", path)
		}
	}
}

// TestListeningHeatGrid pins the heat grid of a year: 53 week columns in 2026, 54 in 2028 (1 January a
// Saturday, 31 December a Sunday), no cell after today and none on 30 February.
func TestListeningHeatGrid(t *testing.T) {
	for _, c := range []struct {
		year, weeks, days int
		today             time.Time
	}{
		{2026, 53, 365, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{2028, 54, 366, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)},
		{2026, 53, 311, time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)},
	} {
		in := heatInput(t, c.year, c.today)
		v := heatGrid(in)
		if v.Weeks != c.weeks {
			t.Errorf("%d: %d weeks, want %d", c.year, v.Weeks, c.weeks)
		}
		if len(v.WeekLabels) != v.Weeks {
			t.Errorf("%d: %d week labels for %d weeks", c.year, len(v.WeekLabels), v.Weeks)
		}
		named := 0
		for _, l := range v.WeekLabels {
			if l != "" {
				named++
			}
		}
		if named != 12 {
			t.Errorf("%d: %d month names in the week labels, want 12", c.year, named)
		}
		if len(v.Cells) != c.days {
			t.Errorf("%d (today %s): %d cells, want %d", c.year, c.today.Format("2006-01-02"), len(v.Cells), c.days)
		}
		last := v.Cells[len(v.Cells)-1]
		if last.Week >= v.Weeks {
			t.Errorf("%d: the last cell is in column %d of %d", c.year, last.Week, v.Weeks)
		}
		for _, cell := range v.Cells {
			if cell.Month == 1 && cell.Day > 29 {
				t.Errorf("%d: a cell on %d February", c.year, cell.Day)
			}
		}
	}
}

func TestListeningCopyHelpers(t *testing.T) {
	for _, c := range []struct {
		ms   int64
		want string
	}{{45 * 60_000, "45 m"}, {2 * 3_600_000, "2 h"}, {90 * 60_000, "1 h 30 m"}, {0, "0 m"}} {
		if got := tickText(c.ms); got != c.want {
			t.Errorf("tickText(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
	for _, c := range []struct{ ms, want int64 }{
		{1, 10 * 60_000}, {10 * 60_000, 10 * 60_000}, {10*60_000 + 1, 20 * 60_000}, {108 * 60_000, 120 * 60_000},
		{371 * 60_000, 480 * 60_000}, {60_000 * 60_000, 60_000 * 60_000}, {60_001 * 60_000, 120_000 * 60_000},
	} {
		if got := niceMax(c.ms); got != c.want {
			t.Errorf("niceMax(%d) = %d, want %d", c.ms, got, c.want)
		}
	}
	for pct, want := range map[int]string{100: "1×", 120: "1.2×", 125: "1.25×", 150: "1.5×"} {
		if got := speedText(pct); got != want {
			t.Errorf("speedText(%d) = %q, want %q", pct, got, want)
		}
	}
	for _, c := range []struct {
		ms   int64
		want int
	}{{0, 0}, {59_999, 0}, {60_000, 1}, {15*60_000 - 1, 1}, {15 * 60_000, 2}, {45 * 60_000, 3}, {90*60_000 - 1, 3}, {90 * 60_000, 4}} {
		if got := heatLevel(c.ms); got != c.want {
			t.Errorf("heatLevel(%d) = %d, want %d", c.ms, got, c.want)
		}
	}
	for _, c := range []struct {
		h    int
		want string
	}{{0, "midnight to 1 am"}, {6, "6 am to 7 am"}, {11, "11 am to noon"}, {12, "noon to 1 pm"}, {23, "11 pm to midnight"}} {
		if got := hourSpan(c.h); got != c.want {
			t.Errorf("hourSpan(%d) = %q, want %q", c.h, got, c.want)
		}
	}
}

// heatInput is a year of listening of 20 minutes a day, seen on `today`.
func heatInput(t *testing.T, year int, today time.Time) listeningIn {
	t.Helper()
	p, err := progress.PeriodOf(progress.RangeYear, time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	l := progress.Listening{Period: p, Days: make([]int64, p.Days), Ever: true, TotalMs: 1}
	l.DaysSoFar = min(p.Days, int(today.Sub(p.First).Hours()/24)+1)
	for i := range l.Days {
		l.Days[i] = 20 * 60_000
	}
	return listeningIn{L: l, Today: today, Loc: time.UTC}
}
