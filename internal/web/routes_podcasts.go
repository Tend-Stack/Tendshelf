package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/opml"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Podcasts (decision 0038, plan P2). Every route answers the plain 404 when the feature is off (the owner's
// setting, or no service in Options), then needs share.UsePodcasts; an episode needs share.ListenEpisode,
// which reads the person's follow or state row from the database. The pages here are working stubs: they carry
// the data, the forms and the authorization, and P3 replaces their markup.

// podServer is the target every podcast action names.
var podServer = share.Target{Kind: share.TargetServer}

// podcastLimits are the per-person rate limits: search 20 a minute, follow and add 30 an hour.
type podcastLimits struct{ search, follow *auth.Limiter }

func (s *server) registerPodcasts(mux *http.ServeMux) {
	notAllowed := func(allow string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			s.plainError(w, r, http.StatusMethodNotAllowed, "Method not allowed.")
		}
	}
	mux.HandleFunc("GET /podcasts", s.getPodcasts)
	mux.HandleFunc("GET /podcasts/search", s.getPodcastSearch)
	mux.HandleFunc("POST /podcasts/follow", s.postPodcastFollow)
	mux.HandleFunc("GET /podcasts/shows/{showID}", s.getPodcastShow)
	mux.HandleFunc("POST /podcasts/shows/{showID}/unfollow", s.postPodcastUnfollow)
	mux.HandleFunc("POST /podcasts/shows/{showID}/refresh", s.postPodcastRefresh)
	mux.HandleFunc("POST /podcasts/shows/{showID}/seen", s.postPodcastSeen)
	mux.HandleFunc("POST /podcasts/shows/{showID}/settings", s.postPodcastShowSettings)
	mux.HandleFunc("GET /podcasts/episodes/{episodeID}", s.getPodcastEpisode)
	mux.HandleFunc("POST /podcasts/episodes/{episodeID}/played", s.postPodcastPlayed)
	mux.HandleFunc("POST /podcasts/episodes/{episodeID}/favourite", s.postPodcastFavourite)
	mux.HandleFunc("POST /podcasts/progress/{episodeID}", s.postPodcastProgress)
	mux.HandleFunc("GET /podcasts/audio/{episodeID}", s.getEpisodeAudio)
	mux.HandleFunc("GET /podcasts/art/{showID}", s.getPodcastArt)
	mux.HandleFunc("GET /podcasts/chapters/{episodeID}", s.getPodcastChapters)
	mux.HandleFunc("GET /hx/player/episode/{episodeID}", s.getPlayerEpisode)
	mux.HandleFunc("GET /podcasts/opml", s.getPodcastOPML)
	mux.HandleFunc("POST /podcasts/opml", s.postPodcastOPML)
	mux.HandleFunc("GET /admin/podcasts", s.getAdminPodcasts)
	mux.HandleFunc("POST /admin/podcasts", s.postAdminPodcasts)
	// Without these the catch-all answers every other method with 404.
	mux.HandleFunc("/podcasts/audio/{episodeID}", notAllowed("GET, HEAD"))
	mux.HandleFunc("/podcasts/art/{showID}", notAllowed("GET, HEAD"))
	mux.HandleFunc("/podcasts/chapters/{episodeID}", notAllowed("GET, HEAD"))
}

// podcastsOn is true when a service is configured and the owner turned podcasts on.
func (s *server) podcastsOn(r *http.Request) bool {
	return s.podcasts != nil && s.podcasts.Enabled(r.Context())
}

// podcastsGate answers 404 unless podcasts are on and the signed-in person may use them.
func (s *server) podcastsGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.podcastsOn(r) {
		s.notFound(w, r)
		return false
	}
	return s.allow(w, r, share.UsePodcasts, podServer)
}

// loadEpisode loads an episode the person may open (follows its show, or has state for it). An unknown
// id and a refused person get the same 404, and Can runs before anything else is read.
func (s *server) loadEpisode(w http.ResponseWriter, r *http.Request) (podcast.Episode, bool) {
	if !s.podcastsOn(r) {
		s.notFound(w, r)
		return podcast.Episode{}, false
	}
	id := r.PathValue("episodeID")
	if !s.allow(w, r, share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: id}) {
		return podcast.Episode{}, false
	}
	ep, err := s.podcasts.Episode(r.Context(), currentUser(r).ID, id)
	if errors.Is(err, podcast.ErrNotFound) {
		s.notFound(w, r)
		return podcast.Episode{}, false
	}
	if err != nil {
		s.serverError(w, r, "load episode", err)
		return podcast.Episode{}, false
	}
	return ep, true
}

// loadShow loads a show the person follows.
func (s *server) loadShow(w http.ResponseWriter, r *http.Request) (podcast.ShowRow, bool) {
	sh, err := s.podcasts.Show(r.Context(), currentUser(r).ID, r.PathValue("showID"))
	if errors.Is(err, podcast.ErrNotFound) {
		s.notFound(w, r)
		return podcast.ShowRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "load show", err)
		return podcast.ShowRow{}, false
	}
	return sh, true
}

func (s *server) postPodcastFollow(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	if ok, wait := s.podLim.follow.Allow(u.ID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		s.refuseSetting(w, r, http.StatusTooManyRequests, podErrBusy)
		return
	}
	s.podLim.follow.Fail(u.ID)
	feed, slug := strings.TrimSpace(r.PostForm.Get("feed_url")), strings.TrimSpace(r.PostForm.Get("slug"))
	switch {
	case feed != "" && slug == "":
	case feed == "" && slug != "" && len(slug) <= 200:
		var err error
		if feed, err = s.podcasts.Net.PodcastFeedURL(ctx, slug); err != nil {
			var pe *podnet.Error
			if errors.As(err, &pe) && pe.Code == "not_found" {
				s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrNoFeed)
				return
			}
			s.refuseSetting(w, r, http.StatusBadGateway, podErrSearchDown)
			return
		}
	default:
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrBadAddress)
		return
	}
	id, _, err := s.podcasts.Follow(ctx, u.ID, feed, slug)
	switch {
	case errors.Is(err, podcast.ErrBadURL):
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrBadAddress)
		return
	case errors.Is(err, podcast.ErrTooMany):
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrTooMany)
		return
	case err != nil:
		s.serverError(w, r, "follow show", err)
		return
	}
	if sh, err := s.podcasts.ShowByID(ctx, id); err == nil && sh.FetchedAt == 0 {
		if _, err := s.podcasts.QueueRefresh(ctx, id, u.ID); err != nil {
			s.podcasts.Log.Warn("podcasts: could not queue the first refresh", "err", err)
		}
	}
	s.answerPage(w, r, "/podcasts/shows/"+url.PathEscape(id))
}

func (s *server) postPodcastUnfollow(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	switch err := s.podcasts.Unfollow(r.Context(), currentUser(r).ID, r.PathValue("showID")); {
	case errors.Is(err, podcast.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "unfollow show", err)
	default:
		s.answerPage(w, r, "/podcasts")
	}
}

func (s *server) postPodcastRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	id := r.PathValue("showID")
	switch _, err := s.podcasts.RefreshNow(r.Context(), currentUser(r).ID, id); {
	case errors.Is(err, podcast.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, podcast.ErrTooSoon):
		w.Header().Set("Retry-After", "300")
		s.refuseSetting(w, r, http.StatusTooManyRequests, podErrTooSoon)
	case err != nil:
		s.serverError(w, r, "refresh show", err)
	default:
		s.answerPage(w, r, "/podcasts/shows/"+url.PathEscape(id))
	}
}

func (s *server) postPodcastSeen(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	id := r.PathValue("showID")
	switch err := s.podcasts.MarkSeen(r.Context(), currentUser(r).ID, id); {
	case errors.Is(err, podcast.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "mark seen", err)
	default:
		s.answerPage(w, r, "/podcasts/shows/"+url.PathEscape(id))
	}
}

func (s *server) postPodcastShowSettings(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) || !s.parseForm(w, r) {
		return
	}
	sh, ok := s.loadShow(w, r)
	if !ok {
		return
	}
	f := r.PostForm
	set := sh.Settings // fields not sent keep their value
	bad := false
	num := func(key string, dst *int) {
		if v := f.Get(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				bad = true
				return
			}
			*dst = n
		}
	}
	if f.Has("speed") {
		set.Speed = nil
		if v := f.Get("speed"); v != "" {
			sp, err := strconv.ParseFloat(v, 64)
			if err != nil || sp != sp {
				bad = true
			}
			set.Speed = &sp
		}
	}
	num("skip_intro_s", &set.SkipIntroS)
	num("skip_outro_s", &set.SkipOutroS)
	num("keep_latest", &set.KeepLatest)
	if f.Has("server_auto") {
		v, ok := flag(lastValue(f["server_auto"]))
		bad = bad || !ok
		// The per-show choice only counts while the owner allows server auto-download.
		set.ServerAuto = v && s.podcasts.Settings(r.Context()).ServerAuto
	}
	if bad {
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrBadValue)
		return
	}
	switch err := s.podcasts.SetShowSettings(r.Context(), currentUser(r).ID, sh.ID, set); {
	case errors.Is(err, podcast.ErrInvalid):
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrBadValue)
	case err != nil:
		s.serverError(w, r, "show settings", err)
	default:
		s.answerSetting(w, r, "/podcasts/shows/"+url.PathEscape(sh.ID))
	}
}

// podFlagForm reads one 0/1 field of a small form; a missing or odd value is a 400.
func (s *server) podFlagForm(w http.ResponseWriter, r *http.Request, key string) (bool, bool) {
	if !s.parseForm(w, r) {
		return false, false
	}
	v := r.PostForm.Get(key)
	if v != "0" && v != "1" {
		s.errorPage(w, r, http.StatusBadRequest)
		return false, false
	}
	return v == "1", true
}

func (s *server) postPodcastPlayed(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	played, ok := s.podFlagForm(w, r, "played")
	if !ok {
		return
	}
	if err := s.podcasts.SetPlayed(r.Context(), currentUser(r).ID, ep.ID, played); err != nil {
		s.serverError(w, r, "mark played", err)
		return
	}
	s.answerPage(w, r, backTarget(r, "/podcasts/episodes/"+url.PathEscape(ep.ID)))
}

func (s *server) postPodcastFavourite(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	on, ok := s.podFlagForm(w, r, "on")
	if !ok {
		return
	}
	if err := s.podcasts.SetFavourite(r.Context(), currentUser(r).ID, ep.ID, on); err != nil {
		s.serverError(w, r, "star episode", err)
		return
	}
	s.answerPage(w, r, backTarget(r, "/podcasts/episodes/"+url.PathEscape(ep.ID)))
}

// podProgressBody bounds a progress save: a few short fields.
const podProgressBody = 4 << 10

// postPodcastProgress answers POST /podcasts/progress/{episodeID} like POST /progress/{bookID}: a urlencoded
// form, a JSON answer, the newest save wins.
func (s *server) postPodcastProgress(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		jsonError(w, http.StatusUnsupportedMediaType, "unsupported", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, podProgressBody)
	if err := r.ParseForm(); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid", "")
		return
	}
	f := r.PostForm
	pos, err := strconv.ParseInt(f.Get("position_ms"), 10, 64)
	if err != nil || pos < 0 {
		jsonError(w, http.StatusBadRequest, "invalid", "position_ms")
		return
	}
	cu, err := strconv.ParseInt(f.Get("client_updated_at"), 10, 64)
	if err != nil || cu <= 0 {
		jsonError(w, http.StatusBadRequest, "invalid", "client_updated_at")
		return
	}
	fin, ok := flag(f.Get("finished"))
	if !ok {
		jsonError(w, http.StatusBadRequest, "invalid", "finished")
		return
	}
	res, err := s.podcasts.SaveProgress(r.Context(), currentUser(r).ID, ep.ID, podcast.Progress{
		PositionMs: pos, ClientUpdatedAt: cu, DeviceID: f.Get("device_id"), Finished: fin})
	var fe *podcast.FieldError
	switch {
	case errors.As(err, &fe):
		jsonError(w, http.StatusBadRequest, "invalid", fe.Field)
	case errors.Is(err, podcast.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "save episode progress", err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"applied": res.Applied, "state": podcastStateJSON(res.State), "serverNow": s.now().UnixMilli()})
	}
}

func podcastStateJSON(st podcast.EpisodeState) map[string]any {
	return map[string]any{"positionMs": st.PositionMs, "played": st.Played, "favourite": st.Favourite, "updatedAt": st.UpdatedAt}
}

func (s *server) getPodcastOPML(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	shows, err := s.podcasts.Shows(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "export opml", err)
		return
	}
	out := make([]opml.Outline, 0, len(shows))
	for _, sh := range shows {
		out = append(out, opml.Outline{Title: sh.Title, XMLURL: sh.FeedURL})
	}
	h := w.Header()
	h.Set("Content-Type", "text/x-opml; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+podcastsOPMLFileName+`"`)
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_ = opml.Write(w, "Tendshelf podcasts", out)
}

// postPodcastOPML reads an uploaded OPML list and queues one import job. The CSRF check has usually parsed
// the multipart form already (up to its 1 MiB form limit).
func (s *server) postPodcastOPML(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	if r.MultipartForm == nil {
		r.Body = http.MaxBytesReader(w, r.Body, opml.MaxBytes+(64<<10))
		if err := r.ParseMultipartForm(opml.MaxBytes + (64 << 10)); err != nil {
			status, text := http.StatusUnprocessableEntity, podErrNoFile
			if !errors.Is(err, http.ErrNotMultipart) {
				status, text = http.StatusRequestEntityTooLarge, podErrOPMLBig
			}
			s.refuseSetting(w, r, status, text)
			return
		}
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrNoFile)
		return
	}
	f, err := files[0].Open()
	if err != nil {
		s.serverError(w, r, "open opml", err)
		return
	}
	defer f.Close()
	outlines, err := opml.Parse(f)
	switch {
	case errors.Is(err, opml.ErrTooLarge):
		s.refuseSetting(w, r, http.StatusRequestEntityTooLarge, podErrOPMLBig)
		return
	case err != nil || len(outlines) == 0:
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, podErrNotOPML)
		return
	}
	urls := make([]string, 0, len(outlines))
	for _, o := range outlines {
		urls = append(urls, o.XMLURL)
	}
	if _, err := s.podcasts.QueueImport(r.Context(), currentUser(r).ID, urls); err != nil {
		s.serverError(w, r, "queue opml import", err)
		return
	}
	s.answerPage(w, r, "/podcasts")
}
