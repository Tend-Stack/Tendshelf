package main

import (
	"log/slog"
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

	// maxWidth is the widest the window may be. The pages stop growing at --size-app-max (120rem,
	// 1,920 px), so a wider window only shows a blank strip. Sizes here are logical pixels, the same
	// as CSS pixels at 100 % page zoom: at a higher zoom the page has fewer CSS pixels than the window
	// (no strip); at a lower zoom, below 100 %, a 1,920 window holds more than 1,920 CSS pixels and a
	// strip returns beside the page. The screen's own scale (HiDPI) is already out of these numbers.
	maxWidth = 1920

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
// never below the window's minimum, never larger than the screen or maxWidth. A screen of 0 means unknown.
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
	w = min(w, maxWidth)
	return max(w, minWidth), max(h, minHeight)
}

// fitTarget is what "fit to screen" does with a work area (the screen less panels and docks, logical
// px, origin x,y). On an area no wider than maxWidth it answers fit=false: the window really
// maximises, as it always did. On a wider one the window becomes maxWidth wide and as high as the
// area, centred across it, so a 3440 or 5120 wide monitor shows no blank strip.
func fitTarget(workX, workY, workW, workH int) (x, y, w, h int, fit bool) {
	if workW <= maxWidth {
		return 0, 0, 0, 0, false
	}
	return workX + (workW-maxWidth)/2, workY, maxWidth, workH, true
}

// windowTracker remembers the last normal size and whether the window is maximised, so closing
// saves the size to return to, not the size of a maximised window.
type windowTracker struct {
	mu        sync.Mutex
	win       desktopshell.Window
	focused   bool
	maximised bool

	// fitted is true while the window is held at its fitted size on a wide screen (fitTarget). It
	// counts as maximised for the bar and for the saved state, while win keeps the size to go back
	// to. fitW, fitH is that fitted size; prevX, prevY the position to go back to.
	busy         bool // the window is being sized by us (fit, unfit, limit): its resize events are ours
	fitted       bool
	fitW, fitH   int
	prevX, prevY int
}

func newWindowTracker(saved desktopshell.Window) *windowTracker {
	return &windowTracker{win: saved, maximised: saved.Maximised}
}

// resized is called on every resize with the window's current size.
// It reports true when that resize ended a fitted window (the person dragged an edge), which is
// then an ordinary window of the size they chose.
func (t *windowTracker) resized(w, h int, maximised bool) (unfitted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.busy {
		return false
	}
	if t.fitted {
		if w == t.fitW { // the height may differ: the compositor keeps it inside the work area
			return false
		}
		t.fitted = false
		unfitted = true
	}
	t.maximised = maximised
	if !maximised && w > 0 && h > 0 {
		t.win.Width, t.win.Height = w, h
	}
	return unfitted
}

// claim takes the right to size the window ourselves; it fails while another sizing is under way.
func (t *windowTracker) claim() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.busy {
		return false
	}
	t.busy = true
	return true
}

func (t *windowTracker) release() {
	t.mu.Lock()
	t.busy = false
	t.mu.Unlock()
}

// fit marks the window as fitted to w by h, leaving from position x, y. The size to go back to is
// the last normal size already in win.
func (t *windowTracker) fit(w, h, fromX, fromY int) {
	t.mu.Lock()
	t.fitted, t.maximised = true, false
	t.fitW, t.fitH, t.prevX, t.prevY = w, h, fromX, fromY
	t.mu.Unlock()
}

// unfit ends the fitted state and answers the size and position to go back to.
func (t *windowTracker) unfit() (w, h, x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fitted = false
	return t.win.Width, t.win.Height, t.prevX, t.prevY
}

func (t *windowTracker) isFitted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fitted
}

// isMaximised is what the bar shows (Restore) and what closing saves: a real maximise or a fitted window.
func (t *windowTracker) isMaximised() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maximised || t.fitted
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

// snapshot is what closing saves.
func (t *windowTracker) snapshot() desktopshell.Window {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.win
	w.Maximised = t.maximised || t.fitted
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
		slog.Info("window: first page ready or fallback reached")
		r.show()
	})
}

// keepOnClose says whether closing the window hides it instead of quitting: only when the person
// chose Keep playing, a tray exists, this is not a real Quit, and a book is playing. A hidden app
// with nothing playing would only be forgotten.
func keepOnClose(keep, tray, quitting, playing bool) bool {
	return keep && tray && !quitting && playing
}

// saveGround writes the theme name into desktop.json so the next start opens on its ground.
func saveGround(path, theme string) error {
	cfg, err := desktopshell.Load(path)
	if err != nil {
		return err
	}
	if cfg.Ground == theme {
		return nil
	}
	cfg.Ground = theme
	return desktopshell.Save(path, cfg)
}

// saveKeepPlaying writes the Keep playing choice into desktop.json.
func saveKeepPlaying(path string, on bool) error {
	cfg, err := desktopshell.Load(path)
	if err != nil {
		return err
	}
	if cfg.KeepPlaying == on {
		return nil
	}
	cfg.KeepPlaying = on
	return desktopshell.Save(path, cfg)
}
