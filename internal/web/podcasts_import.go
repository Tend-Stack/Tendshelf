package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// importShowsWord is "1 show" or "N shows".
func importShowsWord(n int) string {
	if n == 1 {
		return "1 show"
	}
	return fmt.Sprintf("%d shows", n)
}

// importResult is what the import job leaves (podcast.handleImport): shows followed, shows that could not be
// followed, shows refused by the follow limit, and, when the follow budget ran out, that it stopped and how
// many addresses were not tried.
type importResult struct {
	Followed int `json:"followed"`
	Skipped  int `json:"skipped"`
	TooMany  int `json:"too_many"`
	Stopped  int `json:"stopped"`
	NotDone  int `json:"not_done"`
}

// podImportStatus puts a job's state in plain words. Nothing here comes from a feed.
func podImportStatus(j jobs.Job) pages.PodImportStatusView {
	v := pages.PodImportStatusView{JobID: j.ID, Label: podImportLabel}
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
		v.Pending = true
		var p struct{ Done, Of int }
		if json.Unmarshal(j.Progress, &p) == nil && p.Of > 0 {
			v.Lines = []string{fmt.Sprintf(podImportRunningN, min(p.Done, p.Of), p.Of)}
		} else {
			v.Lines = []string{podImportRunning}
		}
	case jobs.StateDone:
		var r importResult
		_ = json.Unmarshal(j.Result, &r)
		switch {
		case r.Followed > 0:
			v.Lines = append(v.Lines, fmt.Sprintf(podImportDone, importShowsWord(r.Followed)))
		case r.Stopped == 0 && r.Skipped == 0 && r.TooMany == 0:
			v.Lines = append(v.Lines, podImportNothing)
		}
		if r.TooMany > 0 {
			v.Lines = append(v.Lines, fmt.Sprintf(podImportTooMany, importShowsWord(r.TooMany)))
		}
		if r.Skipped > 0 {
			v.Lines = append(v.Lines, fmt.Sprintf(podImportSkipped, importShowsWord(r.Skipped)))
		}
		if r.Stopped > 0 {
			v.Lines = append(v.Lines, fmt.Sprintf(podImportStopped, importShowsWord(r.NotDone)))
		}
	default:
		v.Failed = true
		v.Lines = []string{podImportFailed}
	}
	return v
}

// importRecent is how long a finished import stays on the Podcasts home.
const importRecent = 30 * time.Minute

// podImportView is the "Bring your podcasts" section: the form, and the person's latest import while it runs or just finished.
func (s *server) podImportView(r *http.Request) *pages.PodImportView {
	v := &pages.PodImportView{CSRF: s.csrf.TokenFrom(r.Context()), Title: podImportTitle, Hint: podImportHint,
		FileLabel: podImportFile, Button: podImportButton, ExportLabel: podExportButton, ExportHref: "/podcasts/opml"}
	j, ok, err := s.jobs.LatestFor(r.Context(), podcast.KindImport, "user_id", currentUser(r).ID)
	if err == nil && ok && (!jobFinished(j) || s.now().Sub(j.UpdatedAt) < importRecent) {
		v.Status = podImportStatus(j)
	}
	return v
}

func jobFinished(j jobs.Job) bool {
	switch j.State {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
		return false
	}
	return true
}

// getPodcastImportJob answers GET /hx/jobs/{id} for an import: only the person who started it sees it, and only while
// podcasts are on; anyone else gets the plain 404.
func (s *server) getPodcastImportJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	if !s.podcastsOn(r) || j.CreatedBy != currentUser(r).ID {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.PodImportStatus(podImportStatus(j)))
}
