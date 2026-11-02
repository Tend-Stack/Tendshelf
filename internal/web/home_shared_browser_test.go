//go:build dev

package web

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestHomeSharedBrowser walks "Shared with you" in Chromium at 390 and 1440. The admin Daniel shares
// a shelf with one button; Maya, a member who may open the library, sees the row on Home, follows
// "See all" with the keyboard to the Library filtered to shared books, opens the shelf read-only, and
// loses the row when Daniel stops sharing. Rosa, a guest who was given one shelf, has no "New on the
// shelf" because everything she can open was shared. On the owner's Home the series row and the
// shared row sit side by side at 1440.
func TestHomeSharedBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			world := sharedWorldNew(t)
			world.libraryGrant(world.mayaID)
			shelf := world.shareShelf(world.daniel, "Daniel picks", false, 0, 1, 2, 3)
			var who atomic.Pointer[http.Cookie]
			who.Store(world.daniel)
			base := asServer(t, world.a, func() *http.Cookie { return who.Load() })
			wait := func(tab *cdp.Tab, js string) {
				t.Helper()
				tab.WaitFor(js, 10*time.Second)
			}
			tab := b.NewTab(w, 900, w < 768, false)

			// Daniel shares the shelf with its one button, and the button turns into Stop sharing.
			tab.Navigate(base + "/shelves/" + shelf)
			wait(tab, `document.querySelector(".ts-shelf-share button.ts-btn--secondary") !== null`)
			playerClickSel(t, tab, `.ts-shelf-share button`)
			wait(tab, `document.querySelector(".ts-shelf-share button.ts-btn--quiet") !== null`)

			// Maya's Home has the row: three cards, "From Daniel" under the title on a phone.
			who.Store(world.maya)
			tab.Navigate(base + "/")
			wait(tab, `document.querySelector("#row-shared") !== null`)
			var row struct {
				Cards     int    `json:"cards"`
				Text      string `json:"text"`
				SepShown  bool   `json:"sepShown"`
				SubBlock  bool   `json:"subBlock"`
				SubCap    string `json:"subCap"`
				SubBelow  bool   `json:"subBelow"`
				Over      bool   `json:"over"`
				RecentRow bool   `json:"recent"`
			}
			tab.Eval(`(() => {
  const row = document.querySelector("#row-shared");
  const sub = row.querySelector(".ts-home__rowsub");
  const sep = row.querySelector(".ts-home__rowsep");
  const h2 = row.querySelector("h2");
  const title = document.createRange();
  title.setStart(h2.firstChild, 0);
  title.setEnd(h2.firstChild, h2.firstChild.length);
  return {
    cards: row.querySelectorAll(".ts-card").length,
    text: sub.textContent.replace(/^[\s·]+/, ""),
    sepShown: getComputedStyle(sep).display !== "none",
    subBlock: getComputedStyle(sub).display === "block",
    subCap: getComputedStyle(sub, "::first-letter").textTransform,
    subBelow: sub.getBoundingClientRect().top >= title.getBoundingClientRect().bottom - 1,
    over: document.documentElement.scrollWidth > innerWidth,
    recent: document.querySelector("#row-recent") !== null,
  };
})()`, &row)
			if row.Cards != 3 || row.Text != "from Daniel" || row.Over || !row.RecentRow {
				t.Errorf("Shared row: %+v", row)
			}
			if w < 768 && (row.SepShown || !row.SubBlock || row.SubCap != "uppercase" || !row.SubBelow) {
				t.Errorf("phone caption is not under the title and capitalised: %+v", row)
			}
			if w >= 768 && (!row.SepShown || row.SubBlock) {
				t.Errorf("wide caption is not inline: %+v", row)
			}

			// The keyboard reaches See all and Enter opens the Library with the Shared with you chip.
			tab.Eval(`document.body.focus()`, nil)
			reached := false
			for i := 0; i < 60 && !reached; i++ {
				if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
					t.Fatal(err)
				}
				_ = tab.Call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil)
				tab.Eval(`document.activeElement && document.activeElement.matches("#row-shared a.ts-btn--quiet")`, &reached)
			}
			if !reached {
				t.Fatal("Tab never reached See all")
			}
			if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "key": "Enter", "code": "Enter", "text": "\r", "windowsVirtualKeyCode": 13}, nil); err != nil {
				t.Fatal(err)
			}
			_ = tab.Call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13}, nil)
			wait(tab, `location.pathname === "/libraries" && location.search.includes("show=shared") && document.querySelector("main").innerText.includes("Shared with you")`)

			// Maya reads the shelf: the books and "Shared by", and nothing to change.
			tab.Navigate(base + "/shelves/" + shelf)
			wait(tab, `document.querySelector(".ts-shelf-by") !== null`)
			var ro struct {
				By    string `json:"by"`
				Tools int    `json:"tools"`
			}
			tab.Eval(`({by: document.querySelector(".ts-shelf-by").textContent, tools: document.querySelectorAll(".ts-shelf-share, .ts-shelf-rename, .ts-shelf-item__tools, a[href$='/delete']").length})`, &ro)
			if ro.By == "" || ro.Tools != 0 {
				t.Errorf("read-only shelf: %+v", ro)
			}

			// Daniel stops sharing; Maya's Home loses the row.
			who.Store(world.daniel)
			tab.Navigate(base + "/shelves/" + shelf)
			wait(tab, `document.querySelector(".ts-shelf-share button.ts-btn--quiet") !== null`)
			playerClickSel(t, tab, `.ts-shelf-share button`)
			wait(tab, `document.querySelector(".ts-shelf-share button.ts-btn--secondary") !== null`)
			who.Store(world.maya)
			tab.Navigate(base + "/")
			wait(tab, `document.querySelector("#row-recent, .ts-home") !== null`)
			var gone bool
			tab.Eval(`document.querySelector("#row-shared") === null`, &gone)
			if !gone {
				t.Error("the row stays after Daniel stopped sharing")
			}

			// Rosa: a shelf grant is everything she can open, so New on the shelf would repeat it.
			rosaShelf := world.shareShelf(world.daniel, "For Rosa", false, 0, 1)
			world.exec(`INSERT INTO grants(id, user_id, scope, shelf_id, created_by, created_at, updated_at) VALUES ('gsr', ?, 'shelf', ?, ?, 1, 1)`, world.rosaID, rosaShelf, world.danID)
			who.Store(world.ro)
			tab.Navigate(base + "/")
			wait(tab, `document.querySelector("#row-shared") !== null`)
			var recent bool
			tab.Eval(`document.querySelector("#row-recent") !== null`, &recent)
			if recent {
				t.Error("a guest whose books were all shared still sees New on the shelf")
			}
		})
	}

	t.Run("1440 side by side", func(t *testing.T) {
		b := chromium(t)
		a, _, _ := sharedHomeSeed(t)
		base := themedServer(t, a.signedIn(t))
		tab := b.NewTab(1440, 900, false, false)
		tab.Navigate(base + "/")
		tab.WaitFor(`document.querySelector("#row-series") !== null && document.querySelector("#row-shared") !== null`, 10*time.Second)
		var top struct{ Series, Shared float64 }
		tab.Eval(`({Series: document.querySelector("#row-series").getBoundingClientRect().top, Shared: document.querySelector("#row-shared").getBoundingClientRect().top})`, &top)
		if top.Series != top.Shared {
			t.Errorf("row tops differ: series %v, shared %v", top.Series, top.Shared)
		}
	})
}
