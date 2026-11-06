//go:build tendshelf_probe

package main

import "github.com/Tend-Stack/tendshelf/desktop/probe"

// attachProbe starts the scripted run (TENDSHELF_PROBE names the script) and returns the handler
// for its messages. The probe begins when the picker page says it has loaded.
func attachProbe(ui *windowUI, s *Session) func(message string) bool {
	handler := probe.Attach(
		func(js string) {
			if ui.win != nil {
				ui.win.ExecJS(js)
			}
		},
		s.Origin,
		s.Change,
		func() { ui.app.Quit() },
	)
	if handler == nil {
		return nil
	}
	// The "now" plan hides the window the way Keep playing does; a headless session never takes focus away.
	return func(message string) bool {
		if message == "tendshelf:probe hideNow" {
			ui.win.Hide()
			return true
		}
		return handler(message)
	}
}
