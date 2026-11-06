package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/watch"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Watch folders (S1): "Watch for new books" and the scheduled scan, per library, on the Libraries page.
// Owner and admins (they manage libraries); everyone else gets a 404.

func (s *server) registerWatch(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/libraries/{id}/watch", s.postWatch)
}

// watchChoices are the allowed intervals in minutes; 0 is never. Six hours is the default.
var watchChoices = []pages.Option{
	{Value: "0", Label: watchNever},
	{Value: "60", Label: watchHour},
	{Value: "360", Label: watchSixHours},
	{Value: "720", Label: watchTwelveHours},
	{Value: "1440", Label: watchDay},
}

// watchStatus is the plain sentence under a library's switch.
func (s *server) watchStatus(l library.Library) string {
	if !l.Watch {
		return watchStatusOff
	}
	if s.watcher == nil {
		return watchStatusOn
	}
	switch s.watcher.Mode(l.ID) {
	case watch.ModeSchedule:
		return watchStatusSched
	case watch.ModeUnavailable:
		return watchStatusGone
	case watch.ModeOff:
		return watchStatusOff
	}
	return watchStatusOn
}

// adminWatchView is the "Finding new books" section: one row per library.
func (s *server) adminWatchView(r *http.Request) (*pages.AdminWatchView, error) {
	libs, err := s.lib.Libraries(r.Context())
	if err != nil || len(libs) == 0 {
		return nil, err
	}
	v := &pages.AdminWatchView{Title: watchTitle, Lead: watchLead, WatchLabel: watchLabel, EveryLabel: watchEveryLabel,
		Save: watchSave, Choices: watchChoices}
	for _, l := range libs {
		v.Libraries = append(v.Libraries, pages.AdminWatchLibrary{ID: l.ID, Name: l.Name, Watch: l.Watch,
			Every: strconv.Itoa(l.ScanIntervalMin), Status: s.watchStatus(l)})
	}
	return v, nil
}

func (s *server) postWatch(w http.ResponseWriter, r *http.Request) {
	if !s.allowLibraries(w, r) || !s.parseForm(w, r) {
		return
	}
	l, err := s.lib.Library(r.Context(), r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load library", err)
		return
	}
	// The form sends a hidden 0 before the checkbox's 1, so the last value is the answer.
	on := lastValue(r.PostForm["on"])
	every := -1
	for _, c := range watchChoices {
		if c.Value == r.PostForm.Get("every") {
			every, _ = strconv.Atoi(c.Value)
		}
	}
	if (on != "0" && on != "1") || every < 0 {
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, watchErrValue)
		return
	}
	if err := s.lib.SetWatch(r.Context(), l.ID, on == "1", every); err != nil {
		s.serverError(w, r, "save watch setting", err)
		return
	}
	s.audit(r.Context(), currentUser(r).ID, "library.watch_set", l.ID, map[string]any{"watch": on == "1", "every_min": every})
	if s.watcher != nil {
		s.watcher.Nudge()
	}
	s.answerSetting(w, r, "/admin/libraries")
}
