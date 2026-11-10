package web

import (
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// First use of Podcasts and Radio (decision 0038, addendum 2026-11-10). While the owner has turned neither on, every
// podcasts and radio page address shows an onboarding page instead of a 404, and nothing leaves the server. The
// owner turns them on with one plain POST form through the same settings code /admin/podcasts uses.

func (s *server) registerPodStart(mux *http.ServeMux) {
	mux.HandleFunc("POST /podcasts/start", s.postPodStart)
}

// podStartActive is true when this request should get the onboarding page: a service exists, both switches are off
// and the person may use podcasts (a guest never sees it).
func (s *server) podStartActive(r *http.Request) bool {
	if s.podcasts == nil && s.radio == nil {
		return false
	}
	u := currentUser(r)
	if u.ID == "" || s.podcastsOn(r) || s.radioOn(r) {
		return false
	}
	return s.can.Can(r.Context(), u, share.UsePodcasts, podServer)
}

// podStartPage draws the onboarding page and reports true when it did; the caller then returns.
func (s *server) podStartPage(w http.ResponseWriter, r *http.Request) bool {
	if !s.podStartActive(r) {
		return false
	}
	s.drawPodStart(w, r, http.StatusOK, "")
	return true
}

func (s *server) drawPodStart(w http.ResponseWriter, r *http.Request, status int, failure string) {
	v := pages.PodStartView{
		Title: podStartTitle, Lead: podStartLead, NetworkTitle: podStartNetTitle, Network: podStartNetwork,
		CanTurnOn: s.can.Can(r.Context(), currentUser(r), share.ManageServer, podServer), CSRF: s.csrf.TokenFrom(r.Context()),
		FormLabel: podStartFormHead, PodcastsLabel: podStartPodcasts, RadioLabel: podStartRadio, Button: podStartButton,
		NotOwner: podStartNotOwner, Failure: failure,
	}
	s.pageStyled(w, r, status, podStartTitle, "podcasts", pages.PodcastStart(v), nil, pages.PodcastStyles...)
}

// postPodStart turns on what the owner ticked. It only switches things on; /admin/podcasts turns them off.
func (s *server) postPodStart(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, share.ManageServer, podServer) || !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	pod, rad := r.PostForm.Get("podcasts") == "1", r.PostForm.Get("radio") == "1"
	if !pod && !rad {
		s.drawPodStart(w, r, http.StatusUnprocessableEntity, podStartNone)
		return
	}
	set := podcast.LoadSettings(ctx, s.lib.Store())
	set.Enabled = set.Enabled || pod
	set.RadioEnabled = set.RadioEnabled || rad
	if err := podcast.SaveSettings(ctx, s.lib.Store(), currentUser(r).ID, set, s.now()); err != nil {
		s.serverError(w, r, "turn on podcasts", err)
		return
	}
	if pod {
		s.answerPage(w, r, "/podcasts")
		return
	}
	s.answerPage(w, r, "/radio")
}

// podStarters is the "Popular shows" list, or nil when the embedded list is empty. Pictures go through the guarded
// same-origin art route, like search results.
func (s *server) podStarters(r *http.Request) *pages.PodStarterView {
	shows := podnet.StarterShows()
	if len(shows) == 0 {
		return nil
	}
	v := &pages.PodStarterView{CSRF: s.csrf.TokenFrom(r.Context()), Title: podStarterTitle, Hint: podStarterHint, FollowLabel: podFollow, FollowAria: podFollow}
	for _, g := range []struct{ key, title string }{{podnet.StarterTrueCrime, podStarterCrime}, {podnet.StarterMore, podStarterMore}} {
		grp := pages.PodStarterGroup{Title: g.title}
		for _, sh := range shows {
			if sh.Group == g.key {
				grp.Hits = append(grp.Hits, s.podHit(r, podnet.Hit{Slug: sh.Slug, Title: sh.Title, Subtitle: sh.Author, ImageURL: sh.ImageURL}))
			}
		}
		if len(grp.Hits) > 0 {
			v.Groups = append(v.Groups, grp)
		}
	}
	return v
}

// radioPopular is OndaCast's popular stations as cards with a Save control. If OndaCast cannot be reached the list
// is simply absent: a suggestion never shows an error.
func (s *server) radioPopular(r *http.Request) []components.StationRowView {
	list, err := s.radio.Net.PopularStations(r.Context(), 24)
	if err != nil {
		return nil
	}
	csrf := s.csrf.TokenFrom(r.Context())
	var out []components.StationRowView
	for _, p := range list {
		meta := p.Genre
		if p.Country != "" {
			if meta != "" {
				meta += " · "
			}
			meta += p.Country
		}
		art := components.CardView{Title: p.Name}
		if src := s.artKeyFor(r, searchArtRadio, p.Logo); src != "" {
			art.CoverSrc = src
		}
		out = append(out, components.StationRowView{Name: p.Name, Meta: meta, Art: art, AddSlug: p.Slug, AddLabel: radioSave + " " + p.Name, SaveWord: radioSave,
			PlaySlug: p.Slug, PlayLabel: radioPlay + " " + p.Name, PlayWord: radioPlay, CSRF: csrf})
	}
	return out
}
