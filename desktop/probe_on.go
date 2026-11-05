//go:build tendshelf_probe

package main

import "github.com/Tend-Stack/tendshelf/desktop/probe"

// attachProbe starts the scripted run (TENDSHELF_PROBE names the script) and returns the handler
// for its messages. The probe begins when the picker page says it has loaded.
func attachProbe(ui *windowUI, s *Session) func(message string) bool {
	return probe.Attach(
		func(js string) {
			if ui.win != nil {
				ui.win.ExecJS(js)
			}
		},
		s.Origin,
		s.Change,
		func() { ui.app.Quit() },
	)
}
