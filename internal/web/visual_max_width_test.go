//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestVisualMaxWidth captures the shell with the bar and the Now playing panel on a 3840 px screen, wider
// than the app's 2560 px column, in dark and light. The sides are the page ground; the column is centred.
func TestVisualMaxWidth(t *testing.T) {
	out := visualDir(t, "chromium")
	e := nowPanelEnv(t)
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			streamExec(t, e.a, `INSERT OR REPLACE INTO user_settings(user_id, key, value_json, updated_at) VALUES (?, 'ui.theme', ?, 1)`, e.ownerID, `"`+theme+`"`)
			tab := fullpOpen(t, e, 3840, 1080, true)
			fullpOpenPlayer(t, tab)
			tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__desc") !== null`, 5*time.Second)
			time.Sleep(300 * time.Millisecond)
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("maxwidth-%s-3840.png", theme)))
		})
	}
}
