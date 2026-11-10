package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// The JSON API for the app's radio (plan R1). Search stays a web page in the app's WebView (R2). Every route is
// behind the same off switch and share.UseRadio as the pages. No stream address, site address or station text
// other than name, genre and country is ever put in an answer.

func (s *server) registerAPIRadio(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/radio/stations", s.getAPIRadioStations)
	mux.HandleFunc("GET /api/v1/radio/recent", s.getAPIRadioRecent)
	mux.HandleFunc("POST /api/v1/radio/stations", s.postAPIRadioStation)
	mux.HandleFunc("DELETE /api/v1/radio/stations/{stationID}", s.deleteAPIRadioStation)
	mux.HandleFunc("GET /api/v1/radio/live/{stationID}", s.getRadioLive)
	mux.HandleFunc("GET /api/v1/radio/art/{stationID}", s.getRadioArt)
	mux.HandleFunc("GET /api/v1/radio/now/{stationID}", s.getAPIRadioNow)
}

type apiRadioStation struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Codec        string `json:"codec,omitempty"`
	Genre        string `json:"genre,omitempty"`
	Country      string `json:"country,omitempty"`
	Favourite    bool   `json:"favourite"`
	AddedAt      int64  `json:"addedAt,omitempty"`
	LastPlayedAt int64  `json:"lastPlayedAt,omitempty"`
	LiveURL      string `json:"liveUrl"`
	ArtURL       string `json:"artUrl,omitempty"`
	Problem      string `json:"problem,omitempty"`
}

func apiRadioOf(st radio.Station) apiRadioStation {
	v := apiRadioStation{ID: st.ID, Name: st.Name, Codec: st.Codec, Genre: st.Genre, Country: st.Country,
		Favourite: st.Favourite, AddedAt: st.AddedAt, LastPlayedAt: st.LastPlayed, LiveURL: "/api/v1/radio/live/" + st.ID}
	// Present when the logo is kept or can be fetched (the station has a logo address). The first request for a
	// logo not kept yet answers 404 and queues the fetch; the logo's own address is never in an answer.
	if sha := st.ArtSHA(); sha != "" {
		v.ArtURL = "/api/v1/radio/art/" + url.PathEscape(st.ID) + "?v=" + sha[:8]
	} else if st.ArtURL != "" {
		v.ArtURL = "/api/v1/radio/art/" + url.PathEscape(st.ID)
	}
	if st.FailCode != "" {
		v.Problem = radioLiveUnavailable
	}
	return v
}

func (s *server) writeAPIRadioList(w http.ResponseWriter, r *http.Request, list []radio.Station, err error) {
	if err != nil {
		s.serverError(w, r, "api radio", err)
		return
	}
	out := make([]apiRadioStation, 0, len(list))
	for _, st := range list {
		out = append(out, apiRadioOf(st))
	}
	api.Write(w, http.StatusOK, newFeed(out, "", s.now().UnixMilli()))
}

func (s *server) getAPIRadioStations(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	list, err := s.radio.Favourites(r.Context(), currentUser(r).ID)
	s.writeAPIRadioList(w, r, list, err)
}

func (s *server) getAPIRadioRecent(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	list, err := s.radio.Recent(r.Context(), currentUser(r).ID, radio.MaxRecent)
	s.writeAPIRadioList(w, r, list, err)
}

type apiRadioAdd struct {
	Slug      string `json:"slug"`
	StreamURL string `json:"streamUrl"`
}

func (s *server) postAPIRadioStation(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	var in apiRadioAdd
	if !apiJSONBody(w, r, apiBookmarkBody, &in) {
		return
	}
	u := currentUser(r)
	if !s.radioAddAllowed(w, u.ID) {
		api.Error(w, http.StatusTooManyRequests, api.CodeRateLimited, radioErrBusy)
		return
	}
	st, created, status, text := s.addStation(r.Context(), u.ID, strings.TrimSpace(in.Slug), strings.TrimSpace(in.StreamURL))
	if status != 0 {
		code := api.CodeInvalid
		switch status {
		case http.StatusBadGateway:
			code = api.CodeUnavailable
		case http.StatusInternalServerError:
			code = api.CodeUnavailable
		}
		api.Error(w, status, code, text)
		return
	}
	status = http.StatusOK
	if created {
		status = http.StatusCreated
	}
	api.Write(w, status, map[string]any{"station": apiRadioOf(st)})
}

func (s *server) deleteAPIRadioStation(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	switch err := s.radio.Remove(r.Context(), currentUser(r).ID, r.PathValue("stationID")); {
	case errors.Is(err, radio.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "api remove station", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) getAPIRadioNow(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStation(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	api.Write(w, http.StatusOK, map[string]any{"title": s.radio.NowPlaying(st.ID)})
}
