package podcast

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
)

// Job kinds (decision 0038).
const (
	KindRefresh = "podcast.refresh"
	KindArt     = "podcast.art"
	KindCache   = "podcast.cache"
	KindImport  = "podcast.import"
	KindPrune   = "podcast.prune"
)

// Stream limits: upstream audio streams open at once, server wide and per person.
const (
	MaxStreams          = 16
	MaxStreamsPerPerson = 3
	refreshEvery        = 5 * time.Minute // a show can be refreshed by hand this often
	staleAfter          = time.Hour
)

// ErrTooSoon means the show was refreshed within the last five minutes.
var ErrTooSoon = errors.New("podcast: refreshed too recently")

// Service is the podcast feature: the store, the guarded client, the job kinds, the cache and the
// scheduler. Off (settings) means no job fetches and no scheduler tick does anything.
type Service struct {
	*Store
	Net     *podnet.Client
	Runner  *jobs.Runner
	DataDir string
	Log     *slog.Logger
	// Thumb makes a thumbnail of a stored picture (media.Tools.Thumbnail); nil serves the original.
	Thumb func(ctx context.Context, src, dst string, box int) error

	dbs *db.Store

	mu      sync.Mutex
	streams int
	perUser map[string]int
}

// New returns a Service. Register adds its job kinds to the runner.
func New(st *db.Store, net *podnet.Client, runner *jobs.Runner, dataDir string, now func() time.Time, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Store: NewStore(st, now), Net: net, Runner: runner, DataDir: dataDir, Log: log, dbs: st, perUser: map[string]int{}}
}

// Settings reads the owner's settings.
func (s *Service) Settings(ctx context.Context) Settings { return LoadSettings(ctx, s.dbs) }

// Enabled reports whether podcasts are turned on. Everything that could fetch checks it first.
func (s *Service) Enabled(ctx context.Context) bool { return s.Settings(ctx).Enabled }

// CacheDir is where server-kept episodes live: <data>/cache/podcasts.
func (s *Service) CacheDir() string { return filepath.Join(s.DataDir, "cache", "podcasts") }

// ArtDir is where artwork lives: <data>/cache/podcast-art.
func (s *Service) ArtDir() string { return filepath.Join(s.DataDir, "cache", "podcast-art") }

// AcquireStream takes one upstream-stream slot for the person. ok is false when the server or the
// person is at the limit; otherwise release must be called once the stream ends.
func (s *Service) AcquireStream(userID string) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams >= MaxStreams || s.perUser[userID] >= MaxStreamsPerPerson {
		return nil, false
	}
	s.streams++
	s.perUser[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.streams--
			if s.perUser[userID]--; s.perUser[userID] <= 0 {
				delete(s.perUser, userID)
			}
		})
	}, true
}

// enqueueOnce queues a job unless one for the same payload field is already queued or running; it
// returns the id of whichever it is.
func (s *Service) enqueueOnce(ctx context.Context, kind, field, value string, payload any, by string) (string, error) {
	if s.Runner == nil {
		return "", errors.New("podcast: no job runner")
	}
	if j, ok, err := s.Runner.ActiveFor(ctx, kind, field, value); err == nil && ok {
		return j.ID, nil
	}
	return s.Runner.Enqueue(ctx, kind, payload, jobs.EnqueueOptions{CreatedBy: by})
}

type showPayload struct {
	ShowID string `json:"show_id,omitempty"`
}

// QueueRefresh queues a refresh of one show (once at a time per show).
func (s *Service) QueueRefresh(ctx context.Context, showID, by string) (string, error) {
	return s.enqueueOnce(ctx, KindRefresh, "show_id", showID, showPayload{showID}, by)
}

// QueueCache queues keeping one episode on the server.
func (s *Service) QueueCache(ctx context.Context, episodeID string) (string, error) {
	return s.enqueueOnce(ctx, KindCache, "episode_id", episodeID, map[string]string{"episode_id": episodeID}, "")
}

// RefreshNow is the Refresh button: the person must follow the show, and a show refreshed within five
// minutes answers ErrTooSoon.
func (s *Service) RefreshNow(ctx context.Context, userID, showID string) (string, error) {
	sh, err := s.Show(ctx, userID, showID)
	if err != nil {
		return "", err
	}
	if sh.FetchedAt > 0 && s.nowMs()-sh.FetchedAt < refreshEvery.Milliseconds() && sh.FailCode == "" {
		return "", ErrTooSoon
	}
	return s.QueueRefresh(ctx, showID, userID)
}

// QueueImport queues the import of a list of feed addresses (an OPML file) for one person and
// records it in the audit log. The job follows each address and queues a refresh for new shows.
func (s *Service) QueueImport(ctx context.Context, userID string, urls []string) (string, error) {
	id, err := s.Runner.Enqueue(ctx, KindImport, importPayload{UserID: userID, URLs: urls}, jobs.EnqueueOptions{CreatedBy: userID})
	if err != nil {
		return "", err
	}
	_ = db.Audit(ctx, s.dbs.Writer(), db.AuditEntry{At: s.nowMs(), UserID: userID, Action: "podcast.import", Target: "job:" + id,
		Detail: map[string]any{"shows": len(urls)}})
	return id, nil
}

// RefreshStale queues a refresh for each of the person's shows last fetched over an hour ago (opening
// Podcasts). It does nothing while podcasts are off.
func (s *Service) RefreshStale(ctx context.Context, userID string) int {
	if !s.Enabled(ctx) {
		return 0
	}
	ids, err := s.StaleShows(ctx, userID, staleAfter)
	if err != nil {
		s.Log.Warn("podcasts: stale shows", "err", err)
		return 0
	}
	n := 0
	for _, id := range ids {
		if _, err := s.QueueRefresh(ctx, id, userID); err == nil {
			n++
		}
	}
	return n
}
