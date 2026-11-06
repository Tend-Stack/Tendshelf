package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestDeskDropOnlyWhenAllowed: the desktop bar marks itself data-ts-can-add only for a person who may add
// books (owner or admin) and only when a library takes uploads; a member, a guest, a browser and an app
// whose uploads are off get no drop zone link, and the words they see on a drop say why.
func TestDeskDropOnlyWhenAllowed(t *testing.T) {
	u := newUploadApp(t)
	page := func(ck *http.Cookie, shell string) string {
		var cks []*http.Cookie
		if shell != "" {
			cks = append(cks, shellCk(shell))
		}
		cks = append(cks, ck)
		return u.a.do(http.MethodGet, "/", nil, nil, cks...).Body.String()
	}
	add := `href="/libraries/` + u.libID + `/add"`
	for name, tc := range map[string]struct {
		ck   *http.Cookie
		want bool
	}{"owner": {u.owner, true}, "admin": {u.admin, true}, "member": {u.member, false}, "guest": {u.guest, false}} {
		got := page(tc.ck, "desktop")
		if has := strings.Contains(got, `data-ts-can-add="true"`); has != tc.want {
			t.Errorf("%s: data-ts-can-add present = %v, want %v", name, has, tc.want)
		}
		if has := strings.Contains(got, `aria-label="Add books"`); has != tc.want {
			t.Errorf("%s: Add books button present = %v, want %v", name, has, tc.want)
		}
		if has := strings.Contains(got, "data-wbar-add") && strings.Contains(got, add); has != tc.want {
			t.Errorf("%s: add link present = %v, want %v", name, has, tc.want)
		}
	}
	// The words for a person who cannot add depend on who they are.
	if got := page(u.member, "desktop"); !strings.Contains(got, "Only the owner or an admin can add books.") {
		t.Error("member lacks the owner-or-admin words")
	}
	// A browser (no desktop cookie) sees none of it, even as the owner.
	if got := page(u.owner, ""); strings.Contains(got, "can-add") || strings.Contains(got, "wbar-drop") || strings.Contains(got, "data-wbar-add") {
		t.Error("a browser page carries the drop zone")
	}
	// Uploads off: nobody gets the zone; the owner is told where to turn them on.
	if err := u.a.lib.SetUploadFolder(upCtx, u.libID, ""); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"owner": u.owner, "admin": u.admin} {
		got := page(ck, "desktop")
		if strings.Contains(got, "can-add") || strings.Contains(got, "data-wbar-add") {
			t.Errorf("%s: drop zone with uploads off", name)
		}
		if !strings.Contains(got, "Uploads are off") || !strings.Contains(got, `href="/admin/libraries"`) {
			t.Errorf("%s: no words for uploads off", name)
		}
	}
	// The routes stay closed whatever the bar says: a member cannot add through the page or the manifest.
	if rec := u.a.do(http.MethodGet, "/libraries/"+u.libID+"/add", nil, nil, u.member); rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("member Add books page: %d", rec.Code)
	}
}
