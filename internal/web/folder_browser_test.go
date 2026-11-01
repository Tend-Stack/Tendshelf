package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// fbTree makes a folder holding two sub-folders, an audio file whose name must never be sent and a
// text file.
func fbTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"Saltmarsh", "kids"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"secret-title.mp3", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fbBrowse(formURL, p string) string { return formURL + "?browse=" + url.QueryEscape(p) }

func TestFolderBrowserAccess(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	admin := a.as(t, users.RoleAdmin)
	member := a.as(t, users.RoleMember)
	guest := a.as(t, users.RoleGuest)
	root := fbTree(t)

	form := fbBrowse(addFolderURL, root)
	for name, ck := range map[string]*http.Cookie{"owner": owner, "admin": admin} {
		if rec := a.do(http.MethodGet, form, nil, nil, ck); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Saltmarsh") {
			t.Errorf("admin form browse as %s: %d", name, rec.Code)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": member, "guest": guest} {
		rec := a.do(http.MethodGet, form, nil, nil, ck)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Saltmarsh") {
			t.Errorf("admin form browse as %s: %d, want 404 without names", name, rec.Code)
		}
	}
	if rec := a.do(http.MethodGet, form, nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous: %d, want 303", rec.Code)
	}

	setup := fbBrowse(setupFolderURL, root)
	if rec := a.do(http.MethodGet, setup, nil, nil, owner); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Saltmarsh") {
		t.Errorf("setup browse as owner: %d", rec.Code)
	}
	for name, ck := range map[string]*http.Cookie{"admin": admin, "member": member} {
		rec := a.do(http.MethodGet, setup, nil, nil, ck)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Saltmarsh") {
			t.Errorf("setup browse as %s: %d, want 404 without names", name, rec.Code)
		}
	}
}

func TestFolderBrowserNoFileNames(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	root := fbTree(t)
	for _, form := range []string{addFolderURL, setupFolderURL} {
		body := adminHTMLUnescape(a.do(http.MethodGet, fbBrowse(form, root), nil, nil, owner).Body.String())
		for _, bad := range []string{"secret-title", "notes.txt", ".mp3"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: the page names a file (%q)", form, bad)
			}
		}
		if !strings.Contains(body, "1 audio file here") {
			t.Errorf("%s: no audio count", form)
		}
		if !strings.Contains(body, "kids") || !strings.Contains(body, "Use this folder") {
			t.Errorf("%s: no folders or pick link", form)
		}
	}
}

func TestFolderBrowserSheetMarkup(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	root := fbTree(t)
	body := adminHTMLUnescape(a.do(http.MethodGet, fbBrowse(addFolderURL, root), nil, nil, owner).Body.String())

	i := strings.Index(body, `id="sheet"`)
	j := strings.Index(body, "ts-sheet-layer")
	if i < 0 || j < i {
		t.Fatalf("sheet is not inside #sheet (%d, %d)", i, j)
	}
	if !strings.Contains(body, `role="dialog"`) || !strings.Contains(body, `aria-labelledby="folders-title"`) || !strings.Contains(body, `data-ts-close`) {
		t.Error("dialog markup is incomplete")
	}
	sub := adminHTMLUnescape(fbBrowse(addFolderURL, filepath.Join(root, "kids")))
	for _, want := range []string{`hx-get="` + sub, `hx-target="#sheet"`, `hx-select="#sheet > *"`, `hx-swap="innerHTML"`} {
		if !strings.Contains(body, want) {
			t.Errorf("sheet markup lacks %s", want)
		}
	}
	pick := `<a class="ts-btn ts-btn--primary ts-btn--lg ts-btn--block" href="` + addFolderURL + "?folder=" + url.QueryEscape(root) + `" hx-boost="false">Use this folder</a>`
	if !strings.Contains(body, pick) {
		t.Errorf("pick link missing or boosted: %s", body[strings.Index(body, "Use this folder")-200:])
	}
	// The Browse button on the plain form is a link that works without JavaScript and loads in place.
	plain := adminHTMLUnescape(a.do(http.MethodGet, addFolderURL, nil, nil, owner).Body.String())
	if !strings.Contains(plain, `href="`+addFolderURL+`?browse="`) || !strings.Contains(plain, ">Browse</a>") || strings.Contains(plain, "ts-sheet-layer") {
		t.Error("plain form lacks a Browse link or shows a sheet")
	}
}

func TestFolderBrowserSetupInline(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	root := fbTree(t)
	body := adminHTMLUnescape(a.do(http.MethodGet, fbBrowse(setupFolderURL, root), nil, nil, owner).Body.String())
	if strings.Contains(body, `id="sheet"`) || strings.Contains(body, "ts-sheet-layer") || strings.Contains(body, "hx-get") {
		t.Error("the setup frame must not use a sheet or htmx")
	}
	if !strings.Contains(body, `<section class="ts-folders ts-folders--panel"`) || !strings.Contains(body, `href="`+setupFolderURL+`"`) {
		t.Error("inline panel or its Close link is missing")
	}
	if !strings.Contains(body, `href="`+setupFolderURL+"?browse="+url.QueryEscape(filepath.Join(root, "kids"))+`"`) {
		t.Error("folder link missing")
	}
}

func TestFolderBrowserPickPrefills(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	dir := filepath.Join(fbTree(t), "kids")
	for _, form := range []string{addFolderURL, setupFolderURL} {
		body := adminHTMLUnescape(a.do(http.MethodGet, form+"?folder="+url.QueryEscape(dir), nil, nil, owner).Body.String())
		if !strings.Contains(body, `value="`+dir+`"`) {
			t.Errorf("%s: the field is not filled", form)
		}
		if strings.Contains(body, "ts-folders") && !strings.Contains(body, "ts-folderfield") {
			t.Errorf("%s: unexpected browser", form)
		}
	}
	body := adminHTMLUnescape(a.do(http.MethodGet, addFolderURL+"?folder="+url.QueryEscape(dir), nil, nil, owner).Body.String())
	if !strings.Contains(body, `value="kids"`) {
		t.Error("the new library name is not filled with the folder name")
	}
	// A relative or empty ?folder= fills nothing.
	body = a.do(http.MethodGet, addFolderURL+"?folder=..%2F..", nil, nil, owner).Body.String()
	if strings.Contains(body, `value=".."`) {
		t.Error("a relative folder was echoed into the field")
	}
}

func TestFolderBrowserBadPaths(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	root := fbTree(t)
	get := func(p string) string {
		rec := a.do(http.MethodGet, fbBrowse(addFolderURL, p), nil, nil, owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("browse %q: %d", p, rec.Code)
		}
		return adminHTMLUnescape(rec.Body.String())
	}
	for _, p := range []string{"", "relative/dir", "../..", "kids"} {
		body := get(p)
		if !strings.Contains(body, "Home folder") && !strings.Contains(body, "This computer") && !strings.Contains(body, `aria-label="Places"`) {
			t.Errorf("browse %q did not show the Places", p)
		}
		if strings.Contains(body, "Use this folder") {
			t.Errorf("browse %q offers to use a folder", p)
		}
	}
	if body := get("/no/such/folder"); !strings.Contains(body, "Tendshelf can't open that folder.") || strings.Contains(body, ">Use this folder<") {
		t.Error("a missing folder shows no message, or offers to pick it")
	}
	if body := get(filepath.Join(root, "notes.txt")); !strings.Contains(body, "Tendshelf can't open that folder.") || strings.Contains(body, ">Use this folder<") {
		t.Error("a file shows no message")
	}
	// A dirty path is cleaned: kids/../.. is the parent of root.
	body := get(filepath.Join(root, "kids") + "/../..")
	if !strings.Contains(body, `<p class="ts-folders__path ts-num">`+filepath.Dir(root)+`</p>`) {
		t.Errorf("a/../.. was not cleaned: %s", body)
	}
}

func TestFolderBrowserHidesDataDir(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	parent := filepath.Dir(a.dir)
	if err := os.Mkdir(filepath.Join(parent, "fb-sibling"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(parent, "fb-sibling")) })
	body := adminHTMLUnescape(a.do(http.MethodGet, fbBrowse(addFolderURL, parent), nil, nil, owner).Body.String())
	if !strings.Contains(body, "fb-sibling") {
		t.Fatal("the sibling folder is not listed")
	}
	if strings.Contains(body, `ts-folders__name">`+filepath.Base(a.dir)+"<") {
		t.Error("the data folder is listed")
	}
	// Reached through a symbolic link to its parent, it is still hidden.
	link := filepath.Join(t.TempDir(), "via")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	body = adminHTMLUnescape(a.do(http.MethodGet, fbBrowse(addFolderURL, link), nil, nil, owner).Body.String())
	if !strings.Contains(body, "fb-sibling") || strings.Contains(body, `ts-folders__name">`+filepath.Base(a.dir)+"<") {
		t.Error("the data folder is listed through a link")
	}
}

func TestFolderBrowserPostStillValidates(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	parent := filepath.Dir(a.dir)
	// Browsing to the data folder's parent and picking it is allowed to reach the form...
	rec := a.do(http.MethodGet, addFolderURL+"?folder="+url.QueryEscape(parent), nil, nil, owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), parent) {
		t.Fatalf("pick: %d", rec.Code)
	}
	// ...but submitting it still fails the existing checks.
	rec = a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {parent}, "library": {""}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(adminHTMLUnescape(rec.Body.String()), pathMessage("path_data")) {
		t.Errorf("submit: %d, want 422 path_data", rec.Code)
	}
	rec = a.adminPostAs(t, owner, setupFolderURL, url.Values{"folder": {parent}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("setup submit: %d, want 422", rec.Code)
	}
}
