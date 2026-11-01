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
| M1 It plays | P1a-P1c | Not started | |
| M2 Family | P3 | Not started | |
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
combined with `ts-cluster`, fixed (nowrap row, `flex: none` children). Not yet checked by hand: WebKit
reduced motion (needs the desktop animation setting off), the WebKitGTK 6.0 MiniBrowser that Wails uses,
and press feedback by finger; all three are on the kitchen sink (`make run-dev`). Next: M1 (P1a).

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

### P1c Player and progress

- Mini-player, full player (phone, tablet) and desktop player bar and panel; cover accent only on the
  player root.
- Player module (`static/js/player.js`): multi-file timeline, chapters, speed 0.5-3.0, skip intervals,
  sleep timer with fade and +10 min, auto-rewind after pause with its hint, Media Session, keyboard
  shortcuts, progress sync every 10 s and on pause/hide (`sendBeacon`) firing `progress-saved`, mark
  finished, "continue on the other device?" prompt, bookmarks and notes.

Exit: play, seek across a file boundary, speed, sleep timer and resume on a second browser all work;
first sound under 1 s on localhost; the player is usable one-handed at 390 px (recorded manual check).

## M2 Family (P3 multi-user, invitations, sharing)

- Roles owner/admin/member/guest; users admin page; grants per library and per book with expiry and
  download permission; `share.Can` with a role-by-action test matrix.
- Invites by link and optional email (SMTP settings); share links for one book; Rosa's invite flow and
  large-text Home (`screens/people/Invite*`, `RosaHome`); Share & invite pages.
- Device tokens and the JSON API v1; shared shelves; "Shared with you" Home row.

Exit: for every route, a test proves each role gets exactly the documented access, and forbidden books
never appear in lists, counts, search or covers. First tagged release (v0.1) after this milestone.

## M3 Pocket (P4 PWA offline and polish)

- Manifest, service worker, install prompt, offline downloads with persistent storage, offline queue,
  Downloads tab and offline banner (`screens/browse/DownloadsPhone`, `screens/states/StateOffline`).
- Listening statistics, Eyes-free (car) mode, shake to extend sleep (where permitted), scheduled
  database backups.
- Accessibility audit (WCAG 2.2 AA, contrast theme 7:1) and performance budget checks at the four widths.

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
