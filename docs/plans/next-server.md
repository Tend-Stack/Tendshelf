# Next server and listening packages (plan, 2026-11-06)

From [next-stage.md](next-stage.md): L3, S3, S4, S5, S6, L5, L6 (timeline only), R1. Checked against the code at
`85f6676`, with
`0025_watch_folders` about to land. The operator pre-approved recommendations on 2026-11-05. Each package is a builder's
contract; a gap is reported as `PLAN GAP: <one line>`. Build order, waves and shared files are in section 1.

## 1. Order, size, parallelism

| Wave | Package | Size (tool calls) | Needs | Migration | New JS | Test prefix, helpers, captures |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | **L3a** Chapter editor | M, 180 | | none | 0 B | `TestChapterEdit`, `ched`, `chapters-edit-*` |
| 1 | **S4** Duplicates and merge | M, 220 | | `0026_book_merges` | 0 B | `TestDup`, `dup`, `duplicates-*` |
| 1 | **S5** App keys and OPDS | M, 220 | | `0027_app_keys` | 0 B | `TestAppKey`, `TestOPDS`, `akey`, `opds`, `app-keys-*` |
| 2 | **L3b** Chapter lookup | S, 90 | L3a | none | 0 B | `TestChapterLookup`, `chlk`, `chapters-lookup-*` |
| 2 | **S3** Library health | S, 110 | L3a | none | 0 B | `TestHealth`, `hlth`, `health-*` |
| 2 | **S6** KOReader sync | M, 200 | S5 | `0028_kosync` | 0 B | `TestKosync`, `TestPartialMD5`, `kos` |
| 3 | **L5** Clips | M, 150 | L3a (`parseClock`) | `0029_clips` | `clip.js` | `TestClip`, `clip`, `clips-*` |
| 3 | **L6** Listening timeline | S, 80 | | `0030_listening_recent` | 0 B | `TestTimeline`, `tl`, `timeline-*` |
| 3 | **R1** All my highlights | S, 90 | S5 (filter) | none | 0 B | `TestAllHighlights`, `ahl`, `highlights-*` |

At most three builders at once, one worktree each; merge in table order and gate main after each merge. Go first, English
copy in the same package (the app is English only, so no translation package). Sections below follow the requested
order; the waves keep that order wherever a dependency or the limit of three allows.

**Migrations.** `loadMigrations` demands 1..n with no gaps and `db.SchemaVersion` must equal the count, so a number is
decided at
merge time. The stems above never change. If a number is taken: `git mv` the `.sql` and its test to the next free number, fix
the header comment, set `SchemaVersion` to the count. Name test functions by stem (`TestMigrationAppKeys`), never by number.
Each migration has `internal/db/migration_NNNN_test.go` in the 0024 pattern: from empty, and from the previous version
with rows.

**Shared files, one small edit each**: `internal/web/server.go` (a register line), `internal/db/db.go` (`SchemaVersion`),
`docs/roadmap.md` (a status block), `docs/copy-additions.md` (words not in the deck). S3 and S4 both add a link in
`routes_admin.go` and `pages/admin_libraries.templ`, and rules in `css/pages-admin.css`. L5 and S4 both touch `docs/api.md`.
Everything else is new files named by package.

**Budgets.** App JS ceiling `jsTotalGzip` is 90,624 B, measured 90,489 B, so 135 B are free. Only L5 adds JS, see there.
The other
eight packages add 0 B and are server-rendered. New CSS goes into page stylesheets, never `bundle.css`, so no page already in
`weightBudgets` grows except by `pages-admin.css` on admin pages. Report the numbers; never squeeze to fit.

**Android** ([android.md](android.md)): A1 stays last and needs **S4** (merged ids on the API) and **L5** (bookmark
fields), the
only `/api/v1` changes here; both are additive. L3 changes rows, not the `chapters` shape, so the app inherits edits
through the
`GET /books/{id}` ETag. S5 rebuilds `devices` once; A1's `mobile` handoff needs no second rebuild. S5's and S6's routes
are not
`/api/v1` and the app never calls them. L6 and R1 add no API; A9 decides that later. S7 (OIDC) and L4 are not in this plan.

**Every package**: no new Go module; templ regenerated and committed; plain words, `ts-` classes, four themes, 44 px
targets; no
board exists for any page here, so follow `BRAND.md`, list new words in `docs/copy-additions.md`, capture at 390, 768,
1024 and 1440
with the opt-in helper. Forbidden-role cases in every handler test. Gates: package tests with `-race` while working, then
`make
check` and `make build` before the last commit; touched browser specs with `TENDSHELF_TEST_BROWSER`. Update
`docs/roadmap.md`.

## 2. L3a Chapter editor

Reused: `chapters` (its `source` CHECK already allows `user` and `online`; nothing writes them today, so no backfill),
`Tx.ReplaceChapters`, `share.EditBook`, `data-ts-play-at`, `answerPage`, `clockText`, `db.Audit`, the write job. Extended:
`finalize`,
`Writes.Preflight`. New: one page. Replaced: nothing. No migration.

### Invariants
1. An edit changes database rows only. No code here opens a library file; the file changes only through "Write details
   into the file".
2. Once any row of a book has source `user` or `online`, no scan changes that book's chapters. `Reset` is the only way back.
3. A scan never destroys an edit, even when files vanish or shrink, so it never clamps overrides. A list that no longer
   fits is
   reported (S3) and refused by the write job.
4. A stored list is always whole: first start 0, starts strictly rising, each end the next start, last end the duration.
5. Save is all or nothing and refused when the rows changed since the form was drawn.

### Decisions
- Overrides are the rows themselves, not a second table: every reader of `chapters` (player, API, bookmarks) already sees
  them.
- No clamping in `finalize`: a mount hiccup drops files from the timeline, and clamping then would lose the edit for good.
- The form holds starts and titles only; ends are derived, so gaps and overlaps cannot exist.
- Add, delete and shift round-trip through the server as an unsaved draft: no JS, one validator, and a boosted form keeps
  the player.
- A page, not a sheet: 100 rows do not fit a sheet.
- A draft answer is 200 with the error inline, so it is safe to swap and works as a plain POST.
- Multi-file and FLAC books keep the edit in the database only (`writes.go` already skips those); the page says so.

### Steps
1. `internal/scan/finalize.go`: after `old` is read, `if library.ChaptersOverridden(old) { return true, nil }`, before
   `bookChapters`.
   Duration and refused reason still update above it. Update the function comment.
2. `internal/library/chapters.go` (new): `ChaptersOverridden([]Chapter) bool`; `ChapterDraft{StartMs int64; Title string}`;
   `BuildChapters(rows []ChapterDraft, durationMs int64, source string) ([]Chapter, *ChapterError)`, with
   `ChapterError{Row int, Code
   string}`; codes `empty`, `too_many` (over 1000), `first_not_zero`, `out_of_order` (start not above the previous),
   `past_end`,
   `no_title`, `title_long` (over 500 runes after trimming; control characters dropped); `ChaptersRev([]Chapter) string`
   (first 16 hex
   of SHA-256 over idx, start, end, title, source); `ChaptersPastEnd` SQL fragment for alias `b`: a `user` or `online` row
   with
   `end_ms > b.duration_ms + 1000`.
3. `internal/scan/reset.go` (new): `ResetChapters(ctx, repo, bookID) error`. In `repo.Update`: `ReplaceChapters(bookID,
   nil)`, then
   `finalize`; `settled == false` returns `ErrChaptersBusy` and rolls back, so a book with files still being read keeps
   its list.
4. `internal/mediajobs/writes.go`: code `chapter_fit` with the text "The chapter list runs past the end of this book. Fix
   it on the
   Chapters page first."; `Preflight` adds it once when the book has one file, the format is not FLAC, and the last row
   ends more than 1 s past
   `book.DurationMs`.
5. `internal/web/routes_chapters.go` (new), registered from `registerBook`. Loads through `s.loadBook(...,
   share.EditBook)`; 404 unless
   `kind = audio`, listable, duration above 0.
   - `GET /books/{id}/chapters`: the page; `?saved=1` shows a status line.
   - `POST /books/{id}/chapters` with `rev`, `origin` (empty or `online`), `n`, `start_{i}`, `title_{i}`, `shift_by`,
     `shift_from`,
     and the submitter `op`: `save`; `add:{i}` (insert after row i, start the midpoint to the next start or the end, title
     empty);
     `del:{i}`; `shift`. Body capped at 1 MiB with `http.MaxBytesReader`; `n` above 1000 refused. `shift` moves rows from
     `shift_from`
     (default 1) by signed seconds, all or none; a row that would break order is an inline error.
   - On `save`: in `repo.Update`, recompute `ChaptersRev` and compare; a mismatch re-renders the draft with "These
     chapters changed
     while you were editing. Reload the page to see them." Then `BuildChapters` and `ReplaceChapters` with source `online`
     when
     `origin=online`, else `user`. Audit `chapters.edit` with counts only. `answerPage` to `?saved=1`.
   - `POST /books/{id}/chapters/reset`: `scan.ResetChapters`, audit `chapters.reset`; `ErrChaptersBusy` shows "Some files
     are still being read. Try again in a minute."
   - `parseClock(string) (int64, bool)` takes `h:mm:ss`, `m:ss`, and either with `.mmm`; `clockMsText` prints milliseconds
     only when not zero.
6. `internal/web/chapters_copy.go` (new), `internal/web/pages/book_chapters.templ` (new, `ChaptersEditView`),
   `css/pages-chapters.css` (new, a
   `TestCSSBudget` row of 2 KB). Each row: start field, title field, Listen (`data-ts-play-at`, `data-ts-book`), Delete,
   Add below.
   Bottom: Save, Back, a disclosure "Move chapters" with `shift_by` and `shift_from`, and Reset (only when overridden,
   with a confirm
   page in the same pattern as `getPersonDisable`). A line appears when the book has several files or is FLAC: "These
   chapters stay in
   Tendshelf. They are not written into the audio files of this book."
7. `internal/web/book_views.go` and the chapter list on the book page: an "Edit chapters" link only with `EditBook`.
8. `docs/architecture.md`: a paragraph on overrides, naming invariants 2 and 3.

### Tests
- `internal/library/chapters_test.go` table: every code, ends derived, one row, 1000 rows, 1001 rows.
- `internal/scan/chapters_override_test.go`: a rescan keeps `user` and `online` rows; a changed file, a missing file and a
  shorter file
  keep them verbatim; `ResetChapters` restores the file list; unsettled returns `ErrChaptersBusy` with rows intact. Assert
  no library file changes.
- `internal/mediajobs/writes_test.go` additions: `chapter_fit` refusal; after an edit the single-file write carries the
  edited titles
  (fake ffmpeg) and a rescan keeps source `user`.
- `internal/web/chapters_test.go`: each role (owner, admin, member, guest, anonymous, share link) for all routes; text
  book 404; add, delete,
  shift round trips with nothing stored; save stores; stale `rev`; bad clock; missing CSRF; a boosted request gets
  `#main`, no 5xx body.

Out of scope: per-person chapters, waveform dragging, undo beyond Reset, writing chapters into multi-file books.

## 3. L3b Chapter lookup

Reused: `online.Client.getJSON` (cache, pacing, `apiHosts` already has `api.audnex.us`), `audnexusRegions`,
`online_usage`, the jobs runner,
the lookup job's poll pattern (`getLookupJob`), `lookupsOn`, L3a's draft path. New: one client method, one job kind.

Checked live on 2026-11-06: `GET https://api.audnex.us/books/{asin}/chapters` answers `{asin, brandIntroDurationMs,
brandOutroDurationMs, chapters: [{lengthMs, startOffsetMs, startOffsetSec, title}], isAccurate, region, runtimeLengthMs}`;
an ASIN not sold in the
region answers 404 with `REGION_UNAVAILABLE`, which `getJSON` turns into `null`.

### Invariants
1. A lookup happens only when a person presses the button; automatic lookups never fetch chapters.
2. A result only fills the draft. Nothing is stored until the person presses Save, through L3a's validator.
3. The rows come from the job's stored result, never from the form.

### Decisions
- A job, because long work never blocks a request and a miss may ask three stores.
- Offer "titles and times" and, when counts match, "titles only": Audible's timing may not match a person's rip, while its
  titles usually do.
- No scaling of times: a wrong guess looks right. The notice shows the difference and "Move chapters" is there.

### Steps
1. `internal/metadata/online/chapters.go` (new): `ChapterList{ASIN, Region string; Accurate bool; RuntimeMs, IntroMs,
   OutroMs int64; Chapters []ChapterItem}`,
   `ChapterItem{StartMs, LengthMs int64; Title string}`, `(*Client).Chapters(ctx, asin, language string) (ChapterList,
   error)`. Use the same store
   order as `searchAudnexus` (factor out `regionOrder(language)`), at most 3 stores; `null` tries the next; all `null` is
   `&Error{Code: "no_chapters"}`,
   added to `Text()` as "Audnexus has no chapters for this book."
2. `internal/metadata/testdata` or `online/testdata`: synthetic fixture `audnexus_chapters.json`.
3. `internal/metadata/chapters.go` (new): `KindChapterLookup = "chapter_lookup"`, payload `{book_id, asin}`, handler
   storing the `ChapterList`
   as the job result and adding usage as `handleLookup` does; `StartChapterLookup(ctx, bookID, asin, userID) (jobID,
   error)`, reusing an active job
   for the same book; `ChapterLookupFor(ctx, jobID, bookID)`, which rejects another book's job.
4. `internal/web/routes_chapters.go`: `POST /books/{id}/chapters/lookup` with `asin` (upper-cased; must match
   `^[A-Z0-9]{10}$`, else
   "That does not look like an ASIN."); 404 when `lookupsOn` is false. Redirect to `?lookup={job}`. The poll goes through
   `/hx/jobs/{id}`; add the case
   beside `getLookupJob`.
5. `op` gains `lookup_all` and `lookup_titles` with `job`. `lookup_all` replaces the draft: rows starting at or past the
   duration are dropped and counted,
   `origin=online`. `lookup_titles` keeps the draft's starts and takes the titles, only when counts match; `origin` stays
   as it was. Titles
   trimmed and cut to 500 runes.
6. `pages/book_chapters.templ`: an ASIN form (prefilled from `books.asin`) in the disclosure "Look up chapters"; the
   result notice: count, Audible's
   length against this book's, the two buttons, a warning when `isAccurate` is false or the lengths differ by more than 5 s.

### Tests
`internal/metadata/online/chapters_test.go` (fixture; a 404 store then a hit; all 404; non-OK; cache hit sends nothing).
`internal/metadata/chapters_test.go` (job
result, reuse, wrong book, online off). `internal/web/chapters_lookup_test.go` (roles, bad ASIN, draft not stored, drop
past end, titles only with unequal
counts refused, forged `job` of another book 404, save stores `online`).

Out of scope: bulk lookup, automatic lookup, other providers, removing Audible's brand intro by itself (Move chapters does
that).

## 4. S3 Library health

Reused: `Repo.Problems` with its scope and counts, `scopeWhere`, `problemCards`, `allowLibraries`, `book_lookups`,
`/admin/libraries/{id}/lookups`. Extended: `Problems` stays for the setup summary and the card; a new reader shares its
SQL. New: one page. No migration, no job: every group is one indexed-enough `count` plus a page of rows.

### Invariants
1. Reads only; every group respects `sc`, and the page needs `ManageLibraries`.
2. A missing book counts only as missing, as today; a refused one only as refused, so counts do not double within those two.
3. Every row has a fix link or says plainly that there is none.

### Decision
Offset paging per group (`?group=&page=`, 50): an admin page, rarely deep. The card on `/admin/libraries` stays and links
here.

### Steps
1. `internal/library/health.go` (new): `HealthGroup` constants `missing`, `refused`, `unreadable` (a live file with
   `probe_error`), `no_chapters` (audio, `Listable`, settled, no `chapters` row), `no_cover` (the existing condition),
   `no_match` (`book_lookups.outcome IN ('no_match','needs_look','failed')`), `chapters_past_end`
   (`library.ChaptersPastEnd`). `Health(ctx, sc, perGroup) ([]HealthCount with first rows, error)`, `HealthPage(ctx, sc,
   group, offset, limit)`. A book in several of the last five shows in each. `Problems` calls shared SQL pieces where it
   can; its behaviour stays.
2. `internal/web/routes_health.go`, `health_copy.go`, `pages/admin_health.templ` (new): `GET /admin/health`. Unknown group
   404. Fix links: `no_chapters` and `chapters_past_end` to `/books/{id}/chapters`; `no_cover` and `refused` to the book;
   `no_match` to the library's lookups page; `missing` to the folder's emptied page when the whole folder went, else the
   book; `unreadable` says "Tendshelf could not read this file. Check it, then scan again."
3. `routes_admin.go`, `pages/admin_libraries.templ`: a link "Library health" beside `EbooksHref`. `css/pages-admin.css`:
   list rows.

### Tests
`internal/library/health_test.go`: each group, scope, counts against a `Repo.Problems` agreement check, text books absent
from `no_chapters`, unsettled books absent. `internal/web/health_test.go`: roles, unknown group, each fix link, paging.
Add a 100,000-file perf check only if `perf_pages_test` runs this page under budget at the seeded size; report the number.

Out of scope: fixing from the page, ebook problems (`/admin/ebooks` has them), scheduled reports.

## 5. S4 Duplicates and merge

Reused: `audio_files` (`size`, `quick_hash`), `textmatch.Norm`, `merged_books` and `MergedInto`, `redirectMerged`,
`mergeBook`'s statements and `bindFor`, `db.Audit`, `allowLibraries`. Extended: `scan.go`, `progress` writes. New:
`merged_folders`, `book_not_duplicates`, one page. No file is opened.

### Invariants
1. No library file is touched; the dropped copy's folder stays on disk, and the page says so.
2. No person's progress, bookmark, highlight, shelf item or note is lost: each row moves, or a newer row for the same
   person wins.
3. A scan never brings a merged folder back while its merge stands, and never treats its files as "moved" files of another
   book.
4. Same library only, so nobody gains a title their library access did not already reach, beyond a grant on the dropped
   copy, which the confirm page counts.
5. A merge is one transaction, refused while either book has an open file write.
6. A client that still holds the old id lands on the kept book, and only if allowed; otherwise 404 as today.
7. Every table with a foreign key to `books` is handled on purpose; a new table fails a test until it is.

### Decisions
- Delete the dropped row and remember its folder, rather than hide the row: `Listable` is not the only filter, so hiding
  would need a change in dozens of queries. One check in the scan holds this.
- Merge is the owner's act, on one pair at a time, never automatic: two matching titles can be different recordings.
- Detection at page load, no job: one grouped SQL and one Go pass over a library's titles.
- "Same details" needs title, first author and first narrator all present and equal after `textmatch.Norm`, and lengths
  within 60 s or 2 %: a different edition has another length.
- Positions move unscaled: same audio is exact, and "same details" is close enough; `progress` is capped at the kept
  book's length. The page warns on this tier.
- **Undo** ("Show that copy again") removes the `merged_folders` row and scans: the folder returns as a new book. Moved
  data stays on the kept book, and the confirm page says so: that one-way move is the only irreversible part, and it is
  the owner's choice per pair.

### Steps
1. `internal/db/migrations/0026_book_merges.sql`:
   ```sql
   CREATE TABLE merged_folders (root_path TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
     from_id TEXT NOT NULL, to_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
     merged_by TEXT REFERENCES users(id) ON DELETE SET NULL, merged_at INTEGER NOT NULL CHECK (merged_at > 0)) STRICT, WITHOUT ROWID;
   CREATE INDEX merged_folders_to ON merged_folders(to_id);
   CREATE TABLE book_not_duplicates (a_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, b_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
     created_at INTEGER NOT NULL, CHECK (a_id < b_id), PRIMARY KEY (a_id, b_id)) STRICT, WITHOUT ROWID;
   CREATE INDEX book_not_duplicates_b ON book_not_duplicates(b_id);
   ```
   If the kept book is deleted, the cascade frees the folder and the next scan adds it again: the safe direction.
2. `internal/library/merge_rows.go` (new): `MergeRowsSQL []string`, moved verbatim from `ebook/merge.go`: reading
   positions, reading sessions, highlights, bookmarks, word list, shelf items, text choices, `merged_books`.
   `ebook/merge.go` uses it; `merge_test.go` proves no change.
3. `internal/library/duplicates.go` (new): `DuplicateGroup{Tier, Books []DuplicateBook}` with title, root path, files,
   bytes, duration, counts of people with progress and bookmarks. `Duplicates(ctx, sc)`: `same_audio` = same sorted
   `size:quick_hash` list of live files (`group_concat` in a subquery, kinds `audio`, `Listable`); `same_details` as
   decided, excluding pairs already in `same_audio` and pairs in `book_not_duplicates`. `MergePreview(ctx, keep, drop)`:
   the counts for the page, and the grants and links that would move.
4. `internal/library/merge_books.go` (new): `MergeBooks(ctx, keep, drop, userID) error`, errors `ErrMergeLibrary`,
   `ErrMergeBusy`, `ErrMergeSame`, `ErrBookNotFound`. In one `WriteTx`: both `kind='audio'`, keep `Listable`, same
   library, no `file_writes` row in an open state for either. Then, in order:
   `progress` upsert with `position_ms = MIN(position_ms, :dur)`, newest `client_updated_at` wins (the shape of the
   reading upsert); `listening_sessions`; `UPDATE OR IGNORE queue_items`; `UPDATE OR IGNORE book_settings`; `UPDATE OR
   IGNORE grants`; `UPDATE share_links`; `ebook_files` as `mergeBook` does; `UPDATE OR IGNORE ebook_not_same`;
   `MergeRowsSQL`; `UPDATE merged_folders SET to_id=:to WHERE to_id=:from`; `INSERT merged_folders` with the dropped
   `root_path`; `DELETE FROM books WHERE id=:from AND kind='audio'`. If keep has no cover and drop has: `UPDATE covers SET
   book_id=:to` for that cover and set keep's `cover_id`, before the delete. Audit `book.merge` with counts. Afterwards
   rows left by `OR IGNORE` go with the cascade.
5. `internal/scan/scan.go`: load `merged_folders` for the library in the sweep; immediately after grouping and before
   hashing or moved-file matching, drop any group whose `Root` is listed and count `res.BooksMerged`. S1's watcher only
   enqueues scans (`scan/jobs.go` on its branch; verify on rebase, `PLAN GAP` if not).
6. Resolve old ids: `access.go`'s `redirectMerged` also answers `GET /api/v1/books/{id}` (308 to the kept id, under the
   same right). `internal/progress`: move `resolveBook` use into `Save` and `insertBookmark`. `routes_progress.go`,
   `putAPIProgress`, `postAPIProgressBatch`, `postAPIBookmark`: `id = s.lib.MergedInto` before `loadBook`, so an offline
   outbox lands on the kept book, under the kept book's right. Fix the "text book" wording in those comments.
7. `internal/web/routes_duplicates.go`, `duplicates_copy.go`, `pages/admin_duplicates.templ` (new), all `allowLibraries`:
   `GET /admin/duplicates`; `GET /admin/duplicates/merge?keep=&drop=` (confirm with preview; states what moves, who gains
   access, that files stay, that it cannot be undone beyond showing the copy again); `POST /admin/duplicates/merge`; `POST
   /admin/duplicates/not-same` (`a`, `b`); `GET /admin/duplicates/merged` list with `POST
   /admin/duplicates/merged/restore` (`root_path`, then `startScan`). `routes_admin.go`, `admin_libraries.templ`: the
   link. Error words: `ErrMergeBusy` "This book is being written to. Try again when that has finished."
8. `docs/api.md`: merged ids. `docs/architecture.md`: merge section with the table list.

### Tests
`internal/library/merge_books_test.go`: `TestDupEveryBookTableHandled` reads `pragma_foreign_key_list` for all tables and
fails on a table with a foreign key to `books` that is not in an explicit moved-or-cascades map; per-table moves; newest
wins both ways; the cap; a person with data on both; two merges in a chain; refused pairs.
`internal/library/duplicates_test.go`: tiers, multi-file order independence, `not_same`, missing length or narrator not
matched, scope. `internal/scan/merged_test.go`: rescan keeps the folder out, identical files do not count as moved,
restore returns it, cascade frees it. No library file changes, by hash. `internal/web/duplicates_test.go`: roles, old id
on page and API, offline batch and bookmark on an old id, no right 404. `internal/db/migration_0026_test.go`.

Out of scope: deleting the duplicate's files, cross-library merges, text-only duplicates, multi-way merge in one step, a
moved folder (it reappears, and shows on the page again).

## 6. S5 App keys and OPDS

Reused: `devices` (hash, name, last seen, revoke, idle expiry, the limit of 50), `/me/devices` and its sign-out,
`auth.WithDevice`, `s.apiLimiter`,
`auth.NewLimiter`, `share.Can`, `getEbookFile`, `serveCoverFile`, `library.SearchTerms` and `searchFrom`. Extended:
`devices.kind`, `share.Checker`. New: `/opds`.
Replaced: nothing. No parallel token table.

### Threat model and invariants
1. A key is never the login password, and the login password never opens `/opds` or `/kosync`.
2. A key opens one surface: an `opds` key only `/opds/`, a `koreader` key only `/kosync/`. Neither passes `bearer`,
   `/api/v1`, or a page.
3. Only the hash is stored; the key is shown once, in the answer to the POST, never in a URL or a log.
4. A revoked key, a disabled person or an idle key (180 days, the device rule) stops at the next request.
5. Every feed filters by grants in SQL, and that filter agrees with `share.Can(DownloadText)`.
6. Only ready files are listed or served: refused, copy-protected, failed, pending and missing never.
7. OPDS is GET only. It is outside the cookie chain, so a session cookie grants nothing there and CSRF does not apply.
8. Failed attempts are limited by client address.

### Decisions
- `devices` gains kinds, not a table: same life cycle, one place to see and revoke.
- **Keys are 24 characters, 120 random bits, alphabet `a-z2-7`**, not the 32-byte rule: they are typed on e-ink keyboards,
  KOReader hashes exactly what is typed so
  no separators are possible, and they reach only two rate-limited read-mostly surfaces. New decision file and one line in
  the `AGENTS.md` security rule. Reversible: only new keys change.
- Mounted on `outer` beside `/healthz`, not behind `guarded`: no gate, no redirect to `/login`, no cookie.
- Basic password is the key; the user name must match too, so a leaked log line of one is not enough.
- OPDS 1.2 Atom through `encoding/xml`, offset paging at 50: clients expect `rel=next`, and the feed is bounded.
- Audio is not listed: OPDS is for reading apps. Keys do not expire by a date; revocation and idle expiry cover it.

### Steps
1. `internal/db/migrations/0027_app_keys.sql`: rebuild `devices`. Nothing has a foreign key to it (checked).
   ```sql
   CREATE TABLE devices_new (<same columns>, kind TEXT NOT NULL CHECK (kind IN ('web','pwa','desktop','mobile','opds','koreader')), ...) STRICT;
   INSERT INTO devices_new SELECT * FROM devices; DROP TABLE devices; ALTER TABLE devices_new RENAME TO devices;
   CREATE INDEX devices_user ON devices(user_id);
   ```
2. `internal/token/typable.go` (new): `NewTypable() string` (24 chars from `crypto/rand`).
3. `internal/auth/devices.go`: `KindOPDS`, `KindKOReader`, labels "Book app", "KOReader". `ParseDeviceKind` stays at the
   four, so `POST /api/v1/auth/token`
   cannot mint a key. `Lookup` adds `AND d.kind NOT IN ('opds','koreader')`. `internal/auth/appkeys.go` (new):
   `IssueKey(ctx, tx, userID, name, kind) (secret
   string, Device, error)`, sharing `Issue`'s pruning and limit; `KeyHash(kind, secret) [32]byte`, SHA-256 of the secret
   for `opds`, SHA-256 of `hex(md5(secret))` for
   `koreader`; `LookupKey(ctx, kind, hash) (Device, users.User, error)`, updating `last_seen_at` as `Lookup` does.
   `OwnKeys(ctx, userID)`. Device lists used by
   `groupDevices` exclude the two kinds, so keys are never folded.
4. `internal/share/share.go`: `BookFilterFor(u, alias, a Action) Filter` for `ViewBook`, `ReadText`, `DownloadText`, using
   one `EXISTS` per right to match
   `canByGrant`, which takes the maximum across grants. Owner and admin as `filter`.
5. `internal/web/routes_appkeys.go` (new): `POST /me/app-keys` (`name`, `kind` of `opds` or `koreader`; 409 text at the
   limit) answers 200 with `Cache-Control:
   no-store`, the key, and the setup words: server address (`scheme + r.Host` as `routes_share.go`) plus `/opds` or
   `/kosync`, user name, key. Audit `key.create`
   without the secret. Revoke is the existing `POST /me/devices/{did}/signout`. `pages/devices.templ` gains a section "App
   keys"; `appkeys_copy.go` (new).
6. `internal/web/appauth.go` (new): `s.opdsAuth(next)`: read Basic; `keyLimiter` (10 failures in 15 minutes, keyed like
   the login limiter); `KeyHash`, `LookupKey`,
   user name compared with `strings.EqualFold`; failure answers 401 with `WWW-Authenticate: Basic realm="Tendshelf",
   charset="UTF-8"`, or 429; success passes
   `s.apiLimiter` and calls next with `auth.WithDevice`.
7. `internal/library/catalogue.go` (new): `TextCatalogue(ctx, sc Scope, q CatalogueQuery{Terms []string; Sort string;
   Offset, Limit int}) ([]CatalogueBook, total int, error)`:
   books that are `Listable` and have a ready, unmissing, unrefused ebook file, `sc.Books` in the SQL, with the files in
   one batched query.
8. `internal/web/routes_opds.go`, `opds_views.go` (new), mounted with `outer.Handle("/opds/", ...)` and `outer.Handle("GET
   /opds", ...)` on an own mux:
   `GET /opds` (navigation: All books, Newest, search link), `/opds/all?page=`, `/opds/new?page=`,
   `/opds/search?q=&page=`, `/opds/search.xml` (OpenSearch),
   `/opds/books/{id}/file/{fileID}`, `/opds/covers/{id}`. Scope: `s.scope(r)` with `Books` from `BookFilterFor(u, "b",
   DownloadText)`. Entry id
   `urn:tendshelf:book:{id}`, title, authors, `updated`, summary, one `http://opds-spec.org/acquisition` link per file
   with `ebookMime`, `http://opds-spec.org/image`.
   Content types `application/atom+xml;profile=opds-catalog;kind=navigation|acquisition;charset=utf-8`; `Cache-Control:
   no-store`. Split the body of `getEbookFile`
   into `serveEbookFile`; the OPDS route checks `mayDownloadText`.
9. `docs/decisions/00NN-app-keys.md` (next free), `AGENTS.md` one line, `docs/api.md` a section, `docs/architecture.md`
   routes.

### Tests
`internal/auth/appkeys_test.go`: hash per kind, the kinds do not cross, `Lookup` refuses a key, disabled, revoked, idle.
`internal/share/filterfor_test.go`: for a table of grants,
filter agrees with `Can`. `internal/db/migration_0027_test.go`: rows and hashes survive, an old kind and a new kind
insert, a bad kind fails.
`internal/web/opds_test.go`: 401 without credentials, the login password 401, wrong user with a right key 401, a koreader
key 401, a bearer token and a session cookie alone 401, limiter 429,
a key on `/me` and `/api/v1/me` refused; member sees only granted books, `can_download=0` or `can_read=0` absent and
download 404; a refused file absent; paging; XML parses; search escapes; `sw.js` has no `/opds` rule.
`internal/web/appkeys_test.go`: shown once, no-store, not in the audit detail, revoke stops it.

Out of scope: OPDS 2.0, OPDS-PSE, audiobooks in feeds, per-shelf feeds, key scopes beyond the two kinds.

## 7. S6 KOReader sync

Reused: S5 keys, `progress.SaveReading` and its newest-wins rule, `Doc.AnchorAtChar`, `ebook.ReadDoc`, `Service.FilePath`,
`EnqueuePending`. New: `/kosync`, one column, one table.

Protocol, read from KOReader's `kosync.koplugin` on 2026-11-06: headers `x-auth-user` and `x-auth-key`, where the key is
`md5(password)`, and `Accept:
application/vnd.koreader.v1+json`. `POST /users/create` expects 201 or 402; `GET /users/auth` 200 or 401; `PUT
/syncs/progress` with `document, progress, percentage,
device, device_id` expects 200, 202 or 401; `GET /syncs/progress/{document}` 200 or 401. `document` is `util.partialMD5`:
MD5 over 1024-byte samples at offsets 0, then `1024 << 2i` for
i = 0..10, stopping at the first read past the end. `timestamp` is in seconds. KOReader calls
`GotoPage(tonumber(progress))` for paged documents and `GotoXPointer(progress)` otherwise.

### Invariants
1. The key on the wire is `md5(secret)`. The secret is a per-device S5 key, so MD5 protects nothing but also exposes
   nothing: never the login password.
2. An unknown document is stored for that person and touches nothing else.
3. A position reaches `reading_positions` only when `share.Can(ReadText)` passes for the matched book; otherwise it stays
   in the KOReader row.
4. Nothing here reads book text into a response. `progress` is stored as opaque text, bounded.
5. Storage is bounded: 10,000 rows a person, oldest dropped.
6. `/users/create` never creates anyone.

### Decisions
- A row per (person, document) as well as `reading_positions`: it gives KOReader back its exact XPointer, which Tendshelf
  cannot compute.
- Inbound by percentage through `AnchorAtChar`: all that converts; the sync lands near the place (the known limit in
  next-stage).
- Outbound when Tendshelf is newer: PDF by page; EPUB as `/body/DocFragment[N]/body`, the chapter's start, N being
  `Chapter.Idx`. **Guess**: this assumes spine items and chapters line up (skipped
  non-linear items shift it) and that KOReader accepts this short pointer. It lands at or before the place, and a bad
  pointer leaves the page alone. Manual check below. Other formats answer the last KOReader row or `{}`.
- Binary digests only. KOReader's file-name method is a setting people can change back, and a file name is not unique.
- Digest computed in the ingest job plus a backfill job, never during a request.
- `/kosync/` runs ahead of `outer`, because the mux redirects unclean paths and a server address typed with a trailing
  slash sends `//users/auth`; a redirected PUT would fail.

### Steps
1. `internal/db/migrations/0028_kosync.sql`:
   ```sql
   ALTER TABLE ebook_files ADD COLUMN partial_md5 TEXT CHECK (partial_md5 IS NULL OR length(partial_md5) = 32);
   CREATE INDEX ebook_files_partial_md5 ON ebook_files(partial_md5) WHERE partial_md5 IS NOT NULL;
   CREATE TABLE kosync_progress (
     user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
     document   TEXT NOT NULL CHECK (length(document) = 32),
     progress   TEXT NOT NULL CHECK (length(progress) <= 512),
     percentage REAL NOT NULL CHECK (percentage >= 0 AND percentage <= 1),
     device     TEXT NOT NULL CHECK (length(device) <= 64),
     device_id  TEXT NOT NULL CHECK (length(device_id) <= 64),
     updated_at INTEGER NOT NULL CHECK (updated_at > 0),
     PRIMARY KEY (user_id, document)) STRICT, WITHOUT ROWID;
   CREATE INDEX kosync_progress_recent ON kosync_progress(user_id, updated_at);
   ```
2. `internal/ebook/partialmd5.go` (new): `PartialMD5(path string) (string, error)`. Set in the ingest job (`job.go`) when
   a file becomes ready; `reconcile.go`'s changed-file `UPDATE` sets it NULL.
   `KindDigests = "ebook_digests"` handles up to 200 NULL ready rows, re-enqueues while any remain; enqueued beside
   `EnqueuePending` at start-up. Reads only. A read failure leaves NULL and logs.
3. `internal/ebook/doc.go`: `(*Doc).TotalUnits() int`, the sum `AnchorAtChar` walks.
4. `internal/progress/kosync.go` (new): `KosyncPut(ctx, userID, KosyncRow)` (upsert, then prune over the cap),
   `KosyncGet(ctx, userID, document)`.
5. `internal/web/routes_kosync.go` (new): `s.kosyncFront(next)` wrapped inside `SecurityHeaders`: a path starting
   `/kosync/` has that prefix and repeated slashes removed and is dispatched by method and path; all else goes to `outer`.
   - Auth: `x-auth-user`, `x-auth-key` lowercased; `KeyHash` is the SHA-256 of that hex; `LookupKey(koreader)`,
     `EqualFold` on the user name; `keyLimiter`; `s.apiLimiter`. Failure is 401 `{"code":2001,"message":"Unauthorized"}`.
   - `POST /users/create`: always 402 `{"code":2002,"message":"Make a KOReader key in Tendshelf, then choose Login."}`, no
     auth.
   - `GET /users/auth`: 200 `{"authorized":"OK"}`.
   - `PUT /syncs/progress`: body at most 8 KiB. `document` `^[0-9a-f]{32}$` lowercased, finite `percentage` in 0..1,
     bounds as the table; else 400 `{"code":2003,"message":"Invalid fields"}`. Upsert. Then
     match: ready file with that digest, a `book_id`, `Can(ReadText)`, oldest first. With a match and a readable document:
     `pos = percentage * TotalUnits`, `AnchorAtChar`, `SaveReading` with `DeviceID "ko-" + first 16 hex of
     sha256(device_id)`,
     `DeviceLabel "KOReader"`, `ClientUpdatedAt` now, `PercentBP` rounded. A conversion failure is logged and still
     answers 200 `{"document":..., "timestamp":<seconds>}`.
   - `GET /syncs/progress/{document}`: the row; if a matched `reading_positions` row is newer than it and from another
     device, answer `percentage` from `percent_bp`, `device "Tendshelf"`, `device_id "tendshelf"`, `timestamp`, and
     `progress` as decided, the page being `max(1, round(bp/10000 * PageCount))`. Nothing known: 200 `{}`.
6. `routes_appkeys.go` copy: KOReader's steps ("Custom sync server", "Login", not "Register"). `docs/api.md`,
   `docs/manual-checks.md` row: on a real KOReader, log in, sync both ways on an EPUB and a PDF, including a
   Tendshelf-newer place.

### Tests
`internal/ebook/partialmd5_test.go` table: sizes 0, 1000, 1024, 1500, 5000, 2 MiB; an independent reference in the test
(the offset list spelled out). `internal/web/kosync_test.go`: 401 cases (no headers, wrong key, login password's MD5, an
opds key, wrong user, revoked),
limiter, 402 creates nobody, a trailing-slash path and PUT works, fields table, unknown document stored and
`reading_positions` untouched, a no-rights person's position not saved, inbound percent lands in the right chapter,
outbound EPUB and PDF, other format, no row `{}`, newest-wins both ways, the cap. `internal/db/migration_0028_test.go`.
Digest backfill test.

Out of scope: KOReader statistics, highlights sync, file-name digests, audio positions, a "link this file" page for
unmatched documents.

## 8. L5 Clips

Reused: `bookmarks`, the note sheet, `progress` sync feed, `data-ts-play-at`, `window.tsPlayer`, exports. Extended:
bookmark columns, `apiBookmark`, `PATCH`. New: `clip.js`. Replaced: `SetBookmarkNote` becomes a call to the new update.

### Invariants
1. `end_ms` is NULL or above `position_ms`, and only on a moment in the audio, never a place in the text.
2. Old clients keep working: `POST` and `PATCH` with only `note` behave as today, and unknown fields are ignored.
3. Every change bumps `updated_at`, so the sync feed carries it.
4. `player.js` and `player-full.js` gain 0 bytes; without `clip.js` a clip plays on past its end, nothing worse.
5. No audio is exported.

### Decisions
- A clip is a bookmark with an end, set on the note sheet: no new control in the player, whose budget is full.
- Kinds are a closed list; `bookmark` is the default so existing rows need no thought.
- `clip.js` is a page-only script placed inside `BookmarksPanel`, rendered only when a row is a clip: other pages stay at
  0 B. In the Now panel a clip jumps to its start, without the stop.

### Budget
`clip.js`: target 0.5 KB, row `"clip.js": {640, 4 * 1024}`. `jsTotalGzip` goes from `88*1024 + 512` (90,624 B) to `89*1024
+ 128` (91,264 B); if L4 or another package moved it first, add 640 B to whatever it is. Report the measured size. This
raise is the operator's to see in the report.

### Steps
1. `internal/db/migrations/0029_clips.sql`:
   ```sql
   ALTER TABLE bookmarks ADD COLUMN end_ms INTEGER CHECK (end_ms IS NULL OR end_ms > position_ms);
   ALTER TABLE bookmarks ADD COLUMN kind TEXT NOT NULL DEFAULT 'bookmark' CHECK (kind IN ('bookmark','favourite','quote','note','revisit'));
   ```
2. `internal/progress/bookmarks.go`: `Bookmark.EndMs int64` (0 none), `Kind string`; `BookmarkPatch{Note *string; EndMs
   *int64 (0 clears); Kind *string}`; `UpdateBookmark(ctx, userID, id, p)`; `InvalidError` fields `end_ms` and `kind`;
   `SetBookmarkNote` calls it. A text bookmark with `EndMs` is invalid. `AddBookmarkWithClientID` is unchanged; `POST`
   then `PATCH` makes a clip, one shape to retry.
3. `internal/web/routes_bookmarks.go`: `postBookmarkNote` also reads `kind` and `end_at` (`parseClock` from L3a). End past
   the duration is clamped; end at or before the start shows the sheet's error "The end must be after the start."
4. `internal/web/pages/book_bookmarks.templ`, `bookmark_views.go`, `book_copy.go`: on the sheet, kind radios (Bookmark,
   Favourite, Quote, Note, Revisit) and "Ends at" with a hint; a chip "Clips" and a chip per kind in use; the row shows
   kind and length; a clip's button reads "Play this clip" and carries `data-ts-clip-end`. Exports add kind and end.
5. `internal/web/static/js/clip.js` (new): one delegated click on `[data-ts-clip-end]` runs after `player.js` has started
   the book; arm `end`; on `#audio` `timeupdate`, when `tsPlayer.state().positionMs >= end`, `tsPlayer.pause()` and
   disarm; disarm on another book, or a seek before the start or past the end. Add the `motion_test.go` row and ceiling;
   if `build_test` lists precached files, add it.
6. `internal/web/routes_api_sync.go`: `apiBookmark` gains `kind` and `endMs` (`*int64`, null when absent); `POST` accepts
   both; `PATCH` accepts `note`, `endMs`, `kind`, any of them, and 400 with `field` as today when none is sent.
   `docs/api.md`.

### Tests
`internal/progress/clips_test.go`: CHECK, clear, text refused, `updated_at` bump, tombstones.
`internal/db/migration_0029_test.go`. `internal/web/clips_test.go`: sheet, clamp, other person's bookmark 404, roles, the
script present only with a clip. `internal/web/api_sync_test.go` additions: old body unchanged, feed carries the fields.
`internal/web/clip_browser_test.go`: plays and stops within 500 ms of the end; a seek disarms it.

Out of scope: audio export, sharing a clip, dragging on the waveform, a player button for "clip the last 30 s".

## 9. L6 Listening timeline

Reused: `listening_sessions`, `statsLocation`, `Scope`, `clockText`, `bookmarkWhere`, `data-ts-play-at`,
`decodeSyncCursor`'s cursor style, `listening_perf_test`. New: one index, one page. Night sleep from the same row of
next-stage is not here: it needs player JS and the budget, so it waits.

### Invariants
1. Only the signed-in person's sessions, joined through `sc.Books` in SQL, so a revoked book lists nothing.
2. Nothing in the request names a person; a `book` the person cannot see gives an empty list, the same as an unknown id.
3. No resume button: rows use the chapter list's seek control, so the one-resume-per-book rule holds.

### Steps
1. `internal/db/migrations/0030_listening_recent.sql`: `CREATE INDEX listening_sessions_recent ON
   listening_sessions(user_id, ended_at DESC, id);`
2. `internal/library/timeline.go` (new): `Timeline(ctx, sc, userID, bookID, cursor string, limit int) ([]TimelineRow, next
   string, error)`, keyset on `(ended_at, id)`, 50 rows; title and chapters batched per book on the page, chapter found in
   Go. A bad cursor is the first page.
3. `internal/web/routes_timeline.go`, `timeline_copy.go`, `pages/listening_history.templ` (new): `GET
   /me/listening/history?book=&cursor=`, registered from `registerListening`. Grouped by day in the person's zone; a row:
   book, started and ended clock times, "from 1:02:03 to 1:40:10, Chapter 7", "Go back here" at `end_ms`.
4. `pages/listening.templ` link "Where was I?"; `pages/book_bookmarks.templ` link "Listening history" with `?book=`.

### Tests
`internal/library/timeline_test.go` (keyset, scope, revoked, ties). `internal/web/timeline_test.go` (roles, other person's
rows, day grouping across midnight in a zone). `listening_perf_test.go`: the same 4 x 33,000 sessions under 150 ms.
`internal/db/migration_0030_test.go`.

Out of scope: night sleep, editing or deleting sessions, reading sessions, an API route.

## 10. R1 All my highlights

Reused: `highlights`, `share.BookFilterFor` (S5), `s.scope`, the word list's page shape (`routes_words.go`) and exports,
`getReaderMarksExport`'s formatting. New: one page. No migration: a person's rows are few and `highlights_live` serves the
user prefix.

### Invariants
1. Only the signed-in person's rows, `deleted_at IS NULL`, books filtered by `ReadText` in SQL, so a revoked book shows
   nothing and its title never leaves the database. Rows stay stored.
2. Search text is a bound parameter with `LIKE ... ESCAPE`, wildcards escaped.
3. Read-only: editing stays in the reader, so no JS and no second write path.

### Steps
1. `internal/library/highlights.go` (new): `AllHighlights(ctx, sc, userID, HighlightQuery{Text, Colour, BookID, Offset,
   Limit}) ([]HighlightRow, total int, error)`, newest first, 50 a page, with book title. An unknown colour is ignored.
2. `internal/web/reader_marks.go`: extract `marksMarkdown`, `marksText`, `marksJSON` so both exports share one format.
   `readerHref(bookID, editionID string, pairs ...string)` from `rc.href`; a row links to its sentence only when
   `edition_id` is set, else to the reader.
3. `internal/web/routes_highlights.go`, `highlights_copy.go`, `pages/highlights.templ` (new): `GET
   /me/highlights?q=&colour=&book=&page=`, `GET /me/highlights.md`, `GET /me/highlights.json`, registered in `server.go`.
   Colour is shown with its name, never by colour alone.
4. `internal/web/stats_copy.go`: `v.Highlights, v.HighlightsHref` beside `WordsHref`, and the template that shows it.

### Tests
`internal/library/highlights_test.go`: scope, revoked, `can_read=0`, deleted, search with `%` and `_`, colour, paging.
`internal/web/highlights_test.go`: roles, other person's rows absent, export contents and headers, a revoked book absent
from the export, per-book export unchanged byte for byte.

Out of scope: text bookmarks on this page, editing here, a cross-book API (A9 decides), audio bookmarks (their tab has them).
