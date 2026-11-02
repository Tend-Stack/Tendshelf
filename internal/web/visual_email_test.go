//go:build dev

package web

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/smtpfake"
)

// TestVisualEmailPages captures the Share page's Email tab (empty, the answer with the email app,
// "Send it for me" offered, pending rows sent and failed) and the Email settings page (empty, saved,
// test sent, test failed) in the visualMatrix themes and widths. ShareForm is the board for the Email
// tab; the settings page has no board (it is built from the form parts), so its captures are only
// recorded. The ShareForm board is copied beside the captures.
func TestVisualEmailPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e.exec(`UPDATE users SET display_name = 'Daniel Reyes' WHERE id = ?`, e.ownerID)
	if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "people", "ShareForm.png")); err == nil {
		if dst, err := os.Create(filepath.Join(out, "email-board-ShareForm.png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}
	srv := mailServer(t, e)
	shots := 0

	// capture visits path in every shot of the matrix; prep may act on the page first (it gets the
	// shot's number, so each shot can use its own address).
	capture := func(slug, state, path string, main bool, prep func(tab *cdp.Tab, n int)) {
		for _, sh := range visualMatrix(main) {
			theme, w := sh.Theme, sh.Width
			shots++
			n := shots
			t.Run(fmt.Sprintf("%s-%s-%s-%d", slug, state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				tab.Navigate(srv.URL + path + sep + "theme=" + theme)
				tab.WaitFor(`document.querySelector(".ts-share, .ts-email") !== null`, 10*time.Second)
				if prep != nil {
					prep(tab, n)
					time.Sleep(time.Second) // the page swap cross-fades; capture once it has settled
				}
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("%s-%s-%s-%d.png", slug, state, theme, w)))
			})
		}
	}
	answer := func(t *testing.T) func(tab *cdp.Tab, n int) {
		return func(tab *cdp.Tab, n int) {
			tab.Eval(`window.__mailto = ""; navigation.addEventListener("navigate", (e) => { if (e.destination.url.startsWith("mailto:")) { window.__mailto = e.destination.url; e.preventDefault(); } })`, nil)
			inviteFill(tab, "#who", "Rosa Reyes")
			inviteFill(tab, "#share-email", fmt.Sprintf("rosa%d@example.org", n))
			inviteFill(tab, "#msg", "Start with the first book. We can talk about it on Sunday.")
			playerClickSel(t, tab, `label[for="scope-all"]`)
			playerClickSel(t, tab, `.ts-share__create button[value=app]`)
			tab.WaitFor(`document.querySelector("#share-link") !== null`, 10*time.Second)
		}
	}

	// Nothing set up: the Email tab works with the person's own email app.
	capture("admin-email", "empty", "/admin/email", true, nil)
	capture("share-email", "empty", "/share?via=email", true, nil)
	capture("share-email", "answer", "/share?via=email", true, answer(t))

	// Set up and tested: the settings page, and the relay button on the tab.
	e.ready()
	capture("admin-email", "saved", "/admin/email?saved=1", true, nil)
	var jobID string
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'smtp.test' ORDER BY created_at DESC LIMIT 1`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	capture("admin-email", "test-sent", "/admin/email?test="+url.QueryEscape(jobID)+"&to=me%40example.org", false, nil)
	capture("share-email", "relay", "/share?via=email", true, nil)

	// The answer shots left one invite each; clear them so the pending list shows only the three below.
	e.exec(`DELETE FROM invites`)
	// A sent invite, then a refused one: the pending rows.
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"relay"}, "name": {"Rosa Reyes"}, "email": {"rosa@example.org"}}), false), 200, "relay invite")
	e.jobEnd("invite.mail")
	e.fake.SetFail(map[string]int{"rcpt": 550})
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"relay"}, "name": {"Tío Marcos"}, "email": {"marcos@example.org"}}), false), 200, "refused invite")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(e.get(e.owner, "/share").Body.String(), "Couldn&#39;t send to marcos@example.org") {
		time.Sleep(100 * time.Millisecond)
	}
	usersWant(t, e.post(e.owner, "/share", mailForm(e.inviteWeb, url.Values{"how": {"app"}, "name": {"Lena"}, "email": {"lena@example.org"}}), false), 200, "own app invite")
	capture("share-email", "pending", "/share?via=email", true, nil)

	// A failed test email.
	usersWant(t, e.post(e.owner, "/admin/email/test", url.Values{"to": {"me@example.org"}}, false), 200, "failing test")
	e.jobEnd("smtp.test")
	if err := e.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'smtp.test' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	capture("admin-email", "test-failed", "/admin/email?test="+url.QueryEscape(jobID)+"&to=me%40example.org", false, nil)
	t.Logf("screenshots in %s", out)
}
