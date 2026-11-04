package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

func TestAddBooksPage(t *testing.T) {
	u := newUploadApp(t)
	page := "/libraries/" + u.libID + "/add"
	for name, ck := range map[string]*http.Cookie{"owner": u.owner, "admin": u.admin} {
		rec := u.a.do(http.MethodGet, page, nil, nil, ck)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		for _, want := range []string{"Add books", u.folderDir, "Choose files", "Choose a folder", "data-ts-module=", "/static/js/upload.",
			`data-ts-library="` + u.libID + `"`, "Copy-protected files are refused.", "data-ts-msg-progress=", "<template data-ts-row>"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page lacks %q", name, want)
			}
		}
		if strings.Contains(body, ` style="`) || strings.Contains(body, "hx-on") || strings.Contains(body, "<script>") {
			t.Errorf("%s: inline script, style or hx-on", name)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": u.member, "guest": u.guest} {
		if rec := u.a.do(http.MethodGet, page, nil, nil, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, rec.Code)
		}
	}
	if rec := u.a.do(http.MethodGet, "/libraries/nope/add", nil, nil, u.owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
	// A library without an upload folder has no Add books page, for anybody.
	l2, _ := librarytest.NewLibrary(t, u.a.lib, "No uploads", t.TempDir())
	if rec := u.a.do(http.MethodGet, "/libraries/"+l2.ID+"/add", nil, nil, u.owner); rec.Code != http.StatusNotFound {
		t.Errorf("library without uploads: %d", rec.Code)
	}
	// The module is loaded by this page only: no other page names it.
	for _, p := range []string{"/", "/libraries", "/libraries/" + u.libID, "/admin/libraries"} {
		if body := u.a.do(http.MethodGet, p, nil, nil, u.owner).Body.String(); strings.Contains(body, "/static/js/upload.") {
			t.Errorf("%s names upload.js", p)
		}
	}
}

func TestAddBooksQueueAndRow(t *testing.T) {
	u := newUploadApp(t)
	v, rec := u.manifest(t, u.owner,
		upload.FileIn{Path: "Book/one.mp3", Size: 2000}, upload.FileIn{Path: "Book/cover.jpg", Size: 10}, upload.FileIn{Path: "Book/.DS_Store", Size: 5})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rowURL := "/hx/uploads/" + v.ID
	row := u.a.do(http.MethodGet, rowURL, nil, nil, u.owner)
	body := row.Body.String()
	if row.Code != http.StatusOK || !strings.Contains(body, "Stopped at 0 %. Choose the same files again to carry on.") ||
		!strings.Contains(body, "Choose files again") || !strings.Contains(body, "Cancel upload") || !strings.Contains(body, `role="status"`) ||
		!strings.Contains(body, "2 files") {
		t.Errorf("receiving row: %d %s", row.Code, body)
	}
	if page := u.a.do(http.MethodGet, "/libraries/"+u.libID+"/add", nil, nil, u.owner).Body.String(); !strings.Contains(page, `id="up-`+v.ID+`"`) {
		t.Error("the page does not list the person's upload")
	}
	// Somebody else's upload is not found, not even by an admin; nor is one through the other roles.
	for name, ck := range map[string]*http.Cookie{"admin": u.admin, "admin2": u.admin2, "member": u.member, "guest": u.guest} {
		if rec := u.a.do(http.MethodGet, rowURL, nil, nil, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: row status %d, want 404", name, rec.Code)
		}
		if page := u.a.do(http.MethodGet, "/libraries/"+u.libID+"/add", nil, nil, ck).Body.String(); strings.Contains(page, v.ID) {
			t.Errorf("%s: the page shows another person's upload", name)
		}
	}
}

// Each row state shows its own words, an icon and the buttons that belong to it.
func TestUploadRowStates(t *testing.T) {
	u := newUploadApp(t)
	files := []upload.FileState{{ID: "f1", Path: "Salt/one.flac", Size: 1000, Offset: 460}, {ID: "f2", Path: "Salt/two.flac", Size: 1000}}
	cases := []struct {
		name    string
		view    upload.View
		state   string
		want    []string
		without []string
	}{
		{"receiving", upload.View{ID: "u1", State: upload.StateReceiving, Label: "Salt Road", Files: files}, "stopped",
			[]string{"Stopped at 23 %. Choose the same files again to carry on.", "Choose files again", "Cancel upload", "2 files · flac"}, []string{"Open book", "hx-get"}},
		{"queued", upload.View{ID: "u2", State: upload.StateQueued, Label: "Salt Road", Files: files}, "checking",
			[]string{"Checking the files", `hx-get="/hx/uploads/u2"`, `hx-trigger="every 2s"`}, []string{"Choose files again"}},
		{"placing", upload.View{ID: "u3", State: upload.StatePlacing, Label: "Salt Road", Files: files}, "placing",
			[]string{"Putting it in /lib/folder", `hx-trigger="every 2s"`}, nil},
		{"adding", upload.View{ID: "u4", State: upload.StatePlaced, Label: "Salt Road", BookPath: "/lib/folder/A/B", Files: files}, "adding",
			[]string{"Adding it to your library", `hx-trigger="every 2s"`}, []string{"Open book"}},
		{"refused", upload.View{ID: "u5", State: upload.StateRefused, Label: "The Glass Orchard", Refused: "Glass/The Glass Orchard.aax", Files: files}, "refused",
			[]string{"Not added: The Glass Orchard", "The Glass Orchard.aax is copy-protected, so Tendshelf will not open it. Nothing was kept."}, []string{"hx-get"}},
		{"failed", upload.View{ID: "u6", State: upload.StateFailed, ErrorCode: "no_space", Label: "A Quiet Harbour", Files: files}, "failed",
			[]string{"Not added yet: A Quiet Harbour", "The server ran out of space. Free some space, then press Try again.", "Try again"}, []string{"hx-get"}},
		{"failed other", upload.View{ID: "u7", State: upload.StateFailed, ErrorCode: "place_failed", Label: "X", Files: files}, "failed",
			[]string{"Tendshelf could not finish adding this book."}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rv := u.a.srv.uploadRow(upCtx, c.view, "Audiobooks", "/lib/folder")
			if rv.State != c.state {
				t.Fatalf("state %q, want %q", rv.State, c.state)
			}
			var sb strings.Builder
			if err := components.UploadRow(rv).Render(upCtx, &sb); err != nil {
				t.Fatal(err)
			}
			out := sb.String()
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("row lacks %q:\n%s", w, out)
				}
			}
			for _, w := range c.without {
				if strings.Contains(out, w) {
					t.Errorf("row has %q", w)
				}
			}
			if !strings.Contains(out, `role="status"`) {
				t.Error("row is not a live region")
			}
		})
	}
	// A placed book that the scoped import has made is "Added to {library}" with a link to it.
	b := librarytest.AddBook(t, u.a.lib, u.folder, librarytest.BookSpec{Title: "Added Book"})
	rv := u.a.srv.uploadRow(upCtx, upload.View{ID: "u8", State: upload.StatePlaced, Label: "Added Book", Skipped: 2, BookPath: b.RootPath, Files: files}, "Audiobooks", u.folderDir)
	var sb strings.Builder
	if err := components.UploadRow(rv).Render(upCtx, &sb); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Added to Audiobooks", "Open book", `href="/books/` + b.ID + `"`, "2 other files were left out."} {
		if !strings.Contains(sb.String(), w) {
			t.Errorf("added row lacks %q:\n%s", w, sb.String())
		}
	}
}

func TestAdminUploadsSection(t *testing.T) {
	u := newUploadApp(t)
	owner := u.a.do(http.MethodGet, "/admin/libraries", nil, nil, u.owner).Body.String()
	for _, want := range []string{`id="uploads-h"`, "Adding books from this device", `name="folder_id"`, "Nowhere (off)", u.folderDir, "Where new books go", "Save",
		`href="/libraries/` + u.libID + `/add"`} {
		if !strings.Contains(owner, want) {
			t.Errorf("owner section lacks %q", want)
		}
	}
	admin := u.a.do(http.MethodGet, "/admin/libraries", nil, nil, u.admin).Body.String()
	if !strings.Contains(admin, "Only the owner can change this.") || strings.Contains(admin, `name="folder_id"`) || !strings.Contains(admin, u.folderDir) {
		t.Error("an admin sees the choice read-only")
	}
	// A refused save puts the plain words under that library's choice.
	rec := u.a.do(http.MethodPost, "/admin/libraries/"+u.libID+"/uploads", url.Values{"folder_id": {"fld_unknown"}}, func(r *http.Request) { r.Header.Set(csrfHeader, u.a.tokenFor(t, u.owner)) }, u.owner)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), "Tendshelf cannot write in this folder. Check its permissions.") {
		t.Errorf("refused save: %d", rec.Code)
	}
}

func TestLibraryHeaderAddBooks(t *testing.T) {
	u := newUploadApp(t)
	body := u.a.do(http.MethodGet, "/libraries/"+u.libID, nil, nil, u.owner).Body.String()
	if !strings.Contains(body, `href="/libraries/`+u.libID+`/add"`) {
		t.Error("the Library header has no Add books button for the owner")
	}
	if strings.Contains(u.a.do(http.MethodGet, "/libraries/"+u.libID, nil, nil, u.member).Body.String(), "/add\"") {
		t.Error("a member sees the Add books button")
	}
}
