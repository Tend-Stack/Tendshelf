package main

import (
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The window arrives ready (decision 0023, point 6): hidden until the first page has painted, on the
// ground of the saved theme, at the saved size.

const (
	defaultWidth, defaultHeight = 1280, 820
	minWidth, minHeight         = 390, 600

	// showFallback is how long a hidden window waits for a page's "ready" before it shows anyway,
	// so a failed page can never leave an invisible app.
	showFallback = 3 * time.Second
)

// grounds are --ground of the four themes in internal/web/static/css/tokens.css; a test keeps them equal.
var grounds = map[string]application.RGBA{
	"dark":     application.NewRGB(0x14, 0x11, 0x0e),
	"light":    application.NewRGB(0xf4, 0xee, 0xe3),
	"sepia":    application.NewRGB(0xe6, 0xd7, 0xbc),
	"contrast": application.NewRGB(0x00, 0x00, 0x00),
}

// groundColour is the window's background for a saved theme name; anything else is the dark ground.
func groundColour(name string) application.RGBA {
	if c, ok := grounds[name]; ok {
		return c
	}
	return grounds["dark"]
}

// fitWindow turns a saved size into one that fits the screen: the default when nothing was saved,
// never below the window's minimum, never larger than the screen. A screen of 0 means unknown.
func fitWindow(saved desktopshell.Window, screenW, screenH int) (w, h int) {
	w, h = saved.Width, saved.Height
	if w <= 0 || h <= 0 {
		w, h = defaultWidth, defaultHeight
	}
	if screenW > 0 && w > screenW {
		w = screenW
	}
	if screenH > 0 && h > screenH {
		h = screenH
	}
	return max(w, minWidth), max(h, minHeight)
}

// windowTracker remembers the last normal size and whether the window is maximised, so closing
// saves the size to return to, not the size of a maximised window.
type windowTracker struct {
	mu        sync.Mutex
	win       desktopshell.Window
	focused   bool
	maximised bool
}

func newWindowTracker(saved desktopshell.Window) *windowTracker {
	return &windowTracker{win: saved, maximised: saved.Maximised}
}

// resized is called on every resize with the window's current size.
func (t *windowTracker) resized(w, h int, maximised bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maximised = maximised
	if !maximised && w > 0 && h > 0 {
		t.win.Width, t.win.Height = w, h
	}
}

func (t *windowTracker) setMaximised(m bool) {
	t.mu.Lock()
	t.maximised = m
	t.mu.Unlock()
}

func (t *windowTracker) setFocused(f bool) {
	t.mu.Lock()
	t.focused = f
	t.mu.Unlock()
}

func (t *windowTracker) isFocused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.focused
}

func (t *windowTracker) isMaximised() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maximised
}

// snapshot is what closing saves.
func (t *windowTracker) snapshot() desktopshell.Window {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.win
	w.Maximised = t.maximised
	return w
}

// saveWindow writes the window's size into desktop.json, keeping every other setting.
func saveWindow(path string, w desktopshell.Window) error {
	cfg, err := desktopshell.Load(path)
	if err != nil {
		return err
	}
	if cfg.Window == w {
		return nil
	}
	cfg.Window = w
	return desktopshell.Save(path, cfg)
}

// revealer shows the window once: on the first page that says it is ready, or after the fallback.
type revealer struct {
	once sync.Once
	show func()
}

func newRevealer(show func(), fallback time.Duration) *revealer {
	r := &revealer{show: show}
	time.AfterFunc(fallback, r.Ready) // after a ready it finds the once spent and does nothing
	return r
}

// Ready shows the window the first time it is called; later calls do nothing.
func (r *revealer) Ready() {
	r.once.Do(func() {
		r.show()
	})
}
