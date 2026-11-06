# Roadmap

Work is grouped into milestones; each ends with something usable. A milestone is a sequence of phases
(P = server and listening app, R = reader), and each phase ships as packages of about one agent
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
| M1 It plays | P1a-P1c | **Done 2026-11-03.** P1a, P1b and P1c built; the operator's phone check (Android Chrome, http on the LAN) passed on 2026-11-02/03: steps 1-8 and 10-14 pass, shake skipped (needs HTTPS); the four findings (tab bar, player fit, sheet drag-down, toast timeout) fixed and rechecked on the phone. iOS Safari not yet checked. | On main: `make check`, `make build` (CGO_ENABLED=0), real-ffmpeg targets, `make visual` (about 55 s) and `make visual-full` (about 147 s), `make perf` (Library interactive in a median 64 ms at 4x CPU slowdown, 240 ms at 8x, budget 300 ms), `make visual-webkit`; `TestRouteMatrix` covers all 47 routes for every role. Board audit: `docs/design/screens.md`. Push CI green; nightly CI green (run 9052 onward). Player: `make player` (first sound about 270 ms). |
| M2 Family | P3 | **Done 2026-11-03, released as v2026.1.0.** P3-1 to P3-13, the desktop player bar and the Now playing panel; Research dropped (decision 0007). | The P3 status notes below; the route-matrix and leak-test table under "Exit check (P3-13)"; [CHANGELOG.md](../CHANGELOG.md). |
| M3 Pocket | P4 (M3a, M3b, M3c) | **Built 2026-11-03/04.** M3a offline pocket (proven in Chromium); M3b listening statistics, Eyes-free mode, shake to extend and scheduled backups; M3c accessibility audit and performance. **Operator checks open:** the airplane-mode check on iOS (Android Chrome ran 2026-11-04 over `http`, partial: recheck over HTTPS) and the Orca, TalkBack and VoiceOver table. | M3a, M3b and M3c status notes under M3 below; [manual-checks.md](manual-checks.md). |
| M4 Details | P2 | Done 2026-11-03 | [plan](plans/m4-details.md) |
| M5 Book reader | R1, R2, R4, X, B1-B3, RD1-RD4, G1-G7, L | **Built 2026-11-05** ([decision 0018](decisions/0018-book-reader.md), [plan](plans/m5-book-reader.md), [great reader](plans/m5-great-reader.md)). Ebooks as books with automatic pairing, the reader (pages, highlights, search, footnotes), offline reading, look-up and word list, reading stats, skim, comfort, print pages, series and shelves. Read-along was removed. **Operator checks open:** the reader on iPhone Safari and Android Chrome, and the offline-reading airplane check; WebKit was driven only in a scripted Wails window. | The M5 notes below; [manual-checks.md](manual-checks.md). |
| M6 Desktop | P5 | Built on Linux (2026-11-05). **Open:** the operator's own-desktop checks (sound, tray click, file chooser, `https` server); macOS and Windows not verified. | [plan](plans/m6-desktop.md), M6 status below |
| M7 Research | P6 | Dropped (decision 0007) | |
| M8 Android | A0-A10 | In progress. A0 (skeleton and playback proof) built 2026-11-06; see "M8 Android" below. | [decision 0024](decisions/0024-android-app.md), [plan](plans/android.md) |

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

Native feel status 2026-11-03 ([decision 0011](decisions/0011-native-feel.md), plan `docs/plans/native-feel.md`): built.
Scope: `overscroll-behavior-y: none` on `html` (the page locks while a modal dialog is open, because Safari needs a
scrolling box), selection scoped (chrome none, fields and content `text`, with the `-webkit-` prefixes), `:active`
state layers for every control and a `:hover`-after-tap guard that keeps them, `color-scheme` per theme, `theme-color`
and `format-detection` metas, `env(safe-area-inset-*)` on every fixed or sticky edge (tab bar, rail, sidebar, mini
player, toasts, sheets, full player, sign-in frame, book band), `scroll-padding` for the fixed bars, a 16 px email
textarea, contained inner scrollers. Budgets: core CSS 19.5 KB and `utilities.css` 2.5 KB, approved by the operator
2026-11-03. Checks: `static/native_test.go`, `components/head_meta_test.go`, `native_feel_browser_test.go` (Chromium with
`Emulation.setSafeAreaInsetsOverride`, portrait and landscape). Limits: the passive `<body>` touchstart listener for
iOS `:active` did not fit the JS budget (`shell.js` 20 bytes over); the existing `document` listener stays. Pending
manual checks on real devices: iPhone Safari and home-screen (no bounce, no callout on long-press, buttons darken on
touch-down, notch and home bar in both orientations, no zoom on the email textarea); Android Chrome (no
pull-to-refresh, toolbar takes the theme colour).

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

**P1b-7 status (Settings and Profile)**: done. Profile (avatar, role, devices with per-device sign out and "Sign out everywhere else", Libraries entry for owner and admin, sign out), Settings (typed `users.Settings` in `user_settings`, saved as you change it and without JavaScript; theme from `ui.theme`, which beats the `ts_theme` cookie, which beats the system; playback values stored for P1c), Change password (needs the current password, rotates this browser's session and signs out the others) and `tendshelf reset-password [-data DIR] <username>` (prints a generated password once, signs the account out everywhere, audit row). Checks: `make check`, `make build`, `make visual`, `-race -count=3` on users, auth, web and cmd. Lead fix: "Sign out everywhere else" aligned with the device rows. Limits: no stats, bookmarks or finished rows, no Users, Metadata or Research entries (P1c, P2, P3; Research dropped, decision 0007), device rows share one icon; text size and the Larger text mode go with P3.

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

**NP2 status (desktop Now playing panel: CSS, script, Home hero, browser tests, captures)**: built. `now-panel.css` (1.7 KB gzip, loaded on every page like `player.css`, layer `pages`) lays the panel under the full player's top from 1280 px (below it the panel is not displayed and never requested), hides the transport row and the old toolbar there, and gives the tabs, About, series, details, genres, more by and Bookmarks their styles. `now-panel.js` (1.0 KB gzip) loads `/hx/player/panel/{book}` when the player opens or the loaded book changes, puts "read by" and the extra menu items into the empty slots of the full player's top, keeps the tabs (`aria-selected`, roving tab index, arrows) and refreshes the Bookmarks count or tab after a bar bookmark. `player.js` makes the Home hero's button of the playing book "Show player" with the eyebrow "Now playing". Accent text in the player uses `--accent` (contrast fix). Budgets: all app JS 36 KB (operator, 2026-11-02), `now-panel.js` 1 KB, `now-panel.css` 3 KB. Checks: `TestNowPanelBrowser` at 1280, 1440 and 1920 (four-theme contrast, one scroll area, sticky tabs, About with Read more, series states, details, genres, more by, sparse book, tabs by keyboard, bookmark count, narrow widths, Home hero), `make player`. Limits: the contrast check measures against the panel's solid surface; the cover tint behind the player's top is not sampled. Next: a Chapters tab only if wanted (out of scope).

**P3-6b status ("Shared with you" and shared shelves: CSS, captures, browser test)**: built. `pages-home.css` lays the pair (side by side from 1024, stacked below; three-card grid; phone caption under the title), `pages-shelves.css` the Shared with you section, the "Shared by" line and the share button. The caption now reads "and 1 other" for three sharers (table test for 1, 2, 3 and 5). Checks: `TestVisualHomeShared` (overflow, 44 px targets, one h1, the row; Home for a member and for a guest, shelves list, shelf read-only and for its sharer; every theme and width under `make visual-full`) and `TestHomeSharedBrowser` at 390 and 1440 (share with one button, the row, caption, keyboard to See all, read-only shelf, stop sharing, a guest with no New on the shelf, series and shared tops equal). Limits: Larger text and Rosa's two-card grid are P3-11; the covers in the captures are the test generator's flat ones. Next: P3-5 onward.

**P3-5b status (Share links: CSS, player link mode, browser and visual tests)**: built. With `data-ts-link` on the body, `player.js` prefixes `/stream/` with the link path, reads the book from the inline `#player-data`, keeps the place in `localStorage["ts.link.place.<bookId>"]` and never calls `/progress`, the Continue-there check or the pending flush; `player-full.js` skips `/me/settings` and bookmarks; `waveform.js` prefixes `/peaks/`. The page's Play becomes Resume (new player message `resume`). CSS: `.ts-linkshell`/`ts-link*` in `pages-invite.css`, `ts-share__link*` in `pages-share.css`. `TestShareLinkBrowser` (390, 1440) proves the whole life of a link and that the signed-out browser requested nothing outside `/s/{token}` and `/static/`; `TestVisualLinkPages` captures open, locked, broken and the Share page's link section. Limits: seeking in the test uses `tsPlayer.seek`; the sleep card on a phone link page still sits above a tab bar that is not there (player.css). JS total budget raised from 34 to 35 KB (about 0.65 KB of link mode), to be approved by the operator.

**P3-7a status (Device tokens: Go, routes, tests)**: built, without the UI (P3-7b). No migration (the `devices` table of 0007). `internal/auth/devices.go` (issue, lookup, list, revoke; idle expiry 180 days, cap 50, last_seen throttled to an hour), `internal/api` (JSON helpers), `internal/web/bearer.go` (mixed credentials refused, bearer only under `/api/v1/`), `routes_api.go` (`POST|DELETE /api/v1/auth/token`, `GET /api/v1/me`, JSON 404 for `/api/`), `POST /me/devices/{did}/signout`, `POST /admin/users/{id}/devices/{did}/signout` (device id or session public id). Change password, "Sign out everywhere else" and `reset-password` now also revoke device tokens. CSRF gained `Exempt`; the gate answers JSON 401 under `/api/`. Checks: auth, api and web tests (route matrix rows for all six patterns, `TestBearerMatrix`), `make check`, `make build`. Limits: no pairing code, no CORS, no renaming. Next: P3-7b ("Your devices" on `/me`, Devices tab on Users, browser tests, captures).

**P3-9a status (JSON API v1, read side)**: built. `internal/api` gains `ErrorField`, `List`/`NewList`, `WriteCached` (ETag, 304) and `StatusError`; the pages' `errorPage`, `notFound` and the media routes answer JSON under `/api/` (`plainError`), so every reused helper is API-safe. `internal/web/routes_api_read.go` and `api_views.go`: `GET /api/v1/libraries`, `books`, `libraries/{id}/books`, `books/{id}`, `search`, `shelves`, `shelves/{id}`, `shared`, and the token-reachable aliases `covers/{bookID}`, `stream/{audioFileID}`, `peaks/{audioFileID}` bound to the page handlers. Bearer requests are limited to 600 a minute per device. `docs/api.md` is the reference; `docs/architecture.md` lists the shipped and planned routes. Tests: `internal/api/json_test.go`, `api_read_test.go` (all endpoints, paging, bad limits, cursor tampering, error shapes, rate limit), `api_leak_test.go` (a closed library's book and a refused book absent from every list, count, search, shelf; closed ids byte-identical to unknown), route matrix rows (`routes_matrix_api_test.go`) with the granted pass. Next: P3-9b (progress and bookmark sync).

**P3-9b status (JSON API v1, sync side)**: built. Migration 0008 (`bookmarks.client_id`, unique per person) so a retried create is not saved twice. `GET /api/v1/progress?since=&cursor=` and `GET /api/v1/bookmarks?since=&cursor=` (keyset change feeds, scope-filtered in SQL by `library.ProgressChanges` / `BookmarkChanges`, tombstones included), `GET|PUT /api/v1/progress/{bookID}`, `POST /api/v1/progress/batch` (1 to 100, 64 KiB), `GET|POST /api/v1/books/{id}/bookmarks` (`since` gives that book's changes; `clientId` makes POST idempotent: repeat 200, other person or book 409), `PATCH|DELETE /api/v1/bookmarks/{id}`. PUT and batch reuse the player's `saveProgress` (extracted; the form route is unchanged) and `playableFor`, so the clamp and newest-wins rules have one implementation. Tests: `api_sync_test.go` (retry, 409, paging, tombstones, hidden and refused books, cursors, limits, session and bearer), route matrix rows incl. the granted pass, migration 0008 tests. Limits: no per-person ownership check of `clientId` beyond the 409; `serverNow` is the client's next `since`. Next: P3-9 acceptance and M2 close.

**P3-7b status (Device tokens: UI)**: built. "Your devices" on `/me` lists browsers and device tokens together (this device first, then by last use; laptop or phone icon; "Phone app · Not used yet"; Sign out per row; "Sign out everywhere else"). Users page: "Devices · N" tab and `#users-devices` card, per person, Sign out only where the viewer may manage the person. New icon `laptop`; labels "This device" (was "This browser") and "Not used yet". Checks: `TestProfileDevicesMerged`, `TestUsersDevicesTab`, browser tests `TestDevicesBrowser`, `TestDevicesAdminBrowser` at 390 and 1440, `TestVisualMePages` and `TestVisualUsersPages` full matrix, `make check`, `make build`. Limits: the tab is a link to the card under the table, not a separate view; no renaming.

**P3-4b status (Share & invite page: CSS, captures, browser test)**: built. `pages-share.css` styles the page at 390, 768, 1024 and 1440 in four themes (radio cards painted with `:has(:checked)`, expiry chips, the switch, the people list on the ground, the Safe by default card; two columns from 1024). The book page's Share control follows the Add to shelf control (a labelled segment on a phone, an icon button on a tablet, a bordered button at 1280+); the Users header carries Invite someone. Checks: `TestVisualSharePages` (overflow, 44 px targets, labels, one h1; every theme and width under `make visual-full`) and `TestShareBrowser` at 390 and 1440 (create the link for one book, open it in a second browser, accept, the new person opens that book and not another, Stop sharing from the book's page removes the access; Email it disabled and announced; tab order and focus rings). Limits: no phone subtitle, no Change picker, single column below 1024. Next: P3-5 (share links).


- Roles owner/admin/member/guest; users admin page; grants per library and per book with expiry and
  download permission; `share.Can` with a role-by-action test matrix.
- Invites by link and optional email (SMTP settings); share links for one book; Rosa's invite flow and
  large-text Home (`screens/people/Invite*`, `RosaHome`); Share & invite pages.
- Device tokens and the JSON API v1; shared shelves; "Shared with you" Home row.
- Larger text mode (operator, 2026-11-01): **P3-11 done** (11a and 11b). Settings, Easier to use, Text size: Standard or
  Larger (`ui.text_size`, root 125 %, stacks on the browser's own size). Larger uses the tablet shell from 960 px at every
  width (no sidebar, no desktop bar, no Now playing panel: its 1280 px block is guarded too), shows covers instead of
  spines on Home, a two-row player settings bar, fewer cover columns, wrapping tab strip and library toolbar.
  Checks (`TestVisualLargeText*`, 390/768/1024/1440, dark and light): no overflow, 44 px targets, the time left never cut
  (bar, mini player, hero, full player); Home and a book also at 640 and 720 px in both sizes (200 % zoom). Standard text
  proved unchanged by capturediff. Limits: browser-only large text on 1280-1599 px windows keeps the desktop bar
  (recorded risk, no detection script); the board's five-step slider is a declared difference (two choices); signed-out,
  invite and link pages follow the browser's text size only.

**P3-5a, P3-10 and the player packages (one line each)**: P3-5a built the share-link repository, limiter and public `/s/` routes (merged with P3-5b). P3-10 moved sizes to rem, 12 px control corners and the handoff's spine sizes. The desktop player bar (chapter waveform seek, times, hairline, tools; [decision 0005](decisions/0005-chapter-scrubber.md)) and the Now playing panel (NP1 fragments, NP2 panel) are merged, as is Larger text (P3-11).

**PB4 status (phone bubble, operator request 2026-11-03)**: built. The phone mini-player (below 768 px, and below 960 px with Larger text) is a round bubble at the bottom right instead of the full-width bar: cover in a circle, a progress ring of the book, a 44 px play button on its lower-left edge; one resume per screen and persistent-while-playing unchanged; tablets and desktop unchanged. CSS only (`player-bubble.css`, about 1 KB gzip, layer `pages`) plus one line in `player.js` that sets the first letter for cover-less books. Checks: `TestPhoneBubbleGeometry` (360, 390, 430 and 390 Larger text), `TestPhoneBubbleBehaviour`, `TestPhoneBubbleCaptures`, `TestPlayerBrowserLayout`. Limit: no pulse while playing (optional in the request); the Back button is not on the phone bubble (the full player has it).

**NR1 status (menu rail, operator request 2026-11-03)**: built. At 1280 px and up a toggle at the foot of the sidebar collapses it into a rail of large icons (labels kept as accessible names and hover titles, "Your shelves" hidden) and expands it again; the choice is the user setting `ui.nav` (`full` or `rail`, no migration), saved by `POST /me/nav` (plain form, redirects to a same-site referrer) or `POST /hx/me/nav` (returns the sidebar alone; it leaves out `hx-preserve`, which htmx 4 would otherwise honour by putting the old element back). No JavaScript: the layout follows `data-nav` on the sidebar through `:has()` and the `--size-side-rail` token. Tablet rail, phone tab bar and Larger text unchanged. Checks: `TestNav*`, route matrix rows, `TestNavRailBrowser` (width, #main shift, audio keeps playing, boosted navigation, reload, keyboard), `TestVisualShellNav` (both states, four themes, 1280 and 1440). Limit: the sidebar stops above the player bar while it shows, so the toggle is never covered.

**P3-13 status (M2 exit and release check)**: built 2026-11-02. No migration. Checks and fixes:

- Route matrix (`routes_matrix_test.go`): `TestRouteMatrix` fails when a registered route has no row or a row names a route that is not registered; at the exit it covers 134 registered patterns with 197 rows (110 GET, 87 unsafe; 1,420 requests: each GET as owner, admin, member, listener and a visitor, each unsafe row also without the CSRF token). `TestRouteMatrixGranted` repeats the book-serving rows once the member and the listener hold a library grant. Gaps found: none in the matrix. Gaps found and closed in `TestGrantLeaks`: it did not try the waveform peaks, the bookmark export, the book's Share page, the five Now playing panel fragments, or a closed book placed on the member's own shelf; they are in the closed list now.
- Copy audit: the Profile chip said "Guest" while the Users page says "Listener"; it says "Listener" now. No double spaces, dash or "Unknown" placeholders, exclamation marks, or "item/track/token" in the interface were found. 187 strings that are not in the copy deck are listed in [copy-additions.md](copy-additions.md) for review.
- Docs: README (install, first run, flags, data folder and backups, the phone check), architecture (M2 route list, tables to migration 0009, budgets), `api.md` linked, [CHANGELOG.md](../CHANGELOG.md).

Forbidden books never appear (the leak tests, by surface; "closed" is a book in a library the person has no grant on, or a refused book):

| Surface | Routes | Tests that prove it |
| --- | --- | --- |
| Every route, every role | all 134 patterns | `TestRouteMatrix` (Want per role, leak words scanned in every GET body), `TestRouteMatrixGranted` |
| Lists and counts | `/`, `/libraries`, `/libraries/{id}`, `/hx/libraries/items`, `/hx/libraries/{id}/items`, the filter sheet, `/shelves`, `/shelves/{id}`, `/hx/home/hero` | `TestGrantLeaks` (granted, revoked, expired; header counts "1 book" and "0 books"), `TestLibraryAccess`, `TestLibraryHidesRefusedAndMissing`, `TestHomeSharedNoLeak`, `TestShelvesAccess` |
| Search | `/search`, live results, did-you-mean | `TestGrantLeaks` (exact, typo and author queries), `TestSearchScope`, `TestAPILeaks` (`/api/v1/search`) |
| People and series pages | `/authors/{id}`, `/narrators/{id}`, `/series/{id}` | `TestGrantLeaks` (closed ids 404), matrix rows |
| Covers | `/covers/{bookID}`, `/invite/{token}/cover`, `/s/{token}/cover` | `TestCoverForbidden` (refusal identical to unknown, no file opened), `TestGrantLeaks`, `TestLinkMedia` |
| Stream and peaks | `/stream/{id}`, `/peaks/{id}` | `TestGrantLeaks`, `TestPeaksRefusals`, matrix rows (`second file`, `unknown file`) |
| Player and panel | `/hx/player/{bookID}`, `/hx/player/panel/{bookID}` and its four sub-routes, `/hx/books/{id}/play`, `/progress/{bookID}` | `TestGrantLeaks`, `TestNowPanelHidden`, `TestNowPanelRoles`, matrix rows |
| Bookmarks | `/books/{id}/bookmarks`, `/export`, `/bookmarks/{id}/...` | `TestGrantLeaks`, matrix rows (someone else's sheet) |
| JSON API | every `/api/v1` route | `TestAPILeaks` (lists, counts, search, shelves, shared; closed ids byte-identical to unknown), matrix rows with the granted pass |
| Shared with you, shared shelves | Home row, `/libraries?show=shared`, shelf pages | `TestHomeSharedNoLeak`, `TestShelfSharedView`, `TestShelvesSharedSection` |
| Share links | `/s/{token}` and its media | `TestLinkPage`, `TestLinkBrokenPagesAreIdentical`, `TestLinkMedia`, `TestLinkSessionChangesNothing` |

Exit: for every route, a test proves each role gets exactly the documented access, and forbidden books
never appear in lists, counts, search or covers. First tagged release (v2026.1.0, [decision 0006](decisions/0006-version-numbers.md)) after this milestone.

**P3-8 status (Email invites: P3-8a Go and markup, P3-8b styles, browser tests, captures)**: built. The Share page's "Email it" tab works with no server setup: "Send invitation" creates the invite and opens the person's own email app (`mailto:`), and the answer always shows the link and the message with "Copy link" and "Copy message". An optional SMTP relay (provider presets, owner-only `/admin/email`, "Send a test email") adds "Send it for me". `pages-email.css` holds the styles (tab links, answer panel, sent/failed lines, settings page; 3 KB budget). Checks: `TestShareEmailOwnAppBrowser` (mailto subject and body, clipboard text, no-JS answer), `TestShareEmailRelayBrowser` (preset, test, "Sent to"), `TestShareEmailRelayFailureBrowser` ("Couldn't send"), `TestVisualEmailPages` (four themes, four widths). Limits: no board exists for `/admin/email`; the settings page is built from existing form parts. Next: password-reset email on the same relay.

**P3-12 status (small client scripts)**: built. `js/copy.js` (about 1 KB gzip) shows the Copy buttons (hidden in the markup, because they do nothing without script), copies with the Clipboard API or a selection fallback and says "Copied" in a polite live region, and opens the email app once when the answer to "Send invitation" lands. The `/s/` place in `localStorage` was done in P3-5b. M3 note: when the service worker arrives it must never cache or intercept `/invite/` and `/s/` (invite tokens and share links are secrets and one-time).

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

**M3a status (Offline pocket)**: built and proven in Chromium, 2026-11-03; the Android phone check ran 2026-11-04 over `http` (partial: recheck over HTTPS), iOS is not run. Decision
[0012](decisions/0012-offline-pocket.md), plan [m3-offline.md](plans/m3-offline.md); packages A (server), C
(pages), B1 (worker and page module), B2 (download manager), D (proof). Built: `/sw.js`, the manifest and icons,
the `/offline` shell, chunked downloads with pause, resume and cancel, the Downloads page, the book page's
Download control, offline play from stored chunks, queued progress and bookmarks that sync once on reconnect,
sign-out wipe, "Remove finished books". Checks: `make check`, `make build`, `make player`, the TestOffline*
browser tests (worker, shell play, sync, sign-out, download, pause/resume/cancel, quota, compat copy, worker
update, remove finished, storage notes), captures of the Downloads page, the book page's Download states and the
offline shell at 390, 1024 and 1440 px in dark and light, beside the boards. Limits: the Chromium harness cannot
show a real phone's storage limits, iOS eviction or a real airplane mode; the takeover of a new worker right after
playback can wait until the page is left (decision 0012, notes); "Wi-Fi only", "Next in series" and "Other apps"
are deferred; the Wi-Fi-off icon of the board's banner is not drawn. Next: the operator rechecks the airplane-mode check over HTTPS and runs it on iOS
([manual-checks.md](manual-checks.md)). The rest of M3 (statistics, Eyes-free mode, backups, the accessibility audit) is built, below.

**M3b dashboard status (Your listening, package B: the page)**: built 2026-11-04. No migration, no new JavaScript. `GET /me/listening` (`routes_listening.go`, `listening_views.go`, `listening_copy.go`, `pages/listening.templ`): Week, Month and Year by plain links (`range`, `at`), the person's own data only, titles through `library.Scope`. Server-rendered SVG charts (`components/chart.*`, `heat.*`): listening time, days of the week, time of day over a day and night band, a heat grid for the year (weeks layout from 768 px, months layout below, no sideways scroll), most listened, finished covers, year by year; each chart has a visible sentence and a hidden table. Three icons (`sun`, `sunrise`, `sunset`); Profile links to it. Checks: httptest (own data, hidden titles, bad params, roles, link, no style attribute), route matrix, chart and heat geometry, Chromium captures at 390, 768, 1024 and 1440 in dark and light with layout assertions, the year view timed at about 70 ms on 4 x 33,000 sessions. Limits: English only; no devices chart (needs a per-session device name); sepia and contrast are not captured. A first-session month needs one extra read (`MIN(started_at)`).

**M3c status (accessibility audit and performance)**: built 2026-11-04; the manual screen-reader check is not run. Scope: our own audit (`make a11y`, no axe, no new module) over every full-page GET route at 390, 768, 1024 and 1440 px in four themes, with targets, keyboard focus, non-text contrast, reflow and 200 % text; performance at four widths (`make perf`, `make page-weight`). Fixes: B1 decision [0016](decisions/0016-contrast-tokens.md) tokens, focus rings kept on current and selected items, checked switches with an `--accent` border, a visible edge on the Usual speed select; B2 autocomplete on the SMTP fields, one `ts-sr` h1 on share pages, Now playing section headings h3 to h2; B3 `.ts-hit` hit areas on checks, switches and chips, reflow and 200 % wrapping, book tabs wrapping at 768 px (design change, operator approved 2026-11-04). Checks: the full `make a11y` run reports "75 pages, 3 states, 37042 text nodes measured; findings 0, allowed 0, unmeasured 2378" (allowlist empty); `make check`, `make build`. Operator defaults applied: targets 44 px at 390, 768 and 1024 and 24 px at 1440, page HTML not compressed (BREACH), colour changes lightness-only. CSS budgets raised, approved by the operator 2026-11-04: core CSS 20.875 KB (was 19.75 KB), `utilities.css` 3328, `pages-book.css` 3*1024+128, `player-bubble.css` +128. Performance, measured on main 7b111a2 with Chromium at 4x CPU throttle on an AMD Ryzen AI Max+ 395, load average about 4: `TestPerfPages` 20 of 20 cells within 300 ms; `TestPerfLibraryPage` median 182 ms on 2,000 books (cold load about 208 ms); `TestPerfListeningYear` median 97.5 ms (budget 150); `TestPerfListeningPage` median 68.9 ms. Median time to interactive in ms at 390 / 768 / 1024 / 1440 px: Home 227 / 245 / 216 / 224; Library 186 / 196 / 190 / 200; Book 157 / 156 / 160 / 158; Listening 232 / 235 / 237 / 224; Search 107 / 171 / 157 / 170. LCP 132 to 276 ms. An earlier run failed 18 of 20 cells only because the machine load was 55 to 71. Limits: timing budgets are meaningful only on an idle machine, so `make perf` stays out of CI (the nightly runs `make a11y` and `make page-weight`); fonts, at 690 to 930 KB per first load, are the largest part of page weight and a later optimisation, not a failure; forced-colors, placeholder contrast and pointer-gesture alternatives are manual ([accessibility.md](accessibility.md), [manual-checks.md](manual-checks.md)). Next: the operator approves the CSS budgets and the book-tab change and runs the screen-reader table.

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

**M4 status (Details)**: done 2026-11-03. Built in ten packages: M4-1 sidecars and precedence; M4-2 online client; M4-3 cover store; M4-4 lookup service; M4-5 write engine; M4-6 compare sheet; M4-7 write ledger with crash recovery; M4-8 cover picker and author details; M4-9 write details UI; M4-10 styles and captures. Precedence user > metadata.json > opf > txt > nfo > tags > folder > online. Lookups use Audnexus by default; "Search more places" adds Google Books, Open Library and Apple Books. The owner's switch `online.enabled` (default on) turns lookups off; scans never go online. Checks: `make check`, `make build`, `make player`, `make visual`, `make test-media` and `make test-mediajobs-real` (real ffmpeg: m4b, mp3 and flac keep the audio hash, Undo is byte-identical); 9 crash-injection points recover. Limits: the Audnexus author response was checked live once (2026-11-03); the `no_space` refusal is not exercised in a web test; one real-ffmpeg failure seen once by a builder did not reproduce in 9 later runs. Next: M3 Pocket.

**Library header (decision 0008) status**: built 2026-11-03. One Filter menu adds a kind's chip only when it is picked (Author, Narrator, Series, Category, Length; kinds without data are hidden). Category is one multi-select menu. Show holds the show states and Sort by. The "Filter and sort" sheet and "More filters" are retired. Chips sit on one line with Show beside Grid/List. Typing a title and then picking a filter keeps the title. Checks: `make check`, `make build`, `TestVisualLibraryFilters` at 390, 768, 1024 and 1440 px.

**Automatic and bulk lookups (decision 0009, plan [docs/plans/auto-lookup.md](plans/auto-lookup.md)) status**: built. AL-1 pacing: 4 to 6 s between books; after a refusal, back off 30 s, doubling to 15 min; after 4 failed books, pause 30 min, doubling to 4 h; stop on the 6th pause; at most 2000 requests per site per UTC day; at most 3 Audnexus books per search. Confidence rule: an exact ASIN or ISBN match, or exactly one edition with the same title and a matching author. AL-2: "Look up details online" in the Now playing panel. AL-3: bulk job `metadata.online_bulk`, migration 0012 (`libraries.auto_lookup_at`, `book_lookups`, `online_usage`). AL-4: admin "Book details from the internet" section with the per-library "Look up details online for new books" switch, "Look up all books", the "Books that need a look" list and a setup step checkbox. Checks: `make check`, `make build`, `make player`, `make visual`.

**Up next queue (plan [docs/plans/queue.md](plans/queue.md)) status**: built 2026-11-03. Migration 0013 `queue_items`: per account, at most 100 books, each book once, in order. The play button splits into "Play next" and "Add to queue". The Up next tab sits in the Now playing panel; the `/queue` page is reached from the phone ⋯ menu. The next queued book starts when one ends (`POST /hx/player/next`); the JavaScript budget is raised to 39,168 B gzip with the operator's approval of 2026-11-03. A finished mark is kept when another book starts. The series tile is renamed "Next in series", leaving "Up next" to the queue. Limits: on the phone the ⋯ menu stays open after its Up next link is followed; on screens from 1280 to about 1600 px the Now playing panel covers the right edge of the page. Checks: `make check`, `make build`, `make player`, `make visual`.

**Adding books by upload (decision [0015](decisions/0015-upload-into-library.md), plan [docs/plans/upload-books.md](plans/upload-books.md)) status**: designed 2026-11-04; board `docs/design/boards/UploadBooks.html`. Packages U1 (Go), U2 (page and JS), U3 (zip and several books). The operator answered 0015 on 2026-11-04. **U1 done (Go side)**: migration 0014 (`libraries.upload_folder_id`, `uploads`, `upload_files`, `upload_paths`), `share.AddBooks`, `internal/upload` (manifest checks, resumable chunks, `upload.place` job, ledger, restart recovery, 24 h sweep, Remove), `scan.ScanSubtree` and the `folder_id`/`subdir` scan payload, `library.SetUploadFolder`, routes `POST /libraries/{id}/uploads`, `GET /uploads/{id}`, `PUT /uploads/{id}/files/{fileID}`, `POST /uploads/{id}/finish|cancel`, `POST /admin/libraries/{id}/uploads`, `POST /books/{id}/remove`. Checks: `make check`, `make build`, route matrix rows. Limits: no page or JavaScript yet (U2), no zip (U3). **U2 done (page and JavaScript)**: `GET /libraries/{id}/add` (Add books), `GET /hx/uploads/{id}` and the `upload.place` job view (queue rows: sending, paused, stopped after a reload, checking, placing, adding, added, refused, failed), `GET /books/{id}/remove` (confirmation), the Library header button, "Adding books from this device" in Libraries & scanning (owner chooses, admin reads), "Remove this book" in the book's Details panel, `js/upload.js` (one request in flight, Pause by `AbortController`, retries after 1, 2, 4 ... 30 s, piece checksums when `crypto.subtle` exists, resume from the server offset, "Choose files again" after a reload) and the generic `[data-ts-module]` loader in `shell.js`. Limits: no whole-file checksum in the manifest (the browser cannot hash a multi-GB file without reading it into memory); no drag and drop, zip or several books (U3); no author on a row before the server has read the tags. **U3 done (zip, several books, drag and drop)**: `internal/upload/zip.go` unpacks the one staged `.zip` of an upload inside its staging folder at placement (`place.expand`), with `archive/zip` only. Limits: 2,000 entries (`zip_too_large`), total unpacked at most the book limit, the person's free quota and free space (counted before from the headers and while writing, `zip_too_large`), a ratio over 200:1 on any entry over 64 KiB (`zip_bomb`), a header that lies low fails as `zip_too_large` or `zip_bad`. Names with `..`, a leading slash, a backslash, a drive letter, NUL or a case-folded twin fail the book (`bad_path`); encrypted entries fail it (`zip_encrypted`); link entries, nested archives and junk are left out and counted; `.aax` and encrypted audio inside refuse the book. Each `.zip` is one book; `upload.js` makes one book per zip, per folder and, with "This folder holds several books", per subfolder (loose files at its top are left out), creates and sends them one at a time, and takes drops through `webkitGetAsEntry`. **Zip of several books (follow-up)**: a zip with two or more top-level folders that hold audio is split at placement (`internal/upload/split.go`, migration 0015 `uploads.split_from`): the upload the zip came as becomes the first book, every other folder becomes a sibling upload with its own staging folder, file rows, ledger and state, queued like any upload; a folder with copy-protected audio is refused alone; loose files at the top and folders without audio are left out and counted. Entry count, size, ratio, names and encryption are judged on the whole zip first, so any of those refuses all books. The archive is deleted after the split and each book counts its own bytes in the quota. The page shows the other books through the existing poll (`GET /hx/uploads/{id}?seen=N` appends the rows it has not shown, an out-of-band swap), no new JavaScript. Limits: no other archive formats. Checks: table-driven zip tests, `TestUploadBrowserZipsAndSeveralBooks`, captures at 390 and 1440 in dark and light.

**Other fixes 2026-11-03 status**: no full page reload while a book plays (answers go to the page with `answerPage`; a guard test checks it). The Details tab has a side panel, "Options for this book". The app width stops growing at 2560 px. Breakpoints use range syntax; the core CSS budget is 18.5 KB, approved. The Library header is one line, with Show beside Grid and List. "Show less" appears below a long description, and the description wraps under the cover. Home "New on the shelf" hides finished books. Radio choices are drawn round. The live lookup step log shows each step as it runs.

## M5 Book reader (R)

Tendshelf reads books. An ebook is a book; one book with two formats is its advantage. Decision
[0018](decisions/0018-book-reader.md) drops read-along; research: [ebook-reader-features.md](research/ebook-reader-features.md);
boards: `screens/reader/` minus the read-along ones.

- **R1 Ebook ingestion**: `internal/ebook`, migration `ebooks` (`ebook_files`, `editions`,
  `edition_chapters`); scanner finds ebooks in book folders; upload on `/books/{id}/text`;
  copy-protection refusal; parsers for EPUB 2/3, TXT/Markdown, HTML, DOCX, FB2 first, then PDF (text
  layer, reflow check, page fallback) and DRM-free MOBI/AZW3; canonical document with stable IDs;
  `ebook.ingest` job. Exit: every parser and refusal fixture passes; same file gives byte-identical
  document; hostile-file tests pass; the Text page lists chapters and first lines.
  **R1 E1 status** (data, canonical document, core parsers): built. Migration 0016 (`ebook_files`,
  `editions`, `edition_chapters`, schema version 16); `internal/ebook` reads EPUB 2/3, TXT, Markdown, HTML,
  DOCX and FB2 (also `.fb2.zip`) into `tendshelf-doc-v1` with stable block IDs, UTF-16 offsets, sentences,
  words and front-matter marks; copy-protection refusal before parsing (ADEPT, Apple, LCP, EPUB encryption,
  OLE Word, KFX, encrypted zip entries); bounded in-memory zip, XML depth, word and block limits, 60 s
  deadline, panic recovery; read-only `Store`. Checks: `go test -race ./internal/ebook/... ./internal/db/...`
  (parsers, refusals, hostile files, determinism, golden, segmenter, migration), `make check`, `make build`.
  Limits: PDF and MOBI/AZW3 are stubs that refuse `unsupported` (E2); no scanner, upload or job (E3); no
  route (E4). Next: E2 and E3.
  **R1 E3 status** (scanner, upload store, job): built. `media.KindEbook` for epub, mobi, azw3, azw, fb2,
  kfx and kfx-zip; the scanner splits ebooks out of each walk and hands them to `EbookSink`s after its
  write transaction (grouping and probing never see them; `internal/scan` does not import `internal/ebook`).
  `ebook.Service`: `Reconcile` (new, unchanged, changed, moved, missing, skip prefixes, scope; read-only
  hashing), folder pairing, the `ebook.ingest` job (fenced write of edition, chapters and the gzip document;
  refusals; stale results discarded; `not_yet` refusals reset at start-up and after each scan), `AddUpload`
  (byte-identical at `<data>/ebooks/<sha256>.<ext>`, mode 0600, plain multipart-sized streaming, 413/422/
  507/409 errors) and `Dismiss`. PDF, DOCX, TXT, MD and HTML come only by upload. Checks: `TestScanEbook*`,
  `TestIngest*` (race), `make check`, `make build`. Limits: no route or page (E4); R2 pairing rules, inbox
  and an orphaned-bytes sweep are not built.

  **R1 E2 status** (PDF and MOBI/AZW3): built. PDF text layer through `github.com/ledongthuc/pdf`
  (v0.0.0-20261020125853-5e46805f718e), chosen on four generated fixtures (plain, Type0 with ToUnicode, xref
  stream, incremental update); `/Encrypt` found by a raw scan and in the trailer is refused `drm`; lines from
  glyph runs, repeated headers, footers and page numbers dropped, hyphen joins, heading and chapter rules,
  `layout='page'` for columns or mostly short lines, `no_text`/`image_only` for scans. MOBI/AZW3: own PalmDB
  reader (PalmDOC LZ77 and uncompressed, trailing entries, cp1252 or UTF-8, EXTH meta, combined files read
  from the KF8 part); encryption 1 or 2 refused `drm`, HUFF/CDIC `unsupported`; record, back-reference and
  text-length lies are `damaged`. Checks: `go test -race ./internal/ebook/...`, `make check`, `make build`.
  Limits: no OCR, HUFF/CDIC, SKEL/FRAG rebuild; the module takes no context, so the deadline is checked
  between pages; standard fonts without `/Widths` read as zero-width glyphs, so line width falls back to a
  character estimate; the short-line test uses the 90th percentile line width (a median cannot be beaten by
  more than half the lines). Not run: the manual check on real books.
  **R1 E4 status** (Text tab): built. `GET /books/{id}/text` is a book tab (Text, after Chapters) shown to people
  who may `EditBook`, or may `ReadText` when the book has a ready edition; anything else answers 404. Editors
  see every file (reading, copy-protection card, plain refusal sentences, failed or gone) and the add form;
  readers see ready editions only, never a folder path. `POST /books/{id}/text` is one multipart form that also
  works without JavaScript (htmx: files and form again, the new file polls `/hx/jobs/{id}`; plain: 303; a
  refusal answers 413, 422, 507 or 409 with its sentence in the form), `POST .../{fileID}/dismiss` clears a
  refused or failed upload. `csrf.go` takes a 201 MiB multipart body for that one route and keeps 1 MiB of it
  in memory. First 12 chapters, then "Show all {n} chapters" (`?all=1`); front matter is labelled. Checks:
  `TestTextTab*`, route matrix rows, a11y row `book-text`, `TestTextTabVisual` at 390, 768, 1024 and 1440.
  Limits: the board's "Found in your folders" pairing card (Pair and prepare, Not the same book) is R2, and
  Read, Read along and Preparing are R3 to R4.
- **R2 Pairing**: automatic by folder, upload, ISBN/ASIN, title + author (suggestion when not unique);
  manual pair/unpair; `/admin/ebooks` inbox; several editions, book default, per-user choice. Exit:
  pairing table tests; an unpaired ebook never appears as a book; no ebook is ever written.
  **R2 P1 status** (data and pairing engine): built. Migration 0017 (`ebook_files.hold`, `book_default`,
  `ebook_not_same`, `text_choices`); `internal/textmatch` (normaliser moved from `metadata/online` and `ebook`,
  plus `ISBN13`, `Surname`, `TitleKey`); `internal/ebook` `Match`/`Decide`/`Checks`, `Pair`, `Unpair`, `NotSame`,
  `Keep`, `SetBookDefault`, `Choose`, `EditionFor`, `PairPending` (run after every scan and when a file becomes
  ready); folder pairing now honours `hold` and "not the same book". Checks: `TestTextmatch*`, `TestPair*`,
  `TestMigration0017*` (race), `make check`, `make build`. Limits: no page or route (P2, P3); no ebook-only
  books; the engine does not re-run when a book's details change (the next scan does). The pure type is
  `PairMeta` because `ebook.Meta` already names the parsed metadata.
  **R2 P2 status** (Text tab: pair, unpair, editions): built. `components.PairingCard`; routes `POST
  /books/{id}/text/{fileID}/{pair,unpair,not-same,default,choose}` (EditBook, choose ReadText; a file on another
  book moves only for someone who may edit that book too); the tab shows the card with checks for files paired by
  folder, ISBN or title, up to three suggestions under "Found in your folders" (editors only), and chips and
  buttons for the book's edition and your own when a book has two ready ebooks. Checks: `TestTextPair*`,
  `TestTextEdition*`, matrix rows, `book-text` a11y seed (0 findings), opt-in `TestTextPairVisual`. Limits: the
  main button is "Pair with this audiobook" until R3; the core CSS budget is over by 212 gzip bytes (21,588 against
  21,376) and waits for an operator decision.
  **R2 P3 status** (`/admin/ebooks` inbox): built. Routes `GET /admin/ebooks`, `GET /admin/ebooks/{fileID}`, `POST
  /admin/ebooks/{fileID}/{pair,keep}` (ManageLibraries; pairing also needs EditBook on the chosen book; anything
  refused is 404). The list shows the first 100 unpaired files with their best close match, a "Kept on their own"
  section for files kept as ebooks on their own (they stay in the inbox, never become a book), and the R1 sentence
  for files not ready; the pick page is board NoMatchPhone (card, no-match warning, search of the visible
  audiobooks, close matches, keep). `/admin/libraries` links "{n} ebooks to pair". New reads: `ebook.Service.
  CandidatesFor` (books loaded once for many files), `ebook.Store.CountToPair`. Inbox styles sit in
  `pages-text.css`. Checks: `TestEbookInbox*`, matrix rows (`routes_matrix_ebook_inbox_test.go`), a11y rows
  `admin-ebooks` and `admin-ebook`, opt-in `TestEbookInboxVisual`. Limits: no bulk pairing, no deleting unpaired
  upload bytes, no nav item.
- **R3 Alignment job**: removed (decision 0018); the work stays in git history (`docs/plans/m5-r3-alignment.md`).
- **R4 Reader page**: P1 and P2 are built (below). P3 (`readalong.js`) is dropped; P4 reading settings is on its branch and is
  kept; what remains moved to RD1-RD4. The read-along parts of these notes describe code that package X removes.
  - *R4 P1 (data; done)*: migration `0019_highlights` (`highlights` with its note column, `bookmarks.edition_id`
    and `sid`), `progress` highlight repo (`AddHighlight`, `Highlights`, `ChapterHighlights`, `Highlight`,
    `SetHighlight`, `DeleteHighlight`; limit 2,000 per book; client id retry as bookmarks), `AddTextBookmark`
    over one shared insert, `ebook.ParseSID` and `CompareSID`. Checks: `TestMigration0019*` (from the 0018
    fixture and from empty), `TestHighlight*`, `TestBookmarkTextPlace`, `TestParseSID*`. Limits: no sync
    endpoints for highlights (`highlights_sync` is the R5 hook); the `/api` bookmark JSON is unchanged.
    Next: P2.
  - *R4 P2 (server; done)*: `GET /books/{id}/read` (modes `readalong|read`, `ch`, `at`, `sheet=contents|marks`), the chapter
    fragment, the gzip pass-through timing endpoint, highlight create, change and delete (htmx only, the person's own),
    text bookmarks (`POST /books/{id}/bookmarks` with `edition_id` and `sid`), the Text-tab "Read along" / "Read" link, and the
    sentence/run/highlight renderer (`reader_render.go`). Every route is `ReadText` (404 otherwise). Checks: `TestReader*`,
    `TestRouteMatrix*` (6 reader rows, member and guest with and without `can_read`). Limits: no JS or CSS (P3 writes
    `readalong.js`, `reader-marks.js`, `pages-reader.css`; the page leaves `data-ts-module` out until those files ship);
    the reader page is skipped in the a11y list until R4 P5; no reading-settings link (P4).
- **R5 and R6**: removed (decision 0018). Cross-format position and Media Overlays need sentence timing. Offline text and
  `can_read` on share links move to G6 and G7 below.

Book reader packages ([plan](plans/m5-book-reader.md)). Order: P4 merge, X, then B1 and RD1, then B2, B3 and RD2, RD3, RD4.

- **X Remove alignment (done)**: migration 0020 (`highlights` rebuilt without audio times; `alignments`, `alignment_chapters`,
  `runtime_components` and the `align.*` jobs gone), `internal/align`, `runtimes/align`, `/admin/runtimes`, `media.PCM16k`,
  `jobs.Error.Hold`, and the reader's voice code (modes, dock, timing route, Eyes-free button, Highlight, Word, Follow and View
  settings). The reader is a plain scrolling page again; `mode=` is ignored so old links work. `pages-reader-settings.css`
  folded into `pages-reader.css`, which `Set.Precache()` skips. Checks: `TestMigration0020*`, `TestX*`, `make check`,
  `make page-weight`, `make a11y`. Limits: `media.LowerPriority` stays (waveform peaks use it); `bundle.css` voice classes wait for RD4;
  "Also let them read along" (share, admin user) and "text only, not narrated" wait for B2 and B3. Next: the orchestrator removes
  `<data>/runtimes/align`, `<data>/cache/align` and `<data>/align` with the server stopped (plan, "Repair after X merges").
- **B1 Ebook books (done)**: migration 0021 (`books.kind`, `editions.meta_json`, `ebook_files.merged_from`, `merged_books`,
  `reading_positions`, `reading_sessions`; schema version 21). A ready ebook with no audiobook gets a `kind='text'` book
  (`internal/ebook/textbook.go`); the scanner's audio sweep never sees it and `syncTextBooks` marks it missing and back. The
  automatic merge (`merge.go`: `mergePending`, `PairBook`, called by `Finisher` and the new `metadata.Service.OnLocal`) takes
  a text book into the one audiobook `Match`/`Decide` or the folder rule singles out, only when the SQL guard says no grant,
  share link or shared shelf could widen; `MergeBook` is a person's merge (no guard); `Unpair` is "Not the same book" and
  restores the merged-from id. Rows only, no file is opened. Details: `SourceFile` ("file") ranks between `nfo` and `tags`;
  sidecars and a folder picture are lent only when the folder holds one book (`ebook.SoleStemDir`). Covers: `ebook.CoverBytes`
  (EPUB, FB2, MOBI) feeds `mediajobs.Covers.TextCover`, stored as `embedded`. `progress` gained reading positions, sessions
  and speed. `/books/{id}` and `/books/{id}/read` of a merged-away id answer 308. Checks: `TestMigration0021*`, `TestTextBook*`,
  `TestMerge*`, `TestSplit*`, `TestReadingRepo*`, `go test -race` on the touched packages, `make check`, `make build`.
  Limits: no pages (B2, B3); `meta_json` keys assumed `description`, `publisher`, `year`, `series`, `series_index` until RD1
  writes them; WebP ebook covers go through ffmpeg like folder ones. Next: RD1 fills `meta_json`; B2 and B3 add the surfaces.
- **B2 Book surfaces**: book page, Home, cards, filters, API, the resume rule for two formats, the player's book icon.
  Exit: one primary resume per book at every width.
  **Status (built 2026-11-04)**: `lastFormat` (the newer of the listening and reading places; a format without the right is never
  primary) decides the one primary button on the book page: "Start listening", "Resume" and "Listen again", or "Start reading",
  "Continue reading" (with `Chapter n · pct %`) and "Read again"; the other format is a plain secondary "Listen" or "Read".
  A text book has its own page (no Chapters, Bookmarks, write sheet, queue menu or Download; its bare address opens the Text tab;
  "Ebook · n pages" chip; the sentence "You can see this book, but reading it needs permission." with no button for a person without
  `ReadText`), and `/hx/books/{id}/finished` marks it on the reading side. Home: `Repo.ContinueBooks` unions both formats, one row per
  book, and the hero reads "Continue reading" with the time left at the person's own speed. Library: `format=` (Format filter, offered
  once an ebook is visible), the four Show predicates read `reading_positions`, lengths leave text books out, cards say "Ebook" in the
  length slot. `/api/v1/books/{id}` gains `kind`, `formats` and `reading`. `GET /books/{id}/text/{fileID}/file` gives back the original
  file (needs `ReadText` and `DownloadBook`; ready files only). No link section or link route for a text book. The player's book icon:
  `data-read-href`, `[data-ts-bar-read]` right of Chapters in the 1280 px bar and `[data-ts-full-read]` right of Chapters in the full
  player (the phone's mini row is unchanged). Copy changed: "Also let them read", "Can read", "Opening pages" (was "text only, not
  narrated"). Checks: `TestFormats*`, `TestPlayerRead*` (with `TestPlayerReadIconBrowser`), `TestRouteMatrix`, `TestVisualFormats`
  (four widths, dark and light), the full `make a11y` (findings 0), `make player`, `make check`, `make build`. Limits: cards show no
  progress bar (cards have none today); the series row on Home has no Read button for a text book; the book-icon click is checked by
  href, the reader page by its own tests; `vision.md` still lists "Honest timing", a read-along line left for the docs sweep.
  Deviations: no `speaker` icon (RD1 uses `output`); contrast theme gets a darker `ts-avatar--c4` (7:1 under 13 px initials).
- **B3 Add books and `/admin/ebooks`**: ebooks by upload (0015 amended), PDF scanning, suggestions and "Could not be opened".
  **Status (built 2026-11-04)**: Add books takes ebooks and PDFs (manifest, zip and split count an ebook like audio;
  `.kfx` refused by extension; placement opens each staged ebook with `ebook.Read`: DRM or unreadable refuses the whole upload
  and deletes the staged bytes; name from the first audio's tags, else the first ebook's details, else the file name;
  `upload.Removable` and `Remove` count ebook files, delete their rows before the book so none is revived). The scanner treats a
  PDF as an ebook only when no audio sits in its directory (`media.KindLooseEbook`), and an ebook-only scoped scan reaches the
  sink. `/admin/ebooks` is "Maybe the same book" (`ebook.Service.Suggestions`: a match the engine leaves or the guard refused,
  filtered by `BookFilter`; "Make one book" runs `MergeBook`, "Not the same book" and "Keep it as an ebook on its own" are
  remembered) and "Could not be opened" (files with no book; a failed or damaged file has a read-only download). A text book's Text
  tab links "Find the audiobook". The libraries page links "Ebooks" with no count. Checks: `TestAddEbook*`, `TestEbookAdmin*`,
  `TestSuggest*`, the route matrix, `make check`, `make build`, `make a11y`. Limits: the audiobook's Text tab no longer lists
  suggestions (they live on `/admin/ebooks`); no bulk merge, no ebook upload through the API.
- **RD1 Reader server**: document recipe v2 (links, images, metadata), chapter parts, images route, `/reading/{bookID}`,
  the speaker icon. Exit: a hostile EPUB puts no script, handler or style byte in any answer.
  **Status (built 2026-11-04)**: recipe `tendshelf-ebook-v2` (links with resolved targets, image sizes, description,
  publisher, series, year, cover part; spans and links no longer split a paragraph), `meta_json` written, stale files read
  again while the old edition keeps serving, `ebook.Reanchor` and the re-home of highlights, bookmarks and reading positions,
  chapter parts, `data-c`, the start-place ladder, `GET /books/{id}/read/images/{blockID}`, `GET` and `POST /reading/{bookID}`,
  the speaker in the bar (the set's `output` icon through `PlayButtonView.Icon`), Contents and Marks in the bar. Checks:
  `TestDocV2*`, `TestReaderServe*`, `TestReaderSpeaker`, route matrix rows, `make check`, `make build`, full `make a11y` (the
  reader page stays in `a11ySkip` until RD4). Limits: no client yet (RD2 turns pages, saves and reads `data-flow`, which is
  `auto` until the Flow setting exists); the footnote popup address `/hx/books/{id}/read/note/{blockID}` is emitted in
  `data-note-href` and answered in RD3; `revealed` stays in the scroll sentinel's trigger until `reader.js` fires
  `ts-reader-next`. Next: RD2.
- **RD2 Paginator**: `reader.js`, `reader-pages.js`, pages and scroll, tap zones, keys, swipe, time left from the person's
  speed, settings. Exit: the place survives a change of device size, font size and flow.
  **Status (built 2026-11-04)**: `reader.js` (flow, anchor, saving, keys, taps, progress, the other-device offer) and
  `reader-pages.js` (CSS columns, one part at a time, swipe), the page layout in `pages-reader.css`, five new reading
  settings (Margins, Justify text, Hyphens, Page or scroll, Swap tap sides) in `reader.v1`, the end-of-book block, and
  `POST /reading/{bookID}/finished`. Checks: `TestPaginator*`, `TestReaderInput*` (Chromium, `-tags dev`, 390 and 1440),
  `TestReaderSettings*`, route matrix row, `TestJSBudget`, `TestCSSBudget`, `make check`, `make build`. Limits:
  `reader.js` is over its approved 4.5 KB (see the budget note in `motion_test.go`; the app total holds); two columns
  only from 1024 px and when both fit; no right-to-left; WebKit is RD4's manual check; a phone's pages after "Contents"
  restart at the chapter (RD1's `?ch=` ladder), RD3 can pass `at`. Deviation: "Mark as finished" posts to
  `/reading/{bookID}/finished` (the plan's `/hx/books/{id}/finished` answers 404 for text and writes listening
  progress). Next: RD3.
- **RD3 Marks, footnotes, search, export, errors** (done): `reader-marks.js` (four highlight colours, Note, Copy, bookmark
  this page, footnote links, "Back to where I was", offsets as `data-o` plus the offset inside the sentence) and the lazy
  `reader-notes.js` (footnote popup, search-hit mark). The note dialog and every label are server-rendered. Bookmarks in the
  text have `sid` and `edition_id`; `progress.Bookmarks` and the Bookmarks tab hold audio moments only, `TextBookmarks` and
  the marks sheet the text ones (delete from the sheet), and `/api` bookmark JSON gains `sid` and `editionId`. Footnote popup
  `GET /hx/books/{id}/read/note/{blockID}`; search `?sheet=search&q=` and `GET /hx/books/{id}/read/search` (accents, case
  and white space folded, UTF-16 offsets, 200 hits, 80-character snippets, `::highlight(ts-hit)` on arrival); export
  `GET /books/{id}/read/marks.md|.txt|.json`; a missing, damaged or too large document answers "Tendshelf cannot open this
  book right now." (a toast for htmx) and a part that does not load offers "Try again". Limits: a note dot opens the marks
  sheet (notes are changed there); the colour of an existing highlight is not changed in the UI (PATCH accepts it); the
  back chip lasts until the next navigation; no original-file download exists to link from the error page. Next: RD4.
- **RD4 Captures, a11y, contrast, layout time** (done, 2026-11-05): the reader joins `make a11y` (`reader-pages`, `reader-scroll` as
  pages; contents, marks, settings, search, footnote, selection toolbar and note dialog as states; four widths, four themes, 0
  findings, one allowlist line for the paged column box). Fixes the audit found: the book title is the h1; the shell navigation
  is hidden behind the reader and the reader page is `inert` behind a sheet (`shell.js`); plain `ts-btn` submit buttons are
  primary (the browser's grey showed in the contrast theme); note dots are 44 px targets; the marks list's textareas and
  chips are 44 px; the sheets get their height cap and width from `pages-reader.css` (they hung off the top of the screen).
  Colours: contrast `--selection` 35 to 25 percent white (the blue highlight; decision 0016); every avatar colour passes
  (`TestBRAvatarColours`). `TestBRContrast` crosses four highlight colours and the selection with every theme and ground.
  `TestBRVisual` captures 10 states at four widths in four themes beside the boards; `TestBRLayoutTime` (`make perf`) lays
  out a 39,618-unit part at 4x CPU in about 45 ms and times turns. `make page-weight` has a `reader` row. The error page
  links the original file when the person may download. Swept: `.ts-mode`, `.ts-voicepill`, `.ts-focus`, `.ts-align`,
  `.ts-readdock__speed` and the "Honest timing" and Focus line copy in `vision.md`. Limits: WebKit was not driven (no
  `xvfb-run`; the check is in [manual checks](manual-checks.md#m5-book-reader-webkit-and-phone-rd4)); book cards show no
  progress bar for an ebook and Home's series rows have no Read button (not in RD4's scope).
- **G6a Offline reading: server, worker, place queue** (done, 2026-11-05; [decision 0019](decisions/0019-offline-reading.md), [plan](plans/m5-great-reader.md)):
  `GET /offline/books/{id}/manifest` and `/read` (need `ReadText` and `DownloadBook`); the worker answers a held book's chapter
  parts (network first, 3 s), pictures (cache first) and its reader shell (also `?sheet=contents`) when the server cannot be
  reached; `pages-reader.css` is back in the precache; `reader-offline.js` fills the shell and keeps the place queue
  `ts.offline.reading` (written before any send, never `ts.pending`; `offline.js` sends it in slices); reading time is dated by the
  save (30 days at most). Checks: `TestOffRead*` (Go and Chromium, including a stalled network that reaches the reader in under
  15 s and a lost beacon whose place still lands) and the whole `TestOffline*` set. Limits: nothing stores the text yet (G6b: the
  Download button, the daily revision check); the browser tests store it from page script; not tried on a phone (manual check
  after G6b).
- **L Leftovers: series across formats** (done, 2026-11-05; [plan](plans/m5-great-reader.md)): `SeriesInProgress` counts reading
  as well as listening (a book read to the end is done, whichever format; `ReadBP`, `ReadLast` say which was newer);
  Home's series row keeps one control, Round Read or Round Play by `lastFormat`, none for the hero's book or without a right;
  series tiles on the book page show the share of the format used last and skip a book finished in either (`standIn`,
  `progress.ReadingForBooks`). Step L4 (operator yes, 2026-11-04): a 3 px line at the foot of the cover of a started, unfinished
  book on the library grid and list, series page and shelf rows, from the format used last, drawn by `Cover` (`pages-library.css`,
  `ts-cover__line`; no core CSS growth), two queries per page and only the person's own rows; text alternative NEW
  "{n} % through" (hidden words beside the line). Checks: `TestSeriesFormats*`, `make check`, `make a11y` 0 findings,
  `make page-weight`, `TestPerfLibraryPage` median 194 ms (budget 300). Captures: `TestSeriesFormatsVisual`. Limits: Home's
  "New on the shelf" and person pages draw no line; no board shows the line.
- **G6b Offline reading: downloads and surfaces** (done, 2026-11-05; [decision 0019](decisions/0019-offline-reading.md), [plan](plans/m5-great-reader.md)):
  one Download keeps the text as well as the audio. `downloads-text.js` (lazy) stores every chapter part and picture under the exact
  addresses the reader asks for, then the contents shell and the reader shell last; `downloads.js` carries the `text` record (sizes
  and progress add the text's share), `reseed()` sends `&text=`, and the daily check calls the manifest: a new `revision` stores it
  again and drops stale addresses, 404 removes the text (the record too for a text-only book), a redirect or a network failure
  changes nothing; a text-only book takes its finished date from the manifest. The book page shows Download for a text book when
  the person has `ReadText` and `DownloadBook`; `/offline?text=` lists held text books with a Read button (secondary beside Play).
  Checks: `TestOffDownload*` (Go and Chromium at 390 and 1440), the whole `TestOffline*` and `TestOffRead*` sets, `make check`,
  `make a11y`, `make page-weight`. Limits: the sizes are over the approved rows (`downloads.js` 8.7 of 8.5 KB, `downloads-text.js`
  2.2 of 1.5 KB; app JS 82.3 KB, 80.5 KB measured as the step); the manual airplane check on the operator's phone (Tailscale HTTPS,
  "Rules for the Summer", Download, airplane mode, read, close, reconnect) is a recorded step for the orchestrator and is not run.
- **G2 Reading stats** (done, 2026-11-05; [plan](plans/m5-great-reader.md), [decision 0014](decisions/0014-listening-dashboard.md)):
  `GET /me/reading` ("Your reading") draws the person's own reading on the listening page's pieces: time, days read, words a minute
  (about, characters over five, to 10), books finished, a Time chart, a yearly goal (`reading.v1` in `user_settings`, 0 to 365,
  `POST /me/reading/goal`, 303 or a toast on htmx, 422 on junk) and the covers finished in the period, only through `CardsByID`
  with the person's scope (a revoked book's cover goes, the count stays). Numbers come from `reading_sessions` and
  `reading_positions.finished_at`, bound to the signed-in user; no family view, no migration, no new JavaScript. The profile
  panel links to it once the person has read. `getListening` gave its range parsing to `listeningPeriod`; `timeChart` to
  `barsByPeriod`. Checks: `TestReadStats*` (Go service, page, leak, goal, Chromium captures at 390, 768, 1024 and 1440 in four
  themes), `reading-stats` in `make a11y`. Limits: `pages-listening.css` grew 416 B (2,809 B gzip of 3,072) for the goal form; the
  streak is computed but not shown (decision 0013: not gamey); at most 12 finished covers are read before scope, so a revoked
  book can leave fewer than 12.
- **G4 Skim** (done, 2026-11-05; [plan](plans/m5-great-reader.md)): a range input over the dock's ruler names the chapter under the thumb and opens the book at that share of the text on release, leaving "Back to where I was". `bp` in the reader address (server rung in `reader_start.go`), `data-skim` (`reader_skim.go`), `reader-skim.js` (lazy), skim styles in `pages-reader.css`. Checks: `TestSkimStart`, `TestSkimBrowserJump/Keys/Offline`, `TestSkimVisual` (four themes, 390 and 1440), the `reader-skim` a11y state, `make check`, `make build`, `make a11y`. Limits: `reader-skim.js` 1,292 B against its 1,280 row and `reader.js` 6,440 B against 6,144 (main was 6,044), both reported for the operator; offline `bp` is ignored; a book of over 300 chapters keeps every nth in the tip and `ch` is then only a guess (the server still places `bp`). Next: G2.
- **G5 Comfort** (done, 2026-11-05; [plan](plans/m5-great-reader.md)): keep the screen on, warm light (off, in the evening, always) and a bar that hides after a moment, under "More" in the reading settings. `users.ReaderSettings` (`keepAwake`, `warm`, `autoHide`), `data-*` on `#reader`, `reader-comfort.js` (loaded only when one is on, or when the sheet turns one on), the wash in `pages-reader.css`. No brightness gesture (no web API). Checks: `TestComfortSettings`, `TestComfortRoute`, `TestComfortContrast`, `TestComfortBrowserWarm/Hide/Wake`, `TestComfortNotLoaded` (captures at 390 and 1440), `make check`, `make build`, `make a11y`. Limits: `reader-comfort.js` 1,576 B served against its 1,024 row (comments kept); the sheet's focus returning to the Aa link keeps the bar until focus moves on. Next: G3 and G7.
- **G3 Print pages** (done, 2026-11-05; [plan](plans/m5-great-reader.md)): recipe `tendshelf-ebook-v3` keeps the book's own page labels (`Doc.PageList`, `ebook.PageAt`) for EPUB 3, EPUB 2 and PDF; the reader's `data-pg` and the "p. xii of 13" line use them; books without a list behave as before. Checks: `TestPrintPages*` in `internal/ebook` and `internal/web` (including a v2 edition re-read, highlight/bookmark/position re-anchor and the offline manifest revision) and `TestPrintPagesBrowser`. Limits: block-level grain; MOBI has no print pages; no "go to page". Operator note: the first start after this version re-reads every ebook through the normal queue, reading stays available meanwhile (the old edition answers until the new one lands), and an offline copy sees a new manifest revision and stores again at its next daily check; highlights, bookmarks and places move with `Reanchor`.
- **G7 Series and shelves** (done, 2026-11-05; [plan](plans/m5-great-reader.md)): the end of a book in a series offers "Next in {series}: {title}" (a quiet link to the book page, which picks the format; `SeriesInfo` with the person's scope, so a book they cannot see is skipped); series page rows say the place and where the person stands, listening and reading alike (`seriesTileState`); "To read" is a plain shelf made by one tap (`POST /books/{id}/to-read`, `ShelfNamed` get-or-create, idempotent) and listed first in the Add to shelf sheet. No migration, no JS or CSS change. Checks: `TestSeriesNext*`, `TestToRead*`, `TestShelfNamed`, matrix row. Send a book by link stays later.
- **G1 Look-up and word list** (done, 2026-11-05; [plan](plans/m5-great-reader.md)): **Look up** in the selection toolbar opens a popup with definitions from dictionary files the owner adds under `<data>/dictionaries` (StarDict `.ifo/.idx(.gz)/.dict(.dz)/.syn` and `.tsv`; none bundled, nothing fetched), a plain DuckDuckGo link the reader taps (only the selected word in it) and **Save word**. `internal/dict` (bounded, hostile-file safe, no network), migration 0022 `word_list` (merge and split move it), `progress.AddWord/DeleteWord`, `library.WordList` (grant-filtered in SQL), `routes_lookup.go`, `routes_words.go`, `/me/words` with Markdown and JSON downloads, `reader-lookup.js` (lazy). Checks: `TestDict*`, `TestMigration0022*`, `TestWordList*`, `TestDefine*` (including `TestDefineBrowser`), the route matrix and the accessibility audit (`reader-lookup`, `words`, `words-list`). Limits: no look-up offline, no phrase look-up, no flashcards, no bundled or downloaded dictionary, `reader-lookup.js` is over its row (see the budget note in `motion_test.go`). Next: G5 and G3.
- **Not built in M5**: collections and sending a book to a device, then OPDS and KOReader sync (see Later). Everything else listed above is built.

## Profile redesign (decision 0021)

Built 2026-11-05 as three packages; no milestone number. Plan [plans/profile-redesign.md](plans/profile-redesign.md).

**Profile redesign PR1, Your devices** (done, 2026-11-05; [plan](plans/profile-redesign.md), decision [0021](decisions/0021-profile-redesign.md)): sessions moved off Profile to `GET /me/devices` ("Your devices" in the Settings list); Profile keeps one row with "This device and 8 others". Browser sessions with the same label (never this device, never apps) fold into a closed `<details>` ("6 sign-ins" and the newest time), each session still has its own sign-out form (`groupDevices`, `devicesLine`, `pages.Devices`). The three sign-out posts now redirect to `/me/devices`; ids still reveal nothing (unknown or foreign is a 404). No migration, JavaScript or query; `pages-me.css` gains four rules. Checks: `TestBookFirst*`, `make check`, `make build`, `make a11y` (0 findings), captures at 390, 768, 1024, 1440. Next: PR2 the page.

**Profile redesign PR2, the page** (done, 2026-11-05): Profile PR2: `/me` is books first at full width, with the hero, Also in progress (5 phone rows, tiles from 36 rem), Finished lately, Your listening, Your shelves and Account. The `profile` page-weight row is set.

**Profile redesign PR3, Home's row** (done, 2026-11-05): under the Continue hero, Home shows "Also in progress", a row of up to three covers (their time left or place under each, no resume control of their own), with See all to `/me`. `homeHeroView` asks for four books under way; `continueLeft` is shared with Profile's tiles. `pages-invite.css`, `pages-folders.css` and `pages-upload.css` joined `noPrefetch` to pay for the three cover requests. Home's `Img` budget rose from 33792 to 38912 B (operator-approved; the maximum of three runs and four widths is 33761 B, times 1.15, next KiB); requests stay at 61. Checks: `TestBookFirstHomeRow`, `TestBookFirstHomeFragment`, `TestBookFirstHomeRequests`, `TestBookFirstHomeVisual` (dark and contrast at four widths), `make page-weight`.

## M6 Desktop (P5 Wails)

- `desktop/` module, Wails v3 pinned; connect-to-server and personal modes; media keys; tray.

Exit: Linux build runs both modes in an isolated session; other platforms recorded as not verified.

**M6 status (desktop, packages D1 to D7)**: built 2026-11-05 on Linux; the operator's own-desktop checks are not run. Plan
[plans/m6-desktop.md](plans/m6-desktop.md), decision [0020](decisions/0020-desktop-shell.md), user notes [desktop.md](desktop.md).
Scope: `internal/app` (the server's start-up, shared), `internal/desktopshell` (first-run picker, `/healthz` check),
shell marking in the web layer (`ts_shell`, `data-shell="desktop"`, `desktop.js`), the `desktop/` module (Wails v3.0.0, personal
and connect modes, tray, menu, persistent cookies, downloads), the `.deb`, and a desktop CI job.
Checks, all in a private headless GNOME Shell (`scripts/isolated-display.sh`; nothing on the visible desktop), by `make
desktop-exit` on the probe build (`production` tags plus `tendshelf_probe`), both modes, results in `.local/tests/desktop/`:
`data-shell="desktop"` in both modes; no service worker; the Downloads link hidden (present in the markup, `display: none`);
muted play and a seek to 120 s on `/stream`; the CSP header byte-identical to the server binary's; an outside link reaches
`tendshelf:open` and the window stays on its origin; an attachment saved to the download folder; quit and relaunch keeps
the sign-in (after SIGTERM in personal mode, after Quit in connect mode); a second launch hands over to the first; SIGTERM
exits 0 within a second and closes the library's port; choosing connect after personal closes the library's port; the tray
registered a StatusNotifierItem once the private shell's AppIndicator extension was enabled (the shell's settings are in
memory, so the real session's `enabled-extensions` was unchanged, checked before and after); screenshots through
`org.gnome.Shell.Screenshot`. The reader ran in the window on a synthetic EPUB (rows in `docs/manual-checks.md`, M5 table),
which found one real WebKit bug, fixed in `pages-reader.css` (a bare `column-count: 1` made no columns, so Pages could not
turn within a chapter); `make visual-webkit` passes in the private session.
Limits, stated plainly: the tray cannot start a book without a user gesture (WebKit's autoplay rule); the real player's
`mediaSession` handlers have not been seen firing from a real key (WebKit's own MPRIS player answered `Pause`, `Play` and
`PlayPause` over D-Bus); there was no sound card, real keyboard, tray click, file chooser or `https` server in the session;
the Chromium reader tests were not re-run after the CSS change (no Chromium here). macOS and Windows: not verified. Next
step: the operator's desktop section of `docs/manual-checks.md`.

**Wails 3.1.0 and the desktop experience (design, 2026-11-05)**: decision [0023](decisions/0023-desktop-experience.md), proposed; plan
[plans/m7-desktop-experience.md](plans/m7-desktop-experience.md) (not the M7 below); board `docs/design/boards/DesktopShell.html`. Scope so far: the
`desktop/` module on Wails v3.1.0, a CGO-free build (`make desktop-purego`, purego twin of the two WebKit calls), and a fix so `make desktop` builds
at all. Checks: `make check`, `make build`, `make desktop-check`, `make desktop-exit` on CGO and CGO-free (each 11 PASS, 0 FAIL), a title bar spike with
real input in the private session. Limits: no sound card, real keys, tray click or file drag in the session; other desktops not tried. Next step:
the operator's word on 0023 and the board; packages X1 to X6 after the release in flight.

**Desktop experience X4, Now playing (2026-11-06)**: `desktop-shell.js` tells Go what is playing (`tendshelf:state`: title, chapter, left, playing,
theme, canAdd, plus `stop` = `end` or `sleep`); `desktop/state.go` limits and escapes it; the tray (`tray.go`) shows the book, Pause, Skip back and Skip forward
and rebuilds on change; notifications (Wails' notifications service) at book end and sleep stop when the window is not in front; Keep playing
(`desktop.json` `keepPlaying`, offered only when a StatusNotifierWatcher exists, via godbus, `NOTICES.md`); the theme is saved as `ground`; More gained
Change mode or server, Zoom in, Zoom out, Actual size, Keep playing and About Tendshelf (X2's gap). Checks: see the package report in the merge. Limits: `desktop-shell.js` is
3,578 B gzip (approved 2,816 B) and the desktop total 4,734 B (approved 4,096 B), set to 3,584 B and 4,864 B and reported; sound in a hidden window, a real tray
and notification click are for the operator's own desktop. Next step: X5, drop a book.

**Desktop experience X5, Drop a book (2026-11-06)**: the bar gets `data-ts-can-add` (owner or admin, `share.Can`, a library whose uploads are on; `desktopAddBooks`)
and a hidden link to that library's Add books page; `desktop-shell.js` shows `ts-wdrop` on a file drag, keeps the dropped `File`s, goes to Add books by the page's
own boosted link and hands them to its file input with a `change` event, so `internal/upload` and `upload.js` (unchanged) take them and refuse copy-protected files.
A folder, or no permission, gives a toast. Checks: `TestDeskDropOnlyWhenAllowed` (owner, admin, member, guest, browser, uploads off), `TestDeskDropBrowser` (Chromium,
stub channel: an `.aax` is refused with the Add books words and sends nothing; uploads off shows the words), the gates in the merge report. Limits:
`desktop-shell.js` 4,667 B gzip (row 4,736 B) and the desktop total 5,823 B (ceiling 5,888 B), over the approved 2,816 and 4,096 B and reported; a folder drop and a real
drag from Files are untested; the bar is kept across boosted visits, so a change of permission shows at the next full load. Next step: X6.

## Android app: A1 server (decision 0026)

**A1 status**: built 2026-11-06 on the branch `pkg-a1`. Plan [plans/android-a1.md](plans/android-a1.md), decision
[0026](decisions/0026-app-sign-in.md). Scope: migration `0031_device_codes` (`device_codes`, `sessions.device_id`); `auth.IssueDeviceCode`,
`SwapDeviceCode` and session binding (a revoked, pruned or signed-out device ends its window session; `/logout` and password changes end
both halves; "sign out everywhere else" from the phone keeps the phone); `POST /api/v1/auth/token` with a code (201, or 200 on reconnect);
`/app/connect` pages (no new stylesheet, plain form, 303 handoff); `GET`/`PATCH /api/v1/settings`, `PUT /api/v1/books/{id}/speed`,
`GET /api/v1/continue`, `speed` in the book answer, series next on `POST /api/v1/queue/next` (opt-in); `/sw.js` 404 and `native-audio.js`
in the Android window, no Downloads link there. Tests: `TestA1*` in `internal/auth`, `internal/db` and `internal/web` (every forbidden case
of the plan, plus the route-matrix rows and the a11y page rows). Limits: nothing was run on a phone (A2 builds the app); the Connect pages
are audited through the window-mark link; plain `http` is allowed and warned about. Next: A2, the Kotlin shell that uses this.

## M7 Research — dropped (decision 0007, 2026-11-02)

## Next stage (docs/plans/next-stage.md)

- **S1 Watch folders and a scheduled scan** (2026-11-05, done). Scope: `internal/watch` (fsnotify watcher,
  45 s quiet period, mount-table detection of NFS/CIFS and similar, 6 h scheduled scan, skip and catch-up for
  a missing folder), migration 0025 (`libraries.watch`; the unused `scan_interval_min` becomes the schedule),
  `scan.Enqueue` shared by the button, the watcher and the schedule, a "Finding new books" section on the
  Libraries page. Checks: `TestWatch*` (debounce, new folders, hidden folders, schedule table, network
  folder, folder limit, mount gone and back, mount table, routes with forbidden cases, migration).
  Limits: Linux inotify per-user limits (a library past 20000 folders falls back to the schedule); the
  scheduled scan counts any scan job as "last scanned".
- **S5 App keys and OPDS** (2026-11-06, done). Scope: migration `app_keys` (`devices.kind` gains `opds` and `koreader`;
  numbered 0026), `token.NewTypable` (24 characters, `a-z2-7`),
  `auth.IssueKey`/`LookupKey`/`OwnKeys`/`KeyHash` (opds: SHA-256 of the key; koreader: SHA-256 of the hex MD5, for S6),
  `share.BookFilterFor` (one `EXISTS` per right, agrees with `Can`), `library.TextCatalogue`, `POST /me/app-keys` and an
  App keys section on Your devices, `keyAuth` (Basic, user name must match, 10 failures per 15 minutes per address, then 429),
  `/opds` (OPDS 1.2 Atom: front page, All books, Newest, search and OpenSearch description, 50 a page, files, covers),
  `serveEbookFile` split out of `getEbookFile`, decision 0025 and its `AGENTS.md` line. Checks: `TestAppKey*`, `TestOPDS*`,
  `TestBookFilterForAgreesWithCan`, `TestMigrationAppKeys*`, `TestTypable*`; captures `app-keys-*` at four widths and four
  themes, no overflow, targets 44 px. Limits: no OPDS 2.0 or PSE, no audio, no per-shelf feeds, no key scopes beyond two kinds,
  keys do not expire by date. Next: S6 (done below).
- **S6 KOReader sync** (2026-11-06, done). Scope: migration `kosync` (`ebook_files.partial_md5` with its partial index, table
  `kosync_progress`; numbered 0028), `ebook.PartialMD5` (KOReader's `partialMD5`), the digest set in the ingest job and
  cleared by the reconcile of a changed file, the `ebook.digests` catch-up job (200 files a run, cursor so an unreadable file cannot loop, queued beside
  `EnqueuePending`), `Doc.TotalUnits`, `progress.KosyncPut/Get` (10,000 rows a person), `/kosync` (`users/create` always 402, `users/auth`, `PUT` and `GET` of
  `syncs/progress`) in front of the outer mux, `keyGuard` shared with OPDS (`keyAuth` is now a thin wrapper), `auth.KeyHashFromMD5`, the key page note. Inbound by
  percentage through `AnchorAtChar` into `reading_positions` only when `share.Can(ReadText)` passes; outbound EPUB as the chapter start and PDF by page.
  Checks: `TestKosync*`, `TestPartialMD5*`, `TestMigrationKosync*`; manual check on a real KOReader is open (`manual-checks.md`). Limits: the sync lands near the
  place, not on it; the short EPUB pointer is a guess until the manual check; no statistics, highlights, file-name digests, audio or "link this file" page.
- **S2 Whole backups to a folder the owner chooses** (2026-11-05, done). Scope: `backup.folder` setting,
  `backups.Mirror` (incremental by size and time through `tendshelf-backup/manifest.json`, copy through a
  temporary name, read back and compared by SHA-256, 30 day grace for deleted files, newest 7 database copies,
  `RESTORE.txt`), the folder refused inside the data folder or a library folder, a full disk reported as a
  plain job error, a scheduled retry that does not pile up local copies, the Backup folder section and the
  restore steps on the Backups page (owner only). Checks: `TestWholeBackup*` (mirror, retention, disk full,
  read-back, refused folders, service and job errors, routes with forbidden cases, restore into an empty
  folder and start). Limits: no encryption of the backup folder; restore is by copying (no restore button).
- **L3a Chapter editor** (2026-11-06, done; plan `docs/plans/next-server.md` section 2). Scope: `library.BuildChapters`,
  `ChaptersRev`, `ChaptersOverridden`; `finalize` keeps edited chapters (no clamping); `scan.ResetChapters`
  (`ErrChaptersBusy`); the write job's `chapter_fit` refusal; the Edit chapters page `/books/{id}/chapters/edit`
  (start and title per row, Listen, Delete, Add below, Move chapters, Reset with a confirm page), `POST
  /books/{id}/chapters` (draft round trips, Save with `rev`), `pages-chapters.css`; the "Edit chapters" link on the
  book's Chapters tab (EditBook only). No migration, no JS. Checks: `TestChapterEdit*` in library, scan, mediajobs
  and web (roles, text book 404, drafts store nothing, save, stale rev, refusals, CSRF, boosted answers, reset and
  busy reset, clock parsing); `TestChapterEditVisual` (390, 768, 1024, 1440). Limits: the editor address is
  `/chapters/edit` because `/books/{id}/chapters` is the Chapters tab (plan address collided); "move from" counts
  chapters from 1 and starts at 2; multi-file and FLAC books keep edits in the database only, and the page says so.
- **L3b Chapter lookup** (2026-11-06, done; plan `docs/plans/next-server.md` section 3). Scope: `online.Client.Chapters`
  (Audnexus `/books/{asin}/chapters`, stores in `regionOrder(language)` order, at most three, a 404 tries the next,
  all misses are `no_chapters`), `regionOrder` factored out of `searchAudnexus`; job kind `chapter_lookup`
  (`StartChapterLookup` reuses a waiting job for the same book and ASIN, `ChapterLookupFor` rejects another book's job,
  requests counted in `online_usage`); `POST /books/{id}/chapters/lookup`; the poll through `/hx/jobs/{id}?book=&n=`;
  the "Look up chapters" disclosure on `/books/{id}/chapters/edit` (ASIN prefilled from the book) and the result box
  with "Use titles and times" (`lookup_all`: replaces the draft, drops rows at or past the end and says how many,
  origin `online`) and "Use titles only" (`lookup_titles`: equal counts only, starts kept). Rows always come from the
  stored job result; nothing is stored until Save. No migration, no JS, no new stylesheet (rules added to
  `pages-chapters.css`). Checks: `TestChapterLookup*` in online, metadata and web (roles, bad ASIN, lookups off,
  poll states, forged and unknown jobs, drop past end, titles only, save stores `online`), the route matrix row,
  `TestChapterLookupVisual` (390, 768, 1024, 1440; `chapters-lookup-*`). Limits: the lookup form is a separate form,
  so pressing "Look up" discards unsaved edits; no scaling of times (Move chapters does that); bulk, automatic and
  other providers are out of scope.
- **S4 Duplicates and merge** (2026-11-06, done). Scope: migration `0027_book_merges` (`merged_folders`,
  `book_not_duplicates`), `library.MergeRowsSQL` shared with the ebook merge, `Repo.MergeBooks` (one transaction:
  progress newest wins and capped at the kept length, sessions, queue, book settings, grants, share links, ebook
  files, shelves, bookmarks, highlights, cover; refused for another library, a busy file write or a book that is
  not an audiobook), `Repo.Duplicates` (same audio files by size and quick hash, then same title, first author and
  first narrator within 60 s or 2 %), `MergePreview`, the scan leaving merged folders out (`BooksMerged`), old ids
  landing on the kept book (page and API redirect, progress and bookmark writes), `/admin/duplicates` with
  confirm, "Not the same" and "Copies you merged" (show a copy again, then scan). Checks: `TestDup*` (library,
  every table with a key to books handled on purpose, tiers, scope, merge moves, chain, refusals), `TestMerged*`
  (scan), `TestDup*` routes (roles, old ids, offline outbox, no right), `TestMigrationBookMerges*`, route matrix
  rows. Limits: the dropped copy's folder stays on disk and is never deleted; moved data stays on the kept book
  after "Show that copy again"; no cross-library or multi-way merge; a moved folder reappears as a new book.
  Words not in the deck are in `docs/copy-additions.md`.

- **S3 Library health** (2026-11-06, done). Scope: `library/health.go` (`Repo.Health`, `Repo.HealthPage`; groups
  missing, not opened, unreadable file, no chapters, no cover, no online match, chapters past the end; exact counts,
  rows filtered in SQL by scope, 50 per page), `Problems` now shares `noCoverCond` with it, `GET /admin/health` (owner and
  admins; others 404; unknown group 404) with a fix link per row (chapters page, book, the folder's emptied page when
  the whole folder went, the library's lookups page when lookups are on; an unreadable file says so in words), and a
  "Library health" button on `/admin/libraries`. No migration, no job, no JS. Checks: `TestHealth*` (library: every
  group, scope, agreement with `Problems`, paging, text and unsettled books absent; web: roles, fix links, paging,
  all-clear), route matrix and a11y rows, `TestHealthVisual` at 390, 768, 1024 and 1440 in dark and light.
  Limits: read only, no fixing from the page; ebook problems stay on `/admin/ebooks`; no 100,000-file perf row (see
  report). Words not in the deck are in `docs/copy-additions.md`.

- **L6 Listening timeline** (2026-11-06, done; plan section 9). Scope: migration `listening_recent` (index
  `listening_sessions_recent` on user, ended_at DESC, id), `library/timeline.go` (`Repo.Timeline`: keyset on
  ended_at and id, 50 a page, books through the scope in SQL, chapter found in Go from one batched read),
  `GET /me/listening/history?book=&cursor=` grouped by day in the person's zone, a "Go back here" seek control per
  session (no resume button), links "Where was I?" on `/me/listening` and "Listening history" on the Bookmarks tab.
  No JS, new stylesheet `pages-timeline.css` (in `noPrefetch`). Checks: `TestTimeline*` (library: keyset, ties,
  scope, revoked, chapter; web: roles, other person's rows, day grouping across midnight, paging, links),
  `TestMigrationListeningRecent*`, the 4 x 33,000 session perf row (history 0.4 ms against 150 ms),
  route matrix row, `TestTimelineVisual`. Limits: no night sleep, no editing or deleting sessions, no reading
  sessions, no API route. Words not in the deck are in `docs/copy-additions.md`.

- **R1 All my highlights** (2026-11-06, done; plan section 10). Scope: `library/highlights.go`
  (`Repo.AllHighlights`: own live rows, books filtered by view and `ReadText` (`BookFilterFor`) in SQL, search with
  escaped wildcards, colour, book, 50 a page with a total), `GET /me/highlights?q=&colour=&book=&page=`,
  `/me/highlights.md` and `.json`, a link beside the word list on Profile. `reader_marks.go` now has `marksMarkdown`,
  `marksText`, `marksJSON` and `sortMarks` shared with the per-book export (output unchanged), and `readerHref`
  from `readerCtx.href`. No migration, no JS, new stylesheet `pages-highlights.css` (in `noPrefetch`). Checks:
  `TestAllHighlights*` (library: scope, deleted, revoked, can_read=0, search with % and _, colour, paging; web:
  roles, other person's rows, revoked book absent on page and exports, export contents and headers, per-book
  export shape), route matrix rows, `TestHighlightsVisual`. Limits: read only (editing stays in the reader), no
  text bookmarks here, no API. Words not in the deck are in `docs/copy-additions.md`.
## M8 Android (decision 0024, plan `docs/plans/android.md`)

- **A0 Skeleton and playback proof** (2026-11-06, built on the emulator). Scope: `android/` Gradle project (wrapper seeded from the
  Gradle 9.8.2 distribution after checking its published SHA-256, `distributionSha256Sum` pinned; AGP 9.5.1, built-in Kotlin, Kotlin
  2.4.21 for the shared module, Media3 1.11.1; `minSdk 28`, `targetSdk 36`; Gradle dependency verification on, every artifact pinned in
  `android/gradle/verification-metadata.xml`); `shared/` (Kotlin, no Android imports: `ApiPaths`, `Position`, manifest types; 7 unit
  tests pass); `app/` (`PlaybackService`: `MediaLibraryService` + ExoPlayer, foreground type `mediaPlayback`, bearer header on
  `/api/v1/stream/{fileId}`; `MainActivity`: WebView at `/?shell=android&next=/`; hard-coded server `http://10.0.2.2:8097`, test-only
  sign-in by `POST /api/v1/auth/token`, credentials and book id only through start-intent extras, never in the APK); `make apk-debug`;
  `NOTICES.md` rows. Server untouched: the bearer stream needed no server change. Test server: own build on 127.0.0.1:8097, own data
  folder under `.local/tests/android/a0/`, three synthetic 40-minute MP3 files as one book; stopped and deleted afterwards.
- Acceptance, on `emulator-5554` (Pixel 9, Android 16, API 36):
  - Builds from a clean clone with `ANDROID_HOME` set, an empty `GRADLE_USER_HOME`, no Android Studio: `make apk-debug`, 38 tasks, APK
    13,241,043 bytes (12.6 MiB). The first attempt failed dependency verification on five POM/module files; fixed in the metadata.
  - Screen asleep (`input keyevent 223`), 30 minutes, one `dumpsys media_session` sample a minute: 31 of 31 samples PLAYING, position
    35,821 ms to 1,837,721 ms (+1,801,900 ms in 1,800 s). Second 30-minute run with the activity STOPPED (`dumpsys activity`
    state=STOPPED): 31 of 31 PLAYING, 530,620 ms to 2,334,307 ms, crossing from Part 1 to Part 2 on its own. Logs
    `lock-locked.log`, `lock-stopped.log` in the test folder. Limit: the emulator's lock screen has a credential that was not entered,
    so the screen could not be kept on with the activity stopped; both runs had the screen asleep, so the two cases differ only in what
    the activity was doing before (the first run's activity state was not logged). Two aborted runs (screen asleep too) are kept
    as `lock-stopped-aborted-*.log`.
  - `dumpsys activity services`: `isForeground=true types=0x00000002` (`mediaPlayback`).
  - `cmd media_session dispatch` (the `media` shell command is absent on this image): next moved Part 1 to Part 2; pause and play
    changed the state to PAUSED and PLAYING; previous went back (a first press restarts the file after 3 s, as Media3 does).
  - Activity finished with Back: the task was gone and the sound continued. Tapping the media notification brought `MainActivity` back
    and the server's sign-in page loaded in the WebView.
- Not proved by the emulator: a real device's Doze and OEM battery limits (the emulator was never in Doze; the operator repeats the
  screen-off test for an hour on a phone), real speaker or Bluetooth output, a killed process (the service shares the app's process;
  Back and task removal were tried, not `kill -9`), other Android versions, and a mobile network. The WebView page sits close under
  the status bar (A2 does the safe areas). Android Auto not tried.
- Unit tests of `shared` need `kotlin-test`, `junit` and `hamcrest` (not on the approved list; test-only, in `NOTICES.md`): approve or
  replace.

## Later

TV app (its own layouts, in the spirit of Apple TV and LG apps; not designed yet), OPDS, podcasts, RSS feeds of books/series, passkeys, OpenID
Connect, merge MP3 folder to M4B, custom metadata providers, Chromecast.

- **L5 Clips** (2026-11-06, done; plan `docs/plans/next-server.md` section 8). Scope: migration `0029_clips` (`end_ms`,
  `kind` on `bookmarks`; the orchestrator renumbers at merge), `progress.UpdateBookmark`/`BookmarkPatch`/`BookmarkKinds`
  (`SetBookmarkNote` calls it; an end needs an audio bookmark and must be after the position), the note sheet's kind
  radios and "Ends at" (`parseClock`, clamped to the book), the clip and kind chips and filters, kind and length on a
  row, "Play this clip", exports with kind and length, `clip.js` (stops a clip at its end; loaded by the Bookmarks
  tab only when a row is a clip; the Now panel jumps to a clip's start without the stop), API `endMs` and `kind`
  (additive; `POST` and `PATCH`; `docs/api.md`). `player.js` and `player-full.js` unchanged. Checks: `TestClip*` in
  progress, web (sheet, clamp, errors, roles, script only with a clip, filters, export, API) and db
  (`TestMigrationClips*`); browser `TestClipBrowserStopsAtEnd`, `TestClipBrowserSeekDisarms`, `TestClipVisual`
  (`clips-*`). JS: `clip.js` measured about 0.8 KB gzip (plan target 0.5 KB, with comments kept); app JS 90,143 B of
  the new 91,264 B ceiling (+640 B as planned). Limits: the stop follows `timeupdate` (about 250 ms steps); no audio
  export, no sharing, no waveform drag; a bad end on the sheet answers with a toast when it came through htmx.

**Docs for the work after 2026.2.0 (2026-11-06)**: `CHANGELOG.md` has an Unreleased section (watch folders, whole backups, chapter editor and lookup,
duplicates, library health, app keys and OPDS, KOReader sync, clips, listening history, all my highlights, the desktop title bar, now playing, tray,
notifications, More menu and drop a book); the README feature list matches; `docs/manual-checks.md` has the "Desktop app on your own computer" table (X4 to X6,
every row open); the GoTHW Wails note gained a Wails 3.1.0 section outside the repo. No code changed. Checks: `go build ./...`. Limits: the operator's rows
are not run, so the CGO-free default (X6 step 2) has not been switched. Next step: the operator runs the desktop table.

**A3 status (Android playback service, 2026-11-06)**: built on the branch `pkg-a3`, plan [plans/android-a2-a5.md](plans/android-a2-a5.md). A book is one
Media3 window in book time (`ConcatenatingMediaSource2`, missing parts as silence); `BookPlayer` makes next and previous chapters and skip the person's
interval; `BookEngine` starts books (a newer start cancels an older one) and publishes `ts.bookId`, `ts.chapterIdx`, `ts.error`; custom commands are for this app
only; `PlayerRemote` is the seam. Pure logic (chapter jumps, start place, silent ranges, URLs) is in `shared`. Emulator checks (Parts, Single, Odd, speed, skip,
notification, 30 min screen off, outage, revoked token, foreign package) passed; limits: no audible alignment measurement, fixture has no cover. Next step: A4.
