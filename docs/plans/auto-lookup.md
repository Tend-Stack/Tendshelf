# Automatic and bulk book-detail lookups (operator request 2026-11-03)

Builds on M4 (`docs/plans/m4-details.md` S1-S10). Packages: **AL-1** (online package, Go), **AL-2**
(now-panel link, UI), **AL-3** (bulk job, Go), **AL-4** (admin UI). Order: AL-1 and AL-2 in parallel, then
AL-3, then AL-4. Builders do not edit `docs/`; the orchestrator applies "Docs" at merge.

## Shared invariants
1. Nothing goes online unless `online.enabled` is on (owner) **and** a person clicked, or the owner/admin
   turned on "Look up details online for new books" for that library. Off means no job runs.
2. Only title, author, ASIN and ISBN leave the machine (plus the existing store region). `online` stays the only
   package with an `http.Client`.
3. Automatic results are written **only as `online` rows** (lowest precedence, S1). They never write `user`
   rows, never touch a book that already has any `online` or `user` row, and never replace a cover.
4. One bulk job runs at a time server-wide (Concurrency 1), books one after another, never in parallel.
5. A bulk job can be stopped, survives a restart without losing work or its pause, and its counts are honest.
6. A doubtful match is never applied; it is listed as "needs a look" and resolved with the existing compare sheet.
7. Every new route checks `share.Can`; a refusal, an unknown id and "lookups off" all answer 404.

## Shared decisions
| # | Decision | Reason |
| --- | --- | --- |
| A1 | Confidence rule: apply automatically only if (a) the book's own ASIN/ISBN equals a candidate's (`Exact`), or (b) the query has title AND author, and **exactly one** distinct ASIN among candidates passes `online.Confident`: folded title equal (or equal to title+" "+subtitle, or `ratio >= 0.95`) AND an author-name hit (same rule as `Score`). | `Score >= 0.9` alone accepts "Dune Messiah" for "Dune" and picks an arbitrary edition (wrong narrator). A false "needs a look" costs one click; a wrong silent fill costs trust. |
| A2 | Auto writes all `ApplyFields` the candidate has, as `online` rows; author ASINs only where empty (existing `SetAuthorASIN`). Audit `book.details.auto`. | Online rows only fill gaps (S1/S2); tags and sidecars keep winning. |
| A3 | Cover only when `books.cover_id IS NULL` and no `media.cover` job is active for the book, via `media.cover_adopt` with `if_no_cover`, re-checked inside the adopt transaction. | Never replaces the file's own picture; avoids racing the scan's cover job. |
| A4 | Per-library setting is a column `libraries.auto_lookup_at` (NULL = off, else ms when turned on). "New books" = `books.added_at >= auto_lookup_at`. | Typed, deleted with the library, and turning it on never sweeps old books behind the owner's back. |
| A5 | Per-book marker table `book_lookups` is both the skip list and the resume cursor; the job re-queries "next eligible book" each step. | Restart-safe without a runner change; new books scanned mid-run are picked up. |
| A6 | Trigger for "new": at the end of a successful `metadata.local` job (book details read), not at scan end. | Scan end fires before books are probed; local-done means the query has the tags. |
| A7 | Pacing, backoff, breaker and daily cap are constants in `internal/metadata/online/pace.go`, not settings. | Politeness to a free community service is not a user preference; one place to change. |
| A8 | Daily cap counts all network requests (manual lookups too) in table `online_usage`; manual lookups are counted, never blocked. | A restart must not reset the cap; a person's click always works. |
| A9 | Bulk lookups ask Audnexus/Audible only, at most 3 Audnexus books per search; "Also search Google Books, Open Library and Apple Books" is an unticked option. | S3 privacy default; 11 requests per book drops to at most 4. |
| A10 | Permissions: library setting, start/stop, needs-a-look list = `share.ManageLibraries` on `TargetLibrary` (owner, admin). Now-panel link = `share.EditBook` on the book. Owner switch unchanged (`ManageServer`). | Existing actions; no new action. |

---

## AL-1 Online package: counts, short search, confidence, pace (Go, `internal/metadata/online` only)
Steps
1. `steps.go`: add `Sent int \`json:"sent,omitempty"\`` to `Step`; in `recorder.end` set `s.Sent = r.calls - r.hit`.
   Add `func (r Result) SentBy() map[string]int` summing `Step.Sent` by `Step.Provider` (in `lookup.go`).
2. `lookup.go`: `type LookupOptions struct{ MaxAudnexusBooks int; OnStep func([]Step) }`;
   `func (c *Client) LookupWith(ctx, q Query, providers []string, o LookupOptions) (Result, error)`;
   `LookupSteps` becomes `LookupWith(ctx,q,p,LookupOptions{OnStep: onStep})`. In `searchAudnexus`, when
   `MaxAudnexusBooks > 0` truncate `asins` to it (pass the limit via the ctx recorder or a parameter; builder's choice
   of plumbing, behaviour fixed). 0 = unchanged (all, max 10).
3. `score.go`: `const confidentTitleRatio = 0.95`; `func Confident(b Book, title, author string) bool` per A1
   (false when `title` or `author` is blank). Reuse `norm`, `ratio`, `splitNames`; do not change `Score`.
4. New `pace.go`:
   ```go
   const (
     BulkGap = 4*time.Second; BulkJitter = 2*time.Second      // between books that sent a request
     BackoffBase = 30*time.Second; BackoffCap = 15*time.Minute // after a troubled book: 30s,60s,120s
     BreakerAfter = 4                                          // consecutive troubled books -> pause
     BreakerPause = 30*time.Minute; BreakerPauseCap = 4*time.Hour // 30m,60m,120m,240m,240m
     BreakerMaxTrips = 5                                        // 6th trip stops the job
     DailyCap = 2000                                            // network requests per provider per UTC day
     DailyJitter = 10*time.Minute
     BulkAudnexusBooks = 3
   )
   type Outcome int // OutcomeSent, OutcomeCached (no request sent), OutcomeTrouble
   type Pace struct{ Consecutive int `json:"consecutive"`; Trips int `json:"trips"` }
   type Wait struct{ For time.Duration; Reason string /* "", "pace", "backoff", "pause" */; Stop bool }
   func (p *Pace) Next(o Outcome, jitter float64) Wait // jitter in [0,1)
   func NextDay(now time.Time, jitter float64) time.Time // next 00:00 UTC + jitter*DailyJitter
   func Troubled(code string) bool // busy, server, offline, bad_answer, too_large
   ```
   `Next`: Sent -> Consecutive=0, `{BulkGap+jitter*BulkJitter,"pace"}`; Cached -> Consecutive=0, `{0,""}`;
   Trouble -> Consecutive++; if `>= BreakerAfter`: Consecutive=0, Trips++, `Trips > BreakerMaxTrips` -> `{Stop:true}`
   else `{min(BreakerPause<<(Trips-1), BreakerPauseCap),"pause"}`; otherwise `{min(BackoffBase<<(Consecutive-1), BackoffCap),"backoff"}`.
Tests (`pace_test.go`, `online_test.go`; prefix `TestOnlineAuto`)
- `TestOnlineAutoPace` table: every branch above, including the Stop on trip 6 and caps.
- `TestOnlineAutoNextDay`: 23:59:30 UTC with jitter 0.5 -> next day 00:05:00 UTC.
- `TestOnlineAutoConfident` table: equal titles+author hit true; "Dune" vs "Dune Messiah" false; accents
  folded true; title+subtitle equal true; blank author false; author miss false.
- `TestOnlineAutoSent`: fake transport; a fresh search reports Sent per step; the same search again reports 0 and `Cached`.
- `TestOnlineAutoLimit`: `MaxAudnexusBooks:3` with 10 Audible ASINs makes exactly 3 `api.audnex.us` requests.
Gates: `go test -race ./internal/metadata/...`, `make check`, `make build`. Out of scope: anything outside `online/`.

---

## AL-2 "Look up details" in the Now playing panel (UI; parallel with AL-1)
Reused: `lookupHref(id)` (`/books/{id}/details?sheet=lookup`, the existing lookup sheet), `detailsLookupButton`
("Look up details online"), boosted navigation (player in the layout keeps playing).
Steps
1. `internal/web/components/now_panel.templ`: `NowAboutView` gains `LookupHref, LookupLabel string`. Inside
   `<div class="ts-np__empty">`, after the span: `if v.LookupHref != "" { <a class="ts-btn ts-btn--secondary"
   href={ templ.SafeURL(v.LookupHref) }>{ v.LookupLabel }</a> }`. No `hx-*` attribute (inherits `hx-boost:inherited`).
2. `internal/web/now_panel_views.go` `nowAboutView`: when `v.Intro == ""` and `s.details != nil` and
   `s.can.Can(ctx, currentUser(r), share.EditBook, share.Target{Kind: share.TargetBook, ID: d.Book.ID, LibraryID: d.Book.LibraryID})`
   and `metadata.OnlineEnabled(...)` is true: set `LookupHref = lookupHref(id)`, `LookupLabel = detailsLookupButton`.
   An `OnlineEnabled` error returns the error (existing 500 path).
3. If `.ts-np__empty` needs a gap for the button, add at most 6 lines to the stylesheet that already styles
   `.ts-np__empty`; report the gzip size.
Tests
- `now_panel_test.go` `TestNowPanelLookupLink`: owner and admin see the link with the exact href when the book
  has no description; member and guest do not; owner switch off -> no link; book with a description -> no link.
- Browser `internal/web/now_panel_lookup_browser_test.go` `TestNowPanelLookupKeepsPlaying` (copy the shape of
  `write_keeps_playing_browser_test.go`): play, open full player About, click the link, assert the lookup sheet is
  open, the `<audio>` element is the same and `currentTime` keeps increasing, no `load` event fired.
- Captures at 390/768/1024/1440 of the panel empty state (owner), `make visual` before/after, `capturediff` report.
Gates: touched tests, `make generate` clean, `make check`, `make build`.

---

## AL-3 Bulk lookup job (Go: db, library, metadata, mediajobs, wiring; after AL-1)
Migration `internal/db/migrations/0012_auto_lookup.sql`; `SchemaVersion = 12`:
```sql
ALTER TABLE libraries ADD COLUMN auto_lookup_at INTEGER CHECK (auto_lookup_at IS NULL OR auto_lookup_at > 0);
CREATE TABLE book_lookups (
  book_id    TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
  outcome    TEXT NOT NULL CHECK (outcome IN ('matched','needs_look','no_match','failed','kept','no_query','chosen','dismissed')),
  provider   TEXT NOT NULL DEFAULT '',
  job_id     TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX book_lookups_job ON book_lookups(job_id);
CREATE TABLE online_usage (
  day      INTEGER NOT NULL,  -- UTC day number: unix ms / 86400000
  provider TEXT NOT NULL CHECK (provider IN ('audnexus','google','openlibrary','itunes')),
  requests INTEGER NOT NULL CHECK (requests >= 0),
  PRIMARY KEY (day, provider)
) STRICT, WITHOUT ROWID;
```
Steps
1. `internal/library/lookups.go` (new): `Library.AutoLookupAt int64` (read wherever `Library` is scanned);
   `(r *Repo) SetAutoLookup(ctx, libraryID string, at int64) error` (0 -> NULL); `(t *Tx) PutBookLookup(bookID, outcome, provider, jobID string, at int64) error` (upsert);
   `(r *Repo) NextLookupBook(ctx, libraryID, mode string, runStart, skipBefore int64) (id string, remaining int, err error)`;
   `(r *Repo) LookupCounts(ctx, jobID string) (map[string]int, error)`; `(r *Repo) NeedsLook(ctx, libraryID string, limit int) ([]LookupRow, int, error)`
   (`LookupRow{BookID, Title, Author, Outcome string; CheckedAt int64}`, outcomes needs_look and no_match, by sort_title);
   `(r *Repo) AutoLookupDue(ctx, bookID string) (libraryID string, due bool, err error)`.
   Eligible (both modes): same library, `missing_since IS NULL`, `refused_reason IS NULL`, a `book_sidecars` row exists, and
   no `book_lookups` row with `checked_at >= runStart` or outcome in (chosen, dismissed).
   `all` adds: no row with outcome <> 'failed' and `checked_at >= skipBefore`. `new` adds: `added_at >= libraries.auto_lookup_at`
   and no row with outcome <> 'failed'. Order `added_at, id`. `remaining` = count of eligible.
2. `internal/metadata/online_usage.go`: `AddOnlineUsage(ctx, st, day int64, sent map[string]int) error`,
   `OnlineUsage(ctx, q, day int64, provider string) (int, error)`, `PruneOnlineUsage(ctx, st, beforeDay int64) error` (7 days).
   `handleLookup` (manual) also calls `AddOnlineUsage(res.SentBy())`; never blocks.
3. `internal/metadata/auto.go`: `func pickAuto(q online.Query, cands []Candidate) (Candidate, bool)` per A1 (Candidate gains
   `Confident bool`, set from `online.Confident` when mapping); `func (s *Service) applyAuto(ctx, bookID, jobID string, c Candidate) error`
   per A2, in one `repo.Update`: re-check "no online/user rows" inside the tx (else outcome `kept`), `PutMetadataValue` online rows,
   `syncResolved`, ASINs, `PutBookLookup(matched)`, audit `book.details.auto` `{"provider","fields","job"}`. Extract the candidate
   mapping loop of `handleLookup` into `toCandidates(online.Result) []Candidate` and use it in both.
4. `internal/metadata/bulk.go`: `const KindOnlineBulk = "metadata.online_bulk"`, `BulkAll = "all"`, `BulkNew = "new"`, `skipWindow = 30*24*time.Hour`, `autoDelay = time.Minute`.
   `BulkPayload{LibraryID \`library_id\`; Mode \`mode\`; Providers \`providers\`}`;
   `BulkProgress{Done, Total, Matched, NeedsLook, NotFound, Skipped, Failed int; Current string; PausedUntil int64; PauseReason string /* busy|daily_cap */; Pace online.Pace}` (JSON snake_case).
   Service gains `sleep func(context.Context, time.Duration) error`, `jitter func() float64` (defaults: timer sleep, `math/rand/v2`), a `jobStore` assertion
   (`Get`, `ActiveFor`, `LatestFor`, `Cancel`) like `LookupResultFor`, and `SetCoverFill(func(ctx, bookID, url string) error)`.
   - `StartBulk(ctx, libraryID string, more bool, userID string) (string, error)`: `ErrOnlineOff`, `ErrNotFound`; returns the id of an active `all` job if any; else enqueue `all`; audit `library.lookups_start`.
   - `StopBulk(ctx, libraryID, userID string) error`: cancels every active bulk job of the library; audit `library.lookups_stop`.
   - `SetAutoLookup(ctx, libraryID string, on bool, userID string) error`: keeps the old `auto_lookup_at` when already on; audit `library.auto_lookup` `{"on"}`.
   - `BulkStatus(ctx, libraryID) (jobs.Job, BulkProgress, bool, error)` via `LatestFor(KindOnlineBulk,"library_id",id)`.
   - `DismissLookup(ctx, bookID, userID string) error` -> outcome `dismissed`; audit `book.details.dismiss`.
   - Handler, in order: bad payload -> permanent `bad_payload`; library gone -> done; read own job (`Get(t.ID)`) for `CreatedAt` (runStart) and prior
     progress (restore `Pace`; if `PausedUntil > now` sleep the rest); prune usage. Loop: `OnlineEnabled` false -> permanent `online_off`;
     daily cap reached for any payload provider -> `PausedUntil = NextDay`, `PauseReason=daily_cap`, `Pace.Trips=0`, sleep, continue;
     `NextLookupBook`; none -> return final `BulkProgress`. Per book: no query -> `no_query`; has online/user rows -> `kept` (no request);
     `LookupWith(... MaxAudnexusBooks: BulkAudnexusBooks)`; `AddOnlineUsage`; any provider `busy` -> do not record, Trouble;
     all providers errored -> `failed`, Trouble; else no candidates -> `no_match`; `pickAuto` ok -> `applyAuto` (+ cover fill if
     `CoverID==""` and `CoverURL!=""`; a fill error is logged only); else `needs_look`. Then `Pace.Next` (Sent vs Cached from `SentBy`),
     `Stop` -> permanent `provider_refusing`; `pause` sets `PausedUntil`/`PauseReason=busy`; publish progress (counts from `LookupCounts(t.ID)`,
     `Total = Done + remaining`) before every sleep. Every sleep honours ctx (Stop and shutdown).
   - Register: `jobs.Kind{Name: KindOnlineBulk, Concurrency: 1, MaxAttempts: 3}`.
5. `internal/metadata/jobs.go` `handle` (local): after success call `s.maybeAuto(ctx, bookID)` (errors logged, never returned):
   `AutoLookupDue` and `OnlineEnabled`; `ActiveFor` newest bulk job of the library: queued -> nothing; none or running -> enqueue
   `{mode:new, providers:[audnexus]}` with `NotBefore: now+autoDelay`, `CreatedBy: ""`.
6. `internal/metadata/apply.go` `ApplyOnline`: inside its tx `PutBookLookup(bookID,"chosen",c.Provider,"",now)`. `ResetOnline` does not touch `book_lookups`.
7. `internal/mediajobs/cover_adopt.go`: `AdoptPayload.IfNoCover bool \`json:"if_no_cover,omitempty"\``; `func (c *Covers) FillOnline(ctx, bookID, url string) error`
   (no-op if the book has a cover or `ActiveKeys(KindCover,"book_id")` has it; else enqueue). Handler: with `IfNoCover`, return done `{"kept":true}`
   before fetching if a cover exists, and pass a guard that runs `t.Fence` then fails with `errCoverPresent` if `cover_id IS NOT NULL`; map that to done.
8. `cmd/tendshelf/hooks.go`/`main.go`: `svc.SetCoverFill(covers.FillOnline)`.
Tests (prefix `TestBulk`; fake transport from `online/testdata`, fake clock, recording fake sleep, jitter 0.5)
`TestBulkExactApplies`, `TestBulkAmbiguousNeedsLook` (two confident ASINs), `TestBulkNoMatch`, `TestBulkOnlineRowsOnly` (tag narrator kept,
empty description filled, zero `user` rows, audit row), `TestBulkKeepsChosenBooks` (zero requests), `TestBulkSkipWindow` (10 d skipped, 31 d redone,
failed retried), `TestBulkResume` (cancel after 2 books, rerun same job: starts at book 3, counts 2+), `TestBulkPausePersists`, `TestBulkBreaker`
(always 429: sleeps 30s,60s,120s then 30m...; ends `provider_refusing`; no `failed` rows), `TestBulkDailyCap`, `TestBulkPace` (4s+1s between sent
books, none after cached), `TestBulkOnlineOff`, `TestBulkNewMode` (+ maybeAuto queued/running dedupe), `TestBulkCoverFill`, `TestBulkApplyMarksChosen`,
`TestBulkUsageManual`, `TestMigration0012AutoLookup` (v11 fixture survives, column NULL), library `TestLookupsNextBook` table, `TestCoverAdoptIfNoCover`.
Gates: `go test -race` on db, library, metadata/..., mediajobs, cmd/tendshelf; `make check`; `make build`.

---

## AL-4 Admin UI: setting, update all, status, needs a look (Go web + templ; after AL-3)
Copy in new `internal/web/lookups_copy.go` (exact words):
- Checkbox: "Look up details online for new books". Hint: "Tendshelf sends each new book's title, author, ASIN and ISBN to Audible and Audnexus, and fills in only what is missing. Your files are not changed."
- Section title "Book details from the internet" (reuse `adminOnlineTitle`); button "Look up all books"; option `detailsMoreLabel`; "Stop".
- Status: "Looking up {n} of {N} books · {m} found · {k} need a look · {f} not found" (+ " · {x} could not be checked" when > 0);
  "Paused because Audible asked for a break. Continues at {HH:MM}."; "Paused for today to stay within the sites' limits. Continues at {HH:MM}.";
  done "Done: {m} found, {k} need a look, {f} not found."; `provider_refusing` "Audible kept refusing, so Tendshelf stopped. Books already done stay done. Try again later."
  Times in the server's local zone (guess: matches other admin times; builder checks `lastScanText`).
- Link "{k} need a look"; page title "Books that need a look"; row buttons "Compare" (`lookupHref`) and "Leave as it is"; outcome words "Several possible matches" / "Nothing found".
- Change `adminOnlineBody` to: "When someone who edits books asks, or a library is set to look up new books, Tendshelf looks books up on Audible and, if chosen, a few other book sites."
Steps
1. New `internal/web/routes_lookups.go`, registered from `registerDetails`: `POST /admin/libraries/{id}/auto-lookup` (field `on`=1|0),
   `POST /admin/libraries/{id}/lookups` (field `more`), `POST /admin/libraries/{id}/lookups/stop`, `GET /admin/libraries/{id}/lookups`,
   `POST /admin/libraries/{id}/lookups/{book}/dismiss`. Each: `lookupsOn` false -> 404; `ManageLibraries` on `TargetLibrary` else 404;
   unknown library or a book of another library -> 404; POSTs redirect to `/admin/libraries` (dismiss to the list page).
2. `routes_jobs.go` `jobViews`: `metadata.KindOnlineBulk: (*server).getBulkJob` (payload library, same permission, else 404) rendering
   new `components/lookup_bulk_status.templ` `LookupBulkStatus(v)`; polls `hx-get="/hx/jobs/{id}" hx-trigger="every 2s" hx-swap="outerHTML"`
   only while queued/running; one `role="status"` line that changes only on terminal or pause states (as `ScanStatus`).
3. `pages/admin_libraries.templ`: when lookups are on and the viewer may manage, a section after "Folders": one row per library (not per
   folder): name, the auto checkbox form (auto-submit not allowed: a "Save" button), "Look up all books" form with the `more` checkbox,
   or the status + "Stop" while active, and the needs-a-look link when the count > 0. View types `AdminLookupLibrary`.
4. `pages/admin_folder.templ` + `postAddFolder`; `pages/setup_steps.templ` + the setup folder POST: the checkbox `auto_lookup` (unticked by default,
   shown only when lookups are on). Ticked -> `SetAutoLookup(on)` after the library exists, before `startScan`. Unticked never turns a library off.
5. New `pages/admin_lookups.templ` for the list (max 200 rows, then "and {n} more").
6. CSS only if needed, in `css/pages-admin.css`; report gzip size.
Tests: `TestAutoLookupRoutes` (owner, admin OK; member, guest 404; switch off 404; other library's book 404; bad CSRF refused),
`TestBulkJobView` (admin sees status, member 404), `TestAddFolderAutoLookup` and setup (ticked sets, unticked keeps), `TestNeedsLookPage`,
role rows in `routes_matrix_test.go`; captures of `/admin/libraries` (idle, running, paused), add-folder and setup step at 390/768/1024/1440.
Gates: `make generate` clean, `make check`, `make build`.

## Shared files
`internal/web/details_copy.go` (AL-4 only), `routes_details.go` (AL-4 registers), `internal/metadata/online.go` (AL-3), `cmd/tendshelf/hooks.go` (AL-3).
AL-1 touches only `internal/metadata/online/`; AL-2 only now-panel files and its test; they never overlap.

## Out of scope
Changing the manual compare flow; typing edits; a "force redo" for books inside the 30-day window; per-provider breakers (one breaker per job);
the library browse page header; showing usage numbers; translations.

## Docs (orchestrator at merge)
`docs/architecture.md`: kind `metadata.online_bulk`, tables `book_lookups`, `online_usage`, column `libraries.auto_lookup_at`.
`docs/plans/m4-details.md` S4: "Scans never go online" -> see decision 0009. `docs/roadmap.md` status. Add decision below.

## Draft: docs/decisions/0009-automatic-lookups.md
# 0009 Automatic book-detail lookups
Status: proposed (2026-11-03). Changes S4 of `docs/plans/m4-details.md`.
Context: S4 said every lookup needs a click and scans never go online. Owners asked to fill details for new
books and whole libraries without a click per book.
Decision: An owner or admin may turn on "Look up details online for new books" per library (off by default) and
may start "Look up all books" for a library. That explicit choice is the consent. The owner's `online.enabled`
switch still overrides everything. The scan itself never goes online; a separate job, started after a book's
details are read, does. Only title, author, ASIN and ISBN are sent, to Audible and Audnexus unless more sites are chosen.
Automatic results fill gaps only (`online` rows), never override files, sidecars or a person's choice, never
replace a cover, and are applied only on an exact identifier or one unambiguous title-and-author match;
everything else waits for a person. Requests are paced (one book at a time, 4-6 s apart, backoff, a pause
after repeated refusals, at most 2000 requests per site per day).
Consequences: Tendshelf may make requests without a click, which the owner can stop at any time; books can
take days to finish in a large library; the "Scans never go online" sentence in the admin page changes.
