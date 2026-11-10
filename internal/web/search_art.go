package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
)

// Search artwork (operator decision, 2026-11-10): the pictures of search results come from the publishers' sites,
// which the page's CSP never loads. The server fetches each one through the guarded podnet client (address checked
// after DNS, no cookies, fixed User-Agent, 5 MiB cap, type decided by the bytes) and serves it from its own address.
//
// It is not a proxy: the address of a picture is never in the request. When a search answers, each hit's picture
// address is stored under a random key that belongs to the signed-in person and expires; the route takes only that
// key. An unknown, expired or someone else's key is a 404, like every other refusal.

const (
	searchArtKeys      = 400              // keys held at once (a search page lists at most 20)
	searchArtTTL       = 20 * time.Minute // a key and its cached picture live this long
	searchArtCacheMax  = 40               // pictures kept in memory so paging back does not fetch again
	searchArtCacheSize = 32 << 20         // and at most this many bytes of them
	searchArtSlots     = 4                // pictures fetched at the same time, over all people
)

type searchArtKey struct {
	user, kind, url string
	at              time.Time
}

type searchArtPic struct {
	body []byte
	mime string
}

// searchArtStore holds the keys and the small picture cache. Both are in memory: nothing is written to disk and a
// restart simply asks the search again.
type searchArtStore struct {
	mu     sync.Mutex
	keys   map[string]searchArtKey
	order  []string // keys, oldest first
	pics   map[string]searchArtPic
	porder []string
	bytes  int
	slots  chan struct{}
	now    func() time.Time
}

func newSearchArtStore(now func() time.Time) *searchArtStore {
	if now == nil {
		now = time.Now
	}
	return &searchArtStore{keys: map[string]searchArtKey{}, pics: map[string]searchArtPic{}, slots: make(chan struct{}, searchArtSlots), now: now}
}

// add records a picture address for a person and returns the key to put in the page, "" when there is no usable address.
func (a *searchArtStore) add(user, kind, rawURL string) string {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") || len(rawURL) > 2048 {
		return ""
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	key := hex.EncodeToString(raw[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[key] = searchArtKey{user: user, kind: kind, url: rawURL, at: a.now()}
	a.order = append(a.order, key)
	for len(a.order) > searchArtKeys {
		a.dropLocked(a.order[0])
	}
	return key
}

func (a *searchArtStore) dropLocked(key string) {
	delete(a.keys, key)
	if p, ok := a.pics[key]; ok {
		a.bytes -= len(p.body)
		delete(a.pics, key)
	}
	for i, k := range a.order {
		if k == key {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
	for i, k := range a.porder {
		if k == key {
			a.porder = append(a.porder[:i], a.porder[i+1:]...)
			break
		}
	}
}

// lookup returns the key's entry when it is the person's, of this kind and not expired.
func (a *searchArtStore) lookup(user, kind, key string) (searchArtKey, searchArtPic, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.keys[key]
	if !ok || e.user != user || e.kind != kind {
		return searchArtKey{}, searchArtPic{}, false
	}
	if a.now().Sub(e.at) > searchArtTTL {
		a.dropLocked(key)
		return searchArtKey{}, searchArtPic{}, false
	}
	return e, a.pics[key], true
}

func (a *searchArtStore) keep(key string, p searchArtPic) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.keys[key]; !ok || len(p.body) > searchArtCacheSize/2 {
		return
	}
	if _, ok := a.pics[key]; ok {
		return
	}
	a.pics[key] = p
	a.porder = append(a.porder, key)
	a.bytes += len(p.body)
	for len(a.porder) > searchArtCacheMax || a.bytes > searchArtCacheSize {
		old := a.porder[0]
		a.porder = a.porder[1:]
		a.bytes -= len(a.pics[old].body)
		delete(a.pics, old)
	}
}

// acquire takes one of the fetch slots, waiting for the request's own deadline.
func (a *searchArtStore) acquire(ctx context.Context) bool {
	select {
	case a.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *searchArtStore) release() { <-a.slots }

// artKeyFor is the same-origin picture address of a search hit, "" when it has no picture.
func (s *server) artKeyFor(r *http.Request, kind, imageURL string) string {
	key := s.searchArt.add(currentUser(r).ID, kind, imageURL)
	if key == "" {
		return ""
	}
	return "/" + kind + "/search/art/" + key
}

const (
	searchArtPodcasts = "podcasts"
	searchArtRadio    = "radio"
)

// getPodcastSearchArt and getRadioSearchArt answer GET /podcasts/search/art/{key} and /radio/search/art/{key}.
func (s *server) getPodcastSearchArt(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	s.serveSearchArt(w, r, searchArtPodcasts, s.podcasts.Net)
}

func (s *server) getRadioSearchArt(w http.ResponseWriter, r *http.Request) {
	if !s.radioGate(w, r) {
		return
	}
	s.serveSearchArt(w, r, searchArtRadio, s.radio.Net)
}

func (s *server) serveSearchArt(w http.ResponseWriter, r *http.Request, kind string, net *podnet.Client) {
	e, pic, ok := s.searchArt.lookup(currentUser(r).ID, kind, r.PathValue("key"))
	if !ok {
		s.notFound(w, r)
		return
	}
	if pic.body == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if !s.searchArt.acquire(ctx) {
			s.notFound(w, r)
			return
		}
		body, mime, err := net.Picture(ctx, e.url)
		s.searchArt.release()
		if err != nil {
			slog.Debug("search picture not loaded", "err", err) // the page keeps its plain tile
			s.notFound(w, r)
			return
		}
		pic = searchArtPic{body: body, mime: mime}
		s.searchArt.keep(r.PathValue("key"), pic)
	}
	h := w.Header()
	h.Set("Content-Type", pic.mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=900")
	h.Set("Content-Length", strconv.Itoa(len(pic.body)))
	_, _ = w.Write(pic.body)
}
