package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// shelfPost sends a form as the cookie's person with a valid CSRF token.
func shelfPost(t *testing.T, a *testApp, ck *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", a.tokenFor(t, ck))
	return a.do(http.MethodPost, path, form, nil, ck)
}

// shelfCreate makes a shelf as the person and returns its id.
func shelfMake(t *testing.T, a *testApp, ck *http.Cookie, name string) string {
	t.Helper()
	rec := shelfPost(t, a, ck, "/shelves", url.Values{"name": {name}})
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/shelves/") {
		t.Fatalf("create %q: %d %q", name, rec.Code, loc)
	}
	return strings.TrimPrefix(loc, "/shelves/")
}

func shelfAdd(t *testing.T, a *testApp, ck *http.Cookie, shelf, book string) {
	t.Helper()
	rec := shelfPost(t, a, ck, "/shelves/"+shelf+"/items", url.Values{"book_id": {book}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add %s to %s: %d", book, shelf, rec.Code)
	}
}

// shelfOrder lists the book ids on a shelf page in order.
func shelfOrder(t *testing.T, a *testApp, ck *http.Cookie, shelf string) []string {
	t.Helper()
	rec := browseGet(a, "/shelves/"+shelf, false, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET shelf: %d", rec.Code)
	}
	return browseIDs(bookMain(rec.Body.String()))
}

func shelfApp(t *testing.T) (*testApp, library.Folder, []library.Book) {
	a, f := bookApp(t)
	return a, f, browseAddMany(t, a, f, 3)
}

func TestShelvesAccess(t *testing.T) {
	a, _, books := shelfApp(t)
	owner := a.session
	admin := a.as(t, users.RoleAdmin)
	member := a.as(t, users.RoleMember)
	guest := a.as(t, users.RoleGuest)
	sh := shelfMake(t, a, owner, "Mine")
	shelfAdd(t, a, owner, sh, books[0].ID)

	for name, ck := range map[string]*http.Cookie{"owner": owner, "admin": admin, "member": member} {
		if rec := browseGet(a, "/shelves", false, ck); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<h1") {
			t.Errorf("%s GET /shelves: %d", name, rec.Code)
		}
		own := shelfMake(t, a, ck, "Own of "+name)
		for _, p := range []string{"/shelves/" + own, "/shelves/" + own + "/delete"} {
			if rec := browseGet(a, p, false, ck); rec.Code != http.StatusOK {
				t.Errorf("%s GET %s: %d", name, p, rec.Code)
			}
		}
	}

	// Guest and the member on the owner's shelf: 404 on every route.
	routes := []struct{ method, path string }{
		{"GET", "/shelves"}, {"GET", "/shelves/" + sh}, {"GET", "/shelves/" + sh + "/delete"},
		{"POST", "/shelves"}, {"POST", "/shelves/" + sh + "/rename"}, {"POST", "/shelves/" + sh + "/delete"},
		{"POST", "/shelves/" + sh + "/items"}, {"POST", "/shelves/" + sh + "/items/" + books[0].ID + "/remove"},
		{"POST", "/shelves/" + sh + "/items/" + books[0].ID + "/move"},
	}
	for who, ck := range map[string]*http.Cookie{"guest": guest, "member": member, "admin": admin} {
		for _, rt := range routes {
			if rt.path == "/shelves" && who != "guest" {
				continue // their own list is fine
			}
			var rec *httptest.ResponseRecorder
			if rt.method == "GET" {
				rec = browseGet(a, rt.path, false, ck)
			} else {
				rec = shelfPost(t, a, ck, rt.path, url.Values{"name": {"x"}, "book_id": {books[0].ID}, "dir": {"up"}})
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s %s: %d, want 404", who, rt.method, rt.path, rec.Code)
			}
		}
	}
	// The owner's shelf is untouched.
	if got := shelfOrder(t, a, owner, sh); len(got) != 1 || got[0] != books[0].ID {
		t.Errorf("owner's shelf = %v", got)
	}
	if rec := browseGet(a, "/shelves/shf_unknown", false, owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown shelf: %d", rec.Code)
	}

	// Anonymous: sign in first.
	if rec := a.do(http.MethodGet, "/shelves", nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous: %d", rec.Code)
	}
	// Every POST needs the token.
	for _, rt := range routes {
		if rt.method != "POST" {
			continue
		}
		rec := a.do(http.MethodPost, rt.path, url.Values{"name": {"x"}}, nil, owner)
		if rec.Code != http.StatusForbidden {
			t.Errorf("POST %s without a token: %d, want 403", rt.path, rec.Code)
		}
	}
	if got := shelfOrder(t, a, owner, sh); len(got) != 1 {
		t.Errorf("a refused POST changed the shelf: %v", got)
	}
}

func TestShelfFlows(t *testing.T) {
	a, _, books := shelfApp(t)
	ck := a.session
	sh := shelfMake(t, a, ck, "  Sleep  ")

	if body := browseGet(a, "/shelves", false, ck).Body.String(); !strings.Contains(body, ">Sleep<") || !strings.Contains(body, "0 books") {
		t.Errorf("list page: %q", bookMain(body))
	}
	body := browseGet(a, "/shelves/"+sh, false, ck).Body.String()
	for _, want := range []string{"This shelf is empty", "Open a book and choose Add to shelf.", ">Rename<", "Delete shelf"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty shelf lacks %q", want)
		}
	}

	// Rename.
	if rec := shelfPost(t, a, ck, "/shelves/"+sh+"/rename", url.Values{"name": {"Trip"}}); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/shelves/"+sh {
		t.Errorf("rename: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := browseGet(a, "/shelves/"+sh, false, ck).Body.String(); !strings.Contains(body, "Trip") {
		t.Error("renamed shelf not shown")
	}

	// Add, in order; adding twice changes nothing.
	for _, b := range []library.Book{books[2], books[0], books[1], books[0]} {
		shelfAdd(t, a, ck, sh, b.ID)
	}
	want := []string{books[2].ID, books[0].ID, books[1].ID}
	if got := shelfOrder(t, a, ck, sh); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	page := browseGet(a, "/shelves/"+sh, false, ck).Body.String()
	if !strings.Contains(page, "3 books") || !strings.Contains(page, `aria-label="Move Book 001 up"`) || !strings.Contains(page, `aria-label="Remove Book 002 from this shelf"`) {
		t.Error("shelf page lacks the count or the button labels")
	}
	if strings.Contains(page, `aria-label="Move Book 003 up"`) || strings.Contains(page, `aria-label="Move Book 002 down"`) {
		t.Error("the first book has an up button or the last a down button")
	}

	// Move: down then up; the answer returns to the book's row.
	rec := shelfPost(t, a, ck, "/shelves/"+sh+"/items/"+books[2].ID+"/move", url.Values{"dir": {"down"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/shelves/"+sh+"#item-"+books[2].ID {
		t.Errorf("move: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := shelfOrder(t, a, ck, sh); strings.Join(got, ",") != books[0].ID+","+books[2].ID+","+books[1].ID {
		t.Errorf("after down = %v", got)
	}
	shelfPost(t, a, ck, "/shelves/"+sh+"/items/"+books[1].ID+"/move", url.Values{"dir": {"up"}})
	if got := shelfOrder(t, a, ck, sh); strings.Join(got, ",") != books[0].ID+","+books[1].ID+","+books[2].ID {
		t.Errorf("after up = %v", got)
	}
	if rec := shelfPost(t, a, ck, "/shelves/"+sh+"/items/"+books[1].ID+"/move", url.Values{"dir": {"sideways"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad dir: %d, want 400", rec.Code)
	}
	if rec := shelfPost(t, a, ck, "/shelves/"+sh+"/items/bk_nope/move", url.Values{"dir": {"up"}}); rec.Code != http.StatusNotFound {
		t.Errorf("move a book that is not on the shelf: %d, want 404", rec.Code)
	}

	// Remove.
	if rec := shelfPost(t, a, ck, "/shelves/"+sh+"/items/"+books[1].ID+"/remove", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/shelves/"+sh {
		t.Errorf("remove: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := shelfOrder(t, a, ck, sh); len(got) != 2 {
		t.Errorf("after remove = %v", got)
	}

	// Names: empty, too long, a duplicate (ignoring case) all answer 422 with the message.
	for _, c := range []struct{ name, msg string }{
		{"   ", "Give the shelf a name."}, {strings.Repeat("a", 81), "Use 80 characters or fewer."}, {"trip", "You already have a shelf with that name."},
	} {
		rec := shelfPost(t, a, ck, "/shelves", url.Values{"name": {c.name}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("create %q: %d, want 422 with %q", c.name, rec.Code, c.msg)
		}
	}
	shelfMake(t, a, ck, "Other")
	for _, c := range []struct{ name, msg string }{
		{"   ", "Give the shelf a name."}, {strings.Repeat("a", 81), "Use 80 characters or fewer."}, {"OTHER", "You already have a shelf with that name."},
	} {
		rec := shelfPost(t, a, ck, "/shelves/"+sh+"/rename", url.Values{"name": {c.name}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("rename %q: %d, want 422 with %q", c.name, rec.Code, c.msg)
		}
	}

	// Paging: a bad cursor is 400.
	if rec := browseGet(a, "/shelves/"+sh+"?cursor=bad", false, ck); rec.Code != http.StatusBadRequest {
		t.Errorf("bad cursor: %d", rec.Code)
	}

	// Delete: confirm page, then gone. The books stay.
	conf := browseGet(a, "/shelves/"+sh+"/delete", false, ck)
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), "Delete Trip?") || !strings.Contains(conf.Body.String(), "The books stay in your library.") || !strings.Contains(conf.Body.String(), "Keep it") {
		t.Errorf("confirm page: %d", conf.Code)
	}
	if rec := shelfPost(t, a, ck, "/shelves/"+sh+"/delete", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/shelves" {
		t.Errorf("delete: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := browseGet(a, "/shelves/"+sh, false, ck); rec.Code != http.StatusNotFound {
		t.Errorf("deleted shelf: %d", rec.Code)
	}
	if _, err := a.lib.Book(context.Background(), books[0].ID); err != nil {
		t.Errorf("the book went with the shelf: %v", err)
	}
}

func TestShelfSheet(t *testing.T) {
	a, _, books := shelfApp(t)
	ck := a.session
	one := shelfMake(t, a, ck, "Sleep")
	two := shelfMake(t, a, ck, "Trip")
	shelfAdd(t, a, ck, two, books[0].ID)

	page := browseGet(a, "/books/"+books[0].ID, false, ck).Body.String()
	if strings.Count(page, ">Add to shelf<") != 1 || !strings.Contains(page, `href="/books/`+books[0].ID+`?sheet=shelves"`) || !strings.Contains(page, `hx-target="#sheet"`) {
		t.Error("the book page has no single Add to shelf button")
	}
	if strings.Contains(bookMain(page), "ts-shelf-sheet") {
		t.Error("the sheet is open without ?sheet=shelves")
	}
	if !strings.Contains(page, "pages-shelves") {
		t.Error("the book page does not link the shelves stylesheet")
	}

	sheet := browseGet(a, "/books/"+books[0].ID+"?sheet=shelves", false, ck).Body.String()
	pressed := regexp.MustCompile(`aria-pressed="(true|false)"[^>]*>\s*<span class="ts-shelf-sheet__name[^"]*">([^<]*)<`).FindAllStringSubmatch(sheet, -1)
	got := map[string]string{}
	for _, m := range pressed {
		got[m[2]] = m[1]
	}
	if got["Sleep"] != "false" || got["Trip"] != "true" || len(got) != 2 {
		t.Fatalf("sheet toggles = %v", got)
	}
	if !strings.Contains(sheet, `action="/shelves/`+two+`/items/`+books[0].ID+`/remove"`) || !strings.Contains(sheet, `action="/shelves/`+one+`/items"`) || !strings.Contains(sheet, "Create and add") {
		t.Error("sheet forms do not post to add and remove")
	}

	// Add through the sheet returns to the sheet with the new state.
	rec := shelfPost(t, a, ck, "/shelves/"+one+"/items", url.Values{"book_id": {books[0].ID}, "next": {"/books/" + books[0].ID + "?sheet=shelves"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/books/"+books[0].ID+"?sheet=shelves" {
		t.Fatalf("add via sheet: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := browseGet(a, rec.Header().Get("Location"), false, ck).Body.String(); strings.Count(body, `aria-pressed="true"`) != 2 {
		t.Error("the sheet does not show both shelves as on")
	}

	// A new shelf from the sheet: created, the book added, back to the sheet. A duplicate stays in the sheet.
	rec = shelfPost(t, a, ck, "/shelves", url.Values{"name": {"Fresh"}, "book_id": {books[1].ID}, "next": {"/books/" + books[1].ID + "?sheet=shelves"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/books/"+books[1].ID+"?sheet=shelves" {
		t.Fatalf("create and add: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = shelfPost(t, a, ck, "/shelves", url.Values{"name": {"fresh"}, "book_id": {books[1].ID}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "ts-shelf-sheet") || !strings.Contains(rec.Body.String(), "You already have a shelf with that name.") {
		t.Errorf("duplicate from the sheet: %d", rec.Code)
	}
	// A remove via the sheet.
	shelfPost(t, a, ck, "/shelves/"+one+"/items/"+books[0].ID+"/remove", url.Values{"next": {"/books/" + books[0].ID + "?sheet=shelves"}})
	if got := shelfOrder(t, a, ck, one); len(got) != 0 {
		t.Errorf("after remove via the sheet = %v", got)
	}

	// An unknown book, or one the person cannot open, is 404 and is not added.
	if rec := shelfPost(t, a, ck, "/shelves/"+one+"/items", url.Values{"book_id": {"bk_unknown"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown book: %d", rec.Code)
	}
	if rec := shelfPost(t, a, ck, "/shelves", url.Values{"name": {"Ghost"}, "book_id": {"bk_unknown"}}); rec.Code != http.StatusNotFound {
		t.Errorf("create with an unknown book: %d", rec.Code)
	}
	if body := browseGet(a, "/shelves", false, ck).Body.String(); strings.Contains(body, "Ghost") {
		t.Error("a shelf was created for an unknown book")
	}
	member := a.as(t, users.RoleMember)
	msh := shelfMake(t, a, member, "Member shelf")
	if rec := shelfPost(t, a, member, "/shelves/"+msh+"/items", url.Values{"book_id": {books[0].ID}}); rec.Code != http.StatusNotFound {
		t.Errorf("a book the member cannot open: %d, want 404", rec.Code)
	}
	if rec := browseGet(a, "/books/"+books[0].ID+"?sheet=shelves", false, member); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Add to shelf") {
		t.Errorf("member on a book they cannot open: %d", rec.Code)
	}
	guest := a.as(t, users.RoleGuest)
	if rec := browseGet(a, "/books/"+books[0].ID+"?sheet=shelves", false, guest); strings.Contains(rec.Body.String(), "Add to shelf") || strings.Contains(rec.Body.String(), "ts-shelf-sheet") {
		t.Error("a guest sees the shelf button or sheet")
	}
}

func TestSidebarShelves(t *testing.T) {
	a, f, books := shelfApp(t)
	ck := a.session
	side := func(ck *http.Cookie) string {
		body := browseGet(a, "/me", false, ck).Body.String()
		i := strings.Index(body, `id="nav-side"`)
		j := strings.Index(body[i:], "</nav>")
		return body[i : i+j]
	}
	if s := side(ck); !strings.Contains(s, ">Your shelves<") || !strings.Contains(s, `href="/shelves"`) || !strings.Contains(s, "Shelves") {
		t.Errorf("a person with no shelves needs one Shelves entry: %s", s)
	}
	hidden := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Refused", Refused: library.RefusedDRM})
	sh := shelfMake(t, a, ck, "Sleep")
	shelfAdd(t, a, ck, sh, books[0].ID)
	shelfAdd(t, a, ck, sh, books[1].ID)
	shelfAdd(t, a, ck, sh, hidden.ID)
	s := side(ck)
	if !regexp.MustCompile(`href="/shelves/` + sh + `"[^>]*>[\s\S]*?Sleep[\s\S]*?ts-side__count">2<`).MatchString(s) {
		t.Errorf("sidebar does not show Sleep with 2 visible books: %s", s)
	}
	if strings.Index(s, "Your shelves") > strings.Index(s, "Your server") && strings.Contains(s, "Your server") {
		t.Error("the shelves group must come before Your server")
	}
	if rec := browseGet(a, "/shelves/"+sh, false, ck); !strings.Contains(rec.Body.String(), `aria-current="page"`) || !strings.Contains(side(ck), "Sleep") {
		t.Error("the shelf page does not mark itself")
	}
	cur := regexp.MustCompile(`href="/shelves/` + sh + `"[^>]*aria-current="page"|aria-current="page"[^>]*href="/shelves/` + sh + `"`)
	if !cur.MatchString(sideOf(browseGet(a, "/shelves/"+sh, false, ck).Body.String())) {
		t.Error("the sidebar item of the open shelf is not current")
	}

	// More than twelve: the first twelve by name, then All shelves with the total.
	for i := 0; i < 12; i++ {
		shelfMake(t, a, ck, fmt.Sprintf("Z shelf %02d", i))
	}
	s = side(ck)
	if n := strings.Count(s, `href="/shelves/shf_`); n != 12 || !strings.Contains(s, "All shelves") || !strings.Contains(s, ">13<") {
		t.Errorf("after 13 shelves: %d shelf links, sidebar %s", n, s)
	}
	if strings.Contains(s, "Z shelf 11") {
		t.Error("the 13th shelf is listed")
	}

	// Guests have no group and no entry.
	guest := a.as(t, users.RoleGuest)
	if s := side(guest); strings.Contains(s, "shelves") || strings.Contains(s, "Shelves") {
		t.Errorf("a guest sees shelves in the sidebar: %s", s)
	}
	if body := browseGet(a, "/me", false, guest).Body.String(); strings.Contains(body, "Your shelves") {
		t.Error("a guest sees the Your shelves row on Profile")
	}
	if body := browseGet(a, "/me", false, ck).Body.String(); !strings.Contains(bookMain(body), "Your shelves") || !strings.Contains(bookMain(body), "13 shelves") {
		t.Error("Profile lacks the Your shelves row")
	}
}

func sideOf(body string) string {
	i := strings.Index(body, `id="nav-side"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</nav>")
	return body[i : i+j]
}

func TestShelfNextIsSafe(t *testing.T) {
	a, _, books := shelfApp(t)
	ck := a.session
	sh := shelfMake(t, a, ck, "Safe")
	for _, next := range []string{"//evil.example", "https://evil.example/x", `/\evil`, "javascript:alert(1)", "", "/login"} {
		rec := shelfPost(t, a, ck, "/shelves/"+sh+"/items", url.Values{"book_id": {books[0].ID}, "next": {next}})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/shelves/"+sh {
			t.Errorf("next=%q: %d %q", next, rec.Code, rec.Header().Get("Location"))
		}
	}
	rec := shelfPost(t, a, ck, "/shelves", url.Values{"name": {"Second"}, "next": {"//evil.example"}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/shelves/shf_") {
		t.Errorf("create with an unsafe next: %d %q", rec.Code, loc)
	}
}

func TestShelvesNoInlineStyles(t *testing.T) {
	a, f, books := shelfApp(t)
	ck := a.session
	cover := library.Cover{BookID: books[0].ID, Source: library.CoverFolder, Mime: "image/png", Width: 800, Height: 800, SHA256: coverSHA, Path: "covers/" + coverSHA + ".jpg", Tint: "#1f4a52"}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(books[0].ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Blank"})
	sh := shelfMake(t, a, ck, "Styled")
	shelfAdd(t, a, ck, sh, books[0].ID)
	shelfAdd(t, a, ck, sh, books[1].ID)
	for _, path := range []string{
		"/shelves", "/shelves/" + sh, "/shelves/" + sh + "/delete", "/me",
		"/books/" + books[0].ID, "/books/" + books[0].ID + "?sheet=shelves",
	} {
		body := browseGet(a, path, false, ck).Body.String()
		if regexp.MustCompile(`\sstyle\s*=`).MatchString(body) {
			t.Errorf("%s has an inline style attribute", path)
		}
		if len(browseScripts.FindAllString(body, -1)) != len(browseScriptsSrc.FindAllString(body, -1)) {
			t.Errorf("%s has an inline script", path)
		}
		if strings.Contains(body, " hx-on") {
			t.Errorf("%s uses hx-on", path)
		}
	}
	// The forms carry the token as a hidden field, for use without JavaScript.
	if body := browseGet(a, "/shelves/"+sh, false, ck).Body.String(); strings.Count(body, `name="_csrf"`) < 4 {
		t.Error("shelf forms lack the hidden token")
	}
}
