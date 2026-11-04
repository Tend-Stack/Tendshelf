//go:build dev

package web

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEyesFreeVisual captures Eyes-free (M3b) at 390 (beside the EyesFreePhone board), 768, 1024, 1440
// and 390 on its side (844 x 390), in dark and light, plus the dimmed screen at 390. It also checks
// that nothing overflows and every zone is far above 44 px. The board is copied beside the captures.
func TestEyesFreeVisual(t *testing.T) {
	out := visualDir(t, "chromium")
	if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "reader", "EyesFreePhone.png")); err == nil {
		if dst, err := os.Create(filepath.Join(out, "eyes-free-board-EyesFreePhone.png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}
	e := playerApp(t)
	sizes := []struct {
		name string
		w, h int
	}{{"390", 390, 844}, {"768", 768, 1024}, {"1024", 1024, 768}, {"1440", 1440, 900}, {"390-landscape", 844, 390}}
	for _, size := range sizes {
		tab := fullpOpen(t, e, size.w, size.h, true)
		fullpOpenPlayer(t, tab)
		toggle := "[data-ts-full-toggle]"
		if size.w >= 1280 {
			toggle = "#player [data-ts-toggle]" // from 1280 px the panel has no transport; the bar plays
		}
		bmkeyPlay(t, tab, toggle)
		efOpenMode(t, tab)
		time.Sleep(450 * time.Millisecond)
		for _, theme := range []string{"dark", "light"} {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+fmt.Sprintf("%q", theme)+`)`, nil)
			time.Sleep(250 * time.Millisecond)
			var m struct {
				Over  bool
				Small int
			}
			tab.Eval(`({Over: document.documentElement.scrollWidth > innerWidth || document.querySelector("#eyes-free").scrollHeight > document.querySelector("#eyes-free").clientHeight,
			  Small: [...document.querySelectorAll("#eyes-free button")].filter((b) => { const r = b.getBoundingClientRect(); return r.width < 44 || r.height < 44; }).length})`, &m)
			if m.Over || m.Small != 0 {
				t.Errorf("%s %s: overflow %v, targets under 44 px %d", size.name, theme, m.Over, m.Small)
			}
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("eyes-free-%s-%s.png", theme, size.name)))
			if size.name == "390" && theme == "dark" {
				tab.Eval(`document.getElementById("eyes-free").classList.add("is-dim")`, nil)
				time.Sleep(450 * time.Millisecond)
				tab.Screenshot(filepath.Join(out, "eyes-free-dark-390-dimmed.png"))
				tab.Eval(`document.getElementById("eyes-free").classList.remove("is-dim")`, nil)
				time.Sleep(450 * time.Millisecond)
			}
		}
	}
}
