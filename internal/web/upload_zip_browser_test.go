//go:build dev

package web

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// zipOf writes a zip with the given files (name in the archive to a file on disk).
func zipOf(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for in, from := range files {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// dropFiles drops files or folders from the disk on the pick area, as a desktop drag does.
func dropFiles(t *testing.T, tab *cdp.Tab, paths ...string) {
	t.Helper()
	var pt struct{ X, Y float64 }
	tab.Eval(`(() => { const r = document.querySelector(".ts-upload__pick").getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + 20}; })()`, &pt)
	data := map[string]any{"items": []any{}, "files": paths, "dragOperationsMask": 1}
	for _, kind := range []string{"dragEnter", "dragOver", "drop"} {
		if err := tab.Call("Input.dispatchDragEvent", map[string]any{"type": kind, "x": pt.X, "y": pt.Y, "data": data}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func addedRows(t *testing.T, tab *cdp.Tab) int {
	t.Helper()
	var n int
	tab.Eval(`document.querySelectorAll(".ts-upload__row--added").length`, &n)
	return n
}

func waitAdded(t *testing.T, tab *cdp.Tab, n int) {
	t.Helper()
	tab.WaitFor(`document.querySelectorAll(".ts-upload__row--added").length >= `+strconv.Itoa(n), 60*time.Second)
	if got := addedRows(t, tab); got != n {
		t.Fatalf("added rows = %d, want %d (%s)", got, n, rowText(t, tab))
	}
}

// TestUploadBrowserZipsAndSeveralBooks: two zips chosen together are two books; a folder dropped
// with "This folder holds several books" ticked gives one book for each subfolder, and its loose
// file is left out. Every upload is checked on the server and on disk.
func TestUploadBrowserZipsAndSeveralBooks(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tab := openAddBooks(t, u, b)

	zipA := zipOf(t, "Ann Author - Zip One.zip", map[string]string{"Zip One/01.wav": writeTone(t, "a.wav", 1500), "Zip One/02.wav": writeTone(t, "b.wav", 1600)})
	zipB := zipOf(t, "Bob Writer - Zip Two.zip", map[string]string{"01.wav": writeTone(t, "c.wav", 1700)})
	chooseFiles(t, tab, "[data-ts-pick=files]", zipA, zipB)
	waitAdded(t, tab, 2)
	if n := len(placedAudio(t, u.folderDir)); n != 3 {
		t.Errorf("placed %d audio files from the two zips, want 3", n)
	}
	var heading string
	tab.Eval(`document.querySelector("#queue-h").textContent`, &heading)
	if heading != "2 books" {
		t.Errorf("queue heading = %q", heading)
	}

	shelf := t.TempDir()
	for name, ms := range map[string]int{"Shelf/Cara Cook - Book One/01.wav": 1800, "Shelf/Dan Dale - Book Two/01.wav": 1900} {
		p := filepath.Join(shelf, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(writeTone(t, "t.wav", ms), p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(shelf, "Shelf", "notes.txt"), []byte("loose"), 0o600); err != nil {
		t.Fatal(err)
	}
	tab.Eval(`document.querySelector("[data-ts-several]").click()`, nil)
	dropFiles(t, tab, filepath.Join(shelf, "Shelf"))
	waitAdded(t, tab, 4)
	if n := len(placedAudio(t, u.folderDir)); n != 5 {
		t.Errorf("placed %d audio files, want 5", n)
	}
	var notes int
	_ = filepath.Walk(u.folderDir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && filepath.Base(p) == "notes.txt" {
			notes++
		}
		return nil
	})
	if notes != 0 {
		t.Error("the loose file at the top of the folder was placed")
	}
	if n := len(u.uploads(t)); n != 4 {
		t.Errorf("server lists %d uploads, want 4", n)
	}
}

// TestUploadBrowserOneZipOfTwoBooks: a zip with two top-level book folders is two books. The row of
// the upload turns into the first book and the second one appears beside it, both end "Added", and
// every track is placed.
func TestUploadBrowserOneZipOfTwoBooks(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tab := openAddBooks(t, u, b)
	z := zipOf(t, "Two Books.zip", map[string]string{
		"Alpha Tale/01.wav": writeTone(t, "a.wav", 1500),
		"Alpha Tale/02.wav": writeTone(t, "b.wav", 1600),
		"Beta Tale/01.wav":  writeTone(t, "c.wav", 1700),
		"notes.txt":         writeTone(t, "d.wav", 1000),
	})
	chooseFiles(t, tab, "[data-ts-pick=files]", z)
	waitAdded(t, tab, 2)
	if n := len(placedAudio(t, u.folderDir)); n != 3 {
		t.Errorf("placed %d tracks, want 3", n)
	}
	text := rowText(t, tab)
	if !strings.Contains(text, "Alpha Tale") || !strings.Contains(text, "Beta Tale") {
		t.Errorf("rows = %q", text)
	}
	if n := len(u.uploads(t)); n != 2 {
		t.Errorf("server lists %d uploads, want 2", n)
	}
}
