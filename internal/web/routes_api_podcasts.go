package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// The JSON API for the app (plan P2, step 9). Follow and search stay web pages: the app shows them in its
// WebView. Every route is behind the same off switch, share.UsePodcasts and share.ListenEpisode as the pages,
// and answers the API's 404 when podcasts are off. No publisher address is ever put in an answer.

func (s *server) registerAPIPodcasts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/podcasts", s.getAPIPodcasts)
	mux.HandleFunc("GET /api/v1/podcasts/new", s.getAPIPodcastsNew)
	mux.HandleFunc("GET /api/v1/podcasts/shows/{showID}/episodes", s.getAPIPodcastEpisodes)
	mux.HandleFunc("GET /api/v1/podcasts/episodes/{episodeID}", s.getAPIPodcastEpisode)
	mux.HandleFunc("GET /api/v1/podcasts/audio/{episodeID}", s.getEpisodeAudio)
	mux.HandleFunc("GET /api/v1/podcasts/art/{showID}", s.getPodcastArt)
	mux.HandleFunc("GET /api/v1/podcasts/chapters/{episodeID}", s.getPodcastChapters)
	mux.HandleFunc("POST /api/v1/podcasts/progress/batch", s.postAPIPodcastProgressBatch)
	mux.HandleFunc("POST /api/v1/podcasts/episodes/{episodeID}/played", s.postAPIPodcastPlayed)
	mux.HandleFunc("POST /api/v1/podcasts/episodes/{episodeID}/favourite", s.postAPIPodcastFavourite)
}

type apiPodSettings struct {
	Speed      *float64 `json:"speed"`
	SkipIntroS int      `json:"skipIntroS"`
	SkipOutroS int      `json:"skipOutroS"`
	ServerAuto bool     `json:"serverAuto"`
	KeepLatest int      `json:"keepLatest"`
}

func apiPodSettingsOf(s podcast.ShowSettings) apiPodSettings {
	return apiPodSettings{Speed: s.Speed, SkipIntroS: s.SkipIntroS, SkipOutroS: s.SkipOutroS, ServerAuto: s.ServerAuto, KeepLatest: s.KeepLatest}
}

type apiPodShow struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Author      string         `json:"author"`
	ArtURL      string         `json:"artUrl,omitempty"`
	NewCount    int            `json:"newCount"`
	FollowedAt  int64          `json:"followedAt"`
	RefreshedAt int64          `json:"refreshedAt,omitempty"`
	Problem     string         `json:"problem,omitempty"`
	Settings    apiPodSettings `json:"settings"`
}

type apiPodState struct {
	PositionMs int64 `json:"positionMs"`
	Played     bool  `json:"played"`
	Favourite  bool  `json:"favourite"`
	UpdatedAt  int64 `json:"updatedAt"`
}

func apiPodStateOf(st podcast.EpisodeState) apiPodState {
	return apiPodState{PositionMs: st.PositionMs, Played: st.Played, Favourite: st.Favourite, UpdatedAt: st.UpdatedAt}
}

type apiPodEpisode struct {
	ID          string      `json:"id"`
	ShowID      string      `json:"showId"`
	Title       string      `json:"title"`
	Show        string      `json:"show"`
	ArtURL      string      `json:"artUrl,omitempty"`
	PublishedAt int64       `json:"publishedAt"`
	DurationMs  int64       `json:"durationMs,omitempty"`
	Kind        string      `json:"kind"`
	Season      int         `json:"season,omitempty"`
	Number      int         `json:"number,omitempty"`
	Gone        bool        `json:"gone,omitempty"`
	State       apiPodState `json:"state"`
}

// apiPodPlayback is the document the app plays from.
type apiPodPlayback struct {
	apiPodEpisode
	AudioURL    string         `json:"audioUrl"`
	Mime        string         `json:"mime"`
	Bytes       int64          `json:"bytes,omitempty"`
	ChaptersURL string         `json:"chaptersUrl,omitempty"`
	Settings    apiPodSettings `json:"settings"`
}

func apiPodArt(showID string) string { return "/api/v1/podcasts/art/" + url.PathEscape(showID) }

func apiPodEpisodeOf(ep podcast.Episode) apiPodEpisode {
	return apiPodEpisode{ID: ep.ID, ShowID: ep.ShowID, Title: ep.Title, Show: ep.ShowTitle, ArtURL: apiPodArt(ep.ShowID),
		PublishedAt: ep.PublishedAt, DurationMs: ep.DurationMs, Kind: ep.Kind, Season: ep.Season, Number: ep.Number,
		Gone: ep.GoneAt > 0, State: apiPodStateOf(ep.State)}
}

func (s *server) getAPIPodcasts(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	shows, err := s.podcasts.Shows(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "api podcasts", err)
		return
	}
	out := make([]apiPodShow, 0, len(shows))
	for _, sh := range shows {
		v := apiPodShow{ID: sh.ID, Title: sh.Title, Author: sh.Author, NewCount: sh.NewCount, FollowedAt: sh.FollowedAt,
			RefreshedAt: sh.OKAt, Problem: podcast.FailText(sh.FailCode), Settings: apiPodSettingsOf(sh.Settings)}
		if sh.ArtSHA != "" {
			v.ArtURL = apiPodArt(sh.ID)
		}
		out = append(out, v)
	}
	api.Write(w, http.StatusOK, newFeed(out, "", s.now().UnixMilli()))
}

func (s *server) getAPIPodcastEpisodes(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	since, ok := apiSince(w, r)
	if !ok {
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			apiBadField(w, "limit", "Limit must be a whole number, 1 or more.")
			return
		}
		limit = n
	}
	// The show must be one the person follows: Episodes does not check, Show does.
	if _, serr := s.podcasts.Show(r.Context(), currentUser(r).ID, r.PathValue("showID")); errors.Is(serr, podcast.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	eps, next, err := s.podcasts.Episodes(r.Context(), currentUser(r).ID, r.PathValue("showID"), podcast.EpisodeQuery{
		Since: since, After: r.URL.Query().Get("after"), Limit: limit})
	if errors.Is(err, podcast.ErrInvalid) {
		apiBadField(w, "after", "That page marker is not valid.")
		return
	}
	if err != nil {
		s.serverError(w, r, "api podcast episodes", err)
		return
	}
	items := make([]apiPodEpisode, 0, len(eps))
	for _, ep := range eps {
		items = append(items, apiPodEpisodeOf(ep))
	}
	api.Write(w, http.StatusOK, newFeed(items, next, s.now().UnixMilli()))
}

func (s *server) getAPIPodcastsNew(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	since, ok := apiSince(w, r)
	if !ok {
		return
	}
	eps, err := s.podcasts.NewEpisodes(r.Context(), currentUser(r).ID, apiFeedDefault, since)
	if err != nil {
		s.serverError(w, r, "api new episodes", err)
		return
	}
	items := make([]apiPodEpisode, 0, len(eps))
	for _, ep := range eps {
		items = append(items, apiPodEpisodeOf(ep))
	}
	api.Write(w, http.StatusOK, newFeed(items, "", s.now().UnixMilli()))
}

func (s *server) getAPIPodcastEpisode(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	api.Write(w, http.StatusOK, s.apiPodPlaybackOf(r, ep))
}

// apiPodPlaybackOf is the playback document of an episode the person may listen to.
func (s *server) apiPodPlaybackOf(r *http.Request, ep podcast.Episode) apiPodPlayback {
	var set podcast.ShowSettings
	if sh, err := s.podcasts.Show(r.Context(), currentUser(r).ID, ep.ShowID); err == nil {
		set = sh.Settings
	}
	doc := apiPodPlayback{apiPodEpisode: apiPodEpisodeOf(ep), AudioURL: "/api/v1/podcasts/audio/" + url.PathEscape(ep.ID),
		Mime: audioMime(ep), Bytes: ep.AudioBytes, Settings: apiPodSettingsOf(set)}
	if ep.ChaptersURL != "" || ep.ChaptersJSON != "" {
		doc.ChaptersURL = "/api/v1/podcasts/chapters/" + url.PathEscape(ep.ID)
	}
	return doc
}

// apiPodProgressIn is one place as the app sends it; the pointers tell "missing" from zero.
type apiPodProgressIn struct {
	EpisodeID       string `json:"episodeId"`
	PositionMs      *int64 `json:"positionMs"`
	ClientUpdatedAt *int64 `json:"clientUpdatedAt"`
	DeviceID        string `json:"deviceId"`
	Finished        bool   `json:"finished"`
}

type apiPodBatchResult struct {
	EpisodeID string         `json:"episodeId"`
	Applied   bool           `json:"applied"`
	State     *apiPodState   `json:"state"`
	Error     *apiBatchError `json:"error"`
}

func (s *server) postAPIPodcastProgressBatch(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	var body struct {
		Updates []apiPodProgressIn `json:"updates"`
	}
	if !apiJSONBody(w, r, progressBatchLimit, &body) {
		return
	}
	if n := len(body.Updates); n < 1 || n > progressBatchMax {
		apiBadField(w, "updates", "Send 1 to "+strconv.Itoa(progressBatchMax)+" updates at a time.")
		return
	}
	u := currentUser(r)
	results := make([]apiPodBatchResult, 0, len(body.Updates))
	for _, up := range body.Updates {
		res := apiPodBatchResult{EpisodeID: up.EpisodeID}
		fail := func(code, msg, field string) { res.Error = &apiBatchError{Code: code, Message: msg, Field: field} }
		switch {
		case !s.can.Can(r.Context(), u, share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: up.EpisodeID}):
			fail(api.CodeNotFound, "There is nothing at this address.", "")
		case up.PositionMs == nil:
			fail(api.CodeInvalid, "That value is missing or not valid.", "positionMs")
		case up.ClientUpdatedAt == nil:
			fail(api.CodeInvalid, "That value is missing or not valid.", "clientUpdatedAt")
		default:
			saved, err := s.podcasts.SaveProgress(r.Context(), u.ID, up.EpisodeID, podcast.Progress{
				PositionMs: *up.PositionMs, ClientUpdatedAt: *up.ClientUpdatedAt, DeviceID: up.DeviceID, Finished: up.Finished})
			var fe *podcast.FieldError
			switch {
			case errors.As(err, &fe):
				fail(api.CodeInvalid, "That value is not valid.", camelField(fe.Field))
			case errors.Is(err, podcast.ErrNotFound):
				fail(api.CodeNotFound, "There is nothing at this address.", "")
			case err != nil:
				s.serverError(w, r, "save episode progress", err)
				return
			default:
				st := apiPodStateOf(saved.State)
				res.Applied, res.State = saved.Applied, &st
			}
		}
		results = append(results, res)
	}
	api.Write(w, http.StatusOK, map[string]any{"results": results, "serverNow": s.now().UnixMilli()})
}

// apiPodFlag is the body of the played and favourite writes.
type apiPodFlag struct {
	Played *bool `json:"played"`
	On     *bool `json:"on"`
}

func (s *server) postAPIPodcastPlayed(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	var in apiPodFlag
	if !apiJSONBody(w, r, apiBookmarkBody, &in) {
		return
	}
	if in.Played == nil {
		apiBadField(w, "played", "Send played as true or false.")
		return
	}
	if err := s.podcasts.SetPlayed(r.Context(), currentUser(r).ID, ep.ID, *in.Played); err != nil {
		s.serverError(w, r, "mark played", err)
		return
	}
	s.writeAPIPodState(w, r, ep.ID)
}

func (s *server) postAPIPodcastFavourite(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	var in apiPodFlag
	if !apiJSONBody(w, r, apiBookmarkBody, &in) {
		return
	}
	if in.On == nil {
		apiBadField(w, "on", "Send on as true or false.")
		return
	}
	if err := s.podcasts.SetFavourite(r.Context(), currentUser(r).ID, ep.ID, *in.On); err != nil {
		s.serverError(w, r, "star episode", err)
		return
	}
	s.writeAPIPodState(w, r, ep.ID)
}

func (s *server) writeAPIPodState(w http.ResponseWriter, r *http.Request, id string) {
	ep, err := s.podcasts.Episode(r.Context(), currentUser(r).ID, id)
	if err != nil {
		s.serverError(w, r, "episode state", err)
		return
	}
	api.Write(w, http.StatusOK, map[string]any{"state": apiPodStateOf(ep.State)})
}
