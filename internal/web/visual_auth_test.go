//go:build dev

package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// themedServer serves h, adding the ts_theme cookie named by ?theme= (the sign-in pages have no
// theme switch of their own).
func themedServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const authProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("input:not([type=hidden]), button, a.ts-btn")) {
    const r = el.getBoundingClientRect();
    if (r.height < 44) small.push((el.id || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("input:not([type=hidden])")].filter(i => !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id);
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// TestVisualAuthPages captures /setup and /login in every theme at four widths and fails on
// horizontal overflow, small touch targets or an unlabelled input. The 390 dark /setup capture is
// copied beside the design board for a side-by-side look.
func TestVisualAuthPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")

	fresh := newTestApp(t) // no owner: /setup
	setupBase := themedServer(t, fresh.h)
	taken := newTestApp(t) // has an owner: /login
	taken.owner(t)
	loginBase := themedServer(t, taken.h)

	board, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "people", "OnboardAccount.png"))
	if err == nil {
		defer board.Close()
		if dst, err := os.Create(filepath.Join(out, "auth-setup-board-OnboardAccount.png")); err == nil {
			_, _ = io.Copy(dst, board)
			_ = dst.Close()
		}
	}

	pages := []struct{ name, base, path string }{
		{"setup", setupBase, "/setup?code=" + testSetupCode},
		{"login", loginBase, "/login"},
	}
	for _, pg := range pages {
		for _, theme := range themes {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					sep := "?"
					if strings.Contains(pg.path, "?") {
						sep = "&"
					}
					tab.Navigate(pg.base + pg.path + sep + "theme=" + theme)
					var m struct {
						Over       bool     `json:"over"`
						Theme      string   `json:"theme"`
						Small      []string `json:"small"`
						Unlabelled []string `json:"unlabelled"`
						H1         int      `json:"h1"`
					}
					tab.Eval(authProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if m.Theme != theme {
						t.Errorf("data-theme = %q, want %q", m.Theme, theme)
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					if len(m.Unlabelled) > 0 {
						t.Errorf("inputs without a label: %v", m.Unlabelled)
					}
					if m.H1 != 1 {
						t.Errorf("%d h1 elements", m.H1)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("auth-%s-%s-%d.png", pg.name, theme, w)))
				})
			}
		}
	}
	t.Run("setup-errors-dark-390", func(t *testing.T) {
		// Submit the form with problems in the browser (CSRF cookie and hidden field included) and
		// capture the answer.
		tab := b.NewTab(390, 900, true, false)
		tab.Navigate(setupBase + "/setup?code=" + testSetupCode + "&theme=dark")
		tab.Eval(`(() => {
			const set = (id, v) => { document.getElementById(id).value = v; };
			set("display_name", "Ana"); set("username", "a b"); set("password", "short");
			document.querySelector("form").requestSubmit();
		})()`, nil)
		tab.WaitFor(`document.querySelectorAll(".ts-field__error").length >= 2 && document.readyState === "complete"`, 5*time.Second)
		var m struct {
			Over    bool `json:"over"`
			Invalid int  `json:"invalid"`
			Echoed  bool `json:"echoed"`
		}
		tab.Eval(`({over: document.documentElement.scrollWidth > innerWidth,
			invalid: document.querySelectorAll('[aria-invalid="true"]').length,
			echoed: document.getElementById("password").value !== ""})`, &m)
		if m.Over || m.Invalid < 2 || m.Echoed {
			t.Errorf("error state: %+v", m)
		}
		tab.ScreenshotFull(filepath.Join(out, "auth-setup-errors-dark-390.png"))
	})
	t.Logf("screenshots in %s", out)
}

// TestVisualAuthRedirect is the check behind the gate's answer to htmx requests: with the session
// gone, a click on a boosted link must end on /login (a 204 with HX-Redirect), not swap the sign-in
// page into #main and not leave the page unchanged.
func TestVisualAuthRedirect(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	srv := httptest.NewServer(app.signedIn(t))
	t.Cleanup(srv.Close)

	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(srv.URL + "/dev/kitchen-sink")
	if _, err := app.st.Writer().Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	tab.Eval(`document.querySelector('a[href="/libraries"]').click()`, nil)
	tab.WaitFor(`location.pathname === "/login"`, 5*time.Second)
	var got struct {
		Search string `json:"search"`
		Form   bool   `json:"form"`
		Frame  bool   `json:"frame"`
	}
	tab.Eval(`({search: location.search, form: Boolean(document.querySelector('form[action="/login"]')),
		frame: document.body.classList.contains("ts-frame")})`, &got)
	// The clicked nav link (Library), not the page being left: a boosted click is a navigation.
	if got.Search != "?next=%2Flibraries" {
		t.Errorf("search = %q", got.Search)
	}
	if !got.Form || !got.Frame {
		t.Errorf("the sign-in page did not load as a full page: %+v", got)
	}
	tab.Screenshot(filepath.Join(visualDir(t, "chromium"), "auth-redirect-login-1440.png"))
}
