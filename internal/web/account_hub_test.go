package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// accountHubMenus is the head of Profile: the two menus, from the Account button to the books.
func accountHubMenus(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `id="pf-menu-account"`)
	j := strings.Index(body, `<div class="ts-profile__cols">`)
	if i < 0 || j < i {
		t.Fatalf("Profile has no Account and Settings menus (%d, %d)", i, j)
	}
	return body[i:j]
}

// accountHubContributor is a member who may add books.
func accountHubContributor(t *testing.T, a *testApp) *http.Cookie {
	t.Helper()
	ck := a.as(t, users.RoleMember)
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, a, ck)); err != nil {
		t.Fatal(err)
	}
	return ck
}

func TestAccountHubMenusForEveryone(t *testing.T) {
	a := newTestApp(t)
	for name, ck := range map[string]*http.Cookie{"owner": a.owner(t), "admin": a.as(t, users.RoleAdmin), "member": a.as(t, users.RoleMember), "guest": a.as(t, users.RoleGuest)} {
		menus := accountHubMenus(t, a.do(http.MethodGet, "/me", nil, nil, ck).Body.String())
		for _, frag := range []string{
			`href="/me/settings/playback"`, `href="/me/settings/appearance"`, `href="/me/settings/accessibility"`, `href="/me/settings/privacy"`,
			`href="/me/devices"`, `action="/logout"`, `name="_csrf"`, "Sign out", "data-ts-chipmenu",
		} {
			if !strings.Contains(menus, frag) {
				t.Errorf("%s: menus lack %q", name, frag)
			}
		}
		if !strings.Contains(menus, `href="/me/password"`) && !strings.Contains(menus, `href="/welcome"`) { // a test account has no password
			t.Errorf("%s: no way to set a password", name)
		}
		if strings.Index(menus, "pf-menu-account") > strings.Index(menus, "pf-menu-settings") {
			t.Errorf("%s: Settings comes before Account", name)
		}
		if strings.Contains(menus, `href="/me/settings"`) {
			t.Errorf("%s: the old Settings link is back", name)
		}
	}
}

func TestAccountHubServerLinks(t *testing.T) {
	a := newTestApp(t)
	cases := []struct {
		name string
		ck   *http.Cookie
		want []string
		not  []string
	}{
		{"owner", a.owner(t), []string{"Your server", "/admin/users", "/admin/libraries", "/admin/email", "/admin/backups"}, nil},
		{"admin", a.as(t, users.RoleAdmin), []string{"Your server", "/admin/users", "/admin/libraries"}, []string{"/admin/email", "/admin/backups"}},
		{"member", a.as(t, users.RoleMember), nil, []string{"Your server", "/admin/"}},
		{"contributor", accountHubContributor(t, a), nil, []string{"Your server", "/admin/"}},
		{"guest", a.as(t, users.RoleGuest), nil, []string{"Your server", "/admin/"}},
	}
	for _, c := range cases {
		menus := accountHubMenus(t, a.do(http.MethodGet, "/me", nil, nil, c.ck).Body.String())
		for _, w := range c.want {
			if !strings.Contains(menus, w) {
				t.Errorf("%s: menus lack %q", c.name, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(menus, n) {
				t.Errorf("%s: menus show %q", c.name, n)
			}
		}
	}
}

func TestAccountHubKindLabel(t *testing.T) {
	a := newTestApp(t)
	for name, c := range map[string]struct {
		ck   *http.Cookie
		kind string
	}{
		"contributor": {accountHubContributor(t, a), "Contributor"},
		"member":      {a.as(t, users.RoleMember), "Member"},
		"guest":       {a.as(t, users.RoleGuest), "Listener"},
		"admin":       {a.as(t, users.RoleAdmin), "Admin"},
		"owner":       {a.owner(t), "Owner"},
	} {
		body := a.do(http.MethodGet, "/me", nil, nil, c.ck).Body.String()
		if !strings.Contains(body, `ts-role">`+c.kind+`</span>`) && !strings.Contains(body, `ts-role ts-role--owner">`+c.kind+`</span>`) {
			t.Errorf("%s: no %q label on Profile", name, c.kind)
		}
		if !strings.Contains(body, `ts-side__mekind">`+c.kind+`<`) {
			t.Errorf("%s: no %q in the account chip", name, c.kind)
		}
	}
}

func TestAccountHubChip(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	body := a.do(http.MethodGet, "/", nil, nil, ck).Body.String()
	// The sidebar chip and the rail chip link to /me with the accessible name.
	for _, class := range []string{"ts-side__item ts-side__me", "ts-rail__item ts-rail__me"} {
		i := strings.Index(body, `class="`+class+`"`)
		if i < 0 {
			t.Fatalf("no chip with class %q", class)
		}
		tag := body[strings.LastIndex(body[:i], "<a") : i+200]
		if !strings.Contains(tag, `href="/me"`) || !strings.Contains(tag, `aria-label="Profile, Ana Reyes"`) {
			t.Errorf("%s: wrong link or name: %.240s", class, tag)
		}
	}
	// Profile left the main list of the sidebar and rail; the tab bar keeps its fifth tab, with the avatar.
	nav := body[strings.Index(body, `id="nav-tabbar"`):]
	if n := strings.Count(nav, `data-nav="/me"`); n != 3 {
		t.Errorf("want the tab, the rail chip and the sidebar chip, got %d links to /me", n)
	}
	tab := nav[strings.Index(nav, `data-nav="/me"`):]
	tab = tab[:strings.Index(tab, "</a>")]
	if !strings.Contains(tab, "ts-avatar") || !strings.Contains(tab, "Profile") {
		t.Errorf("the Profile tab lost its label or avatar: %s", tab)
	}
	// On Profile the chip is the current page.
	prof := a.do(http.MethodGet, "/me", nil, nil, ck).Body.String()
	if !strings.Contains(prof, `data-nav="/me" aria-label="Profile, Ana Reyes" aria-current="page"`) {
		t.Error("the chip is not marked current on Profile")
	}
	// The collapse swap keeps the chip; no tooltip on it.
	code, frag, _ := navPost(t, a, "/hx/me/nav", "rail", true, true, "")
	if code != http.StatusOK || !strings.Contains(frag, `ts-side__me`) || !strings.Contains(frag, `aria-label="Profile, Ana Reyes"`) || strings.Contains(frag, `title="Profile`) {
		t.Errorf("collapse swap = %d %.300s", code, frag)
	}
}
