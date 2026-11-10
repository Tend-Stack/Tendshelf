package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/radio"
	"github.com/Tend-Stack/tendshelf/internal/radio/hls"
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
	mux.HandleFunc("POST /radio/stations/{stationID}/save", s.postRadioSave)
	mux.HandleFunc("POST /radio/play", s.postRadioPlay)
	mux.HandleFunc("GET /radio/live/{stationID}", s.getRadioLive)
	mux.HandleFunc("GET /radio/art/{stationID}", s.getRadioArt)
	mux.HandleFunc("GET /hx/radio/now/{stationID}", s.getRadioNow)
	s.registerRadioPages(mux) // the R2 pages: /radio, /radio/search and the player fragment
	// Without this the catch-all answers every other method with 404.
	getOnly := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		s.plainError(w, r, http.StatusMethodNotAllowed, "Method not allowed.")
	}
	mux.HandleFunc("/radio/live/{stationID}", getOnly)
	mux.HandleFunc("/radio/art/{stationID}", getOnly)
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
	return s.resolveStation(ctx, userID, slug, streamURL, true)
}

// resolveStation is addStation with a choice: favourite false stores the station as played, not saved (Play on a
// search result, POST /radio/play), so it skips the favourites limit and answers at once for a station the
// person already reaches.
func (s *server) resolveStation(ctx context.Context, userID, slug, streamURL string, favourite bool) (st radio.Station, created bool, status int, text string) {
	// A station already on the list answers at once; a full list is refused before anything is dialled, so it
	// cannot be used to make probes.
	if favourite {
		if kept, ok := s.radio.Kept(ctx, userID, slug, streamURL); ok {
			s.radio.QueueArt(ctx, kept)
			return kept, false, 0, ""
		}
		if n, err := s.radio.FavouriteCount(ctx, userID); err == nil && n >= radio.MaxFavourites {
			return st, false, http.StatusUnprocessableEntity, radioErrTooMany
		}
	} else if known, ok := s.radio.Known(ctx, userID, slug); ok {
		return known, false, 0, ""
	}
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
	in.StreamURL, in.Codec, in.Format = probed.StreamURL, probed.Codec, probed.Format
	if favourite {
		st, created, err = s.radio.Save(ctx, userID, in)
	} else {
		st, err = s.radio.Remember(ctx, userID, in)
	}
	switch {
	case errors.Is(err, radio.ErrTooMany):
		return st, false, http.StatusUnprocessableEntity, radioErrTooMany
	case errors.Is(err, radio.ErrBadURL):
		return st, false, http.StatusUnprocessableEntity, radioErrBadAddress
	case err != nil:
		s.radio.Log.Warn("radio: could not save a station", "err", err)
		return st, false, http.StatusInternalServerError, radioLiveUnavailable
	}
	s.radio.QueueArt(ctx, st) // best effort: the logo comes later and never fails the add
	return st, created, 0, ""
}

// radioAddAllowed spends one of the person's 30 additions an hour.
func (s *server) radioAddAllowed(w http.ResponseWriter, userID string) bool {
	// Take checks and counts under one lock, so parallel additions cannot all pass before one is counted.
	if !s.radioLim.add.Take(userID) {
		_, wait := s.radioLim.add.Allow(userID)
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		return false
	}
	return true
}

// relayWriteDeadline is how long one write to the listener may take. The server has no WriteTimeout (long
// streams), so a client that stops reading would otherwise hold its stream slot and live place forever.
// radioEnabledEvery is how often a running relay looks at the owner's radio switch. Tests shorten both.
var (
	relayWriteDeadline = 30 * time.Second
	radioEnabledEvery  = 3 * time.Second
)

// relayCopy copies src to the client, giving each write its own deadline, flushing when asked, and stops at the
// first error from either side. A recorder without deadline support is written to as before.
func relayCopy(w http.ResponseWriter, src io.Reader, flush bool) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 16<<10)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if err := rc.SetWriteDeadline(time.Now().Add(relayWriteDeadline)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return err
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
			if flush {
				_ = rc.Flush()
			}
		}
		if rerr != nil {
			return rerr
		}
	}
}

// stopWhenRadioOff cancels a running relay within radioEnabledEvery of the owner turning radio off.
func (s *server) stopWhenRadioOff(ctx context.Context, cancel context.CancelFunc, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.radio.Enabled(ctx) {
				cancel()
				return
			}
		}
	}
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

// postRadioPlay is Play on a station that is not saved yet (a search result or a suggestion), POST /radio/play with
// an OndaCast slug. The station is resolved through the same OndaCast call and stream check as Save, kept as
// played and not as a favourite, and the answer is what the Play link of a saved card is: a redirect to the live
// stream (the card works without JavaScript). The player script asks for JSON and gets the station id, which
// it hands to the player like any saved card. Only a slug is taken, never an address.
func (s *server) postRadioPlay(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) || !s.parseForm(w, r) {
		return
	}
	u := currentUser(r)
	slug := strings.TrimSpace(r.PostForm.Get("slug"))
	if slug == "" || len(slug) > 200 {
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, radioErrBadAddress)
		return
	}
	st, ok := s.radio.Known(r.Context(), u.ID, slug)
	if !ok {
		// Only a station not reached before costs OndaCast a call and the station a probe, so only it spends an addition.
		if !s.radioAddAllowed(w, u.ID) {
			s.refuseSetting(w, r, http.StatusTooManyRequests, radioErrBusy)
			return
		}
		var status int
		var text string
		if st, _, status, text = s.resolveStation(r.Context(), u.ID, slug, "", false); status != 0 {
			s.refuseSetting(w, r, status, text)
			return
		}
	}
	s.radio.QueueArt(r.Context(), st)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": st.ID})
		return
	}
	http.Redirect(w, r, "/radio/live/"+url.PathEscape(st.ID), http.StatusSeeOther)
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
	go s.stopWhenRadioOff(ctx, cancel, radioEnabledEvery) // the interval is read here, in the handler, not in the goroutine
	if st.Format == "hls" {
		s.relayHLS(ctx, w, r, st)
		return
	}
	up, metaint, err := s.radio.Net.OpenLive(ctx, st.StreamURL)
	if err != nil {
		s.radioCheckFailed(r.Context(), st.ID, err)
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
	if r.Method != http.MethodHead { // a HEAD asks about the stream; it is not listening
		if err := s.radio.NotePlay(r.Context(), u.ID, st.ID); err != nil {
			s.radio.Log.Warn("radio: could not record a play", "err", err)
		}
	}
	setLiveHeaders(h, mt)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	var body io.Reader = up.Body
	if metaint > 0 {
		body = &radio.Splitter{R: up.Body, Metaint: metaint, OnTitle: func(t string) { s.radio.SetTitle(st.ID, t) }}
	}
	_ = relayCopy(w, body, true)
}

// setLiveHeaders sets the headers every relayed live answer carries: an allowlisted audio type, no sniffing,
// a sandbox CSP so nothing from the station can run on this origin, and no caching or ranges.
func setLiveHeaders(h http.Header, mt string) {
	h.Set("Content-Type", mt)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Accept-Ranges", "none")
}

// radioCheckFailed stores why the last look at a station's stream failed: the guarded client's code, else "offline".
func (s *server) radioCheckFailed(ctx context.Context, id string, err error) {
	var pe *podnet.Error
	code := "offline"
	switch {
	case errors.As(err, &pe):
		code = pe.Code
	case errors.Is(err, hls.ErrLocked), errors.Is(err, hls.ErrVideo), errors.Is(err, hls.ErrCodec), errors.Is(err, hls.ErrNotPlaylist),
		errors.Is(err, hls.ErrUnsupported), errors.Is(err, hls.ErrTooLarge), errors.Is(err, hls.ErrChanged):
		code = "bad_answer" // the station answered, but not with a stream Tendshelf plays
	}
	_ = s.radio.SetChecked(ctx, id, "", code)
}

// relayHLS serves an HLS station as one continuous ADTS or MPEG audio stream (decision 0039). ctx already
// carries the 12 hour session limit and the radio-off stop, so cancelling it ends the session and every
// request it has in flight. The caller holds the live place and the stream slot.
func (s *server) relayHLS(ctx context.Context, w http.ResponseWriter, r *http.Request, st radio.Station) {
	u := currentUser(r)
	sess, err := hls.Open(ctx, s.radio.Net, st.StreamURL, hls.Options{OnTitle: func(t string) { s.radio.SetTitle(st.ID, t) }})
	if err != nil {
		s.radioCheckFailed(r.Context(), st.ID, err)
		s.plainError(w, r, http.StatusBadGateway, radioLiveUnavailable)
		return
	}
	defer sess.Close()
	f := sess.Format()
	_ = s.radio.SetChecked(r.Context(), st.ID, f.Codec, "")
	if r.Method != http.MethodHead {
		if err := s.radio.NotePlay(r.Context(), u.ID, st.ID); err != nil {
			s.radio.Log.Warn("radio: could not record a play", "err", err)
		}
	}
	setLiveHeaders(w.Header(), f.ContentType())
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = relayCopy(w, sess, true)
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

// postRadioSave puts a station the person has played (a Recent row) back among their saved ones by its id. The
// station row exists already, so nothing is dialled; the 100 limit and the 30 an hour apply as for any addition.
func (s *server) postRadioSave(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) || !s.parseForm(w, r) {
		return
	}
	u, id := currentUser(r), r.PathValue("stationID")
	// An unknown or foreign id is the plain 404 before an attempt is spent.
	if _, err := s.radio.Get(r.Context(), u.ID, id); errors.Is(err, radio.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, "save station", err)
		return
	}
	if !s.radioAddAllowed(w, u.ID) {
		s.refuseSetting(w, r, http.StatusTooManyRequests, radioErrBusy)
		return
	}
	switch _, _, err := s.radio.Resave(r.Context(), u.ID, id); {
	case errors.Is(err, radio.ErrTooMany):
		s.refuseSetting(w, r, http.StatusUnprocessableEntity, radioErrTooMany)
	case errors.Is(err, radio.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "save station", err)
	default:
		s.answerPage(w, r, "/radio")
	}
}

// getRadioArt serves a station's logo from the shared art cache, to a person who saved or played the station.
// The picture was fetched through the guarded client (JPEG, PNG or WebP by its bytes, 5 MiB) and is kept under
// a hash name; a station without one yet queues the fetch and answers 404 (the page shows the plain tile).
func (s *server) getRadioArt(w http.ResponseWriter, r *http.Request) {
	st, ok := s.loadStation(w, r)
	if !ok {
		return
	}
	width, ok := s.artWidth(w, r)
	if !ok {
		return
	}
	if st.ArtSHA() == "" {
		s.radio.QueueArt(r.Context(), st)
		s.plainError(w, r, http.StatusNotFound, "Not found.")
		return
	}
	s.serveArt(w, r, width, st.ArtSHA(), st.ArtPath)
}
