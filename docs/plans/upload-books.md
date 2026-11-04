# Adding books by upload (plan, 2026-11-04)

Decision [0015](../decisions/0015-upload-into-library.md) (read it first: invariants 1-8, decisions 1-13). Board
`docs/design/boards/UploadBooks.html`. Three packages in order: **U1 Go** (builder, sonnet), then **U2 page and JS**,
then **U3 zip and several books** (optional). U2 cannot merge before the operator answers 0015's JS budget decision.

Reused, never duplicated: `scan.library` job and `walk`/`GroupFiles`; `media.Classify`, `Tools.Probe`, `QuickHash`,
`FileSHA256`, `FreeBytes`; `metadata.FromTags`/`ParseBookName`; jobs runner and `jobViews`; `api.Write`/`api.Error`;
`db.NewID`, `db.Audit`; `components.ErrorToast`/`ToastOOB`; the lazy-module pattern of `offline.js`; `testutil/cdp`
and `testutil/wav`; the route-matrix tests (`routes_matrix_*_test.go`).

---------------------------------------------------------------------------------------------------------------
## U1: Go (staging, chunk API, placement, scoped import, setting, ledger, remove) — about 200 tool calls

### Invariants (U1)
1. The only writes into a library folder are in `internal/upload/place.go` and `remove.go`; both act only on paths
   under `uploads.folder_id`'s path, built from `SafeName` segments, and assert `within(root, p)` before any call.
2. Every created path has an `upload_paths` row inserted (`planned`) before `os.Mkdir`/`os.OpenFile(O_CREATE|O_EXCL)`.
   `EEXIST` deletes that row and moves on (another name); never retries the same name with a weaker flag.
3. `bytes_received` is advanced only after `f.Sync()`, by `UPDATE ... WHERE id=? AND bytes_received=?`.
4. No `os.Rename` onto an existing path: `Lstat` must return `fs.ErrNotExist` first. No `os.RemoveAll` except on the
   hidden `.tendshelf-upload-<uploadID>` folder after `Lstat` proves it is a directory (not a link) with that exact name
   in the recorded parent, and on `<data>/uploads/incoming/<uploadID>`.
5. Refusal for members, guests, disabled people and other people's uploads is 404; CSRF is checked by the existing
   middleware (`X-CSRF-Token`); `PUT` bodies are `application/octet-stream`, never parsed as a form.

### Steps
1. `internal/db/migrations/0014_uploads.sql` (if 0014 is taken on `main`, report `PLAN GAP`, do not renumber):
   `ALTER TABLE libraries ADD COLUMN upload_folder_id TEXT REFERENCES library_folders(id) ON DELETE SET NULL;`
   then `uploads`, `upload_files`, `upload_paths` exactly as in `docs/architecture.md` (STRICT; `uploads.state` CHECK;
   `upload_paths.kind` CHECK IN ('dir','file'); `state` CHECK IN ('planned','staged','present','removed','kept');
   indexes `uploads_person (created_by, state)`, `upload_paths_upload (upload_id)`, partial unique
   `upload_paths_live ON upload_paths(path) WHERE state IN ('planned','staged','present')`).
2. `internal/db/migration_0014_test.go`: from the 0013 fixture (rows survive, column NULL), from empty, FK SET NULL when
   the folder is removed, the partial unique index (a `removed` path can be planned again; two `present` cannot).
3. `internal/share/share.go`: `AddBooks Action = "books.add"` in `knownActions`. No grant case (members false).
4. `internal/library`: `Library.UploadFolderID string` (scan it in `repo.go scanLibrary` and every SELECT of
   libraries); `uploads.go`: `(r *Repo) SetUploadFolder(ctx, libraryID, folderID string) error` (`""` = off;
   `ErrNotFound` unless the folder belongs to the library, checked in the same UPDATE's WHERE); `(r *Repo)
   BookRootsIn(ctx, folderID string) ([]string, error)`.
5. `internal/scan`: export `IsDiscDir` and `SkipName` (rename, keep callers). `ScanPayload` gains
   `FolderID string \`json:"folder_id,omitempty"\`` and `Subdir string \`json:"subdir,omitempty"\`` (slash form).
   `handleScan` calls `ScanSubtree(ctx, lib, folder, subdir, t.Fence)` when `FolderID != ""`. `ScanSubtree` reuses
   `ScanLibrary`'s body through an internal `scanScoped(ctx, libraryID, scope *scope, guard)`: walks only
   `folder.Path/subdir` (unavailable or empty: nothing changes, `FoldersSkipped=1`), rewrites each `Found.RelDir` to
   `path.Join(subdir, RelDir)`, sets `plan.scope` so step 2 of `apply` only takes vanished rows under the scope root.
   Finishers and settle followers run as now. `ScanLibrary` behaviour is byte-for-byte unchanged (its tests pass).
6. `internal/upload/upload.go`: `type Config struct{ DataDir string; ChunkSize int64 (default 8<<20); MaxFile int64
   (20<<30); MaxBook int64 (50<<30); MaxFiles int (1000); MaxReceiving int (3); Margin int64 (1<<30); Now func()
   time.Time }`; `New(st *db.Store, repo *library.Repo, tools media.Tools, enq scan.Enqueuer, log) *Service`;
   `Register(r *jobs.Runner)` registers `KindPlace = "upload.place"` (Concurrency 1, MaxAttempts 3);
   `Run(ctx)` sweeps hourly (first after 1 min). Per-upload `sync.Mutex` map for chunk writes.
7. `manifest.go`: `Create(ctx, user, libraryID, Manifest{Label string; Files []FileIn{Path string; Size int64;
   ModifiedMs int64}})`. Checks in this order, each a typed `*Error{Status, Code}`: library takes uploads (409
   `uploads_off`); files 1..MaxFiles, sizes 1..MaxFile, sum <= MaxBook (413 `too_large`); every path: UTF-8, no NUL,
   no `\`, <= 1,024 bytes, 1..8 segments, no segment `""`, `.`, `..`, no duplicates after `strings.ToLower` (422
   `bad_path`, field = the path); any `media.KindProtected` (422 `copy_protected`, field = path); no `KindAudio`
   (422 `no_audio`); receiving uploads of this person < MaxReceiving (429 `too_many`); staging free space (507
   `no_space`, message "The server's data folder is full."); library folder free space (507 `no_space`, "The library
   folder is full."). Then one transaction inserts `uploads` (`receiving`) and `upload_files`; mkdir 0700 staging.
   Left-out files (not audio, picture or sidecar; `.` names; `SkipName`) are not rows: `skipped` counts them.
8. `chunk.go`: `WriteChunk(ctx, user, uploadID, fileID string, offset int64, sum string, body io.Reader)`: 404 unless
   creator; 410 `expired` for `expired`/`cancelled`; 409 `not_receiving` otherwise; 409 `wrong_offset` with the
   server offset when `offset != bytes_received`; read at most ChunkSize+1 (413 `too_large`), and exactly
   `min(ChunkSize, size-offset)` bytes (else 422 `short_chunk`); `sum` hex SHA-256 when present must match (422
   `checksum`); open `.part` (`O_CREATE|O_WRONLY`, 0600), `Truncate(offset)`, `WriteAt`, `Sync`; `ENOSPC` gives 507
   `no_space` and truncates back; then the conditional UPDATE of both offsets and `updated_at`.
9. `place.go` (job handler, payload `{upload_id}`), steps in order, progress `{step}` at each:
   a. `recoverPlacement` (step 10). b. state `placing`. c. sizes on disk equal declared, else `failed`/`incomplete`.
   d. `Tools.Probe` every audio file; any `Encrypted` or `media.CodeDRM`: delete staging, state `refused`,
   `refused_file`, audit `upload.refuse`, done. e. names: `FromTags` of the first audio file by `rel_path`, then
   `ParseBookName(label)`, then "Unknown author"/"Untitled book". f. `layout()` (step 11). g. free space in the target
   (507-equivalent job error `no_space`, permanent, staging kept, state `failed`). h. create missing ancestors one by
   one (`dir` rows `planned` then `present`). i. create `.tendshelf-upload-<id>` (0755), store `temp_path`. j. per file
   (book-relative path from `rel_path` minus the first segment when the label is a folder, segments through
   `SafeName`, in-book collisions get " (2)" before the extension): `file` row `staged` with the final path; move with
   `os.Link`+`os.Remove` when `sameFS` (fall back to copy on error), else copy with `O_EXCL`, `Sync`, re-read for
   SHA-256; record size, mtime_ns, quick hash, sha256 (copy only). k. `Lstat(final)` must be not-exist (else back to f
   with the next suffix), `os.Rename(temp, final)`; one transaction: rows `staged`→`present`, `book_path`, clear
   `temp_path`, state `placed`, audit `upload.place`. l. enqueue `scan.library` `{library_id, folder_id, subdir}`,
   store `scan_job_id`. m. remove staging. Every DB write in the handler begins with `t.Fence`.
10. `recover.go` `recoverPlacement`: `planned` rows whose path is missing: delete row; existing `planned` dir: `kept`.
   `temp_path` set: `RemoveAll` it (invariant 4), delete its `staged` rows. Book folder exists with `staged` rows:
   check sizes, then mark `present` and continue at step l.
11. `names.go`: `SafeName(s, fallback string) string`: NFC not applied; control chars dropped; `: ` → ` - `, other `:`
   → `-`; `/ \ * ? " < > |` → `_`; spaces collapsed; leading `.` and spaces and trailing `.` and spaces trimmed;
   Windows names `CON PRN AUX NUL COM1-9 LPT1-9` (any case, with or without extension) get `_` appended; `SkipName`
   matches and `IsDiscDir` matches get the prefix `Book `; cut to 120 bytes on a rune boundary; empty → fallback.
   `layout(root, author, series, seq, title, roots)` tries `A/S/NN - T`, `A/T`, `A - T`; a candidate is taken when no
   existing ancestor is a link or non-directory, is inside or equal to a book root, or holds audio directly; the leaf
   gets " (2)".." (99)" when it exists; all fail: `failed`/`layout`. `within(root, p)` after `filepath.Clean`.
12. `remove.go`: `Removable(ctx, bookID) bool`; `Remove(ctx, user, bookID) (RemoveResult{Deleted, Kept []string})`:
   unchanged files deleted (`removed`), changed ones `kept`; `dir` rows `present` removed deepest first with
   `os.Remove` (non-empty: `kept`); when no audio file is left, one transaction deletes the book's `audio_files` and
   `DeleteEmptyBook`; else enqueue the scoped scan. Audit `upload.remove`.
13. `sweep.go`: `receiving` with `updated_at` older than 24 h → `expired` and staging removed; staging folders without
   a row and older than 24 h removed; ended `uploads` rows older than 30 days deleted.
14. `internal/web/routes_upload.go` `registerUploads(mux)` (call in `server.go`; `Options.Uploads *upload.Service`):
   `POST /libraries/{id}/uploads` (JSON body <= 1 MiB → 201 `{id, chunk_size, files:[{id,path,offset}], skipped}`),
   `GET /uploads/{id}` (200 same shape plus `state`, `job_id`), `PUT /uploads/{id}/files/{fileID}?offset=` (415
   `bad_type` unless octet-stream; 200 `{offset}`; read deadline 120 s via `http.ResponseController`), `POST
   /uploads/{id}/finish` (409 `incomplete`; 202 `{job_id}`; also from `failed` with staging intact), `POST
   /uploads/{id}/cancel` (204; only `receiving`/`failed`), `POST /admin/libraries/{id}/uploads` (form `folder_id`;
   owner only, `share.ManageServer`; write check `.tendshelf-write-check-<rand>`; 422 re-renders `/admin/libraries`
   with the error; 303 `/admin/libraries#uploads-h`; audit `library.uploads`), `POST /books/{id}/remove` (303 to the
   book's library). Errors through `api.Error`; `wrong_offset` adds top-level `"offset"`.
15. `cmd/tendshelf`: build the service, `Register`, `go uploads.Run(ctx)`, pass to web `Options`.
16. Docs: `docs/api.md` is not touched (these are not `/api/v1`); roadmap status line.

### Acceptance tests (U1)
- `internal/upload/names_test.go` table: `../x`, `a/b`, `CON`, `con.txt`, `.hidden`, `CD1`, `@eaDir`, `x:y`, 300-byte
  UTF-8, empty, control chars; `layout` with an ancestor holding audio, a link ancestor, a file at the name, (2)..(99).
- `manifest_test.go` table: every error code above, including traversal paths (`../a.mp3`, `a/../../b.mp3`, `/etc/x`,
  `a\\b.mp3`, `a/./b.mp3`, NUL), duplicates by case, `.aax` among mp3s.
- `chunk_test.go`: in order; wrong offset 409 with offset; resend of an applied chunk 409; bad checksum leaves offset;
  oversized and short chunks; **resume after interrupt** (body reader errors half-way: offset unchanged, `.part`
  truncated on the next write); **disk full** (an injected writer returning `syscall.ENOSPC`) gives `no_space`.
- `place_test.go` with the fake ffprobe (`media/mediatest`): placed layout and ledger; **never overwriting** (target
  pre-created as a file, as a non-empty folder, as an empty folder, and a pre-existing file in an ancestor: each untouched,
  byte- and mtime-identical); **DRM refusal** (fake probe reports encrypted: nothing under the library folder, staging
  gone); crash injection after each of h, j, k (recovery finishes or cleans, no stray paths); scoped scan adds the book
  and marks nothing else missing (a sibling book's folder renamed away stays live).
- `remove_test.go`: unchanged files deleted, changed file kept, a file the user added to the folder kept, folders
  removed only when empty, book rows deleted only when no audio is left.
- `scan` tests: `ScanSubtree` disc folders, untouched siblings, unavailable subtree.
- `internal/web/routes_upload_test.go` (httptest): owner and admin succeed; member, guest, disabled, signed out (and
  another admin's upload) get 404; missing CSRF 403; setting by admin 404; route-matrix rows added.
- Gates: `make check`, `make build`, `go test -race ./internal/upload/... ./internal/scan/... ./internal/web/...`.

### Out of scope (U1)
Pages, templ, CSS, JS (U2); zip and several books (U3); `/api/v1` routes; writing into existing books.

---------------------------------------------------------------------------------------------------------------
## U2: page, fragments, uploader module, captures — about 200 tool calls

### Invariants (U2)
1. `upload.js` is never loaded on another page; nothing else imports it. No inline script or style, no `hx-on`.
2. Every word comes from the server (`upload_copy.go`, the strings in 0015 "Copy"), passed in `data-` attributes or
   `<template>` elements, like `downloads.js`.
3. Each queue row is a live region (`role="status"`, polite); errors use `role="alert"`; 44 px targets; no colour alone
   (refused rows carry the shield icon and the word "Not added").

### Steps
1. `internal/web/static/js/shell.js`: generic loader `loadPageModules()`: for each `[data-ts-module]` in `#main`
   whose module was not imported, `import(el.dataset.tsModule).then((m) => m.mount(el))`; run at start and on
   `htmx:after:swap`; a module already imported is mounted again on a fresh element. About 8 lines with comments.
2. `internal/web/static/js/upload.js` (ES module, written for reading): `export function mount(root)`. Picks via two
   `<input type="file">` (`multiple`; second also `webkitdirectory`); client checks: protected extensions refuse the
   book at once (row "Not added ..."), left-out types counted; `POST` manifest; per file sequential `PUT` of
   `file.slice(offset, offset + chunk)` with `X-CSRF-Token` from `<body hx-headers>` and `X-Chunk-SHA256` when
   `crypto.subtle` exists; one request in flight; Pause aborts with `AbortController`; network errors retry after 1, 2,
   4 ... 30 s; 409 `wrong_offset` jumps to the server offset; 507/422/413 stop the row with the server's message;
   after the last byte `POST finish`, then `htmx.ajax("GET", "/hx/uploads/" + id, {target: row, swap: "outerHTML"})`.
   Unfinished uploads listed by the page (`receiving`) offer "Choose files again": matched by path, size and
   modified time, then continue from `GET /uploads/{id}` offsets. Progress text at most once a second; time left from
   a 20 s moving average.
3. Budgets in `internal/web/static/motion_test.go`: `"upload.js": {5 * 1024, 32 * 1024}` and the shell.js and total
   change the operator chose (0015 decision 1). Measure and report; never minify.
4. `internal/web/pages/upload.templ` `AddBooks(v AddBooksView)`: title, lead with the folder path, the library chooser
   (links) when several, the picker group `data-ts-module={hashed upload.js}`, the hint, the queue `<ul>` of
   `components.UploadRow` for this person's uploads of the last 24 h, the no-JS sentence in `<noscript>`.
5. `internal/web/components/upload_row.templ` `UploadRow(v UploadRowView)`: states receiving (bar, sent, Pause or
   Resume), interrupted (Choose files again, Cancel upload), checking, placing, adding (polls `/hx/uploads/{id}` every
   2 s), added (path, Open book), refused, failed (Try again posts finish). `GET /hx/uploads/{id}` renders it; the
   `jobViews` entry `upload.KindPlace` renders the same row.
6. `internal/web/routes_upload_pages.go`: `GET /libraries/{id}/add`, `GET /hx/uploads/{id}`, `GET /books/{id}/remove`
   (confirm page listing the files), the Library page header button (`pages/library.templ` `ts-lib-head`), the admin
   section "Adding books from this device" (`pages/admin_libraries.templ`, `id="uploads-h"`, a `ts-select` per
   library for the owner, read-only text for admins), and "Remove this book" in the book page's More menu.
7. `internal/web/static/css/pages-upload.css` from the board's proposed block (`@container frame` → `@media`), its
   `TestCSSBudget` row (measure; propose 1.5 KB), `docs/copy-additions.md` rows.

### Acceptance tests (U2)
- httptest: page 404 for member, guest, a library without uploads; the admin section renders a select for the owner
  and text for an admin; `/hx/uploads/{id}` 404 for another person; each row state renders its words.
- Browser (`TENDSHELF_TEST_BROWSER`, `testutil/cdp`): `upload_browser_test.go` writes a 3 s `wav.Write` file and a
  generated `.aax` stub under the run dir, sets them with `DOM.setFileInputFiles`, server `ChunkSize` 64 KiB: the wav
  becomes a book under `Unknown author/...` and the row ends "Added to"; the `.aax` row says "Not added" and the server
  saw no `PUT`; pausing and resuming finishes the same upload; throttled offline (CDP `Network.emulateNetworkConditions`)
  then online resumes without restarting.
- Captures at 390, 768, 1024, 1440 of the page (empty, queue with every state) and the admin section, compared with
  the board; `make visual` stays repeatable (seeded ids).
- Gates: `make check`, `make build`, `TestJSBudget`, `TestCSSBudget`, the browser tests above.

### Out of scope (U2)
Zip, several books, drag and drop (U3); a no-JavaScript upload form (0015 decision 12).

---------------------------------------------------------------------------------------------------------------
## U3 (optional): zip and several books — about 150 tool calls

### Invariants (U3)
1. An archive is opened only with `archive/zip` from the staged file; nothing is extracted outside the hidden book
   folder; entry names pass the same checks as manifest paths plus: no absolute name, no drive letter, `\` treated as
   a separator before checking, no link entries (`Mode()&fs.ModeSymlink`), no encrypted entries (flag bit 0).
2. Nested `.zip` (and `.rar`, `.7z`, `.tar*`) entries are never opened; they count as left out.
3. Limits: 2,000 entries; total uncompressed <= MaxBook and <= free space; per entry the bytes actually written are
   counted through `io.LimitReader(size+1)` and a larger entry fails the book (`zip_too_large`); a compression ratio
   over 200:1 for any entry fails it (`zip_bomb`).

### Steps
1. `internal/upload/zip.go`: `extract(ctx, zipPath, dst string, put func(rel string, r io.Reader) error)` used by
   `place.go` step j when the upload's only file is a `.zip` (manifest allows `.zip` only as the single file of an
   upload). Codes: `zip_bad`, `zip_encrypted`, `zip_too_large`, `zip_bomb`, `bad_path`; DRM rules apply to the
   extracted audio exactly as to uploaded files (probe after extraction, into staging, before placement).
2. `upload.js`: several books at once: each `.zip` and, with "This folder holds several books" ticked, each top-level
   subfolder becomes its own manifest; loose files at the top of such a folder are listed as left out. Desktop drag and
   drop (`DataTransferItem.webkitGetAsEntry`, recursive read, same rules). Uploads run one book at a time, in order.
3. Copy rows: "{n} books" header, "This folder holds several books", "Archives inside archives are left out."

### Acceptance tests (U3)
- `zip_test.go` table with archives generated in the test: `../evil.mp3`, `/abs.mp3`, `C:\x.mp3`, `a\..\..\b.mp3`,
  symlink entry, encrypted flag, nested zip, 2,001 entries, header size lying low, ratio 1000:1, `.aax` inside;
  each leaves the library folder byte-identical.
- Browser: a folder of two subfolders gives two rows and two books; a zip gives one book.
- Gates as U1 and U2.

### Out of scope (U3)
Other archive formats, password-protected zips, splitting one file into several books.
