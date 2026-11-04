//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/align"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// TestTextAlignVisual captures preparing read-along on the Text tab (M5 R3 P5) beside the board
// screens/reader/PreparingPhone: the suggestion with "Pair and prepare read-along", a running alignment
// (the chapter bar, the stage, the time left), a held one with Resume and Discard, and the results (ready,
// partial with a note, failed). It runs at 390, 768, 1024 and 1440 px in dark and light and fails on
// horizontal overflow or a target under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestTextAlignVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"pair", "preparing", "held", "done", "partial", "failed"} {
		l := newTalignLab(t, true)
		ctx := context.Background()
		if state == "pair" {
			if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, l.file.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			al, err := l.svc2.Start(ctx, l.ed.ID, l.book.ID, l.ownerID)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "preparing":
				l.setRow(t, al.ID, align.StateAligning, "", "[]", 0)
				if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'running', lease_until = 9999999999999, progress_json = ? WHERE id = ?`,
					`{"stage":"aligning","chaptersDone":2,"chaptersTotal":4,"leftMs":420000}`, al.JobID); err != nil {
					t.Fatal(err)
				}
			case "held":
				l.setRow(t, al.ID, align.StateHeld, align.HoldAudioChanged, "[]", 0)
				if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'held' WHERE id = ?`, al.JobID); err != nil {
					t.Fatal(err)
				}
			case "done":
				l.setRow(t, al.ID, align.StateDone, "", "[]", 0.97)
			case "partial":
				l.setRow(t, al.ID, align.StatePartial, "", `["abridged","different_edition"]`, 0.64)
			case "failed":
				l.setRow(t, al.ID, align.StateFailed, align.CodeNoMatch, "[]", 0)
			}
		}
		base := themedServer(t, l.a.signedIn(t))
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("textalign-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + l.path("") + "?theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-ebook") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("textalign-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}

// TestAdminRuntimesVisual captures the word-timing page: nothing installed (the steps are open), one language
// ready and one with a problem, and a check running. Same widths, themes and checks as the Text tab.
func TestAdminRuntimesVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"notset", "mixed", "checking"} {
		l := newArtLab(t)
		path := "/admin/runtimes"
		switch state {
		case "mixed", "checking":
			l.install(t)
			l.checked(t, "en", "")
			l.checked(t, "es", alignrt.CodeModelMismatch)
		}
		if state == "checking" {
			id, err := l.a.runner.Enqueue(context.Background(), alignrt.KindCheck, alignrt.CheckPayload{Language: "es"}, jobs.EnqueueOptions{})
			if err != nil {
				t.Fatal(err)
			}
			path += "?check=" + id
		}
		base := themedServer(t, l.a.signedIn(t))
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("runtimes-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					sep := "?"
					if state == "checking" {
						sep = "&"
					}
					tab.Navigate(base + path + sep + "theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-runtimes") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("runtimes-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
