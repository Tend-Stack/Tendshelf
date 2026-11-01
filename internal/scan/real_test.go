package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestRealScan runs the scan and probe jobs over files made by real ffmpeg:
// `make test-scan-real` (TENDSHELF_TEST_FFMPEG=1).
func TestRealScan(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	e, r := jobsEnv(t, tools)
	mk := func(rel string) string {
		p := filepath.Join(e.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mediatest.GenM4B(t, tools, mk("Chaptered/book.m4b"), mediatest.Synth{Title: "Chaptered", DurationMs: 6000,
		Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 2000, "One"), mediatest.Chapter(2000, 4000, "Two"), mediatest.Chapter(4000, 6000, "Three")}})
	mp3Dir := filepath.Dir(mk("Parts/x"))
	mediatest.GenMP3Parts(t, tools, mp3Dir, 3, mediatest.Synth{Title: "Parts", Author: "Ana Autora", Album: "Parts"})
	mediatest.GenEncryptedM4A(t, tools, mk("Encrypted/x.m4a"))
	mediatest.WriteProtected(t, mk("Protected/x.aax"))

	if _, err := r.Enqueue(bg, KindScan, ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	idle(t, e)

	m4b := e.bookAt(filepath.Join(e.root, "Chaptered"))
	chs, err := e.repo.Chapters(bg, m4b.ID)
	if err != nil || len(chs) != 3 {
		t.Fatalf("m4b chapters = %+v, %v", chs, err)
	}
	for i, want := range []int64{0, 2000, 4000} {
		if chs[i].StartMs != want || chs[i].Source != library.ChapterFile {
			t.Errorf("m4b chapter %d = %+v", i, chs[i])
		}
	}
	if m4b.DurationMs < 5900 || m4b.DurationMs > 6100 || m4b.RefusedReason != "" {
		t.Errorf("m4b book = %+v", m4b)
	}

	parts := e.bookAt(filepath.Join(e.root, "Parts"))
	pch, err := e.repo.Chapters(bg, parts.ID)
	if err != nil || len(pch) != 3 {
		t.Fatalf("mp3 chapters = %+v, %v", pch, err)
	}
	for i, c := range pch {
		if c.Source != library.ChapterFiles || c.Title == "" {
			t.Errorf("mp3 chapter %d = %+v", i, c)
		}
	}
	// Three 2 s parts; allow 100 ms per file: ffmpeg 6.1 counts the MP3 encoder's padding frames
	// (6,192 ms in the nightly job), ffmpeg 8 does not (about 6,000 ms). The offsets below must
	// still add up exactly.
	if parts.DurationMs < 5700 || parts.DurationMs > 6300 {
		t.Errorf("mp3 duration = %d, want 6000 +- 300", parts.DurationMs)
	}
	files, _ := e.repo.AudioFiles(bg, parts.ID)
	if len(files) != 3 || files[0].OffsetMs != 0 || files[1].OffsetMs != files[0].DurationMs || files[2].OffsetMs != files[0].DurationMs+files[1].DurationMs {
		t.Errorf("mp3 offsets = %+v", files)
	}

	for _, root := range []string{"Encrypted", "Protected"} {
		b := e.bookAt(filepath.Join(e.root, root))
		if b.RefusedReason != library.RefusedDRM || RefusalText(b.RefusedReason) != refusedSentence {
			t.Errorf("%s book = %+v", root, b)
		}
	}
}
