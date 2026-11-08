package web

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestShelfViewSpineWidth(t *testing.T) {
	for _, c := range []struct {
		ms   int64
		want string
	}{
		{0, "w4"}, {-1, "w4"}, {30 * 60_000, "w1"}, {1 * hourMs, "w1"}, {3 * hourMs / 2, "w1"}, {2 * hourMs, "w2"},
		{3 * hourMs, "w3"}, {4 * hourMs, "w3"}, {5 * hourMs, "w4"}, {7 * hourMs, "w4"}, {8 * hourMs, "w5"}, {10 * hourMs, "w5"},
		{12 * hourMs, "w6"}, {15 * hourMs, "w7"}, {20 * hourMs, "w8"}, {25 * hourMs, "w9"}, {30 * hourMs, "w10"},
		{35 * hourMs, "w11"}, {40 * hourMs, "w12"}, {100 * hourMs, "w12"},
	} {
		if got := spineWidth(c.ms); got != "ts-spine--"+c.want {
			t.Errorf("spineWidth(%d) = %s, want %s", c.ms, got, c.want)
		}
	}
	seen, prev := map[string]bool{}, 0
	for ms := int64(10 * 60_000); ms <= 100*hourMs; ms += 10 * 60_000 {
		step := spineStep(ms)
		if step < prev {
			t.Fatalf("step fell from %d to %d at %d ms", prev, step, ms)
		}
		prev = step
		seen[spineWidth(ms)] = true
	}
	if len(seen) != 12 {
		t.Errorf("%d distinct widths, want 12", len(seen))
	}
}

func TestShelfViewSpineLength(t *testing.T) {
	for _, c := range []struct {
		name string
		card library.BookCard
		want string
	}{
		{"text 9,300 words", library.BookCard{Kind: library.BookText, TextWords: 9300}, "ts-spine--w1"},
		{"text 93,000 words", library.BookCard{Kind: library.BookText, TextWords: 93000}, "ts-spine--w5"},
		{"text 558,000 words", library.BookCard{Kind: library.BookText, TextWords: 558000}, "ts-spine--w12"},
		{"audio ignores words", library.BookCard{Kind: library.BookAudio, DurationMs: 15 * hourMs, TextWords: 9300}, "ts-spine--w7"},
	} {
		if got := spineWidth(spineLengthMs(c.card)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestShelfViewSpineHeight(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("bk_%03d", i)
		h := spineHeight(id)
		if h != spineHeight(id) {
			t.Fatalf("height of %s is not stable", id)
		}
		seen[h] = true
	}
	if len(seen) != 5 {
		t.Errorf("%d heights over 200 ids, want 5", len(seen))
	}
	if spineHeight("") != "ts-spine--h3" {
		t.Errorf("no seed: %s", spineHeight(""))
	}
	a := cardView(library.BookCard{ID: "bk_1", Series: &library.SeriesRef{ID: "ser_1"}})
	b := cardView(library.BookCard{ID: "bk_2", Series: &library.SeriesRef{ID: "ser_1"}})
	if a.HeightClass != b.HeightClass {
		t.Errorf("one series, two heights: %s %s", a.HeightClass, b.HeightClass)
	}
}

func TestShelfViewSpineAuthor(t *testing.T) {
	p := func(n string) []library.PersonRef { return []library.PersonRef{{Name: n}} }
	for _, c := range []struct {
		in   []library.PersonRef
		want string
	}{
		{p("Jane Austen"), "Austen"}, {p("Homer"), "Homer"}, {p("Austen, Jane"), "Austen"},
		{p("Martin Luther King Jr."), "King"}, {p("Hank Aaron III"), "Aaron"}, {nil, ""},
	} {
		if got := spineAuthor(c.in); got != c.want {
			t.Errorf("spineAuthor(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func shelfViewBody(a *testApp, path string, ck *http.Cookie) string {
	return html.UnescapeString(browseGet(a, path, false, ck).Body.String())
}

func TestShelfViewHandler(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 60)

	body := shelfViewBody(a, "/libraries?view=shelf&sort=title", nil)
	for _, want := range []string{`<ul id="library-grid"`, "ts-shelf--wrap", `<li class="ts-shelf__more">`, `hx-target="closest li"`,
		`name="view" value="shelf" aria-current="true"`, `name="next" value="/libraries?sort=title"`} {
		if !strings.Contains(body, want) {
			t.Errorf("shelf page lacks %q", want)
		}
	}
	more := browseMore(body)
	if !strings.Contains(more, "view=shelf") {
		t.Errorf("the sentinel's address loses the view: %q", more)
	}
	if strings.Contains(body, "ts-card") {
		t.Error("the shelf draws cards")
	}
	for _, m := range regexp.MustCompile(`href="(/libraries[^"]*)"`).FindAllStringSubmatch(body, -1) {
		if strings.HasPrefix(m[1], "/libraries?") && !strings.Contains(m[1], "view=shelf") {
			t.Errorf("a link loses the view: %s", m[1])
		}
	}
	if !strings.Contains(body, `<input type="hidden" name="view" value="shelf">`) {
		t.Error("the title form loses the view")
	}

	// The fragment is list items only, and walks the same books as the grid does.
	frag := html.UnescapeString(browseGet(a, more, true, nil).Body.String())
	if !strings.HasPrefix(strings.TrimSpace(frag), "<li") || strings.Contains(frag, "<ul") || strings.Contains(frag, "<html") {
		t.Errorf("fragment is not bare items: %.80s", frag)
	}
	grid := shelfViewBody(a, "/libraries?sort=title", nil)
	gridIDs, shelfIDs := browseIDs(grid), browseIDs(body)
	if strings.Join(gridIDs, ",") != strings.Join(shelfIDs, ",") {
		t.Errorf("shelf and grid differ in the first page")
	}

	// Defaults stay: a bare address and nonsense are the grid.
	for _, path := range []string{"/libraries", "/libraries?view=nonsense"} {
		b := shelfViewBody(a, path, nil)
		if strings.Contains(b, "ts-shelf--wrap") || !strings.Contains(b, "ts-grid") {
			t.Errorf("%s is not the grid", path)
		}
	}
	if b := shelfViewBody(a, "/libraries", nil); strings.Contains(b, "view=") && !strings.Contains(b, `name="view"`) {
		t.Error("the bare address carries a view")
	}
}

func TestShelfViewLabels(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	books := []library.Book{
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Started", Authors: []string{"Imogen Hale"}, DurationMs: 12*hourMs + 30*60_000, AddedAt: 3000}),
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Done", Authors: []string{"Imogen Hale"}, DurationMs: 12*hourMs + 30*60_000, AddedAt: 2000}),
	}
	u, _, err := a.srv.users.ByUsername(context.Background(), "ana")
	if err != nil {
		t.Fatal(err)
	}
	now := int64(1_000_000)
	if _, err := a.st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
		VALUES (?, ?, ?, ?, NULL, ?, ?, 'dev')`, u.ID, books[0].ID, (12*hourMs+30*60_000)*42/100, 12*hourMs+30*60_000, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'dev')`, u.ID, books[1].ID, 12*hourMs+30*60_000, 12*hourMs+30*60_000, now, now, now); err != nil {
		t.Fatal(err)
	}
	body := shelfViewBody(a, "/libraries?view=shelf", nil)
	for _, want := range []string{`aria-label="Started by Imogen Hale, 12 h 30 m, 42 % through"`, `aria-label="Done by Imogen Hale, 12 h 30 m, finished"`, "ts-spine__ribbon", "ts-spine__done"} {
		if !strings.Contains(body, want) {
			t.Errorf("shelf lacks %q", want)
		}
	}
	if home := shelfViewBody(a, "/", nil); !strings.Contains(home, `aria-label="Started by Imogen Hale, 12 h 30 m, 42 % through"`) {
		t.Error("Home's New on the shelf carries no progress")
	}
}

func TestShelfViewLargeText(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 5)
	textsizeSet(t, a, "large")
	u, _, _ := a.srv.users.ByUsername(context.Background(), "ana")
	if err := a.srv.users.SaveLibraryView(context.Background(), u.ID, "shelf"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/libraries?view=shelf", "/libraries"} {
		rec := browseGet(a, path, false, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || strings.Contains(body, "ts-spine") && strings.Contains(body, `class="ts-spine"`) || !strings.Contains(body, "ts-grid") ||
			strings.Contains(body, "Shelf</span>") || strings.Contains(html.UnescapeString(body), "view=shelf") {
			t.Errorf("%s under large text: %d, shelf still offered or drawn", path, rec.Code)
		}
	}
	if home := browseGet(a, "/", false, nil).Body.String(); strings.Contains(home, `class="ts-spine"`) {
		t.Error("Home draws spines under large text")
	}
}

func TestShelfViewGrants(t *testing.T) {
	w := sharedWorldNew(t)
	w.libraryGrant(w.mayaID)
	if b := shelfViewBody(w.a, "/libraries?view=shelf", w.maya); !strings.Contains(b, "Book 001") {
		t.Error("a member with a grant sees nothing on the shelf")
	}
	if b := shelfViewBody(w.a, "/libraries?view=shelf", w.ro); strings.Contains(b, "Book 00") {
		t.Error("a guest without a grant sees titles on the shelf")
	}
	anon := w.a.do(http.MethodGet, "/libraries?view=shelf", nil, nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next=%2Flibraries%3Fview%3Dshelf" {
		t.Errorf("anonymous: %d %q", anon.Code, anon.Header().Get("Location"))
	}
}

// The view a person picks is theirs: saved by a post, used by a bare address, beaten by an explicit one.
func TestShelfViewSaved(t *testing.T) {
	w := sharedWorldNew(t)
	w.libraryGrant(w.mayaID)
	a := w.a

	post := func(ck *http.Cookie, form url.Values) *httptest.ResponseRecorder {
		return a.adminPostAs(t, ck, "/me/library-view", form)
	}
	rec := post(w.lena, url.Values{"view": {"shelf"}, "next": {"/libraries?sort=title&view=list&cursor=zz"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/libraries?sort=title" {
		t.Fatalf("post: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Lena's bare address is the shelf; an explicit view wins; its links keep it.
	b := shelfViewBody(a, "/libraries?sort=title", w.lena)
	if !strings.Contains(b, "ts-shelf--wrap") || !strings.Contains(b, `name="view" value="shelf" aria-current="true"`) {
		t.Error("the saved shelf does not open")
	}
	if strings.Contains(b, "view=shelf") && !strings.Contains(b, `value="shelf"`) {
		t.Error("links repeat the saved view")
	}
	if g := shelfViewBody(a, "/libraries?view=grid", w.lena); strings.Contains(g, "ts-shelf--wrap") || !strings.Contains(g, "ts-grid") {
		t.Error("an explicit view=grid does not win")
	}
	if l := shelfViewBody(a, "/libraries?view=list", w.lena); !strings.Contains(l, "ts-lib-rows") {
		t.Error("an explicit view=list does not win")
	}
	// Maya has her own: the grid.
	if m := shelfViewBody(a, "/libraries", w.maya); strings.Contains(m, "ts-shelf--wrap") {
		t.Error("another person's choice leaks")
	}
	post(w.maya, url.Values{"view": {"list"}})
	if m := shelfViewBody(a, "/libraries", w.maya); !strings.Contains(m, "ts-lib-rows") {
		t.Error("Maya's list is not kept")
	}
	if l := shelfViewBody(a, "/libraries", w.lena); !strings.Contains(l, "ts-shelf--wrap") {
		t.Error("Maya's choice changed Lena's")
	}
	// Bad input saves nothing; a post with no token is refused; the redirect stays on the site.
	if rec := post(w.maya, url.Values{"view": {"cover"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad view: %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/me/library-view", url.Values{"view": {"grid"}}, nil, w.maya); rec.Code == http.StatusSeeOther {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := post(w.maya, url.Values{"view": {"grid"}, "next": {"https://evil.example/libraries"}}); rec.Header().Get("Location") != "/libraries" {
		t.Errorf("foreign next: %q", rec.Header().Get("Location"))
	}
	if rec := post(w.maya, url.Values{"view": {"grid"}, "next": {"/admin/users"}}); rec.Header().Get("Location") != "/libraries" {
		t.Errorf("other page next: %q", rec.Header().Get("Location"))
	}
	anon := a.do(http.MethodPost, "/me/library-view", url.Values{"view": {"list"}}, nil)
	if anon.Code == http.StatusSeeOther && anon.Header().Get("Location") == "/libraries" {
		t.Error("anonymous post saved a view")
	}
}
