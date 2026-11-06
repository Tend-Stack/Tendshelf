package main

import (
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Fit to screen: the pages stop at 1,920 px (maxWidth), so on a wider screen the window never
// maximises; it is set to 1,920 wide, as high as the work area, and centred. fitTarget decides;
// this file does it to the real window and keeps windowTracker truthful.

// workArea is the work area (screen less panels, logical px) of the screen the window is on.
func (u *windowUI) workArea() (x, y, w, h int, ok bool) {
	screen, err := u.win.GetScreen()
	if err != nil || screen == nil {
		screen = u.app.Screen.GetPrimary()
	}
	if screen == nil {
		return 0, 0, 0, 0, false
	}
	a := screen.WorkArea
	if a.Width <= 0 || a.Height <= 0 {
		a = application.Rect{X: screen.Bounds.X, Y: screen.Bounds.Y, Width: screen.Size.Width, Height: screen.Size.Height}
	}
	return a.X, a.Y, a.Width, a.Height, a.Width > 0 && a.Height > 0
}

// toggleMaximise is the bar's Maximise button. On a screen no wider than maxWidth it is a true
// maximise, as before. On a wider one it fits the window, and a second press goes back.
func (u *windowUI) toggleMaximise() {
	if u.tracker.isFitted() {
		go u.unfit()
		return
	}
	if !u.win.IsMaximised() {
		if x, y, w, h, ok := u.workArea(); ok {
			if fx, fy, fw, fh, fit := fitTarget(x, y, w, h); fit {
				go u.fit(fx, fy, fw, fh) // off the main thread: it waits for the compositor
				return
			}
		}
	}
	u.win.ToggleMaximise()
}

// resizeTo sets the size of a window that is on screen and waits for it to land. The tracker
// ignores the resize events on the way, so the half-way sizes are never taken for the person's
// choice. With hold set, the window stays locked at that size until the main button is let go, or
// the compositor would give it the pointer's size back (the caller holds the tracker's claim). It returns the size the window actually has: the compositor may keep the height inside
// the work area. Must not run on the GTK thread, because it waits.
func (u *windowUI) resizeTo(w, h int, hold bool) (int, int) {
	handle := u.win.NativeWindow()
	locked := false
	application.InvokeSync(func() {
		u.win.SetSize(w, h)
		locked = setResizable(handle, false) == nil // see setResizable
	})
	// Wait until the size has stopped changing (the height lands after the width).
	lastW, lastH, still := -1, -1, 0
	for i := 0; i < 100 && still < 6; i++ {
		time.Sleep(20 * time.Millisecond)
		aw, ah := u.win.Size()
		if aw == lastW && ah == lastH {
			still++
		} else {
			lastW, lastH, still = aw, ah, 0
		}
	}
	for i := 0; hold && locked && i < 200 && pointerHeld(handle); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if locked {
		application.InvokeSync(func() { _ = setResizable(handle, true) })
	}
	time.Sleep(100 * time.Millisecond)
	return u.win.Size()
}

// fit sets the window to the fitted size and place. A window the system has maximised is first
// unmaximised, and the size waits for that to take effect, or the compositor would drop it. It does
// not rely on the unmaximise event, so it cannot loop with the maximise handler.
func (u *windowUI) fit(x, y, w, h int) {
	if !u.tracker.claim() {
		return
	}
	defer u.tracker.release()
	prevX, prevY := u.win.Position()
	if u.win.IsMaximised() {
		u.win.UnMaximise()
		for i := 0; i < 50 && u.win.IsMaximised(); i++ {
			time.Sleep(20 * time.Millisecond)
		}
	}
	aw, ah := u.resizeTo(w, h, false)
	u.win.SetPosition(x, y) // a no-op under Wayland: the compositor places the window
	u.tracker.fit(aw, ah, prevX, prevY)
	u.bridge.Send("maximised")
	slog.Info("window fitted to the screen", "width", aw, "height", ah, "wanted", w, "high", h)
}

// unfit goes back to the size (and, where the system allows, the place) from before the fit.
func (u *windowUI) unfit() {
	if !u.tracker.claim() {
		return
	}
	defer u.tracker.release()
	w, h, x, y := u.tracker.unfit()
	aw, ah := u.resizeTo(w, h, false)
	u.win.SetPosition(x, y)
	u.bridge.Send("restored")
	slog.Info("window back from the fit", "width", aw, "height", ah)
}

// limitWidth keeps a window the person is dragging at maxWidth: GTK 4 has no maximum size for a
// window on screen (Wails' MaxWidth only bounds the size it sets itself). Past the limit the window
// snaps back to it, which also ends that drag.
func (u *windowUI) limitWidth(w, h int) {
	if w <= maxWidth || u.win.IsMaximised() || !u.tracker.claim() {
		return
	}
	go func() {
		aw, ah := u.resizeTo(maxWidth, h, true)
		u.tracker.release()
		u.tracker.resized(aw, ah, false) // the size the person keeps, not the one they dragged to
		slog.Info("window held at its widest", "width", aw, "height", ah)
	}()
}

// maximiseNotFromButton handles a maximise the bar did not ask for (double-click, Super+Up, the
// window manager): on a wide screen it becomes the fit, and the answer is true. A fitted window
// ignores it.
func (u *windowUI) maximiseNotFromButton() bool {
	if x, y, w, h, ok := u.workArea(); ok {
		if fx, fy, fw, fh, fit := fitTarget(x, y, w, h); fit {
			go u.fit(fx, fy, fw, fh)
			return true
		}
	}
	return false
}
