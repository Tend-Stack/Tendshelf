//go:build !linux

package main

// hasTrayWatcher: the tray is Linux's StatusNotifier story; elsewhere Keep playing is not offered.
func hasTrayWatcher() bool { return false }
