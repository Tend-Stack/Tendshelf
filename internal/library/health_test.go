package library_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func hlthExec(t *testing.T, r *library.Repo, q string, args ...any) {
	t.Helper()
	if _, err := r.Store().Writer().ExecContext(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}

func hlthChapters(t *testing.T, r *library.Repo, id, source string, end int64) {
	t.Helper()
	err := r.Update(ctx, func(tx *library.Tx) error {
		return tx.ReplaceChapters(id, []library.Chapter{{StartMs: 0, EndMs: end, Title: "One", Source: source}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hlthTitles(rows []library.HealthRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

func hlthCounts(t *testing.T, r *library.Repo, sc library.Scope) map[string]library.HealthCount {
	t.Helper()
	hs, err := r.Health(ctx, sc, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]library.HealthCount{}
	for _, h := range hs {
		m[h.Group] = h
	}
	return m
}

func TestHealthGroups(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	lost := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lost", MissingSince: 9})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Locked", Refused: library.RefusedDRM})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lost and locked", MissingSince: 9, Refused: library.RefusedDRM})
	bad := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Bad file", DurationMs: 1000})
	hlthExec(t, r, `UPDATE audio_files SET probe_error = 'not_audio' WHERE book_id = ?`, bad.ID)
	fine := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Fine", DurationMs: 1000})
	hlthChapters(t, r, fine.ID, library.ChapterFile, 1000)
	hlthExec(t, r, `UPDATE books SET cover_id = NULL WHERE id = ?`, fine.ID)
	text := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Text", DurationMs: 1000})
	hlthExec(t, r, `UPDATE books SET kind = 'text' WHERE id = ?`, text.ID)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Waiting", Unsettled: true})
	past := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Past end", DurationMs: 1000})
	hlthChapters(t, r, past.ID, library.ChapterUser, 5000)
	short := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Within slack", DurationMs: 1000})
	hlthChapters(t, r, short.ID, library.ChapterUser, 1500)
	hlthExec(t, r, `INSERT INTO book_lookups (book_id, outcome, checked_at) VALUES (?, 'no_match', 1), (?, 'matched', 1), (?, 'failed', 1)`,
		fine.ID, bad.ID, past.ID)
	_ = lost

	got := hlthCounts(t, r, adminAllScope)
	want := map[string][]string{
		library.HealthMissing:         {"Lost", "Lost and locked"},
		library.HealthRefused:         {"Locked"},
		library.HealthUnreadable:      {"Bad file"},
		library.HealthNoChapters:      {"Bad file"},
		library.HealthNoMatch:         {"Fine", "Past end"},
		library.HealthChaptersPastEnd: {"Past end"},
	}
	for g, titles := range want {
		h := got[g]
		if h.Count != len(titles) || !equalStrings(hlthTitles(h.Rows), titles) {
			t.Errorf("%s = %d %v, want %v", g, h.Count, hlthTitles(h.Rows), titles)
		}
	}
	// Text books and unsettled books are not "no chapters"; the unsettled one is not "no cover" either.
	for _, g := range []string{library.HealthNoChapters, library.HealthNoCover} {
		for _, title := range hlthTitles(got[g].Rows) {
			if title == "Text" && g == library.HealthNoChapters || title == "Waiting" {
				t.Errorf("%s lists %q", g, title)
			}
		}
	}
	if len(got) != len(library.HealthGroups) {
		t.Errorf("groups = %d", len(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHealthAgreesWithProblems(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	for _, s := range []librarytest.BookSpec{
		{Title: "A"}, {Title: "B", Refused: library.RefusedDRM}, {Title: "C", MissingSince: 5},
		{Title: "D", Refused: library.RefusedDamaged}, {Title: "E", Unsettled: true}, {Title: "F", MissingSince: 5, Refused: library.RefusedDRM},
	} {
		librarytest.AddBook(t, r, f, s)
	}
	p, err := r.Problems(ctx, adminAllScope, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := hlthCounts(t, r, adminAllScope)
	refused := 0
	for _, n := range p.RefusedCount {
		refused += n
	}
	if got[library.HealthMissing].Count != p.MissingCount || got[library.HealthRefused].Count != refused || got[library.HealthNoCover].Count != p.NoCoverCount {
		t.Errorf("health %d/%d/%d, problems %d/%d/%d", got[library.HealthMissing].Count, got[library.HealthRefused].Count,
			got[library.HealthNoCover].Count, p.MissingCount, refused, p.NoCoverCount)
	}
}

func TestHealthScopeAndPaging(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: n, MissingSince: 3})
	}
	rows, total, err := r.HealthPage(ctx, adminAllScope, library.HealthMissing, 2, 2)
	if err != nil || total != 5 || !equalStrings(hlthTitles(rows), []string{"c", "d"}) {
		t.Errorf("page = %v %d %v", hlthTitles(rows), total, err)
	}
	if !rows[0].FolderGone {
		t.Error("whole folder missing, FolderGone false")
	}
	rows, total, err = r.HealthPage(ctx, adminAllScope, library.HealthMissing, 10, 2)
	if err != nil || total != 5 || len(rows) != 0 {
		t.Errorf("past the end = %v %d %v", rows, total, err)
	}
	if _, _, err := r.HealthPage(ctx, adminAllScope, "nope", 0, 2); err != library.ErrUnknownHealthGroup {
		t.Errorf("unknown group err = %v", err)
	}
	if got := hlthCounts(t, r, adminNoneScope); got[library.HealthMissing].Count != 0 || len(got[library.HealthMissing].Rows) != 0 {
		t.Errorf("none scope = %+v", got[library.HealthMissing])
	}
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "present"})
	rows, _, _ = r.HealthPage(ctx, adminAllScope, library.HealthMissing, 0, 1)
	if rows[0].FolderGone {
		t.Error("a present book is in the folder, FolderGone true")
	}
}

// HealthTotal is the sum of the group counts, scoped like Health.
func TestHealthTotalMatchesHealth(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	a := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A", DurationMs: 1000})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "B", DurationMs: 1000})
	hlthExec(t, r, `UPDATE books SET missing_since = 5 WHERE id = ?`, a.ID)
	groups, err := r.Health(ctx, adminAllScope, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, g := range groups {
		want += g.Count
	}
	got, err := r.HealthTotal(ctx, adminAllScope)
	if err != nil || got != want || want == 0 {
		t.Errorf("HealthTotal = %d, %v; Health sums to %d", got, err, want)
	}
}
