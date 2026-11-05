//go:build linux && !gtk3

package main

/*
#cgo pkg-config: webkitgtk-6.0
#include <webkit/webkit.h>
#include <glib.h>
#include <stdlib.h>
#include <string.h>

// Wails exposes neither of these (decision 0020, point 7), so the two calls are made here, on the
// default network session that Wails' webview uses.

// ts_unique_path returns dir/name, or dir/stem (1).ext, (2) and so on when the name is taken.
// The caller frees the result.
static char *ts_unique_path(const char *dir, const char *suggested) {
  // Only a bare file name: a download never picks its own folder.
  char *name = g_path_get_basename(suggested && *suggested ? suggested : "download");
  const char *dot = strrchr(name, '.');
  gsize stem_len = (dot && dot != name) ? (gsize)(dot - name) : strlen(name);
  const char *ext = (dot && dot != name) ? dot : "";
  char *stem = g_strndup(name, stem_len);
  char *path = g_build_filename(dir, name, NULL);
  for (int n = 1; g_file_test(path, G_FILE_TEST_EXISTS) && n < 10000; n++) {
    g_free(path);
    char *candidate = g_strdup_printf("%s (%d)%s", stem, n, ext);
    path = g_build_filename(dir, candidate, NULL);
    g_free(candidate);
  }
  g_free(stem);
  g_free(name);
  return path;
}

static gboolean ts_decide_destination(WebKitDownload *download, const gchar *suggested, gpointer data) {
  const char *dir = g_get_user_special_dir(G_USER_DIRECTORY_DOWNLOAD);
  if (!dir) dir = g_get_home_dir();
  g_mkdir_with_parents(dir, 0700);
  char *path = ts_unique_path(dir, suggested);
  webkit_download_set_destination(download, path);
  g_free(path);
  return TRUE;
}

static void ts_download_started(WebKitNetworkSession *session, WebKitDownload *download, gpointer data) {
  g_signal_connect(download, "decide-destination", G_CALLBACK(ts_decide_destination), NULL);
}

static void ts_init_webkit(const char *cookie_file) {
  WebKitNetworkSession *session = webkit_network_session_get_default();
  WebKitCookieManager *cookies = webkit_network_session_get_cookie_manager(session);
  webkit_cookie_manager_set_persistent_storage(cookies, cookie_file, WEBKIT_COOKIE_PERSISTENT_STORAGE_SQLITE);
  g_signal_connect(session, "download-started", G_CALLBACK(ts_download_started), NULL);
}
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

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
	path := C.CString(cookieFile)
	defer C.free(unsafe.Pointer(path))
	C.ts_init_webkit(path)
	return nil
}
