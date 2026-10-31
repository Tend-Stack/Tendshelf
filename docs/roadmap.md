# Roadmap

Each phase ends with a usable vertical slice. A phase is done when its exit criteria pass on the gates
in `AGENTS.md` and the status below records scope, checks, limits and next step.

## Status

| Phase | State | Evidence |
| --- | --- | --- |
| P0 skeleton | **Partly done**: documents, stdlib server on 127.0.0.1:8095 with `/` and `/healthz`, `internal/db` schema-check placeholder, Makefile. Dependencies not yet added. | `go build ./... && go test ./...` pass (initial commit). |
| P1-P6 | Not started | |
| R read-along (R1-R6) | Designed 2026-10-31 ([read-along.md](read-along.md), [decision 0001](decisions/0001-read-along.md)); not started. Dependencies await approval. | Design only; no code, no checks run. |

## P0 Skeleton

- Add the approved dependencies (see `docs/architecture.md`, "Dependencies awaiting approval").
- `internal/db`: open with read-only pre-check, `application_id`, `user_version`, embedded migrations,
  `VACUUM INTO` backups, typed refusal errors, migration test harness. Migration `0001_init.sql`
  creates `settings`, `users`, `sessions`.
- `internal/web`: layout templ component (header, `#main`, persistent player bar slot), static asset
  embedding with hashed names, security headers, CSRF middleware, error page, vendored htmx.
- `internal/jobs`: queue table, runner, lease recovery, `/hx/jobs/{id}` fragment.
- CI-equivalent `make check` green; `CGO_ENABLED=0 make build` green.

Exit: a templ page served with htmx loaded from `/static`, migrations applied to a fresh database, a
newer-schema database refused untouched (test), a job survives a restart (test).

## P1 Single-user library, scanner, player, progress

- `/setup` creates the owner; login/logout; sessions.
- Libraries and folders (admin page); scanner for Author/Series/Book, single files, numbered MP3
  folders, multi-disc folders; DRM refusal; move detection by quick hash; missing files kept.
- ffprobe probe, chapters (embedded, or one per file), embedded/folder covers and thumbnails.
- Local details: folder names and file tags with per-field source (`metadata_values`).
- Home, library grid (infinite scroll), book page, authors, narrators, series, search (FTS5), personal shelves.
- Player (`static/js/player.js`): multi-file timeline, chapters, speed 0.5-3.0, skip intervals, sleep
  timer with fade, auto-rewind after pause, Media Session, keyboard shortcuts, progress sync every 15 s
  and on pause/hide (`sendBeacon`), mark finished, bookmarks and notes.
- Compatibility copies for unplayable formats.

Exit: on a synthetic 200-book fixture library, first scan groups every layout correctly; play, seek
across a file boundary, speed, sleep timer and resume on a second browser all work; first sound under
1 s on localhost; Playwright-free checks are Go `httptest` plus a manual phone check recorded here
(browser automation tooling must not add Node; evaluate a Go-driven option such as chromedp separately).

## P2 Metadata providers, covers, write-to-file

- Sidecars: `.opf`, `.nfo`, `desc.txt`, `reader.txt`, `metadata.json`; precedence names < tags <
  sidecars < user/online choices.
- Online lookup (explicit button): Audnexus by ASIN (other Audible regions), Audible catalogue,
  Google Books, Open Library, iTunes; scoring, cache, spacing; field-by-field compare and apply.
- Covers: online (fetched by the server), upload, choose; author photos and bios from Audnexus.
- Write details into the file with verification, backup, "Looks good" and Undo; restart recovery.

Exit: ported parser tables from the Extractor tests pass in Go; write-details round-trip on synthetic
m4b, mp3 and flac with real ffmpeg (`TENDSHELF_TEST_FFMPEG`) keeps the audio hash; a crash injected at
each `file_writes` state recovers correctly.

## R Read-along (after P2)

Integrated into the book, player, position, sharing and offline downloads; not a second app. Starts
after P2 (player and details exist). R5's sharing part needs P3 and its offline part P4; until then
those rows stay open in the status. Design: [read-along.md](read-along.md).

- **R1 Ebook ingestion**: `internal/ebook`, migration `ebooks` (`ebook_files`, `editions`,
  `edition_chapters`, `user_settings`); scanner finds ebooks in book folders; upload on `/books/{id}/text`;
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
- **R4 Read-along UI**: `/books/{id}/read` with Listen, Read along, Read, Focus line (Car once P4 lands);
  `static/js/readalong.js`; auto-scroll, Back to the voice, tap to play; reader settings and vendored
  fonts; migration `highlights` (`highlights`, `bookmarks.edition_id/sid`, `notes.highlight_id`); highlights,
  bookmarks and notes in both directions. Exit: handler tests by role; contrast checked for every theme
  and highlight colour; manual check at 390/768/1440 px recorded (Go-driven browser check once chosen);
  highlight changes within one frame of a sentence start on a mid-range phone (measured).
- **R5 Cross-format position**: `progress.position_source/text_*`; Read mode drives the position;
  edition switch maps through time or quote; `can_read` on grants and share links with the P3 matrix;
  offline text download with P4. Exit: read on one browser, press play on another, the voice starts at
  that sentence; guest without `can_read` gets 404 on every text route; airplane-mode read-along check.
- **R6 Media Overlays**: import SMIL when the audio matches, otherwise align; export a new EPUB 3.3 with
  sentence-level overlays. Exit: import fixtures (match used, mismatch ignored); export then re-import
  within 1 ms; opt-in epubcheck passes.

## P3 Multi-user, invitations, sharing

- Roles owner/admin/member/guest; users admin page; grants per library and per book with expiry and
  download permission; `share.Can` with a role-by-action test matrix.
- Invites by link and optional email (SMTP settings); share links for one book.
- Device tokens and the JSON API v1; shared shelves.

Exit: for every route, a test proves each role gets exactly the documented access, and forbidden books
never appear in lists, counts, search or covers.

## P4 PWA offline and polish

- Manifest, service worker, install prompt, offline downloads with persistent storage, offline queue.
- Listening statistics, "continue on this device?" prompt, car mode (big buttons), shake to extend
  sleep (where permitted), scheduled database backups.
- Accessibility audit (WCAG 2.2 AA) and performance budget checks at 390/768/1440 px.

Exit: a downloaded book plays in airplane mode on Android Chrome and iOS Safari (recorded manual
check), and offline progress syncs on reconnect.

## P5 Wails desktop

- `desktop/` module, Wails v3 pinned; connect-to-server and personal modes; media keys; tray.

Exit: Linux build runs both modes in an isolated session; other platforms recorded as not verified.

## P6 Research module port

- Port the TendVox Extractor: clip extraction, categories, measurements, coverage, keep-numbers/delete-
  clips, `tendvox-reference-styles` v1 export; off by default, admin-only, fair-practice rules.

Exit: the Go export matches the Extractor's export for the same synthetic fixture within stated
tolerances; disabled module returns 404 for every research route.

## Later

Native mobile (evaluate Wails v3 mobile when stable), OPDS, podcasts, RSS feeds of books/series,
passkeys, OpenID Connect, folder watcher, merge MP3 folder to M4B, custom metadata providers, Chromecast.
