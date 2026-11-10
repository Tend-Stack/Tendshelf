package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/radio"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// Radio (decision 0038, plan R1). Every route answers the plain 404 when radio is off (the owner's setting,
// or no service in Options), then needs share.UseRadio, so a guest is refused. A station is reached only by
// a person who saved or played it; anyone else gets the same 404 as an unknown id. The R2 pages come later:
// these are the actions, the relay and the app's API.

// radioLimits is the per-person limit on adding stations: 30 an hour (decision 0038, point 11).
type radioLimits struct{ add *auth.Limiter }

func (s *server) registerRadio(mux *http.ServeMux) {
	mux.HandleFunc("POST /radio/stations", s.postRadioStation)
	mux.HandleFunc("POST /radio/stations/{stationID}/remove", s.postRadioRemove)
	mux.HandleFunc("GET /radio/live/{stationID}", s.getRadioLive)
	mux.HandleFunc("GET /hx/radio/now/{stationID}", s.getRadioNow)
	// Without this the catch-all answers every other method with 404.
	mux.HandleFunc("/radio/live/{stationID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		s.plainError(w, r, http.StatusMethodNotAllowed, "Method not allowed.")
	})
}

// radioOn is true when a service is configured and the owner turned radio on.
func (s *server) radioOn(r *http.Request) bool {
	return s.radio != nil && s.radio.Enabled(r.Context())
}

// radioGate answers 404 unless radio is on and the signed-in person may use it.
func (s *server) radioGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.radioOn(r) {
		s.notFound(w, r)
		return false
	}
	return s.allow(w, r, share.UseRadio, podServer)
}

// loadStation loads a station the person saved or played.
func (s *server) loadStation(w http.ResponseWriter, r *http.Request) (radio.Station, bool) {
	if !s.radioGate(w, r) {
		return radio.Station{}, false
	}
	st, err := s.radio.Get(r.Context(), currentUser(r).ID, r.PathValue("stationID"))
	if errors.Is(err, radio.ErrNotFound) {
		s.notFound(w, r)
		return radio.Station{}, false
	}
	if err != nil {
		s.serverError(w, r, "load station", err)
		return radio.Station{}, false
	}
	return st, true
}

// addStation turns a picked OndaCast slug or a typed stream address into a saved station. It returns the
// HTTP status and plain words when it refuses; nothing the station or OndaCast said is repeated.
func (s *server) addStation(ctx context.Context, userID, slug, streamURL string) (st radio.Station, created bool, status int, text string) {
	in := radio.Input{}
	switch {
	case streamURL != "" && slug == "":
		in.StreamURL = streamURL
	case streamURL == "" && slug != "" && len(slug) <= 200:
		hit, err := s.radio.Net.Station(ctx, slug)
		if err != nil {
			var pe *podnet.Error
			if errors.As(err, &pe) && pe.Code == "not_found" {
				return st, false, http.StatusUnprocessableEntity, radioErrNoStation
			}
			return st, false, http.StatusBadGateway, radioErrSearchDown
		}
		in = radio.Input{StreamURL: hit.StreamURL, Name: hit.Name, ArtURL: hit.Logo, Genre: hit.Genre, Country: hit.Country, Slug: slug}
	default:
		return st, false, http.StatusUnprocessableEntity, radioErrBadAddress
	}
	probed, err := s.radio.Probe(ctx, in.StreamURL)
	if err != nil {
		var rf *radio.Refusal
		var pe *podnet.Error
		switch {
		case errors.Is(err, radio.ErrBadURL):
			return st, false, http.StatusUnprocessableEntity, radioErrBadAddress
		case errors.As(err, &rf):
			return st, false, http.StatusUnprocessableEntity, rf.Text
		case errors.As(err, &pe):
			return st, false, http.StatusUnprocessableEntity, pe.Text()
		}
		return st, false, http.StatusBadGateway, radioLiveUnavailable
	}
	// A playlist address is stored as the stream it names, so playing never needs the playlist again.
	in.StreamURL, in.Codec = probed.StreamURL, probed.Codec
	st, created, err = s.radio.Save(ctx, userID, in)
	switch {
	case errors.Is(err, radio.ErrTooMany):
		return st, false, http.StatusUnprocessableEntity, radioErrTooMany
	case errors.Is(err, radio.ErrBadURL):
		return st, false, http.StatusUnprocessableEntity, radioErrBadAddress
	case err != nil:
		s.radio.Log.Warn("radio: could not save a station", "err", err)
		return st, false, http.StatusInternalServerError, radioLiveUnavailable
	}
	return st, created, 0, ""
}

// radioAddAllowed spends one of the person's 30 additions an hour.
func (s *server) radioAddAllowed(w http.ResponseWriter, userID string) bool {
	if ok, wait := s.radioLim.add.Allow(userID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		return false
	}
	s.radioLim.add.Fail(userID)
	return true
}

func (s *server) postRadioStation(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) || !s.parseForm(w, r) {
		return
	}
	u := currentUser(r)
	if !s.radioAddAllowed(w, u.ID) {
		s.refuseSetting(w, r, http.StatusTooManyRequests, radioErrBusy)
		return
	}
	_, _, status, text := s.addStation(r.Context(), u.ID, strings.TrimSpace(r.PostForm.Get("slug")), strings.TrimSpace(r.PostForm.Get("stream_url")))
	if status != 0 {
		s.refuseSetting(w, r, status, text)
		return
	}
	s.answerPage(w, r, "/radio")
}

func (s *server) postRadioRemove(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) || !s.parseForm(w, r) {
		return
	}
	switch err := s.radio.Remove(r.Context(), currentUser(r).ID, r.PathValue("stationID")); {
	case errors.Is(err, radio.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "remove station", err)
	default:
		s.answerPage(w, r, "/radio")
	}
}

// getRadioLive relays a station's live audio, GET /radio/live/{stationID} and GET /api/v1/radio/live/{id}
// (decision 0038, point 5). Order: radio on, share.Can, the person's station, one live place, a shared stream
// slot, and only then the dial. Range is never passed on (a live stream has no position), in-stream titles are
// cut out and kept, and the answer is only ever a type from the allowlist, so a station cannot serve active
// content from this origin. Nothing the station sent is shown except audio and the cleaned title.
func (s *server) getRadioLive(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStation(w, r)
	if !ok {
		return
	}
	u := currentUser(r)
	h := w.Header()
	release, err := s.radio.Acquire(u.ID, st.ID)
	if err != nil {
		h.Set("Retry-After", "5")
		msg := radioLiveBusy
		if errors.Is(err, radio.ErrPersonBusy) {
			msg = radioLivePersonBusy
		}
		s.plainError(w, r, http.StatusServiceUnavailable, msg)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), s.radio.MaxSession)
	defer cancel()
	up, metaint, err := s.radio.Net.OpenLive(ctx, st.StreamURL)
	if err != nil {
		var pe *podnet.Error
		code := "offline"
		if errors.As(err, &pe) {
			code = pe.Code
		}
		_ = s.radio.SetChecked(r.Context(), st.ID, "", code)
		s.plainError(w, r, http.StatusBadGateway, radioLiveUnavailable)
		return
	}
	defer up.Body.Close()
	mt, codec, allowed := radio.StreamType(up.Header.Get("Content-Type"))
	if !allowed {
		_ = s.radio.SetChecked(r.Context(), st.ID, "", "bad_answer")
		s.plainError(w, r, http.StatusBadGateway, radioLiveUnavailable)
		return
	}
	_ = s.radio.SetChecked(r.Context(), st.ID, codec, "")
	if err := s.radio.NotePlay(r.Context(), u.ID, st.ID); err != nil {
		s.radio.Log.Warn("radio: could not record a play", "err", err)
	}
	h.Set("Content-Type", mt)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	var body io.Reader = up.Body
	if metaint > 0 {
		body = &radio.Splitter{R: up.Body, Metaint: metaint, OnTitle: func(t string) { s.radio.SetTitle(st.ID, t) }}
	}
	rc := http.NewResponseController(w)
	buf := make([]byte, 16<<10)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if rerr != nil {
			return
		}
	}
}

// getRadioNow answers the now-playing line, polled every 15 seconds while a station plays.
func (s *server) getRadioNow(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStation(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	s.fragment(w, r, http.StatusOK, components.RadioNow(st.ID, s.radio.NowPlaying(st.ID)))
}
