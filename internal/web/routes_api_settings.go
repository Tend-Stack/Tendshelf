package web

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The app's playback settings and Continue shelf (decision 0026). Both credentials reach these
// routes; every one acts for the caller only, and a book goes through share.Can before anything about
// it is read or written. The settings answer carries the player's keys and nothing else: no theme, no
// account, no server setting.

const (
	apiSettingsBody     = 4 << 10 // a PATCH of five small values
	apiSpeedBody        = 1 << 10
	apiContinueDefault  = 12
	apiContinueMax      = 24
	apiSettingsBadSpeed = "Speed must be from 0.5 to 3, in steps of 0.05."
	apiSettingsBadSkip  = "Skip must be 10, 15, 30 or 60 seconds."
	apiSettingsBadBool  = "This must be true or false."
)

func (s *server) registerAPISettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", s.getAPISettings)
	mux.HandleFunc("PATCH /api/v1/settings", s.patchAPISettings)
	mux.HandleFunc("PUT /api/v1/books/{id}/speed", s.putAPIBookSpeed)
	mux.HandleFunc("GET /api/v1/continue", s.getAPIContinue)
}

// apiSettings is the settings the app reads and writes.
type apiSettings struct {
	Speed        float64 `json:"speed"`
	SkipBackS    int     `json:"skipBackS"`
	SkipForwardS int     `json:"skipForwardS"`
	AutoRewind   bool    `json:"autoRewind"`
	NextInSeries bool    `json:"nextInSeries"`
}

func apiSettingsOf(set users.Settings) apiSettings {
	return apiSettings{Speed: set.Speed, SkipBackS: set.SkipBackS, SkipForwardS: set.SkipForwardS,
		AutoRewind: set.AutoRewind, NextInSeries: set.NextInSeries}
}

func (s *server) writeAPISettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.users.LoadSettings(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "api settings", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.Write(w, http.StatusOK, apiSettingsOf(set))
}

func (s *server) getAPISettings(w http.ResponseWriter, r *http.Request) {
	s.writeAPISettings(w, r)
}

// patchAPISettings overlays the keys present in the body on the person's settings and answers with
// the result. Unknown keys are ignored, so a newer app can send more. The first bad key, in the
// order of the answer, is named in the 400; nothing is saved then.
func (s *server) patchAPISettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !apiJSONBody(w, r, apiSettingsBody, &body) {
		return
	}
	u := currentUser(r)
	set, err := s.users.LoadSettings(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, "api settings", err)
		return
	}
	// Each key is checked on its own against settings that are valid already, so a bad value can only
	// be the key being tried.
	try := func(key, bad string, put func(*users.Settings, json.RawMessage) bool) bool {
		raw, present := body[key]
		if !present {
			return true
		}
		cand := set
		if !put(&cand, raw) || cand.Validate() != nil {
			apiBadField(w, key, bad)
			return false
		}
		set = cand
		return true
	}
	number := func(raw json.RawMessage) (float64, bool) {
		var v float64
		return v, json.Unmarshal(raw, &v) == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	whole := func(raw json.RawMessage) (int, bool) {
		v, ok := number(raw)
		return int(v), ok && v == math.Trunc(v)
	}
	flag := func(raw json.RawMessage) (bool, bool) {
		var v bool
		return v, json.Unmarshal(raw, &v) == nil
	}
	ok := try("speed", apiSettingsBadSpeed, func(c *users.Settings, raw json.RawMessage) bool {
		v, ok := number(raw)
		c.Speed = v
		return ok
	}) &&
		try("skipBackS", apiSettingsBadSkip, func(c *users.Settings, raw json.RawMessage) bool {
			v, ok := whole(raw)
			c.SkipBackS = v
			return ok
		}) &&
		try("skipForwardS", apiSettingsBadSkip, func(c *users.Settings, raw json.RawMessage) bool {
			v, ok := whole(raw)
			c.SkipForwardS = v
			return ok
		}) &&
		try("autoRewind", apiSettingsBadBool, func(c *users.Settings, raw json.RawMessage) bool {
			v, ok := flag(raw)
			c.AutoRewind = v
			return ok
		}) &&
		try("nextInSeries", apiSettingsBadBool, func(c *users.Settings, raw json.RawMessage) bool {
			v, ok := flag(raw)
			c.NextInSeries = v
			return ok
		})
	if !ok {
		return
	}
	if err := s.users.SaveSettings(r.Context(), u.ID, set); err != nil {
		if errors.Is(err, users.ErrInvalidSetting) {
			apiBadField(w, "speed", apiSettingsBadSpeed)
			return
		}
		s.serverError(w, r, "api settings save", err)
		return
	}
	s.writeAPISettings(w, r)
}

// putAPIBookSpeed remembers the person's speed for one book, or makes it their usual speed with
// asUsual. The book is checked first, so a hidden book answers 404 whatever the body says.
func (s *server) putAPIBookSpeed(w http.ResponseWriter, r *http.Request) {
	b, ok := s.playableBook(w, r, s.followMerged(r.Context(), r.PathValue("id")))
	if !ok {
		return
	}
	var body struct {
		Speed   *float64 `json:"speed"`
		AsUsual bool     `json:"asUsual"`
	}
	if !apiJSONBody(w, r, apiSpeedBody, &body) {
		return
	}
	if body.Speed == nil {
		apiBadField(w, "speed", apiSettingsBadSpeed)
		return
	}
	var err error
	if body.AsUsual {
		err = s.users.UseAsUsualSpeed(r.Context(), currentUser(r).ID, b.ID, *body.Speed)
	} else {
		err = s.users.SetBookSpeed(r.Context(), currentUser(r).ID, b.ID, *body.Speed)
	}
	switch {
	case errors.Is(err, users.ErrInvalidSetting):
		apiBadField(w, "speed", apiSettingsBadSpeed)
	case err != nil:
		s.serverError(w, r, "api book speed", err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

type apiContinueItem struct {
	Book       apiBookCard `json:"book"`
	PositionMs int64       `json:"positionMs"`
	DurationMs int64       `json:"durationMs"`
	UpdatedAt  int64       `json:"updatedAt"`
}

// getAPIContinue lists the audiobooks the person has started and not finished, most recent first.
// The query filters by the person's grants in SQL; each row is checked for ListenBook once more.
func (s *server) getAPIContinue(w http.ResponseWriter, r *http.Request) {
	limit, ok := apiLimit(w, r, apiContinueDefault, apiContinueMax)
	if !ok {
		return
	}
	u := currentUser(r)
	list, err := s.lib.ContinueListening(r.Context(), s.scope(r), u.ID, limit)
	if err != nil {
		s.serverError(w, r, "api continue", err)
		return
	}
	items := make([]apiContinueItem, 0, len(list))
	for _, l := range list {
		if !s.can.Can(r.Context(), u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: l.Book.ID, LibraryID: l.Book.LibraryID}) {
			continue
		}
		items = append(items, apiContinueItem{Book: apiCardOf(l.Book), PositionMs: l.PositionMs, DurationMs: l.DurationMs, UpdatedAt: l.UpdatedAt})
	}
	w.Header().Set("Cache-Control", "no-store")
	api.Write(w, http.StatusOK, map[string]any{"items": items})
}
