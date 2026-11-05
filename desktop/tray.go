package main

import (
	"log/slog"

	"github.com/Tend-Stack/tendshelf/internal/web/static"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayIcon is the picture in the notification area; it is the app icon the pages already ship.
const trayIcon = "img/icon-192.png"

// trayActions are what the tray does. Each one is a plain function, so tests call them without a
// window or a desktop.
type trayActions struct {
	show      func()
	hide      func()
	playPause func()
	quit      func()
}

// trayEntry is one line of the tray menu.
type trayEntry struct {
	label string
	run   func()
}

// trayEntries is the tray menu in order (plan D5, point 4).
func trayEntries(a trayActions) []trayEntry {
	return []trayEntry{
		{"Show Tendshelf", a.show},
		{"Hide Tendshelf", a.hide},
		{"Play or pause", a.playPause},
		{"Quit Tendshelf", a.quit},
	}
}

// toggleWindow shows a hidden window and hides a visible one. A click on the tray icon does this.
func toggleWindow(visible func() bool, a trayActions) {
	if visible() {
		a.hide()
		return
	}
	a.show()
}

// setupTray adds the tray icon. Wails registers with the StatusNotifier watcher in the background;
// on a desktop without one the icon simply does not appear and the window works as before. Closing
// the window quits the app, so a desktop without a tray never strands a hidden app.
func setupTray(ui *windowUI, assets *static.Set) {
	a := trayActions{
		show: func() {
			ui.win.Show()
			ui.win.Restore()
			ui.win.Focus()
		},
		hide:      func() { ui.win.Hide() },
		playPause: func() { ui.bridge.Send("toggle") },
		quit:      func() { ui.app.Quit() },
	}
	tray := ui.app.SystemTray.New()
	tray.SetTooltip("Tendshelf")
	if icon, ok := assets.Asset(trayIcon); ok {
		tray.SetIcon(icon.Body)
	} else {
		slog.Warn("tray icon missing", "name", trayIcon)
	}
	menu := ui.app.Menu.New()
	for _, e := range trayEntries(a) {
		menu.Add(e.label).OnClick(func(*application.Context) { e.run() })
	}
	tray.SetMenu(menu)
	tray.OnClick(func() { toggleWindow(ui.win.IsVisible, a) })
}
