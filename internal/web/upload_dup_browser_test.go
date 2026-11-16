//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The browser's quick hash (js/upload.js quickHash) must equal media.QuickHash byte for byte: the server compares
// the two to say "already in the library" before a piece is sent.

// quickBytes is the deterministic content of the hash test files; the page builds the same bytes with the same rule.
func quickBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131 + (i>>8)*7 + (i>>20)*13 + 3)
	}
	return b
}

const quickJSBytes = `(n) => { const b = new Uint8Array(n); for (let i = 0; i < n; i++) b[i] = (i*131 + (i>>8)*7 + (i>>20)*13 + 3) & 255; return b; }`

func TestUploadJSQuickHashEqualsGo(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tab := openAddBooks(t, u, b)
	const mib = 1 << 20
	sizes := []int{0, 1, mib - 1, 2*mib - 1, 2 * mib, 2*mib + 1, 5 * mib}
	for _, n := range sizes {
		p := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(p, quickBytes(n), 0o600); err != nil {
			t.Fatal(err)
		}
		want, size, err := media.QuickHash(p)
		if err != nil || size != int64(n) {
			t.Fatal(err, size)
		}
		var got string
		expr := fmt.Sprintf(`(async () => { const m = await import(document.querySelector("[data-ts-module]").dataset.tsModule); `+
			`return m.quickHash(new Blob([(%s)(%d)])); })()`, quickJSBytes, n)
		if err := tab.TryEval(expr, &got); err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		if got != want {
			t.Errorf("size %d: browser %s, Go %s", n, got, want)
		}
	}
	// Without crypto.subtle (plain http, an old browser) the hash is skipped: the answer is empty.
	var skipped string
	if err := tab.TryEval(`(async () => { Object.defineProperty(crypto, "subtle", { value: undefined, configurable: true }); `+
		`const m = await import(document.querySelector("[data-ts-module]").dataset.tsModule); return "[" + await m.quickHash(new Blob(["abc"])) + "]"; })()`, &skipped); err != nil || skipped != "[]" {
		t.Errorf("without subtle: %q %v", skipped, err)
	}
}

// libraryHas puts a book into the library whose one audio file is the given file (as a scan would record it).
func (u *uploadBrowser) libraryHas(t *testing.T, title, file string) string {
	t.Helper()
	hash, size, err := media.QuickHash(file)
	if err != nil {
		t.Fatal(err)
	}
	book := librarytest.AddBook(t, u.a.lib, u.folder, librarytest.BookSpec{Title: title, Files: 1})
	if _, err := u.a.st.Writer().Exec(`UPDATE audio_files SET size = ?, quick_hash = ? WHERE book_id = ?`, size, hash, book.ID); err != nil {
		t.Fatal(err)
	}
	return book.ID
}

// TestUploadBrowserWarnsBeforeSending: the incident. A book is already in the library (copied by hand); choosing
// the same file on Add books shows the warning with no piece sent; Open it goes to the book; Add anyway uploads.
func TestUploadBrowserWarnsBeforeSending(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tone := writeTone(t, "The Long Road.wav", 3000)
	bookID := u.libraryHas(t, "The Long Road", tone)
	for _, vp := range []struct {
		name          string
		w, h          int
		mobile        bool
		anyway, leave bool
	}{{"mobile-390", 390, 844, true, false, true}, {"desktop-1440", 1440, 900, false, true, false}} {
		t.Run(vp.name, func(t *testing.T) {
			tab := b.NewTab(vp.w, vp.h, vp.mobile, false)
			tab.Navigate(u.URL + "/libraries/" + u.libID + "/add")
			tab.WaitFor(`document.querySelector("[data-ts-pick=files]") !== null`, 10*time.Second)
			time.Sleep(300 * time.Millisecond)
			before := u.putCount()
			chooseFiles(t, tab, "[data-ts-pick=files]", tone)
			tab.WaitFor(`document.querySelector(".ts-upload__row--already") !== null`, 10*time.Second)
			text := rowText(t, tab)
			for _, w := range []string{"This book is already in Audiobooks: The Long Road.", "Open it", "Add anyway", "Do not add"} {
				if !strings.Contains(text, w) {
					t.Errorf("warning lacks %q: %q", w, text)
				}
			}
			if u.putCount() != before || len(u.uploads(t)) != 0 {
				t.Errorf("something was sent before the choice: %d PUTs, %d uploads", u.putCount()-before, len(u.uploads(t)))
			}
			var geo struct {
				Open    string
				Heights []float64
				Over    bool
			}
			tab.Eval(`({ open: document.querySelector(".ts-upload__row--already [data-ts-open]").getAttribute("href"),
				heights: [...document.querySelectorAll(".ts-upload__row--already .ts-btn:not([hidden])")].map(e => e.getBoundingClientRect().height),
				over: document.documentElement.scrollWidth > window.innerWidth })`, &geo)
			if geo.Open != "/books/"+bookID || geo.Over || len(geo.Heights) < 3 {
				t.Errorf("geometry: %+v", geo)
			}
			for _, h := range geo.Heights {
				if h < 44 {
					t.Errorf("a choice is %.0f px tall", h)
				}
			}
			if vp.leave {
				// Do not add: the row goes and nothing was made.
				tab.Eval(`document.querySelector("[data-ts-act=skip]").click()`, nil)
				tab.WaitFor(`document.querySelector(".ts-upload__row--already") === null`, 5*time.Second)
				if len(u.uploads(t)) != 0 {
					t.Error("Do not add left an upload")
				}
				return
			}
			tab.Eval(`document.querySelector("[data-ts-act=anyway]").click()`, nil)
			tab.WaitFor(`document.querySelector(".ts-upload__row--added") !== null`, 40*time.Second)
			if u.putCount() == before {
				t.Error("Add anyway sent nothing")
			}
		})
	}
	// Open it goes to the book.
	tab := openAddBooks(t, u, b)
	chooseFiles(t, tab, "[data-ts-pick=files]", tone)
	tab.WaitFor(`document.querySelector(".ts-upload__row--already") !== null`, 10*time.Second)
	tab.Eval(`document.querySelector(".ts-upload__row--already [data-ts-open]").click()`, nil)
	tab.WaitFor(`location.pathname === "/books/`+bookID+`"`, 10*time.Second)
}

// TestUploadBrowserNoPreSendHashStillWarns: without crypto.subtle (plain http) the files are sent, the server
// holds the book after staging, and the polled row shows the same choices; Add anyway then places it.
func TestUploadBrowserNoPreSendHashStillWarns(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tone := writeTone(t, "Quiet Harbour.wav", 3000)
	u.libraryHas(t, "Quiet Harbour", tone)
	tab := openAddBooks(t, u, b)
	tab.Eval(`Object.defineProperty(crypto, "subtle", { value: undefined, configurable: true })`, nil)
	chooseFiles(t, tab, "[data-ts-pick=files]", tone)
	tab.WaitFor(`document.querySelector(".ts-upload__row--already") !== null`, 40*time.Second)
	if u.putCount() == 0 {
		t.Error("the file was never sent, so this did not test the held path")
	}
	text := rowText(t, tab)
	if !strings.Contains(text, "Quiet Harbour") || !strings.Contains(text, "Add anyway") || !strings.Contains(text, "Do not add") {
		t.Errorf("held row = %q", text)
	}
	if n := len(placedAudio(t, u.folderDir)); n != 0 {
		t.Errorf("a held upload placed %d files", n)
	}
	tab.Eval(`document.querySelector(".ts-upload__row--already button[type=submit].ts-btn--primary").click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-upload__row--added") !== null`, 40*time.Second)
	if n := len(placedAudio(t, u.folderDir)); n != 1 {
		t.Errorf("after Add anyway placed = %d", n)
	}
}

// TestUploadBrowserUploadThenHandCopy: the other order. A book was added by upload; the same files then turn up in
// the folder (a scan records them as a second book). The book page tells the owner, linking to the merge preview.
func TestUploadBrowserUploadThenHandCopy(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tone := writeTone(t, "Salt Road.wav", 3000)
	first := u.libraryHas(t, "Salt Road", tone)
	second := u.libraryHas(t, "Salt Road (hand copy)", tone)
	for _, vp := range []struct {
		w, h   int
		mobile bool
	}{{390, 844, true}, {1440, 900, false}} {
		tab := b.NewTab(vp.w, vp.h, vp.mobile, false)
		tab.Navigate(u.URL + "/books/" + second)
		tab.WaitFor(`document.querySelector(".ts-book__dup") !== null`, 10*time.Second)
		var link string
		var over bool
		tab.Eval(`document.querySelector(".ts-book__dup a").getAttribute("href")`, &link)
		tab.Eval(`document.documentElement.scrollWidth > window.innerWidth`, &over)
		if link != "/admin/duplicates/merge?keep="+first+"&drop="+second || over {
			t.Errorf("%dpx: link %q, overflow %v", vp.w, link, over)
		}
		tab.Navigate(u.URL + link)
		tab.WaitFor(`location.pathname === "/admin/duplicates/merge"`, 10*time.Second)
		tab.Close()
	}
}

var _ = cdp.Start
