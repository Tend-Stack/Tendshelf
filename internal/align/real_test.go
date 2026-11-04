package align_test

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/align"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	alignfiles "github.com/Tend-Stack/tendshelf/runtimes/align"
)

// realRuntimes adapts the runtime package to the service.
type realRuntimes struct {
	comps *alignrt.Components
	mgr   *alignrt.Manager
	cfg   alignrt.Config
}

func (r realRuntimes) Usable(ctx context.Context, lang string) (alignrt.Component, bool, error) {
	c, ok, err := r.comps.Get(ctx, lang)
	return c, ok && err == nil && c.CheckError == "" && !c.Disabled, err
}

func (r realRuntimes) Emitter(ctx context.Context, lang string) (alignrt.Emitter, error) {
	return r.mgr.Emitter(ctx, lang)
}

// TestAlignJobReal runs the whole job with the real worker and ffmpeg on a hand-prepared sample and writes a
// report. Opt-in: TENDSHELF_TEST_ALIGN_RUNTIME (folder with venv/ and models/), TENDSHELF_TEST_ALIGN_SAMPLE
// (folder with audio.mp3, text.txt and truth.tsv, see docs/plans/m5-r3-alignment.md "Real run") and
// TENDSHELF_TEST_FFMPEG. Not run by the Go suite; the output goes under .local/tests/align-accuracy/.
func TestAlignJobReal(t *testing.T) {
	root, sample := os.Getenv("TENDSHELF_TEST_ALIGN_RUNTIME"), os.Getenv("TENDSHELF_TEST_ALIGN_SAMPLE")
	if root == "" || sample == "" || os.Getenv("TENDSHELF_TEST_FFMPEG") == "" {
		t.Skip("set TENDSHELF_TEST_ALIGN_RUNTIME, TENDSHELF_TEST_ALIGN_SAMPLE and TENDSHELF_TEST_FFMPEG to run the real alignment")
	}
	ctx := context.Background()
	tools, err := media.Locate(media.Config{})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := filepath.Abs(filepath.Join(sample, "audio.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	pr, err := tools.Probe(ctx, audio)
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := media.QuickHash(audio)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ebook.Read(ctx, filepath.Join(sample, "text.txt"), "txt", ebook.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	runID := time.Now().UTC().Format("20060102T150405")
	data := filepath.Join(".local", "tests", "align-accuracy", runID)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := db.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	docJSON, err := ebook.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	docSHA, err := ebook.WriteDoc(filepath.Join(data, "text"), docJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, ext, size, quick_hash, sha256, state, created_at, updated_at)
VALUES ('ef_1', 'library', 'lib_1', 'lf_1', '/audio/a/a.txt', 'bk_1', 'txt', 10, '` + ajobHex("e") + `', '` + ajobHex("a") + `', 'ready', 1, 1)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, language, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + ajobHex("a") + `', '` + ebook.Recipe + `', '` + docSHA + `', 5, 'txt', 'en', 1, 1, 1, 1)`,
	} {
		if _, err := st.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := st.Writer().Exec(`INSERT INTO audio_files(id, book_id, path, idx, size, mtime_ns, quick_hash, container, codec, duration_ms, offset_ms, updated_at)
VALUES ('af_1', 'bk_1', ?, 0, ?, 1, ?, 'mp3', 'mp3', ?, 0, 1)`, audio, size, hash, pr.DurationMs); err != nil {
		t.Fatal(err)
	}

	raw, ok := alignfiles.ManifestJSON("en")
	if !ok {
		t.Fatal("no manifest")
	}
	man, err := alignrt.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := alignrt.Config{
		Python: filepath.Join(root, "venv", "bin", "python"), ModelsDir: filepath.Join(root, "models"),
		WorkDir: filepath.Join(data, "runtimes", "align"), Script: alignfiles.WorkerPy(),
		Manifests: map[string]alignrt.Manifest{"en": man}, Threads: alignrt.DefaultThreads(),
		ReadyTimeout: 3 * time.Minute, RequestTimeout: 3 * time.Minute, DescribeTimeout: 5 * time.Minute,
	}
	comps := alignrt.NewComponents(st)
	if c, err := alignrt.Check(ctx, cfg, comps, "en"); err != nil || c.CheckError != "" {
		t.Fatalf("runtime check: %+v %v", c, err)
	}
	mgr := alignrt.New(cfg, comps, nil)
	defer mgr.Close()

	runner, err := jobs.New(st, jobs.Options{PollEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	svc := align.NewService(align.Deps{
		Store: st, Library: library.New(st), Ebooks: ebook.NewStore(st), Runner: runner,
		Runtimes: realRuntimes{comps: comps, mgr: mgr, cfg: cfg}, Decoder: tools, DataDir: data, CacheMaxBytes: 2 << 30,
	})
	if err := svc.Register(runner); err != nil {
		t.Fatal(err)
	}
	if err := runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runner.Stop(ctx)
	began := time.Now()
	a, err := svc.Start(ctx, "ed_1", "bk_1", "")
	if err != nil {
		t.Fatal(err)
	}
	for a.State != align.StateDone && a.State != align.StatePartial && a.State != align.StateFailed {
		time.Sleep(time.Second)
		if a, _, err = svc.Store().Get(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if time.Since(began) > 2*time.Hour {
			t.Fatal("timed out")
		}
	}
	rows, err := svc.Store().Chapters(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	starts := map[string]int64{} // sentence id -> start
	byWords := map[string]int64{}
	for _, r := range rows {
		tm, err := align.ReadTiming(filepath.Join(data, "align"), r.ChapterKey)
		if err != nil {
			continue
		}
		for i, sid := range tm.SID {
			starts[sid] = tm.SS[i]
		}
	}
	for _, ch := range doc.Chapters {
		for _, b := range ch.Blocks {
			spans := ebook.WordSpans(b)
			for k := range b.Sentences {
				var w []string
				for _, sp := range spans {
					if sp.Sentence == k && len(w) < 5 {
						w = append(w, strings.ToLower(sp.Text))
					}
				}
				byWords[strings.Join(w, " ")] = starts[ebook.SentenceID(b.ID, k)]
			}
		}
	}
	f, err := os.Open(filepath.Join(sample, "truth.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var total, good int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ms, words, ok := strings.Cut(sc.Text(), "\t")
		want, err := strconv.ParseInt(ms, 10, 64)
		if !ok || err != nil {
			continue
		}
		total++
		if got, ok := byWords[strings.ToLower(strings.TrimSpace(words))]; ok && got >= 0 && math.Abs(float64(got-want)) <= 250 {
			good++
		}
	}
	var cache int64
	_ = filepath.Walk(filepath.Join(data, "cache"), func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			cache += fi.Size()
		}
		return nil
	})
	var timing int64
	_ = filepath.Walk(filepath.Join(data, "align"), func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			timing += fi.Size()
		}
		return nil
	})
	hours := float64(pr.DurationMs) / 3.6e6
	report := map[string]any{
		"state": a.State, "coverage": a.Coverage, "labels": a.Labels, "truthSentences": total, "within250ms": good,
		"share":           float64(good) / math.Max(1, float64(total)),
		"cpuSecPerHour":   mgr.CPUTime().Seconds() / math.Max(hours, 1e-9),
		"wallSeconds":     time.Since(began).Seconds(),
		"timingBytes":     timing,
		"emissionsBytes":  cache,
		"audioDurationMs": pr.DurationMs,
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(data, "report.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", out)
	if a.State == align.StateFailed {
		t.Fatalf("alignment failed: %s", a.ErrorCode)
	}
}
