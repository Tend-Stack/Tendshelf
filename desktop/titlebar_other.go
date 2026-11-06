//go:build !linux || gtk3

package main

import (
	"errors"
	"unsafe"
)

// hideTitlebar is for Linux only: macOS and Windows chrome needs its own check (plan, out of scope).
func hideTitlebar(unsafe.Pointer) error { return errors.New("not supported on this platform") }
