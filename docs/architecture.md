# Architecture

One Go process serves pages, fragments, a small JSON API, audio and ebook text. SQLite (WAL) holds everything that
is not an audio file or a cached image. ffmpeg/ffprobe are the only child processes ([reader.md](reader.md),
[decision 0018](decisions/0018-book-reader.md)). No Node, no SPA.

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
9. A text location is stored beside the position (invariant 6), never instead of it.

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
| `desktop/` (P5) | Separate Go module: Wails v3 shell. Not imported by the server. |
| `android/` (M8) | Separate Gradle project: Kotlin app with a WebView shell and Media3 playback (decision 0024). A shared Kotlin module with no Android imports holds API and position logic. Not imported by the server; `make build` is unchanged. |

Dependency direction: `web`, `api` → services (`library`, `progress`, `share`, `users`, `metadata`,
`scan`, `jobs`, `ebook`) → `db`, `media`. `ebook` imports neither. Services never import `web` or `api`.

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
| `books` | `id`, `library_id`, `folder_id` (cascade on folder removal; the scan works per folder), `root_path` UNIQUE, `title`, `sort_title`, `subtitle`, `description`, `publisher`, `published_year`, `language`, `isbn`, `asin`, `abridged`, `dramatized`, `explicit`, `duration_ms`, `cover_id`, `kind` ('audio' = the scanner owns the row, 'text' = the ebook service; B1, 0021), `refused_reason` NULL, `added_at`, `updated_at`, `missing_since` NULL | Resolved values (cache of the merge). |
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
| `book_settings` (0024) | `user_id` (cascade on `users`), `book_id` (cascade on `books`), `speed` REAL 0.5-3.0, `updated_at` ms; PK (`user_id`,`book_id`) | A person's own settings for one book, the speed first (L1). A book with no row plays at the usual speed (`user_settings` `playback.speed`). Written by `POST /hx/books/{id}/speed`; `make_default=1` sets the usual speed and drops the row. |
| `progress` | `user_id`, `book_id`, `position_ms`, `duration_ms`, `finished_at` NULL, `client_updated_at`, `server_updated_at`, `device_id`, `device_label`; PK (`user_id`,`book_id`); text columns (`position_source`, `text_edition_id`, `text_sid`, `text_off`) arrive with R5 | One row per person and book. A write applies only if its `client_updated_at` is strictly newer, compared and written in one statement; the client time is clamped to server now + 60 s, and explicit server acts (mark finished, listen again, "continue here") win. `device_id` and `device_label` only name the device for the "continue there?" prompt; they never decide access. A text update (R5) sets `position_ms` to the sentence start (or, in a gap, the previous timed sentence's end). |
| `listening_sessions` | `id`, `user_id`, `book_id`, `device_id`, `started_at`, `ended_at`, `start_ms`, `end_ms`, `listened_ms`, `speed_pct` | Written with each progress save since P1c (same transaction); P4 reads it. A save extends the device's latest session unless the pause exceeds 60 s, the speed changed or the position jumped over 35 s beyond the audio played; `listened_ms` is wall-clock playing time reported by the device and capped at 10 min per save. |
| `bookmarks` | `id`, `user_id`, `book_id`, `position_ms`, `note`, `created_at`, `updated_at`, `deleted_at`, `client_id` NULL (0008, unique per person), `edition_id` NULL, `sid` NULL (0019) | Soft delete (the note is cleared) so offline clients sync deletions. `edition_id` and `sid` NULL (0019): both set when the bookmark is made in the text (`sid` = sentence id); `edition_id` is SET NULL if the edition is forgotten. |
| `shelves` / `shelf_items` | `id`, `owner_id`, `name` (1-80 characters, unique per owner ignoring case), `shared` (P3-6a: 1 = visible to members and staff, effective only while the owner is an enabled owner or admin), `created_at`, `updated_at` / `shelf_id`, `book_id`, `idx`, `added_at`; PK (`shelf_id`,`book_id`) | Schema v5 (P1b-8). Cascade on the owner and on the book. At most 200 shelves per person and 5,000 books per shelf. Every read filters on `owner_id` and on the person's book scope; rows of a hidden or missing book stay and reappear with it. |
| `jobs` | `id`, `kind`, `state` ('queued','running','done','failed','cancelled','held'), `payload_json`, `result_json`, `error_code`, `error_text`, `attempts`, `max_attempts`, `lease_until`, `not_before`, `progress_json`, `created_by`, `created_at`, `updated_at` | |
| `file_writes` | `id`, `job_id`, `book_id`, `audio_file_id`, `state` ('copying','verifying','swapping','swapped','confirming','confirmed','undoing','undone','failed'), `original_path`, `backup_path`, `temp_path`, `before_sha256`, `after_sha256` NULL, `audio_hash`, `error_code` NULL, `created_by`, `created_at`, `updated_at` | Migration 0011. No foreign keys, so a row outlives a pruned job and a deleted book. The write-details ledger; drives restart recovery and Undo. |
| `uploads` (0014) | `id` (`upl_`), `library_id` (cascade), `folder_id` (cascade), `created_by`, `label`, `state` CHECK IN ('receiving','queued','placing','placed','refused','failed','cancelled','expired'), `bytes_total`, `bytes_received`, `job_id` NULL, `scan_job_id` NULL, `book_path` NULL, `temp_path` NULL, `refused_file` NULL, `error_code` NULL, `skipped` (count of left-out files), `created_at`, `updated_at` | One upload is one book. Rows of ended uploads are pruned after 30 days; `upload_paths` stays. |
| `upload_files` (0014) | `id` (`upf_`), `upload_id` (cascade), `idx`, `rel_path` (slash-separated, checked), `size`, `modified_ms`, `sha256` NULL (optional promised hash), `bytes_received`; UNIQUE (`upload_id`,`rel_path`) | The manifest and resume offsets. |
| `upload_paths` (0014) | `id` (`upp_`), `upload_id` (no foreign key), `library_folder_id`, `path`, `kind` ('dir','file'), `state` ('planned','staged','present','removed','kept'), `size` NULL, `mtime_ns` NULL, `quick_hash` NULL, `sha256` NULL, `created_at`, `updated_at`; partial UNIQUE (`path`) WHERE `state` IN ('planned','staged','present') | The ledger of every path an upload created; written before the path is created. The only list "Remove this book" deletes from. |
| `audit_log` | `id`, `at`, `user_id`, `action`, `target`, `detail_json` | Logins, invites, grants, file writes. |
| `books_fts` | FTS5, contentless with delete, over title, subtitle, authors, narrators, series, description; rowid from `books_fts_ids`; kept by triggers on books, book_people, book_series, people and series names (migration 0004); `books_fts_vocab` feeds did-you-mean. | Notes are per user: searched with a user filter, not in this index. |
| `user_settings` | `user_id`, `key`, `value_json`, `updated_at`; PK (`user_id`,`key`) | The one per-user settings store: typed in `users.Settings` (`ui.theme`, `ui.player_tint`, `ui.big_buttons`, `playback.speed`, `playback.skip_back_s`, `playback.skip_forward_s`, `playback.auto_rewind`, `playback.next_in_series`; the player reads them with `LoadSettings`), `reader.v1`. |
| `ebook_files` (R1, R2) | `id` (`ebf_`), `origin` ('library','upload'), `library_id`, `folder_id` NULL, `path` UNIQUE NULL, `book_id` NULL (NULL = no book yet, only until the next catch-up; the book going away unpairs it), `merged_from` NULL (the text book this file was merged out of; a split restores that id), `paired_by` NULL ('folder','upload','isbn','title','person'; an ASIN match is stored as 'isbn'), `hold` NULL ('person' = a person unpaired it, 'kept' = kept on its own; automatic pairing touches only NULL), `book_default` (0/1; partial unique index: one per book), `ext`, `size`, `mtime_ns`, `quick_hash`, `sha256` NULL, `format`, `state` ('pending','ready','refused','failed'), `refused_reason` NULL, `uploaded_by`, `missing_since` NULL | Pairing lives here (migrations 0016, 0017). Library ebooks read-only; uploads under `<data>/ebooks/`. A ready file with no audiobook is a `kind='text'` book of its own (decision 0018); `ebook/merge.go` moves it onto an audiobook. |
| `editions` (R1) | `id` (`edn_`), `ebook_file_id`, `source_sha256`, `recipe`, `doc_sha256`, `doc_bytes`, `format`, `layout` ('flow','page'), `title`, `subtitle`, `authors_json`, `language`, `isbn`, `asin`, `chapter_count`, `word_count`, `char_count`, `page_count`, `meta_json` (description, publisher, year, series, series_index; written by the reader's ingest), `created_at`; UNIQUE (`ebook_file_id`, `source_sha256`, `recipe`) | One canonical document read from one file's exact bytes by one recipe; document file `<data>/text/<doc_sha256>.json.gz`. Not the pairing: that is `ebook_files.book_id`. |
| `ebook_not_same` (R2) | `ebook_file_id`, `book_id`, `user_id` NULL, `created_at`; PK (`ebook_file_id`, `book_id`) | "Not the same book": keeps folder and automatic pairing from offering that file for that book again. Cascades with the file and the book. |
| `text_choices` (R2) | `user_id`, `book_id`, `ebook_file_id`, `updated_at`; PK (`user_id`, `book_id`) | A person's edition of a book (replaces the planned `edition.<bookID>` user setting). Removed when the file is unpaired or re-paired. |
| `merged_books` (B1, 0021) | `from_id` PK, `to_id` (cascade with the book), `merged_at` | A merged-away text book id still answers: `/books/{id}` and `/books/{id}/read` answer 308 to `to_id` when the person may see it; a reading-position save lands on `to_id`. A split deletes the row and reuses the id. |
| `merged_folders` (S4, 0027) | `root_path` PK, `library_id` (cascade), `from_id`, `to_id` (cascade with the kept book), `merged_by` NULL, `merged_at` | The folder of an audiobook copy that was merged into another. The scan reads it and leaves that folder out (nothing is added, hashed or taken for a moved file) while the merge stands; deleting the kept book frees it. "Show that copy again" deletes the row. |
| `book_not_duplicates` (S4, 0027) | `a_id`, `b_id` (both cascade; `a_id < b_id`), `created_at`; PK (`a_id`,`b_id`) | "Not the same" on the Duplicates page. |
| `reading_positions` (B1, 0021) | `user_id`, `book_id`, `edition_id` NULL, `chapter_idx`, `sid`, `off`, `quote`, `char_pos`, `percent_bp` (0..10000), `finished_at`, `client_updated_at`, `server_updated_at`, `device_id`, `device_label`; PK (`user_id`, `book_id`) | The text place of one person in one book (anchor, quote, percent; never a page number). Newest `client_updated_at` wins. Never written by listening, and listening never reads it. Private to `user_id`. |
| `reading_sessions` (B1, 0021) | `id`, `user_id`, `book_id`, `device_id`, `started_at`, `ended_at`, `chars_read`, `active_ms` | Measures reading speed (`progress.ReadingSpeed`: last 30 sessions of at least a minute, 300 to 3000 characters a minute). A pause over 30 minutes opens a new session. |
| `edition_chapters` (R1) | `edition_id`, `idx`, `title`, `src`, `front`, `word_count`, `sentence_count`, `text_sha256`; PK (`edition_id`,`idx`) | `text_sha256` over the normalised text drives re-anchoring of changed chapters only. |
| `highlights` (R4, 0019; rebuilt without audio times by 0020) | `id` (`hl_`), `user_id`, `book_id`, `edition_id` NULL (SET NULL), `chapter_idx`, `start_sid`, `start_off`, `end_sid`, `end_off`, `quote`, `quote_prefix`, `quote_suffix`, `colour` ('yellow','green','blue','pink'), `note` (max 2,000 characters; a note on a moment stays `bookmarks.note`, there is no notes table), `client_id` NULL (unique per person), `created_at`, `updated_at`, `deleted_at` | Text range. Private to `user_id`. Soft delete for offline sync. |

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

- **Chapter edits (L3a)**: an edit is the `chapters` rows themselves, stored with source `user` (typed) or `online`
  (a lookup), so the player, the API and bookmarks read them unchanged; it changes rows only and never a library file.
  Invariant 2: once any row of a book has source `user` or `online`, `finalize` leaves the book's chapters alone
  (`library.ChaptersOverridden`); only `scan.ResetChapters` (the "Go back to the chapters in the files" button) rebuilds
  them from the files, and it refuses while a file still waits for its probe. Invariant 3: a scan never destroys an
  edit and never clamps it, even when files vanish or shrink; a list that no longer fits the book (a last end more than
  1 s past the duration) is refused by the write job (`chapter_fit`). A stored list is always whole: first start 0,
  strictly rising starts, each end the next start, last end the duration (`library.BuildChapters`). The editor is
  `/books/{id}/chapters/edit` (the plain `/books/{id}/chapters` is the book page's Chapters tab); Save is all or nothing
  and refused when `library.ChaptersRev` of the stored rows differs from the form's `rev`.

## Background jobs

Kinds: `scan.library`, `media.probe`, `media.cover`, `media.compat`, `metadata.local`,
`metadata.online` (only user-triggered), `metadata.online_bulk` (AL-3, one bulk lookup per library, paced; decision [0009](decisions/0009-automatic-lookups.md)), `metadata.online_author`, `media.cover_adopt`, `media.write_details`,
`upload.place` (decision 0015; concurrency 1, 3 attempts, recovery reruns its idempotent recovery step first), `ebook.ingest` (R1).
A job runner per kind with bounded concurrency (probe 2, compat 1, write_details 1, ingest 1). Leases of 60 s
renewed while running; on startup, expired `running` jobs return to `queued` except `write_details`,
which goes to `held` until `file_writes` recovery decides. Cancellation via context. Progress is
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
| `/books/{id}/text` (R1) | The book's text editions: upload, pairing, default edition |
| `/books/{id}/read?ch=&at=&off=&sheet=contents\|marks\|reading&edition=` | Reader inside the layout (player keeps playing); needs `ReadText`, a link visitor and a person without `can_read` get 404. |
| `/libraries/{id}/add` | Add books (decision 0015): owner and admin (`share.AddBooks`), 404 otherwise and when the library takes no uploads; with several such libraries a chooser links to the others' pages. JSON routes used by `js/upload.js` (session and `X-CSRF-Token`; errors through `api.Error`): `POST /libraries/{id}/uploads` (manifest, 201), `GET /uploads/{id}`, `PUT /uploads/{id}/files/{fileID}?offset=` (one chunk), `POST /uploads/{id}/finish` (202, starts `upload.place`), `POST /uploads/{id}/cancel` (204). Fragment `GET /hx/uploads/{id}` (one queue row; polls every 2 s while placing). Setting: `POST /admin/libraries/{id}/uploads` (owner only, 303). Remove: `GET\|POST /books/{id}/remove` (confirm page, then 303 to the library) |
| `/admin/users`, `/admin/libraries`, `/admin/jobs`, `/admin/settings` | Admin. `/admin/libraries` (P1b-4) lists folders and problems, starts scans, and links to `/admin/libraries/folders/new` and `/admin/folders/{id}/emptied`; scan status is `GET /hx/jobs/{id}`. The folder browser is the `?browse=<path>` mode of `/admin/libraries/folders/new` (sheet) and `/setup/folder` (inline); picking links back with `?folder=<path>`, and the submit still runs the add-folder checks |
| `/admin/ebooks` (B3), `/about` (R3) | Suggested ebook and audiobook merges, and ebook files that could not be opened; credits and licences |

HTMX fragments (`/hx/...`): `GET /hx/libraries/items` and `GET /hx/libraries/{id}/items` with the same query as the page (infinite scroll; the sentinel is a link to the next page, so "Show more" works without JavaScript; a bad cursor is a 400 toast),
`GET /hx/search/suggest?q=`,
`POST /hx/books/{id}/finished`, `GET /hx/jobs/{id}`). Ebooks (as built):
`POST /books/{id}/text` (upload), `POST /books/{id}/text/{fileID}/pair|unpair|not-same|default|choose|dismiss`,
(These need edit rights on the book; a file not paired to that book is a 404.)
`GET /hx/books/{id}/read/chapters/{idx}?edition=`,
`POST /hx/books/{id}/highlights`, `PATCH|DELETE /hx/highlights/{id}` (the person's own; htmx only), and `POST /books/{id}/bookmarks`
with `edition_id` and `sid` for a bookmark made in the text. Built in R4 P4: `POST /me/reader-settings` (any signed-in person; `reader.v1` fields and `theme`, merged over the stored value; `?sheet=reading` on the reader page). Planned for later:
`POST /hx/editions/{id}/exports/overlays`.

Details, cover, author photo and write routes (M4; the patterns are in `routes_details.go`, `routes_cover_pick.go`, `routes_author.go`, `routes_write.go` and `routes_nav.go`): `POST /books/{id}/match` (starts a lookup job), `POST /books/{id}/match/apply`, `GET /books/{id}/match/{job}/cover/{n}`, `POST /books/{id}/details/reset`, `POST /books/{id}/cover` (choose a cover), `POST /books/{id}/cover/upload`, `GET /covers/{bookID}/choices/{coverID}`, `POST /books/{id}/write`, `POST /books/{id}/write/confirm` (Looks good), `POST /books/{id}/write/undo` (Undo), `POST /people/{id}/lookup` and `GET /people/{id}/photo` (authors), `POST /admin/online` (the `online.enabled` switch), `POST /me/nav` and `POST /hx/me/nav` (sidebar setting). `GET /hx/jobs/{id}` answers by the job's kind; a lookup job needs `?book=`.

Media and system: `GET /stream/{audioFileID}` (`?download=1` marks an offline copy), `GET /offline`, `GET /sw.js`, `GET /manifest.webmanifest`, `GET /peaks/{audioFileID}` (waveform peaks, binary, 404 while being made), `GET|POST /progress/{bookID}` (JSON, session + CSRF header or
`_csrf` body field, 4 KiB, form-encoded), `GET /covers/{bookID}?w=320`,
`GET /text/{editionID}/res/{resID}`
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
| `GET /admin/duplicates`, `GET`/`POST /admin/duplicates/merge`, `POST /admin/duplicates/not-same`, `GET /admin/duplicates/merged`, `POST /admin/duplicates/merged/restore` (S4) | Copies of one audiobook, the merge confirm page and merge, "Not the same", and the folders merged away with "Show that copy again" (owner and admins; both books need `EditBook`) |
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
| `GET /api/v1/settings`, `PATCH /api/v1/settings`, `PUT /api/v1/books/{id}/speed`, `GET /api/v1/continue` | JSON app settings, per-book speed and the Continue shelf (A1, see [api.md](api.md)) |
| `GET /app/connect`, `POST /app/connect`, `GET /app/connect/done` | The Connect page of the Android app (A1, [decision 0026](decisions/0026-app-sign-in.md)): one tap makes a one-time code bound to the app's PKCE challenge; `POST /api/v1/auth/token` trades it for a device token |
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
Sign-in by code (A1, decision 0026): `POST /api/v1/auth/token` also takes `{code, verifier, name, kind: "mobile"}`, a one-time code from
`/app/connect` (migration 0031, table `device_codes`, hash only, 2 minutes, single use) and the PKCE verifier of the app that began.
A good swap binds the page session to the device (`sessions.device_id`, enforced in `Sessions.Lookup`), so ending either half ends both.
No route turns a bearer token into a cookie.
The reference is `docs/api.md`. Shipped in P3-9a (read side): `POST|DELETE /auth/token`, `GET /me`, `GET /libraries`,
`GET /books?...` and `GET /libraries/{id}/books?...` (the Library page's filters, keyset cursor, `limit` 1..120),
`GET /books/{id}` (details, chapters, files with alias URLs, permissions, own progress), `GET /search?q=`,
`GET /shelves`, `GET /shelves/{id}`, `GET /shared`, and the media aliases `GET /covers/{bookID}`,
`/stream/{audioFileID}`, `/peaks/{audioFileID}` (the same handlers as the page routes, reachable with a token).
Built in P3-9b (sync): `GET /progress?since=`, `GET|PUT /progress/{bookID}`, `POST /progress/batch`,
`GET|POST /books/{id}/bookmarks`, `GET /bookmarks?since=`, `PATCH|DELETE /bookmarks/{id}`. Trimmed from the earlier
list on 2026-11-02: notes are bookmarks with a note (`?notes=1`), so there are no `/notes` routes; `POST /sessions`
is folded into the progress body (`speed`, `listenedMs`); editions, chapter text and highlights move to the
reader phases. Under `/api/` every error, including those of the reused page helpers, is JSON:
`{"error":{"code","message","field"?}}` with codes `unauthorized` 401, `forbidden` 403, `not_found` 404, `invalid` 400,
`conflict` 409, `too_large` 413, `unsupported` 415, `rate_limited` 429, `unavailable` 503 (`drm_refused`, `not_ready`
come with the reader). A book the person may not see, and one that does not exist, give the same `not_found` body.
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
| Reading settings (R4) | `change` | `POST /me/reader-settings` (htmx: 204; plain: 303 to `next`), `hx-swap="none"` |
| Highlights and notes (R4) | selection menu | `POST /hx/books/{id}/highlights` (out-of-band touched blocks `#b-{blockID}` and `#reader-marks-count`) |

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
- Default deny: every path except `/static/*`, `/healthz`, `/setup`, `/login` and `/opds` (its own Basic sign-in with an app key, see
  decision 0025; `/kosync` joins it, signing in with KOReader's own headers, S6) needs a signed-in, enabled
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
- Reader actions: `read_text` (reader page, chapter text, images) needs `can_read` on the
  grant or share link (default off: sharing a book shares listening only); `download_text` (original
  ebook) needs `can_read` and `can_download`; upload and pairing are owner/admin.
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
- **Offline reading** ([decision 0019](decisions/0019-offline-reading.md), amends the rule above): the worker also
  answers a downloaded book's chapter parts and pictures from the person's cache (parts network-first with a
  3 s limit, pictures cache-first) and, for a navigation to `/books/{id}/read` (or `?sheet=contents`) that
  cannot reach the server, the stored reader shell. `GET /offline/books/{id}/manifest` (JSON: parts, images,
  `revision`) and `GET /offline/books/{id}/read` (the reader page without chapter text, filled in by
  `reader-offline.js`) need `ReadText` and `DownloadBook`. A reading place waits in
  `localStorage["ts.offline.reading"]` (never `ts.pending`), written before any send and sent by `offline.js`
  to `POST /reading/{id}` in slices; the time counts on the day of the save. `pages-reader.css` is in the precache.
- **Browser storage** (all wiped on sign-out or a change of person): `ts.offline` (the download index per book),
  `ts.offline.person`, `ts.offline.prefs` (Remove finished books), `ts.offline.checkedAt` (the daily finished
  check), `ts.offline.marks` (queued bookmarks), `ts.pending` (queued positions), and `ts.offline.msgs` (the
  Downloads page's sentences, kept so the book page's Download control can word its states).
- Progress, bookmarks and notes made offline wait in `localStorage` (`ts.pending`, `ts.offline.marks`) and go
  to `POST /api/v1/progress/batch` and the bookmark endpoint on reconnect. iOS storage limits and eviction are
  stated in the UI (Safari may clear downloads after a week without a visit). With `read_text`, Download will also store chapter
  fragments and images (later, offline reading).

## Merging two copies of an audiobook (S4)

The owner or an admin merges one copy into another from `/admin/duplicates` (same library only, one pair at a time,
never automatic). `library.Repo.MergeBooks` is one transaction and changes database rows only; no library file is
opened, written, renamed or deleted, and the dropped copy's folder stays on disk. Every table with a foreign key to
`books` is handled on purpose, and `TestDupEveryBookTableHandled` fails when a new one is not:

- Moved onto the kept book: `progress` (newest `client_updated_at` wins, position capped at the kept length),
  `listening_sessions`, `queue_items`, `book_settings`, `grants`, `share_links`, `ebook_files`, `ebook_not_same`,
  and through `library.MergeRowsSQL` (shared with the text-book merge) `reading_positions`, `reading_sessions`,
  `highlights`, `bookmarks`, `word_list`, `shelf_items`, `text_choices`, `merged_books`. Rows that would collide with
  the kept book's own (`OR IGNORE`) stay behind and go with the dropped row.
- Cascade with the dropped row: `audio_files`, `chapters`, `metadata_values`, `book_people`, `book_series`,
  `book_labels`, `book_sidecars`, `book_lookups`, `book_not_duplicates`. The cover moves when the kept book has none.
- `merged_books` keeps the old id answering (page and API redirect, progress and bookmark writes land on the kept
  book under its right); `merged_folders` keeps the folder out of the scan.

## Reader

Full description: [reader.md](reader.md). `internal/ebook` turns a DRM-free ebook into an immutable canonical document
with stable sentence IDs; the reader page is templ and HTMX, one chapter at a time, and keeps the person's
highlights, notes and bookmarks by sentence id and offset. There is no audio timing and no worker process.

## Desktop (P5)

`desktop/` module, Wails v3.1.0 (pinned; decision 0023), Linux on its default GTK 4 + WebKitGTK 6.0 (the `gtk3` tag is the
legacy fallback; [decision 0003](decisions/0003-newest-versions.md)). Two modes, decided in
[decision 0020](decisions/0020-desktop-shell.md) and planned in `docs/plans/m6-desktop.md`:

- **Pages always load from an http(s) origin.** Wails' asset handler (`wails://`) serves only the first-run
  picker (`internal/desktopshell`); there it drops cookies and breaks `<audio>`, so no page runs there.
- **Personal mode** runs `internal/app` in the desktop process on `127.0.0.1:<random port, then saved>`, with its
  own data folder (never the server's default). Sign-in works as on any server.
- **Connect mode** loads the typed server directly in the window. No proxy, and no rewriting of cookies or the CSP.
  The shell checks `GET /healthz` first (no credentials); a server without `shell` support is refused.
- **The window chrome** (decision 0023, plan X1). The window is created `Hidden`, GTK's titlebar is replaced by a hidden
  empty widget (`desktop/titlebar.go`, three GTK calls through purego once `NativeWindow()` exists), and the window is
  shown on the first page's `tendshelf:ready` (the picker's too) or after 3 s. GTK keeps the shadow, rounded corners and
  resize border, and no resize code exists. Never `Frameless` (square, no shadow, own resize script).
  `TENDSHELF_NATIVE_FRAME=1` keeps GTK's own bar. On Linux no menu is attached (`HideMenuBar` is empty there); Ctrl+Q is
  a key binding. Size and maximised are saved on `WindowClosing` and at shutdown in `desktop.json` (`window`; Wayland
  ignores position), clamped to the screen; `BackgroundColour` comes from `ground`, one of the four `--ground` values.
- **One native-call file.** `desktop/nativelib.go` opens GTK 4, WebKitGTK, GObject and GLib once with `dlopen`;
  `webkit.go` (cookies, downloads) and `titlebar.go` call them through purego, so `desktop/` holds no C in either build.
- **Bridge additions**: `tendshelf:window minimise|maximise|close|quit` from the session's pages and the picker only
  (table-driven, unknown verbs ignored); Go sends the fixed `ts:desktop` values `active`, `inactive`, `maximised`,
  `restored` and `tray`. The picker hears the window's state, never a media key.
- **The bar** (decision 0023, plan X2). `components.DesktopBar` is the first child of `<body>` under `data-shell="desktop"`
  only (Layout, Frame, LinkShell; the picker passes `Page.Picker` and gets no Back, Forward or More), outside `#main`
  and `hx-preserve`. `desktop-shell.js` drags the window (`wails:drag` on the first move with the button held), runs
  the window buttons and More, keeps Back and Forward by a position (`tsAt`) it writes into each history state, and
  copies the title, `#main[data-ts-crumb-href]` and the mini-player's book into the bar. Fixed and sticky layers move
  below it with `--desktop-bar` (`TestDeskBarOffsets` fails for a new full-screen layer). Budgets: `desktop.js` and
  `desktop-shell.js` count in `jsDesktopTotalGzip` (4,096 B approved; X4 measured 4,734 B and set 4,864 B, reported), not in the browser total.
  X4 adds `tendshelf:state <json>` (page to Go: title, chapter, left, playing, theme, canAdd, stop), the tray, notifications and Keep playing in `desktop/`.
- The Android app has no service worker either (`/sw.js` answers 404 there, decision 0026), so it stores a downloaded book's text itself
  and `shouldInterceptRequest` plays the worker's part (A9, [decision 0027](decisions/0027-offline-script-in-the-app.md)): the same
  addresses 0019 lists, kept under `noBackupFilesDir/offline/text/<book>/` and `static/`, answered only for the exact stored origin, only
  for GET, only while `GET /healthz` fails, with the stored `Content-Type` and CSP. It fetches with the WebView's session cookie; the
  device token stays under `/api/v1/`. `offline.js` loads in the app (reading flush, bookmark queue) with its worker and download code off by `data-shell`.
- `ts_shell=desktop` marks the window in both modes (`data-shell`); there is no service worker in it, and no
  Wails runtime in pages. One bridge: `tendshelf:` messages to Go, a `ts:desktop` event back.

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

Ebook reading (phase R), **approved by the operator on 2026-11-03**; each
still gets its `NOTICES.md` row, with its licence read from the release, in the commit that adds it, and the
PDF reader was chosen on fixtures in R1 (E2):

| Module / file / runtime | Why | Note |
| --- | --- | --- |
| `golang.org/x/net/html` (**added in M5 R1 E1**, v0.61.0, also `html/charset`) | HTML5 tokenizing of EPUB XHTML, HTML files and MOBI/KF8 content (R1) | BSD-3; Go team |
| `golang.org/x/text` (`unicode/norm`, `encoding/...`) (**added in M5 R1 E1**, v0.43.0) | NFC normalisation for matching (R1); charset decoding for FB2, HTML and text (R1) | BSD-3; Go team |
| `github.com/ledongthuc/pdf` (**added in M5 R1 E2**, v0.0.0-20261020125853-5e46805f718e) | PDF text layer (R1): per-glyph position and size, ToUnicode CMaps, xref streams and incremental updates passed the four spike fixtures | BSD-3 (The Go Authors; licence read from the release); no context support, so the parser checks the deadline between pages; encrypted files are refused before it reads text. Limit: glyph widths of standard fonts without `/Widths` read as 0 |
| Font vendored to `internal/web/static/fonts/` (woff2): OpenDyslexic | The one extra reader font (R4); the reader otherwise uses the design's Fraunces and Fira Sans | SIL OFL 1.1 (read from the v0.91.12 tag; built in R4 P4); `NOTICES.md` row |

Considered and not chosen: `github.com/clipperhouse/uax29` (sentence segmentation; own rules handle
abbreviations better and stay versioned), `github.com/yuin/goldmark` (Markdown; only structure is
needed), a speech-recognition runtime (decision 0018).

Later, **approved by the operator on 2026-11-03** (each with its `NOTICES.md` row in the commit that adds it): `github.com/go-webauthn/webauthn` (passkeys), `github.com/fsnotify/fsnotify`
(folder watcher), `github.com/wailsapp/wails/v3` (desktop module only), an OpenID Connect client.

Android, **approved by the operator on 2026-11-05** ([decision 0024](decisions/0024-android-app.md)), `android/` only, each with its `NOTICES.md` row and pinned by Gradle dependency verification: Kotlin (through AGP), `androidx.media3` (exoplayer, session, datasource), `androidx.webkit`, `androidx.core`, and `androidx.work` if downloads need it. Every other module needs approval. Gradle fetches at build time only.
