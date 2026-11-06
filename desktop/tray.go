package main

import (
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayActions are what the tray does. Each one is a plain function, so tests call them without a
// window or a desktop.
type trayActions struct {
	show      func()
	hide      func()
	playPause func() // pauses the book that is playing (never starts one: decision 0020)
	back      func()
	forward   func()
	quit      func()
}

// trayEntry is one line of the tray menu. A disabled line only says something.
type trayEntry struct {
	label    string
	run      func()
	disabled bool
	divider  bool
}

// trayEntries is the tray menu in order (plan X4, point 3). With a book loaded it shows the title and
// the chapter and place, then Pause (only while it plays) and the two skips. Nothing playing: Show and
// Quit. The tray never starts a book, because WebKit refuses sound the page did not ask for.
func trayEntries(a trayActions, s nowState) []trayEntry {
	var out []trayEntry
	if s.Title != "" {
		out = append(out, trayEntry{label: plainWords(s.Title), disabled: true})
		if line := plainWords(joinLine(s.Chapter, s.Left)); line != "" {
			out = append(out, trayEntry{label: line, disabled: true})
		}
		out = append(out, trayEntry{divider: true})
		if s.Playing {
			out = append(out, trayEntry{label: "Pause", run: a.playPause})
		}
		out = append(out,
			trayEntry{label: "Skip back", run: a.back},
			trayEntry{label: "Skip forward", run: a.forward},
			trayEntry{divider: true},
		)
	}
	return append(out,
		trayEntry{label: "Show Tendshelf", run: a.show},
		trayEntry{label: "Quit Tendshelf", run: a.quit},
	)
}

// joinLine puts two non-empty parts side by side.
func joinLine(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " · " + b
}

// trayTooltip is the hover text: the app's name, or the book that is on.
func trayTooltip(s nowState) string {
	switch {
	case s.Title == "":
		return "Tendshelf"
	case s.Playing:
		return plainWords(s.Title)
	}
	return plainWords(s.Title) + " (paused)"
}

// toggleWindow shows a hidden window and hides a visible one. A click on the tray icon does this.
func toggleWindow(visible func() bool, a trayActions) {
	if visible() {
		a.hide()
		return
	}
	a.show()
}

// trayView is the live icon: it rebuilds its menu when the book changes.
type trayView struct {
	app     *application.App
	tray    *application.SystemTray
	actions trayActions
	last    nowState
}

// Update shows the new state in the menu and the tooltip. It does nothing when nothing the tray
// shows has changed, so the menu is not rebuilt for every second of progress.
func (t *trayView) Update(s nowState) {
	s.Theme, s.CanAdd, s.Stop = "", false, "" // the tray shows none of these
	if s == t.last {
		return
	}
	t.last = s
	menu := t.app.Menu.New()
	for _, e := range trayEntries(t.actions, s) {
		if e.divider {
			menu.AddSeparator()
			continue
		}
		item := menu.Add(e.label)
		if e.disabled {
			item.SetEnabled(false)
			continue
		}
		run := e.run
		item.OnClick(func(*application.Context) { run() })
	}
	t.tray.SetMenu(menu)
	menu.Update()
	t.tray.SetTooltip(trayTooltip(s))
}

// setupTray adds the tray icon. Wails registers with the StatusNotifier watcher in the background;
// on a desktop without one the icon simply does not appear and the window works as before. Closing
// the window quits the app unless Keep playing is on, and that is offered only when a watcher exists,
// so a hidden app is never stranded.
func setupTray(ui *windowUI) *trayView {
	a := trayActions{
		show:      ui.showWindow,
		hide:      func() { ui.win.Hide() },
		playPause: func() { ui.bridge.Send("toggle") },
		back:      func() { ui.bridge.Send("back") },
		forward:   func() { ui.bridge.Send("forward") },
		quit:      ui.quit,
	}
	tray := ui.app.SystemTray.New()
	if light, dark := iconBytes(trayIconLight), iconBytes(trayIconDark); light != nil && dark != nil {
		tray.SetIcon(light)
		tray.SetDarkModeIcon(dark)
	} else {
		slog.Warn("tray icons missing")
	}
	view := &trayView{app: ui.app, tray: tray, actions: a}
	view.last = nowState{Title: "\x00"} // forces the first Update to build the menu
	view.Update(nowState{})
	tray.OnClick(func() { toggleWindow(ui.win.IsVisible, a) })
	return view
}
