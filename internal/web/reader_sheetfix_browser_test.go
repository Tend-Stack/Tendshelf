//go:build dev

package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rfixShot is where evidence screenshots go (TENDSHELF_FIX_SHOTS); empty means none are taken.
func rfixShot(name string) string {
	dir := os.Getenv("TENDSHELF_FIX_SHOTS")
	if dir == "" {
		return ""
	}
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, name)
}

// TestReaderSheetFixedHead: the handle and head stay put while the body of a reader sheet scrolls under
// them, the scrollbar belongs to the body (under the head, inside the rounded box), and the sheet itself
// never scrolls.
func TestReaderSheetFixedHead(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 60, 1)
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		for _, sheet := range []string{"contents", "settings"} {
			t.Run(vp.name+"-"+sheet, func(t *testing.T) {
				tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
				if sheet == "contents" {
					tab.Eval(`document.querySelector('a[href*="sheet=contents"]').click()`, nil)
				} else {
					tab.Eval(`document.querySelector(".ts-aa").click()`, nil)
				}
				tab.WaitFor(`document.querySelector("#sheet .ts-sheet__body") !== null`, 5*time.Second)
				time.Sleep(400 * time.Millisecond)
				geo := `(() => { const s = document.querySelector("#sheet .ts-details-sheet"), h = s.querySelector(".ts-sheet__head"), b = s.querySelector(".ts-sheet__body");
					return {head: h.getBoundingClientRect().top, headBottom: h.getBoundingClientRect().bottom, bodyTop: b.getBoundingClientRect().top,
					sheetScroll: s.scrollTop, bodyScroll: b.scrollTop, scrolls: b.scrollHeight > b.clientHeight, contain: getComputedStyle(b).overscrollBehaviorY}; })()`
				var before, after struct {
					Head, HeadBottom, BodyTop, SheetScroll, BodyScroll float64
					Scrolls                                            bool
					Contain                                            string
				}
				tab.Eval(geo, &before)
				if !before.Scrolls {
					t.Fatalf("the %s body does not scroll: %+v", sheet, before)
				}
				if before.BodyTop < before.HeadBottom-0.5 {
					t.Errorf("body starts above the head's bottom: %+v", before)
				}
				tab.Eval(`document.querySelector("#sheet .ts-sheet__body").scrollTop = 200`, nil)
				time.Sleep(100 * time.Millisecond)
				tab.Eval(geo, &after)
				if after.BodyScroll < 100 || after.SheetScroll != 0 || after.Head != before.Head {
					t.Errorf("head moved or sheet scrolled: before %+v after %+v", before, after)
				}
				if after.Contain != "contain" {
					t.Errorf("overscroll-behavior is %q", after.Contain)
				}
				if p := rfixShot(sheet + "-" + vp.name + ".png"); p != "" {
					tab.Screenshot(p)
				}
			})
		}
	}
}

// TestReaderContentsPickSaves: picking a chapter in the contents sheet closes the sheet, shows that chapter
// and saves it as the reading place at once, so closing the reader straight away keeps it. Opening the same
// address without a pick still saves nothing.
func TestReaderContentsPickSaves(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 4, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=1")
	tab.Eval(`document.querySelector('a[href*="sheet=contents"]').click()`, nil)
	tab.WaitFor(`document.querySelectorAll("#sheet [data-ts-sheet-go]").length > 2`, 5*time.Second)
	if _, _, ok := pgSaved(t, l); ok {
		t.Fatal("opening the book and its contents must not write a place")
	}
	tab.Eval(`document.querySelectorAll("#sheet [data-ts-sheet-go]")[2].click()`, nil)
	tab.WaitFor(`document.querySelector("#sheet .ts-sheet:not(.is-leaving)") === null && location.search.includes("ch=3")`, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	var sid string
	for time.Now().Before(deadline) {
		if got, _, ok := pgSaved(t, l); ok {
			sid = got
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(sid) < 3 || sid[:3] != "c3." {
		t.Fatalf("the picked chapter was saved as %q, want a place in the chapter ch=3 names", sid)
	}
	var search string
	tab.Eval(`location.search`, &search)
	if search != "?ch=3" {
		t.Errorf("after the pick the address is %q; the sheet must not come back", search)
	}
}
