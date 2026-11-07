package upload

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// KindRemoveAll is the job that removes every book one person added ("Remove all their books", decision 0029).
const KindRemoveAll = "upload.remove_all"

// RemoveAllPayload names the person whose books go and the admin who asked.
type RemoveAllPayload struct {
	UserID string `json:"user_id"`
	By     string `json:"by"`
}

// The reasons a book stays after "Remove all their books".
const (
	KeptChanged      = "changed"       // a file changed since Tendshelf placed it, so it was left alone
	KeptNotRemovable = "not_removable" // Tendshelf did not place every file of the book
	KeptNotTheirs    = "not_theirs"    // someone else added part of the book
)

// KeptBook is a book "Remove all" left in place, with the reason.
type KeptBook struct {
	BookID string `json:"book_id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// RemoveAllResult is the stored result of an upload.remove_all job.
type RemoveAllResult struct {
	Removed int        `json:"removed"`
	Kept    []KeptBook `json:"kept"`
}

func (s *Service) handleRemoveAll(ctx context.Context, t *jobs.Task) (any, error) {
	var p RemoveAllPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.UserID == "" || p.By == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	books, err := s.repo.BooksAddedBy(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	res := RemoveAllResult{Kept: []KeptBook{}}
	for i, b := range books {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		t.Progress(map[string]int{"done": i, "total": len(books)})
		_, err := s.Remove(ctx, p.By, b.ID, p.UserID)
		var ue *Error
		switch {
		case errors.As(err, &ue) && ue.Code == "not_found":
			continue // gone already
		case errors.As(err, &ue) && ue.Code == KeptNotTheirs:
			res.Kept = append(res.Kept, KeptBook{b.ID, b.Title, KeptNotTheirs})
		case errors.As(err, &ue):
			res.Kept = append(res.Kept, KeptBook{b.ID, b.Title, KeptNotRemovable})
		case err != nil:
			return nil, err
		default:
			if _, gerr := s.repo.Book(ctx, b.ID); errors.Is(gerr, library.ErrNotFound) {
				res.Removed++
			} else {
				res.Kept = append(res.Kept, KeptBook{b.ID, b.Title, KeptChanged})
			}
		}
	}
	t.Progress(map[string]int{"done": len(books), "total": len(books)})
	return res, nil
}
