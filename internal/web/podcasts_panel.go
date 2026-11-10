package web

import (
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// getPodcastPanel answers GET /hx/player/episode-panel/{episodeID}: the desktop Now playing panel of an episode the
// person may listen to (same gate as the player fragment). It holds the facts, the stored chapters, the show notes and
// the episode's action forms. It reads the chapters already stored (episodeChapters) and fetches nothing.
func (s *server) getPodcastPanel(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	ev := s.episodeView(r, ep)
	ev.NoPlay = true
	v := pages.PodEpisodePanelView{
		EpisodeID: ep.ID, AriaLabel: podPanelLabel, Meta: ev.Meta,
		ChaptersTitle: podPanelChapters, Notes: rss.CleanHTML(ep.NotesHTML, rss.NotesMax), NotesTitle: podAboutEpisode,
		Episode: ev, ActionsLabel: podPanelActions, ShowLinkLabel: podGoToShow, EpLinkLabel: podOpenEpisode,
		EpisodeHref: "/podcasts/episodes/" + url.PathEscape(ep.ID),
	}
	chapters := episodeChapters(ep)
	rows := make([]components.ChapterRow, 0, len(chapters))
	for i, c := range chapters {
		end := ep.DurationMs
		if i+1 < len(chapters) {
			end = chapters[i+1].StartMs
		}
		rows = append(rows, components.ChapterRow{
			Num: i + 1, Title: c.Title, Dur: durationText(max(end-c.StartMs, 0)), PlayAtMs: c.StartMs,
			Href: "/podcasts/audio/" + url.PathEscape(ep.ID),
		})
	}
	if !ev.Video {
		v.Chapters = components.ChapterListView{Rows: rows, BookID: ep.ID}
	} else {
		v.Chapters = components.ChapterListView{Rows: rows}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	s.fragment(w, r, http.StatusOK, pages.PodEpisodePanel(v))
}
