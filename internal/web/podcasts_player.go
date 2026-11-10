package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// podEpisodeKind is the player fragment's data-kind for a podcast episode (player.js reads it).
const podEpisodeKind = "episode"

// episodeMime is the type the player gives canPlayType for an episode's one file: the feed's audio type, or MP3 when
// the feed named none (the proxy serves audio types only).
func episodeMime(ep podcast.Episode) string {
	t := strings.ToLower(strings.TrimSpace(ep.AudioType))
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if strings.HasPrefix(t, "audio/") {
		return t
	}
	return "audio/mpeg"
}

// episodePlayerView gathers what the player needs for an episode the person may listen to. The speed is the show's
// own when the person set one, else their usual speed. Chapters come from the stored file; the player asks
// ChaptersHref for them on the first ask when the feed names a file and none is stored yet.
func (s *server) episodePlayerView(ctx context.Context, u users.User, ep podcast.Episode) (components.PlayerDataView, error) {
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	var skipIntro, skipOutro int
	if ss, err := s.podcasts.ShowSettings(ctx, u.ID, ep.ShowID); err == nil {
		if ss.Speed != nil {
			set.Speed = *ss.Speed
		}
		skipIntro, skipOutro = ss.SkipIntroS, ss.SkipOutroS
	} else if !errors.Is(err, podcast.ErrNotFound) {
		return components.PlayerDataView{}, err
	}
	id := url.PathEscape(ep.ID)
	v := components.PlayerDataView{
		BookID: ep.ID, Title: ep.Title, Author: ep.ShowTitle,
		DurationMs: ep.DurationMs, PositionMs: ep.State.PositionMs, Finished: ep.State.Played,
		UpdatedAt: ep.State.ClientUpdated, DeviceID: ep.State.DeviceID,
		ServerNow: s.now().UnixMilli(),
		Speed:     strconv.FormatFloat(set.Speed, 'f', 2, 64),
		SkipBackS: set.SkipBackS, SkipForwardS: set.SkipForwardS,
		AutoRewind: set.AutoRewind, Rewind: rewindAttr(),
		Kind: podEpisodeKind, ProgressHref: "/podcasts/progress/" + id,
		ShowHref:    "/podcasts/shows/" + url.PathEscape(ep.ShowID),
		SkipIntroMs: int64(skipIntro) * 1000, SkipOutroMs: int64(skipOutro) * 1000,
		Files: []components.PlayerFileView{{
			ID: ep.ID, DurationMs: ep.DurationMs, Mime: episodeMime(ep), Playable: !podIsVideo(ep),
			Href: "/podcasts/audio/" + id,
		}},
	}
	// The show's picture is the cover (and the lock screen artwork) once one is stored; without it the bar shows its letter tile.
	if sh, err := s.podcasts.ShowByID(ctx, ep.ShowID); err == nil && len(sh.ArtSHA) >= 8 {
		v.CoverURL = "/podcasts/art/" + url.PathEscape(ep.ShowID) + "?v=" + sh.ArtSHA[:8] + "&w=512"
	}
	chapters := episodeChapters(ep)
	for i, c := range chapters {
		end := ep.DurationMs
		if i+1 < len(chapters) {
			end = chapters[i+1].StartMs
		}
		v.Chapters = append(v.Chapters, components.PlayerChapterView{Idx: i, StartMs: c.StartMs, EndMs: max(end, c.StartMs), Title: c.Title})
	}
	if len(chapters) == 0 && ep.ChaptersURL != "" {
		v.ChaptersHref = "/podcasts/chapters/" + id
	}
	return v, nil
}

// getPlayerEpisode answers GET /hx/player/episode/{episodeID}: the player fragment of an episode the person may listen to.
func (s *server) getPlayerEpisode(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	s.renderEpisodePlayer(w, r, ep)
}

func (s *server) renderEpisodePlayer(w http.ResponseWriter, r *http.Request, ep podcast.Episode) {
	v, err := s.episodePlayerView(r.Context(), currentUser(r), ep)
	if err != nil {
		s.serverError(w, r, "build episode player", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.PlayerData(v))
}

// lastEpisode is the person's newest unfinished episode, when podcasts are on and they may still listen to it.
func (s *server) lastEpisode(r *http.Request) (podcast.Episode, bool, error) {
	if !s.podcastsOn(r) {
		return podcast.Episode{}, false, nil
	}
	u := currentUser(r)
	ep, err := s.podcasts.LastUnfinished(r.Context(), u.ID)
	if errors.Is(err, podcast.ErrNotFound) {
		return podcast.Episode{}, false, nil
	}
	if err != nil {
		return podcast.Episode{}, false, err
	}
	if podIsVideo(ep) || !s.can.Can(r.Context(), u, share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: ep.ID}) {
		return podcast.Episode{}, false, nil
	}
	return ep, true, nil
}

// homeNewLimit is how many new episodes Home's shelf shows.
const homeNewLimit = 10

// homeNewEpisodes is Home's "New episodes" shelf: the person's new episodes, newest first. It is empty (and the
// shelf is hidden) when podcasts are off, the person may not use them, or nothing is new.
func (s *server) homeNewEpisodes(r *http.Request) ([]components.EpisodeRowView, error) {
	u := currentUser(r)
	if !s.podcastsOn(r) || !s.can.Can(r.Context(), u, share.UsePodcasts, podServer) {
		return nil, nil
	}
	eps, err := s.podcasts.NewEpisodes(r.Context(), u.ID, homeNewLimit, 0)
	if err != nil {
		return nil, err
	}
	var rows []components.EpisodeRowView
	for _, ep := range eps {
		rows = append(rows, s.podRow(r, ep, "/", 0, true))
	}
	return rows, nil
}
