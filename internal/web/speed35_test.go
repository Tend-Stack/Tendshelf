package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestSpeed35Setting: the usual speed saves at 3.5 and is refused just above it.
func TestSpeed35Setting(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	for _, c := range []struct {
		speed string
		code  int
	}{{"3.5", http.StatusNoContent}, {"3.55", http.StatusUnprocessableEntity}} {
		if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"speed": {c.speed}}, true); rec.Code != c.code {
			t.Errorf("speed %s = %d, want %d", c.speed, rec.Code, c.code)
		}
	}
	if got := settingsOf(t, a).Speed; got != 3.5 {
		t.Errorf("saved speed = %v, want 3.5", got)
	}
}

// TestSpeed35Options: the settings select lists 3.05 to 3.5 and nothing above.
func TestSpeed35Options(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	body := a.do(http.MethodGet, "/me/settings", nil, nil, ck).Body.String()
	for _, want := range []string{`value="3.05"`, `value="3.50"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %s", want)
		}
	}
	if strings.Contains(body, `value="3.55"`) {
		t.Error("settings page offers 3.55")
	}
}
