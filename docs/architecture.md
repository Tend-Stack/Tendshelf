# Architecture

One Go process serves pages, fragments, a small JSON API and audio. SQLite (WAL) holds everything that
is not an audio file or a cached image. ffmpeg/ffprobe are child processes. No Node, no SPA.

## Invariants

1. Library audio files are never written, renamed or deleted, except by the write-details job, which
   verifies before swapping and keeps a backup until the user confirms or undoes.
2. Copy-protected files are never decoded, streamed, remuxed or analysed.
3. Every read of a book, file, cover, progress row or search result passes one authorization function
   (`share.Can`), and list/search queries filter by grants in SQL.
4. An older binary never opens a newer database writable; it refuses and leaves the file byte-identical.
5. No outbound network traffic except explicit metadata lookups/cover fetches and configured SMTP.
6. Positions are milliseconds on the book's global timeline (all files concatenated); the newest
   client update wins per user and book.
7. Background work is a persisted job; a restart resumes or re-queues it, never silently repeats a
   file swap.
8. The server binary builds with `CGO_ENABLED=0` and contains no JavaScript build output.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/tendshelf` | Flags/env config, open the database, start jobs and the HTTP server, graceful shutdown. Subcommands later: `tendshelf migrate`, `tendshelf reset-password`, `tendshelf version`. |
| `internal/db` | Open/check/migrate SQLite, `application_id`/`user_version`, pre-migration backups, ID generation, transactions helper. Owns `migrations/*.sql`. |
| `internal/library` | Libraries, folders, books, files, chapters, people, series, labels, shelves: the read/write repository used by everything else. No HTTP, no ffmpeg. |
| `internal/scan` | Walks library folders, groups files into books, detects new/changed/moved/missing files, refuses DRM, queues probe and metadata jobs. Ports ABS `scandir.js`/`BookScanner.js` behaviour. |
| `internal/media` | ffprobe/ffmpeg runner (fixed args, protocol whitelist, deadlines, output caps); probe parsing (streams, tags, chapters, encryption); cover extraction and thumbnails; compatibility copies; stream-copy remux and verification. |
| `internal/metadata` | Local sources (names, tags, sidecars), the field-provenance merge, abridged/dramatized detection. Ports `extractor_details.py`. |
| `internal/metadata/online` | Provider clients (Audnexus, Audible catalogue, Google Books, Open Library, iTunes), scoring, per-site spacing, 10 s timeouts, 7-day cache, safe cover fetch (5 MB, magic-byte check). The only `http.Client` in the program. Ports `extractor_online.py`. |
| `internal/users` | Accounts, roles, password hashing, devices, per-user settings. |
| `internal/auth` | Sessions (cookie), device tokens (bearer), CSRF, login rate limiting, middleware that puts the user in the request context. Passkeys later. |
| `internal/share` | Grants, invites, share links, `share.Can(user, action, target)`, the SQL grant filter, invite email (`mail.go`, `net/smtp`). |
| `internal/progress` | Progress, listening sessions, bookmarks, notes; the newest-wins merge; offline batch apply. |
| `internal/jobs` | Persisted job queue: leases, per-kind concurrency, cancellation, progress events, restart recovery. |
| `internal/web` | templ pages/components, static assets (embedded), HTMX handlers, security headers, the page router. |
| `internal/api` | JSON API v1 for native/mobile clients and the player's sync calls. Same services as `web`. |
| `internal/research` | Optional Research module (P6): clip extraction, measurements, export. Registered only when enabled. |
| `desktop/` (P5) | Separate Go module: Wails v3 shell. Not imported by the server. |

Dependency direction: `web`, `api` → services (`library`, `progress`, `share`, `users`, `metadata`,
`scan`, `jobs`, `research`) → `db`, `media`. Services never import `web` or `api`.

## Data model

All tables `STRICT`. IDs TEXT (ULID-style, prefixed: `usr_`, `lib_`, `bk_`, `af_`, ...). Times INTEGER
Unix ms UTC. Positions INTEGER ms. Booleans INTEGER 0/1. Foreign keys on.

| Table | Columns (key ones) | Notes |
| --- | --- | --- |
| `settings` | `key` PK, `value_json` | Server settings (`research.enabled`, `online.enabled`, `smtp.*`, `listen.addr`). |
| `users` | `id`, `username` UNIQUE NOCASE, `display_name`, `email`, `role` CHECK IN ('owner','admin','member','guest'), `password_hash`, `disabled`, `created_at`, `last_seen_at` | Exactly one owner (partial unique index on `role='owner'`). |
| `sessions` | `token_hash` PK, `user_id`, `created_at`, `expires_at`, `last_seen_at`, `user_agent` | Cookie sessions. |
| `devices` | `id`, `user_id`, `name`, `kind` ('web','pwa','desktop','mobile'), `token_hash` UNIQUE NULL, `created_at`, `last_seen_at`, `revoked_at` | Device tokens for the API. |
| `invites` | `id`, `token_hash` UNIQUE, `created_by`, `email` NULL, `role` ('member','guest'), `grants_json`, `expires_at`, `max_uses`, `use_count`, `revoked_at`, `created_at` | Accepting creates a user and its grants in one transaction. |
| `share_links` | `id`, `token_hash` UNIQUE, `book_id`, `created_by`, `expires_at` NOT NULL, `password_hash` NULL, `revoked_at`, `created_at` | Anonymous listen-only link for one book; progress kept per browser in local storage only. |
| `grants` | `id`, `user_id`, `library_id` NULL, `book_id` NULL, `can_download`, `expires_at` NULL, `created_by`, `created_at` | CHECK exactly one of `library_id`/`book_id`. Owner/admin implicitly see all. |
| `libraries` | `id`, `name`, `created_at`, `scan_interval_min` | |
| `library_folders` | `id`, `library_id`, `path` UNIQUE, `created_at` | |
| `books` | `id`, `library_id`, `root_path` UNIQUE, `title`, `sort_title`, `subtitle`, `description`, `publisher`, `published_year`, `language`, `isbn`, `asin`, `abridged`, `dramatized`, `explicit`, `duration_ms`, `cover_id`, `refused_reason` NULL, `added_at`, `updated_at`, `missing_since` NULL | Resolved values (cache of the merge). |
| `audio_files` | `id`, `book_id`, `path` UNIQUE, `idx`, `size`, `mtime_ns`, `quick_hash`, `sha256` NULL, `container`, `codec`, `bitrate`, `channels`, `sample_rate`, `duration_ms`, `offset_ms`, `encrypted`, `probe_json`, `updated_at` | `offset_ms` places the file on the global timeline. `quick_hash` = SHA-256 of size + first and last 1 MiB. |
| `chapters` | `book_id`, `idx`, `start_ms`, `end_ms`, `title`, `source`; PK (`book_id`,`idx`) | Global timeline. Source: 'file','files','sidecar','online','user'. |
| `metadata_values` | `book_id`, `field`, `source`, `value_json`, `observed_at`; PK (`book_id`,`field`,`source`) | Every candidate value with its source. |
| `people` | `id`, `name`, `sort_name`, `description`, `image_id`, `asin` | Authors and narrators share one table. |
| `book_people` | `book_id`, `person_id`, `role` ('author','narrator'), `idx` | |
| `series` / `book_series` | `id`, `name`, `sort_name`, `description` / `book_id`, `series_id`, `sequence` TEXT, `idx` | Sequence is text ("1.5", "0"). |
| `book_labels` | `book_id`, `kind` ('genre','tag'), `value` | |
| `covers` | `id`, `book_id`, `source`, `mime`, `width`, `height`, `sha256`, `path`, `created_at` | Files under `<data>/covers/`; thumbnails under `<data>/cache/thumbs/`. |
| `online_cache` | `provider`, `query_key`, `response_json`, `fetched_at`; PK (`provider`,`query_key`) | Expires after 7 days. |
| `progress` | `user_id`, `book_id`, `position_ms`, `duration_ms`, `finished_at` NULL, `client_updated_at`, `server_updated_at`, `device_id`; PK (`user_id`,`book_id`) | |
| `listening_sessions` | `id`, `user_id`, `book_id`, `device_id`, `started_at`, `ended_at`, `start_ms`, `end_ms`, `listened_ms`, `speed` | Statistics (P4). |
| `bookmarks` | `id`, `user_id`, `book_id`, `position_ms`, `title`, `created_at`, `updated_at`, `deleted_at` | Soft delete so offline clients sync deletions. |
| `notes` | `id`, `user_id`, `book_id`, `position_ms` NULL, `body`, `created_at`, `updated_at`, `deleted_at` | |
| `shelves` / `shelf_items` | `id`, `owner_id`, `name`, `shared` (P3), `created_at` / `shelf_id`, `book_id`, `idx`, `added_at` | |
| `jobs` | `id`, `kind`, `state` ('queued','running','done','failed','cancelled','held'), `payload_json`, `result_json`, `error_code`, `error_text`, `attempts`, `max_attempts`, `lease_until`, `not_before`, `progress_json`, `created_by`, `created_at`, `updated_at` | |
| `file_writes` | `id`, `job_id`, `audio_file_id`, `state` ('copying','verifying','swapped','confirmed','undone','failed'), `original_path`, `backup_path`, `temp_path`, `before_sha256`, `after_sha256`, `audio_hash`, `created_at`, `updated_at` | The write-details ledger; drives restart recovery and Undo. |
| `audit_log` | `id`, `at`, `user_id`, `action`, `target`, `detail_json` | Logins, invites, grants, file writes, research toggles. |
| `books_fts` | FTS5 over title, subtitle, authors, narrators, series, description | Notes are per user: searched with a user filter, not in this index. |
| `research_*` (P6) | `research_sources`, `research_clips`, `research_measurements` | Created by the P6 migration. |

## Media handling

- **Supported**: m4b, m4a, mp4, mp3, flac, ogg, opus, wav, aac, webm. **Refused**: aax, aaxc, aa, m4p,
  and any stream ffprobe reports as encrypted.
- **Streaming**: `GET /stream/{audioFileID}` uses `http.ServeContent` on the opened file (HTTP Range,
  `If-Range`, `ETag` = quick hash, `Cache-Control: private`). Authorization is checked before opening.
  The player maps the global timeline to (file, offset) using `offset_ms`.
- **Transcoding policy**: none by default (direct play). The player reports `audio.canPlayType()` for the
  book's codecs; if the browser cannot play a file, the server creates a **compatibility copy** (AAC-LC
  128 kbit/s in `.m4a`, faststart, stream-copied when the codec is already AAC) as a job, stored in
  `<data>/cache/compat/` keyed by `quick_hash` + recipe version, total size capped (default 5 GiB,
  least recently used removed). `GET /stream/{audioFileID}?compat=1` serves it once ready. No live
  HLS; reconsider only with a measured need.
- **Covers**: embedded picture or folder image (`cover.*`, `folder.*`) at scan time; thumbnails
  (160, 320, 640 px JPEG) produced by ffmpeg and cached.
- **Write details into the file** (P2): `ffmpeg -map 0 -c copy` with the new tags and chapters into a
  temp file in the same directory; verify the audio stream hash (`-map 0:a -c copy -f hash`) equals the
  original's, duration within 50 ms, chapter count and times equal, tags read back; check free space
  (file size + 5 %) and refuse files modified in the last 10 minutes; rename original to
  `<name>.before-tags.<ext>`, rename temp into place, record `file_writes` state `swapped`. "Looks good"
  deletes the backup (`confirmed`); "Undo" restores it (`undone`). Restart recovery reads `file_writes`
  and completes or rolls back each half-done row; it never re-runs ffmpeg on its own.

## Background jobs

Kinds: `scan.library`, `media.probe`, `media.cover`, `media.compat`, `metadata.local`,
`metadata.online` (only user-triggered), `media.write_details`, `research.extract`, `research.measure`.
A job runner per kind with bounded concurrency (probe 2, compat 1, write_details 1). Leases of 60 s
renewed while running; on startup, expired `running` jobs return to `queued` except `write_details`,
which goes to `held` until `file_writes` recovery decides. Cancellation via context. Progress is
written to `progress_json` at most once per second and read by `/hx/jobs/{id}` polling.

## HTTP routes

Pages (GET; full page; navigation via `hx-boost` selecting `#main`):

| Route | Page |
| --- | --- |
| `/` | Home: continue listening, recently added, shelves |
| `/setup` | First run: create the owner (only while no owner exists) |
| `/login`, `POST /logout` | |
| `/libraries/{id}` | Grid/list with sort and filter |
| `/books/{id}` | Book: cover, details, chapters, play, bookmarks, notes |
| `/books/{id}/edit`, `/books/{id}/match` | Edit details; online lookup and field compare (P2) |
| `/authors/{id}`, `/narrators/{id}`, `/series/{id}` | |
| `/search?q=` | |
| `/shelves`, `/shelves/{id}` | |
| `/me` | Account, devices, playback preferences, downloads (P4), statistics (P4) |
| `/invite/{token}`, `/s/{token}` | Accept an invite; anonymous share link |
| `/admin/users`, `/admin/libraries`, `/admin/jobs`, `/admin/settings`, `/admin/research` | Admin; research only when enabled |

HTMX fragments (`/hx/...`): `GET /hx/libraries/{id}/items?cursor=&sort=&filter=` (infinite scroll),
`GET /hx/search/suggest?q=`, `GET /hx/books/{id}/bookmarks`, `POST /hx/books/{id}/bookmarks`,
`DELETE /hx/bookmarks/{id}`, `POST /hx/books/{id}/finished`, `GET /hx/jobs/{id}`,
`POST /hx/books/{id}/match` (starts lookup job), `GET /hx/books/{id}/compare?candidate=`,
`POST /hx/books/{id}/apply`, `POST /hx/shelves/{id}/items`.

Media and system: `GET /stream/{audioFileID}`, `GET /covers/{bookID}?w=320`, `GET /healthz`,
`GET /sw.js`, `GET /manifest.webmanifest`, `GET /static/...` (embedded, immutable, hashed names).

JSON API v1 (`/api/v1`, bearer device token or session cookie + CSRF):
`POST /auth/token`, `DELETE /auth/token`, `GET /me`, `GET /libraries`,
`GET /libraries/{id}/books?cursor=&sort=`, `GET /books/{id}` (files, chapters, cover URLs),
`GET /progress?since=`, `PUT /progress/{bookID}`, `POST /progress/batch`, `GET|POST /books/{id}/bookmarks`,
`PATCH|DELETE /bookmarks/{id}`, `GET|POST /books/{id}/notes`, `PATCH|DELETE /notes/{id}`,
`GET /search?q=`, `POST /sessions`. Errors: `{"error":{"code":"...","message":"..."}}` with codes
`unauthorized` 401, `forbidden` 403, `not_found` 404, `invalid` 400, `conflict` 409, `too_large` 413,
`rate_limited` 429, `drm_refused` 422, `unavailable` 503. A forbidden book returns `not_found` to
non-admins (no existence leak).

**Progress update** body: `{"positionMs","durationMs","finished","clientUpdatedAt","deviceId"}`.
`clientUpdatedAt` is clamped to server time + 60 s; the update applies only if it is newer than the
stored one. Response: `{"applied":bool,"progress":{...}}` (200 either way, so offline batches never fail).

## Auth and sharing

- Passwords: argon2id (OWASP: 19 MiB, t=2, p=1). Login rate limit per IP and per username.
- Sessions: 32 random bytes in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` when served over TLS or
  behind a configured trusted proxy); 30-day sliding expiry. Device tokens for the API, revocable on `/me`.
- CSRF: per-session token required in `X-CSRF-Token` or `_csrf` for unsafe methods.
- Roles: **owner** (everything, server settings, Research toggle, one per server), **admin** (users,
  libraries, metadata, jobs, invites, Research when enabled), **member** (granted libraries/books, own
  progress, bookmarks, notes, shelves, downloads if granted), **guest** (granted books or libraries,
  listen-only by default, own progress and bookmarks, no shelves shared, no downloads unless granted).
- Invites: link `/invite/{token}`, default expiry 7 days, default one use, optional email via SMTP.
  Share links `/s/{token}`: one book, expiry required (max 90 days), optional password, no account.
- Passkeys (WebAuthn) later, added beside passwords.
- Security headers: `Content-Security-Policy: default-src 'self'; img-src 'self' data:; media-src 'self'
  blob:; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action
  'self'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`.

## PWA and offline (P4)

Service worker (`/sw.js`, plain JS) caches the app shell and visited pages. "Download" stores a book's
files in Cache Storage after `navigator.storage.persist()`; offline playback answers Range requests from
the cached response. Progress, bookmarks and notes made offline queue in IndexedDB and go to
`POST /api/v1/progress/batch` and the bookmark/note endpoints on reconnect. iOS storage limits and
eviction must be measured and stated in the UI (guess: large books may be evicted on iOS).

## Desktop (P5)

`desktop/` module, Wails v3 (pinned), Linux `gtk3` build tag as in TendVox until GTK4 is verified. Two
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

Standard library only today. The first package (P0) needs:

| Module / file | Why | Note |
| --- | --- | --- |
| `github.com/a-h/templ` (v0.3.1070, already in the local module cache) | templ runtime; the CLI at the same version for `make generate` | MIT |
| `modernc.org/sqlite` (v1.60.1, already in the local module cache; TendVox uses it) | SQLite driver | Chosen over `mattn/go-sqlite3`: pure Go, so `CGO_ENABLED=0` cross-compiles to ARM NAS boxes, Windows, macOS and scratch containers; FTS5 is compiled in (checked in `lib/sqlite_linux_amd64.go`). Cost: slower than the C driver on heavy writes (guess 1.5-2x); fine for this workload. BSD-3 |
| `htmx.min.js` 2.x, vendored to `internal/web/static/vendor/` | Interactivity | Download once from the official release, record version and SHA-256 in `NOTICES.md`; licence 0BSD (guess, read it from the release) |
| `golang.org/x/crypto` (argon2) | Password hashing in P1 | BSD-3 |

Later, each with its own approval: `github.com/go-webauthn/webauthn` (passkeys), `github.com/fsnotify/fsnotify`
(folder watcher), `github.com/wailsapp/wails/v3` (desktop module only), an OpenID Connect client.
