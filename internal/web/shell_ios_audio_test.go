package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// I2a (decision 0048): the iPhone app's window loads the two native scripts it implements, and a browser,
// the desktop shell and an Android window are unchanged. Names are prefixed TestIOSAudio.

func TestIOSAudioScripts(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	all := []string{"js/native-audio.", "js/native-downloads.", "js/native-speech.", "js/native-feel."}
	cases := []struct {
		name   string
		cookie *http.Cookie
		want   []string
	}{
		{"ios", a1IOS, []string{"js/native-audio.", "js/native-feel."}},
		{"android", &http.Cookie{Name: shellCookie, Value: "android"}, all},
		{"desktop", &http.Cookie{Name: shellCookie, Value: "desktop"}, nil},
		{"none", nil, nil},
	}
	for _, c := range cases {
		cks := []*http.Cookie{ck}
		if c.cookie != nil {
			cks = append(cks, c.cookie)
		}
		rec := a.do(http.MethodGet, "/", nil, nil, cks...)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: GET / = %d", c.name, rec.Code)
		}
		for _, s := range all {
			want := false
			for _, w := range c.want {
				want = want || w == s
			}
			if got := strings.Contains(body, s); got != want {
				t.Errorf("%s: page has %q = %v, want %v", c.name, s, got, want)
			}
		}
	}
	// The android window keeps today's order.
	rec := a.do(http.MethodGet, "/", nil, nil, ck, &http.Cookie{Name: shellCookie, Value: "android"})
	body := rec.Body.String()
	last := -1
	for _, s := range all {
		at := strings.Index(body, s)
		if at < last {
			t.Errorf("android: %q is out of order", s)
		}
		last = at
	}
}

func TestIOSAudioPlayerWords(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	for name, c := range map[string]struct {
		cookie *http.Cookie
		want   bool
	}{
		"ios":     {a1IOS, true},
		"android": {&http.Cookie{Name: shellCookie, Value: "android"}, true},
		"desktop": {&http.Cookie{Name: shellCookie, Value: "desktop"}, false},
		"browser": {nil, false},
	} {
		cks := []*http.Cookie{ck}
		if c.cookie != nil {
			cks = append(cks, c.cookie)
		}
		rec := a.do(http.MethodGet, "/", nil, nil, cks...)
		if got := strings.Contains(rec.Body.String(), "data-ts-msg-native-failed"); got != c.want {
			t.Errorf("%s: data-ts-msg-native-failed = %v, want %v", name, got, c.want)
		}
	}
}

func TestIOSAudioSettings(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	ios := a.do(http.MethodGet, "/me/settings", nil, nil, ck, a1IOS).Body.String()
	for _, name := range []string{`name="skip_quiet"`, `name="auto_download"`} {
		if strings.Contains(ios, name) {
			t.Errorf("ios settings has %s: skip silence and the next-book download stay Android-only", name)
		}
	}
	and := a.do(http.MethodGet, "/me/settings", nil, nil, ck, &http.Cookie{Name: shellCookie, Value: "android"}).Body.String()
	for _, name := range []string{`name="skip_quiet"`, `name="auto_download"`} {
		if !strings.Contains(and, name) {
			t.Errorf("android settings lacks %s", name)
		}
	}
}

func TestIOSAudioNativeScriptsList(t *testing.T) {
	for _, c := range []struct {
		shell string
		app   bool
		list  []string
	}{
		{"android", true, []string{"native-audio.js", "native-downloads.js", "native-speech.js", "native-feel.js"}},
		{"ios", true, []string{"native-audio.js", "native-feel.js"}},
		{"desktop", false, nil},
		{"", false, nil},
		{"browser", false, nil},
		{"IOS", false, nil},
	} {
		if got := components.IsAppShell(c.shell); got != c.app {
			t.Errorf("IsAppShell(%q) = %v, want %v", c.shell, got, c.app)
		}
		got := components.NativeScripts(c.shell)
		if strings.Join(got, ",") != strings.Join(c.list, ",") {
			t.Errorf("NativeScripts(%q) = %v, want %v", c.shell, got, c.list)
		}
	}
}
