# Roadmap

Work is grouped into milestones; each ends with something usable. A milestone is a sequence of phases
(P = server and listening app, R = read-along), and each phase ships as packages of about one agent
session (split by layer: data and jobs, then pages, then client code). A phase is done when its exit
criteria pass on the gates in `AGENTS.md` and the status below records scope, checks, limits and next
step. Order and scope decisions: [decision 0002](decisions/0002-design-handoff.md) (2026-11-01).

Visual source: [design handoff](design/tendshelf-handoff/README.md). Every page package names the boards
it builds (`screens/INDEX.md`) and records a side-by-side check at 390, 768, 1024 and 1440 px in all four
themes.

## Status

| Milestone | Phase | State | Evidence |
| --- | --- | --- | --- |
| M0 Foundation | P0 skeleton | Docs, stdlib server on 127.0.0.1:8095 with `/` and `/healthz`, `internal/db` header-check rules, Makefile. P0 dependencies approved 2026-11-01 (htmx 4.0.0, fonts and templ v0.3.1100 downloads approved). **Done 2026-11-01.** P0a (data and jobs), P0b (UI shell) and P0c (utilities and motion) built, reviewed and merged; see their notes below. | `make check` (with and without `-tags dev`), `make build` (CGO_ENABLED=0), `make visual` (Chromium) and `make visual-webkit` (WebKitGTK 2.52.6) green on main 2026-11-01; CI green. |
| M1 It plays | P1a-P1c | In progress. **P1a done** and **P1b done 2026-11-01** (accounts and first-run setup, Libraries admin and folder browser, Home, Library with filters, book, people and search pages, Settings and Profile, shelves, exit audit). **P1c built 2026-11-02** (progress data, streaming, player core, full player, speed, sleep timer, bookmarks, play buttons and Home rows, exit sweep); it closes, and M1 with it, after the operator's phone check in [manual-checks.md](manual-checks.md). | On main: `make check`, `make build` (CGO_ENABLED=0), real-ffmpeg targets, `make visual` (about 55 s) and `make visual-full` (about 147 s), `make perf` (Library interactive in a median 64 ms at 4x CPU slowdown, 240 ms at 8x, budget 300 ms), `make visual-webkit`; `TestRouteMatrix` covers all 47 routes for every role. Board audit: `docs/design/screens.md`. Push CI green; nightly CI green (run 9052 onward). Player: `make player` (first sound about 270 ms). |
| M2 Family | P3 | In progress. P3-1 (authorization core: migration 0007, grants in `Can` and the listing filters) built 2026-11-02; P3-2 (Users admin: Go, markup, CSS, captures) built; P3-3a (Invites: Go and markup) built; P3-3b (Invites: CSS, captures, browser test) built; P3-4 onward not started. | See the P3-1 status note below. |
| M3 Pocket | P4 | Not started | |
| M4 Details | P2 | Not started | |
| M5 Read along | R1-R6 | Designed 2026-10-31 ([read-along.md](read-along.md), [decision 0001](decisions/0001-read-along.md)); UI designed in the handoff (`screens/reader/`). Dependencies await approval. | Design only. |
| M6 Desktop | P5 | Not started | |
| M7 Research | P6 | Not started; lab designed (`screens/admin/Research*`). | |

## M0 Foundation

### P0a Data and jobs

- Add `modernc.org/sqlite` v1.61.1 and `golang.org/x/crypto` v0.58.0 with `NOTICES.md` rows.
- `internal/db`: open with read-only pre-check, `application_id`, `user_version`, embedded migrations,
  `VACUUM INTO` backups (newest three), typed refusal errors, migration test harness. Migration
  `0001_init.sql` creates `settings`, `users`, `sessions`, `user_settings`, `jobs`, `audit_log`.
- `internal/jobs`: queue table, runner with per-kind concurrency, 60 s leases, restart recovery,
  cancellation, progress at most once per second.
- `cmd/tendshelf`: `-data` directory flag/env, open the database before serving, graceful shutdown of
  the job runner.

Exit: migrations applied to a fresh database and from the empty fixture; a newer-schema or foreign
database refused byte-identical (test); a job survives a restart (test); `CGO_ENABLED=0 make build`.

Status 2026-11-01: built. Scope: `internal/db` (open with a header pre-check that never writes a refused
file, single-connection writer plus query-only reader pools, migration `0001_init.sql`, verified
`VACUUM INTO` backups, newest three kept), `internal/jobs` (leases with a fencing token, per-kind
concurrency, recovery at start and every 30 s, cancel, resume, throttled progress, graceful stop),
`cmd/tendshelf` (`-data` / `TENDSHELF_DATA`, per-user default folder, plain refusal messages). Dependency:
`modernc.org/sqlite` v1.61.1 with its transitive modules, rows in `NOTICES.md`. Checks: `make check`
(gofmt, vet, `go test -race ./...`) and `make build` green; `internal/db` 22 tests, `internal/jobs` 29,
`cmd/tendshelf` 5; the built binary reports `CGO_ENABLED=0`. Limits: `golang.org/x/crypto` moves to P1b
(users and sessions logic; only the tables exist now); no lock against two processes on one data folder;
no job list or pruning; `Enqueue` cannot join a caller's transaction. Next: P0b (UI shell).

### P0b UI shell

- Add `github.com/a-h/templ` v0.3.1100 (runtime; CLI pinned to the same version in `make generate`, CI fails on a
  dirty tree) and vendor htmx 4.0.0 with version and SHA-256 in `NOTICES.md`.
- Static tree embedded with hashed, immutable names: `tokens.css` and `bundle.css` from the handoff,
  Fraunces, Fira Sans and Fira Mono woff2 (self-hosted, OFL rows in `NOTICES.md`), the logo SVGs and an
  inline SVG icon sprite.
- Layout templ component: theme on `<html data-theme>` (system default, user choice later from
  `user_settings`), `#main` swap region, the three shells (phone tab bar 72 px, tablet rail 88 px,
  desktop sidebar 264 px) and the empty player slot, `<audio>` and navigation outside swaps with
  `hx-preserve`; `#toasts` region; error page with plain words; htmx config meta; CSRF middleware.
- Visual capture helper (opt-in `TENDSHELF_TEST_BROWSER`, system Chromium, no Go module) writing to
  `.local/tests/visual/<run-id>/`.

Exit: a templ page served with htmx from `/static` under the strict CSP; each shell renders at 390,
768, 1024 and 1440 in dark, light, sepia and contrast with no horizontal scroll (captures reviewed
against `screens/core/Home*.png` for the frame only); CSRF rejects an unsafe request without the token.

Status 2026-11-01: built. Scope: templ v0.3.1100 (runtime plus `go tool templ`, `make generate`, CI step
that fails on a dirty tree), htmx 4.0.0 and eight woff2 fonts vendored with SHA-256 in `NOTICES.md`
(fonts 1,226,212 B in total, 361,428 B preloaded), `internal/web/static` (embedded tree, content-hashed
immutable URLs, load-time `@layer` wrapping, system light and contrast blocks, font URL rewrite),
`internal/web/components` (24 icons, TabBar, SideRail, Sidebar, player slot, toasts, Layout), pages
(Home empty state, plain-words error page), CSRF (HMAC token over a `ts_csrf` cookie plus
`http.CrossOriginProtection`), `ts_theme` cookie, `static/js/shell.js`. Checks: `make generate` leaves
a clean tree, `make check` and `CGO_ENABLED=0 make build` green, 34 top-level tests in `internal/web`,
`components` and `static`; `TestVisualShell` passes 16 captures (4 widths by 4 themes, no horizontal
overflow). Lead review 2026-11-01: captures match the `screens/core/Home*` frame (tab bar, rail,
sidebar); the merged binary serves hashed assets under the strict CSP and refuses a token-less POST
(403); WebKitGTK 2.52.6 through WebKitWebDriver + MiniBrowser at 1440 and 447 px (GTK's narrowest window)
renders the same shell, no overflow, Fraunces loaded, View Transitions and `@starting-style` supported.
Limits: MiniBrowser has no headless mode (P0c needs a hidden display for automated WebKit runs); the
player, theme picker and sessions are P1; `style="width:..."` in handoff markup needs the P0c
replacement under `style-src 'self'`; no compression or ETags. Next: P0c.

### P0c Utilities and motion layer

Light but not boring: small touches that make the app feel native on every device and in the Wails
v3 WebViews (WebKitGTK on Linux, WKWebView on macOS and iOS, WebView2 on Windows, Android WebView).

- Layout utilities added to `bundle.css` as `ts-` classes on the design's spacing tokens (stack,
  cluster, grid, center, switcher, visually-hidden, safe-area padding), replacing the boards' inline
  layout styles.
- The handoff motion spec as reusable CSS: page swap fade-and-rise (htmx swap/settle classes, View
  Transitions where supported), sheets and toasts entering with `@starting-style`, press feedback,
  progress easing, skeleton shimmer, the mini-player to full-player shared cover where supported.
- Native feel: `100dvh` and `env(safe-area-inset-*)`, hover only under `(hover: hover)`, no tap
  highlight, `overscroll-behavior` and `touch-action` where gestures live, 44 px targets.
- Every newer feature behind `@supports` with a plain fallback; `prefers-reduced-motion` turns every
  movement into a 150 ms fade. No JavaScript animation library; at most a few lines in the client module.

Exit: a `/dev/kitchen-sink` page (registered only in development builds) shows every utility and
motion state; captures in Chromium and WebKitGTK (MiniBrowser) at 390 and 1440 match in all four
themes; reduced motion verified; no layout shift on swap.

Status 2026-11-01: built, reviewed and merged. Scope: `utilities.css` (4.3 KB) and `motion.css`
(5.1 KB) in the `utilities` and `motion` layers; boosted navigation runs one View Transition
(`transition:true` in the boost value, `#main` named `ts-main`, `outerSync` keeps the node) with a
`ts-entering` keyframe where the API is missing; sheets and toasts enter with `@starting-style`;
`@media (hover: none)` resets every `:hover` rule of `bundle.css` (a test keeps the list complete);
reduced motion fades in 150 ms and stops shimmer, spinner and eq; `shell.js` (79 lines) turns
`data-ts-value` into `--ts-value` through the CSSOM, replacing the handoff's inline widths; the
P0b skip link moved to `.ts-sr-focusable` (it shared `.ts-skip` with the skip-back button). The
kitchen sink and its fragments exist only under the `dev` build tag (`make run-dev`); a release
build answers 404. Tests: `internal/testutil/cdp` (stdlib client over `--remote-debugging-pipe`)
drives Chromium for `make visual`; `internal/testutil/webdriver` drives WebKitGTK for
`make visual-webkit`. Limits: WebKit runs at 447 px, not 390 (GTK minimum window width), and shows a
window unless run under `xvfb-run -a`; reduced motion and layout shift cannot be read in WebKit.
Sheets render in `#sheet`, outside `#main` (a sibling of `#toasts`), because the `view-transition-name`
on `#main` makes it a stacking context that would put a sheet under the tab bar; stacking order is player
slot 9, tab bar 10, toasts 20, sheet layer 25, skip link 30. `shell.js` focuses a sheet when it opens,
closes it on Escape or a close link after the exit motion, and returns focus to the opener.
Lead review: Chromium captures (kitchen sink in four themes, sheet over the tab bar, toasts) match the
motion spec; `make visual-webkit` run by the lead passes 10 subtests (four themes at 447 and 1440 plus the
swap check; WebKit reports View Transitions and `@starting-style`); it found `ts-scroll-x` wrapping when
combined with `ts-cluster`, fixed (nowrap row, `flex: none` children). Operator hand checks 2026-11-01 on
the kitchen sink (`make run-dev`): press feedback, page swap, sheet, toasts, progress and themes in the
operator's browser; the same with GNOME Reduce Animation on (fades only); and the WebKitGTK 6.0
MiniBrowser that Wails uses (`make app`). All behaved as specified. Next: M1 (P1a).

## M1 It plays (single owner)

Goal: the owner installs it, points it at a folder and listens on phone and laptop. Built in three
packages; each page package uses the `screens/core`, `screens/browse`, `screens/people` and
`screens/states` boards.

### P1a Library, scanner and media

- Libraries and folders; scanner for Author/Series/Book, single files, numbered MP3 folders, multi-disc
  folders; DRM refusal; move detection by quick hash; missing files kept.
- ffprobe probe, chapters (embedded, or one per file), embedded/folder covers and thumbnails.
- Cover tint and per-theme cover accent computed at scan time (handoff "Cover accent and theme
  rules"), stored with the cover; ember fallback.
- Local details: folder names and file tags with per-field source (`metadata_values`).
- Compatibility copies for unplayable formats.

Exit: on a synthetic 200-book fixture library, first scan groups every layout correctly; refused files
are listed with a plain reason; accent contrast checks pass for the tested pairs in the handoff.

**P1a-1 status (2026-11-01)**: migration `0002_library.sql` (12 STRICT tables: libraries, library_folders,
books, audio_files, chapters, metadata_values, people, book_people, series, book_series, book_labels,
covers; `books.folder_id` and `audio_files.missing_since` added to the architecture); `internal/library`
(reads, `CreateLibrary`, `AddFolder` with overlap refusal, `Update`/`Tx` writers for books, audio files,
missing marks and chapters); `internal/media` (`Locate`, `Classify`, `Probe` with DRM refusal by extension
and by ffprobe signals, `QuickHash`) and `internal/media/mediatest` (helper-process fake ffprobe, ffmpeg
fixtures); `make test-media`. Checks: `make check`, `make build`, `CGO_ENABLED=0 go build ./...`,
`make test-media` (6 real-ffmpeg tests: M4B with chapters and cover, MP3 parts, FLAC, CENC refused, `.aax`
refused, junk refused). Limits: `books_fts` waits for P1b (migration 0003); the DRM signals for `drms`
and `aavd` codec tags are unverified guesses, only the CENC packet side data was checked on a real file;
no MP4 box walk; ffprobe version is not checked. People, series, labels, metadata values and covers have
tables but no writers yet. Next: P1a-2 (scanner and move detection on this repository).

**P1a-2 status (2026-11-01)**: scanner built on that repository. Migration `0003_probe_error.sql`
(`audio_files.probe_error`: a settled-but-unreadable state; `books_fts` therefore becomes migration 0004).
`internal/scan`: `walk` (never follows links, skips hidden, NAS and recycle folders, pipes and
devices; an unreadable subtree changes nothing), `GroupFiles` (root singles, Author/Book, Author/Series/Book,
numbered folders, CD/Disc folders, containers with loose files, split-part m4b), smart track order (ports of ABS
`runSmartTrackOrder` and Extractor `order_parts`), chapters (embedded, whole-book list repeated in
parts, or one per file), `finalize` (order, offsets, duration, chapters, refused reason as a pure function of
the rows, inside the tx that settles the last file), `ScanLibrary` (one guarded write tx; moved and renamed files
and folders keep their IDs; vanished files are marked missing, never deleted; a scan with nothing
new writes and queues nothing), `ProbeFile`, and the `scan.library` and `media.probe` jobs. Added
`Task.Fence`, `Runner.ActiveKeys`, `library.UpdateGuarded` and transaction reads, the `-ffprobe`
and `-ffmpeg` flags, and `make test-scan-real`. A book whose every file is unreadable is
`refused_reason='damaged'` (the file's `probe_error` keeps the exact code, such as `file_unreadable`).
A folder that is unavailable or newly empty marks nothing missing. Deliberately emptied folders are handled
by an explicit admin action in P1b. Checks: see the package report. Limits: no metadata beyond
title = folder or file name (P1a-3), no covers (P1a-4), no periodic scan or file watching, no full SHA-256,
no MP4 box walk. Next: P1a-3 (local details).

**P1a-3 status (2026-11-01)**: `internal/metadata` reads details from folder names (Audiobookshelf
`scandir.js` order: title, series, author; ASIN, `{narrator}`, sequence, year, subtitle) and file tags
(TendVox Extractor `meta_from_tags`), detects abridged and dramatized editions, and stores every candidate
in `metadata_values` with its source. The merge follows one pinned precedence (`user, opf, txt, nfo, tags,
folder, online`); the automatic job writes only `folder` and `tags` rows, is refused when it tries a `user`
row, and a `user` value wins even when empty, so P2 edits outrank it. The `metadata.local` job runs from the
scanner's book-settled hook (prerequisite H, merged first). People, series and labels have writers in
`internal/library/details.go`. Checks: `make check`, `make build`, `make test-scan-real`, `-race -count=3` on
metadata and scan; 44 metadata tests (Audiobookshelf name cases and Extractor cases ported); the 200-book
fixture now asserts title, authors, narrators, series and sequence per layout. Limits: no sidecars (`.opf`,
`.nfo`, `desc.txt`, P2), no online lookup (P2), no full-text index (P1b, migration 0004).

**P1a-4 status (2026-11-01)**: `internal/mediajobs` runs `media.cover` (from the book-settled hook) and
`media.compat` (on request). Covers: `cover.*`, then `folder.*`, then the first playable file's embedded
picture (mjpeg/png kept byte-identical; webp folder images converted to PNG by ffmpeg, no new module);
stored under `<data>/covers/<sha256>`, thumbnails 160/320/640 JPEG under `<data>/cache/thumbs/` (never
upscaled); an `upload` or `online` cover is never replaced. Tint from the 160 px thumbnail; per-theme accent
(dark, light, sepia) in OKLCH with chroma capped at 0.10 and 4.5:1 / 3:1 contrast checks, ember fallback
for grey covers. Goldens (dark / light): teal `#9ac9c2`/`#35615c`, moss `#b3c695`/`#4f5f32`, rust
`#f4a987`/`#854423`, plum `#c8b3e1`/`#614e76`, within ΔE_OK 0.019 (dark) and 0.041 (light) of the
handoff examples, which were hand-tuned outside the handoff's own rules. Compatibility copies: AAC-LC
128 kbit/s `.m4a` (stream copy for AAC), keyed by quick hash and recipe, file-system cache capped by
`-compat-cache-gib` (default 5), oldest first; the P1c player decides when to ask. Copy-protected or
unreadable files never reach ffmpeg (tested with a fake ffmpeg). Checks: `make check`, `make build`,
`make test-media`, `make test-mediajobs-real`, `-race -count=3` on media and mediajobs; 23 media, 15
mediajobs, 17 library tests. Limits: no online or uploaded covers (P2), no HTTP serving of covers, thumbs
or copies (P1b/P1c), no free-space check before converting (the cap bounds the cache), and an embedded
cover is re-extracted to compare each time its book settles (no write when unchanged). Next: P1b.

### P1b Accounts and browse pages

- `/setup`: owner account first, then server name and first folder with the scan streaming in
  (`screens/people/Onboard*`, reordered per decision 0002); login/logout; sessions; Settings and
  Profile (theme, playback preferences).
- Home (Continue listening hero with the one-resume rule, New on the shelf spines, Your series in
  progress, rule-based Because you listened to), library grid and list with filters in the URL
  (infinite scroll), book page with tabs, authors, narrators, series, search (FTS5) with did-you-mean,
  personal shelves, Libraries admin with scan progress and problems in plain words.
- Edge states: loading skeletons, server error, no results.

Exit: every page's handler tests; boards matched side by side at the four widths in four themes;
library page interactive in under 300 ms on a mid-range phone (measured).

**P1b-1 Auth foundation done 2026-11-01** (not yet merged when written; see the merge commit). Scope:
`internal/users` (argon2id, validation, owner creation), `internal/share` (`Can`, actions, listing filters),
`internal/auth` (sessions, sign-in, setup, limits, trusted proxies, setup code, `SafeNext`), `internal/db`
settings and audit helpers, session-bound CSRF with a persisted key, the default-deny gate, `/setup`
(step 1 of 3, the owner account), `/login`, `POST /logout`, the sign-in frame, `TextField`, `SetupSteps`,
`PrivacyNote`, flag `-trusted-proxy`, and the start-up setup link. No migration (0001 had the tables); new
module `golang.org/x/crypto` v0.58.0. All existing page and route tests now run signed in. Checks:
`make generate` (no diff), `make check`, `make build`, `CGO_ENABLED=0 go build ./...`, `make visual` (setup
and login in four themes at four widths; the 204 + `HX-Redirect` answer to an unauthenticated htmx request
works with the vendored htmx 4), `-race -count=3` on auth, users and web. Limits: no sign-out button yet and
no account menu (P1b-7), theme still in the `ts_theme` cookie, setup steps 2 and 3 and the scan stream
(P1b-4), no passkeys, no absolute session cap, the limiter state is per process.

**P1b-2 status**: done. Scope: `internal/library/browse.go` (`ListBooks` with keyset cursors for the
added, title, author and duration sorts, genre/author/narrator/series/library filters, `CardsByID`, `Genres`,
`VisibleLibraries`, `BookDetail`, `Person`, `SeriesInfo`; every query carries the caller's `Scope` and
`Listable` in SQL, counts and facets use the list's own WHERE) and `GET /covers/{bookID}?w=` (`book.view`
before any file is opened, thumbnail fallback to the next size then the original, ETag per size, immutable
cache only with the matching `v`, 405 for other methods, cover paths confined to `<data>/covers/` and
`<data>/cache/thumbs/`). Checks: `make generate` (no diff), `make check`, `make build`,
`CGO_ENABLED=0 go build ./...`, `make visual`, `-race -count=3` on library and web (browse and cover tests:
17 library, 12 web). Limits: no new indexes (migration 0004 belongs to P1b-3; P1b-9 measures the 300 ms
target), no page uses these reads until P1b-5.

**P1b-3 status**: done. Scope: migration 0004 (`books_fts` contentless FTS5 with `books_fts_ids` rowids, `books_fts_src` view, `books_fts_vocab`, 11 triggers on books, book_people, book_series, people and series names, initial fill); `library.Search` (quoted prefix terms, bm25 weights, total capped at 1000, did-you-mean over the vocabulary with a visible-hit check), `library.Suggest` (books, authors, narrators, series), `RebuildSearch`; all through `Listable` and `Scope` in SQL. Checks: `make check`, `make build`, `CGO_ENABLED=0 go build ./...`, `make test-scan-real`, `go test -race -count=3` on `internal/db` and `internal/library`; new tests cover triggers, cascade, consistency, query sanitising, ranking, scope, refused and missing books, suggestions. Limits: did-you-mean suggestions come from the folded vocabulary (for example "tomas", not "Tomás"); the tokeniser is language-agnostic, no stemming or synonyms; Suggest name matching lowercases ASCII only. Next: P1b-6 builds the search page and `/hx/search/suggest` on this.

**P1b-4 status**: done (Libraries admin and setup steps 2 and 3). Scope: `/admin/libraries` (folders with
book counts and sizes, Scan now per library or for all, reusing a scan that is already waiting; "things need a
look" for protected, damaged, unplayable, missing and cover-less books), `/admin/libraries/folders/new`
(typed absolute path, checked for existence, readability, the data directory and overlap; library and folder
created in one transaction), the "emptied on purpose" confirmation (marks books missing only when the folder
and every subfolder were read and no audio is left, checked again on POST, 409 otherwise), the scan status
fragment `GET /hx/jobs/{id}` (polls every 2 s until the scan is over and every file has been read), and the
owner-only setup steps `/setup/server`, `/setup/folder`, `/setup/done`. Reused: access helpers,
`library.Scope`, `jobs.Runner`, `scan` walk, Frame, SetupSteps, TextField. Extended: AddFolder and
CreateLibrary (bodies shared), `components.Page` (Styles, Admin), Sidebar ("Your server" group), Frame
(page styles). Checks: unit tests in library, jobs, scan and web; `make check`, `make build`, `make visual`
at 390/768/1024/1440 in four themes, `make test-scan-real`. Limits: no Browse button (the next package adds a
folder browser beside the path field, id `folder`), no folder removal, watching or scheduled scans, scan
history, duplicate or "still copying" detection, cover lookup, or phone and tablet entry to the admin page
(P1b-7); OnboardDone has no cover wall (P1b-5). Next: P1b-5.

**P1b-fb status (folder browser)**: done. A Browse button beside the audiobook folder field opens a folder browser: a sheet on Add a folder, an inline panel on setup step 3, as the `?browse=<path>` mode of those two pages (picking links back with `?folder=<path>`; works without JavaScript). Folder names only plus one audio-file count for the open folder; symlinks never followed; the scan's skip list; the data folder and system folders hidden at the root; capped at 5,000 entries, 500 folders and 3 s, then it asks for the typed path. Owner and admin on Add a folder, owner on setup; the chosen path still passes the add-folder checks. Checks: `make check`, `make build`, `make visual` (5 pages x 4 themes x 4 widths, no overflow, 44 px targets, sheet fits). Limits: no per-folder counts, no folder creation, the library choice on Add a folder resets after Browse.

**P1b-5 status (Home and Library)**: done. Home greets the person and shows the 12 newest books as spines, with first-run, nothing-shared and scanning states. The Library lists all visible books or one library, grid or list, four sorts and up to five genre filters in the URL (Back works), a filter sheet below 1280 px and an inline bar above, infinite scroll with a no-JS "Show more" link. Every list goes through `ListBooks` with the person's grant scope; per-book colour comes through `data-ts-tint` (no inline styles; dropped below 4.5:1, ignored by the contrast theme). Checks: unit tests, `-race -count=3`, browser checks at four themes and four widths (overflow, 44 px targets, labels), the filter sheet closing through `hx-select-oob` confirmed in Chromium. CSS budget raised to 96 KB. Limits: no Continue listening hero, series, shared or because rows, no progress or Resume (P1c, P3; the one-resume rule is noted where the hero goes). **Left for the P1b-9 sweep** (lead review against `browse/LibraryPage.png`): Author, Narrator, Series and Length filters, a "Filter by title" field, "Clear filters" and the "N books match" line; the header line "books · folders · hours"; the "Show books" submit button hidden when JavaScript applies filters on change; "Unfinished" and "Downloaded" wait for P1c and P4; the OnboardDone cover wall.

**P1b-6 status (book, people and search pages)**: done. The book page has About, Chapters and Details tabs, each a canonical URL swapped into `#book-panel`, the series list and "Also narrated by"; author, narrator and series pages show visible books only; Search shows books, people and series with live results, an Everything/Books/People/Series filter, "Did you mean …?" and the "Nothing for …" state. Every read goes through the person's scope, and a book, person or series they cannot see answers 404 like an unknown id. Checks: `make check`, `make build`, `make visual` (about 17 s added), `-race -count=3`. Limits: no play, download, share or shelf controls (P1c, P4, P3, P1b-8; the play button's single slot is marked in `book.templ`); desktop uses a two-column layout rather than the board's full-width tab row; search totals cap at 1,000; no "Start series" link, per-book percentages or stacked series covers (need progress, P1c); person pages have no sort.

**P1b-7 status (Settings and Profile)**: done. Profile (avatar, role, devices with per-device sign out and "Sign out everywhere else", Libraries entry for owner and admin, sign out), Settings (typed `users.Settings` in `user_settings`, saved as you change it and without JavaScript; theme from `ui.theme`, which beats the `ts_theme` cookie, which beats the system; playback values stored for P1c), Change password (needs the current password, rotates this browser's session and signs out the others) and `tendshelf reset-password [-data DIR] <username>` (prints a generated password once, signs the account out everywhere, audit row). Checks: `make check`, `make build`, `make visual`, `-race -count=3` on users, auth, web and cmd. Lead fix: "Sign out everywhere else" aligned with the device rows. Limits: no stats, bookmarks or finished rows, no Users, Metadata or Research entries (P1c, P2, P3, P6), device rows share one icon; text size and the Larger text mode go with P3.

**P1b-8 status (personal shelves)**: done. Migration 0005 (`shelves`, `shelf_items`, names unique per person); `/shelves` (list and create) and `/shelves/{id}` (paging, rename, delete with a confirm, remove, move up and down with buttons); an "Add to shelf" sheet on the book page after the P1c play slot (not a play control, so the one-resume rule holds); the "Your shelves" sidebar group and a Profile row on phone. Every query filters on the owner and the person's book scope; guests, other people's shelves and unknown ids answer 404; at most 200 shelves and 5,000 books per shelf. Checks: `make check`, `make build`, `-race -count=3`, `make visual` (52 s) and `visual-full` for the shelf pages. Limits: sidebar counts refresh on the next full load; shared shelves (P3), drag ordering and adding from cards are out of scope; an empty shelf shows no cover placeholder (sweep).

**P1b-9 status (sweep and exit audit)**: done. 9a: Library filters (Author, Narrator, Series, Length, Filter by title, Clear filters, "N books match"), header line "books · folders · hours", results swapped in `#library-results`, "Show books" only without JavaScript, the setup cover wall, `make perf`, WebKit captures of eight pages; prerequisite B: per-load CSS budgets (core 72 KB, each page file 8 KB) and the fast/full visual matrices. 9b: `TestRouteMatrix` (47 patterns, 63 rows, 445 requests: anonymous, owner, admin, member and guest, plus every unsafe request without a CSRF token; member and guest answers checked for leaked titles, people, series and ids; a route without a row fails the test) found no access bug; the empty-shelf placeholder. Lead fixes: the setup done line leaves out zero counts. **P1b exit**: handler tests and forbidden cases for every route (route matrix); every built board has a capturing test (`docs/design/screens.md`); Library interactive within budget (median 64 ms at 4x, 240 ms at 8x CPU slowdown on the lead's machine, 2,000 books; not yet measured on a real phone). Nightly CI (`.gitea/workflows/nightly.yml`) is configured; its first run passed the pinned-Chromium checksum and then failed on the Chrome library list (fixed), and the next two runs could not check out the repository from the amd64 runner (connection reset by the Git server), an infrastructure issue outside the repository.

### P1c Player and progress

- Mini-player, full player (phone, tablet) and desktop player bar and panel; cover accent only on the
  player root.
- Player module (`static/js/player.js`): multi-file timeline, chapters, speed 0.5-3.0, skip intervals,
  sleep timer with fade and +10 min, auto-rewind after pause with its hint, Media Session, keyboard
  shortcuts, progress sync every 10 s and on pause/hide (`sendBeacon`) firing `progress-saved`, mark
  finished, "continue on the other device?" prompt, bookmarks and notes.

Exit: play, seek across a file boundary, speed, sleep timer and resume on a second browser all work;
first sound under 1 s on localhost; the player is usable one-handed at 390 px (recorded manual check,
the table in [`docs/manual-checks.md`](manual-checks.md)).

**P1c-1 status (progress data)**: done. Migration 0006 (`progress`, `bookmarks`, `listening_sessions`) and `internal/progress`: newest-wins compare-and-write in one SQL statement inside the single-writer transaction, client time clamped to server now + 60 s, explicit server acts (mark finished) always win, the listening session extended in the same transaction (operator: record sessions from P1c); scoped Home reads in `library/listening.go` (Continue listening, Your series in progress, Because you listened). Checks: `make check`, `-race -count=3`, `TestProgressConcurrentDevices` (2 devices × 50 goroutines); 55.8 µs per save on the lead's machine. Limit: no HTTP yet.

**P1c-2 status (streaming)**: done. `GET /stream/{audioFileID}` with Range, If-Range, HEAD and an ETag while the file matches its scan; access checked before any file is opened; refused, encrypted, unreadable, unprobed and missing files answer 404 without touching the disk; `?compat=1` serves a ready copy or requests one and answers 503 with `Retry-After: 3`. Lead fix: a library file swapped for a symlink after a scan is refused (Lstat + SameFile; the new test got 200 without the fix). Checks: `make check`, `make visual`, `-race -count=3`; 49 routes in the route matrix.

**P1c-3 status (progress and player HTTP)**: done. `GET|POST /progress/{bookID}` (JSON answer with `applied`, `progress` and `serverNow`; form-encoded saves with the CSRF token as header or `_csrf` body field for `sendBeacon`; 4 KiB cap; the server's book duration, never the client's), `POST /hx/books/{id}/finished`, `GET /hx/player` and `/hx/player/{bookID}` (a data-only `PlayerData` fragment: files with offsets, formats and playability, chapters, settings, stored position and device), and `streamable()` shared with `/stream`. A signed-out save gets the gate's 303 (operator decision via lead; the player treats a redirect as signed out). Checks: `make check`, `make visual`, `-race -count=3`. Limit: `HX-Trigger: progress-saved` on htmx 4 is checked in P1c-6.

**P1c-7a status (bookmarks)**: done. The book page's Bookmarks tab (shown to people who can listen): add ("Bookmark this moment" at the saved position until the player lands), note, delete, txt/md export; every action checks the bookmark is the person's own and the book still listenable (anything else 404). Checks: `make check`, `make visual`, `visual-full` for the tab, route matrix with a note-leak word. Limits: day words use the server's time zone; "Play from here" and the live toast wait for P1c-5; no highlights or quotes until R4. Route matrix after both: 58 patterns, 82 rows, 580 requests.

**P1c-4 status (player core)**: done. `static/js/player.js` (own module): the one `<audio>` outside `#main`, the `data-ts-play` click protocol (first sound inside the click), multi-file timeline with next-file warm-up, saves every 10 s / pause / seek / end and a `sendBeacon` on page hide with unsent saves kept in localStorage, signed-out stop, "Continue there?", auto-rewind after a break with its hint, compat polling, Media Session (8 actions, artwork), keys and the `?` sheet; the mini-player and desktop bar; `player.css` with its own budget. Measured with `make player` (Chromium over CDP, no Node): first sound 274 ms median, file switch 266 ms. Checks: `make check`, `make visual`, `make player`, `-race -count=3`. Review note: player.js was squeezed to fit a raw-size budget; prerequisite C moves budgets to gzip bytes and rewrites it for reading. Limits: no scrubber, speed, sleep or full player yet (P1c-5).

**Prerequisite C status (compressed static files)**: done. CSS, JS, SVG, TXT and the vendored htmx are gzipped once at start-up (deterministic, kept only when smaller) and served on `Accept-Encoding` with `Vary`, an ETag per encoding and 304; Range is ignored on `/static/`. Size budgets now measure gzip bytes, and AGENTS.md says code is written for reading; `player.js` was rewritten for reading (937 lines, 90 comment lines; 10.2 KB gzip, budget 11 KB) with no behaviour change (first sound 265 ms, file switch 266 ms). Lead fix: `player.js` loads as an async module, since a plain module delayed every page (`make perf` at 4x: 64 → 135 ms; now 70 ms; 8x: 296 ms, close to the 300 ms budget — P1c-8 to widen the margin). Limit: gzip only.

**P1c-5a status (full player)**: done. `static/js/player-full.js` (its own async module, through `window.tsPlayer`, never the media element): the full player as a modal `<dialog>` on phone and tablet and a side panel on desktop, the book-wide scrubber with chapter ticks and a labelled range input, the chapters sheet, the cover accent carried as a tint in the player data and set through the CSSOM, and swipe down to close. Checks: `make check`, `make visual`, `make player` (`TestFullPlayerBrowser*`: open and close with focus kept inside, scrubber, times and chapters, layout at four widths). Limits: speed, sleep timer, bookmarks from the player and the output-device item are P1c-5b.

**P1c-6 status (play buttons and Home)**: done. Play buttons on the book page (resume or start, with each chapter's play), the Home "Continue listening" hero and rows, and the Library "Show" filter. Play links keep `hx-boost="false"` so a click that lands before `player.js` loads follows the plain `/stream` fallback. The one-resume rule: while the hero's Resume is on screen the mini-player is hidden. `HX-Trigger: progress-saved` verified on htmx 4. First sound 270 ms from the book page and from Home. Lead fix at merge: the full-player browser tests open the player from the Library, since the hero hides the bar on Home. Checks on the merged tree: `make check`, `make visual`, `make player`, `make perf` (72 ms at 4x).

**P1c-5b status (speed, sleep timer, volume, bookmark key)**: done. The full player's Speed, Sleep and Bookmark controls work. Speed: presets, a fine slider and a long press on the bar's open link; saved through `POST /me/settings` (debounced 1 s, flushed on page hide), with pitch kept. Sleep timer: end of chapter, 15, 30 or 60 min, a custom minute count, Turn off; it counts only while sound plays, fades `audio.volume` over the last 10 s without touching the person's volume, shows the "Sleeping in N seconds" card with "+10 min", and a shake adds 10 minutes. Volume is per device (a slider in the desktop bar). `B` and the bookmark buttons save the live position and show a toast with "Add a note" (the JSON now carries `noteHref`); toasts appear inside the open full player; the Bookmarks tab form shows the live place. No new route, table or setting. Gzip: player.js 11.0 KB, player-full.js 11.0 KB (budgets 12 KB each, total app JS 28 KB), player.css 3.4 KB. Lead fix at merge (a 5a bug the hints captures showed): `.ts-iconbtn:hover`/`:active` outranked `.ts-iconbtn--cover`, so the big play button lost its circle under the pointer and, through the touch-screen reset in `motion.css`, after every tap on a phone; the cover (and tonal) buttons now keep their fill, and the hints test checks it. Checks: `make check`, `make build`, `make player`, `make visual`, `make perf`. Limits: no fade or volume slider on iOS (it ignores `audio.volume`); the timer does not survive a reload; Custom has a "Start timer" button the board does not (declared deviation); WebKit and a real phone not yet checked. Next: P1c-8.

**P1c-8 status (exit sweep)**: done, except the manual phone check. "Left in book" uses the Home cards' word forms ("2 m", "2 h", "1 h 5 m") built from one set of Go strings, with a drift test; the live countdown rounds up. The full player's subtitle reads "chapter title · author" (chapter in serif italic; the author alone for an untitled chapter or a one-chapter book). The bar's sleep card is a fixed sibling of `#player`, so a fade shows on every page, including Home while the hero hides the bar, without covering the hero's Resume. The full-screen player now reaches the screen's edge where scrollbars take room (768-1279 px). Not built: the output-device item (no portable browser API; declared in screens.md); the two-column book-page player is deferred to M3 (no board). Tablet captures compared with the boards: no other defects. Gzip: player.js 11.0 KB, player-full.js 11.3 KB, player.css 3.6 KB. First sound 273 ms; `make perf` 92 ms at 4x (the gate), 309 ms at 8x (a stress figure only). Exit items and their proof: play and file boundary `TestPlayerBrowserFlow`, first sound `TestPlayerBrowserFirstSound`/`TestPlayButtonBrowserFirstSound`, speed `TestFullPlayerBrowserSpeed`, sleep `TestFullPlayerBrowserSleep`/`SleepCardOnHome`, second browser `TestPlayerBrowserResume`/`ContinueThere`, one resume `TestPlayerBrowserOneResume`/`TestHomeHeroBrowserOneResume`; one-handed use at 390 px is the manual check in [`docs/manual-checks.md`](manual-checks.md) (not yet run; shake needs HTTPS). Limits: the WebKit harness does not cover player pages; shake, the lock screen and iOS volume are manual only. Next: the operator's phone check closes P1c and M1; then M2 (Family).

**W1 status (waveform peaks, Go side)**: done. Job `media.peaks`, the cache `<data>/cache/peaks/<quick_hash>-v1.peaks` (one byte per 500 ms, RMS in dBFS from -60..0 dB; no table, no migration), `Tools.Peaks` (streaming reducer, mono 8 kHz PCM, one decoder thread, reniced), `jobs.Runner.Expedite`, a scan follower, a start-up `Backfill` and `Sweep`, `GET /peaks/{audioFileID}` (authorised like `/stream/`, 404 with `Retry-After: 30` while being made) and the flag `-waveforms` (env `TENDSHELF_WAVEFORMS`, default on). Checks: `make check`, `make build`, the route matrix rows (all roles, granted pass), the opt-in real-ffmpeg test (`TENDSHELF_TEST_FFMPEG=1`: a 6 s AAC book gives 13 bins and a 29 byte file in 23 ms). Limits: nothing draws the peaks yet (W2: `waveform.js`, the scrubber markup and CSS); no offline caching of peaks; a mid-file change that keeps the size and the first and last MiB keeps the old peaks (cosmetic). Next: W2.

**W2 status (waveform drawing, client side)**: done. `js/waveform.js` (3.0 KB gzip; the plan said 2 KB, the version written for reading is larger, so its budget is 3.5 KB and the app total 30 KB) draws two stacked inline SVGs of bars (2 px bar, 2 px gap, `max(8, width/4)` bars) in `[data-ts-wave]`, the first child of every `Scrubber` (full player and the desktop bar's compact scrubber). The picture is `aria-hidden`; the played copy is clipped by CSS from the scrubber's `--ts-pos` (the in-chapter fraction of decision 0005), in the scrubber's own colour (the cover accent in the full player); the contrast theme uses ink and the strong line, and forced-colors hides it. It redraws on chapter change, file change and resize only, never per second, and draws nothing while `/peaks/` answers 404 (one retry after `Retry-After`, then none for that file); a chapter spanning two files waits for both. `player.js` `info()` also returns the file list. `TestWaveformBrowser` (390 and 1440, in `make player`). Limits: captures of the full player with a waveform at 390, 768, 1024 and 1440 were not taken (`make visual` has no peaks, so its pictures are unchanged).

**Book page rebuild (B1 data and markup, B2 CSS), status**: done. `/books/{id}` follows BookPhone, BookTablet and BookDesktop at 390, 768-1279 and 1280+: tinted band (cover tint at a server-verified strength per theme, plain in contrast), progress ruler and where-line in the play region, series tiles with "Start series", the narrator rail, and at 1440 the chapters on `/books/{id}` with the rails beside every tab. Checks: `TestVisualBookPages` four themes at 390, 768, 1024 and 1440 (no overflow, no small targets, no inline style), `TestBookPageBrowser` (desktop and phone layout, tab swaps keep `#book-play`, one resume control while playing, band gradient in dark and none in contrast), `make check`, `make build`, `make player`. Limits: no Download, Share, two-column full player or tablet metadata editor (M3, M4); the overline is not refreshed on a progress save. Next: translations of the new strings.

## M2 Family (P3 multi-user, invitations, sharing)

**P3-1 status (authorization core)**: built. Migration 0007 adds `grants` (scopes all, library, shelf, book; one per person and target; optional end, download and read flags), `invites`, `share_links` and `devices` with their constraints, so the later M2 packages add no migration. `share.Can`, `BookFilter` and `LibraryFilter` now decide member and guest access from active grants in SQL (the same fragments for both, so a listing and `Can` cannot disagree); a shelf grant follows the shelf and needs an owner or admin to own it; `ViewShelf` and `TargetUser` (`users.manage`) are in place; a database error is a refusal. `share.Grants` (`Put`, `Revoke`, `Get`, `ForUser`) writes grants and the audit log. Checks: Go tests for the migration, the role-by-action-by-target-by-grant matrix, Can-versus-filter agreement, expiry boundary, shelf follow-through, store errors, the repository; `TestGrantScopeHidesEverything` (every scoped library read with a real member scope), `TestRouteMatrixGranted` and `TestGrantLeaks` (web). Limits: no screen or route uses grants yet (users, invites, share links, devices and the API are P3-2 to P3-9); `SharedWithYou` is not built; the did-you-mean oracle (a word found only in hidden books can suppress a correction) is left for P3-13. Next: P3-2, the users repository and admin page.

**P3-2a status (Users admin: Go and markup)**: built, without CSS (P3-2b). `GET /admin/users`, `/hx/admin/users`, `/admin/users/{id}` and the role, disable, enable, grants (add, update, renew, remove) and stop-sharing routes; every decision through `share.Can(ManageUsers)`, 404 on refusal, one write transaction per change that re-reads actor and target (`recheckManage`). `users.SetRole/SetDisabled/ByIDTx`, `auth.Sessions.EndAllForUser`, `share.Grants.People/ForUserNamed/ShelfTargets/RevokeAll`, `share.WaitingInvites`. Sidebar "Users & invites" with "{n} waiting" and a profile row. Checks: unit tests per package, `users_admin_test.go`, route matrix rows. Limits: "Invite someone", the Invites tab and the Devices tab wait for P3-3, P3-4 and P3-7; the sidebar count refreshes on a full load only; book counts are raw (refused or missing files included). Next: P3-2b (CSS and captures).

**P3-2b status (Users admin: CSS, captures, browser test)**: built. `ts-users__*` rules in `pages-admin.css` (phone list, table from 768 with the Devices column hidden at 768-1023, person page and grant cards, real-checkbox switches); `TestVisualUsersPages` (list, person, add-grant form, both confirm pages, four themes at four widths); `TestUsersGrantEditorBrowser` at 390 and 1440 (renew, add with end date, download and date change, remove, stop sharing, each checked with the person's own session; 44 px targets, focus ring). Limits: the person page and confirm pages have no board and wait for operator review; at 768 long names wrap in the table. Next: P3-3.

**P3-3a status (Invites: Go, markup, tests)**: built, without CSS beyond one working rule (P3-3b). No migration: the 0007 `invites` table is used as is. `share.Invites` (Create, Lookup, Featured, Rotate, Revoke, Pending, Accept, `WaitingInvites` by role) stores only the SHA-256 of a link and re-checks the inviter with `Can(ManageUsers, ...)` on a fresh row inside the write transaction at every step, Accept included; Accept is one `BEGIN IMMEDIATE` (claim, account, grants, audit), so eight parallel accepts of a one-use link give one account. `auth.Service.AcceptInvite` starts the session and shares the sign-in limiter (20 failures per 15 minutes per address); an invited account has no password. Routes: public `GET/POST /invite/{token}` and `GET /invite/{token}/cover` (the only new public prefix), `GET/POST /welcome` ("Keep your place": add a name and password, or keep this phone), `POST /admin/invites/{id}/link` and `/cancel`; the pending-invite card sits in `#users-invites` on the People tab. Unknown, revoked, replaced and refused links give one byte-identical 404; expired and used ones 410; tokens never reach logs (`logPath`) or referrers. Token primitives moved to the leaf package `internal/token` (`auth.NewToken` and `HashToken` delegate). Session renewal needed no change: `Sessions.Lookup` already pushes expiry to now + 30 days once an hour of use, so a password-less account stays signed in while it is used; Profile shows "Keep your place / Add a password so you can sign in elsewhere" for it. Declared differences from the board: "Use my fingerprint or face" (passkeys) is left out, and the "Listening keeps going while you choose" footer (no player on that page). Limits: no invite form (P3-4), no email (P3-8), no copy-link script (P3-12). Next: P3-3b (styles, captures, browser test).

**P3-3b status (Invites: CSS, captures, browser test)**: built. `pages-invite.css` (1.3 KB gzip, loaded only by the invite and welcome pages; core CSS unchanged) styles the invite page, its state pages, the signed-in notice and "Keep your place" (custom radio cards with a `:has(:checked)` ring, fields hidden while "Just this phone" is on); the pending-invite card in `pages-admin.css` (+0.3 KB gzip) now sits under the people table as on the board. `ts-btn--ghost` (no such style existed) became `ts-btn--quiet`. Checks: `TestVisualInvitePages` (invite, ended, used, unknown, signed-in, keep and Users card at all four widths and themes: no overflow, targets at least 44 px, labels, one h1) and `TestInviteAcceptBrowser` at 390 and 1440 (open link signed out, accept, land on the book, keep with a password, sign out, sign in again; the "just this phone" path leaves no password). Declared differences are in `docs/design/screens.md`. Reworked after review: server-name brand, avatar and first name, italic title, book block, tinted ground, play and lock/phone/link icons (three added to the icon set).

**P3-4a status (Share & invite page: Go, markup, tests)**: built, with a plain layout (finish and captures are P3-4b). No migration, no change to `share.Can`. `GET/POST /share` and `GET/POST /books/{id}/share` (owner and admins; others 404), the invite link shown once in the POST answer (no-store, no-referrer), `back` accepted on the invite link/cancel routes and the stop-sharing confirm and grant-remove only when it is one of the two share paths. On a book's page "Stop sharing" removes only the grant that gives that book; a wider share is explained and links to the person's page. New: `Grants.CoveringGrants/CoveringBook/TargetNames`, `Invites.PendingCovering`, `ShelfTarget.OwnerID`, the `mail` icon, Share link on the book page (staff), "Invite someone" on Users. Checks: share and web tests (route matrix rows for the four routes), `make check`, `make build`. Limits: no Email it (P3-8), no `/s/` links (P3-5), counts do not update live. Next: P3-4b (CSS, captures, browser test).

**P3-6a status ("Shared with you" and shared shelves: Go, markup, tests)**: built, without CSS (P3-6b). No migration (`shelves.shared` is from 0005). New: `share.SharedBooks`, `share.SharerSQL`, `library.SharedWithYou`, `SharedShelves`, `ShelfByID`, `SetShelfShared`, `Scope.Shared`, the Library "Show" value `shared`, the Home row (`#row-shared`, three cards, beside the series row in `ts-home__pair`), the "Shared with you" section on `/shelves`, the read-only shared shelf view and `POST /shelves/{id}/share`. Checks: `go test -race` on share, library, web; route matrix row for the toggle; leak tests (hidden books never appear in the row, counts, names or shelves). Limits: no styling yet; the caption says "from {names}" only (no shelf name); "3 new this week" dropped. Next: P3-6b.

**P3-6b status ("Shared with you" and shared shelves: CSS, captures, browser test)**: built. `pages-home.css` lays the pair (side by side from 1024, stacked below; three-card grid; phone caption under the title), `pages-shelves.css` the Shared with you section, the "Shared by" line and the share button. The caption now reads "and 1 other" for three sharers (table test for 1, 2, 3 and 5). Checks: `TestVisualHomeShared` (overflow, 44 px targets, one h1, the row; Home for a member and for a guest, shelves list, shelf read-only and for its sharer; every theme and width under `make visual-full`) and `TestHomeSharedBrowser` at 390 and 1440 (share with one button, the row, caption, keyboard to See all, read-only shelf, stop sharing, a guest with no New on the shelf, series and shared tops equal). Limits: Larger text and Rosa's two-card grid are P3-11; the covers in the captures are the test generator's flat ones. Next: P3-5 onward.

**P3-5b status (Share links: CSS, player link mode, browser and visual tests)**: built. With `data-ts-link` on the body, `player.js` prefixes `/stream/` with the link path, reads the book from the inline `#player-data`, keeps the place in `localStorage["ts.link.place.<bookId>"]` and never calls `/progress`, the Continue-there check or the pending flush; `player-full.js` skips `/me/settings` and bookmarks; `waveform.js` prefixes `/peaks/`. The page's Play becomes Resume (new player message `resume`). CSS: `.ts-linkshell`/`ts-link*` in `pages-invite.css`, `ts-share__link*` in `pages-share.css`. `TestShareLinkBrowser` (390, 1440) proves the whole life of a link and that the signed-out browser requested nothing outside `/s/{token}` and `/static/`; `TestVisualLinkPages` captures open, locked, broken and the Share page's link section. Limits: seeking in the test uses `tsPlayer.seek`; the sleep card on a phone link page still sits above a tab bar that is not there (player.css). JS total budget raised from 34 to 35 KB (about 0.65 KB of link mode), to be approved by the operator.

**P3-7a status (Device tokens: Go, routes, tests)**: built, without the UI (P3-7b). No migration (the `devices` table of 0007). `internal/auth/devices.go` (issue, lookup, list, revoke; idle expiry 180 days, cap 50, last_seen throttled to an hour), `internal/api` (JSON helpers), `internal/web/bearer.go` (mixed credentials refused, bearer only under `/api/v1/`), `routes_api.go` (`POST|DELETE /api/v1/auth/token`, `GET /api/v1/me`, JSON 404 for `/api/`), `POST /me/devices/{did}/signout`, `POST /admin/users/{id}/devices/{did}/signout` (device id or session public id). Change password, "Sign out everywhere else" and `reset-password` now also revoke device tokens. CSRF gained `Exempt`; the gate answers JSON 401 under `/api/`. Checks: auth, api and web tests (route matrix rows for all six patterns, `TestBearerMatrix`), `make check`, `make build`. Limits: no pairing code, no CORS, no renaming. Next: P3-7b ("Your devices" on `/me`, Devices tab on Users, browser tests, captures).

**P3-7b status (Device tokens: UI)**: built. "Your devices" on `/me` lists browsers and device tokens together (this device first, then by last use; laptop or phone icon; "Phone app · Not used yet"; Sign out per row; "Sign out everywhere else"). Users page: "Devices · N" tab and `#users-devices` card, per person, Sign out only where the viewer may manage the person. New icon `laptop`; labels "This device" (was "This browser") and "Not used yet". Checks: `TestProfileDevicesMerged`, `TestUsersDevicesTab`, browser tests `TestDevicesBrowser`, `TestDevicesAdminBrowser` at 390 and 1440, `TestVisualMePages` and `TestVisualUsersPages` full matrix, `make check`, `make build`. Limits: the tab is a link to the card under the table, not a separate view; no renaming.

**P3-4b status (Share & invite page: CSS, captures, browser test)**: built. `pages-share.css` styles the page at 390, 768, 1024 and 1440 in four themes (radio cards painted with `:has(:checked)`, expiry chips, the switch, the people list on the ground, the Safe by default card; two columns from 1024). The book page's Share control follows the Add to shelf control (a labelled segment on a phone, an icon button on a tablet, a bordered button at 1280+); the Users header carries Invite someone. Checks: `TestVisualSharePages` (overflow, 44 px targets, labels, one h1; every theme and width under `make visual-full`) and `TestShareBrowser` at 390 and 1440 (create the link for one book, open it in a second browser, accept, the new person opens that book and not another, Stop sharing from the book's page removes the access; Email it disabled and announced; tab order and focus rings). Limits: no phone subtitle, no Change picker, single column below 1024. Next: P3-5 (share links).


- Roles owner/admin/member/guest; users admin page; grants per library and per book with expiry and
  download permission; `share.Can` with a role-by-action test matrix.
- Invites by link and optional email (SMTP settings); share links for one book; Rosa's invite flow and
  large-text Home (`screens/people/Invite*`, `RosaHome`); Share & invite pages.
- Device tokens and the JSON API v1; shared shelves; "Shared with you" Home row.
- Larger text mode (operator, 2026-11-01): the in-app setting for Rosa's large-text design (bigger type,
  spines become cards, the player's settings bar stacks). It first moves the design system's fixed pixel sizes
  to scalable units across `bundle.css`, then adds the setting to `users.Settings`. Until then browser zoom
  to 200 % is the supported path (WCAG 1.4.4).

Exit: for every route, a test proves each role gets exactly the documented access, and forbidden books
never appear in lists, counts, search or covers. First tagged release (v2026.1.0, [decision 0006](decisions/0006-version-numbers.md)) after this milestone.

## M3 Pocket (P4 PWA offline and polish)

- Manifest, service worker, install prompt, offline downloads with persistent storage, offline queue,
  Downloads tab and offline banner (`screens/browse/DownloadsPhone`, `screens/states/StateOffline`).
- Listening statistics, Eyes-free (car) mode, shake to extend sleep (where permitted), scheduled
  database backups.
- Accessibility audit (WCAG 2.2 AA, contrast theme 7:1) and performance budget checks at the four widths.
- Deferred from P1c: the two-column full player on the book's own page at tablet and desktop. It has no
  board, only a line in `HANDOFF.md`; P1c ships the 400-wide side panel from 1280 px and the full-screen
  player below it. Design a board first, then build it.

Exit: a downloaded book plays in airplane mode on Android Chrome and iOS Safari (recorded manual
check), and offline progress syncs on reconnect.

## M4 Details (P2 metadata providers, covers, write-to-file)

- Sidecars: `.opf`, `.nfo`, `desc.txt`, `reader.txt`, `metadata.json`; precedence names < tags <
  sidecars < user/online choices.
- Online lookup (explicit button): Audnexus by ASIN (other Audible regions), Audible catalogue,
  Google Books, Open Library, iTunes; scoring, cache, spacing; field-by-field compare sheet and apply
  (`screens/core/ComparePhone`).
- Covers: online (fetched by the server), upload, choose; author photos and bios from Audnexus.
- Write details into the file with verification, backup, "Looks good" and Undo; restart recovery.

Exit: ported parser tables from the Extractor tests pass in Go; write-details round-trip on synthetic
m4b, mp3 and flac with real ffmpeg (`TENDSHELF_TEST_FFMPEG`) keeps the audio hash; a crash injected at
each `file_writes` state recovers correctly.

## M5 Read along (R)

Integrated into the book, player, position, sharing and offline downloads; not a second app. Comes
after M2 and M3 so R5 lands whole. Design: [read-along.md](read-along.md); boards: `screens/reader/`.

- **R1 Ebook ingestion**: `internal/ebook`, migration `ebooks` (`ebook_files`, `editions`,
  `edition_chapters`); scanner finds ebooks in book folders; upload on `/books/{id}/text`;
  copy-protection refusal; parsers for EPUB 2/3, TXT/Markdown, HTML, DOCX, FB2 first, then PDF (text
  layer, reflow check, page fallback) and DRM-free MOBI/AZW3; canonical document with stable IDs;
  `ebook.ingest` job. Exit: every parser and refusal fixture passes; same file gives byte-identical
  document; hostile-file tests pass; the Text page lists chapters and first lines.
- **R2 Pairing**: automatic by folder, upload, ISBN/ASIN, title + author (suggestion when not unique);
  manual pair/unpair; `/admin/ebooks` inbox; several editions, book default, per-user choice. Exit:
  pairing table tests; an unpaired ebook never appears as a book; no ebook is ever written.
- **R3 Alignment job**: `internal/align` and `internal/align/runtime`, migration `alignments`
  (`runtime_components`, `alignments`, `alignment_chapters`); `runtimes/align/worker.py`; `/admin/runtimes`
  check; emissions cache; mapping, forced alignment, confidence, failure labels; resume, cancel, hold;
  re-align changed chapters only. Exit: synthetic-emission accuracy (clean at least 99 %, perturbed at
  least 95 % of sentences within ±250 ms, zero invented times); job crash/resume tests; opt-in real run
  on a public-domain recording records the ±250 ms share, CPU time per audio hour and storage per book
  here (replacing the guesses in read-along.md).
- **R4 Read-along UI**: `/books/{id}/read` with Listen, Read along, Read, Focus line and Eyes-free;
  `static/js/readalong.js`; auto-scroll, Back to the voice, tap to play; reading settings (Fraunces,
  Fira Sans, OpenDyslexic); migration `highlights` (`highlights`, `bookmarks.edition_id/sid`,
  `notes.highlight_id`); highlights, bookmarks and notes in both directions. Exit: handler tests by
  role; contrast checked for every theme and highlight colour; side-by-side check at the four widths;
  highlight changes within one frame of a sentence start on a mid-range phone (measured).
- **R5 Cross-format position**: `progress.position_source/text_*`; Read mode drives the position;
  edition switch maps through time or quote; `can_read` on grants and share links with the P3 matrix;
  offline text download with P4. Exit: read on one browser, press play on another, the voice starts at
  that sentence; guest without `can_read` gets 404 on every text route; airplane-mode read-along check.
- **R6 Media Overlays**: import SMIL when the audio matches, otherwise align; export a new EPUB 3.3 with
  sentence-level overlays. Exit: import fixtures (match used, mismatch ignored); export then re-import
  within 1 ms; opt-in epubcheck passes.

## M6 Desktop (P5 Wails)

- `desktop/` module, Wails v3 pinned; connect-to-server and personal modes; media keys; tray.

Exit: Linux build runs both modes in an isolated session; other platforms recorded as not verified.

## M7 Research (P6 module port)

- Port the TendVox Extractor: clip extraction, categories, measurements, coverage, keep-numbers/delete-
  clips, `tendvox-reference-styles` v1 export; off by default, admin-only, fair-practice rules; the
  Research door and lab from `screens/admin/Research*` with the `lab-*` tokens.

Exit: the Go export matches the Extractor's export for the same synthetic fixture within stated
tolerances; disabled module returns 404 for every research route.

## Later

TV app (its own layouts, in the spirit of Apple TV and LG apps; not designed yet), native mobile
(evaluate Wails v3 mobile when stable), OPDS, podcasts, RSS feeds of books/series, passkeys, OpenID
Connect, folder watcher, merge MP3 folder to M4B, custom metadata providers, Chromecast.
