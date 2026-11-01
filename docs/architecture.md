# Architecture

One Go process serves pages, fragments, a small JSON API, audio and read-along text. SQLite (WAL) holds everything that
is not an audio file or a cached image. ffmpeg/ffprobe are child processes, and so is the optional alignment worker for read-along
([read-along.md](read-along.md), [decision 0001](decisions/0001-read-along.md)). No Node, no SPA.

## Invariants

1. Library audio files are never written, renamed or deleted, except by the write-details job, which
   verifies before swapping and keeps a backup until the user confirms or undoes.
2. Copy-protected files are never decoded, streamed, remuxed or analysed. The same holds for ebooks:
   library ebooks are read-only, uploads are kept byte-identical, protected ebooks are refused.
3. Every read of a book, file, cover, progress row or search result passes one authorization function
   (`share.Can`), and list/search queries filter by grants in SQL.
4. An older binary never opens a newer database writable; it refuses and leaves the file byte-identical.
5. No outbound network traffic except explicit metadata lookups/cover fetches and configured SMTP.
6. Positions are milliseconds on the book's global timeline (all files concatenated); the newest
   client update wins per user and book.
7. Background work is a persisted job; a restart resumes or re-queues it, never silently repeats a
   file swap.
8. The server binary builds with `CGO_ENABLED=0` and contains no JavaScript build output.
9. A read-along time is measured from the audio or marked as a gap, never interpolated; an alignment is
   keyed by recipe, model hashes, edition text hash and audio quick hashes, so a re-run is reproducible.
10. A text location is stored beside the position (invariant 6), never instead of it.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/tendshelf` | Flags/env config, open the database, start jobs and the HTTP server, graceful shutdown. Subcommands later: `tendshelf migrate`, `tendshelf reset-password`, `tendshelf version`. |
| `internal/db` | Open/check/migrate SQLite, `application_id`/`user_version`, pre-migration backups, ID generation, transactions helper, settings (`GetSetting`/`PutSetting`) and audit-log (`Audit`) helpers. Owns `migrations/*.sql`. |
| `internal/library` | Libraries, folders, books, files, chapters, people, series, labels, shelves: the read/write repository used by everything else. `Scope` (grant filters with aliases `b` and `l`) and `Listable`. No HTTP, no ffmpeg. |
| `internal/scan` | Walks library folders, groups files into books, detects new/changed/moved/missing files, refuses DRM, queues probe and metadata jobs. Ports ABS `scandir.js`/`BookScanner.js` behaviour. `ListFolders` and `Places` feed the folder browser: folder names only, never a file name, links never followed, 5,000 entries / 500 folders / 3 s. |
| `internal/media` | ffprobe/ffmpeg runner (fixed args, protocol whitelist, deadlines, output caps); probe parsing (streams, tags, chapters, encryption); cover extraction and thumbnails; compatibility copies; stream-copy remux and verification. |
| `internal/mediajobs` | Jobs `media.cover` (cover choice, stored picture, thumbnails, tint and accent colours) and `media.compat` (compatibility-copy cache with LRU cap). Imports library, media and jobs; never scan or metadata. Registered by `cmd/tendshelf/hooks.go`; the cover job follows the scanner's settled books. |
| `internal/metadata` | Local sources (names, tags, sidecars), the field-provenance merge, abridged/dramatized detection. Ports `extractor_details.py`. |
| `internal/metadata/online` | Provider clients (Audnexus, Audible catalogue, Google Books, Open Library, iTunes), scoring, per-site spacing, 10 s timeouts, 7-day cache, safe cover fetch (5 MB, magic-byte check). The only `http.Client` in the program. Ports `extractor_online.py`. |
| `internal/users` | Accounts, roles, argon2id password hashing (at most 4 at once, never inside a write transaction), account validation, owner creation. Devices and per-user settings later. |
| `internal/auth` | Sessions (cookie, hashed tokens), sign-in/sign-out/setup service, failed-attempt limits, trusted-proxy client address, setup code, safe `next` redirects, middleware that puts the user in the request context. Device tokens (bearer) and passkeys later. CSRF lives in `internal/web`. |
| `internal/share` | `share.Can(user, action, target)`, the action names and the SQL listing filters (owner and admin only until P3); later grants, invites, share links, invite email (`mail.go`, `net/smtp`). |
| `internal/progress` | Progress, listening sessions, bookmarks, notes; the newest-wins merge; offline batch apply. |
| `internal/jobs` | Persisted job queue: leases, per-kind concurrency, cancellation, progress events, restart recovery. |
| `internal/web` | templ pages/components, static assets (embedded), HTMX handlers, security headers, the page router. |
| `internal/api` | JSON API v1 for native/mobile clients and the player's sync calls. Same services as `web`. |
| `internal/ebook` | Ebook ingestion (R1): format detection, copy-protection check, one parser per format (EPUB 2/3 with SMIL, PDF text layer, TXT/Markdown, DOCX, HTML, FB2, DRM-free MOBI/AZW3), the canonical document `tendshelf-doc-v1` with stable IDs and source offsets, sentence segmenter, pairing scores. No HTTP, no model. |
| `internal/align` | Read-along alignment (R3): normaliser, emissions cache, greedy CTC transcript, block mapping and anchors, CTC forced alignment, confidence, timing files, the `align.book` job; Media Overlays import/export (R6). Pure Go. |
| `internal/align/runtime` | Checks runtime components (hashes, `--describe`, licence), launches and owns the loopback emissions worker (`tendshelf-emissions-v1`), idle stop, parent-death cleanup. The only code that starts it. |
| `internal/research` | Optional Research module (P6): clip extraction, measurements, export. Registered only when enabled. |
| `desktop/` (P5) | Separate Go module: Wails v3 shell. Not imported by the server. |

Dependency direction: `web`, `api` → services (`library`, `progress`, `share`, `users`, `metadata`,
`scan`, `jobs`, `research`, `ebook`, `align`) → `db`, `media`. `align` uses `ebook` (documents) and
`media` (ffmpeg decode); `ebook` imports neither. Services never import `web` or `api`.

## Data model

All tables `STRICT`. IDs TEXT (ULID-style, prefixed: `usr_`, `lib_`, `bk_`, `af_`, ...). Times INTEGER
Unix ms UTC. Positions INTEGER ms. Booleans INTEGER 0/1. Foreign keys on.

| Table | Columns (key ones) | Notes |
| --- | --- | --- |
| `settings` | `key` PK, `value_json` | Server settings (`research.enabled`, `online.enabled`, `smtp.*`, `listen.addr`, `server.name` (JSON string), `setup.done` (JSON true, set when the setup folder is added)). |
| `users` | `id`, `username` UNIQUE NOCASE, `display_name`, `email`, `role` CHECK IN ('owner','admin','member','guest'), `password_hash`, `disabled`, `created_at`, `last_seen_at` | Exactly one owner (partial unique index on `role='owner'`). |
| `sessions` | `token_hash` PK, `user_id`, `created_at`, `expires_at`, `last_seen_at`, `user_agent` | Cookie sessions. |
| `devices` | `id`, `user_id`, `name`, `kind` ('web','pwa','desktop','mobile'), `token_hash` UNIQUE NULL, `created_at`, `last_seen_at`, `revoked_at` | Device tokens for the API. |
| `invites` | `id`, `token_hash` UNIQUE, `created_by`, `email` NULL, `role` ('member','guest'), `grants_json`, `expires_at`, `max_uses`, `use_count`, `revoked_at`, `created_at` | Accepting creates a user and its grants in one transaction. |
| `share_links` | `id`, `token_hash` UNIQUE, `book_id`, `created_by`, `expires_at` NOT NULL, `password_hash` NULL, `can_read` (R5, default 0), `revoked_at`, `created_at` | Anonymous listen-only link for one book; progress kept per browser in local storage only. |
| `grants` | `id`, `user_id`, `library_id` NULL, `book_id` NULL, `can_download`, `can_read` (R5, default 0), `expires_at` NULL, `created_by`, `created_at` | CHECK exactly one of `library_id`/`book_id`. Owner/admin implicitly see all. |
| `libraries` | `id`, `name`, `created_at`, `scan_interval_min` | |
| `library_folders` | `id`, `library_id`, `path` UNIQUE, `created_at` | |
| `books` | `id`, `library_id`, `folder_id` (cascade on folder removal; the scan works per folder), `root_path` UNIQUE, `title`, `sort_title`, `subtitle`, `description`, `publisher`, `published_year`, `language`, `isbn`, `asin`, `abridged`, `dramatized`, `explicit`, `duration_ms`, `cover_id`, `refused_reason` NULL, `added_at`, `updated_at`, `missing_since` NULL | Resolved values (cache of the merge). |
| `audio_files` | `id`, `book_id`, `path` UNIQUE, `idx`, `size`, `mtime_ns`, `quick_hash`, `sha256` NULL, `container`, `codec`, `bitrate`, `channels`, `sample_rate`, `duration_ms`, `offset_ms`, `encrypted`, `probe_json`, `probe_error` NULL, `missing_since` NULL, `updated_at` | `probe_error` (migration 0003) holds the media error code of a file that could not be read; a file is settled when `probe_json` or `probe_error` is set, so a damaged file never blocks its book. `offset_ms` places the file on the global timeline. `missing_since` flags a lost part of a multi-file book instead of dropping it. `quick_hash` = SHA-256 of size + first and last 1 MiB. |
| `chapters` | `book_id`, `idx`, `start_ms`, `end_ms`, `title`, `source`; PK (`book_id`,`idx`) | Global timeline. Source: 'file','files','sidecar','online','user'. |
| `metadata_values` | `book_id`, `field`, `source`, `value_json`, `observed_at`; PK (`book_id`,`field`,`source`) | Every candidate value with its source. `internal/metadata` (job `metadata.local`, queued when a book settles) writes the `folder` and `tags` rows and resolves each field by the fixed order user, opf, txt, nfo, tags, folder, online; `user` rows are never written by a job and win even when empty. |
| `people` | `id`, `name`, `sort_name`, `description`, `image_id`, `asin` | Authors and narrators share one table. |
| `book_people` | `book_id`, `person_id`, `role` ('author','narrator'), `idx` | |
| `series` / `book_series` | `id`, `name`, `sort_name`, `description` / `book_id`, `series_id`, `sequence` TEXT, `idx` | Sequence is text ("1.5", "0"). |
| `book_labels` | `book_id`, `kind` ('genre','tag'), `value` | |
| `covers` | `id`, `book_id`, `source`, `mime`, `width`, `height`, `sha256`, `path`, `tint` NULL, `accent_json` NULL, `created_at` | Files under `<data>/covers/`; thumbnails under `<data>/cache/thumbs/`. `tint` is the sampled hex colour, `accent_json` the checked accent per theme (`dark`, `light`, `sepia`); NULL means the ember fallback. |
| `online_cache` | `provider`, `query_key`, `response_json`, `fetched_at`; PK (`provider`,`query_key`) | Expires after 7 days. |
| `progress` | `user_id`, `book_id`, `position_ms`, `duration_ms`, `finished_at` NULL, `client_updated_at`, `server_updated_at`, `device_id`, `position_source` ('audio','text', R5), `text_edition_id` NULL, `text_sid` NULL, `text_off` NULL; PK (`user_id`,`book_id`) | A text update sets `position_ms` to the sentence start (or, in a gap, the previous timed sentence's end). |
| `listening_sessions` | `id`, `user_id`, `book_id`, `device_id`, `started_at`, `ended_at`, `start_ms`, `end_ms`, `listened_ms`, `speed` | Statistics (P4). |
| `bookmarks` | `id`, `user_id`, `book_id`, `position_ms`, `title`, `edition_id` NULL, `sid` NULL (R4), `created_at`, `updated_at`, `deleted_at` | Soft delete so offline clients sync deletions. `sid` set when made in the text. |
| `notes` | `id`, `user_id`, `book_id`, `position_ms` NULL, `highlight_id` NULL (R4), `body`, `created_at`, `updated_at`, `deleted_at` | A note on a text passage hangs on its highlight. |
| `shelves` / `shelf_items` | `id`, `owner_id`, `name`, `shared` (P3), `created_at` / `shelf_id`, `book_id`, `idx`, `added_at` | |
| `jobs` | `id`, `kind`, `state` ('queued','running','done','failed','cancelled','held'), `payload_json`, `result_json`, `error_code`, `error_text`, `attempts`, `max_attempts`, `lease_until`, `not_before`, `progress_json`, `created_by`, `created_at`, `updated_at` | |
| `file_writes` | `id`, `job_id`, `audio_file_id`, `state` ('copying','verifying','swapped','confirmed','undone','failed'), `original_path`, `backup_path`, `temp_path`, `before_sha256`, `after_sha256`, `audio_hash`, `created_at`, `updated_at` | The write-details ledger; drives restart recovery and Undo. |
| `audit_log` | `id`, `at`, `user_id`, `action`, `target`, `detail_json` | Logins, invites, grants, file writes, research toggles. |
| `books_fts` | FTS5, contentless with delete, over title, subtitle, authors, narrators, series, description; rowid from `books_fts_ids`; kept by triggers on books, book_people, book_series, people and series names (migration 0004); `books_fts_vocab` feeds did-you-mean. | Notes are per user: searched with a user filter, not in this index. |
| `user_settings` | `user_id`, `key`, `value_json`, `updated_at`; PK (`user_id`,`key`) | The one per-user settings store (playback preferences, `reader.v1`, chosen edition per book `edition.<bookID>`). |
| `ebook_files` (R1) | `id` (`ebf_`), `sha256` UNIQUE, `size`, `format` CHECK IN ('epub','pdf','txt','md','docx','html','fb2','mobi','azw3'), `origin` ('library','upload'), `path` UNIQUE, `library_folder_id` NULL, `mtime_ns`, `quick_hash`, `refused_reason` NULL ('drm','unsupported','damaged','too_large','no_text'), `added_at`, `missing_since` NULL | Library ebooks read-only; uploads under `<data>/ebooks/`. |
| `editions` (R1) | `id` (`ed_`), `ebook_file_id`, `book_id` NULL (NULL = unpaired), `doc_sha256`, `parse_recipe`, `title`, `authors_json`, `language`, `isbn`, `asin`, `layout` ('reflow','page'), `chapter_count`, `word_count`, `has_overlays`, `is_default`, `pair_source` ('folder','upload','identifier','title','user'), `pair_score` REAL, `paired_by` NULL, `paired_at`, `created_at`, `updated_at` | Partial unique index: one `is_default` per book. Document file `<data>/text/<doc_sha256>.json.gz`. |
| `edition_chapters` (R1) | `edition_id`, `idx`, `title`, `src`, `front`, `word_count`, `sentence_count`, `text_sha256`; PK (`edition_id`,`idx`) | `text_sha256` over the normalised text drives re-alignment of changed chapters only. |
| `runtime_components` (R3) | `id` (`rt_`), `kind` ('emissions-model'), `name`, `language`, `revision`, `model_sha256`, `files_json` (path, size, sha256), `python_path`, `worker_sha256`, `licence_spdx`, `licence_url`, `source_url`, `checked_at`, `disabled` | Only checked rows are usable; the licence is shown on `/admin/runtimes` and `/about`. |
| `alignments` (R3) | `id` (`al_`), `edition_id`, `book_id`, `source` ('model','overlay'), `recipe`, `runtime_id` NULL, `model_sha256` NULL, `audio_key`, `doc_sha256`, `state` CHECK IN ('queued','emitting','aligning','done','partial','failed','cancelled','held'), `labels_json` (`abridged`, `different_edition`), `job_id`, `coverage` REAL, `sentences_total`, `sentences_timed`, `error_code` NULL, `created_at`, `finished_at` NULL, `superseded_by` NULL | `audio_key` = SHA-256 of the ordered audio `quick_hash` list. The newest `done`/`partial` row per edition is used. |
| `alignment_chapters` (R3) | `alignment_id`, `chapter_idx`, `state` ('pending','aligned','unmatched','failed'), `chapter_key`, `start_ms` NULL, `end_ms` NULL, `sentences_total`, `sentences_timed`, `mean_confidence` REAL; PK (`alignment_id`,`chapter_idx`) | Timing file `<data>/align/<chapter_key>.json.gz`, shared by every alignment with the same key. |
| `highlights` (R4) | `id` (`hl_`), `user_id`, `book_id`, `edition_id`, `start_sid`, `start_off`, `end_sid`, `end_off`, `quote`, `quote_prefix`, `quote_suffix`, `start_ms` NULL, `end_ms` NULL, `alignment_id` NULL, `colour` ('yellow','green','blue','pink'), `created_at`, `updated_at`, `deleted_at` | Text range and time range. Private to `user_id`. Soft delete for offline sync. |
| `research_*` (P6) | `research_sources`, `research_clips`, `research_measurements` | Created by the P6 migration. |

## Media handling

- **Supported**: m4b, m4a, mp4, mp3, flac, ogg, opus, wav, aac, webm. **Refused**: aax, aaxc, aa, m4p,
  and any stream ffprobe reports as encrypted. ffprobe has no `-nostdin`; it runs with stdin from
  /dev/null (ffmpeg gets `-nostdin`).
- **Streaming**: `GET /stream/{audioFileID}` uses `http.ServeContent` on the opened file (HTTP Range,
  `If-Range`, `ETag` = quick hash, `Cache-Control: private`). Authorization is checked before opening.
  The player maps the global timeline to (file, offset) using `offset_ms`.
- **Transcoding policy**: none by default (direct play). The player reports `audio.canPlayType()` for the
  book's codecs; if the browser cannot play a file, the server creates a **compatibility copy** (AAC-LC
  128 kbit/s in `.m4a`, faststart, stream-copied when the codec is already AAC) as a job, stored in
  `<data>/cache/compat/` keyed by `quick_hash` + recipe version, total size capped (default 5 GiB,
  least recently used removed). `GET /stream/{audioFileID}?compat=1` serves it once ready. No live
  HLS; reconsider only with a measured need. Built by `internal/mediajobs` (job `media.compat`): the
  cache is the file system alone (`<quick_hash>-r<recipe>.m4a`, last use = file mtime, oldest evicted
  above the cap, other recipes removed; `-compat-cache-gib`, default 5), the result is probed (mp4,
  AAC, length within 1 %) before it is renamed into place, and no database row exists for it.
  `media.MayNeedCompat` names the formats browsers always play; the player's `canPlayType` decides
  when to ask (P1c). No free-space check before converting: the cap bounds the cache.
- **Covers**: job `media.cover` (`internal/mediajobs`), queued for each book the scanner settles. A
  folder image the owner placed wins: `cover.*`, then `folder.*` (folder books only; jpg, jpeg, png,
  webp, regular files, at most 20 MiB); otherwise the embedded picture of the first playable file that
  has one. Embedded JPEG and PNG pictures keep their bytes; a WebP folder image is converted to PNG by
  ffmpeg (no `x/image`). Stored as `<data>/covers/<sha256>.<jpg|png>`; thumbnails (160, 320, 640 px
  JPEG, fitted, never enlarged) produced by ffmpeg in `<data>/cache/thumbs/`. A cover whose source is
  `upload` or `online` is never replaced; rerunning on unchanged input writes nothing. Served by
  `GET /covers/{bookID}?w=` after `book.view`; thumbnail fallback to the next size, then the original; ETag
  per size; immutable only with the `v` hash.
- **Cover colour** (at scan time, standard library `image/jpeg` on the 160 px thumbnail): the dominant
  colour ignoring near-black, near-white and small areas becomes `tint`; converted to OKLCH, chroma
  capped at 0.10, lightness set per theme (dark 0.75-0.85, light and sepia 0.38-0.46), then checked
  (play-button text 4.5:1, scrubber fill 3:1 on its ground). Failing or grey covers fall back to ember.
  Rules: handoff `docs/design/tendshelf-handoff/docs/HANDOFF.md`, "Cover accent and theme rules". The contrast theme never uses it. The handoff's example accent pairs were hand-tuned outside its own rules; Tendshelf follows the rules (goldens and ΔE_OK tolerances in internal/media/accent_test.go).
- **Write details into the file** (P2): `ffmpeg -map 0 -c copy` with the new tags and chapters into a
  temp file in the same directory; verify the audio stream hash (`-map 0:a -c copy -f hash`) equals the
  original's, duration within 50 ms, chapter count and times equal, tags read back; check free space
  (file size + 5 %) and refuse files modified in the last 10 minutes; rename original to
  `<name>.before-tags.<ext>`, rename temp into place, record `file_writes` state `swapped`. "Looks good"
  deletes the backup (`confirmed`); "Undo" restores it (`undone`). Restart recovery reads `file_writes`
  and completes or rolls back each half-done row; it never re-runs ffmpeg on its own.

## Background jobs

Kinds: `scan.library`, `media.probe`, `media.cover`, `media.compat`, `metadata.local`,
`metadata.online` (only user-triggered), `media.write_details`, `research.extract`, `research.measure`,
`ebook.ingest` (R1), `align.book` (R3, states and resume in [read-along.md](read-along.md) section 2),
`align.export_overlays` (R6).
A job runner per kind with bounded concurrency (probe 2, compat 1, write_details 1, ingest 1, align 1, export 1). Leases of 60 s
renewed while running; on startup, expired `running` jobs return to `queued` except `write_details`,
which goes to `held` until `file_writes` recovery decides, and `align.book`, which resumes from its
emissions cache and finished chapters (a held alignment waits for Resume or Discard). Cancellation via context. Progress is
written to `progress_json` at most once per second and read by `/hx/jobs/{id}` polling.
Claiming a job bumps `attempts`, which is the fencing token: every write a running handler makes is
conditioned on `state='running' AND attempts=?`, so a handler that lost its lease is cancelled with
`ErrLeaseLost` and never writes a result. Startup recovery also runs every 30 s as a sweep. A running row
whose kind is `HoldOnRecovery` or not registered goes to `held` (unknown work never re-runs on its own),
and a row that used all its attempts goes to `failed` (`interrupted`). On shutdown, handlers are cancelled
with `ErrShutdown` and their rows go back to `queued` without using an attempt (or to `held`).
A handler that writes other tables begins its write transaction with `Task.Fence` (the same check as above,
as a read inside the transaction), so a run that lost its lease writes nothing. Probe dedupe: after every
scan, `scan.library` queues `media.probe` for each live unsettled file that has no queued, running or
held probe, found with `Runner.ActiveKeys` (so a crash only costs a re-queue, with no jobs-table
migration). Finalize in the settling tx: the probe that settles a book's last unread file finalises the
book (play order, offsets, duration, chapters, refused reason) in that same write transaction.

## HTTP routes

Pages (GET; full page; navigation via `hx-boost` selecting `#main`):

| Route | Page |
| --- | --- |
| `/` | Home (P1b-5): greeting, "New on the shelf" (the 12 newest books as spines) and the first-run, nothing-shared and scanning states; the continue-listening hero and the series, shared and because rows arrive with P1c and P3 |
| `/setup` | First run (only while no owner exists, 404 after; always needs the setup code from the server log): create the owner account first, then name the server and add the first folder ([decision 0002](decisions/0002-design-handoff.md)). The next steps are owner-only (404 for anyone else): `/setup/server`, `/setup/folder` (`?job=` shows the scan streaming in), `/setup/done`; once `setup.done` is set the first two redirect to `/` |
| `/login`, `POST /logout` | Plain forms with full page loads (no hx-boost); 422 on a wrong answer, 429 when limited; `next` is checked by `auth.SafeNext` |
| `/libraries`, `/libraries/{id}` | Library (P1b-5): every visible book, or one library (404 when not visible); `?view=grid\|list&sort=added\|title\|author\|duration&genre=` (up to five, all must match)`&cursor=&sheet=filters`, all in the address; a cursor page is a full page with "Back to the start"; phone and tablet filter in a sheet, from 1280 px in a bar |
| `/books/{id}` | Book: cover, details, chapters, play, bookmarks, notes |
| `/books/{id}/edit`, `/books/{id}/match` | Edit details; online lookup and field compare (P2) |
| `/authors/{id}`, `/narrators/{id}`, `/series/{id}` | |
| `/search?q=` | |
| `/shelves`, `/shelves/{id}` | |
| `/me` | Account, devices, playback preferences, downloads (P4), statistics (P4) |
| `/invite/{token}`, `/s/{token}` | Accept an invite; anonymous share link |
| `/books/{id}/text` (R1) | The book's text editions: upload, pairing, default edition, alignment status, export |
| `/books/{id}/read?mode=readalong\|read\|focus&edition=&at=` (R4) | Reader inside the layout (player keeps playing) |
| `/admin/users`, `/admin/libraries`, `/admin/jobs`, `/admin/settings`, `/admin/research` | Admin; research only when enabled. `/admin/libraries` (P1b-4) lists folders and problems, starts scans, and links to `/admin/libraries/folders/new` and `/admin/folders/{id}/emptied`; scan status is `GET /hx/jobs/{id}`. The folder browser is the `?browse=<path>` mode of `/admin/libraries/folders/new` (sheet) and `/setup/folder` (inline); picking links back with `?folder=<path>`, and the submit still runs the add-folder checks |
| `/admin/ebooks` (R2), `/admin/runtimes` (R3), `/about` (R3) | Unpaired and refused ebooks; word-timing runtime check and licences; credits and model licences |

HTMX fragments (`/hx/...`): `GET /hx/libraries/items` and `GET /hx/libraries/{id}/items` with the same query as the page (infinite scroll; the sentinel is a link to the next page, so "Show more" works without JavaScript; a bad cursor is a 400 toast),
`GET /hx/search/suggest?q=`, `GET /hx/books/{id}/bookmarks`, `POST /hx/books/{id}/bookmarks`,
`DELETE /hx/bookmarks/{id}`, `POST /hx/books/{id}/finished`, `GET /hx/jobs/{id}`,
`POST /hx/books/{id}/match` (starts lookup job), `GET /hx/books/{id}/compare?candidate=`,
`POST /hx/books/{id}/apply`, `POST /hx/shelves/{id}/items`. Read-along: `POST /hx/books/{id}/editions`
(upload), `POST /hx/editions/{id}/pair`, `POST /hx/editions/{id}/unpair`, `POST /hx/editions/{id}/default`,
`DELETE /hx/editions/{id}` (forgets the edition; deletes only an uploaded copy), `POST /hx/editions/{id}/alignments`,
`POST /hx/alignments/{id}/cancel|resume|discard`, `GET /hx/editions/{id}/alignment`,
`GET /hx/books/{id}/read/chapters/{idx}?edition=`, `GET /hx/books/{id}/read/toc?edition=`, `POST /hx/me/reader-settings`,
`GET|POST /hx/books/{id}/highlights`, `PATCH|DELETE /hx/highlights/{id}`, `POST /hx/editions/{id}/exports/overlays`,
`POST /hx/admin/runtimes/check`.

Media and system: `GET /stream/{audioFileID}`, `GET /covers/{bookID}?w=320`,
`GET /text/{editionID}/chapters/{idx}/timing.json` (ETag = chapter key), `GET /text/{editionID}/res/{resID}`
(raster images only), `GET /text/{editionID}/original` and `GET /exports/{id}` (`download_text`), `GET /healthz`,
`GET /sw.js`, `GET /manifest.webmanifest`, `GET /static/...` (embedded, immutable, hashed names).

JSON API v1 (`/api/v1`, bearer device token or session cookie + CSRF):
`POST /auth/token`, `DELETE /auth/token`, `GET /me`, `GET /libraries`,
`GET /libraries/{id}/books?cursor=&sort=`, `GET /books/{id}` (files, chapters, cover URLs),
`GET /progress?since=`, `PUT /progress/{bookID}`, `POST /progress/batch`, `GET|POST /books/{id}/bookmarks`,
`PATCH|DELETE /bookmarks/{id}`, `GET|POST /books/{id}/notes`, `PATCH|DELETE /notes/{id}`,
`GET /search?q=`, `POST /sessions`, `GET /books/{id}/editions`, `GET /editions/{id}/chapters/{idx}` (text
model JSON), `GET /editions/{id}/chapters/{idx}/timing`, `GET|POST /books/{id}/highlights`,
`PATCH|DELETE /highlights/{id}`. Errors: `{"error":{"code":"...","message":"..."}}` with codes
`unauthorized` 401, `forbidden` 403, `not_found` 404, `invalid` 400, `conflict` 409, `too_large` 413,
`rate_limited` 429, `drm_refused` 422 (audio and ebooks), `unsupported` 415 (ebook format), `not_ready` 409
(no usable alignment yet), `unavailable` 503 (word-timing runtime missing). A forbidden book returns `not_found` to
non-admins (no existence leak).

**Progress update** body: `{"positionMs","durationMs","finished","clientUpdatedAt","deviceId"}`, plus optional
`"text":{"editionId","sentenceId","offset"}` and `"source":"audio"|"text"` (R5).
`clientUpdatedAt` is clamped to server time + 60 s; the update applies only if it is newer than the
stored one. Response: `{"applied":bool,"progress":{...}}` (200 either way, so offline batches never fail).

## Design regions

The design handoff (`docs/design/tendshelf-handoff/docs/HANDOFF.md`, "Server-rendered partials") names
targets and triggers; its suggested routes are replaced by these ([decision 0002](decisions/0002-design-handoff.md)).
"Page + `hx-select`" means the canonical page URL answers with a full page and htmx selects the region,
so Back, reload and links without JavaScript all work.

| Region (target) | Trigger | Route |
| --- | --- | --- |
| Continue listening hero `#home-hero` | load, `progress-saved from:body` | `GET /hx/home/hero` |
| Home rows `#row-{name}` | load, `revealed` below the fold | `GET /hx/home/rows/{name}` (`recent`, `series`, `finished`, `shared`, `because`) |
| Library grid `#library-grid` | change on filters, sort, view | Page + `hx-select`: `GET /libraries/{id}?view=&sort=&filter=` with `hx-push-url`; more rows from `/hx/libraries/{id}/items` |
| Search results `#search-results` | `input changed delay:150ms` | Page + `hx-select`: `GET /search?q=` |
| Book tab panels `#book-panel` | click on a tab | Page + `hx-select`: `GET /books/{id}/{tab}` (`about`, `chapters`, `bookmarks`, `details`) with `hx-push-url` |
| Compare sheet (P2), in `#sheet` | "Look up details online" | `POST /hx/books/{id}/match` then `GET /hx/books/{id}/compare?candidate=` into `#sheet` (see the Sheets row) |
| Scan progress `#scan` | while a scan runs | `GET /hx/jobs/{id}` |
| Download rows | client only | Service worker and IndexedDB (P4); no server polling |
| Users list `#users` | after invite, revoke, role change | `GET /hx/admin/users` |
| Toasts `#toasts` | any response | `hx-swap-oob` (`role="status"`, errors `role="alert"`) |
| Sheets `#sheet` | click on the opener | Page + `hx-select="#sheet > *"`, `hx-swap="innerHTML"`; sheets render in `#sheet` outside `#main`, and the canonical URL renders the sheet inside it on a full load |
| Reader text `#reader-text` (R4) | open, `revealed` near the end | `GET /hx/books/{id}/read/chapters/{idx}?edition=` |
| Reader mode | mode switcher | Page + `hx-select`: `GET /books/{id}/read?mode=` |
| Reading settings (R4) | `change` | `POST /hx/me/reader-settings`, `hx-swap="none"` |
| Highlights and notes (R4) | selection menu | `POST /hx/books/{id}/highlights` (out-of-band sentence span and count) |
| Alignment progress `#align` (R3) | `every 2s` until done | `GET /hx/editions/{id}/alignment` |

Theme and other UI preferences live in `user_settings` (`ui.theme`, `ui.text_size`), so they follow the
user across devices; before login the system preference applies.

## CSS, motion and browser baseline

Stylesheets are hand-written in `internal/web/static/css/` and wrapped at load time in cascade layers
(`@layer tokens, components, utilities, motion, pages`); source files contain no `@layer`, no
`!important` and no inline styles. `tokens.css` and `fonts.css` are the `tokens` layer, `bundle.css`
(the design handoff, verbatim, plus the P0b shell rules) is `components`, `utilities.css` holds layout
primitives, safe-area and native-feel rules, `motion.css` holds every transition, animation and the
reduced-motion overrides, and `pages-*.css` are per-page. Layer order lets `motion` and `utilities`
win without specificity fights. Client code is one file, `static/js/shell.js` (hashed files cannot
import each other): it only toggles classes and sets custom properties with `style.setProperty`.

Browser baseline: current Chromium (WebView2, Android WebView), WebKit (WKWebView, WebKitGTK 2.52). View
Transitions, `@starting-style`, `translate` and `dvh` are enhancements: where one is missing the page
is complete and static (a keyframe on `#main` replaces the view transition). Desktop on Linux uses
WebKitGTK; its automation window cannot be narrower than 447 CSS px, so WebKit captures use 447 and
1440 and the 390 px layout is checked in Chromium. `make visual` (Chromium, needs
`TENDSHELF_TEST_BROWSER`) and `make visual-webkit` (needs `TENDSHELF_TEST_WEBKIT_DRIVER`, best under
`xvfb-run -a`) write captures to `.local/tests/visual/<run>/`. The `dev` build tag adds `/dev/kitchen-sink`
and its `/hx/dev/*` fragments; release builds do not register them.

## Auth and sharing

- Passwords: argon2id (OWASP: 19 MiB, t=2, p=1); the hash string carries its own parameters, so the cost can
  change later. At most four hashes run at once and never inside a write transaction. Minimum 10
  characters, maximum 256.
- First run (P1b-1): the server makes a **setup code** (10 Crockford characters, `XXXXX-XXXXX`, new on every
  start, in memory only) and logs the link `http://<addr>/setup?code=...` while there is no owner. `/setup`
  always asks for the code (no loopback exemption); compared without case, hyphen or spaces, in constant time.
  Exactly one owner: the check and the insert share one write transaction and the `users_one_owner` index
  backs it up; the loser of a race gets "This server already has an owner" (409). After setup `/setup` is a 404.
- Default deny: every path except `/static/*`, `/healthz`, `/setup` and `/login` needs a signed-in, enabled
  account. Anonymous visitors go to `/login?next=...` (303; for an htmx request, 204 with `HX-Redirect`, which
  makes htmx navigate the whole window); with no owner they go to `/setup`.
- Sessions: 32 random bytes in an `HttpOnly`, `SameSite=Lax` cookie `ts_session` (`Secure` when served over TLS
  or behind a trusted proxy that says `X-Forwarded-Proto: https`); only the SHA-256 is stored. 30-day sliding
  expiry, refreshed at most once an hour; signing in ends the browser's previous session and sweeps expired
  ones. Device tokens for the API, revocable on `/me`, come later.
- Limits on failed attempts: 20 per client address per 15 minutes (shared by sign-in and setup; IPv6 counted
  per /64) and 10 per account per 15 minutes, in memory with a bounded table. A limited request answers 429
  with `Retry-After` before any hashing. Every sign-in failure (unknown name, wrong password, disabled
  account) gives the same answer and takes the same time (a dummy hash runs for unknown names).
- `-trusted-proxy` (env `TENDSHELF_TRUSTED_PROXY`): comma-separated IPs or CIDR ranges of reverse proxies. Only
  from those peers are `X-Forwarded-For` (the right-most entry that is not itself trusted) and
  `X-Forwarded-Proto` believed. Default: none.
- `next` after sign-in must be a same-origin path (`auth.SafeNext`); anything else becomes `/`.
- Audit log rows: `auth.login`, `auth.login_failed`, `auth.logout`, `setup.owner_created`,
  `setup.code_failed`. Tokens, passwords and the setup code never reach a log line or audit row; the one
  deliberate exception is the start-up setup link.
- Authorization: `share.Can(user, action, target)` with the actions `library.view`, `book.view`, `book.listen`,
  `book.download`, `book.edit`, `text.read`, `text.download`, `libraries.manage`, `jobs.manage`, `users.manage`,
  `server.manage`, `shelves.use`, `research.use`. Until grants exist (P3): owner everything; admin everything
  except `server.manage`; members only `shelves.use` on their own shelves; guests nothing. A shelf is always
  its owner's. `BookFilter` and `LibraryFilter` give the SQL fragment for listings.
- CSRF: token required in `X-CSRF-Token` or `_csrf` for unsafe methods. It is an HMAC of the signed-in
  session (`s:` + session hash) or, for anonymous visitors, of the `ts_csrf` cookie (`c:` + value), so a token
  for one can never pass as the other. The HMAC key is kept in `settings` (`web.csrf_key`), so open forms
  survive a restart. Cross-site browser requests are refused by the standard library's origin check first.
- Roles: **owner** (everything, server settings, Research toggle, one per server), **admin** (users,
  libraries, metadata, jobs, invites, Research when enabled), **member** (granted libraries/books, own
  progress, bookmarks, notes, shelves, downloads if granted), **guest** (granted books or libraries,
  listen-only by default, own progress and bookmarks, no shelves shared, no downloads unless granted).
- Invites: link `/invite/{token}`, default expiry 7 days, default one use, optional email via SMTP.
  Share links `/s/{token}`: one book, expiry required (max 90 days), optional password, no account.
- Passkeys (WebAuthn) later, added beside passwords.
- Read-along actions (R5): `read_text` (reader page, chapter text, timing, images) needs `can_read` on the
  grant or share link (default off: sharing a book shares listening only); `download_text` (original
  ebook, overlay export) needs `can_read` and `can_download`; upload, pairing and alignment are owner/admin.
  Without `read_text` every text route answers `not_found`. Highlights, notes and reader settings are
  readable only by their own user, admins included.
- Security headers: `Content-Security-Policy: default-src 'self'; img-src 'self' data:; media-src 'self'
  blob:; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action
  'self'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`.

## PWA and offline (P4)

Service worker (`/sw.js`, plain JS) caches the app shell and visited pages. "Download" stores a book's
files in Cache Storage after `navigator.storage.persist()`; offline playback answers Range requests from
the cached response. Progress, bookmarks and notes made offline queue in IndexedDB and go to
`POST /api/v1/progress/batch` and the bookmark/note endpoints on reconnect. iOS storage limits and
eviction must be measured and stated in the UI (guess: large books may be evicted on iOS). With
`read_text` and an aligned edition, Download also stores chapter fragments, timing JSON and images
(keyed by alignment ID); offline highlights queue with bookmarks and notes.

## Read-along (R)

Full design: [read-along.md](read-along.md). In short: `internal/ebook` turns a DRM-free ebook into an
immutable canonical document with stable sentence IDs; `internal/align` decodes the audio with ffmpeg,
asks the owned worker only for wav2vec2 CTC emissions (cached, resumable), then maps, anchors and
force-aligns in Go and writes content-addressed per-chapter timing files; the reader page is templ +
HTMX and `static/js/readalong.js` follows the player's global time with `requestAnimationFrame` and
binary search over typed arrays. Storage per 10-hour book about 1-1.5 MB of text and timings plus a
reconstructible emissions cache of about 58 MB (guesses, measured in R3).

## Desktop (P5)

`desktop/` module, Wails v3.0.0 (pinned), Linux on its default GTK 4 + WebKitGTK 6.0 (the `gtk3` tag is the
legacy fallback; [decision 0003](decisions/0003-newest-versions.md)). Two
modes: connect to a Tendshelf server (the window loads the server's pages; native media keys and tray),
or personal mode running the same core in-process on a local library. See TendVox `docs/wails-v3.md`
for origin guards and the audio gateway pattern.

## Research module (P6)

Port of the TendVox Extractor behind `research.enabled` (owner toggle, default off, admin-only routes).
Clip extraction (random, ranges, by chapter, seeded), measurements (pace, pause types, F0 IQR, LUFS/LRA,
pause noise floor; Go port of `extractor_pitch.py`, ffmpeg `ebur128` and `silencedetect`), categories
from `categories.json` semantics, coverage, and the `tendvox-reference-styles` v1 export. Fair-practice
rules are in `AGENTS.md`.

## Dependencies awaiting approval

Approved 2026-11-01 for P0 ([decision 0002](decisions/0002-design-handoff.md)); each gets its
`NOTICES.md` row in the commit that adds it:

| Module / file | Why | Note |
| --- | --- | --- |
| `github.com/a-h/templ` v0.3.1100 (**approved and added in P0b**) | templ runtime; the CLI runs through the `tool` directive in `go.mod` (`make generate`), so one version source | MIT |
| `modernc.org/sqlite` v1.61.1 (**approved and added in P0a**) | SQLite driver | Chosen over `mattn/go-sqlite3`: pure Go, so `CGO_ENABLED=0` cross-compiles to ARM NAS boxes, Windows, macOS and scratch containers; FTS5 is compiled in (checked in `lib/sqlite_linux_amd64.go`). Cost: slower than the C driver on heavy writes (guess 1.5-2x); fine for this workload. BSD-3 |
| `htmx.min.js` 4.0.0 (**vendored in P0b**) as `internal/web/static/vendor/htmx-4.0.0.min.js` | Interactivity | Official release file, version and SHA-256 in `NOTICES.md`; licence 0BSD, text beside it. htmx 4 inheritance is explicit (`:inherited`) and 4xx/5xx responses swap |
| `golang.org/x/crypto` v0.58.0 (argon2) (**added in P1b-1**) | Password hashing, `internal/users` only | BSD-3 |
| Fonts **vendored in P0b** to `internal/web/static/fonts/` (woff2): Fraunces (variable roman and italic), Fira Sans (400, 400 italic, 500, 600), Fira Mono (400, 500); 1.23 MB in total, 361 KB preloaded | The design's type; Fira Mono only in the Research lab | SIL OFL 1.1, texts beside the files; upstream commits and SHA-256 in `NOTICES.md`; self-hosted, never Google Fonts |

Read-along (phase R), each awaiting approval:

| Module / file / runtime | Why | Note |
| --- | --- | --- |
| `golang.org/x/net/html` | HTML5 parsing of EPUB 2 XHTML, HTML files and MOBI/KF8 content (R1) | BSD-3; Go team |
| `golang.org/x/text` (`unicode/norm`) | NFC normalisation for matching and alignment (R1, R3) | BSD-3; Go team |
| PDF text-layer reader: candidate `github.com/ledongthuc/pdf`, or an own minimal reader | PDF import (R1) | BSD-3 (guess, read it); evaluate ToUnicode/CMap coverage on fixtures before choosing; the module may not be enough for complex PDFs (guess) |
| Font vendored to `internal/web/static/fonts/` (woff2): OpenDyslexic | The one extra reader font (R4); the reader otherwise uses the design's Fraunces and Fira Sans | SIL OFL 1.1 (guess; read it from the release); `NOTICES.md` row |
| Optional runtime, not shipped: Python 3.12 + `torch` CPU (2.8, as TendVox) + `transformers` + `safetensors` + `numpy` | The emissions worker (R3) | Owner-installed; BSD-3 / Apache-2.0 / Apache-2.0 / BSD-3 (guess for exact versions) |
| Optional models, not shipped: `facebook/wav2vec2-base-960h` (English), `jonatasgrosman/wav2vec2-large-xlsr-53-spanish` (Spanish) | Emissions (R3) | Apache-2.0 per model cards (TendVox `docs/alignment-worker.md`); revision and file hashes pinned in `runtimes/align/models/*.json` |
| Test-only, opt-in: epubcheck (Java) | Validate the overlay export (R6) | BSD-3 (guess); never in `make check` |

Considered and not chosen: `github.com/clipperhouse/uax29` (sentence segmentation; own rules handle
abbreviations better and stay versioned), `github.com/yuin/goldmark` (Markdown; only structure is
needed), whisper.cpp or Parakeet as a second ASR runtime (see read-along.md section 2).

Later, each with its own approval: `github.com/go-webauthn/webauthn` (passkeys), `github.com/fsnotify/fsnotify`
(folder watcher), `github.com/wailsapp/wails/v3` (desktop module only), an OpenID Connect client.
