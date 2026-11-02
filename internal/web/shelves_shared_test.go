package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

func TestShelfSharedView(t *testing.T) {
	w := sharedWorldNew(t)
	w.libraryGrant(w.mayaID)
	shared := w.shareShelf(w.daniel, "Daniel picks", true, 0, 1)
	private := w.shareShelf(w.daniel, "Daniel private", false, 0)
	other := w.a.as(t, users.RoleMember)
	w.libraryGrant(sweepUserID(t, w.a, other))
	memberShelf := w.shareShelf(other, "Member shared", false, 0)
	w.exec(`UPDATE shelves SET shared = 1 WHERE id = ?`, memberShelf) // the flag of a member is not effective

	rec := browseGet(w.a, "/shelves/"+shared, false, w.maya)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Shared by Daniel") || !strings.Contains(body, "Daniel picks") {
		t.Fatalf("member GET shared shelf: %d", rec.Code)
	}
	for _, banned := range []string{"/rename", "/delete", "/move", "/remove", "/share", "Rename", "Delete shelf"} {
		if strings.Contains(body, banned) {
			t.Errorf("read-only shelf carries %q", banned)
		}
	}
	for name, c := range map[string]struct {
		ck *http.Cookie
		id string
	}{
		"unshared":        {w.maya, private},
		"member shared":   {w.maya, memberShelf},
		"guest":           {w.ro, shared},
		"unknown":         {w.maya, "nope"},
		"another's guest": {w.ro, private},
	} {
		if rec := browseGet(w.a, "/shelves/"+c.id, false, c.ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	// A disabled owner stops sharing.
	w.exec(`UPDATE users SET disabled = 1 WHERE id = ?`, w.danID)
	if rec := browseGet(w.a, "/shelves/"+shared, false, w.maya); rec.Code != http.StatusNotFound {
		t.Errorf("disabled owner: %d, want 404", rec.Code)
	}
	w.exec(`UPDATE users SET disabled = 0 WHERE id = ?`, w.danID)
	if rec := browseGet(w.a, "/shelves/"+shared, false, w.maya); rec.Code != http.StatusOK {
		t.Errorf("re-enabled owner: %d, want 200", rec.Code)
	}
	// Every write to somebody else's shelf stays 404.
	for _, p := range []struct {
		path string
		form url.Values
	}{
		{"/rename", url.Values{"name": {"Stolen"}}}, {"/delete", nil}, {"/share", url.Values{"shared": {"0"}}},
		{"/items", url.Values{"book_id": {w.books[2].ID}}}, {"/items/" + w.books[0].ID + "/remove", nil},
		{"/items/" + w.books[0].ID + "/move", url.Values{"dir": {"down"}}},
	} {
		if rec := shelfPost(t, w.a, w.maya, "/shelves/"+shared+p.path, p.form); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", p.path, rec.Code)
		}
	}
	// The flag survived all of that.
	var n int
	if err := w.a.st.Reader().QueryRow(`SELECT shared FROM shelves WHERE id = ?`, shared).Scan(&n); err != nil || n != 1 {
		t.Errorf("flag = %d, %v", n, err)
	}
}

func TestShelfShareToggle(t *testing.T) {
	w := sharedWorldNew(t)
	flag := func(id string) int {
		var n int
		if err := w.a.st.Reader().QueryRow(`SELECT shared FROM shelves WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	audits := func(id string) int {
		var n int
		if err := w.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'shelf.share' AND target = ?`, "shelf:"+id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for name, ck := range map[string]*http.Cookie{"owner": w.lena, "admin": w.daniel} {
		id := shelfMake(t, w.a, ck, "Mine "+name)
		page := browseGet(w.a, "/shelves/"+id, false, ck).Body.String()
		if !strings.Contains(page, `action="/shelves/`+id+`/share"`) || !strings.Contains(page, "Share this shelf") ||
			!strings.Contains(page, "Only you can see this shelf.") {
			t.Errorf("%s: the share form is missing", name)
		}
		if rec := shelfPost(t, w.a, ck, "/shelves/"+id+"/share", url.Values{"shared": {"1"}}); rec.Code != http.StatusSeeOther || flag(id) != 1 || audits(id) != 1 {
			t.Errorf("%s share: %d flag %d audits %d", name, rec.Code, flag(id), audits(id))
		}
		page = browseGet(w.a, "/shelves/"+id, false, ck).Body.String()
		if !strings.Contains(page, "Stop sharing") || !strings.Contains(page, "only the books they already have") {
			t.Errorf("%s: no Stop sharing after sharing", name)
		}
		if rec := shelfPost(t, w.a, ck, "/shelves/"+id+"/share", url.Values{"shared": {"0"}}); rec.Code != http.StatusSeeOther || flag(id) != 0 || audits(id) != 2 {
			t.Errorf("%s stop: %d flag %d audits %d", name, rec.Code, flag(id), audits(id))
		}
		if rec := shelfPost(t, w.a, ck, "/shelves/"+id+"/share", url.Values{"shared": {"x"}}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s bad value: %d, want 400", name, rec.Code)
		}
		if rec := w.a.do(http.MethodPost, "/shelves/"+id+"/share", url.Values{"shared": {"1"}}, nil, ck); rec.Code != http.StatusForbidden || flag(id) != 0 {
			t.Errorf("%s without a token: %d", name, rec.Code)
		}
	}
	// A member has no share form on their own shelf and cannot post one.
	own := shelfMake(t, w.a, w.maya, "Maya's")
	if page := browseGet(w.a, "/shelves/"+own, false, w.maya).Body.String(); strings.Contains(page, "/share") {
		t.Error("a member sees the share form")
	}
	if rec := shelfPost(t, w.a, w.maya, "/shelves/"+own+"/share", url.Values{"shared": {"1"}}); rec.Code != http.StatusNotFound || flag(own) != 0 {
		t.Errorf("member share: %d", rec.Code)
	}
	// Another person's shelf, even for an admin, is 404 and unchanged.
	theirs := shelfMake(t, w.a, w.lena, "Lena's")
	if rec := shelfPost(t, w.a, w.daniel, "/shelves/"+theirs+"/share", url.Values{"shared": {"1"}}); rec.Code != http.StatusNotFound || flag(theirs) != 0 {
		t.Errorf("another's shelf: %d", rec.Code)
	}
	if rec := shelfPost(t, w.a, w.ro, "/shelves/"+theirs+"/share", url.Values{"shared": {"1"}}); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d", rec.Code)
	}
}

func TestShelvesSharedSection(t *testing.T) {
	w := sharedWorldNew(t)
	w.libraryGrant(w.mayaID)
	// Nothing shared: no section at all.
	if body := browseGet(w.a, "/shelves", false, w.maya).Body.String(); strings.Contains(body, "ts-shelves-shared") {
		t.Error("an empty shared section is drawn")
	}
	w.shareShelf(w.daniel, "Daniel picks", true, 0, 1, 2)
	w.shareShelf(w.daniel, "Private", false, 0)
	rec := browseGet(w.a, "/shelves", false, w.maya)
	body := rec.Body.String()
	for _, want := range []string{"ts-shelves-shared", "Shared with you", "Daniel picks", "Shared by Daniel · 3 books"} {
		if !strings.Contains(body, want) {
			t.Errorf("shelves page lacks %q", want)
		}
	}
	if strings.Contains(body, "Private") {
		t.Error("an unshared shelf is listed")
	}
	// A shared shelf holding nothing Maya may open is not listed or counted.
	w.exec(`DELETE FROM grants WHERE user_id = ?`, w.mayaID)
	w.exec(`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gb', ?, 'book', ?, 1, 1)`, w.mayaID, w.books[3].ID)
	if body := browseGet(w.a, "/shelves", false, w.maya).Body.String(); strings.Contains(body, "Daniel picks") {
		t.Error("a shelf with no visible book is listed")
	}
	// The owner sees staff shelves too; the guest has no shelves page.
	if body := browseGet(w.a, "/shelves", false, w.lena).Body.String(); !strings.Contains(body, "Shared by Daniel · 3 books") {
		t.Error("the owner does not see the admin's shared shelf")
	}
	if rec := browseGet(w.a, "/shelves", false, w.ro); rec.Code != http.StatusNotFound {
		t.Errorf("guest /shelves: %d", rec.Code)
	}
}
