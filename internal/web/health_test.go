package web

import (
	"context"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// healthSweepRows are the route matrix rows of Library health (S3): owner and admin manage libraries; a member
// and a guest get 404.
func healthSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	return []sweepRow{
		sweepGet("GET /admin/health", func(*sweepEnv, int) string { return "/admin/health" }, staff),
		{Pattern: "GET /admin/health", Label: "one group", Method: http.MethodGet,
			Path: func(*sweepEnv, int) string { return "/admin/health?group=no_cover" }, Want: staff},
		{Pattern: "GET /admin/health", Label: "unknown group", Method: http.MethodGet,
			Path: func(*sweepEnv, int) string { return "/admin/health?group=nope" }, Want: sweepStaff(http.StatusNotFound, http.StatusNotFound)},
	}
}

type hlthEnv struct {
	a       *testApp
	owner   *http.Cookie
	folder  library.Folder
	missing []string // ids, in title order
}

func hlthNew(t *testing.T) *hlthEnv {
	t.Helper()
	a := newTestApp(t)
	e := &hlthEnv{a: a, owner: a.owner(t)}
	_, e.folder = librarytest.NewLibrary(t, a.lib, "Health library", t.TempDir())
	return e
}

func (e *hlthEnv) get(path string, ck *http.Cookie) (int, string) {
	rec := e.a.do(http.MethodGet, path, nil, nil, ck)
	return rec.Code, rec.Body.String()
}

func (e *hlthEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.a.st.Writer().Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestHealthRoles(t *testing.T) {
	e := hlthNew(t)
	librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: "Healthword gone", MissingSince: 5})
	for _, path := range []string{"/admin/health", "/admin/health?group=missing", "/admin/health?group=nope"} {
		for _, role := range []users.Role{users.RoleMember, users.RoleGuest} {
			if code, body := e.get(path, e.a.as(t, role)); code != http.StatusNotFound || strings.Contains(body, "Healthword") {
				t.Errorf("%s as %s = %d", path, role, code)
			}
		}
		if code, _ := e.get(path, nil); code == http.StatusOK {
			t.Errorf("%s signed out = 200", path)
		}
	}
	if code, _ := e.get("/admin/health", e.owner); code != http.StatusOK {
		t.Errorf("owner = %d", code)
	}
	if code, _ := e.get("/admin/health?group=nope", e.owner); code != http.StatusNotFound {
		t.Errorf("unknown group = %d", code)
	}
}

func TestHealthFixLinks(t *testing.T) {
	e := hlthNew(t)
	add := func(s librarytest.BookSpec) library.Book { return librarytest.AddBook(t, e.a.lib, e.folder, s) }
	// The only present book of the folder is "Fine", so the folder is not gone and "Lost" links to the book.
	lost := add(librarytest.BookSpec{Title: "Lost", MissingSince: 5})
	locked := add(librarytest.BookSpec{Title: "Locked", Refused: library.RefusedDRM})
	bad := add(librarytest.BookSpec{Title: "Badfile", DurationMs: 1000})
	e.exec(t, `UPDATE audio_files SET probe_error = 'not_audio' WHERE book_id = ?`, bad.ID)
	past := add(librarytest.BookSpec{Title: "Pastend", DurationMs: 1000})
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error {
		return tx.ReplaceChapters(past.ID, []library.Chapter{{StartMs: 0, EndMs: 9000, Title: "x", Source: library.ChapterUser}})
	}); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `INSERT INTO book_lookups (book_id, outcome, checked_at) VALUES (?, 'no_match', 1)`, past.ID)
	add(librarytest.BookSpec{Title: "Fine", DurationMs: 1000})

	code, body := e.get("/admin/health", e.owner)
	if code != http.StatusOK {
		t.Fatalf("overview = %d", code)
	}
	for _, want := range []string{
		`href="/books/` + lost.ID + `"`, `href="/books/` + locked.ID + `"`,
		html.EscapeString(healthUnreadable),
		`href="/books/` + past.ID + `/chapters"`,
		`href="/books/` + bad.ID + `/chapters"`, // an unreadable book also has no chapters
		healthMissingHead, healthRefusedHead, html.EscapeString(healthUnreadHead), healthNoChapHead, healthNoCoverHead, healthPastEndHead,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// Lookups are off in the test app, so a no-match book links to the book, not the lookups page.
	if strings.Contains(body, "/lookups") {
		t.Error("lookups link shown with lookups off")
	}
	if strings.Contains(body, healthAllClear) {
		t.Error("all clear shown with problems")
	}
}

func TestHealthFolderGoneLinksToEmptiedPage(t *testing.T) {
	e := hlthNew(t)
	b := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: "Only one", MissingSince: 5})
	_, body := e.get("/admin/health?group=missing", e.owner)
	if !strings.Contains(body, `href="/admin/folders/`+e.folder.ID+`/emptied"`) || !strings.Contains(body, healthReviewDir) {
		t.Errorf("no emptied link for %s", b.ID)
	}
}

func TestHealthPagingAndAllClear(t *testing.T) {
	e := hlthNew(t)
	_, body := e.get("/admin/health", e.owner)
	if !strings.Contains(body, healthAllClear) {
		t.Error("empty library lacks the all-clear line")
	}
	for i := 0; i < 55; i++ {
		librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: "Gone " + string(rune('A'+i/26)) + string(rune('a'+i%26)), MissingSince: 5})
	}
	_, over := e.get("/admin/health", e.owner)
	if !strings.Contains(over, "Show all 55") || !strings.Contains(over, `href="/admin/health?group=missing"`) {
		t.Error("overview lacks the show-all link")
	}
	_, p1 := e.get("/admin/health?group=missing", e.owner)
	_, p2 := e.get("/admin/health?group=missing&page=2", e.owner)
	if strings.Count(p1, `ts-needs__row`) != 50 || strings.Count(p2, `ts-needs__row`) != 5 {
		t.Errorf("rows page1 %d page2 %d", strings.Count(p1, `ts-needs__row`), strings.Count(p2, `ts-needs__row`))
	}
	prev, next := `>`+healthPrev+`</a>`, `>`+healthNext+`</a>`
	if !strings.Contains(p1, "Page 1 of 2") || !strings.Contains(p1, "group=missing&amp;page=2") || strings.Contains(p1, prev) || !strings.Contains(p1, next) {
		t.Error("page 1 paging links wrong")
	}
	if !strings.Contains(p2, "Page 2 of 2") || !strings.Contains(p2, prev) || strings.Contains(p2, next) {
		t.Error("page 2 paging links wrong")
	}
	if code, p9 := e.get("/admin/health?group=missing&page=9", e.owner); code != http.StatusOK || strings.Contains(p9, "ts-needs__row") {
		t.Errorf("past the end = %d", code)
	}
}

func TestHealthLinkOnLibrariesPage(t *testing.T) {
	e := hlthNew(t)
	_, body := e.get("/admin/libraries", e.owner)
	if !strings.Contains(body, `href="/admin/health"`) || !strings.Contains(body, healthTitle) {
		t.Error("Libraries page lacks the Library health link")
	}
}

// TestHealthLeadsWithProblems: the page opens with how many things need a look and a jump link to each
// group, the cover group comes before the groups that need no one's choice, and a refused book says why.
func TestHealthLeadsWithProblems(t *testing.T) {
	e := hlthNew(t)
	add := func(s librarytest.BookSpec) library.Book { return librarytest.AddBook(t, e.a.lib, e.folder, s) }
	add(librarytest.BookSpec{Title: "Lost", MissingSince: 5})
	add(librarytest.BookSpec{Title: "Locked", Refused: library.RefusedDRM})
	add(librarytest.BookSpec{Title: "Bare", DurationMs: 1000})
	_, body := e.get("/admin/health", e.owner)
	if !strings.Contains(body, "things need a look") || !strings.Contains(body, `href="#health-h-`+library.HealthNoCover+`"`) {
		t.Error("the summary and its jump links are missing")
	}
	if !strings.Contains(body, html.EscapeString(healthRefusedDRM)) {
		t.Error("a refused book does not say why")
	}
	cover, missing := strings.Index(body, `id="health-h-no_cover"`), strings.Index(body, `id="health-h-missing"`)
	if cover < 0 || missing < 0 || cover > missing {
		t.Errorf("the cover group (%d) does not lead the missing one (%d)", cover, missing)
	}
	// The Libraries page keeps only a short summary that links here.
	_, page := e.get("/admin/libraries", e.owner)
	if !strings.Contains(page, `href="/admin/health">`) || strings.Contains(page, "Locked") {
		t.Error("the Libraries page does not keep a short summary")
	}
}
