package web

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
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

// deviceIcon is "phone" for a phone or tablet User-Agent and "laptop" for anything else.
func deviceIcon(ua string) string {
	if strings.Contains(ua, "iPhone") || strings.Contains(ua, "Android") || strings.Contains(ua, "iPad") {
		return "phone"
	}
	return "laptop"
}

// appIcon is the icon of a device token by its kind.
func appIcon(k auth.DeviceKind) string {
	if k == auth.KindMobile {
		return "phone"
	}
	return "laptop"
}

// deviceRows merges signed-in browsers and device tokens into rows, the current browser first and
// the rest by last activity, newest first. base is "/me" or "/admin/users/{id}"; the sign-out form
// of a row is base + "/devices/{id}/signout" for an app, and for a browser "/me/sessions/{id}/signout"
// on /me or the same devices path on an admin page (it takes either id). others counts the rows that
// are not the current browser. Admin rows never carry a current browser.
func deviceRows(now time.Time, sessions []auth.SessionInfo, apps []auth.Device, base string, forAdmin bool) ([]pages.ProfileDevice, int) {
	type entry struct {
		row  pages.ProfileDevice
		last int64
	}
	var cur *pages.ProfileDevice
	var rest []entry
	for _, si := range sessions {
		label := deviceLabel(si.UserAgent)
		d := pages.ProfileDevice{ID: si.PublicID, Icon: deviceIcon(si.UserAgent)}
		if forAdmin {
			d.Action = base + "/devices/" + si.PublicID + "/signout"
		} else {
			d.Action = "/me/sessions/" + si.PublicID + "/signout"
		}
		if si.Current {
			d.Title, d.Meta, d.Current = msgThisBrowser, label, true
			cur = &d
			continue
		}
		d.Title, d.Meta = label, agoText(now, time.UnixMilli(si.LastSeenAt))
		rest = append(rest, entry{d, si.LastSeenAt})
	}
	for _, a := range apps {
		kind := auth.KindLabel(a.Kind)
		meta := kind + " · " + msgNotUsedYet
		if a.LastSeenAt > 0 {
			meta = kind + " · " + agoText(now, time.UnixMilli(a.LastSeenAt))
		}
		rest = append(rest, entry{pages.ProfileDevice{ID: a.ID, Title: a.Name, Meta: meta, Icon: appIcon(a.Kind),
			Action: base + "/devices/" + a.ID + "/signout"}, max(a.LastSeenAt, a.CreatedAt)})
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].last > rest[j].last })
	var out []pages.ProfileDevice
	if cur != nil {
		out = append(out, *cur)
	}
	for _, e := range rest {
		out = append(out, e.row)
	}
	return out, len(rest)
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
	if v, has := last("time_zone"); has {
		if !users.ValidTimeZone(v) {
			ok = false
		}
		next.TimeZone = v // the browser's zone, posted by shell.js (decision 0013)
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

// timeZoneOnly reports whether a settings post carries the browser's time zone and nothing else
// (besides the CSRF field), as shell.js sends it, and returns the zone.
func timeZoneOnly(f url.Values) (string, bool) {
	zone, has := f["time_zone"]
	if !has || len(zone) != 1 {
		return "", false
	}
	for k := range f {
		if k != "time_zone" && k != csrfField {
			return "", false
		}
	}
	return zone[0], true
}
