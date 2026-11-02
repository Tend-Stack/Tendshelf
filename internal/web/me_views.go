package web

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// deviceLabel names a browser from its User-Agent: "Chrome on Android", "Safari", "Linux" or, when
// nothing is recognised, "A web browser". It is a label for a person to recognise their own
// devices, not a fingerprint: only a handful of well-known names are matched.
func deviceLabel(ua string) string {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(ua, s) {
				return true
			}
		}
		return false
	}
	var browser, system string
	switch {
	case has("Edg/", "EdgA/", "EdgiOS/"):
		browser = "Edge"
	case has("Firefox/", "FxiOS/"):
		browser = "Firefox"
	case has("Chrome/", "CriOS/"):
		browser = "Chrome"
	case has("Safari/") && !has("Chromium/"):
		browser = "Safari"
	}
	switch {
	case has("iPhone"):
		system = "iPhone"
	case has("iPad"):
		system = "iPad"
	case has("Android"):
		system = "Android"
	case has("Windows"):
		system = "Windows"
	case has("Macintosh", "Mac OS X"):
		system = "Mac"
	case has("Linux", "X11"):
		system = "Linux"
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	return msgUnknownDevice
}

// settingsView maps stored settings to the page.
func settingsView(csrf string, s users.Settings) pages.SettingsView {
	return pages.SettingsView{
		CSRF: csrf, Theme: s.Theme, Speed: s.Speed, SkipBackS: s.SkipBackS, SkipForwardS: s.SkipForwardS,
		AutoRewind: s.AutoRewind, NextInSeries: s.NextInSeries, PlayerTint: s.PlayerTint, BigButtons: s.BigButtons,
		TextSize: s.TextSize,
	}
}

// settingsFromForm applies a posted form to the current settings. A key that is absent keeps its
// current value, an unknown key is ignored, and a value outside its set makes ok false. A switch
// posts a hidden 0 before its checkbox's 1, so the last value of a key is the one that counts.
func settingsFromForm(f url.Values, cur users.Settings) (next users.Settings, ok bool) {
	next = cur
	last := func(k string) (string, bool) {
		vs := f[k]
		if len(vs) == 0 {
			return "", false
		}
		return vs[len(vs)-1], true
	}
	ok = true
	flag := func(k string, dst *bool) {
		v, has := last(k)
		switch {
		case !has:
		case v == "1":
			*dst = true
		case v == "0":
			*dst = false
		default:
			ok = false
		}
	}
	flag("auto_rewind", &next.AutoRewind)
	flag("next_in_series", &next.NextInSeries)
	flag("player_tint", &next.PlayerTint)
	flag("big_buttons", &next.BigButtons)

	if v, has := last("speed"); has {
		sp, err := strconv.ParseFloat(v, 64)
		if err != nil {
			ok = false
		}
		next.Speed = sp
	}
	skip := func(k string, dst *int) {
		if v, has := last(k); has {
			n, err := strconv.Atoi(v)
			if err != nil {
				ok = false
			}
			*dst = n
		}
	}
	if v, has := last("text_size"); has {
		next.TextSize = v // Validate refuses anything but standard and large
	}
	skip("skip_back", &next.SkipBackS)
	skip("skip_forward", &next.SkipForwardS)

	auto, hasAuto := last("theme_auto")
	pick, hasPick := last("theme")
	switch {
	case hasAuto && auto != "0" && auto != "1":
		ok = false
	case hasAuto && auto == "1":
		next.Theme = "system" // "Match my phone" overrides the tiles
	case hasPick:
		switch pick {
		case "dark", "light", "sepia", "contrast":
			next.Theme = pick
		default:
			ok = false
		}
	case hasAuto && cur.Theme == "system":
		next.Theme = "dark" // the switch went off with no tile posted
	}
	return next, ok && next.Validate() == nil
}
