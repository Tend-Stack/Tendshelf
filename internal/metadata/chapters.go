package metadata

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// KindChapterLookup is the job that asks Audnexus for the chapter list of one ASIN. It starts only
// from a click on "Look up chapters" and stores nothing about the book: the list is the job's result.
const KindChapterLookup = "chapter_lookup"

// ChapterLookupPayload is the payload of a chapter_lookup job.
type ChapterLookupPayload struct {
	BookID string `json:"book_id"`
	ASIN   string `json:"asin"`
}

// StartChapterLookup queues a chapter lookup for one book. The ASIN must be ten letters and
// digits (online.ErrEmptyQuery otherwise). A waiting or running job of the same book and ASIN is
// returned instead of a second one. It returns ErrOnlineOff when lookups are off or no client is
// set, and ErrNotFound for an unknown book. The caller checks who may edit.
func (s *Service) StartChapterLookup(ctx context.Context, bookID, asin, userID string) (string, error) {
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
	if asin = cleanASIN(asin); asin == "" {
		return "", online.ErrEmptyQuery
	}
	if st, ok := s.store(); ok {
		j, found, err := st.ActiveFor(ctx, KindChapterLookup, "book_id", bookID)
		if err != nil {
			return "", err
		}
		var p ChapterLookupPayload
		if found && json.Unmarshal(j.Payload, &p) == nil && p.ASIN == asin {
			return j.ID, nil
		}
	}
	return s.jobs.Enqueue(ctx, KindChapterLookup, ChapterLookupPayload{BookID: bookID, ASIN: asin}, jobs.EnqueueOptions{CreatedBy: userID})
}

func (s *Service) handleChapterLookup(ctx context.Context, t *jobs.Task) (any, error) {
	var p ChapterLookupPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.BookID == "" || cleanASIN(p.ASIN) == "" {
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
	q, err := s.LookupQuery(ctx, p.BookID)
	if errors.Is(err, ErrNotFound) {
		return nil, &jobs.Error{Code: "gone", Text: "This book is no longer in the library.", Permanent: true, Err: err}
	}
	if err != nil {
		return nil, err
	}
	list, err := s.online.Chapters(ctx, p.ASIN, q.Language)
	// Requests really sent count against the day even when the answer was a miss.
	if uerr := AddOnlineUsage(context.WithoutCancel(ctx), s.repo.Store(), usageDay(s.now()), map[string]int{"audnexus": list.Sent}); uerr != nil {
		s.log.Warn("count online requests", "err", uerr)
	}
	var oe *online.Error
	if errors.As(err, &oe) {
		return nil, &jobs.Error{Code: oe.Code, Text: oe.Text(), Permanent: true, Err: err}
	}
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ChapterLookupFor returns a chapter lookup job and its result. It is ErrNotFound unless the job
// is a chapter_lookup job for this book. The list is empty while the job has not finished.
func (s *Service) ChapterLookupFor(ctx context.Context, jobID, bookID string) (jobs.Job, online.ChapterList, error) {
	st, ok := s.store()
	if !ok {
		return jobs.Job{}, online.ChapterList{}, ErrNotFound
	}
	j, err := st.Get(ctx, jobID)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, online.ChapterList{}, ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, online.ChapterList{}, err
	}
	var p ChapterLookupPayload
	if j.Kind != KindChapterLookup || json.Unmarshal(j.Payload, &p) != nil || p.BookID != bookID {
		return jobs.Job{}, online.ChapterList{}, ErrNotFound
	}
	var l online.ChapterList
	if j.State == jobs.StateDone && len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &l); err != nil {
			return j, online.ChapterList{}, err
		}
	}
	return j, l, nil
}
