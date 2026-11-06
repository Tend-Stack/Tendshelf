package metadata_test

import (
	"errors"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// chlkFirst puts a route in front of the others: the book route is a prefix of the chapters address.
func chlkFirst(n *lookupNet, prefix string, body []byte) {
	n.routes = append([]lookupRoute{{prefix, body}}, n.routes...)
}

func TestChapterLookupJob(t *testing.T) {
	e, id := newLookupBook(t)
	chlkFirst(e.net, "https://api.audnex.us/books/B0TESTBOOK/chapters", lookupSample(t, "audnexus_chapters.json"))

	if _, err := e.svc.StartChapterLookup(bg, id, "not an asin", ""); !errors.Is(err, online.ErrEmptyQuery) {
		t.Errorf("bad asin: %v", err)
	}
	if _, err := e.svc.StartChapterLookup(bg, "bk_missing", "B0TESTBOOK", ""); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown book: %v", err)
	}
	jid, err := e.svc.StartChapterLookup(bg, id, "b0testbook", "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(jid); j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	j, l, err := e.svc.ChapterLookupFor(bg, jid, id)
	if err != nil || j.State != jobs.StateDone || len(l.Chapters) != 3 || l.Chapters[2].Title != "Chapter 2" || l.RuntimeMs != 1802000 {
		t.Fatalf("result: %v %+v", err, l)
	}
	if n := e.count(`SELECT COUNT(*) FROM online_usage`); n == 0 {
		t.Error("the request is counted against the day")
	}
}

func TestChapterLookupReuseAndOtherBook(t *testing.T) {
	e, id := newLookupBook(t)
	e.write("Bo Book/Second/s.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "Second"}})
	e.scanAndProbe()
	other := e.book("Bo Book/Second")
	// With the runner stopped nothing takes the job, so the second start finds it waiting.
	if err := e.runner.Stop(bg); err != nil {
		t.Fatal(err)
	}
	a, err := e.svc.StartChapterLookup(bg, id, "B0TESTBOOK", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.StartChapterLookup(bg, id, "B0TESTBOOK", "")
	if err != nil || a != b {
		t.Errorf("a job for the same book and ASIN is reused: %s %s %v", a, b, err)
	}
	if _, _, err := e.svc.ChapterLookupFor(bg, a, other.ID); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("other book: %v", err)
	}
	if _, _, err := e.svc.ChapterLookupFor(bg, "jb_missing", id); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
	// A lookup job of the details kind is not a chapter result.
	lj, err := e.svc.StartLookup(bg, id, online.Query{Title: "The Test Match"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ChapterLookupFor(bg, lj, id); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("other kind: %v", err)
	}
}

func TestChapterLookupMissAndOff(t *testing.T) {
	e, id := newLookupBook(t)
	// No route for this ASIN: every store answers 404 and the job ends with the plain words.
	jid, err := e.svc.StartChapterLookup(bg, id, "B0MISSING1", "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(jid); j.State != jobs.StateFailed || j.ErrorCode != "no_chapters" || j.ErrorText != "Audnexus has no chapters for this book." {
		t.Errorf("miss: %s %s %q", j.State, j.ErrorCode, j.ErrorText)
	}

	if err := metadata.SetOnlineEnabled(bg, e.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartChapterLookup(bg, id, "B0TESTBOOK", ""); !errors.Is(err, metadata.ErrOnlineOff) {
		t.Errorf("start while off: %v", err)
	}
	before := e.net.requests()
	qid, err := e.runner.Enqueue(bg, metadata.KindChapterLookup, metadata.ChapterLookupPayload{BookID: id, ASIN: "B0TESTBOOK"}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(qid); j.State != jobs.StateFailed || j.ErrorCode != "online_off" || e.net.requests() != before {
		t.Errorf("queued while off: %s %s, %d new requests", j.State, j.ErrorCode, e.net.requests()-before)
	}
}
