package web

import (
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// The owner's podcast and radio settings (decision 0038): on or off, the scheduled-refresh interval, the
// room for saved episodes, and whether per-show server auto-download is allowed. Owner only (ManageServer);
// the page works whether podcasts are on or off, so the owner can turn them on. P3 replaces the markup.

const gib = 1 << 30

func (s *server) adminPodcastsGate(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageServer, podServer)
}

func (s *server) getAdminPodcasts(w http.ResponseWriter, r *http.Request) {
	if !s.adminPodcastsGate(w, r) {
		return
	}
	set := podcast.LoadSettings(r.Context(), s.lib.Store())
	var used int64
	if s.podcasts != nil {
		used = s.podcasts.CacheUsage(r.Context())
	}
	checked := func(b bool) string {
		if b {
			return " checked"
		}
		return ""
	}
	sel := func(v string) string {
		if set.Refresh == v {
			return " selected"
		}
		return ""
	}
	s.stubPage(w, r, podAdminTitle, func(w io.Writer) {
		io.WriteString(w, `<form method="post" action="/admin/podcasts">`+s.csrfField(r)+
			`<input type="hidden" name="enabled" value="0"/><label><input type="checkbox" name="enabled" value="1"`+checked(set.Enabled)+`/> Let people follow podcasts</label>`+
			`<label>Check for new episodes <select name="refresh"><option value="off"`+sel(podcast.RefreshOff)+`>Only when someone looks</option>`+
			`<option value="1h"`+sel(podcast.Refresh1h)+`>Every hour</option><option value="6h"`+sel(podcast.Refresh6h)+`>Every 6 hours</option>`+
			`<option value="24h"`+sel(podcast.Refresh24h)+`>Once a day</option></select></label>`+
			`<label>Room for saved episodes on this server (GB) <input type="text" name="cache_gib" value="`+html.EscapeString(strconv.FormatFloat(float64(set.CacheBytes)/gib, 'f', -1, 64))+`"/></label>`+
			`<input type="hidden" name="server_auto" value="0"/><label><input type="checkbox" name="server_auto" value="1"`+checked(set.ServerAuto)+`/> Allow saving new episodes on the server</label>`+
			`<input type="hidden" name="radio" value="0"/><label><input type="checkbox" name="radio" value="1"`+checked(set.RadioEnabled)+`/> Let people listen to radio</label>`+
			`<button type="submit">Save</button></form>`)
		io.WriteString(w, `<p>`+html.EscapeString(fmt.Sprintf("Saved episodes use %s of %s", sizeText(used), sizeText(set.CacheBytes)))+`</p>`)
		io.WriteString(w, `<form method="post" action="/admin/podcasts">`+s.csrfField(r)+`<input type="hidden" name="clear" value="1"/><button type="submit">Remove saved episodes</button></form>`)
	})
}

func (s *server) postAdminPodcasts(w http.ResponseWriter, r *http.Request) {
	if !s.adminPodcastsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	f := r.PostForm
	if f.Get("clear") == "1" {
		if s.podcasts != nil {
			if _, err := s.podcasts.ClearCache(ctx); err != nil {
				s.serverError(w, r, "clear podcast cache", err)
				return
			}
		}
		s.answerPage(w, r, "/admin/podcasts?saved=cleared")
		return
	}
	set := podcast.LoadSettings(ctx, s.lib.Store())
	bad := false
	boolOf := func(key string, dst *bool) {
		if f.Has(key) {
			v, ok := flag(lastValue(f[key]))
			bad = bad || !ok
			*dst = v
		}
	}
	boolOf("enabled", &set.Enabled)
	boolOf("server_auto", &set.ServerAuto)
	boolOf("radio", &set.RadioEnabled)
	if f.Has("refresh") {
		set.Refresh = f.Get("refresh")
	}
	if v := f.Get("cache_gib"); v != "" {
		g, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(g) || math.IsInf(g, 0) || g < 0 || g > float64(podcast.MaxCacheBytes)/gib {
			bad = true
		} else {
			set.CacheBytes = int64(g * gib)
		}
	}
	if bad || !set.Valid() {
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podAdminBad)
		return
	}
	if err := podcast.SaveSettings(ctx, s.lib.Store(), currentUser(r).ID, set, s.now()); err != nil {
		s.serverError(w, r, "save podcast settings", err)
		return
	}
	s.answerPage(w, r, "/admin/podcasts?saved=1")
}
