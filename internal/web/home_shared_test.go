package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// sharedWorld is an app with five books and one person of each role, named as on the boards: Lena
// owns, Daniel is an admin, Maya a member and Rosa a guest. Nobody has a grant yet.
type sharedWorld struct {
	t                      *testing.T
	a                      *testApp
	books                  []library.Book
	lena, daniel, maya, ro *http.Cookie
	lenaID, danID, mayaID  string
	rosaID, libID          string
}

func sharedWorldNew(t *testing.T) *sharedWorld {
	t.Helper()
	a, f := bookApp(t)
	w := &sharedWorld{t: t, a: a, books: browseAddMany(t, a, f, 5), libID: f.LibraryID}
	w.lena, w.daniel, w.maya, w.ro = a.session, a.as(t, users.RoleAdmin), a.as(t, users.RoleMember), a.as(t, users.RoleGuest)
	w.lenaID, w.danID, w.mayaID, w.rosaID = sweepUserID(t, a, w.lena), sweepUserID(t, a, w.daniel), sweepUserID(t, a, w.maya), sweepUserID(t, a, w.ro)
	for id, name := range map[string]string{w.lenaID: "Lena", w.danID: "Daniel", w.mayaID: "Maya", w.rosaID: "Rosa"} {
		w.exec(`UPDATE users SET display_name = ? WHERE id = ?`, name, id)
	}
	return w
}

func (w *sharedWorld) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.a.st.Writer().Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

// libraryGrant opens the whole library to a person (a library grant is not a share).
func (w *sharedWorld) libraryGrant(userID string) {
	w.exec(`INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gl_' || ?, ?, 'library', ?, 1, 1)`, userID, userID, w.libID)
}

// shareShelf makes a shelf for the person with the books, in order (a pause keeps added_at apart),
// and turns sharing on through the page when share is set.
func (w *sharedWorld) shareShelf(ck *http.Cookie, name string, share bool, books ...int) string {
	w.t.Helper()
	id := shelfMake(w.t, w.a, ck, name)
	for _, i := range books {
		shelfAdd(w.t, w.a, ck, id, w.books[i].ID)
		time.Sleep(3 * time.Millisecond)
	}
	if share {
		rec := shelfPost(w.t, w.a, ck, "/shelves/"+id+"/share", url.Values{"shared": {"1"}})
		if rec.Code != http.StatusSeeOther {
			w.t.Fatalf("share %s: %d", name, rec.Code)
		}
	}
	return id
}

var sharedRowRE = regexp.MustCompile(`(?s)<section id="row-shared".*?</section>`)

// sharedRow is the Shared with you section of a page, or "".
func sharedRow(body string) string { return sharedRowRE.FindString(body) }

func TestSharedFrom(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{nil, ""}, {[]string{"Daniel"}, "from Daniel"}, {[]string{"Lena", "Daniel"}, "from Lena and Daniel"},
		{[]string{"A", "B", "C"}, "from A, B and 1 others"}, {[]string{"A", "B", "C", "D", "E"}, "from A, B and 3 others"},
	} {
		if got := sharedFrom(c.names); got != c.want {
			t.Errorf("sharedFrom(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}

func TestHomeSharedRow(t *testing.T) {
	t.Run("a member gets the row: three cards, newest first, from Daniel", func(t *testing.T) {
		w := sharedWorldNew(t)
		w.libraryGrant(w.mayaID)
		w.shareShelf(w.daniel, "Daniel picks", true, 0, 1, 2, 3)
		rec := browseGet(w.a, "/", false, w.maya)
		body := rec.Body.String()
		row := sharedRow(body)
		if rec.Code != http.StatusOK || row == "" {
			t.Fatalf("GET /: %d, no shared row", rec.Code)
		}
		for _, want := range []string{"Shared with you", "from Daniel", `href="/libraries?show=shared"`, "See all", "ts-home__cards--three", "ts-home__rowsub", "ts-home__rowsep"} {
			if !strings.Contains(row, want) {
				t.Errorf("row lacks %q", want)
			}
		}
		hrefs := regexp.MustCompile(`href="/books/([^"/]+)"`).FindAllStringSubmatch(row, -1)
		var got []string
		seen := map[string]bool{}
		for _, m := range hrefs {
			if !seen[m[1]] {
				seen[m[1]] = true
				got = append(got, m[1])
			}
		}
		want := []string{w.books[3].ID, w.books[2].ID, w.books[1].ID}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("cards = %v, want %v", got, want)
		}
		if !strings.Contains(body, "ts-home__pair") || !strings.Contains(body, `id="row-recent"`) {
			t.Error("the pair wrapper or the New row is missing")
		}
		list := browseGet(w.a, "/libraries?show=shared", false, w.maya).Body.String()
		if !strings.Contains(list, "Shared with you") {
			t.Error("the Library does not show the Shared with you chip")
		}
	})
	t.Run("two and four sharers", func(t *testing.T) {
		w := sharedWorldNew(t)
		w.libraryGrant(w.mayaID)
		w.shareShelf(w.daniel, "D", true, 0)
		w.shareShelf(w.lena, "L", true, 1)
		if row := sharedRow(browseGet(w.a, "/", false, w.maya).Body.String()); !strings.Contains(row, "from Lena and Daniel") {
			t.Errorf("two sharers: %s", row)
		}
		w.shareShelf(w.a.as(t, users.RoleAdmin), "X", true, 2)
		w.shareShelf(w.a.as(t, users.RoleAdmin), "Y", true, 3)
		row := sharedRow(browseGet(w.a, "/", false, w.maya).Body.String())
		if !regexp.MustCompile(`from [^<]+, [^<]+ and 2 others`).MatchString(row) {
			t.Errorf("four sharers: %s", row)
		}
	})
	t.Run("an owner with nothing shared has no row and no pair", func(t *testing.T) {
		w := sharedWorldNew(t)
		body := browseGet(w.a, "/", false, w.lena).Body.String()
		if strings.Contains(body, "row-shared") || strings.Contains(body, "ts-home__pair") {
			t.Error("the page draws a shared row or pair wrapper with nothing shared")
		}
	})
	t.Run("a guest with a shelf grant gets the row and no New row", func(t *testing.T) {
		w := sharedWorldNew(t)
		id := w.shareShelf(w.daniel, "For Rosa", false, 0, 1)
		w.exec(`INSERT INTO grants(id, user_id, scope, shelf_id, created_by, created_at, updated_at) VALUES ('gs1', ?, 'shelf', ?, ?, 1, 1)`, w.rosaID, id, w.danID)
		rec := browseGet(w.a, "/", false, w.ro)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || sharedRow(body) == "" || !strings.Contains(body, "from Daniel") {
			t.Fatalf("guest home: %d\n%s", rec.Code, sharedRow(body))
		}
		if strings.Contains(body, `id="row-recent"`) {
			t.Error("the New row repeats the shared row for a guest")
		}
	})
	t.Run("a guest with nothing sees the empty state", func(t *testing.T) {
		w := sharedWorldNew(t)
		w.shareShelf(w.daniel, "Not for Rosa", true, 0) // a shared flag never reaches a guest
		body := browseGet(w.a, "/", false, w.ro).Body.String()
		if !strings.Contains(body, "Nothing shared yet") || strings.Contains(body, "row-shared") {
			t.Error("guest with nothing: wrong page")
		}
	})
}

func TestHomeSharedNoLeak(t *testing.T) {
	w := sharedWorldNew(t)
	// Maya may open book 0 only; Daniel shares a shelf with books 0 and 1, Lena one with book 1 alone.
	w.exec(`INSERT INTO grants(id, user_id, scope, book_id, created_by, created_at, updated_at) VALUES ('gb1', ?, 'book', ?, ?, 1, 1)`, w.mayaID, w.books[0].ID, w.danID)
	w.exec(`UPDATE books SET title = 'Hiddenword Quill' WHERE id = ?`, w.books[1].ID)
	w.shareShelf(w.daniel, "Daniel picks", true, 0, 1)
	w.shareShelf(w.lena, "Lena secret", true, 1)
	for _, path := range []string{"/", "/libraries?show=shared", "/libraries", "/shelves"} {
		body := browseGet(w.a, path, false, w.maya).Body.String()
		for _, hidden := range []string{"Hiddenword", w.books[1].ID, "Lena secret", "Lena"} {
			if strings.Contains(body, hidden) {
				t.Errorf("%s leaked %q", path, hidden)
			}
		}
	}
	body := browseGet(w.a, "/", false, w.maya).Body.String()
	if !strings.Contains(sharedRow(body), "from Daniel") {
		t.Error("the visible share is missing")
	}
	list := browseGet(w.a, "/libraries?show=shared", false, w.maya).Body.String()
	if strings.Contains(list, "2 books") || !strings.Contains(list, "1 book") {
		t.Error("the Library count includes a book Maya cannot open")
	}
}
