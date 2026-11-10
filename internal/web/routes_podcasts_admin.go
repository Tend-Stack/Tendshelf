package web

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The owner's podcast and radio settings (decision 0038): on or off, the scheduled-refresh interval, the
// room for saved episodes, and whether per-show server auto-download is allowed. Owner only (ManageServer);
// the page works whether podcasts are on or off, so the owner can turn them on. P3 replaces the markup.

const gib = 1 << 30

func (s *server) adminPodcastsGate(w http.ResponseWriter, r *http.Request) bool {
	return s.allow(w, r, share.ManageServer, podServer)
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

// adminPodcastsView builds the settings page from the saved settings.
func (s *server) adminPodcastsView(r *http.Request) pages.AdminPodcastsView {
	set := podcast.LoadSettings(r.Context(), s.lib.Store())
	var used int64
	if s.podcasts != nil {
		used = s.podcasts.CacheUsage(r.Context())
	}
	saved := r.URL.Query().Get("saved")
	return pages.AdminPodcastsView{
		CSRF: s.csrf.TokenFrom(r.Context()), Title: podAdminTitle, Intro: podAdminIntro, Heading: podAdminTitle,
		Enabled: set.Enabled, EnabledLabel: podAdminEnabled, EnabledHint: podAdminEnabledHnt,
		RefreshLabel: podAdminRefresh, Refresh: set.Refresh,
		Refreshes: []pages.Option{{Value: podcast.RefreshOff, Label: podAdminOnLook}, {Value: podcast.Refresh1h, Label: podAdminHourly},
			{Value: podcast.Refresh6h, Label: podAdminSixHours}, {Value: podcast.Refresh24h, Label: podAdminDaily}},
		CacheLabel: podAdminCacheLabel, CacheValue: strconv.FormatFloat(float64(set.CacheBytes)/gib, 'f', -1, 64), CacheUnit: podAdminCacheUnit, CacheHint: podAdminCacheHint,
		ServerAuto: set.ServerAuto, ServerAutoLabel: podAdminAuto, ServerAutoHint: podAdminAutoHint,
		Radio: set.RadioEnabled, RadioLabel: podAdminRadio, RadioHint: podAdminRadioHint,
		Save: podAdminSave, CacheHeading: podAdminCacheHead, ClearLabel: podAdminClear,
		UsedLine: fmt.Sprintf(podAdminUsed, sizeText(used), strconv.FormatFloat(float64(set.CacheBytes)/gib, 'f', -1, 64)+" GB"),
		Saved:    saved == "1", SavedNotice: podAdminSaved, Cleared: saved == "cleared", ClearedNotice: podAdminCleared,
	}
}

func (s *server) getAdminPodcasts(w http.ResponseWriter, r *http.Request) {
	if !s.adminPodcastsGate(w, r) {
		return
	}
	s.prefsPage(w, r, http.StatusOK, podAdminTitle, "admin-podcasts", "podcasts", false, pages.AdminPodcasts(s.adminPodcastsView(r)), nil, pages.PodcastAdminStyles...)
}
