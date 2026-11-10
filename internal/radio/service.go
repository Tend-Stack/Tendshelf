package radio

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// Limits and timeouts (plan R1).
const (
	ProbeTimeout   = 10 * time.Second
	MaxSession     = 12 * time.Hour // one listening connection is closed after this; the player reconnects
	maxPlaylist    = 64 << 10       // a playlist file is read up to this size
	maxPlaylistHop = 2              // a playlist naming a playlist naming a playlist is refused
	maxTitles      = 1024           // remembered now-playing titles
)

// Refusal is a stream address Tendshelf will not take, in plain words.
type Refusal struct{ Text string }

func (r *Refusal) Error() string { return r.Text }

var (
	// ErrHLS is an HLS (.m3u8) stream. It needs a segment proxy, which Tendshelf does not have yet.
	ErrHLS = &Refusal{"Tendshelf can't play this kind of stream yet."}
	// ErrNotAudio is an address that answers something other than a stream Tendshelf plays.
	ErrNotAudio = &Refusal{"That address is not a radio stream Tendshelf can play."}
	// ErrNoEntry is a playlist file with no web address in it.
	ErrNoEntry = &Refusal{"That playlist has no stream address Tendshelf can use."}
)

// streamTypes are the Content-Types a station may be relayed as (also the only ones the relay answers).
var streamTypes = map[string]string{
	"audio/mpeg": "mp3", "audio/aac": "aac", "audio/aacp": "aac", "audio/ogg": "ogg", "audio/opus": "opus", "audio/flac": "flac",
}

// StreamType normalizes a Content-Type header to an allowed type and its codec name. ok is false for
// anything else (HTML, SVG, video, a playlist), which the relay then never passes on.
func StreamType(contentType string) (mt, codec string, ok bool) {
	m, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", "", false
	}
	m = strings.ToLower(m)
	codec, ok = streamTypes[m]
	return m, codec, ok
}

// Service is the radio feature: the store, the guarded client, the shared stream slots and the in-memory
// now-playing titles. Off (podcast settings, radio.enabled) means no route answers and nothing is fetched.
type Service struct {
	*Store
	Net *podnet.Client
	Log *slog.Logger
	// MaxSession is how long one relay connection lasts; tests shorten it.
	MaxSession time.Duration

	pod *podcast.Service
	dbs *db.Store

	mu     sync.Mutex
	live   map[string]string    // person -> station they are listening to
	count  map[string]int       // station -> listeners
	titles map[string]string    // station -> latest cleaned title
	order  []string             // titles in insertion order, for the cap
	artTry map[string]time.Time // station -> last logo fetch queued, so a dead logo is not retried on every view
}

// New returns a Service. pod provides the server-wide stream slots shared with podcast audio and the settings.
func New(st *db.Store, net *podnet.Client, pod *podcast.Service, now func() time.Time, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Store: NewStore(st, now), Net: net, Log: log, MaxSession: MaxSession, pod: pod, dbs: st,
		live: map[string]string{}, count: map[string]int{}, titles: map[string]string{}, artTry: map[string]time.Time{}}
}

// Enabled reports whether the owner turned radio on. Everything that could fetch checks it first.
func (s *Service) Enabled(ctx context.Context) bool {
	return podcast.LoadSettings(ctx, s.dbs).RadioEnabled
}

// artRetry is how long a station's logo is left alone after a fetch was queued for it.
const artRetry = time.Hour

// QueueArt asks for the station's logo, best effort: nothing happens when it has none to fetch, already has
// the file, or a fetch was queued within the last hour (a dead logo is not retried on every view). The
// fetch is the podcast art job, so it goes through the guarded Picture path and stores nothing unchecked.
func (s *Service) QueueArt(ctx context.Context, st Station) {
	if s.pod == nil || st.ArtURL == "" || st.ArtPath != "" {
		return
	}
	s.mu.Lock()
	if t, ok := s.artTry[st.ID]; ok && s.Store.now().Sub(t) < artRetry {
		s.mu.Unlock()
		return
	}
	if len(s.artTry) >= maxTitles {
		s.artTry = map[string]time.Time{}
	}
	s.artTry[st.ID] = s.Store.now()
	s.mu.Unlock()
	if _, err := s.pod.QueueStationArt(ctx, st.ID); err != nil {
		s.Log.Warn("radio: could not queue a station logo", "err", err)
	}
}

// Busy reasons for Acquire.
var (
	// ErrPersonBusy means the person already has a live station open.
	ErrPersonBusy = errors.New("radio: one live station per person")
	// ErrServerBusy means all 16 upstream stream slots are in use.
	ErrServerBusy = errors.New("radio: no free stream slot")
)

// Acquire takes the person's one live place and one of the server's 16 stream slots (shared with podcast
// audio). release must be called when the stream ends.
func (s *Service) Acquire(userID, stationID string) (release func(), err error) {
	s.mu.Lock()
	if _, on := s.live[userID]; on {
		s.mu.Unlock()
		return nil, ErrPersonBusy
	}
	s.live[userID] = stationID
	s.mu.Unlock()
	slot, ok := func() (func(), bool) {
		if s.pod == nil {
			return func() {}, true
		}
		return s.pod.AcquireGlobalStream()
	}()
	if !ok {
		s.mu.Lock()
		delete(s.live, userID)
		s.mu.Unlock()
		return nil, ErrServerBusy
	}
	s.mu.Lock()
	s.count[stationID]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			slot()
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.live, userID)
			if s.count[stationID]--; s.count[stationID] <= 0 {
				delete(s.count, stationID)
				delete(s.titles, stationID) // nobody is listening: the old title would be stale
				for i, id := range s.order {
					if id == stationID { // an entry with no title left must not evict a live one later
						s.order = append(s.order[:i], s.order[i+1:]...)
						break
					}
				}
			}
		})
	}, nil
}

// SetTitle keeps the latest title the station sent. The cap keeps a hostile set of stations from growing it.
func (s *Service) SetTitle(stationID, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.titles[stationID]; !ok {
		if len(s.order) >= maxTitles {
			delete(s.titles, s.order[0])
			s.order = s.order[1:]
		}
		s.order = append(s.order, stationID)
	}
	s.titles[stationID] = CleanTitle(title)
}

// NowPlaying is the latest title of a station someone is listening to, or "".
func (s *Service) NowPlaying(stationID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.titles[stationID]
}

// Probed is what one look at a stream address found.
type Probed struct {
	StreamURL string // the stream itself (a playlist's first entry, not the playlist)
	Codec     string
}

// Probe checks a stream address with one OpenLive request (10 s): it must answer an allowed audio
// Content-Type. A .pls or .m3u playlist is read for its first http(s) entry, which is probed the same way,
// so every address goes through the guarded client. HLS is refused.
func (s *Service) Probe(ctx context.Context, raw string) (Probed, error) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	for hop := 0; ; hop++ {
		norm, err := podcast.NormalizeFeedURL(raw)
		if err != nil {
			return Probed{}, ErrBadURL
		}
		if isHLSPath(norm) {
			return Probed{}, ErrHLS
		}
		up, _, err := s.Net.OpenLive(ctx, norm)
		if err != nil {
			return Probed{}, err
		}
		mt, _, _ := mime.ParseMediaType(up.Header.Get("Content-Type"))
		mt = strings.ToLower(mt)
		if _, codec, ok := StreamType(mt); ok {
			up.Body.Close()
			return Probed{StreamURL: norm, Codec: codec}, nil
		}
		if hlsType(mt) {
			up.Body.Close()
			return Probed{}, ErrHLS
		}
		if !playlistLike(mt, norm) {
			up.Body.Close()
			return Probed{}, ErrNotAudio
		}
		body, err := io.ReadAll(io.LimitReader(up.Body, maxPlaylist))
		up.Body.Close()
		if err != nil {
			return Probed{}, &podnet.Error{Code: "offline"}
		}
		next, hls := FirstEntry(string(body))
		switch {
		case hls:
			return Probed{}, ErrHLS
		case next == "":
			return Probed{}, ErrNoEntry
		case hop+1 > maxPlaylistHop:
			return Probed{}, ErrNoEntry
		}
		raw = next
	}
}

func isHLSPath(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(path.Ext(u.Path), ".m3u8")
}

func hlsType(mt string) bool {
	return mt == "application/vnd.apple.mpegurl" || mt == "application/x-mpegurl"
}

// playlistLike says a response (or its address) is a .pls or .m3u playlist file.
func playlistLike(mt, raw string) bool {
	switch mt {
	case "audio/x-scpls", "application/pls+xml", "audio/x-mpegurl", "audio/mpegurl":
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	ext := strings.ToLower(path.Ext(u.Path))
	return (ext == ".pls" || ext == ".m3u") && (mt == "" || strings.HasPrefix(mt, "text/") || mt == "application/octet-stream")
}

// FirstEntry returns the first http(s) address of a .pls or .m3u playlist. hls is true when the text is an
// HLS playlist (it carries #EXT-X- tags), which Tendshelf does not play. Relative entries are not followed:
// only absolute http(s) addresses count, and each is re-checked by the guarded client when probed.
func FirstEntry(text string) (entry string, hls bool) {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 4096), maxPlaylist)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if strings.HasPrefix(line, "#EXT-X-") {
			return "", true
		}
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		cand := line
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(strings.ToLower(k), "file") {
			cand = strings.TrimSpace(v)
		}
		l := strings.ToLower(cand)
		if entry == "" && (strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")) {
			entry = cand
		}
	}
	return entry, false
}
