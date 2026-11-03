package mediajobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// TestCoverAdoptIfNoCover: an automatic online cover fills only a book without a picture, and gives
// way to one that appears meanwhile.
func TestCoverAdoptIfNoCover(t *testing.T) {
	e, c, b := adoptEnv(t)
	if b.CoverID != "" {
		t.Skip("the fake scan produced a cover")
	}
	fetches := 0
	c.SetFetcher(func(context.Context, string) ([]byte, string, error) {
		fetches++
		return sample(t, "jpg"), "image/jpeg", nil
	})

	// FillOnline queues for a book with no cover and no waiting media.cover job.
	e.enq.active = map[string]map[string]bool{KindCover: {b.ID: true}}
	before := len(e.enq.kinds)
	if err := c.FillOnline(bg, b.ID, "https://x/c.jpg"); err != nil || len(e.enq.kinds) != before {
		t.Fatalf("waiting media.cover job: err %v, queued %d", err, len(e.enq.kinds)-before)
	}
	e.enq.active = nil
	if err := c.FillOnline(bg, b.ID, ""); err != nil || len(e.enq.kinds) != before {
		t.Fatalf("empty url must queue nothing: %v", err)
	}
	if err := c.FillOnline(bg, b.ID, "https://x/c.jpg"); err != nil || len(e.enq.kinds) != before+1 || e.enq.kinds[before] != KindCoverAdopt {
		t.Fatalf("FillOnline: err %v kinds %v", err, e.enq.kinds)
	}

	// The guard refuses once a cover exists, after the fence passed.
	ok := func(context.Context, *sql.Tx) error { return nil }
	if err := e.st.WriteTx(bg, func(tx *sql.Tx) error { return ifNoCoverGuard(ok, b.ID)(bg, tx) }); err != nil {
		t.Fatalf("no cover yet: %v", err)
	}
	if _, err := c.Adopt(bg, b.ID, library.CoverOnline, sample(t, "png"), ifNoCoverGuard(ok, b.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Adopt(bg, b.ID, library.CoverOnline, sample(t, "jpg"), ifNoCoverGuard(ok, b.ID)); !errors.Is(err, errCoverPresent) {
		t.Fatalf("second adoption: %v", err)
	}
	if n := len(e.covers1(b)); n != 1 {
		t.Errorf("covers = %d, want 1", n)
	}

	// With a cover, FillOnline queues nothing and the job ends kept without fetching.
	before = len(e.enq.kinds)
	if err := c.FillOnline(bg, b.ID, "https://x/c.jpg"); err != nil || len(e.enq.kinds) != before {
		t.Errorf("book with a cover: err %v", err)
	}
	payload, _ := json.Marshal(AdoptPayload{BookID: b.ID, Source: library.CoverOnline, URL: "https://x/c.jpg", IfNoCover: true})
	out, err := c.adoptHandler(bg, &jobs.Task{ID: "j", Payload: payload})
	if m, isMap := out.(map[string]bool); err != nil || !isMap || !m["kept"] || fetches != 0 {
		t.Errorf("handler: %v %v, fetches %d", out, err, fetches)
	}
}
