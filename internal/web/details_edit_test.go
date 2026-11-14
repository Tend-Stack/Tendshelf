package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

type editLab struct {
	a                           *testApp
	owner, admin, member, guest *http.Cookie
	book                        library.Book
}

func newEditLab(t *testing.T) *editLab {
	t.Helper()
	a := newTestApp(t, compareMod(t, &compareNet{}))
	l := &editLab{a: a}
	l.owner, l.admin = a.owner(t), a.as(t, "admin")
	l.member, l.guest = a.as(t, "member"), a.as(t, "guest")
	_, f := librarytest.NewLibrary(t, a.lib, "Edit library", t.TempDir())
	l.book = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Edit Me", Authors: []string{"Ann Author"}, PublishedYear: 2001, Files: 1})
	// A scan always leaves the folder's title as a row; that is what clearing a hand-typed title returns to.
	if _, err := a.st.Writer().Exec(`INSERT INTO metadata_values(book_id, field, source, value_json, observed_at) VALUES (?, 'title', 'folder', '"Edit Me"', 1)`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *editLab) post(t *testing.T, ck *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("_csrf", l.a.tokenFor(t, ck))
	return l.a.do(http.MethodPost, "/books/"+l.book.ID+"/details/edit", form, nil, ck)
}

func (l *editLab) page(ck *http.Cookie, query string) string {
	return l.a.do(http.MethodGet, "/books/"+l.book.ID+"/details"+query, nil, nil, ck).Body.String()
}

func (l *editLab) userRows(t *testing.T) int {
	t.Helper()
	return l.a.count(t, `SELECT count(*) FROM metadata_values WHERE book_id = ? AND source = 'user'`, l.book.ID)
}

// full is the form as the page sends it back, with changes on top.
func (l *editLab) full(t *testing.T, change url.Values) url.Values {
	t.Helper()
	form := url.Values{"title": {"Edit Me"}, "authors": {"Ann Author"}, "year": {"2001"}}
	for k, v := range change {
		form[k] = v
	}
	return form
}

func TestEditDetailsForm(t *testing.T) {
	l := newEditLab(t)
	// Online lookups are off here, and the form is still on the page, prefilled, closed until asked for.
	tab := l.page(l.owner, "")
	for _, want := range []string{`id="edit-details"`, `value="Edit Me"`, `value="Ann Author"`, `value="2001"`, html.EscapeString(detailsEditNote)} {
		if !strings.Contains(tab, want) {
			t.Errorf("the form lacks %q", want)
		}
	}
	if strings.Contains(tab, `id="edit-details" class="ts-book__edit" open`) {
		t.Error("the form is open without being asked")
	}
	if !strings.Contains(l.page(l.owner, "?edit=1"), `id="edit-details" class="ts-book__edit" open`) {
		t.Error("?edit=1 does not open the form")
	}
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest} {
		if strings.Contains(l.page(ck, ""), "edit-details") {
			t.Errorf("%s sees the form", name)
		}
		if rec := l.post(t, ck, l.full(t, url.Values{"title": {"Nope"}})); rec.Code != http.StatusNotFound {
			t.Errorf("%s POST: %d", name, rec.Code)
		}
	}
	// The More menu opens the form.
	if body := l.a.do(http.MethodGet, "/books/"+l.book.ID, nil, nil, l.owner).Body.String(); !strings.Contains(body, "details?edit=1#edit-details") {
		t.Error("the More menu does not lead to the form")
	}
}

func TestEditDetailsSave(t *testing.T) {
	l := newEditLab(t)
	// Unchanged writes nothing.
	rec := l.post(t, l.owner, l.full(t, nil))
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "?edited=0") || l.userRows(t) != 0 {
		t.Fatalf("unchanged: %d %q rows %d", rec.Code, rec.Header().Get("Location"), l.userRows(t))
	}
	rec = l.post(t, l.admin, l.full(t, url.Values{"title": {"New Title"}, "narrators": {"Cy Voice, Di Reader"}, "series": {"The Saga"}, "series_number": {"3"}}))
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "?edited=1") {
		t.Fatalf("save: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	b, err := l.a.lib.Book(upCtx, l.book.ID)
	if err != nil || b.Title != "New Title" {
		t.Fatalf("book: %+v %v", b, err)
	}
	if n := l.a.count(t, `SELECT count(*) FROM book_people WHERE book_id = ? AND role = 'narrator'`, l.book.ID); n != 2 {
		t.Errorf("narrators = %d", n)
	}
	if n := l.a.count(t, `SELECT count(*) FROM book_series WHERE book_id = ?`, l.book.ID); n != 1 {
		t.Errorf("series rows = %d", n)
	}
	if !strings.Contains(l.page(l.owner, "?edited=1"), html.EscapeString(detailsEditSaved)) {
		t.Error("the page does not say it was saved")
	}
	// Clearing a field brings back the value the files gave.
	rec = l.post(t, l.owner, l.full(t, url.Values{"title": {""}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d", rec.Code)
	}
	if b, _ := l.a.lib.Book(upCtx, l.book.ID); b.Title != "Edit Me" {
		t.Errorf("title after clearing = %q", b.Title)
	}
}

func TestEditDetailsRefusal(t *testing.T) {
	l := newEditLab(t)
	rec := l.post(t, l.owner, l.full(t, url.Values{"title": {"Typed Title"}, "year": {"19x9"}, "isbn": {"12"}}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`value="Typed Title"`, `value="19x9"`, `value="12"`, html.EscapeString(detailsEditProblem), "The year is four digits", "The ISBN is 10 or 13 digits.", `id="edit-details" class="ts-book__edit" open`} {
		if !strings.Contains(body, want) {
			t.Errorf("the refused page lacks %q", want)
		}
	}
	if l.userRows(t) != 0 {
		t.Error("a refused form wrote rows")
	}
	if b, _ := l.a.lib.Book(upCtx, l.book.ID); b.Title != "Edit Me" {
		t.Errorf("title = %q", b.Title)
	}
}
