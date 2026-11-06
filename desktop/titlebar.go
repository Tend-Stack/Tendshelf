//go:build linux && !gtk3

package main

import (
	"errors"
	"unsafe"

	"github.com/ebitengine/purego"
)

// hideTitlebar gives the window GTK's shadow, rounded corners and resize border but no bar of its
// own, so the page can draw one (decision 0023, point 2). It is the spike's three calls: an empty
// box, hidden, set as the window's titlebar. It never uses Frameless, which loses all three. The
// window must still be hidden, and the call must run on the GTK thread.
func hideTitlebar(window unsafe.Pointer) error {
	if window == nil {
		return errors.New("the window has no native handle yet")
	}
	l, err := openNativeLibs()
	if err != nil {
		return err
	}
	var (
		boxNew           func(orientation, spacing int32) uintptr
		widgetSetVisible func(widget uintptr, visible int32)
		setTitlebar      func(window, widget uintptr)
	)
	purego.RegisterLibFunc(&boxNew, l.gtk, "gtk_box_new")
	purego.RegisterLibFunc(&widgetSetVisible, l.gtk, "gtk_widget_set_visible")
	purego.RegisterLibFunc(&setTitlebar, l.gtk, "gtk_window_set_titlebar")

	box := boxNew(0, 0) // GTK_ORIENTATION_HORIZONTAL; a visible empty box still draws a 44 px strip
	widgetSetVisible(box, 0)
	setTitlebar(uintptr(window), box)
	return nil
}
