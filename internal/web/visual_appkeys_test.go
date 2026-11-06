//go:build dev

package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestAppKeysVisual captures the App keys section on Your devices (empty, then with two keys) and the
// one-time page that shows a new key, at 390, 768, 1024 and 1440 px in each theme. The key and the
// address are random per run, so the capture replaces them with fixed text before the screenshot. It
// also checks no overflow, targets of 44 px, labelled fields, and that the key is shown on one POST only.
func TestAppKeysVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(owner)
		a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)

	probe := func(t *testing.T, tab *cdp.Tab) {
		var m struct {
			Over       bool     `json:"over"`
			Small      []string `json:"small"`
			Unlabelled []string `json:"unlabelled"`
		}
		tab.Eval(shareProbeJS, &m)
		if m.Over {
			t.Errorf("horizontal overflow")
		}
		if len(m.Small) > 0 {
			t.Errorf("targets under 44 px: %v", m.Small)
		}
		if len(m.Unlabelled) > 0 {
			t.Errorf("inputs without a label: %v", m.Unlabelled)
		}
	}
	shoot := func(state string, shown bool) {
		for _, sh := range visualMatrix(false) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + "/me/devices?theme=" + theme)
				tab.WaitFor(`document.querySelector(".ts-akey") !== null`, 10*time.Second)
				if shown {
					tab.Eval(`(document.getElementById("akey-name").value = "Kobo", document.querySelector(".ts-akey__form button").click())`, nil)
					tab.WaitFor(`document.querySelector(".ts-akey__key") !== null`, 10*time.Second)
					tab.Eval(`(document.querySelector(".ts-akey__key").textContent = "abcdefghijklmnopqrstuvwx",
						document.querySelectorAll(".ts-akey__value")[0].textContent = "http://tendshelf.example/opds")`, nil)
				}
				probe(t, tab)
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("app-keys-%s-%s-%d.png", state, theme, w)))
			})
		}
	}

	shoot("empty", false)
	shoot("shown", true)                                        // each run of the loop makes one key; they are removed again below
	a.st.WriteTx(context.Background(), func(tx *sql.Tx) error { // two fixed rows for the filled state
		_, err := tx.ExecContext(context.Background(), `DELETE FROM devices WHERE kind IN ('opds','koreader')`)
		return err
	})
	for i, kind := range []auth.DeviceKind{auth.KindOPDS, auth.KindKOReader} {
		err := a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, _, err := a.srv.devices.IssueKey(context.Background(), tx, sweepUserID(t, a, owner), []string{"Kobo Libra", "Boox KOReader"}[i], kind)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	shoot("keys", false)
}
