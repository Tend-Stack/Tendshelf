# Architecture

One Go process serves pages, fragments, a small JSON API, audio and read-along text. SQLite (WAL) holds everything that
is not an audio file or a cached image. ffmpeg/ffprobe are child processes, and so is the optional alignment worker for read-along
([read-along.md](read-along.md), [decision 0001](decisions/0001-read-along.md)). No Node, no SPA.

## Invariants

1. Library audio files are never written, renamed or deleted, except by the write-details job, which
   verifies before swapping and keeps a backup until the user confirms or undoes, and by adding books by upload
   (`internal/upload`, decision [0015](decisions/0015-upload-into-library.md)), which only creates new paths in
   the library's chosen upload folder, records each in `upload_paths` first, publishes a book by one rename of a
   hidden folder, and deletes only unchanged paths it recorded, on "Remove this book".
2. Copy-protected files are never decoded, streamed, remuxed or analysed. The same holds for ebooks:
   library ebooks are read-only, uploads are kept byte-identical, protected ebooks are refused.
3. Every read of a book, file, cover, progress row or search result passes one authorization function
   (`share.Can`), and list/search queries filter by grants in SQL.
4. An older binary never opens a newer database writable; it refuses and leaves the file byte-identical.
5. No outbound network traffic except explicit metadata lookups/cover fetches and configured SMTP. Email is optional: invitations open the person's own email app by default, and the admin-configured SMTP relay (any provider; `internal/share/mail.go` is the only dialler) is used only for "Send it for me" and the test email.
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
| `internal/mediajobs` | Jobs `media.cover` (cover choice, stored picture, thumbnails, tint and accent colours), `media.compat` (compatibility-copy cache with LRU cap) and `media.peaks` (waveform peaks cache). Imports library, media and jobs; never scan or metadata. Registered by `cmd/tendshelf/hooks.go`; the cover job follows the scanner's settled books. |
| `internal/backups` | Job `db.backup` and the `backup.schedule` setting (`off`, `daily`, `weekly`; missing is daily). `db.WriteCopy` makes a verified `VACUUM INTO` copy `backups/tendshelf-copy-v<n>-<UTC>.db` over its own `mode=ro` connection (it never holds the writer; a writer transaction may stay open), mode 0600, and keeps the newest 7 copies. Pre-migration backups (`tendshelf-v<n>-...`, newest 3) rotate separately and neither rotation touches the other's files. `Run` checks every 15 minutes (first after 2) whether a copy is due; one backup runs at a time. Owner only. |
| `internal/metadata` | Local sources (names, tags, sidecars), the field-provenance merge, abridged/dramatized detection. Ports `extractor_details.py`. |
| `internal/metadata/online` | Provider clients (Audnexus, Audible catalogue, Google Books, Open Library, iTunes), scoring, per-site spacing, 10 s timeouts, 7-day cache, safe cover fetch (5 MB, magic-byte check). The only `http.Client` in the program. Ports `extractor_online.py`. |
| `internal/upload` | Adding books by upload (decision 0015): upload sessions and the chunk store under `<data>/uploads/incoming/`, limits and free-space checks, the `upload.place` job (DRM probe, names, safe layout, hidden-folder build, publish rename, scoped `scan.library`), the `upload_paths` ledger, restart recovery, the hourly expiry sweep and "Remove this book". The only package besides `mediajobs` write-details that writes into a library folder. Imports `library`, `media`, `metadata`, `jobs`, `scan` (payload and kind only), `db`. |
| `internal/users` | Accounts, roles, argon2id password hashing (at most 4 at once, never inside a write transaction), account validation, owner creation. Devices and per-user settings later. |
| `internal/auth` | Sessions (cookie, hashed tokens), sign-in/sign-out/setup service, failed-attempt limits, trusted-proxy client address, setup code, safe `next` redirects, middleware that puts the user in the request context. Device tokens (bearer) and passkeys later. CSRF lives in `internal/web`. |
| `internal/share` | `share.Can(user, action, target)`, the action names and the SQL listing filters (owner and admin only until P3); later grants, invites, share links, invite email (`mail.go`, `net/smtp`). |
| `internal/progress` | Progress (newest wins) and bookmarks; no access decisions. |
| `internal/jobs` | Persisted job queue: leases, per-kind concurrency, cancellation, progress events, restart recovery. |
| `internal/web` | templ pages/components, static assets (embedded), HTMX handlers, security headers, the page router. |
| `internal/api` | JSON helpers of API v1 (envelope, errors, lists, ETag); the routes live in `internal/web` (`routes_api*.go`) and reuse the pages' library and share functions. |
| `internal/ebook` | Ebook ingestion (R1): format detection, copy-protection check, one parser per format (EPUB 2/3 with SMIL, PDF text layer, TXT/Markdown, DOCX, HTML, FB2, DRM-free MOBI/AZW3), the canonical document `tendshelf-doc-v1` with stable IDs and source offsets, sentence segmenter, pairing scores. No HTTP, no model. |
| `internal/align` | Read-along alignment (R3): normaliser, emissions cache, greedy CTC transcript, block mapping and anchors, CTC forced alignment, confidence, timing files, the `align.book` job; Media Overlays import/export (R6). Pure Go. |
| `internal/align/runtime` | Checks runtime components (hashes, `--describe`, licence), launches and owns the loopback emissions worker (`tendshelf-emissions-v1`), idle stop, parent-death cleanup. The only code that starts it. |
| `desktop/` (P5) | Separate Go module: Wails v3 shell. Not imported by the server. |

Dependency direction: `web`, `api` → services (`library`, `progress`, `share`, `users`, `metadata`,
`scan`, `jobs`, `ebook`, `align`) → `db`, `media`. `align` uses `ebook` (documents) and
`media` (ffmpeg decode); `ebook` imports neither. Services never import `web` or `api`.

## Data model

All tables `STRICT`. IDs TEXT (ULID-style, prefixed: `usr_`, `lib_`, `bk_`, `af_`, ...). Times INTEGER
Unix ms UTC. Positions INTEGER ms. Booleans INTEGER 0/1. Foreign keys on.

| Table | Columns (key ones) | Notes |
| --- | --- | --- |
| `settings` | `key` PK, `value_json` | Server settings (`online.enabled`, `smtp.host`, `smtp.port`, `smtp.tls`, `smtp.username`, `smtp.password`, `smtp.from_name`, `smtp.from_address` (optional relay; `invites.mail_job_id` links an invite to its send job), `listen.addr`, `server.name` (JSON string), `setup.done` (JSON true, set when the setup folder is added)). |
| `users` | `id`, `username` UNIQUE NOCASE, `display_name`, `email`, `role` CHECK IN ('owner','admin','member','guest'), `password_hash`, `disabled`, `created_at`, `last_seen_at` | Exactly one owner (partial unique index on `role='owner'`). |
| `sessions` | `token_hash` PK, `user_id`, `created_at`, `expires_at`, `last_seen_at`, `user_agent` | Cookie sessions. |
| `devices` | `id`, `user_id`, `name`, `kind` ('web','pwa','desktop','mobile'), `token_hash` UNIQUE NULL (32 bytes), `created_at`, `last_seen_at`, `revoked_at` | Device tokens for the API (migration 0007). A revoked device keeps no token. |
| `invites` | `id`, `token_hash` UNIQUE (32 bytes), `created_by`, `name`, `email` NULL, `note`, `role` ('admin','member','guest'; only the owner may offer admin), `grants_json` (array), `expires_at` NULL ("Until I stop it"), `max_uses` 1-50, `use_count` <= `max_uses`, `revoked_at`, `created_at`, `updated_at` | Accepting creates a user and its grants in one transaction (migration 0007). |
| `share_links` | `id`, `token_hash` UNIQUE (32 bytes), `book_id`, `created_by`, `label`, `expires_at` NOT NULL (at most 90 days after `created_at`), `password_hash` NULL, `can_read` (R5, default 0), `revoked_at`, `created_at` | Anonymous listen-only link for one book; progress kept per browser in local storage only (migration 0007). |
| `grants` | `id`, `user_id`, `scope` ('all','library','shelf','book'), `library_id` / `shelf_id` / `book_id` (only the one the scope names), `can_download`, `can_read` (R5, default 0), `expires_at` NULL, `created_by`, `created_at`, `updated_at` | One grant per (user, scope, target). Active while `expires_at` is NULL or later than now and the person is not disabled. A shelf grant covers a book only while it is on the shelf and the shelf's owner is an enabled owner or admin. Deleted with the person, library, shelf or book. Owner/admin implicitly see all (migration 0007). |
| `libraries` | `id`, `name`, `created_at`, `scan_interval_min`, `auto_lookup_at` (0012; NULL = the per-library switch for new books is off), `upload_folder_id` (0014; NULL = uploads off; `library_folders(id)` ON DELETE SET NULL; must be a folder of the same library) | |
| `library_folders` | `id`, `library_id`, `path` UNIQUE, `created_at` | |
| `books` | `id`, `library_id`, `folder_id` (cascade on folder removal; the scan works per folder), `root_path` UNIQUE, `title`, `sort_title`, `subtitle`, `description`, `publisher`, `published_year`, `language`, `isbn`, `asin`, `abridged`, `dramatized`, `explicit`, `duration_ms`, `cover_id`, `refused_reason` NULL, `added_at`, `updated_at`, `missing_since` NULL | Resolved values (cache of the merge). |
| `audio_files` | `id`, `book_id`, `path` UNIQUE, `idx`, `size`, `mtime_ns`, `quick_hash`, `sha256` NULL, `container`, `codec`, `bitrate`, `channels`, `sample_rate`, `duration_ms`, `offset_ms`, `encrypted`, `probe_json`, `probe_error` NULL, `missing_since` NULL, `updated_at` | `probe_error` (migration 0003) holds the media error code of a file that could not be read; a file is settled when `probe_json` or `probe_error` is set, so a damaged file never blocks its book. `offset_ms` places the file on the global timeline. `missing_since` flags a lost part of a multi-file book instead of dropping it. `quick_hash` = SHA-256 of size + first and last 1 MiB. |
| `chapters` | `book_id`, `idx`, `start_ms`, `end_ms`, `title`, `source`; PK (`book_id`,`idx`) | Global timeline. Source: 'file','files','sidecar','online','user'. |
| `metadata_values` | `book_id`, `field`, `source`, `value_json`, `observed_at`; PK (`book_id`,`field`,`source`) | Every candidate value with its source. `internal/metadata` (job `metadata.local`, queued when a book settles) writes the `folder` and `tags` rows and resolves each field by the fixed order user > metadata.json (source `json`) > opf > txt > nfo > tags > folder > online; `user` rows are never written by a job and win even when empty. |
| `people` | `id`, `name`, `sort_name`, `description`, `image_id`, `asin` | Authors and narrators share one table. |
| `book_people` | `book_id`, `person_id`, `role` ('author','narrator'), `idx` | |
| `series` / `book_series` | `id`, `name`, `sort_name`, `description` / `book_id`, `series_id`, `sequence` TEXT, `idx` | Sequence is text ("1.5", "0"). |
| `book_labels` | `book_id`, `kind` ('genre','tag'), `value` | |
| `covers` | `id`, `book_id`, `source`, `mime`, `width`, `height`, `sha256`, `path`, `tint` NULL, `accent_json` NULL, `created_at` | Files under `<data>/covers/`; thumbnails under `<data>/cache/thumbs/`. `tint` is the sampled hex colour, `accent_json` the checked accent per theme (`dark`, `light`, `sepia`); NULL means the ember fallback. |
| `online_cache` | `provider` (audnexus, audible, google, openlibrary, itunes), `query_key`, `response_json`, `fetched_at`; PK (`provider`,`query_key`) | Migration 0010. Expires after 7 days. |
| `book_sidecars` | `book_id` PK (cascade on `books`), `signature`, `checked_at` | Migration 0010. The sidecar signature that tells a scan a book's sidecar files changed. |
| `book_lookups` (0012) | `book_id` PK (cascade on `books`), `outcome` ('matched','needs_look','no_match','failed','kept','no_query','chosen','dismissed'), `provider`, `job_id`, `checked_at` | Skip list and resume cursor of the bulk lookup job; WITHOUT ROWID. |
| `online_usage` (0012) | `day` (UTC day number), `provider` ('audnexus','google','openlibrary','itunes'), `requests`; PK (`day`,`provider`) | Network requests per UTC day, so a restart cannot reset the daily cap. |
| `queue_items` (0013) | `user_id` (cascade on `users`), `book_id` (cascade on `books`), `position` (signed INTEGER), `added_at` ms; PK (`user_id`,`book_id`) | Up next: a person's ordered list, at most 100 books (`library.QueueMax`), each book once. |
| `progress` | `user_id`, `book_id`, `position_ms`, `duration_ms`, `finished_at` NULL, `client_updated_at`, `server_updated_at`, `device_id`, `device_label`; PK (`user_id`,`book_id`); text columns (`position_source`, `text_edition_id`, `text_sid`, `text_off`) arrive with R5 | One row per person and book. A write applies only if its `client_updated_at` is strictly newer, compared and written in one statement; the client time is clamped to server now + 60 s, and explicit server acts (mark finished, listen again, "continue here") win. `device_id` and `device_label` only name the device for the "continue there?" prompt; they never decide access. A text update (R5) sets `position_ms` to the sentence start (or, in a gap, the previous timed sentence's end). |
| `listening_sessions` | `id`, `user_id`, `book_id`, `device_id`, `started_at`, `ended_at`, `start_ms`, `end_ms`, `listened_ms`, `speed_pct` | Written with each progress save since P1c (same transaction); P4 reads it. A save extends the device's latest session unless the pause exceeds 60 s, the speed changed or the position jumped over 35 s beyond the audio played; `listened_ms` is wall-clock playing time reported by the device and capped at 10 min per save. |
| `bookmarks` | `id`, `user_id`, `book_id`, `position_ms`, `note`, `created_at`, `updated_at`, `deleted_at`, `client_id` NULL (0008, unique per person) | Soft delete (the note is cleared) so offline clients sync deletions. `edition_id` and `sid` arrive with R4; `sid` is set when made in the text. |
| `notes` | `id`, `user_id`, `book_id`, `highlight_id`, `body`, `created_at`, `updated_at`, `deleted_at` (R4) | A note on a text passage hangs on its highlight; a note on a moment is the bookmark's `note`. |
| `shelves` / `shelf_items` | `id`, `owner_id`, `name` (1-80 characters, unique per owner ignoring case), `shared` (P3-6a: 1 = visible to members and staff, effective only while the owner is an enabled owner or admin), `created_at`, `updated_at` / `shelf_id`, `book_id`, `idx`, `added_at`; PK (`shelf_id`,`book_id`) | Schema v5 (P1b-8). Cascade on the owner and on the book. At most 200 shelves per person and 5,000 books per shelf. Every read filters on `owner_id` and on the person's book scope; rows of a hidden or missing book stay and reappear with it. |
| `jobs` | `id`, `kind`, `state` ('queued','running','done','failed','cancelled','held'), `payload_json`, `result_json`, `error_code`, `error_text`, `attempts`, `max_attempts`, `lease_until`, `not_before`, `progress_json`, `created_by`, `created_at`, `updated_at` | |
| `file_writes` | `id`, `job_id`, `book_id`, `audio_file_id`, `state` ('copying','verifying','swapping','swapped','confirming','confirmed','undoing','undone','failed'), `original_path`, `backup_path`, `temp_path`, `before_sha256`, `after_sha256` NULL, `audio_hash`, `error_code` NULL, `created_by`, `created_at`, `updated_at` | Migration 0011. No foreign keys, so a row outlives a pruned job and a deleted book. The write-details ledger; drives restart recovery and Undo. |
| `uploads` (0014) | `id` (`upl_`), `library_id` (cascade), `folder_id` (cascade), `created_by`, `label`, `state` CHECK IN ('receiving','queued','placing','placed','refused','failed','cancelled','expired'), `bytes_total`, `bytes_received`, `job_id` NULL, `scan_job_id` NULL, `book_path` NULL, `temp_path` NULL, `refused_file` NULL, `error_code` NULL, `skipped` (count of left-out files), `created_at`, `updated_at` | One upload is one book. Rows of ended uploads are pruned after 30 days; `upload_paths` stays. |
| `upload_files` (0014) | `id` (`upf_`), `upload_id` (cascade), `idx`, `rel_path` (slash-separated, checked), `size`, `modified_ms`, `sha256` NULL (optional promised hash), `bytes_received`; UNIQUE (`upload_id`,`rel_path`) | The manifest and resume offsets. |
| `upload_paths` (0014) | `id` (`upp_`), `upload_id` (no foreign key), `library_folder_id`, `path`, `kind` ('dir','file'), `state` ('planned','staged','present','removed','kept'), `size` NULL, `mtime_ns` NULL, `quick_hash` NULL, `sha256` NULL, `created_at`, `updated_at`; partial UNIQUE (`path`) WHERE `state` IN ('planned','staged','present') | The ledger of every path an upload created; written before the path is created. The only list "Remove this book" deletes from. |
| `audit_log` | `id`, `at`, `user_id`, `action`, `target`, `detail_json` | Logins, invites, grants, file writes. |
| `books_fts` | FTS5, contentless with delete, over title, subtitle, authors, narrators, series, description; rowid from `books_fts_ids`; kept by triggers on books, book_people, book_series, people and series names (migration 0004); `books_fts_vocab` feeds did-you-mean. | Notes are per user: searched with a user filter, not in this index. |
| `user_settings` | `user_id`, `key`, `value_json`, `updated_at`; PK (`user_id`,`key`) | The one per-user settings store: typed in `users.Settings` (`ui.theme`, `ui.player_tint`, `ui.big_buttons`, `playback.speed`, `playback.skip_back_s`, `playback.skip_forward_s`, `playback.auto_rewind`, `playback.next_in_series`; the player reads them with `LoadSettings`), `reader.v1`, chosen edition per book `edition.<bookID>`. |
| `ebook_files` (R1) | `id` (`ebf_`), `sha256` UNIQUE, `size`, `format` CHECK IN ('epub','pdf','txt','md','docx','html','fb2','mobi','azw3'), `origin` ('library','upload'), `path` UNIQUE, `library_folder_id` NULL, `mtime_ns`, `quick_hash`, `refused_reason` NULL ('drm','unsupported','damaged','too_large','no_text'), `added_at`, `missing_since` NULL | Library ebooks read-only; uploads under `<data>/ebooks/`. |
| `editions` (R1) | `id` (`ed_`), `ebook_file_id`, `book_id` NULL (NULL = unpaired), `doc_sha256`, `parse_recipe`, `title`, `authors_json`, `language`, `isbn`, `asin`, `layout` ('reflow','page'), `chapter_count`, `word_count`, `has_overlays`, `is_default`, `pair_source` ('folder','upload','identifier','title','user'), `pair_score` REAL, `paired_by` NULL, `paired_at`, `created_at`, `updated_at` | Partial unique index: one `is_default` per book. Document file `<data>/text/<doc_sha256>.json.gz`. |
| `edition_chapters` (R1) | `edition_id`, `idx`, `title`, `src`, `front`, `word_count`, `sentence_count`, `text_sha256`; PK (`edition_id`,`idx`) | `text_sha256` over the normalised text drives re-alignment of changed chapters only. |
| `runtime_components` (R3) | `id` (`rt_`), `kind` ('emissions-model'), `name`, `language`, `revision`, `model_sha256`, `files_json` (path, size, sha256), `python_path`, `worker_sha256`, `licence_spdx`, `licence_url`, `source_url`, `checked_at`, `disabled` | Only checked rows are usable; the licence is shown on `/admin/runtimes` and `/about`. |
| `alignments` (R3) | `id` (`al_`), `edition_id`, `book_id`, `source` ('model','overlay'), `recipe`, `runtime_id` NULL, `model_sha256` NULL, `audio_key`, `doc_sha256`, `state` CHECK IN ('queued','emitting','aligning','done','partial','failed','cancelled','held'), `labels_json` (`abridged`, `different_edition`), `job_id`, `coverage` REAL, `sentences_total`, `sentences_timed`, `error_code` NULL, `created_at`, `finished_at` NULL, `superseded_by` NULL | `audio_key` = SHA-256 of the ordered audio `quick_hash` list. The newest `done`/`partial` row per edition is used. |
| `alignment_chapters` (R3) | `alignment_id`, `chapter_idx`, `state` ('pending','aligned','unmatched','failed'), `chapter_key`, `start_ms` NULL, `end_ms` NULL, `sentences_total`, `sentences_timed`, `mean_confidence` REAL; PK (`alignment_id`,`chapter_idx`) | Timing file `<data>/align/<chapter_key>.json.gz`, shared by every alignment with the same key. |
| `highlights` (R4) | `id` (`hl_`), `user_id`, `book_id`, `edition_id`, `start_sid`, `start_off`, `end_sid`, `end_off`, `quote`, `quote_prefix`, `quote_suffix`, `start_ms` NULL, `end_ms` NULL, `alignment_id` NULL, `colour` ('yellow','green','blue','pink'), `created_at`, `updated_at`, `deleted_at` | Text range and time range. Private to `user_id`. Soft delete for offline sync. |

## Media handling

- **ffmpeg versions**: developed against ffmpeg 8.0 (the version on the lead's machine, `make test-media`) and
  tested nightly against ffmpeg 6.1 (Ubuntu 24.04's package, the nightly CI job). Where the two report the same
  condition differently (a non-audio file is `AVERROR_INVALIDDATA` on 8, `EINVAL` on 6.1), `internal/media`
  maps both.
- **Supported**: m4b, m4a, mp4, mp3, flac, ogg, opus, wav, aac, webm. **Refused**: aax, aaxc, aa, m4p,
  and any stream ffprobe reports as encrypted. ffprobe has no `-nostdin`; it runs with stdin from
  /dev/null (ffmpeg gets `-nostdin`).
- **Streaming**: `GET /stream/{audioFileID}` uses `http.ServeContent` on the opened file (HTTP Range,
  `If-Range`, `Cache-Control: private, no-cache`). Authorization (`ListenBook` on the file's book) is
  checked before opening; the path comes only from the row and must lie under the book's root;
  refused, encrypted, unreadable, unprobed and missing files answer 404 like an unknown file. The
  `ETag` is the quick hash while the file's size and mtime match the row (another tag otherwise).
  The player maps the global timeline to (file, offset) using `offset_ms`.
- **Transcoding policy**: none by default (direct play). The player reports `audio.canPlayType()` for the
  book's codecs; if the browser cannot play a file, the server creates a **compatibility copy** (AAC-LC
  128 kbit/s in `.m4a`, faststart, stream-copied when the codec is already AAC) as a job, stored in
  `<data>/cache/compat/` keyed by `quick_hash` + recipe version, total size capped (default 5 GiB,
  least recently used removed). `GET /stream/{audioFileID}?compat=1`: a ready copy is served (ETag `<quick_hash>-c<recipe>`);
  otherwise the job is requested and the answer is 503 with `Retry-After: 3` (the player polls with
  HEAD). No live
  HLS; reconsider only with a measured need. Built by `internal/mediajobs` (job `media.compat`): the
  cache is the file system alone (`<quick_hash>-r<recipe>.m4a`, last use = file mtime, oldest evicted
  above the cap, other recipes removed; `-compat-cache-gib`, default 5), the result is probed (mp4,
  AAC, length within 1 %) before it is renamed into place, and no database row exists for it.
  `media.MayNeedCompat` names the formats browsers always play; the player's `canPlayType` decides
  when to ask (P1c). No free-space check before converting: the cap bounds the cache.
- **Waveform peaks**: the loudness of each audio file, decoded once by a background job and drawn
  behind the chapter scrubber. `media.Peaks` runs ffmpeg (mono, 8 kHz, signed 16 bit PCM to a pipe, one
  decoder thread, reniced to 10; the library file is only read) and a streaming reducer turns it into
  one byte per 500 ms: the RMS level in dBFS mapped linearly from -60..0 dB to 0..255 (RMS on a dB scale
  shows the pauses of speech; peak values give a flat wall on mastered audio). A 17 hour book is about 120 KiB.
  File format: 16 byte header (`TSPK`, format 1, bin length in ms, value count, floor in dB) and the values.
  Stored as `<data>/cache/peaks/<quick_hash>-v1.peaks` and nowhere else: no table, no migration, no backup
  growth, and losing the directory loses nothing. A changed file has a new quick hash, so stale peaks are
  never served; `Peaks.Sweep` removes other format versions and hashes no `audio_files` row carries (rows of
  missing files keep theirs). Jobs `media.peaks` (concurrency 1) are queued by a scan follower for settled
  books, by `Backfill` once after start (files scanned before), and by the first request for a file with
  none; a request queues with no delay and `jobs.Runner.Expedite` moves an already queued job to now, so the
  book being played goes first. A refused, encrypted, copy-protected, missing or unprobed file is never decoded.
  The flag `-waveforms` (env `TENDSHELF_WAVEFORMS`, default on) turns the feature off for weak hardware:
  nothing is queued and the route answers 404; so does a missing ffmpeg. `GET /peaks/{audioFileID}` is
  authorised exactly like `/stream/` (`share.ListenBook`, same refusals, same 404), answers the binary with
  `ETag "pk1-<quick_hash>"` and `Cache-Control: private, no-cache` (304 on a match), and answers 404 with
  `Retry-After: 30` while the job is queued or running. Measured with ffmpeg 8.0.1: 30 minutes of AAC take about
  2 s of CPU, so a 17 hour book is about 70 s (a guess of 8 to 10 times that on a Raspberry Pi 4).
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
  temp file in the same directory, named `.<name>.tendshelf-new`; verify the audio stream hash (`-map 0:a -c copy -f hash`) equals the
  original's, duration within 50 ms, chapter count and times equal, tags read back; check free space
  (file size + 5 %) and refuse files modified in the last 10 minutes; rename original to
  `.<name>.tendshelf-before`, rename temp into place, record `file_writes` state `swapped`. "Looks good"
  deletes the backup (`confirmed`); "Undo" restores it (`undone`). Restart recovery reads `file_writes`
  and completes or rolls back each half-done row; it never re-runs ffmpeg on its own.
- **Detail data folders (M4)**: `<data>/covers/incoming/` holds staged cover uploads until a job adopts them (swept after 24 h);
  `<data>/people/` holds author photos `<sha>.<ext>` and their 320 px thumbnails `<sha>-320.jpg`; `<data>/cache/online-covers/`
  holds covers fetched by lookups.
- **Upload staging (decision 0015)**: `<data>/uploads/incoming/<uploadID>/<fileID>.part` (mode 0600) holds the bytes of an
  upload until `upload.place` moves or copies them into the library folder; removed when the upload ends, and swept
  hourly for uploads with no chunk for 24 h and for folders with no `uploads` row.

## Background jobs

Kinds: `scan.library`, `media.probe`, `media.cover`, `media.compat`, `metadata.local`,
`metadata.online` (only user-triggered), `metadata.online_bulk` (AL-3, one bulk lookup per library, paced; decision [0009](decisions/0009-automatic-lookups.md)), `metadata.online_author`, `media.cover_adopt`, `media.write_details`,
`upload.place` (decision 0015; concurrency 1, 3 attempts, recovery reruns its idempotent recovery step first), `ebook.ingest` (R1), `align.book` (R3, states and resume in [read-along.md](read-along.md) section 2),
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
| `/` | Home (P1b-5): greeting, "New on the shelf" (the 12 newest books as spines) and the first-run, nothing-shared and scanning states; the continue-listening hero (cover, ruler with a tick per chapter, "Chapter n · m min left in chapter · h m left in book" at the person's speed, Resume, Chapters on tablet and desktop), "Your series in progress" (up to three rows with a round play link, none for the hero's book) and "Because you listened to" (up to 12 plain cards) are drawn into the page (P1c-6; complete without JavaScript); only the hero refreshes, from `GET /hx/home/hero`; the shared row arrives with P3 "Shared with you" (P3-6a): up to three of the newest visible shared books beside the series row, captioned "from {names}", with See all to `/libraries?show=shared`; nothing is drawn when nothing visible is shared, and "New on the shelf" is hidden when every visible book was shared. |
| `/setup` | First run (only while no owner exists, 404 after; always needs the setup code from the server log): create the owner account first, then name the server and add the first folder ([decision 0002](decisions/0002-design-handoff.md)). The next steps are owner-only (404 for anyone else): `/setup/server`, `/setup/folder` (`?job=` shows the scan streaming in), `/setup/done`; once `setup.done` is set the first two redirect to `/` |
| `/login`, `POST /logout` | Plain forms with full page loads (no hx-boost); 422 on a wrong answer, 429 when limited; `next` is checked by `auth.SafeNext` |
| `/libraries`, `/libraries/{id}` | Library (P1b-5): every visible book, or one library (404 when not visible); `?view=grid\|list&sort=added\|title\|author\|duration&genre=` (up to five, all must match)`&length=short\|medium\|long&show=unfinished\|not_started\|finished` (Show reads the person's own progress; an unknown value is ignored)`&cursor=&sheet=filters`, all in the address; a cursor page is a full page with "Back to the start"; phone and tablet filter in a sheet, from 1280 px in a bar |
| `/books/{id}`, `/books/{id}/{about\|chapters\|bookmarks\|details}` | Book: cover, details, chapters (P1b-6); the play region `#book-play` (P1c-6: "Start listening", "Resume" or "Listen again" as a link to `/stream/{file}#t=` that player.js takes over, plus "Mark as finished" or "Mark as not started"; refreshed from `GET /hx/books/{id}/play`) and chapter rows that play from the chapter, shown only to people who may listen; the Bookmarks tab (P1c-7a) lists the person's own bookmarks and notes (`?only=notes`, `?sheet=bookmark&bookmark=`, `?sheet=export`) and is shown only to people who can listen. Its plain-form routes: `POST /books/{id}/bookmarks` (add; `Accept: application/json` answers 201 for the player), `POST /bookmarks/{id}/note`, `POST /bookmarks/{id}/delete` (each answers 303; another person's bookmark, an unknown one and a book the person cannot listen to are all 404) and `GET /books/{id}/bookmarks/export?format=txt\|md`. A book the person cannot open is a 404 |
| `/books/{id}/edit`, `/books/{id}/match` | Edit details; online lookup and field compare (P2) |
| `/authors/{id}`, `/narrators/{id}`, `/series/{id}` | Person (books by title, `?cursor=`) and series (in order) pages (P1b-6); 404 unless the person can open a book of that role |
| `/search?q=&only=books\|people\|series` | Search: books, people and series from scoped reads, did-you-mean, live results into `#search-results` (P1b-6) |
| `/shelves`, `/shelves/{id}` | Personal shelves (P1b-8), the person's own only: a guest, another person's shelf and an unknown id are 404. `POST /shelves` (`name`, optional `book_id`, `next`), `POST /shelves/{id}/rename`, `GET\|POST /shelves/{id}/delete` (confirm page, then 303 `/shelves`), `POST /shelves/{id}/items` (`book_id`, `next`), `POST /shelves/{id}/items/{bookID}/remove`, `POST /shelves/{id}/items/{bookID}/move` (`dir=up\|down`). Plain forms with 303; `/books/{id}?sheet=shelves` opens the Add to shelf sheet, whose forms use `hx-post` and re-render the sheet. `?cursor=` pages a shelf. P3-6a: a shelf an enabled owner or admin marked shared (`shelves.shared = 1`) is readable, read-only, by owners, admins and members at `/shelves/{id}` ("Shared by {name}", only the books the reader can open); `/shelves` lists them under "Shared with you" when they hold at least one such book; `POST /shelves/{id}/share` (`shared` 1 or 0, staff and own shelf only, audited as `shelf.share`) turns sharing on and off. |
| `/me`, `/me/settings`, `/me/password`, `POST /me/sessions/{sid}/signout`, `POST /me/sessions/signout-others` | Profile (devices by the first 16 hex characters of the session hash), Settings (saved on change: `POST /me/settings` answers 204, with `HX-Refresh` when the theme changed; 303 without htmx; 422 when invalid), Change password (current password needed; limited 429; rotates this session and ends every other). Always the signed-in person only, any role. Downloads and statistics come in P4 |
| `/queue`, `GET /hx/queue`, `POST /queue`, `POST /queue/{bookID}/move`, `POST /queue/{bookID}/delete` | Up next (Q2): the full page (from the phone ⋯ menu), the list fragment (`?in=panel` in the Now playing panel), and add (`where` next or end), move and remove. Each needs the same book access as the book page; 404 otherwise. |
| `/invite/{token}` | Accept an invite |
| `/s/{token}` (GET, POST to unlock) | Share link (P3-5): one book's listen page; no session; `Referrer-Policy: no-referrer`, `noindex`, `no-store` |
| `/s/{token}/stream/{file}`, `/s/{token}/peaks/{file}`, `/s/{token}/cover` | The link's media, authorized from the link row; the player in link mode keeps its place in `localStorage` and calls no session route |
| `POST /books/{id}/share/link`, `POST /share/links/{id}/stop` | Make and stop a link (owner and admin) |
| `/books/{id}/text` (R1) | The book's text editions: upload, pairing, default edition, alignment status, export |
| `/books/{id}/read?mode=readalong\|read\|focus&edition=&at=` (R4) | Reader inside the layout (player keeps playing) |
| `/libraries/{id}/add` | Add books (decision 0015): owner and admin (`share.AddBooks`), 404 otherwise and when the library takes no uploads; with several such libraries a chooser links to the others' pages. JSON routes used by `js/upload.js` (session and `X-CSRF-Token`; errors through `api.Error`): `POST /libraries/{id}/uploads` (manifest, 201), `GET /uploads/{id}`, `PUT /uploads/{id}/files/{fileID}?offset=` (one chunk), `POST /uploads/{id}/finish` (202, starts `upload.place`), `POST /uploads/{id}/cancel` (204). Fragment `GET /hx/uploads/{id}` (one queue row; polls every 2 s while placing). Setting: `POST /admin/libraries/{id}/uploads` (owner only, 303). Remove: `GET\|POST /books/{id}/remove` (confirm page, then 303 to the library) |
| `/admin/users`, `/admin/libraries`, `/admin/jobs`, `/admin/settings` | Admin. `/admin/libraries` (P1b-4) lists folders and problems, starts scans, and links to `/admin/libraries/folders/new` and `/admin/folders/{id}/emptied`; scan status is `GET /hx/jobs/{id}`. The folder browser is the `?browse=<path>` mode of `/admin/libraries/folders/new` (sheet) and `/setup/folder` (inline); picking links back with `?folder=<path>`, and the submit still runs the add-folder checks |
| `/admin/ebooks` (R2), `/admin/runtimes` (R3), `/about` (R3) | Unpaired and refused ebooks; word-timing runtime check and licences; credits and model licences |

HTMX fragments (`/hx/...`): `GET /hx/libraries/items` and `GET /hx/libraries/{id}/items` with the same query as the page (infinite scroll; the sentinel is a link to the next page, so "Show more" works without JavaScript; a bad cursor is a 400 toast),
`GET /hx/search/suggest?q=`,
`POST /hx/books/{id}/finished`, `GET /hx/jobs/{id}`. Read-along: `POST /hx/books/{id}/editions`
(upload), `POST /hx/editions/{id}/pair`, `POST /hx/editions/{id}/unpair`, `POST /hx/editions/{id}/default`,
`DELETE /hx/editions/{id}` (forgets the edition; deletes only an uploaded copy), `POST /hx/editions/{id}/alignments`,
`POST /hx/alignments/{id}/cancel|resume|discard`, `GET /hx/editions/{id}/alignment`,
`GET /hx/books/{id}/read/chapters/{idx}?edition=`, `GET /hx/books/{id}/read/toc?edition=`, `POST /hx/me/reader-settings`,
`GET|POST /hx/books/{id}/highlights`, `PATCH|DELETE /hx/highlights/{id}`, `POST /hx/editions/{id}/exports/overlays`,
`POST /hx/admin/runtimes/check`.

Details, cover, author photo and write routes (M4; the patterns are in `routes_details.go`, `routes_cover_pick.go`, `routes_author.go`, `routes_write.go` and `routes_nav.go`): `POST /books/{id}/match` (starts a lookup job), `POST /books/{id}/match/apply`, `GET /books/{id}/match/{job}/cover/{n}`, `POST /books/{id}/details/reset`, `POST /books/{id}/cover` (choose a cover), `POST /books/{id}/cover/upload`, `GET /covers/{bookID}/choices/{coverID}`, `POST /books/{id}/write`, `POST /books/{id}/write/confirm` (Looks good), `POST /books/{id}/write/undo` (Undo), `POST /people/{id}/lookup` and `GET /people/{id}/photo` (authors), `POST /admin/online` (the `online.enabled` switch), `POST /me/nav` and `POST /hx/me/nav` (sidebar setting). `GET /hx/jobs/{id}` answers by the job's kind; a lookup job needs `?book=`.

Media and system: `GET /stream/{audioFileID}` (`?download=1` marks an offline copy), `GET /offline`, `GET /sw.js`, `GET /manifest.webmanifest`, `GET /peaks/{audioFileID}` (waveform peaks, binary, 404 while being made), `GET|POST /progress/{bookID}` (JSON, session + CSRF header or
`_csrf` body field, 4 KiB, form-encoded), `GET /covers/{bookID}?w=320`,
`GET /text/{editionID}/chapters/{idx}/timing.json` (ETag = chapter key), `GET /text/{editionID}/res/{resID}`
(raster images only), `GET /text/{editionID}/original` and `GET /exports/{id}` (`download_text`), `GET /healthz`,
`GET /sw.js`, `GET /manifest.webmanifest`, `GET /static/...` (embedded, immutable, hashed names; precompressed gzip served on `Accept-Encoding`, `Vary: Accept-Encoding`, ETag per encoding, Range ignored).

Routes added by the family milestone (M2), as registered at its exit. `TestRouteMatrix` fails when a registered
route has no row, so this list and the code cannot drift unnoticed (the matrix is the exact inventory; this is
the readable one):

| Route | What it does and who may use it |
| --- | --- |
| `GET /admin/users`, `/admin/users/{id}`; `POST /admin/users/{id}/role`, `/disable`, `/enable`, `/stop`, `/grants`, `/grants/{gid}`, `/grants/{gid}/remove`, `/grants/{gid}/renew` | Users admin: role, turn an account off or on, stop sharing, grants (owner and admin; members and listeners get 404) |
| `POST /admin/invites/{id}/cancel`, `/link`, `/email` | Cancel an invite, make a fresh link, open or send the invitation email (owner and admin) |
| `GET /admin/email`; `POST /admin/email`, `/admin/email/test`, `/admin/email/off`; `GET /hx/admin/email/test/{id}` | Optional mail server settings and its test message (owner only) |
| `GET /admin/backups`; `POST /admin/backups/schedule`, `/admin/backups/now`; job status through `GET /hx/jobs/{id}` | Scheduled database copies: schedule, "Back up now", the copy list (owner only; everyone else gets 404). To restore a copy: stop Tendshelf, replace `tendshelf.db` in the data folder with the copy and delete `tendshelf.db-wal` and `tendshelf.db-shm` if present, then start Tendshelf again. |
| `POST /admin/libraries/{id}/auto-lookup`, `POST /admin/libraries/{id}/lookups`, `POST /admin/libraries/{id}/lookups/stop`, `GET /admin/libraries/{id}/lookups`, `POST /admin/libraries/{id}/lookups/{book}/dismiss` | Automatic and bulk lookups (AL-4): the per-library switch for new books, start and stop of "Look up all books", the "Books that need a look" list and dismissing one book (owner and admin) |
| `POST /admin/libraries/scan`, `POST /admin/libraries/{id}/scan` | Rescan everything or one library (owner and admin) |
| `GET /invite/{token}`, `/invite/{token}/cover`; `GET`, `POST /welcome` | The invite page, the cover on it, and the welcome step after the account is made; no session needed for the first two |
| `GET /s/{token}`, `/s/{token}/stream/{file}`, `/s/{token}/peaks/{file}`, `/s/{token}/cover`; `POST /s/{token}` | Share link: one book, from the link row alone (see above) |
| `GET /share`; `POST /share`; `GET`, `POST /books/{id}/share`; `POST /books/{id}/share/link`, `/share/links/{id}/stop` | Share & invite page and the book's Share control (owner and admin) |
| `POST /shelves/{id}/share`, `/rename`, `/items`, `/items/{bookID}/move`, `/items/{bookID}/remove`, `/delete` | Shelf actions; sharing shows the shelf to members and admins, only with books they can already open |
| `GET /hx/player/panel/{bookID}`, `/about`, `/bookmarks`, `/count`, `/chapter/{index}` | Now playing panel fragments (desktop, 1280 px and wider); the same book access as the book page, 404 otherwise |
| `POST /hx/player/next` | Autoplay: the book that just ended is dropped and the next playable queued book starts (204 when the queue has nothing to play) |
| `GET /api/v1/queue`, `POST /api/v1/queue`, `POST /api/v1/queue/next`, `POST /api/v1/queue/{bookID}/move`, `DELETE /api/v1/queue/{bookID}` | JSON Up next (see [api.md](api.md) "Up next (queue)") |
| `GET /hx/home/hero`, `/hx/books/{id}/play` | Home hero refresh and the play region of a book page |
| `POST /api/v1/progress/batch`, `GET /api/v1/progress`, `GET`, `POST /api/v1/books/{id}/bookmarks`, `GET /api/v1/bookmarks`, `PATCH`, `DELETE /api/v1/bookmarks/{id}` | JSON sync (full list under "JSON API v1" below and in [api.md](api.md)) |

Tables at the M2 exit (schema 9): `users`, `sessions`, `settings`, `libraries`, `folders`, `books`, `audio_files`,
people, series and their links, `books_fts` (migrations 0001-0004), `shelves` and `shelf_items` (0005),
`progress`, `bookmarks` and `listening_sessions` (0006), `devices`, `invites`, `share_links` and `grants` (0007), `bookmarks.client_id`
(0008) and the invite email columns (0009). A new database is migrated from empty to 9; an older one is backed up
first (see `internal/db`).

At M4 the schema is 11 (`SchemaVersion` in `internal/db/db.go`): `book_sidecars` and `online_cache` (0010), `file_writes` (0011).

The schema is 13 after the automatic lookups and Up next work: `libraries.auto_lookup_at`, `book_lookups` and `online_usage` (0012), `queue_items` (0013).

Size budgets (gzip bytes the server sends): core CSS at most 19.5 KB (raised 2026-11-03 for native feel, decision 0011) and each page file a limit in `TestCSSBudget`
(`internal/web/static/motion_test.go`); the JavaScript limits are in the "Design regions" notes below (all app JS
38 KB). A budget is never met by minifying, only by writing less.

JSON API v1 (`/api/v1`, bearer device token or session cookie + CSRF):
Device tokens (P3-7a): `POST /api/v1/auth/token` takes JSON `{username, password, name, kind}` (kind `web`, `pwa`,
`desktop` or `mobile`; body at most 4 KiB) with the same limits and failure path as the sign-in form, and answers 201
with the 32-random-byte token once (only its SHA-256 is stored, never logged), the device and the person.
`Authorization: Bearer` is accepted only under `/api/v1/` and is looked up in the database on every request, so
revoking, disabling or a password change work on the next call. A request with both a bearer token and a
`ts_session` cookie is refused (400). Bearer requests carry no ambient credential, so they skip the CSRF token (the
cross-origin check still runs) and never get a `ts_csrf` cookie; cookie requests keep session + CSRF. A token dies
after 180 days unused, a person holds at most 50, and rows revoked over 30 days ago are pruned on the next issue.
Anonymous `/api/` requests get JSON 401, never a redirect.
The reference is `docs/api.md`. Shipped in P3-9a (read side): `POST|DELETE /auth/token`, `GET /me`, `GET /libraries`,
`GET /books?...` and `GET /libraries/{id}/books?...` (the Library page's filters, keyset cursor, `limit` 1..120),
`GET /books/{id}` (details, chapters, files with alias URLs, permissions, own progress), `GET /search?q=`,
`GET /shelves`, `GET /shelves/{id}`, `GET /shared`, and the media aliases `GET /covers/{bookID}`,
`/stream/{audioFileID}`, `/peaks/{audioFileID}` (the same handlers as the page routes, reachable with a token).
Built in P3-9b (sync): `GET /progress?since=`, `GET|PUT /progress/{bookID}`, `POST /progress/batch`,
`GET|POST /books/{id}/bookmarks`, `GET /bookmarks?since=`, `PATCH|DELETE /bookmarks/{id}`. Trimmed from the earlier
list on 2026-11-02: notes are bookmarks with a note (`?notes=1`), so there are no `/notes` routes; `POST /sessions`
is folded into the progress body (`speed`, `listenedMs`); editions, chapter text, timing and highlights move to the
read-along phases. Under `/api/` every error, including those of the reused page helpers, is JSON:
`{"error":{"code","message","field"?}}` with codes `unauthorized` 401, `forbidden` 403, `not_found` 404, `invalid` 400,
`conflict` 409, `too_large` 413, `unsupported` 415, `rate_limited` 429, `unavailable` 503 (`drm_refused`, `not_ready`
come with read-along). A book the person may not see, and one that does not exist, give the same `not_found` body.
Bearer requests are limited to 600 a minute per device (429 with `Retry-After`); sessions are not limited. There are
no CORS headers: the PWA is same-origin and native clients do not need them.

**Progress update** body: `{"positionMs","durationMs","finished","clientUpdatedAt","deviceId"}`, plus optional
`"text":{"editionId","sentenceId","offset"}` and `"source":"audio"|"text"` (R5).
`clientUpdatedAt` is clamped to server time + 60 s; the update applies only if it is newer than the
stored one. Response: `{"applied":bool,"progress":{...}}` (200 either way, so offline batches never fail).

Web player (P1c-3): `GET /hx/player` (the person's newest unfinished book, or 204) and `GET /hx/player/{bookID}`
answer the data-only `PlayerData` fragment (`#player-data`, hidden, attributes only: the book, the stored position,
the person's playback settings, optional per-theme accents, one `<li>` per audio file with offset, MIME and
whether `/stream` will serve it, one per chapter; no file path). `POST /hx/books/{id}/finished` (`finished=1|0`)
marks a book finished or unfinished: htmx gets 204 and `HX-Trigger: progress-saved`, a plain form a 303 to a safe `next`.

Web player (P1c-4, `internal/web/static/js/player.js`, one module, no imports): the one `<audio id="audio">` and the
mini-player sit in `#player` outside `#main`, so a boosted swap never touches them; all state lives in the module.
Click protocol: a control `[data-ts-play="{bookID}"]` carries `data-ts-file`, `data-ts-file-start-ms`,
`data-ts-start-ms`, `data-ts-mime` and `data-ts-may-need-compat`; the click sets `/stream/{file}#t={sec}` and calls
`play()` in the same task (the gesture is kept), then `GET /hx/player/{book}` hydrates chapters, files and settings.
`[data-ts-play-at="{ms}"][data-ts-book]` seeks inside the loaded book or loads that book there. `window.tsPlayer` exposes
`state load play pause toggle seek skip chapter setSpeed debug`; `ts:player:state` (kind: load, play, pause, seek,
chapter, speed, error) and `ts:player:tick` (once a second) fire on `document`, `progress-saved` (detail
`{bookId, positionMs, applied}`) bubbles from `<body>`. Saves go to `POST /progress/{book}` (form body, `X-CSRF-Token`
header) every 10 s while playing, on pause, 2 s after a seek while paused, at the end of the last file
(`finished=1`), and on hide through `sendBeacon` (`_csrf` field, once per hide). The last save is kept in
`localStorage["ts.pending"]` until answered and re-sent at the next start; a redirected or non-JSON answer means signed
out: saves stop and one toast says so. "Continue there?" asks when the stored row is from another device, not
finished, and more than 60 s away from here; it is checked after a save answers `applied:false`, on play after a break
of 60 s or more, and on becoming visible after 60 s hidden; "Stay here" saves with `force=1`. A file the browser
cannot decode is requested as `?compat=1` (`HEAD`, polled at `Retry-After` for up to 10 minutes). Media Session
carries the title, author, chapter and cover with eight action handlers. Keys: Space and the arrows in player.js; `/`,
`g l`, `g h` and `?` in shell.js. Budgets (`TestJSBudget`, gzip bytes): player.js 12 KB, shell.js 4 KB, player-full.js 12 KB,
waveform.js 7 KB (it also runs the desktop bar's wave: bars, hover, drag, keys, times, hairline, volume pop; 7.0 KB measured), now-panel.js 1 KB (the desktop Now playing panel: load, tabs, slots; player.js carries 0.2 KB more for the Home hero's Show player, so its share is 12.5 KB), 36 KB in all (operator 2026-11-02: 28, then 30, then 34, 35 for the share-link mode, then 36 KB); `player.css` 4 KB; `now-panel.css` 3 KB (loaded on every page).

Full player (P1c-5a, `static/js/player-full.js`, a second async module, no imports; `components/player_full.templ`): one
`<dialog id="player-full" class="ts-player" hx-preserve>` in `Layout` after `#player`. It never touches `#audio`: it reads
`window.tsPlayer` (`info()`, `state()`, `seek`, `skip`, `chapter`, `play`, `toggle`) and the `ts:player:state`, `ts:player:tick`
and `ts:player:ready` events (player.js fires ready once the API exists, because both modules load async). Below 1280 px it
opens with `showModal()` (focus trap, Escape, focus back on the opener; the bar is hidden by `is-covered`) inside a view
transition that glides the cover (`view-transition-name: ts-now-cover`, named only during the transition; none with reduced
motion); from 1280 px it is a non-modal 400 px side panel and the bar stays. The mini open link (kept `hx-boost="false"`) and a
swipe up on the bar open it; the chevron, Escape and a swipe down on the header close it (`--ts-drag` follows the finger;
under 30 % of the height it springs back). The cover accent and tint reach CSS only as `--cover-accent` and `--cover-tint`
on `#player-full` (`style.setProperty`, validated as `#rrggbb`, per `data-theme` or the system scheme, removed for the contrast
theme and when missing); `data-tint` on the dialog picks the strength (full in dark, pale in light and sepia, none in contrast).
The scrubber covers the whole book: ticks at chapter starts, fill, needle and chapter line from `--ts-pos`, `--ts-ch-at`,
`--ts-ch-w`; a real range input under it (keys by the skip setting, PageUp/PageDown a chapter, Home/End; a pointer drag is
ours, halved past 40 px and quartered past 80 px from the bar, snapping within 1.5 % of a chapter start, seek on release;
`aria-valuetext` at most every 10 s while playing). The chapters sheet is built with DOM APIs from a `<template>`.

Speed, sleep timer, volume, bookmark key (P1c-5b). The Speed, Sleep and Chapters sheets share one `openSheet(name)` (the body goes
`inert`, focus lands on the current choice, Up/Down move between choices, Escape closes the sheet before the player). Speed: presets
and a 0.05 slider call `tsPlayer.setSpeed`; the change is saved to the person's settings with `POST /me/settings` (`speed=` alone,
debounced one second, sent with `keepalive` on `pagehide`, failures silent), so it follows them to other devices; `preservesPitch`
is set in player.js. Volume is per device (`localStorage["ts.volume"]`); iOS ignores `audio.volume`, so there
`tsPlayer.canSetVolume` is false: the desktop bar's slider is hidden and the sleep timer pauses without a fade. The sleep timer
lives in memory (a reload clears it) and counts only while sound plays, from `performance.now()` deltas. "End of chapter" ends at
the chapter end when it is chosen (the next chapter's when under 2 s are left) and, at that end, pauses and seeks there so Resume
starts the next chapter; minutes are real minutes whatever the speed. The last ten seconds ramp `audio.volume = userVolume * fade`
in 250 ms steps; the fade never overwrites the person's level, and every timer end, "+10 min", shake or turn-off restores it. The
sleep card shows while fading and playing, in the dialog when it is open and above the bar otherwise; a polite status line says
"Sleeping in N seconds" once and the per-second count is `aria-hidden`. A shake (two jolts over 12 m/s² within a second) adds ten
minutes where `devicemotion` is allowed; iOS asks inside the tap that sets the timer. The bookmark button and the `B` key post
`position_ms` of the live position to `POST /books/{id}/bookmarks` (JSON answer with `noteHref`) and show a toast with an "Add a
note" link that uses the Bookmarks tab's htmx attributes; while the modal player is open toasts go into its own
`[data-ts-full-toasts]` region. The Bookmarks tab's form is always rendered (hidden when there is no stored place) and the player
fills in the live position. JS budgets (gzip): player.js 12.5 KB, player-full.js 12.5 KB, shell.js 4.5 KB, waveform.js 7 KB, now-panel.js 1 KB, all app JS 38 KB (operator, 2026-11-02) (raised from 28 KB for the waveform, the desktop bar and the Now playing panel, operator 2026-11-02); `player.css` 4 KB; `now-panel.css` 3 KB.

## Design regions

The design handoff (`docs/design/tendshelf-handoff/docs/HANDOFF.md`, "Server-rendered partials") names
targets and triggers; its suggested routes are replaced by these ([decision 0002](decisions/0002-design-handoff.md)).
"Page + `hx-select`" means the canonical page URL answers with a full page and htmx selects the region,
so Back, reload and links without JavaScript all work.

| Region (target) | Trigger | Route |
| --- | --- | --- |
| Continue listening hero `#home-hero` | `progress-saved from:body` only (the hero is already in the page, so no `load`) | `GET /hx/home/hero` |
| Home rows `#row-{name}` | none: drawn inline (P1c-6) | no `/hx/home/rows/*` endpoints; the rows do not change while listening |
| Book play region `#book-play` | `progress-saved from:body` | `GET /hx/books/{id}/play` |
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
win without specificity fights. Client code is plain ES modules in `static/js/` (`shell.js`, `player.js`), loaded by separate script tags because hashed names rule out relative imports; budgets are gzip bytes (AGENTS.md). `shell.js` only toggles classes and sets custom properties with `style.setProperty`, closes
filter chip menus (`<details data-ts-chipmenu>` works without it), and ends with
`performance.mark("ts-ready")`, a measurement mark that nothing reads in production and no data leaves
the browser; `make perf` (Chromium, 4x CPU throttle, 390 px, 2,000 books) reads it. A page loads the
core files plus its own `pages-*.css`, so the CSS budgets are per load: core at most 19.5 KB gzip, each
`pages-*.css` at most 3 KB gzip.

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
  ones. Device tokens for the API (below) are revoked
  when a person signs out everywhere else, changes the password, is disabled or has the password reset. A forgotten password is reset on the
  server with `tendshelf reset-password [-data DIR] <username>`: it prints a new generated password once,
  signs the account out everywhere and writes an audit row; it works while the server runs.
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
  `server.manage`, `shelves.use`, `shelf.view`. Owner everything; admin everything except
  `server.manage`; a disabled person nothing. Members and guests act only through an active grant (table
  `grants`, scopes all, library, shelf, book): `library.view` on a library holding a covered book;
  `book.view` and `book.listen` on a covered book; `book.download` needs `can_download`, `text.read` needs
  `can_read`, `text.download` both; nothing else. `Can` reads the book's library from the database and
  ignores `Target.LibraryID`; a database error is a refusal. Members also have `shelves.use` on their own
  shelves; a shelf is always its owner's, and `shelf.view` lets an owner, admin or member read a shelf that
  is shared. `users.manage` on a person: the owner for anyone, an admin for members and guests only.
  `SharedBooks` (P3-6a) is a SELECT of `(book_id, shared_at, sharer_id)` (book grants, shelf grants on a
  staff shelf, and for non-guests the other staff's shared shelves; not deduplicated; library and "all"
  grants are not shares) that `library.Scope.Shared` carries; the visibility filter still decides what
  shows. `SharerSQL` is the one "enabled owner or admin" condition.
  `BookFilter` and `LibraryFilter` give the SQL fragment for listings: one `EXISTS` subquery on `grants`
  with two positional arguments (person, now), built from the same SQL as `Can`, so a book is listed if and
  only if `Can(book.view)` is true. Inner aliases start with `share_`, which callers may not use. The
  `share.Grants` repository (`Put`, `Revoke`, `Get`, `ForUser`) writes grants and the audit log; it does
  not authorize its caller.
- CSRF: token required in `X-CSRF-Token` or `_csrf` for unsafe methods. It is an HMAC of the signed-in
  session (`s:` + session hash) or, for anonymous visitors, of the `ts_csrf` cookie (`c:` + value), so a token
  for one can never pass as the other. The HMAC key is kept in `settings` (`web.csrf_key`), so open forms
  survive a restart. Cross-site browser requests are refused by the standard library's origin check first.
- Roles: **owner** (everything, server settings, one per server), **admin** (users,
  libraries, metadata, jobs, invites), **member** (granted libraries/books, own
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

M3a offline pocket ([decision 0012](decisions/0012-offline-pocket.md), plan [m3-offline.md](plans/m3-offline.md));
this replaces the earlier sketch (app shell and visited pages in the cache, an IndexedDB queue).

- **Install files** (public, on the outer mux beside `/static/`, outside the gate and CSRF): `GET /sw.js` is
  `self.TS_BUILD = {"build":"<12 hex>","assets":[...hashed /static/ URLs...]};` followed by
  `static/js/sw.js` (`Cache-Control: no-cache`, ETag `"sw-<build>"`, 304, gzip). `build` is a hash of the
  server version and every hashed asset name (`static.Set.Build`), so any new asset makes a new worker and a
  new `ts-static-<build>` cache. `GET /manifest.webmanifest` is the web app manifest (standalone, hashed
  192/512/maskable PNG icons and the SVG). Layout and Frame heads link the manifest, the 180 px
  `apple-touch-icon` and the Home Screen title; share-link pages do not (`Page.NoInstall`).
- **`GET /offline?books=a,b`** (signed in, any role): the shell the worker shows for a navigation it cannot
  send to the server, `Cache-Control: private, no-store`. Up to 200 ids; each is checked again for
  `ListenBook` and a streamable file and skipped silently otherwise, so the page never names a book the
  person may not play. It lists the ready books (play control, "Ready offline · Chapter n"), then up to five
  recent unfinished books that need a connection.
- **`GET /stream/{id}?download=1`**: needs `share.DownloadBook` as well as `ListenBook` (403 plain text
  otherwise), `Cache-Control: private, no-store`; `GET /api/v1/books/{id}` is the download manifest.
- **Layout contract** for `js/offline.js`: `<html data-ts-person data-ts-chunk>` and a hidden `#ts-offline`
  banner (words on `data-ts-msg-*`, the download manager's URL on `data-ts-downloads-src`) before `.ts-shell`.
  The page scripts are linked only when the file exists. Sign-out answers `Clear-Site-Data: "storage"` and
  redirects to `/login?signed_out=1`, where the page wipes what the header could not (insecure address,
  older browser).
- **Rule** (also in AGENTS.md): the worker answers only hashed `/static/` files and, from the signed-in person's
  own `ts-person-<id>` cache, the stream, cover, peaks and player fragment of a downloaded book, plus the
  `/offline` shell for a navigation that cannot reach the server. Nothing else is cached or answered, and a
  change of person or a sign-out deletes the cache and the offline records.
- **Browser storage** (all wiped on sign-out or a change of person): `ts.offline` (the download index per book),
  `ts.offline.person`, `ts.offline.prefs` (Remove finished books), `ts.offline.checkedAt` (the daily finished
  check), `ts.offline.marks` (queued bookmarks), `ts.pending` (queued positions), and `ts.offline.msgs` (the
  Downloads page's sentences, kept so the book page's Download control can word its states).
- Progress, bookmarks and notes made offline wait in `localStorage` (`ts.pending`, `ts.offline.marks`) and go
  to `POST /api/v1/progress/batch` and the bookmark endpoint on reconnect. iOS storage limits and eviction are
  stated in the UI (Safari may clear downloads after a week without a visit). With `read_text` and an aligned
  edition, Download will also store chapter fragments, timing JSON and images (later, read-along).

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

## Dependencies and approvals

Approved 2026-11-01 for P0 ([decision 0002](decisions/0002-design-handoff.md)); each gets its
`NOTICES.md` row in the commit that adds it:

| Module / file | Why | Note |
| --- | --- | --- |
| `github.com/a-h/templ` v0.3.1100 (**approved and added in P0b**) | templ runtime; the CLI runs through the `tool` directive in `go.mod` (`make generate`), so one version source | MIT |
| `modernc.org/sqlite` v1.61.1 (**approved and added in P0a**) | SQLite driver | Chosen over `mattn/go-sqlite3`: pure Go, so `CGO_ENABLED=0` cross-compiles to ARM NAS boxes, Windows, macOS and scratch containers; FTS5 is compiled in (checked in `lib/sqlite_linux_amd64.go`). Cost: slower than the C driver on heavy writes (guess 1.5-2x); fine for this workload. BSD-3 |
| `htmx.min.js` 4.0.0 (**vendored in P0b**) as `internal/web/static/vendor/htmx-4.0.0.min.js` | Interactivity | Official release file, version and SHA-256 in `NOTICES.md`; licence 0BSD, text beside it. htmx 4 inheritance is explicit (`:inherited`) and 4xx/5xx responses swap |
| `golang.org/x/crypto` v0.58.0 (argon2) (**added in P1b-1**) | Password hashing, `internal/users` only | BSD-3 |
| Fonts **vendored in P0b** to `internal/web/static/fonts/` (woff2): Fraunces (variable roman and italic), Fira Sans (400, 400 italic, 500, 600), Fira Mono (400, 500); 1.23 MB in total, 361 KB preloaded | The design's type; Fira Mono for mono text only (the Research lab is dropped, decision 0007) | SIL OFL 1.1, texts beside the files; upstream commits and SHA-256 in `NOTICES.md`; self-hosted, never Google Fonts |

Read-along (phase R), **approved by the operator on 2026-11-03** ("all dependencies of read along"); each
still gets its `NOTICES.md` row, with its licence read from the release, in the commit that adds it, and the
PDF reader is chosen between the two options below on fixtures during R1:

| Module / file / runtime | Why | Note |
| --- | --- | --- |
| `golang.org/x/net/html` (**added in M5 R1 E1**, v0.61.0, also `html/charset`) | HTML5 tokenizing of EPUB XHTML, HTML files and MOBI/KF8 content (R1) | BSD-3; Go team |
| `golang.org/x/text` (`unicode/norm`, `encoding/...`) (**added in M5 R1 E1**, v0.43.0) | NFC normalisation for matching and alignment (R1, R3); charset decoding for FB2, HTML and text (R1) | BSD-3; Go team |
| PDF text-layer reader: candidate `github.com/ledongthuc/pdf`, or an own minimal reader | PDF import (R1) | BSD-3 (guess, read it); evaluate ToUnicode/CMap coverage on fixtures before choosing; the module may not be enough for complex PDFs (guess) |
| Font vendored to `internal/web/static/fonts/` (woff2): OpenDyslexic | The one extra reader font (R4); the reader otherwise uses the design's Fraunces and Fira Sans | SIL OFL 1.1 (guess; read it from the release); `NOTICES.md` row |
| Optional runtime, not shipped: Python 3.12 + `torch` CPU (2.8, as TendVox) + `transformers` + `safetensors` + `numpy` | The emissions worker (R3) | Owner-installed; BSD-3 / Apache-2.0 / Apache-2.0 / BSD-3 (guess for exact versions) |
| Optional models, not shipped: `facebook/wav2vec2-base-960h` (English), `jonatasgrosman/wav2vec2-large-xlsr-53-spanish` (Spanish) | Emissions (R3) | Apache-2.0 per model cards (TendVox `docs/alignment-worker.md`); revision and file hashes pinned in `runtimes/align/models/*.json` |
| Test-only, opt-in: epubcheck (Java) | Validate the overlay export (R6) | BSD-3 (guess); never in `make check` |

Considered and not chosen: `github.com/clipperhouse/uax29` (sentence segmentation; own rules handle
abbreviations better and stay versioned), `github.com/yuin/goldmark` (Markdown; only structure is
needed), whisper.cpp or Parakeet as a second ASR runtime (see read-along.md section 2).

Later, **approved by the operator on 2026-11-03** (each with its `NOTICES.md` row in the commit that adds it): `github.com/go-webauthn/webauthn` (passkeys), `github.com/fsnotify/fsnotify`
(folder watcher), `github.com/wailsapp/wails/v3` (desktop module only), an OpenID Connect client.
