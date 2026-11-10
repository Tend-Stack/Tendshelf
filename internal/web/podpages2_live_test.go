package web

import (
	"strings"
	"testing"
)

// TestPodPages2LiveBarLogoBeforeFetch: a station whose logo job has not run yet still hands the player bar the
// logo address (it answers 404 until the file is kept and player.js asks again), so the tile is not left empty.
func TestPodPages2LiveBarLogoBeforeFetch(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	if _, err := w.a.st.Writer().Exec(`UPDATE radio_stations SET art_url = ? WHERE id = ?`, w.fx.pub.URL("logo.test", "/x.png"), id); err != nil {
		t.Fatal(err)
	}
	frag := w.get("/hx/player/radio/"+id, w.ben).Body.String()
	if !strings.Contains(frag, `data-cover-url="/radio/art/`+id+`?w=512"`) || strings.Contains(frag, "logo.test") {
		t.Errorf("player fragment before the logo is kept: %q", frag)
	}
	// A station with no logo address at all keeps the plain tile.
	plain := w.add(w.ben, w.fx.liveURL()+"?plain=1")
	if frag := w.get("/hx/player/radio/"+plain, w.ben).Body.String(); !strings.Contains(frag, `data-cover-url=""`) {
		t.Errorf("station without a logo: %q", frag)
	}
}
