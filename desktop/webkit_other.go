//go:build !linux || gtk3

package main

// initWebKit does nothing off Linux: macOS and Windows keep cookies and handle downloads in their own
// web views. Those platforms are not verified (plan M6, out of scope).
func initWebKit(cookieFile string) error { return nil }
