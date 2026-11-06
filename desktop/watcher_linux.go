package main

import (
	"github.com/godbus/dbus/v5"
)

// hasTrayWatcher reports whether something on the session bus can show a tray icon (decision 0023,
// point 5). Without one, "Keep playing when I close the window" is not offered, so a hidden app can
// never be stranded with no way back.
func hasTrayWatcher() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	var has bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has)
	return err == nil && has
}
