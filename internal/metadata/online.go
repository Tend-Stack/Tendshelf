package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// KindOnline is the job that looks one book up on the online sites. It starts only from a click.
const KindOnline = "metadata.online"

// Errors of the lookup service.
var (
	ErrOnlineOff = errors.New("metadata: online lookups are turned off")
	ErrNotFound  = library.ErrNotFound
)

const (
	onlineDescriptionLimit = 2000
	coverPreviews          = 3 // candidates whose cover picture the job fetches for the compare sheet
	onlineCacheAge         = 7 * 24 * time.Hour
)

// LookupPayload is the payload of a metadata.online job.
type LookupPayload struct {
	BookID    string       `json:"book_id"`
	Query     online.Query `json:"query"`
	Providers []string     `json:"providers"`
}

// Candidate is one online match, cleaned for the compare sheet. AuthorASINs maps an author's name
// to the ASIN the site gave.
type Candidate struct {
	Provider      string
	ProviderLabel string
	Score         float64
	Exact         bool
	Confident     bool // safe to apply without a person looking (online.Confident)
	Details       Details
	CoverURL      string
	CoverCached   bool
	AuthorASINs   map[string]string
}

// LookupResult is what a lookup job returns and the compare sheet reads.
type LookupResult struct {
	Query      online.Query
	FetchedAt  int64
	Candidates []Candidate
	Errors     map[string]string // provider -> error code
	Searched   []string
	Steps      []online.Step // the log of the search, for "How Tendshelf searched"
}

// FromOnline turns a provider's book into Details: markup removed from the text, the description
// cut to 2000 characters, "Book 2" reduced to "2", and the production words (abridged,
// dramatized) read from the description when the site does not say.
func FromOnline(b online.Book) Details {
	d := Details{
		Title:       cleanText(b.Title, 0),
		Subtitle:    cleanText(b.Subtitle, 0),
		Publisher:   cleanText(b.Publisher, 0),
		Language:    cleanText(b.Language, 0),
		Description: html.UnescapeString(cleanText(b.Description, onlineDescriptionLimit)),
		ISBN:        cleanISBN(b.ISBN),
		ASIN:        cleanASIN(b.ASIN),
		Year:        b.Year,
		Narrators:   cleanNames(b.Narrators),
		Genres:      cleanNames(b.Genres),
	}
	var authors []string
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	d.Authors = cleanNames(authors)
	if name := cleanText(b.SeriesName, 0); name != "" {
		d.Series = []SeriesRef{{Name: name, Sequence: cleanSequence(b.Sequence)}}
	}
	switch strings.ToLower(strings.TrimSpace(b.Format)) {
	case "abridged":
		d.Abridged = boolp(true)
	case "unabridged":
		d.Abridged = boolp(false)
	}
	d.fillProduction(d.Description)
	return d
}

func cleanNames(in []string) []string {
	var out []string
	for _, n := range in {
		if n = cleanText(n, 0); n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// SetOnline gives the service the online client, the media tools (author photo thumbnails) and the
// data directory (<data>/people). Without a client, lookups answer ErrOnlineOff.
func (s *Service) SetOnline(c *online.Client, tools media.Tools, dataDir string) {
	s.online, s.tools, s.dataDir = c, tools, dataDir
}

// LookupQuery prefills a lookup from the book's resolved details: title, first author, ASIN, ISBN
// and language.
func (s *Service) LookupQuery(ctx context.Context, bookID string) (online.Query, error) {
	book, err := s.repo.Book(ctx, bookID)
	if err != nil {
		return online.Query{}, err
	}
	rows, err := s.repo.MetadataValues(ctx, bookID)
	if err != nil {
		return online.Query{}, err
	}
	d, _ := Resolve(rows)
	q := online.Query{Title: d.Title, ASIN: d.ASIN, ISBN: d.ISBN, Language: d.Language}
	if q.Title == "" {
		q.Title = book.Title
	}
	if len(d.Authors) > 0 {
		q.Author = d.Authors[0]
	}
	return q, nil
}

// StartLookup queues a lookup of one book. Only Audnexus is asked unless more is true. It returns
// ErrOnlineOff when lookups are off or no client is set, online.ErrEmptyQuery when the query has
// nothing to search for, and ErrNotFound for an unknown book. The caller checks who may edit.
func (s *Service) StartLookup(ctx context.Context, bookID string, q online.Query, more bool, userID string) (string, error) {
	if s.online == nil {
		return "", ErrOnlineOff
	}
	if on, err := OnlineEnabled(ctx, s.repo.Store().Reader()); err != nil {
		return "", err
	} else if !on {
		return "", ErrOnlineOff
	}
	if _, err := s.repo.Book(ctx, bookID); err != nil {
		return "", err
	}
	if strings.TrimSpace(q.Title) == "" && cleanASIN(q.ASIN) == "" && cleanISBN(q.ISBN) == "" {
		return "", online.ErrEmptyQuery
	}
	providers := []string{"audnexus"}
	if more {
		providers = online.Providers
	}
	return s.jobs.Enqueue(ctx, KindOnline, LookupPayload{BookID: bookID, Query: q, Providers: providers}, jobs.EnqueueOptions{CreatedBy: userID})
}

func (s *Service) handleLookup(ctx context.Context, t *jobs.Task) (any, error) {
	var p LookupPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.BookID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	if s.online == nil {
		return nil, offError()
	}
	if on, err := OnlineEnabled(ctx, s.repo.Store().Reader()); err != nil {
		return nil, err
	} else if !on {
		return nil, offError()
	}
	if err := PruneOnlineCache(ctx, s.repo.Store(), s.now().Add(-onlineCacheAge)); err != nil {
		return nil, err
	}
	res, err := s.online.LookupSteps(ctx, p.Query, p.Providers, func(steps []online.Step) { t.Progress(steps) })
	if errors.Is(err, online.ErrEmptyQuery) {
		return nil, &jobs.Error{Code: "empty_query", Text: "There is nothing to look up. Add a title, ASIN or ISBN.", Permanent: true, Err: err}
	}
	if err != nil {
		return nil, err
	}
	// A manual lookup is counted against the daily cap, never blocked by it.
	if err := AddOnlineUsage(context.WithoutCancel(ctx), s.repo.Store(), usageDay(s.now()), res.SentBy()); err != nil {
		s.log.Warn("count online requests", "err", err)
	}
	out := LookupResult{Query: p.Query, FetchedAt: s.now().UnixMilli(), Errors: res.Errors, Searched: res.Searched, Steps: res.Steps}
	out.Candidates = toCandidates(p.Query, res)
	for i := 0; i < len(out.Candidates) && i < coverPreviews; i++ {
		c := &out.Candidates[i]
		if c.CoverURL == "" {
			continue
		}
		if _, _, err := s.online.FetchCover(ctx, c.CoverURL); err == nil {
			c.CoverCached = true
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return out, nil
}

// toCandidates cleans a lookup's books into candidates and marks the ones that are confident
// matches for the query.
func toCandidates(q online.Query, res online.Result) []Candidate {
	var out []Candidate
	for _, b := range res.Candidates {
		c := Candidate{Provider: b.Provider, ProviderLabel: b.ProviderLabel, Score: b.Score, Exact: b.Exact,
			Confident: online.Confident(b, q.Title, q.Author), Details: FromOnline(b), CoverURL: b.CoverURL}
		for _, a := range b.Authors {
			if asin := cleanASIN(a.ASIN); asin != "" {
				if c.AuthorASINs == nil {
					c.AuthorASINs = map[string]string{}
				}
				c.AuthorASINs[a.Name] = asin
			}
		}
		out = append(out, c)
	}
	return out
}

func offError() error {
	return &jobs.Error{Code: "online_off", Text: "Online lookups are turned off.", Permanent: true, Err: ErrOnlineOff}
}

// LookupResultFor returns a lookup job and its result. It is ErrNotFound unless the job is a
// metadata.online job for this book. The result is empty while the job has not finished.
func (s *Service) LookupResultFor(ctx context.Context, jobID, bookID string) (jobs.Job, LookupResult, error) {
	g, ok := s.jobs.(interface {
		Get(context.Context, string) (jobs.Job, error)
	})
	if !ok {
		return jobs.Job{}, LookupResult{}, ErrNotFound
	}
	j, err := g.Get(ctx, jobID)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, LookupResult{}, ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, LookupResult{}, err
	}
	var p LookupPayload
	if j.Kind != KindOnline || json.Unmarshal(j.Payload, &p) != nil || p.BookID != bookID {
		return jobs.Job{}, LookupResult{}, ErrNotFound
	}
	var r LookupResult
	if j.State == jobs.StateDone && len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &r); err != nil {
			return j, LookupResult{}, err
		}
	}
	return j, r, nil
}

// CachedCover returns a cover picture the lookup job already fetched: its bytes, its kind
// ("jpeg", "png" or "webp") and whether it is there. It never starts a download.
func (s *Service) CachedCover(url string) ([]byte, string, bool) {
	if s.online == nil {
		return nil, "", false
	}
	return s.online.CachedCover(url)
}

// LookupSteps is the log of a lookup job: the finished one from its result, else the latest one the
// running job reported as its progress (empty before the first step).
func LookupSteps(j jobs.Job, r LookupResult) []online.Step {
	if j.State == jobs.StateDone {
		return r.Steps
	}
	var steps []online.Step
	if len(j.Progress) > 0 {
		_ = json.Unmarshal(j.Progress, &steps)
	}
	return steps
}
