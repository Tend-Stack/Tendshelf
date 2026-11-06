package main

import "github.com/wailsapp/wails/v3/pkg/application"

// menuActions are what the menu does. Each one is a plain function, so tests call them without a
// window.
type menuActions struct {
	change    func() // back to the picker
	quit      func()
	zoomIn    func()
	zoomOut   func()
	zoomReset func()
}

// zoomKeys are the window's key bindings for text size, WCAG 1.4.4: zoom must reach 200 %.
// "ctrl+=" is the same key as plus without Shift on most keyboards.
func zoomKeys(a menuActions) map[string]func(application.Window) {
	run := func(f func()) func(application.Window) { return func(application.Window) { f() } }
	return map[string]func(application.Window){
		"ctrl+plus": run(a.zoomIn),
		"ctrl+=":    run(a.zoomIn),
		"ctrl+-":    run(a.zoomOut),
		"ctrl+0":    run(a.zoomReset),
		// The menu row is gone on Linux, and Quit lived there (decision 0023, point 5).
		"ctrl+q": run(a.quit),
	}
}

// buildMenu fills the application menu: Tendshelf, Edit and View.
func buildMenu(m *application.Menu, a menuActions) {
	app := m.AddSubmenu("Tendshelf")
	app.Add("Change mode or server…").OnClick(func(*application.Context) { a.change() })
	app.AddSeparator()
	app.Add("Quit").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) { a.quit() })

	m.AddRole(application.EditMenu)

	view := m.AddSubmenu("View")
	view.Add("Zoom in").SetAccelerator("CmdOrCtrl+plus").OnClick(func(*application.Context) { a.zoomIn() })
	view.Add("Zoom out").SetAccelerator("CmdOrCtrl+-").OnClick(func(*application.Context) { a.zoomOut() })
	view.Add("Actual size").SetAccelerator("CmdOrCtrl+0").OnClick(func(*application.Context) { a.zoomReset() })
}
