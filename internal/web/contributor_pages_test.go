package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

func (c *contributorApp) post(t *testing.T, path string, form url.Values, ck *http.Cookie, token bool) int {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if token {
		form.Set("_csrf", c.a.tokenFor(t, ck))
	}
	return c.a.do(http.MethodPost, path, form, nil, ck).Code
}

// TestContributorEditRoutes: the details, look-up and cover routes answer a contributor on their own book and
// 404 on another person's book and on a scanned book (decision 0029, covered by share.EditBook).
func TestContributorEditRoutes(t *testing.T) {
	c := newContributorApp(t)
	a := c.a
	cn := &compareNet{}
	svc := metadata.New(a.lib, a.runner, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cn.client = online.New(online.Options{Cache: metadata.NewOnlineCache(a.st), CoverDir: t.TempDir(), Transport: cn})
	svc.SetOnline(cn.client, media.Tools{}, t.TempDir())
	if err := svc.Register(a.runner); err != nil {
		t.Fatal(err)
	}
	a.srv.details = svc
	if err := a.runner.Register(jobs.Kind{Name: mediajobs.KindCoverAdopt, Concurrency: 1, MaxAttempts: 2,
		Handler: func(context.Context, *jobs.Task) (any, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	a.srv.covers = mediajobs.NewCovers(a.lib, media.Tools{}, a.dir, a.runner, nil)

	own := c.addedBook(t, c.folder, "Kestrel Own", c.id[c.c1])
	other := c.addedBook(t, c.folder, "Kestrel Other", c.id[c.cAll])
	plain := c.addedBook(t, c.folder, "Kestrel Plain", "")
	get := func(path string) int { return a.do(http.MethodGet, path, nil, nil, c.c1).Code }

	// The sheets: own book 200 (every one is on the details tab), another person's book 200 only as a read-only page.
	for _, sheet := range []string{"", "?sheet=cover", "?sheet=lookup"} {
		if got := get("/books/" + own.ID + "/details" + sheet); got != http.StatusOK {
			t.Errorf("own details%s: %d", sheet, got)
		}
	}
	body := a.do(http.MethodGet, "/books/"+own.ID+"/details", nil, nil, c.c1).Body.String()
	if !strings.Contains(body, "sheet=lookup") {
		t.Error("the contributor's own details page has no look-up form")
	}
	body = a.do(http.MethodGet, "/books/"+plain.ID+"/details", nil, nil, c.c1).Body.String()
	if strings.Contains(body, "sheet=lookup") {
		t.Error("a scanned book shows the look-up form to a contributor")
	}
	// Look-up and cover routes: own book passes the gate, anything else is 404.
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/match", url.Values{"title": {"Kestrel"}}},
		{"/cover", url.Values{"cover": {"x"}}},
		{"/cover/upload", url.Values{}},
		{"/details/reset", url.Values{}},
	} {
		if got := c.post(t, "/books/"+own.ID+tc.path, tc.form, c.c1, true); got == http.StatusNotFound && tc.path != "/cover" { // an unknown cover id is 404 too
			t.Errorf("POST %s on their own book: 404", tc.path)
		}
		for name, id := range map[string]string{"another contributor's": other.ID, "a scanned": plain.ID} {
			if got := c.post(t, "/books/"+id+tc.path, tc.form, c.c1, true); got != http.StatusNotFound {
				t.Errorf("POST %s on %s book: %d, want 404", tc.path, name, got)
			}
		}
	}
	if got := c.post(t, "/books/"+own.ID+"/match", url.Values{"title": {"Kestrel"}}, c.c2, true); got != http.StatusNotFound {
		t.Errorf("another contributor looks up c1's book: %d, want 404", got)
	}
	if got := c.post(t, "/books/"+own.ID+"/match", url.Values{"title": {"Kestrel"}}, c.member, true); got != http.StatusNotFound {
		t.Errorf("a member looks up c1's book: %d, want 404", got)
	}
}

// TestContributorPickers: the invite and role pickers offer Contributor to the owner and an admin, never to a member.
func TestContributorPickers(t *testing.T) {
	c := newContributorApp(t)
	for name, ck := range map[string]*http.Cookie{"owner": c.owner, "admin": c.admin} {
		rec := c.a.do(http.MethodGet, "/share", nil, nil, ck)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Can also add books, and edit or remove the ones they add.") {
			t.Errorf("%s share page: %d, no Contributor card", name, rec.Code)
		}
	}
	if got := shareRoleChoices(users.User{Role: users.RoleMember}, ""); len(got) != 0 {
		t.Errorf("a member is offered %+v", got)
	}
	if rec := c.a.do(http.MethodGet, "/share", nil, nil, c.member); rec.Code != http.StatusNotFound {
		t.Errorf("member share page: %d", rec.Code)
	}
	// The person page of a contributor offers Contributor as the current role and shows the section.
	page := c.a.do(http.MethodGet, "/admin/users/"+c.id[c.c1], nil, nil, c.owner).Body.String()
	for _, want := range []string{"Contributor", "Books they added", "No books yet", "can add books to whole libraries shared with them, where uploads are on."} {
		if !strings.Contains(page, want) {
			t.Errorf("person page lacks %q", want)
		}
	}
}

// TestContributorRemoveAll: confirm, start, status and the refusals of "Remove all their books".
func TestContributorRemoveAll(t *testing.T) {
	c := newContributorApp(t)
	person := c.id[c.c1]
	base := "/admin/users/" + person + "/books/remove"
	// No books: back to the person.
	for _, ck := range []*http.Cookie{c.owner, c.admin} {
		if rec := c.a.do(http.MethodGet, base, nil, nil, ck); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users/"+person {
			t.Errorf("confirm with no books: %d %q", rec.Code, rec.Header().Get("Location"))
		}
		if got := c.post(t, base, nil, ck, true); got != http.StatusSeeOther {
			t.Errorf("POST with no books: %d", got)
		}
	}
	if n := c.a.count(t, `SELECT count(*) FROM jobs WHERE kind = ?`, upload.KindRemoveAll); n != 0 {
		t.Fatalf("a job was queued for a person with no books: %d", n)
	}
	c.addedBook(t, c.folder, "Marlin One", person)
	c.addedBook(t, c.folder, "Marlin Two", person)

	rec := c.a.do(http.MethodGet, base, nil, nil, c.admin)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "This removes 2 books from the library.") || !strings.Contains(body, "Remove 2 books") || !strings.Contains(body, "Keep them") {
		t.Fatalf("confirm page: %d", rec.Code)
	}
	// The person page shows the count and both buttons.
	page := c.a.do(http.MethodGet, "/admin/users/"+person, nil, nil, c.admin).Body.String()
	for _, want := range []string{"2 books", "See their books", "Remove all their books", "/libraries?added=" + person} {
		if !strings.Contains(page, want) {
			t.Errorf("person page lacks %q", want)
		}
	}
	// Refusals: member, contributor, wrong CSRF, and an admin aiming at an admin.
	for name, ck := range map[string]*http.Cookie{"member": c.member, "contributor": c.c2, "guest": c.guest} {
		if got := c.a.do(http.MethodGet, base, nil, nil, ck).Code; got != http.StatusNotFound {
			t.Errorf("%s GET: %d", name, got)
		}
		if got := c.post(t, base, nil, ck, true); got != http.StatusNotFound {
			t.Errorf("%s POST: %d", name, got)
		}
	}
	if got := c.post(t, base, nil, c.admin, false); got != http.StatusForbidden {
		t.Errorf("POST without a CSRF token: %d, want 403", got)
	}
	adminID := sweepUserID(t, c.a, c.admin2)
	if got := c.post(t, "/admin/users/"+adminID+"/books/remove", nil, c.admin, true); got != http.StatusNotFound {
		t.Errorf("admin removes an admin's books: %d, want 404", got)
	}
	if n := c.a.count(t, `SELECT count(*) FROM jobs WHERE kind = ?`, upload.KindRemoveAll); n != 0 {
		t.Fatalf("a refused request queued %d jobs", n)
	}
	// The admin starts it: 303 to ?job=, the status page and the fragment render and poll.
	rec = c.a.do(http.MethodPost, base, url.Values{"_csrf": {c.a.tokenFor(t, c.admin)}}, nil, c.admin)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, base+"?job=") {
		t.Fatalf("start: %d %q", rec.Code, loc)
	}
	jobID := strings.TrimPrefix(loc, base+"?job=")
	status := c.a.do(http.MethodGet, loc, nil, nil, c.admin)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `hx-get="/hx/jobs/`+jobID+`"`) || !strings.Contains(status.Body.String(), "Removing books") {
		t.Errorf("status page: %d", status.Code)
	}
	if got := c.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, nil, c.owner).Code; got != http.StatusOK {
		t.Errorf("owner job view: %d", got)
	}
	for name, ck := range map[string]*http.Cookie{"member": c.member, "contributor": c.c2, "guest": c.guest} {
		if got := c.a.do(http.MethodGet, "/hx/jobs/"+jobID, nil, nil, ck).Code; got != http.StatusNotFound {
			t.Errorf("%s job view: %d", name, got)
		}
		if got := c.a.do(http.MethodGet, loc, nil, nil, ck).Code; got != http.StatusNotFound {
			t.Errorf("%s status page: %d", name, got)
		}
	}
	// A job about an admin is the owner's alone: another admin sees 404.
	id, err := c.a.runner.Enqueue(context.Background(), upload.KindRemoveAll, upload.RemoveAllPayload{UserID: adminID, By: "x"}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.a.do(http.MethodGet, "/hx/jobs/"+id, nil, nil, c.admin).Code; got != http.StatusNotFound {
		t.Errorf("admin views a job about an admin: %d, want 404", got)
	}
	if got := c.a.do(http.MethodGet, "/hx/jobs/"+id, nil, nil, c.owner).Code; got != http.StatusOK {
		t.Errorf("owner views a job about an admin: %d", got)
	}
}

// TestRemoveAllView: the words of a finished, failed and running job.
func TestRemoveAllView(t *testing.T) {
	j := jobs.Job{ID: "j1", State: jobs.StateDone,
		Result: []byte(`{"removed":9,"kept":[{"book_id":"b","title":"Kept One","reason":"changed"},{"book_id":"c","title":"Kept Two","reason":"not_theirs"}]}`)}
	v := removeAllView(j, "/r")
	if v.Active || v.Done != "Removed 9 books." || v.KeptHeading != "Kept 2 books" || v.Kept[0].Reason != "Some files changed since they were added." || v.Kept[1].Reason != "Someone else added part of it." {
		t.Errorf("done view %+v", v)
	}
	if v := removeAllView(jobs.Job{ID: "j", State: jobs.StateFailed}, "/r"); v.Active || v.Failed == "" {
		t.Errorf("failed view %+v", v)
	}
	if v := removeAllView(jobs.Job{ID: "j", State: jobs.StateRunning, Progress: []byte(`{"done":3,"total":12}`)}, "/r"); !v.Active || v.Progress != "Removing books… 3 of 12" || v.Percent != 25 {
		t.Errorf("running view %+v", v)
	}
}

// TestContributorLibraryAndBookPages: the "Added by" menu, the book's "Added by" line and the contributor's Add books lead.
func TestContributorLibraryAndBookPages(t *testing.T) {
	c := newContributorApp(t)
	own := c.addedBook(t, c.folder, "Heron Own", c.id[c.c1])
	c.addedBook(t, c.folder, "Heron Other", c.id[c.cAll])
	lib := "/libraries/" + c.libID
	menu := func(ck *http.Cookie) string { return c.a.do(http.MethodGet, lib, nil, nil, ck).Body.String() }
	if b := menu(c.owner); !strings.Contains(b, "Added by") || !strings.Contains(b, "Anyone") {
		t.Error("owner has no Added by menu")
	}
	b := menu(c.c1)
	if !strings.Contains(b, "Added by") || !strings.Contains(b, ">You<") {
		t.Error("contributor has no Added by menu with You")
	}
	if strings.Contains(b, "Anyone") && !strings.Contains(b, "Added by") {
		t.Error("menu shape")
	}
	for name, ck := range map[string]*http.Cookie{"member": c.member, "guest": c.guest} {
		if strings.Contains(menu(ck), "Added by") {
			t.Errorf("%s sees an Added by menu", name)
		}
	}
	// Active chip for the owner choosing a person.
	if b := c.a.do(http.MethodGet, lib+"?added="+c.id[c.c1], nil, nil, c.owner).Body.String(); !strings.Contains(b, "Remove filter: Added by") {
		t.Error("no active Added by chip")
	}
	// The book page.
	page := func(ck *http.Cookie) string {
		return c.a.do(http.MethodGet, "/books/"+own.ID+"/details", nil, nil, ck).Body.String()
	}
	if b := page(c.owner); !strings.Contains(b, `href="/admin/users/`+c.id[c.c1]+`"`) || !strings.Contains(b, "Added by") {
		t.Error("owner book page lacks the Added by link")
	}
	if b := page(c.c1); !strings.Contains(b, "Added by") || !strings.Contains(b, ">you<") || strings.Contains(b, "/admin/users/") {
		t.Error("contributor book page should say Added by you")
	}
	if b := c.a.do(http.MethodGet, "/books/"+own.ID+"/details", nil, nil, c.cAll).Body.String(); strings.Contains(b, "Added by") {
		t.Errorf("a different person (404 or page) mentions Added by")
	}
	// Add books: no server path for a contributor, the path for the owner.
	add := func(ck *http.Cookie) string { return c.a.do(http.MethodGet, lib+"/add", nil, nil, ck).Body.String() }
	cb := add(c.c1)
	if strings.Contains(cb, c.folderDir) || !strings.Contains(cb, "Books you add go into a folder of your own.") {
		t.Error("contributor Add books page leaks the path or lacks the lead")
	}
	if ob := add(c.owner); !strings.Contains(ob, c.folderDir) {
		t.Error("the owner's Add books page lost the folder path")
	}
	if _, err := c.a.st.Writer().Exec(`INSERT INTO contributor_folders(folder_id, user_id, path, created_at) VALUES (?, ?, ?, 1)`,
		c.folderID, c.id[c.c1], c.folderDir+"/Added by Ana"); err != nil {
		t.Fatal(err)
	}
	cb = add(c.c1)
	if strings.Contains(cb, c.folderDir) || !strings.Contains(cb, "Books you add go into your own folder, Added by Ana.") {
		t.Error("contributor lead after the first upload")
	}
}
