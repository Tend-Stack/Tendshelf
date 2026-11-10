package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/radio"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The Radio pages (decision 0038, plan R2): the person's stations, the station search with the stream address
// form, and the player fragment of a station. The actions and the relay are in routes_radio.go. Words come from
// radio_copy.go; no stream address and no upstream text is ever drawn.

func (s *server) registerRadioPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /radio", s.getRadio)
	mux.HandleFunc("GET /radio/search", s.getRadioSearch)
	mux.HandleFunc("GET /hx/player/radio/{stationID}", s.getPlayerRadio)
}

// radioNavOn is true when radio is on and this person may use it: the Radio tab and the nav item are drawn.
func (s *server) radioNavOn(r *http.Request) bool {
	if !s.radioOn(r) {
		return false
	}
	u := currentUser(r)
	return u.ID != "" && s.can.Can(r.Context(), u, share.UseRadio, podServer)
}

// podcastsAreaTabs are the tabs shared by the Podcasts home and the Radio page: the four podcast tabs when
// podcasts are on, then Radio when radio is on. active is the key of the one marked current.
func (s *server) podcastsAreaTabs(r *http.Request, active string) []pages.PodTab {
	var tabs []pages.PodTab
	if s.podcastsOn(r) {
		for _, t := range []struct{ key, label string }{{"shows", podShows}, {"new", podNew}, {"favourites", podFavourites}, {"history", podHistory}} {
			href := "/podcasts"
			if t.key != "shows" {
				href += "?tab=" + t.key
			}
			tabs = append(tabs, pages.PodTab{Label: t.label, Href: href, On: t.key == active})
		}
	}
	if s.radioNavOn(r) && len(tabs) > 0 {
		tabs = append(tabs, pages.PodTab{Label: radioTitle, Href: "/radio", On: active == "radio"})
	}
	return tabs
}

func (s *server) stationRow(r *http.Request, st radio.Station, removable bool) components.StationRowView {
	id := url.PathEscape(st.ID)
	meta := st.Genre
	if st.Country != "" {
		if meta != "" {
			meta += " · "
		}
		meta += st.Country
	}
	v := components.StationRowView{
		ID: st.ID, Name: st.Name, Meta: meta, PlayHref: "/radio/live/" + id, PlayLabel: radioPlay + " " + st.Name, CSRF: s.csrf.TokenFrom(r.Context()),
	}
	if removable {
		v.RemoveAction, v.RemoveLabel = "/radio/stations/"+id+"/remove", radioRemove+" "+st.Name
	}
	return v
}

func (s *server) getRadio(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	saved, err := s.radio.Favourites(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "radio stations", err)
		return
	}
	recent, err := s.radio.Recent(ctx, u.ID, radio.MaxRecent)
	if err != nil {
		s.serverError(w, r, "radio recent", err)
		return
	}
	v := pages.RadioView{
		Title: radioTitle, FindLabel: radioFind, FindHref: "/radio/search", TabsLabel: radioTabsLabel, Tabs: s.podcastsAreaTabs(r, "radio"),
		SavedTitle: radioSavedTitle, RecentTitle: radioRecentTitle, EmptyTitle: radioNoStationsHd, EmptyBody: radioNoStations,
	}
	for _, st := range saved {
		v.Saved = append(v.Saved, s.stationRow(r, st, true))
	}
	for _, st := range recent {
		if !st.Favourite { // a saved station is already above
			v.Recent = append(v.Recent, s.stationRow(r, st, false))
		}
	}
	s.pageStyled(w, r, http.StatusOK, radioTitle, "podcasts", pages.Radio(v), nil, pages.PodcastStyles...)
}

func (s *server) getRadioSearch(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	u := currentUser(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 100 {
		q = string([]rune(q)[:100])
	}
	v := pages.RadioSearchView{
		CSRF: s.csrf.TokenFrom(r.Context()), Title: radioFind, Q: q, SearchLabel: radioSearchLabel, SearchButton: radioSearchWord,
		BackHref: "/radio", BackLabel: radioBack, NoResults: radioNoResults, ResultsLabel: radioResultsLabel,
		AddLabel: radioAdd, AddAria: radioAdd, AddTitle: radioAddByAddress, AddHint: radioAddHint, AddFieldLabel: radioAddFieldLabel, AddButton: radioAddButton,
	}
	if q != "" {
		if ok, wait := s.podLim.search.Allow(u.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
			s.refuseSetting(w, r, http.StatusTooManyRequests, podErrBusy)
			return
		}
		s.podLim.search.Fail(u.ID) // each search counts once, like podcast search
		v.Searched = true
		hits, err := s.radio.Net.Search(r.Context(), q, "stations", 20)
		if err != nil {
			v.Failure = radioErrSearchDown
			v.AddOpen = true
		}
		for _, h := range hits {
			v.Hits = append(v.Hits, radioHit(h))
		}
	}
	s.pageStyled(w, r, http.StatusOK, radioFind, "podcasts", pages.RadioSearch(v), nil, pages.PodcastStyles...)
}

// radioHit is one OndaCast result. Its logo lives on another site, which this server never loads, so the cover is the
// plain tile with the name.
func radioHit(h podnet.Hit) pages.RadioHit {
	return pages.RadioHit{Title: h.Title, Subtitle: h.Subtitle, Slug: h.Slug, Card: components.CardView{Title: h.Title}}
}

// getPlayerRadio answers GET /hx/player/radio/{stationID}: the player fragment of a station the person keeps or has
// played. It has no length, no chapters and no place: kind "radio" is the player's live mode.
func (s *server) getPlayerRadio(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStation(w, r)
	if !ok {
		return
	}
	set, err := s.users.LoadSettings(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "build radio player", err)
		return
	}
	v := components.PlayerDataView{
		BookID: st.ID, Title: st.Name, Author: s.radio.NowPlaying(st.ID), ServerNow: s.now().UnixMilli(),
		Speed: "1.00", SkipBackS: set.SkipBackS, SkipForwardS: set.SkipForwardS, Rewind: rewindAttr(),
		Kind: radioKind, ShowHref: "/radio",
		Files: []components.PlayerFileView{{ID: st.ID, Mime: "audio/mpeg", Playable: true, Href: "/radio/live/" + url.PathEscape(st.ID)}},
	}
	w.Header().Set("Cache-Control", "private, no-store")
	s.fragment(w, r, http.StatusOK, components.PlayerData(v))
}

// radioKind is the player fragment's data-kind for a live station (player.js reads it).
const radioKind = "radio"
