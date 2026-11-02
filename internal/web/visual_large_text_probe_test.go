//go:build dev

package web

import (
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// ltProbeAt runs the probes against the window width the test asked for. A phone emulation widens its
// layout viewport to fit an overflowing page, so innerWidth would hide the very overflow being looked for.
func ltProbeAt(tab *cdp.Tab, w int, m *ltProbe) {
	tab.Eval(fmt.Sprintf("window.__ltW = %d", w), nil)
	tab.Eval(ltProbeJS, m)
}
