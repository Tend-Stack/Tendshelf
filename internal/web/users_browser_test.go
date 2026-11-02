//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// usersWaitFor polls cond for five seconds; the grant editor answers each form with a redirect that
// htmx swaps in, so the test waits for the stored result instead of sleeping.
func usersWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); time.Sleep(60 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestUsersGrantEditorBrowser drives the person page in Chromium at 390 and 1440: add a library
// grant with an end date, change download and the end date, renew an ended grant, remove a grant and
// stop sharing through the confirm page. After each step the person's own access is checked with
// their own session, so the page and the permission agree.
func TestUsersGrantEditorBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			a := newTestApp(t)
			owner := a.owner(t)
			rosaCk := a.as(t, users.RoleGuest)
			rosa := sweepUserID(t, a, rosaCk)
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := a.st.Writer().Exec(q, args...); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			count := func(q string, args ...any) int {
				var n int
				if err := a.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			kids, kf := librarytest.NewLibrary(t, a.lib, "Kids", adminAudioDir(t, true))
			kidsBook := librarytest.AddBook(t, a.lib, kf, librarytest.BookSpec{Title: "Kidsword", Files: 1, DurationMs: 3_600_000, AddedAt: 5})
			wide, wf := librarytest.NewLibrary(t, a.lib, "Audiobooks", adminAudioDir(t, true))
			wideBook := librarytest.AddBook(t, a.lib, wf, librarytest.BookSpec{Title: "Wideword", Files: 1, DurationMs: 3_600_000, AddedAt: 5})
			// Rosa starts with an ended grant on Kids.
			exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, expires_at, created_at, updated_at) VALUES ('old', ?, 'library', ?, 1, 2, 1, 1)`, rosa, kids.ID)

			status := func(bookID string) int {
				return a.do(http.MethodGet, "/books/"+bookID, nil, nil, rosaCk).Code
			}
			if status(kidsBook.ID) != 404 || status(wideBook.ID) != 404 {
				t.Fatal("Rosa sees books before any grant")
			}

			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
				a.h.ServeHTTP(rw, r)
			}))
			t.Cleanup(srv.Close)

			tab := b.NewTab(w, 900, w < 768, false)
			tab.Navigate(srv.URL + "/admin/users/" + rosa)
			tab.WaitFor(`document.querySelector(".ts-users__grant") !== null`, 10*time.Second)
			click := func(sel string) { playerClickSel(t, tab, sel) }

			// Targets of 44 px, a visible focus ring after a keyboard Tab, every field labelled.
			var m struct {
				Small      []string `json:"small"`
				Unlabelled []string `json:"unlabelled"`
			}
			tab.Eval(usersProbeJS, &m)
			if len(m.Small) > 0 || len(m.Unlabelled) > 0 {
				t.Errorf("targets under 44 px %v, unlabelled %v", m.Small, m.Unlabelled)
			}
			for _, typ := range []string{"keyDown", "keyUp"} {
				if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
					t.Fatal(err)
				}
			}
			var ring bool
			tab.Eval(`document.activeElement !== document.body && document.activeElement.matches(":focus-visible") && getComputedStyle(document.activeElement).boxShadow !== "none"`, &ring)
			if !ring {
				t.Error("no visible focus ring after Tab")
			}

			// Renew the ended grant: Rosa now sees Kids.
			click(`form[action$="/grants/old/renew"] button`)
			usersWaitFor(t, "renew", func() bool {
				return count(`SELECT count(*) FROM grants WHERE id = 'old' AND expires_at > ?`, time.Now().AddDate(0, 0, 30).UnixMilli()) == 1
			})
			if status(kidsBook.ID) != 200 {
				t.Error("renewed grant does not open the book")
			}

			// Add a library grant with an end date. The renewal's answer replaces the page: wait until it has
			// (no Renew button left, no page change running), or the new page would wipe the fields set here.
			until := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
			tab.WaitFor(`document.querySelector('form[action$="/grants/old/renew"]') === null && !document.activeViewTransition &&
			  document.querySelector("#us-until") !== null`, 10*time.Second)
			tab.Eval(`document.querySelector('select[name=library]').value = `+cdp.Quote(wide.ID), nil)
			click(`input[name=scope][value=library]`)
			tab.Eval(`document.querySelector("#us-until").value = `+cdp.Quote(until), nil)
			click(`.ts-users__addform button[type=submit]`)
			var gid string
			usersWaitFor(t, "add", func() bool {
				return a.st.Reader().QueryRow(`SELECT id FROM grants WHERE user_id = ? AND library_id = ?`, rosa, wide.ID).Scan(&gid) == nil
			})
			if count(`SELECT count(*) FROM grants WHERE id = ? AND can_download = 1 AND expires_at IS NOT NULL`, gid) != 1 {
				t.Error("new grant: want download on and an end date")
			}
			if status(wideBook.ID) != 200 {
				t.Error("new grant does not open the book")
			}

			// Turn download off and move the end date.
			later := time.Now().AddDate(0, 0, 40).Format("2006-01-02")
			tab.WaitFor(`document.querySelector("#dl-`+gid+`") !== null`, 10*time.Second)
			click(`#dl-` + gid)
			tab.Eval(`document.querySelector("#until-`+gid+`").value = `+cdp.Quote(later), nil)
			click(`form[action$="/grants/` + gid + `"] button[type=submit]`)
			usersWaitFor(t, "save", func() bool {
				return count(`SELECT count(*) FROM grants WHERE id = ? AND can_download = 0`, gid) == 1
			})
			wantEnd, _ := time.ParseInLocation("2006-01-02", later, time.Local)
			var endMs int64
			_ = a.st.Reader().QueryRow(`SELECT expires_at FROM grants WHERE id = ?`, gid).Scan(&endMs)
			if got := time.UnixMilli(endMs); got.Before(wantEnd) || got.After(wantEnd.AddDate(0, 0, 2)) {
				t.Errorf("end date %v, want about %v", got, wantEnd)
			}

			// Remove the new grant: its book closes again, the renewed one stays.
			tab.WaitFor(`document.querySelector('form[action$="/grants/`+gid+`/remove"] button') !== null`, 10*time.Second)
			click(`form[action$="/grants/` + gid + `/remove"] button`)
			usersWaitFor(t, "remove", func() bool { return count(`SELECT count(*) FROM grants WHERE id = ?`, gid) == 0 })
			if status(wideBook.ID) != 404 || status(kidsBook.ID) != 200 {
				t.Error("after remove: want the new book closed and the renewed one open")
			}

			// Stop sharing through the confirm page.
			tab.WaitFor(`document.querySelector('a[href$="/stop"]') !== null`, 10*time.Second)
			click(`a[href$="/stop"]`)
			tab.WaitFor(`document.querySelector('form[action$="/stop"]') !== null`, 10*time.Second)
			time.Sleep(400 * time.Millisecond) // let the swap settle
			click(`form[action$="/stop"] button[type=submit]`)
			usersWaitFor(t, "stop sharing", func() bool { return count(`SELECT count(*) FROM grants WHERE user_id = ?`, rosa) == 0 })
			if status(kidsBook.ID) != 404 {
				t.Error("after stop sharing the book is still open")
			}
			tab.WaitFor(`document.body.innerText.includes("Nothing shared yet")`, 10*time.Second)
		})
	}
}
