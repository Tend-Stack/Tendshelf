//go:build dev

package web

import (
	"context"
	"strings"
	"testing"
	"time"
)

// deskDropScript runs a drop of generated files on the page the way the window delivers one: dragenter
// first (the zone shows), then drop with a DataTransfer that holds the files.
const deskDropScript = `(async (names) => {
  const list = new DataTransfer();
  for (const name of names) list.items.add(new File(["not really audio"], name));
  window.dispatchEvent(new DragEvent("dragenter", { dataTransfer: list, bubbles: true }));
  const shown = !document.querySelector("[data-wbar-drop]").hidden;
  document.body.dispatchEvent(new DragEvent("drop", { dataTransfer: list, bubbles: true, cancelable: true }));
  return shown;
})`

// TestDeskDropBrowser: dropping a copy-protected file on Home sends it to Add books and the page's own
// uploader refuses it with its own words, sending nothing; with uploads off the zone never shows and the
// words say where to turn them on. The channel is a stub: no real window, no real drag from Files.
func TestDeskDropBrowser(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tab := b.NewTab(1440, 900, false, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": deskStateStub}, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(u.URL + "/?shell=desktop")
	tab.WaitFor(`document.querySelector("[data-wbar-drop]") !== null && window.htmx !== undefined`, 8*time.Second)
	time.Sleep(300 * time.Millisecond)

	var shown bool
	tab.Eval(deskDropScript+`(["The Glass Orchard.aax"])`, &shown)
	if !shown {
		t.Error("the drop zone did not show on dragenter")
	}
	tab.WaitFor(`document.querySelector(".ts-upload__row--refused") !== null`, 10*time.Second)
	if text := rowText(t, tab); !strings.Contains(text, "The Glass Orchard.aax is copy-protected, so Tendshelf will not open it. Nothing was kept.") {
		t.Errorf("refused row = %q", text)
	}
	var path string
	tab.Eval(`location.pathname`, &path)
	if path != "/libraries/"+u.libID+"/add" {
		t.Errorf("landed on %q", path)
	}
	if u.putCount() != 0 || u.creates != 0 || len(u.uploads(t)) != 0 {
		t.Errorf("a copy-protected file reached the server: %d PUTs, %d creates", u.putCount(), u.creates)
	}

	// Uploads off: no zone, and the words appear.
	if err := u.a.lib.SetUploadFolder(context.Background(), u.libID, ""); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(u.URL + "/?shell=desktop")
	tab.WaitFor(`document.querySelector("[data-wbar-drop]") !== null && window.htmx !== undefined`, 8*time.Second)
	time.Sleep(300 * time.Millisecond)
	tab.Eval(deskDropScript+`(["Book.m4b"])`, &shown)
	if shown {
		t.Error("the drop zone showed with uploads off")
	}
	var words string
	tab.Eval(`document.getElementById("toasts").textContent.replace(/\s+/g, " ").trim()`, &words)
	if !strings.Contains(words, "Uploads are off") {
		t.Errorf("toast = %q", words)
	}
}
