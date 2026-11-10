package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The podcast pages (decision 0038, plan P3). The handlers in routes_podcasts.go do the work and the
// authorization; this file builds what the pages draw. Words come from podcasts_copy.go.

// podListLimit is how many rows the New, Favourites and History tabs show.
const podListLimit = 50

// podNav is the navigation's view of podcasts: whether the item is drawn and the badge count. It is drawn only
// when the owner turned podcasts on and this person may use them.
func (s *server) podNav(r *http.Request) components.PodNav {
	if !s.podcastsOn(r) {
		return components.PodNav{}
	}
	u := currentUser(r)
	if u.ID == "" || !s.can.Can(r.Context(), u, share.UsePodcasts, podServer) {
		return components.PodNav{}
	}
	n, err := s.podcasts.NewTotal(r.Context(), u.ID)
	if err != nil {
		n = 0
	}
	return components.PodNav{Show: true, New: n}
}

// podArt is the show's picture as a cover: the local art route at three sizes, or none (the cover then
// shows the title on a plain tile). The art address never leaves this server.
func podArt(id, title, artSHA string) components.CardView {
	c := components.CardView{ID: id, Title: title, Href: "/podcasts/shows/" + url.PathEscape(id)}
	if len(artSHA) >= 8 {
		base := "/podcasts/art/" + url.PathEscape(id) + "?v=" + artSHA[:8] + "&w="
		c.CoverSrc = base + "320"
		c.CoverSrcset = base + "160 160w, " + base + "320 320w, " + base + "640 640w"
	}
	return c
}

func podNewCount(n int) (badge, label string) {
	switch {
	case n <= 0:
		return "", ""
	case n > 99:
		return podNewBadgeCap, "more than 99 new episodes"
	case n == 1:
		return "1", podNewBadgeOne
	}
	return strconv.Itoa(n), fmt.Sprintf(podNewBadgeMany, n)
}

func podShowCard(sh podcast.ShowRow) components.ShowCardView {
	badge, label := podNewCount(sh.NewCount)
	return components.ShowCardView{Card: podArt(sh.ID, sh.Title, sh.ArtSHA), Author: sh.Author, New: badge, NewLabel: label}
}

// podDate is a day as "2 Jan 2024".
func podDate(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2 Jan 2006")
}

// podMeta is the second line of an episode: the day, the length and, for a started episode, what is left.
func podMeta(ep podcast.Episode) string {
	var parts []string
	if d := podDate(ep.PublishedAt); d != "" {
		parts = append(parts, d)
	}
	if t := durationText(ep.DurationMs); t != "" && !ep.State.Played {
		parts = append(parts, t)
	}
	switch {
	case ep.State.Played:
		parts = append(parts, podPlayed)
	case ep.State.PositionMs > 0 && ep.DurationMs > ep.State.PositionMs:
		parts = append(parts, durationText(ep.DurationMs-ep.State.PositionMs)+" "+podLeftWord)
	}
	return strings.Join(parts, " · ")
}

func podIsVideo(ep podcast.Episode) bool {
	return strings.HasPrefix(strings.ToLower(ep.AudioType), "video/")
}

// podRow is one episode row. seenAt marks the episodes that came after the person last looked at the show
// (0 marks none); mixed draws the show's name above the title.
func (s *server) podRow(r *http.Request, ep podcast.Episode, back string, seenAt int64, mixed bool) components.EpisodeRowView {
	id := url.PathEscape(ep.ID)
	v := components.EpisodeRowView{
		ID: ep.ID, Title: ep.Title, Href: "/podcasts/episodes/" + id, Meta: podMeta(ep),
		IsNew:   seenAt > 0 && ep.PublishedAt > seenAt && !ep.State.Played,
		NewWord: podNew, PlayHref: "/podcasts/audio/" + id, PlayLabel: podPlay + " " + ep.Title, Video: podIsVideo(ep),
		Played: ep.State.Played, PlayedAction: "/podcasts/episodes/" + id + "/played",
		Starred: ep.State.Favourite, StarAction: "/podcasts/episodes/" + id + "/favourite", StarLabel: podStar,
		Back: back, CSRF: s.csrf.TokenFrom(r.Context()),
	}
	if mixed {
		v.Show = ep.ShowTitle
	}
	v.PlayedValue, v.PlayedLabel = "1", podMarkPlayed
	if ep.State.Played {
		v.PlayedValue, v.PlayedLabel = "0", podMarkNotPlayed
	}
	v.StarValue = "1"
	if ep.State.Favourite {
		v.StarValue = "0"
	}
	return v
}

// backTarget is where a mark or star returns to: the page the form came from when it is a podcast page of
// this server (or Home), else def. It never leaves the site.
func backTarget(r *http.Request, def string) string {
	b := r.PostForm.Get("back")
	if b == "/" { // Home's "New episodes" shelf
		return b
	}
	if len(b) > 1500 || !strings.HasPrefix(b, "/podcasts") || strings.HasPrefix(b, "//") || strings.ContainsAny(b, "\\\r\n") {
		return def
	}
	u, err := url.Parse(b)
	if err != nil || u.Host != "" || u.Scheme != "" || (u.Path != "/podcasts" && !strings.HasPrefix(u.Path, "/podcasts/")) {
		return def
	}
	return (&url.URL{Path: u.Path, RawQuery: u.RawQuery}).String()
}

func backPath(r *http.Request) string {
	if len(r.URL.RequestURI()) > 1500 {
		return r.URL.Path
	}
	return r.URL.RequestURI()
}

func (s *server) getPodcasts(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	s.podcasts.RefreshStale(ctx, u.ID)
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "new", "favourites", "history":
	default:
		tab = "shows"
	}
	v := pages.PodcastsView{Title: podcastsTitle, FindLabel: podcastsSearchTitle, FindHref: "/podcasts/search", TabsLabel: podTabsLabel}
	for _, t := range []struct{ key, label string }{{"shows", podShows}, {"new", podNew}, {"favourites", podFavourites}, {"history", podHistory}} {
		href := "/podcasts"
		if t.key != "shows" {
			href += "?tab=" + t.key
		}
		v.Tabs = append(v.Tabs, pages.PodTab{Label: t.label, Href: href, On: t.key == tab})
	}
	var eps []podcast.Episode
	var err error
	switch tab {
	case "shows":
		var shows []podcast.ShowRow
		if shows, err = s.podcasts.Shows(ctx, u.ID); err == nil {
			for _, sh := range shows {
				v.Shows = append(v.Shows, podShowCard(sh))
			}
		}
		v.EmptyTitle, v.EmptyBody, v.EmptyAction = podNoShowsTitle, podNoShows, true
		v.Import = s.podImportView(r)
	case "new":
		eps, err = s.podcasts.NewEpisodes(ctx, u.ID, podListLimit, 0)
		v.EmptyTitle, v.EmptyBody = podNoNewTitle, podNoNewBody
	case "favourites":
		eps, err = s.podcasts.Favourites(ctx, u.ID, podListLimit)
		v.EmptyTitle, v.EmptyBody = podNoFavTitle, podNoFavBody
	case "history":
		eps, err = s.podcasts.History(ctx, u.ID, podListLimit)
		v.EmptyTitle, v.EmptyBody = podNoHistTitle, podNoHistBody
	}
	if err != nil {
		s.serverError(w, r, "podcasts", err)
		return
	}
	back := backPath(r)
	for _, ep := range eps {
		v.Episodes = append(v.Episodes, s.podRow(r, ep, back, 0, true))
	}
	s.pageStyled(w, r, http.StatusOK, podcastsTitle, "podcasts", pages.Podcasts(v), nil, pages.PodcastStyles...)
}

func (s *server) getPodcastSearch(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	u := currentUser(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		q = q[:200]
	}
	v := pages.PodcastSearchView{
		CSRF: s.csrf.TokenFrom(r.Context()), Title: podcastsSearchTitle, Q: q, SearchLabel: podSearchPodcasts, SearchButton: podSearchWord,
		BackHref: "/podcasts", BackLabel: podBackToPodcasts, FollowLabel: podFollow, FollowAriaLabel: podFollow, NoResults: podNoResults,
		AddTitle: podAddByRSS, AddHint: podPasteRSS, AddLabel: podRSSLabel, ResultsLabel: podResultsLabel,
	}
	if q != "" {
		if ok, wait := s.podLim.search.Allow(u.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
			s.refuseSetting(w, r, http.StatusTooManyRequests, podErrBusy)
			return
		}
		s.podLim.search.Fail(u.ID) // each search counts once
		v.Searched = true
		hits, err := s.podcasts.Net.Search(r.Context(), q, "podcasts", 20)
		if err != nil {
			v.Failure = podErrSearchDown
			v.AddOpen = true
		}
		for _, h := range hits {
			v.Hits = append(v.Hits, podHit(h))
		}
	}
	s.pageStyled(w, r, http.StatusOK, podcastsSearchTitle, "podcasts", pages.PodcastSearch(v), nil, pages.PodcastStyles...)
}

// podHit is one OndaCast result. Its picture lives on another site, which this server never loads, so the cover is
// the plain tile with the title.
func podHit(h podnet.Hit) pages.PodHit {
	return pages.PodHit{Title: h.Title, Subtitle: h.Subtitle, Slug: h.Slug, Card: components.CardView{Title: h.Title}}
}

var podSpeeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3, 3.5}

func podSpeedText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func podSpeedOptions(cur *float64) []pages.Option {
	var out []pages.Option
	seen := cur == nil
	for _, sp := range podSpeeds {
		out = append(out, pages.Option{Value: podSpeedText(sp), Label: podSpeedText(sp) + "×"})
		if cur != nil && *cur == sp {
			seen = true
		}
	}
	if !seen { // a value an app set that the list lacks stays selectable
		out = append(out, pages.Option{Value: podSpeedText(*cur), Label: podSpeedText(*cur) + "×"})
	}
	return out
}

func (s *server) getPodcastShow(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	sh, ok := s.loadShow(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	q := r.URL.Query()
	eq := podcast.EpisodeQuery{Sort: q.Get("sort"), Filter: q.Get("filter"), Text: strings.TrimSpace(q.Get("q")), After: q.Get("after")}
	eps, next, err := s.podcasts.Episodes(ctx, u.ID, sh.ID, eq)
	if err == nil && len(eq.Text) > 200 {
		err = podcast.ErrInvalid
	}
	if errors.Is(err, podcast.ErrInvalid) {
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, "show episodes", err)
		return
	}
	sort, filter := eq.Sort, eq.Filter
	if sort == "" {
		sort = "newest"
	}
	if filter == "" {
		filter = "all"
	}
	base := "/podcasts/shows/" + url.PathEscape(sh.ID)
	link := func(sortV, filterV string, extra url.Values) string {
		p := url.Values{}
		if sortV != "newest" {
			p.Set("sort", sortV)
		}
		if filterV != "all" {
			p.Set("filter", filterV)
		}
		if eq.Text != "" {
			p.Set("q", eq.Text)
		}
		for k, vs := range extra {
			p[k] = vs
		}
		if len(p) == 0 {
			return base
		}
		return base + "?" + p.Encode()
	}
	back := backPath(r)
	rows := make([]components.EpisodeRowView, 0, len(eps))
	for _, ep := range eps {
		rows = append(rows, s.podRow(r, ep, back, sh.SeenAt, false))
	}
	v := pages.PodcastShowView{
		CSRF: s.csrf.TokenFrom(ctx), ID: sh.ID, Title: sh.Title, Author: sh.Author, BackHref: "/podcasts", BackLabel: podBackToPodcasts,
		Art: podArt(sh.ID, sh.Title, sh.ArtSHA), Following: podFollowing, UnfollowLabel: podUnfollow, RefreshLabel: podRefresh,
		UnfollowAction: base + "/unfollow", RefreshAction: base + "/refresh",
		NotesTitle: podAboutShow, Failure: podcast.FailText(sh.FailCode), Fetching: sh.FetchedAt == 0 && sh.FailCode == "", FetchingLine: podFetching,
		Episodes: rows, Empty: podNoEpisodes, ListLabel: podEpisodesLabel, MoreText: podLoadMore,
		Notes: rss.CleanHTML(sh.NotesHTML, rss.NotesMax),
	}
	if sh.Title == "" {
		v.Title = sh.FeedURL
	}
	if sh.SiteURL != "" && (strings.HasPrefix(sh.SiteURL, "https://") || strings.HasPrefix(sh.SiteURL, "http://")) {
		v.SiteHref = sh.SiteURL
		v.SiteLabel = sh.SiteURL
	}
	if eq.Text != "" || filter != "all" {
		v.Empty = podNoMatch
	}
	if next != "" {
		v.MoreHref = link(sort, filter, url.Values{"after": {next}})
	}
	set := sh.Settings
	v.Settings = pages.PodSettingsView{
		Action: base + "/settings", CSRF: v.CSRF, Title: podSettingsTitle, SpeedLabel: podSpeedForShow, SpeedDefault: podSpeedDefault,
		Speeds: podSpeedOptions(set.Speed), SkipIntroBefore: podSkipFirst, SkipIntroAfter: podSecondsWord,
		SkipOutroBefore: podSkipLast, SkipOutroAfter: podSecondsWord, SkipIntro: set.SkipIntroS, SkipOutro: set.SkipOutroS, Save: podSettingsSave,
	}
	if set.Speed != nil {
		v.Settings.Speed = podSpeedText(*set.Speed)
	}
	f := components.EpisodeFiltersView{
		FilterLabel: podFilterLabel, SortLabel: podSortLabel, Action: base, Q: eq.Text, SearchLabel: podSearchShow, SearchButton: podSearchWord,
		Sort: eq.Sort, Filter: eq.Filter,
	}
	for _, c := range []struct{ key, label string }{{"all", podAll}, {"unplayed", podUnplayed}, {"in_progress", podInProgress}, {"played", podPlayed}} {
		f.Filters = append(f.Filters, components.LinkChoice{Label: c.label, Href: link(sort, c.key, nil), On: c.key == filter})
	}
	for _, c := range []struct{ key, label string }{{"newest", podNewest}, {"oldest", podOldest}} {
		f.Sorts = append(f.Sorts, components.LinkChoice{Label: c.label, Href: link(c.key, filter, nil), On: c.key == sort})
	}
	v.Filters = f
	// Looking at the show clears its "new" count; the rows above were marked before this.
	if eq.After == "" {
		if err := s.podcasts.MarkSeen(ctx, u.ID, sh.ID); err != nil {
			s.podcasts.Log.Warn("podcasts: could not mark a show seen", "err", err)
		}
	}
	title := v.Title
	s.pageStyled(w, r, http.StatusOK, title, "podcasts", pages.PodcastShow(v), nil, pages.PodcastStyles...)
}

func (s *server) getPodcastEpisode(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	id := url.PathEscape(ep.ID)
	here := "/podcasts/episodes/" + id
	sh, _ := s.podcasts.ShowByID(ctx, ep.ShowID)
	v := pages.PodcastEpisodeView{
		CSRF: s.csrf.TokenFrom(ctx), ID: ep.ID, Title: ep.Title, Show: ep.ShowTitle, BackHref: "/podcasts", BackLabel: podBackToPodcasts,
		Art: podArt(ep.ShowID, ep.ShowTitle, sh.ArtSHA), Meta: podMeta(ep), PlayHref: "/podcasts/audio/" + id, PlayLabel: podPlay,
		Video: podIsVideo(ep), VideoLine: podErrVideo, Played: ep.State.Played, PlayedAction: here + "/played",
		Starred: ep.State.Favourite, StarAction: here + "/favourite", StarLabel: podStar, Back: here,
		NotesTitle: podAboutEpisode, Notes: rss.CleanHTML(ep.NotesHTML, rss.NotesMax),
	}
	if !v.Video {
		v.SaveHref, v.SaveLabel = "/podcasts/audio/"+id+"?download=1", podSaveFile
	}
	v.PlayedValue, v.PlayedLabel = "1", podMarkPlayed
	if ep.State.Played {
		v.PlayedValue, v.PlayedLabel = "0", podMarkNotPlayed
	}
	v.StarValue = "1"
	if ep.State.Favourite {
		v.StarValue = "0"
	}
	if s.podcasts.Follows(ctx, u.ID, ep.ShowID) {
		v.ShowHref = "/podcasts/shows/" + url.PathEscape(ep.ShowID)
		v.BackHref, v.BackLabel = v.ShowHref, ep.ShowTitle
	}
	switch {
	case ep.Season > 0 && ep.Number > 0:
		v.Number = fmt.Sprintf("%s %d · %s %d", podSeasonWord, ep.Season, podEpisodeWord, ep.Number)
	case ep.Number > 0:
		v.Number = fmt.Sprintf("%s %d", podEpisodeWord, ep.Number)
	}
	if !ep.State.Played && ep.State.PositionMs > 0 && ep.DurationMs > ep.State.PositionMs {
		v.Progress = durationText(ep.DurationMs-ep.State.PositionMs) + " " + podLeftWord
	}
	s.pageStyled(w, r, http.StatusOK, ep.Title, "podcasts", pages.PodcastEpisode(v), nil, pages.PodcastStyles...)
}
