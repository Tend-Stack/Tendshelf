//go:build linux && !gtk3

package main

import (
	"errors"
	"unsafe"

	"github.com/ebitengine/purego"
)

// setResizable turns the user's ability to resize the window on or off. A GTK 4 window that is
// already on screen ignores gtk_window_set_default_size when the new size is smaller than the
// current one, and GTK 4 has no maximum size for a window the user drags. A window that is not
// resizable takes its default size at once, so resizeTo (windowfit.go) switches resizing off for a
// moment, lets the size land, and switches it on again. Must run on the GTK thread.
func setResizable(window unsafe.Pointer, on bool) error {
	if window == nil {
		return errors.New("the window has no native handle")
	}
	l, err := openNativeLibs()
	if err != nil {
		return err
	}
	var set func(window uintptr, resizable int32)
	purego.RegisterLibFunc(&set, l.gtk, "gtk_window_set_resizable")
	v := int32(0)
	if on {
		v = 1
	}
	set(uintptr(window), v)
	return nil
}

// pointerHeld reports whether the main mouse button is down over the window. While it is, an
// edge or corner drag is under way and the compositor keeps handing the window the pointer's
// size, so a size set now would be undone. False when it cannot tell.
func pointerHeld(window unsafe.Pointer) bool {
	if window == nil {
		return false
	}
	l, err := openNativeLibs()
	if err != nil {
		return false
	}
	var (
		surfaceOf  func(native uintptr) uintptr
		defDisplay func() uintptr
		defSeat    func(display uintptr) uintptr
		pointerOf  func(seat uintptr) uintptr
		position   func(surface, device uintptr, x, y *float64, mask *uint32) int32
	)
	purego.RegisterLibFunc(&surfaceOf, l.gtk, "gtk_native_get_surface")
	purego.RegisterLibFunc(&defDisplay, l.gtk, "gdk_display_get_default")
	purego.RegisterLibFunc(&defSeat, l.gtk, "gdk_display_get_default_seat")
	purego.RegisterLibFunc(&pointerOf, l.gtk, "gdk_seat_get_pointer")
	purego.RegisterLibFunc(&position, l.gtk, "gdk_surface_get_device_position")
	surface := surfaceOf(uintptr(window))
	device := pointerOf(defSeat(defDisplay()))
	if surface == 0 || device == 0 {
		return false
	}
	var x, y float64
	var mask uint32
	position(surface, device, &x, &y, &mask)
	const button1 = 1 << 8 // GDK_BUTTON1_MASK
	return mask&button1 != 0
}
