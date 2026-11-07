package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var upCtx = context.Background()

// uploadApp is an app whose library takes uploads, with a person of each role.
type uploadApp struct {
	a                          *testApp
	libID, folderID, folderDir string
	folder                     library.Folder
	owner, admin, admin2       *http.Cookie
	member, guest              *http.Cookie
}

func newUploadApp(t *testing.T) *uploadApp {
	t.Helper()
	a := newTestApp(t, func(o *Options) {
		svc := upload.New(upload.Config{DataDir: o.DataDir, ChunkSize: 1024, MaxFile: 1 << 20, MaxBook: 4 << 20, Margin: 0},
			o.Store, o.Library, media.Tools{}, o.Jobs, nil)
		if err := svc.Register(o.Jobs); err != nil {
			t.Fatal(err)
		}
		o.Uploads = svc
	})
	u := &uploadApp{a: a}
	u.owner = a.as(t, users.RoleOwner)
	u.admin, u.admin2 = a.as(t, users.RoleAdmin), a.as(t, users.RoleAdmin)
	u.member, u.guest = a.as(t, users.RoleMember), a.as(t, users.RoleGuest)
	u.folderDir = t.TempDir()
	l, f := librarytest.NewLibrary(t, a.lib, "Uploads library", u.folderDir)
	u.libID, u.folderID, u.folder = l.ID, f.ID, f
	if err := a.lib.SetUploadFolder(upCtx, l.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	return u
}

// req sends a JSON-area request. token false leaves the CSRF header out; ck nil sends no session.
func (u *uploadApp) req(t *testing.T, method, path, ctype string, body []byte, ck *http.Cookie, token bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if ck != nil {
		r.AddCookie(ck)
		if token {
			r.Header.Set(csrfHeader, u.a.tokenFor(t, ck))
		}
	} else {
		c, tok := u.a.anon(t, "/login")
		r.AddCookie(c)
		if token {
			r.Header.Set(csrfHeader, tok)
		}
	}
	rec := httptest.NewRecorder()
	u.a.h.ServeHTTP(rec, r)
	return rec
}

func (u *uploadApp) manifest(t *testing.T, ck *http.Cookie, files ...upload.FileIn) (upload.View, *httptest.ResponseRecorder) {
	t.Helper()
	b, _ := json.Marshal(upload.Manifest{Label: "Book", Files: files})
	rec := u.req(t, http.MethodPost, "/libraries/"+u.libID+"/uploads", "application/json", b, ck, true)
	var v upload.View
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v, rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("not a JSON error: %q", rec.Body.String())
	}
	return e.Error.Code
}

func TestUploadFlowAsOwnerAndAdmin(t *testing.T) {
	u := newUploadApp(t)
	data := bytes.Repeat([]byte("abcdefgh"), 200) // 1600 bytes: chunks of 1024 and 576
	for name, ck := range map[string]*http.Cookie{"owner": u.owner, "admin": u.admin} {
		t.Run(name, func(t *testing.T) {
			v, rec := u.manifest(t, ck, upload.FileIn{Path: "B/" + name + ".mp3", Size: int64(len(data))}, upload.FileIn{Path: "B/.DS_Store", Size: 5})
			if rec.Code != http.StatusCreated || v.ID == "" || v.ChunkSize != 1024 || len(v.Files) != 1 || v.Skipped != 1 {
				t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
			}
			f := v.Files[0]
			base := "/uploads/" + v.ID + "/files/" + f.ID
			octet := "application/octet-stream"
			// A wrong offset says where the server is.
			rec = u.req(t, http.MethodPut, base+"?offset=1024", octet, data[1024:], ck, true)
			var wo struct {
				Offset *int64 `json:"offset"`
				Error  struct{ Code string }
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &wo)
			if rec.Code != http.StatusConflict || wo.Error.Code != "wrong_offset" || wo.Offset == nil || *wo.Offset != 0 {
				t.Fatalf("wrong offset: %d %s", rec.Code, rec.Body.String())
			}
			rec = u.req(t, http.MethodPut, base+"?offset=0", octet, data[:1024], ck, true)
			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"offset":1024}` {
				t.Fatalf("chunk 1: %d %s", rec.Code, rec.Body.String())
			}
			rec = u.req(t, http.MethodPut, base+"?offset=abc", octet, nil, ck, true)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("bad offset: %d", rec.Code)
			}
			rec = u.req(t, http.MethodPost, "/uploads/"+v.ID+"/finish", "", nil, ck, true)
			if rec.Code != http.StatusConflict || errCode(t, rec) != "incomplete" {
				t.Errorf("finish early: %d %s", rec.Code, rec.Body.String())
			}
			rec = u.req(t, http.MethodGet, "/uploads/"+v.ID, "", nil, ck, true)
			var got upload.View
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if rec.Code != http.StatusOK || got.State != "receiving" || got.Files[0].Offset != 1024 {
				t.Errorf("get: %d %s", rec.Code, rec.Body.String())
			}
			rec = u.req(t, http.MethodPut, base+"?offset=1024", octet+"; charset=binary", data[1024:], ck, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("chunk 2: %d %s", rec.Code, rec.Body.String())
			}
			rec = u.req(t, http.MethodPost, "/uploads/"+v.ID+"/finish", "", nil, ck, true)
			var fin struct {
				Job string `json:"job_id"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &fin)
			if rec.Code != http.StatusAccepted || fin.Job == "" {
				t.Fatalf("finish: %d %s", rec.Code, rec.Body.String())
			}
			// Cancel after finish is refused; a second upload cancels with 204.
			rec = u.req(t, http.MethodPost, "/uploads/"+v.ID+"/cancel", "", nil, ck, true)
			if rec.Code != http.StatusConflict {
				t.Errorf("cancel after finish: %d", rec.Code)
			}
			v2, _ := u.manifest(t, ck, upload.FileIn{Path: "x.mp3", Size: 10})
			rec = u.req(t, http.MethodPost, "/uploads/"+v2.ID+"/cancel", "", nil, ck, true)
			if rec.Code != http.StatusNoContent {
				t.Errorf("cancel: %d", rec.Code)
			}
		})
	}
	// Nothing was written to the library folder by any of it.
	if entries, _ := os.ReadDir(u.folderDir); len(entries) != 0 {
		t.Errorf("library folder touched: %v", entries)
	}
}

func TestUploadRoutesRefuse(t *testing.T) {
	u := newUploadApp(t)
	good, rec := u.manifest(t, u.owner, upload.FileIn{Path: "a.mp3", Size: 10})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	f := good.Files[0]
	id := good.ID
	manifest, _ := json.Marshal(upload.Manifest{Files: []upload.FileIn{{Path: "a.mp3", Size: 10}}})
	type route struct {
		name, method, path, ctype string
		body                      []byte
	}
	routes := []route{
		{"create", http.MethodPost, "/libraries/" + u.libID + "/uploads", "application/json", manifest},
		{"get", http.MethodGet, "/uploads/" + id, "", nil},
		{"chunk", http.MethodPut, "/uploads/" + id + "/files/" + f.ID + "?offset=0", "application/octet-stream", []byte("0123456789")},
		{"finish", http.MethodPost, "/uploads/" + id + "/finish", "", nil},
		{"cancel", http.MethodPost, "/uploads/" + id + "/cancel", "", nil},
	}
	contributor := u.a.as(t, users.RoleMember) // a contributor with no grant adds nothing anywhere
	if _, err := u.a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, u.a, contributor)); err != nil {
		t.Fatal(err)
	}
	// Roles that may not: a member, a guest, another admin (for the upload routes of someone else's
	// upload), a disabled account and a visitor.
	for _, who := range []struct {
		name   string
		ck     *http.Cookie
		status int
		skip   string // a route this person is allowed to reach and fails differently
	}{
		{"member", u.member, http.StatusNotFound, ""},
		{"guest", u.guest, http.StatusNotFound, ""},
		{"contributor without a grant", contributor, http.StatusNotFound, ""},
		{"another admin", u.admin2, http.StatusNotFound, "create"},
		{"visitor", nil, http.StatusUnauthorized, ""},
	} {
		for _, rt := range routes {
			if rt.name == who.skip {
				continue
			}
			rec := u.req(t, rt.method, rt.path, rt.ctype, rt.body, who.ck, true)
			if rec.Code != who.status {
				t.Errorf("%s %s: %d, want %d (%s)", who.name, rt.name, rec.Code, who.status, rec.Body.String())
			}
			if who.status == http.StatusNotFound && errCode(t, rec) != "not_found" {
				t.Errorf("%s %s: not a JSON refusal: %s", who.name, rt.name, rec.Body.String())
			}
		}
	}
	// A person turned off mid-session is signed out.
	var id2 string
	if err := u.a.st.Reader().QueryRow(`SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1`).Scan(&id2); err != nil {
		t.Fatal(err)
	}
	if _, err := u.a.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, id2); err != nil {
		t.Fatal(err)
	}
	for _, ck := range []*http.Cookie{u.admin, u.admin2} {
		r := httptest.NewRequest(http.MethodGet, "/uploads/"+id, nil)
		r.AddCookie(ck)
		rec := httptest.NewRecorder()
		u.a.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
			t.Errorf("a disabled or other admin reached an upload: %d", rec.Code)
		}
	}
	// Nothing from any refusal reached the upload.
	got, err := u.a.srv.uploads.Get(upCtx, mustUserID(t, u), id)
	if err != nil || got.Files[0].Offset != 0 || got.State != "receiving" {
		t.Errorf("upload after refusals: %+v, %v", got, err)
	}

	// Without the token every unsafe route is 403 and nothing happens, whoever asks.
	for _, rt := range routes {
		if rt.method == http.MethodGet {
			continue
		}
		for _, ck := range []*http.Cookie{u.owner, u.member, nil} {
			if rec := u.req(t, rt.method, rt.path, rt.ctype, rt.body, ck, false); rec.Code != http.StatusForbidden {
				t.Errorf("%s without a token: %d, want 403", rt.name, rec.Code)
			}
		}
	}
	// A chunk is raw bytes, never a form.
	for _, ct := range []string{"", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "text/plain"} {
		rec := u.req(t, http.MethodPut, "/uploads/"+id+"/files/"+f.ID+"?offset=0", ct, []byte("0123456789"), u.owner, true)
		if rec.Code != http.StatusUnsupportedMediaType || errCode(t, rec) != "bad_type" {
			t.Errorf("content type %q: %d %s", ct, rec.Code, rec.Body.String())
		}
	}
	// A bad manifest names the problem.
	bad, _ := json.Marshal(upload.Manifest{Files: []upload.FileIn{{Path: "../a.mp3", Size: 1}}})
	rec = u.req(t, http.MethodPost, "/libraries/"+u.libID+"/uploads", "application/json", bad, u.owner, true)
	if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "bad_path" || !strings.Contains(rec.Body.String(), "../a.mp3") {
		t.Errorf("bad manifest: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.req(t, http.MethodPost, "/libraries/"+u.libID+"/uploads", "application/json", []byte("{not json"), u.owner, true)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
}

// mustUserID is the owner's id: the uploads of the test belong to the owner.
func mustUserID(t *testing.T, u *uploadApp) string {
	t.Helper()
	var id string
	if err := u.a.st.Reader().QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUploadsOffAnswers409(t *testing.T) {
	u := newUploadApp(t)
	if err := u.a.lib.SetUploadFolder(upCtx, u.libID, ""); err != nil {
		t.Fatal(err)
	}
	_, rec := u.manifest(t, u.owner, upload.FileIn{Path: "a.mp3", Size: 10})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "uploads_off" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := u.req(t, http.MethodPost, "/libraries/lib_nope/uploads", "application/json", []byte(`{}`), u.owner, true); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
}

func TestUploadSetting(t *testing.T) {
	u := newUploadApp(t)
	post := func(ck *http.Cookie, folder string) *httptest.ResponseRecorder {
		form := "folder_id=" + folder
		r := httptest.NewRequest(http.MethodPost, "/admin/libraries/"+u.libID+"/uploads", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set(csrfHeader, u.a.tokenFor(t, ck))
		r.AddCookie(ck)
		rec := httptest.NewRecorder()
		u.a.h.ServeHTTP(rec, r)
		return rec
	}
	current := func() string {
		l, err := u.a.lib.Library(upCtx, u.libID)
		if err != nil {
			t.Fatal(err)
		}
		return l.UploadFolderID
	}
	// Only the owner turns it on or off.
	if err := u.a.lib.SetUploadFolder(upCtx, u.libID, ""); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"admin": u.admin, "member": u.member, "guest": u.guest} {
		if rec := post(ck, u.folderID); rec.Code != http.StatusNotFound || current() != "" {
			t.Errorf("%s: %d, setting %q", name, rec.Code, current())
		}
	}
	rec := post(u.owner, u.folderID)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/libraries#uploads-"+u.libID || current() != u.folderID {
		t.Fatalf("owner: %d %q setting %q", rec.Code, rec.Header().Get("Location"), current())
	}
	// The write check leaves no file behind.
	if entries, _ := os.ReadDir(u.folderDir); len(entries) != 0 {
		t.Errorf("write check left %v", entries)
	}
	var audits int
	if err := u.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'library.uploads'`).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("audits = %d, %v", audits, err)
	}
	// A folder that cannot be written, and a folder of another library, are refused with the reason.
	other, of := librarytest.NewLibrary(t, u.a.lib, "Other", t.TempDir())
	_ = other
	rec = post(u.owner, of.ID)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Tendshelf cannot write in this folder") || current() != u.folderID {
		t.Errorf("foreign folder: %d, setting %q", rec.Code, current())
	}
	if err := os.RemoveAll(u.folderDir); err != nil {
		t.Fatal(err)
	}
	rec = post(u.owner, u.folderID)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Tendshelf cannot write in this folder") {
		t.Errorf("missing folder: %d", rec.Code)
	}
	// Off.
	if rec := post(u.owner, ""); rec.Code != http.StatusSeeOther || current() != "" {
		t.Errorf("off: %d setting %q", rec.Code, current())
	}
}

func TestBookRemoveRefusals(t *testing.T) {
	u := newUploadApp(t)
	b := librarytest.AddBook(t, u.a.lib, u.folder, librarytest.BookSpec{Title: "Plain", Files: 1})
	post := func(ck *http.Cookie, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/books/"+id+"/remove", nil)
		r.Header.Set(csrfHeader, u.a.tokenFor(t, ck))
		r.AddCookie(ck)
		rec := httptest.NewRecorder()
		u.a.h.ServeHTTP(rec, r)
		return rec
	}
	contributor := u.a.as(t, users.RoleMember)
	if _, err := u.a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, u.a, contributor)); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"member": u.member, "guest": u.guest, "contributor": contributor} {
		if rec := post(ck, b.ID); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// A book Tendshelf did not add is not removed, by anyone.
	for name, ck := range map[string]*http.Cookie{"owner": u.owner, "admin": u.admin} {
		if rec := post(ck, b.ID); rec.Code != http.StatusConflict {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// An htmx request gets an out-of-band toast fragment, not a plain-text page.
	r := httptest.NewRequest(http.MethodPost, "/books/"+b.ID+"/remove", nil)
	r.Header.Set(csrfHeader, u.a.tokenFor(t, u.owner))
	r.Header.Set("HX-Request", "true")
	r.AddCookie(u.owner)
	hx := httptest.NewRecorder()
	u.a.h.ServeHTTP(hx, r)
	if hx.Code != http.StatusConflict || !strings.HasPrefix(hx.Header().Get("Content-Type"), "text/html") || !strings.Contains(hx.Body.String(), "hx-swap-oob") {
		t.Errorf("htmx refusal: %d %q %s", hx.Code, hx.Header().Get("Content-Type"), hx.Body.String())
	}
	if rec := post(u.owner, "bk_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown book: %d", rec.Code)
	}
	if n := u.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, b.ID); n != 1 {
		t.Errorf("the book was removed")
	}
}
