//go:build linux && !gtk3

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Wails 3.1 exposes neither the cookie store nor downloads, in either backend (decision 0020, point 7),
// so the two calls are made here on WebKit's default network session, through purego. This one file
// replaces the C file and its purego twin: it works in the cgo build and the CGO-free one.

const (
	cookieStorageSQLite = 1 // WEBKIT_COOKIE_PERSISTENT_STORAGE_SQLITE
	downloadDirectory   = 2 // G_USER_DIRECTORY_DOWNLOAD
)

var (
	sessionGetDefault      func() uintptr
	sessionGetCookies      func(session uintptr) uintptr
	cookiesSetStorage      func(manager uintptr, file string, storage int32)
	signalConnectData      func(instance uintptr, signal string, handler, data, destroy uintptr, flags int32) uint64
	downloadSetDestination func(download uintptr, path string)
	userSpecialDir         func(directory int32) string

	// One callback of each: purego never frees them, so they are created once, here.
	onDownloadStarted = purego.NewCallback(func(session, download, data uintptr) uintptr {
		signalConnectData(download, "decide-destination", onDecideDestination, 0, 0, 0)
		return 0
	})
	onDecideDestination uintptr
)

func init() {
	onDecideDestination = purego.NewCallback(func(download uintptr, suggested unsafe.Pointer, data uintptr) uintptr {
		dir := userSpecialDir(downloadDirectory)
		if dir == "" {
			dir, _ = os.UserHomeDir()
		}
		_ = os.MkdirAll(dir, 0o700)
		downloadSetDestination(download, uniquePath(dir, cString(suggested)))
		return 1 // TRUE: handled
	})
}

// cString copies a NUL-terminated string that belongs to the caller.
func cString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// uniquePath returns dir/name, or dir/stem (1).ext, (2) and so on when the name is taken. Only a bare
// file name is used: a download never picks its own folder.
func uniquePath(dir, suggested string) string {
	name := filepath.Base(suggested)
	if suggested == "" || name == "." || name == string(filepath.Separator) {
		name = "download"
	}
	ext := filepath.Ext(name)
	if ext == name {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for n := 1; n < 10000; n++ {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
	}
	return path
}

// initWebKit keeps the window's cookies on disk and saves attachments to the download folder.
// It must run before application.New, while WebKit's default session is still unused.
func initWebKit(cookieFile string) error {
	dir := filepath.Dir(cookieFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the cookie folder: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("protect the cookie folder: %w", err)
	}
	l, err := openNativeLibs()
	if err != nil {
		return err
	}
	purego.RegisterLibFunc(&sessionGetDefault, l.webkit, "webkit_network_session_get_default")
	purego.RegisterLibFunc(&sessionGetCookies, l.webkit, "webkit_network_session_get_cookie_manager")
	purego.RegisterLibFunc(&cookiesSetStorage, l.webkit, "webkit_cookie_manager_set_persistent_storage")
	purego.RegisterLibFunc(&downloadSetDestination, l.webkit, "webkit_download_set_destination")
	purego.RegisterLibFunc(&signalConnectData, l.gobject, "g_signal_connect_data")
	purego.RegisterLibFunc(&userSpecialDir, l.glib, "g_get_user_special_dir")

	session := sessionGetDefault()
	if session == 0 {
		return errors.New("WebKit has no default network session")
	}
	cookiesSetStorage(sessionGetCookies(session), cookieFile, cookieStorageSQLite)
	signalConnectData(session, "download-started", onDownloadStarted, 0, 0, 0)
	return nil
}
