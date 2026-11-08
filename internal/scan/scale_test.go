package scan

import (
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// TestScaleLibrary measures a scan of a large synthetic library. It is opt-in and prints numbers; it asserts
// only that the scan finds the books it was given.
//
//	TENDSHELF_TEST_SCALE=5000 go test -count=1 -timeout 60m -run TestScaleLibrary -v ./internal/scan/
//
// TENDSHELF_TEST_SCALE_PROBE_MS=20 makes every fake ffprobe answer 20 ms late, a stand-in for a slow share
// (the walk itself has no hook for a slow stat, so slow directory listings are not simulated).
// Files and database live under .local/tests/scale/<run-id>/ and are removed at the end.
func TestScaleLibrary(t *testing.T) {
	books, _ := strconv.Atoi(os.Getenv("TENDSHELF_TEST_SCALE"))
	if books <= 0 {
		t.Skip("set TENDSHELF_TEST_SCALE=<books> to run the large-library measurement")
	}
	probeMs, _ := strconv.Atoi(os.Getenv("TENDSHELF_TEST_SCALE_PROBE_MS"))

	runDir := scaleRunDir(t)
	t.Cleanup(func() { os.RemoveAll(runDir) })
	root := filepath.Join(runDir, "library")
	files := buildScaleTree(t, root, books, probeMs)
	t.Logf("tree: %d books, %d files", books, files)

	st, err := db.Open(bg, filepath.Join(runDir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo := library.New(st)
	lib, err := repo.CreateLibrary(bg, "Books", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddFolder(bg, lib.ID, root); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	r, err := jobs.New(st, jobs.Options{Logger: quiet}) // production timings
	if err != nil {
		t.Fatal(err)
	}
	sc := New(repo, mediatest.FakeTools(t), r, quiet)
	if err := sc.Register(r); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(bg); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(bg)

	changes := func() int64 {
		var n int64
		if err := st.Writer().QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Peak heap, sampled every 20 ms.
	runtime.GC()
	var peak atomic.Uint64
	stopHeap := make(chan struct{})
	heapDone := make(chan struct{})
	go func() {
		defer close(heapDone)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stopHeap:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()

	// A page of the library list, as the Library page asks for it, every 25 ms, tagged with the phase.
	var phase atomic.Value
	phase.Store("scan")
	var mu sync.Mutex
	lat := map[string][]time.Duration{}
	var readErrs atomic.Int64
	stopRead := make(chan struct{})
	readDone := make(chan struct{})
	all := library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	go func() {
		defer close(readDone)
		for {
			select {
			case <-stopRead:
				return
			case <-time.After(25 * time.Millisecond):
			}
			p := phase.Load().(string)
			t0 := time.Now()
			_, err := repo.ListBooks(bg, all, library.ListQuery{LibraryID: lib.ID, Sort: library.SortAdded})
			d := time.Since(t0)
			if err != nil {
				readErrs.Add(1)
			}
			mu.Lock()
			lat[p] = append(lat[p], d)
			mu.Unlock()
		}
	}()

	c0 := changes()
	t0 := time.Now()
	res, err := sc.ScanLibrary(bg, lib.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstScan := time.Since(t0)
	scanChanges := changes() - c0
	if res.BooksAdded != books {
		t.Fatalf("books added = %d, want %d", res.BooksAdded, books)
	}

	phase.Store("probes")
	for {
		ids, err := repo.UnsettledAudioFileIDs(bg, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		var active int
		if err := st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE state IN ('queued','running')`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 && active == 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	settled := time.Since(t0)
	probeChanges := changes() - c0 - scanChanges

	phase.Store("rescan")
	c1 := changes()
	t1 := time.Now()
	res2, err := sc.ScanLibrary(bg, lib.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	rescan := time.Since(t1)
	rescanChanges := changes() - c1
	if res2.FilesAdded+res2.FilesChanged+res2.ProbesEnqueued != 0 {
		t.Errorf("a rescan with no change did work: %+v", res2)
	}

	phase.Store("idle")
	time.Sleep(time.Second)
	close(stopRead)
	<-readDone
	close(stopHeap)
	<-heapDone

	mib := func(b uint64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }
	t.Logf("SCALE books=%d files=%d probe_delay_ms=%d gomaxprocs=%d", books, files, probeMs, runtime.GOMAXPROCS(0))
	t.Logf("SCALE first scan (walk, hash, write)      %v  (%d rows changed)", firstScan.Round(time.Millisecond), scanChanges)
	t.Logf("SCALE all probes settled, from scan start %v  (%d rows changed by probes and jobs)", settled.Round(time.Millisecond), probeChanges)
	t.Logf("SCALE no-change rescan                    %v  (%d rows changed)", rescan.Round(time.Millisecond), rescanChanges)
	t.Logf("SCALE peak heap in the server process     %s (the fake ffprobe processes are not counted)", mib(peak.Load()))
	for _, p := range []string{"scan", "probes", "rescan", "idle"} {
		mu.Lock()
		ds := append([]time.Duration(nil), lat[p]...)
		mu.Unlock()
		if len(ds) == 0 {
			t.Logf("SCALE library list during %-7s no sample", p)
			continue
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		q := func(f float64) time.Duration { return ds[int(float64(len(ds)-1)*f)].Round(100 * time.Microsecond) }
		t.Logf("SCALE library list during %-7s n=%-5d p50=%v p95=%v max=%v", p, len(ds), q(0.5), q(0.95), ds[len(ds)-1].Round(100*time.Microsecond))
	}
	if n := readErrs.Load(); n > 0 {
		t.Errorf("%d library list queries failed", n)
	}
}

// scaleRunDir is .local/tests/scale/<run-id>/ at the module root.
func scaleRunDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
	run := filepath.Join(dir, ".local", "tests", "scale", fmt.Sprintf("run-%d-%d", time.Now().Unix(), os.Getpid()))
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	return run
}

// buildScaleTree writes the books and returns the number of files. The mix, from a fixed seed: 70 % one m4b in
// Author/Title/; 20 % a numbered multi-part folder of 10 to 40 mp3 files in Author/Title/; 10 % in
// Author/Series/Book N/ (a third of those multi-part). Files are a few hundred bytes of fake description.
func buildScaleTree(t *testing.T, root string, books, probeMs int) int {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	authors := books/8 + 1
	var n int
	write := func(dir, name, container string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mediatest.WriteFake(t, filepath.Join(dir, name), mediatest.FakeFile{Container: container, DurationMs: 3_600_000, SleepMs: probeMs,
			Tags: map[string]string{"title": name}})
		n++
	}
	parts := func(dir string) {
		k := 10 + rng.Intn(31)
		for i := 1; i <= k; i++ {
			write(dir, fmt.Sprintf("Part %03d.mp3", i), "mp3")
		}
	}
	for i := 0; i < books; i++ {
		author := filepath.Join(root, fmt.Sprintf("Author %04d", rng.Intn(authors)))
		switch x := rng.Intn(100); {
		case x < 70:
			write(filepath.Join(author, fmt.Sprintf("Book %05d", i)), "book.m4b", "mp4")
		case x < 90:
			parts(filepath.Join(author, fmt.Sprintf("Book %05d", i)))
		default:
			dir := filepath.Join(author, fmt.Sprintf("Series %04d", i/10), fmt.Sprintf("Book %d - Title %05d", i%10+1, i))
			if rng.Intn(3) == 0 {
				parts(dir)
			} else {
				write(dir, "book.m4b", "mp4")
			}
		}
	}
	return n
}
