//go:build dev

package web

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// fmt2Ebook gives the player fixture's book a ready ebook (the row is all the player's book icon needs).
func fmt2Ebook(t *testing.T, e *playerEnv) {
	t.Helper()
	svc := ebook.New(e.a.st, e.a.lib, e.a.runner, e.a.dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.a.srv.ebooks = svc
	placeEbook(t, e.a, e.book, e.ownerID, "icon.epub", "ready", "")
}

type fmt2Icon struct {
	Hidden, Shown          bool
	Href, Label, Name      string
	Left, Right, ChapRight float64
	Width, Height          float64
}

// TestPlayerReadIconBrowser: the book icon sits right of the chapters icon in the wide bar and in the full
// player, is named for a screen reader, points at the reader, and is hidden for a book with no ebook. On a
// phone the mini row stays as it is.
func TestPlayerReadIconBrowser(t *testing.T) {
	e := playerApp(t)
	fmt2Ebook(t, e)
	other := librarytest.AddBook(t, e.a.lib, library.Folder{ID: e.book.FolderID, LibraryID: e.book.LibraryID, Path: t.TempDir()},
		librarytest.BookSpec{Title: "Bare Audio", DurationMs: 90_000, Files: 1})
	files, err := e.a.lib.AudioFiles(context.Background(), other.ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	if err := os.MkdirAll(filepath.Dir(files[0].Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := wav.Write(files[0].Path, 90_000, 440); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	streamExec(t, e.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=90000, size=?, mtime_ns=? WHERE id=?`,
		fi.Size(), fi.ModTime().UnixNano(), files[0].ID)

	// 1280 px: the bar.
	tab := fullpOpen(t, e, 1280, 800, true)
	var bar fmt2Icon
	tab.Eval(`(() => {
	  const a = document.querySelector("[data-ts-bar-read]"), c = document.querySelector("[data-ts-bar-chapters]");
	  const r = a.getBoundingClientRect(), cr = c.getBoundingClientRect();
	  return { Hidden: a.hidden, Shown: a.offsetParent !== null, Href: a.getAttribute("href"), Label: a.getAttribute("aria-label"),
	    Left: r.left, Right: r.right, ChapRight: cr.right, Width: r.width, Height: r.height };
	})()`, &bar)
	if bar.Hidden || !bar.Shown || bar.Href != "/books/"+e.book.ID+"/read" || bar.Label != "Read this book" {
		t.Errorf("bar icon: %+v", bar)
	}
	if bar.Left < bar.ChapRight-1 {
		t.Errorf("the book icon is not right of the chapters icon: left %.1f, chapters right %.1f", bar.Left, bar.ChapRight)
	}
	if bar.Width < 44 || bar.Height < 44 {
		t.Errorf("touch target %.0f by %.0f", bar.Width, bar.Height)
	}
	var fits bool
	tab.Eval(`(() => { const b = document.querySelector("[data-ts-bar-read]").getBoundingClientRect(); return b.right <= innerWidth; })()`, &fits)
	if !fits {
		t.Error("the bar no longer fits at 1280 px")
	}
	// Loading a book with no ebook hides it.
	tab.Eval(`window.tsPlayer.load(`+jsString(other.ID)+`)`, nil)
	tab.WaitFor(`window.tsPlayer.state().bookId === `+jsString(other.ID), 10*time.Second)
	var hidden bool
	tab.Eval(`document.querySelector("[data-ts-bar-read]").hidden && document.querySelector("[data-ts-full-read]").hidden`, &hidden)
	if !hidden {
		t.Error("the icon stays after loading a book with no ebook")
	}

	// 390 px: the full player has it, right of Chapters; the mini row is unchanged.
	phone := fullpOpen(t, e, 390, 844, true)
	var mini bool
	phone.Eval(`document.querySelector("[data-ts-bar-read]").offsetParent === null`, &mini)
	if !mini {
		t.Error("the mini row shows the book icon on a phone")
	}
	fullpOpenPlayer(t, phone)
	var full fmt2Icon
	phone.Eval(`(() => {
	  const a = document.querySelector("[data-ts-full-read]"), c = document.querySelector("[data-ts-open-chapters]");
	  const r = a.getBoundingClientRect(), cr = c.getBoundingClientRect();
	  return { Hidden: a.hidden, Shown: a.offsetParent !== null, Href: a.getAttribute("href"), Name: a.textContent.trim(),
	    Left: r.left, Right: r.right, ChapRight: cr.right, Width: r.width, Height: r.height };
	})()`, &full)
	if full.Hidden || !full.Shown || full.Href != "/books/"+e.book.ID+"/read" || full.Name != "Read this book" {
		t.Errorf("full player item: %+v", full)
	}
	if full.Left < full.ChapRight-1 {
		t.Errorf("the book item is not right of Chapters: left %.1f, chapters right %.1f", full.Left, full.ChapRight)
	}
	if full.Width < 44 || full.Height < 44 || full.Right > 390 {
		t.Errorf("the item does not fit: %+v", full)
	}
}

func jsString(s string) string { return `"` + s + `"` }
