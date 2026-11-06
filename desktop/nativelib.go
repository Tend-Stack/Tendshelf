//go:build linux && !gtk3

package main

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// The window's own calls into GTK 4, WebKitGTK 6, GObject and GLib go through purego, so desktop/
// holds no C and the same file works in the cgo build and the CGO-free one (decision 0023, point 8).
// The libraries are the ones the window already needs; they are opened once, here, and a missing one
// is reported in plain words naming the package to install.

// nativeLibs are the handles of the opened libraries.
type nativeLibs struct {
	gtk, webkit, gobject, glib uintptr
}

var (
	libsOnce sync.Once
	libs     nativeLibs
	libsErr  error
)

// openNativeLibs opens every library once. A later call returns the first call's answer.
func openNativeLibs() (nativeLibs, error) {
	libsOnce.Do(func() {
		libs, libsErr = openAll()
	})
	return libs, libsErr
}

func openAll() (nativeLibs, error) {
	var l nativeLibs
	wanted := []struct {
		handle  *uintptr
		file    string
		pkg     string
		library string
	}{
		{&l.gtk, "libgtk-4.so.1", "libgtk-4-1", "GTK 4"},
		{&l.webkit, "libwebkitgtk-6.0.so.4", "libwebkitgtk-6.0-4", "WebKitGTK 6.0"},
		{&l.gobject, "libgobject-2.0.so.0", "libglib2.0-0", "GObject"},
		{&l.glib, "libglib-2.0.so.0", "libglib2.0-0", "GLib"},
	}
	for _, w := range wanted {
		h, err := purego.Dlopen(w.file, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return nativeLibs{}, fmt.Errorf("Tendshelf needs %s, which is not installed (package %s): %w", w.library, w.pkg, err)
		}
		*w.handle = h
	}
	return l, nil
}
