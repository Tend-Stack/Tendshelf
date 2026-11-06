//go:build !linux || gtk3

package main

import (
	"errors"
	"unsafe"
)

// setResizable is for GTK 4 only; elsewhere SetSize already resizes a window that is on screen.
func setResizable(unsafe.Pointer, bool) error { return errors.New("not supported on this platform") }

// pointerHeld cannot tell outside GTK 4.
func pointerHeld(unsafe.Pointer) bool { return false }
