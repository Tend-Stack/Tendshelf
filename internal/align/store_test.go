package align

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

var (
	alstHexA = strings.Repeat("a", 64)
	alstHexB = strings.Repeat("b", 64)
	alstHexC = strings.Repeat("c", 64)
)

// alstStore opens a fresh database with one book and one edition (ed_1).
func alstStore(t *testing.T) (*Store, *db.Store) {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, ext, size, quick_hash, sha256, state, created_at, updated_at)
VALUES ('ef_1', 'library', 'lib_1', 'lf_1', '/audio/a/a.epub', 'bk_1', 'epub', 10, '` + alstHexA + `', NULL, 'ready', 1, 1)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + alstHexA + `', 'tendshelf-ebook-v1', '` + alstHexB + `', 5, 'epub', 1, 2, 3, 1)`,
	} {
		if _, err := st.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return NewStore(st), st
}

// alstClockAfter returns a clock that reads s's time plus sec seconds, so rows differ in created_at.
func alstClockAfter(s *Store, sec int) func() time.Time {
	base := s.now()
	return func() time.Time { return base.Add(time.Duration(sec) * time.Second) }
}

func alstRow(id, job string) Alignment {
	return Alignment{ID: id, EditionID: "ed_1", BookID: "bk_1", AudioKey: alstHexA, DocSHA256: alstHexB, JobID: job}
}

func TestAlignStoreGuard(t *testing.T) {
	s, _ := alstStore(t)
	ctx := context.Background()
	if err := s.Insert(ctx, alstRow("al_1", "jb_1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, alstRow("al_2", "jb_2")); err == nil {
		t.Fatal("a second active alignment must fail")
	}
	if ok, err := s.SetState(ctx, "al_1", "jb_other", StateEmitting, ""); err != nil || ok {
		t.Errorf("another job's SetState = %v, %v; want false", ok, err)
	}
	if ok, err := s.SetState(ctx, "al_1", "jb_1", StateEmitting, ""); err != nil || !ok {
		t.Fatalf("SetState = %v, %v", ok, err)
	}
	if ok, err := s.SetState(ctx, "al_1", "jb_1", StateCancelled, "cancelled"); err != nil || !ok {
		t.Fatalf("cancel = %v, %v", ok, err)
	}
	for name, run := range map[string]func() (bool, error){
		"SetState": func() (bool, error) { return s.SetState(ctx, "al_1", "jb_1", StateAligning, "") },
		"Finish":   func() (bool, error) { return s.Finish(ctx, "al_1", "jb_1", Finish{State: StateDone}) },
		"Adopt":    func() (bool, error) { return s.Adopt(ctx, "al_1", "jb_1", alstHexC, "", "") },
		"PutChapter": func() (bool, error) {
			return s.PutChapter(ctx, "al_1", "jb_1", ChapterRow{Idx: 0, State: ChapterPending})
		},
	} {
		if ok, err := run(); err != nil || ok {
			t.Errorf("%s after cancel = %v, %v; want false", name, ok, err)
		}
	}
	a, found, err := s.Get(ctx, "al_1")
	if err != nil || !found || a.State != StateCancelled || a.ErrorCode != "cancelled" || a.FinishedAt == 0 {
		t.Fatalf("row = %+v found %v err %v", a, found, err)
	}
	if rows, _ := s.Chapters(ctx, "al_1"); len(rows) != 0 {
		t.Errorf("a cancelled job wrote %d chapter rows", len(rows))
	}
	// The edition is free for a new alignment, and ByJob/Active find the right rows.
	if err := s.Insert(ctx, alstRow("al_2", "jb_2")); err != nil {
		t.Fatal(err)
	}
	if a, ok, _ := s.Active(ctx, "ed_1"); !ok || a.ID != "al_2" {
		t.Errorf("Active = %+v %v", a, ok)
	}
	if a, ok, _ := s.ByJob(ctx, "jb_1"); !ok || a.ID != "al_1" {
		t.Errorf("ByJob = %+v %v", a, ok)
	}
	if _, ok, _ := s.Latest(ctx, "ed_1"); ok {
		t.Error("Latest must ignore cancelled and active rows")
	}
}

func TestAlignStoreAdopt(t *testing.T) {
	s, _ := alstStore(t)
	ctx := context.Background()
	a := alstRow("al_1", "jb_1")
	a.State = StateHeld
	a.ErrorCode = "audio_changed"
	if err := s.Insert(ctx, a); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Adopt(ctx, "al_1", "jb_1", alstHexC, alstHexC, ""); err != nil || !ok {
		t.Fatalf("Adopt = %v, %v", ok, err)
	}
	got, _, _ := s.Get(ctx, "al_1")
	if got.AudioKey != alstHexC || got.ModelSHA256 != alstHexC || got.State != StateQueued || got.ErrorCode != "" || got.RuntimeID != "" {
		t.Errorf("after Adopt: %+v", got)
	}
	if ok, _ := s.Adopt(ctx, "al_1", "jb_1", alstHexA, "", ""); ok {
		t.Error("Adopt must work only from held")
	}
}

func TestAlignStoreSupersede(t *testing.T) {
	s, _ := alstStore(t)
	ctx := context.Background()
	fin := func(id, job, state string, labels []string) {
		t.Helper()
		if err := s.Insert(ctx, alstRow(id, job)); err != nil {
			t.Fatal(err)
		}
		ok, err := s.Finish(ctx, id, job, Finish{State: state, Labels: labels, Coverage: 0.5, SentencesTotal: 4, SentencesTimed: 2})
		if err != nil || !ok {
			t.Fatalf("Finish %s = %v, %v", id, ok, err)
		}
	}
	fin("al_1", "jb_1", StateDone, nil)
	s.now = alstClockAfter(s, 10)
	fin("al_2", "jb_2", StatePartial, []string{"gaps"})
	one, _, _ := s.Get(ctx, "al_1")
	two, _, _ := s.Get(ctx, "al_2")
	if one.SupersededBy != "al_2" || two.SupersededBy != "" {
		t.Errorf("superseded_by: al_1=%q al_2=%q", one.SupersededBy, two.SupersededBy)
	}
	if two.Coverage != 0.5 || two.SentencesTimed != 2 || len(two.Labels) != 1 || two.FinishedAt == 0 {
		t.Errorf("al_2 = %+v", two)
	}
	if l, ok, _ := s.Latest(ctx, "ed_1"); !ok || l.ID != "al_2" {
		t.Errorf("Latest = %+v %v", l, ok)
	}
	// A failed run replaces nothing.
	s.now = alstClockAfter(s, 20)
	fin("al_3", "jb_3", StateFailed, nil)
	if two, _, _ = s.Get(ctx, "al_2"); two.SupersededBy != "" {
		t.Error("a failed alignment must not supersede")
	}
}

func TestAlignStoreChapterByKey(t *testing.T) {
	s, _ := alstStore(t)
	ctx := context.Background()
	if err := s.Insert(ctx, alstRow("al_1", "jb_1")); err != nil {
		t.Fatal(err)
	}
	start, end, conf := int64(1000), int64(5000), 0.8
	rows := []ChapterRow{
		{Idx: 0, State: ChapterAligned, ChapterKey: alstHexC, StartMS: &start, EndMS: &end, SentencesTotal: 3, SentencesTimed: 2, MeanConfidence: &conf},
		{Idx: 1, State: ChapterPending},
	}
	for _, r := range rows {
		if ok, err := s.PutChapter(ctx, "al_1", "jb_1", r); err != nil || !ok {
			t.Fatalf("PutChapter %d = %v, %v", r.Idx, ok, err)
		}
	}
	// Upsert replaces the row.
	rows[1] = ChapterRow{Idx: 1, State: ChapterUnmatched, SentencesTotal: 2}
	if ok, err := s.PutChapter(ctx, "al_1", "jb_1", rows[1]); err != nil || !ok {
		t.Fatal(ok, err)
	}
	got, err := s.Chapters(ctx, "al_1")
	if err != nil || len(got) != 2 || got[1].State != ChapterUnmatched || got[1].StartMS != nil || got[0].MeanConfidence == nil {
		t.Fatalf("Chapters = %+v err %v", got, err)
	}
	c, ok, err := s.ChapterByKey(ctx, alstHexC)
	if err != nil || !ok || c.Idx != 0 || *c.StartMS != 1000 || *c.EndMS != 5000 || c.SentencesTimed != 2 {
		t.Fatalf("ChapterByKey = %+v %v %v", c, ok, err)
	}
	if _, ok, _ := s.ChapterByKey(ctx, alstHexA); ok {
		t.Error("unknown key must not be found")
	}
	// Only aligned rows count.
	if _, err := s.db.Writer().Exec(`UPDATE alignment_chapters SET state = 'failed' WHERE chapter_idx = 0`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ChapterByKey(ctx, alstHexC); ok {
		t.Error("a failed chapter must not be found by key")
	}
}
