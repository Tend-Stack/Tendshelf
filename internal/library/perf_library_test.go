package library_test

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// The Duplicates page and Library health at the size of a very large library (decision: S3/S4 review, 2026-11).
// Skips without TENDSHELF_TEST_PERF=1, like the other perf tests (`make perf`). TENDSHELF_PERF_BOOKS changes the
// size (default 100,000 books with 12 files each). Every 1,000th book is a copy of the one before it, so the page
// has books/1000 same-audio groups to find. The timings are logged; the test asserts the results.
func perfBooks(t *testing.T) int {
	t.Helper()
	if os.Getenv("TENDSHELF_TEST_PERF") != "1" {
		t.Skip("TENDSHELF_TEST_PERF=1 not set")
	}
	if n, err := strconv.Atoi(os.Getenv("TENDSHELF_PERF_BOOKS")); err == nil && n >= 1000 {
		return n
	}
	return 100_000
}

func perfSeed(t *testing.T, r *library.Repo, f library.Folder, books int, missing bool) {
	t.Helper()
	miss := "NULL"
	if missing {
		miss = "1000"
	}
	tx, err := r.Store().Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// A book n is a copy of book n-1 when n % 1000 == 1: the file sizes and hashes derive from `g`, which repeats.
	for _, q := range []string{
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ` + strconv.Itoa(books) + `)
INSERT INTO books(id, library_id, folder_id, root_path, title, sort_title, duration_ms, added_at, updated_at, missing_since)
SELECT printf('bk_%08d', i), '` + f.LibraryID + `', '` + f.ID + `', printf('/perf/b%08d', i), printf('Title %08d', i), printf('title %08d', i),
  3600000, i, i, ` + miss + ` FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ` + strconv.Itoa(books) + `),
 k(j) AS (SELECT 0 UNION ALL SELECT j+1 FROM k WHERE j < 11)
INSERT INTO audio_files(id, book_id, path, idx, size, mtime_ns, quick_hash, updated_at)
SELECT printf('af_%08d_%02d', i, j), printf('bk_%08d', i), printf('/perf/b%08d/%02d.mp3', i, j), j,
  1000000 + (CASE WHEN i % 1000 = 1 THEN i - 1 ELSE i END) * 4099 + j * 17, 1, printf('%064x', (CASE WHEN i % 1000 = 1 THEN i - 1 ELSE i END) * 16 + j), 1 FROM n, k`,
	} {
		if _, err := tx.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPerfDuplicatesPage(t *testing.T) {
	books := perfBooks(t)
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Perf", "/perf")
	perfSeed(t, r, f, books, false)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	groups, err := r.Duplicates(ctx, adminAllScope)
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Duplicates at %d books x 12 files: %v, %d MB allocated in total, %d groups", books, took,
		(after.TotalAlloc-before.TotalAlloc)>>20, len(groups))
	if want := (books - 1) / 1000; len(groups) != want {
		t.Errorf("groups = %d, want %d", len(groups), want)
	}
	for _, g := range groups {
		if g.Tier != library.DupSameAudio || len(g.Books) != 2 || g.Books[0].Files != 12 || g.Books[0].Bytes == 0 {
			t.Fatalf("group = %+v", g)
		}
	}
}

func TestPerfHealthDuringOutage(t *testing.T) {
	books := perfBooks(t)
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Perf", "/perf")
	perfSeed(t, r, f, books, true)
	for _, g := range library.HealthGroups {
		start := time.Now()
		rows, n, err := r.HealthPage(ctx, adminAllScope, g, 0, 5)
		t.Logf("health %-18s %v (count %d, rows %d)", g, time.Since(start), n, len(rows))
		if err != nil {
			t.Fatal(err)
		}
		if g == library.HealthMissing && n != books {
			t.Errorf("missing = %d, want %d", n, books)
		}
	}
	start := time.Now()
	total, err := r.HealthTotal(ctx, adminAllScope)
	t.Logf("health total (count only) %v = %d", time.Since(start), total)
	if err != nil || total != books {
		t.Errorf("total = %d, %v, want %d", total, err, books)
	}
}
