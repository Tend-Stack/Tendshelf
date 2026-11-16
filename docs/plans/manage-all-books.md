# Plan: "already in the library" check + remove any book (decision 0045)

## 1. Invariants
- I1 Only `internal/upload` writes, renames or deletes a library file. The new remove path lives there (`removeany.go`).
- I2 A hand-added file is touched only when it is a live `audio_files` / `ebook_files(origin='library')` row of the book,
  or a listed "extra" (rule D6), and only after Lstat (regular file, not a link) + size + mtime + quick hash match the scan's record.
- I3 All-or-nothing: every file is verified before the first move; a failed move rolls back every earlier move; the
  removal and its paths are recorded in the database before anything on disk changes; a crash mid-move is rolled back.
- I4 Nothing is destroyed at confirm time: files go to `<folder>/.tendshelf-removed/<removalID>/<path below folder>` (one
  rename on the same filesystem), are put back on request, and are deleted only by purge after 30 days or "Delete now".
- I5 The person confirms an exact plan; the POST carries the plan's hash and is refused if the disk no longer matches it.
- I6 Owner and admin only for hand-added files (`share.RemoveLibraryFiles`); a contributor's rules (0029) do not change.
- I7 The duplicate check never names a book the uploader cannot list (library.Scope in SQL), looks only in the target
  library, and uses the Duplicates rules (`dupSigSQL` pairs, `closeLengths`, `textmatch.Norm`), not a second rule.
- I8 Existing upload-placed removal (Remove, RemoveWhole, parts) is unchanged; the new path is offered only where it is not.

## 2. Decisions (one-line reasons)
- D1 Check twice: pre-send (browser quick hash in the manifest) and after staging (server quick hash + tags), because
  pre-send saves gigabytes but needs `crypto.subtle` (secure context only) and a .zip hides its files; stage 2 is authoritative.
- D2 "Same file" = equal size + quick hash. No full SHA-256 of library files: the result only warns, never deletes.
- D3 Tiers: `same_files` (every kept audio/ebook file of the upload matches a live file of one book), `some_files`
  (at least one matches), `same_details` (stage 2 only: DupSameDetails rule on staged tags; title, first author and
  first narrator all present, lengths `closeLengths`). Best tier wins; at most 3 books reported.
- D4 A held upload is `state='failed'`, `error_code='already_in_library'` plus new columns; no new state (that would
  rebuild `uploads`). "Add anyway" = Finish with `add_anyway=1`; "Do not add" = existing Cancel (staged bytes deleted);
  failed-upload expiry in `sweep.go` cleans abandoned ones.
- D5 Add anyway: owner/admin for every tier; contributor only for `same_details` (operator decision E4).
- D6 Extras moved with a hand-added book: only when `book.root_path` is a directory strictly inside the folder holding no
  other book's root or recorded file; regular non-hidden files in it with extension in
  {jpg,jpeg,png,webp,gif,opf,nfo,json,txt,cue,m3u,m3u8,log,sfv,md5}, each ≤ 50 MiB, checked by size + mtime. Anything else
  stays, and then the folder stays. Empty subfolders of the root and the root itself are rmdir'd (never above the root).
- D7 Holding folder over delete or OS trash: one rename, Undo works, no dependency, scanner already skips dot names
  (`scan/walk.go:41`). Retention constant `RemovedKeepDays = 30`.
- D8 "Remove from Tendshelf, keep the files" = a `removals` row with `mode='keep_files'`; the scanner skips its root while
  `state='held'`. Reuses one table instead of a second ignore table; merged_folders cannot hold it (`to_id` NOT NULL FK).
- D9 Moving runs as job `upload.remove_files` (Concurrency 1, MaxAttempts 1); the request only plans and records.
- D10 Confirmation = required checkbox + button (Rosa voice), not typed text; holding folder makes it recoverable.
- D11 Scanner side (upload first, hand copy later): no blocking; owner/admin see a notice on both book pages linking to the
  existing merge preview. Duplicates page stays the place to resolve. The incident's pair is resolved today by Merge.

## 3. Data — migration `internal/db/migrations/0045_removals.sql` (next free: 0044 is last)
```sql
ALTER TABLE uploads ADD COLUMN dup_book_id TEXT;
ALTER TABLE uploads ADD COLUMN dup_tier TEXT CHECK (dup_tier IS NULL OR dup_tier IN ('same_files','some_files','same_details'));
ALTER TABLE uploads ADD COLUMN add_anyway INTEGER NOT NULL DEFAULT 0 CHECK (add_anyway IN (0,1));
CREATE TABLE removals (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_id TEXT NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE, book_id TEXT NOT NULL, title TEXT NOT NULL,
  root_path TEXT NOT NULL, mode TEXT NOT NULL CHECK (mode IN ('files','keep_files')),
  state TEXT NOT NULL CHECK (state IN ('planned','moving','held','restored','purged','failed')), error_code TEXT,
  plan_hash TEXT NOT NULL CHECK (length(plan_hash) = 64), files INTEGER NOT NULL DEFAULT 0, bytes INTEGER NOT NULL DEFAULT 0,
  job_id TEXT, removed_by TEXT REFERENCES users(id) ON DELETE SET NULL, created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL, purge_after INTEGER) STRICT;
CREATE UNIQUE INDEX removals_active_book ON removals(book_id) WHERE state IN ('planned','moving');
CREATE INDEX removals_library ON removals(library_id, mode, state);
CREATE TABLE removal_paths (removal_id TEXT NOT NULL REFERENCES removals(id) ON DELETE CASCADE, idx INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('file','extra','dir')), path TEXT NOT NULL, held_path TEXT,
  size INTEGER NOT NULL DEFAULT 0, mtime_ns INTEGER NOT NULL DEFAULT 0, quick_hash TEXT,
  state TEXT NOT NULL CHECK (state IN ('planned','held','kept','restored','purged')),
  PRIMARY KEY (removal_id, idx)) STRICT, WITHOUT ROWID;
```
IDs `db.NewID("rmv")`. Times Unix ms. `purge_after` = held time + 30 d for `files`, NULL for `keep_files`.
Plan hash: SHA-256 hex over sorted lines `kind|path|size|mtime_ns\n` (files from the DB record, extras and dirs from Lstat).

## 4. Endpoints and service API
- Manifest (`POST /libraries/{id}/uploads`): each file gains optional `"quick"` (64 hex, `sha256Re`); body gains
  `"add_anyway": bool`. Match → 409 `already_in_library`, JSON adds `"match": {"book_id","title","href","tier","may_add_anyway"}`.
  `add_anyway` by someone D5 forbids → 403 `not_allowed`. Bad `quick` → 422 `bad_checksum`.
- `POST /uploads/{id}/finish` form/JSON `add_anyway=1`: sets `uploads.add_anyway=1` (same D5 check) then Finish.
- `GET /books/{id}/remove` (existing): when `!parts.Whole && !combined` and `Can(RemoveLibraryFiles)` → new confirm page
  (`pages.BookRemoveAny`); else today's behaviour (contributor still 404).
- `POST /books/{id}/remove-files` form `plan`, `understand=1`, `mode=files|keep_files` → 303 `/removals/{rid}`.
  Errors (toast fragment for htmx, page otherwise): 409 `plan_changed`, 409 `changed_since_scan`, 409 `busy`,
  422 `not_confirmed`, 409 `read_only`, 409 `not_removable` (no live recorded file / folder gone / root is a link), 404.
- `GET /removals/{rid}` page; `GET /hx/removals/{rid}` status fragment (`hx-trigger="every 2s"` while planned/moving).
- `POST /removals/{rid}/restore` ("Put back"), `POST /removals/{rid}/purge` ("Delete now", state held, mode files only).
- `GET /admin/libraries/{id}/removed` list of held removals of both modes (filtered by Libraries scope in SQL).
- Service (`internal/upload/removeany.go`): `PlanRemoval(ctx, bookID) (RemovalPlan, error)`;
  `StartRemoval(ctx, userID, bookID, planHash, mode string) (removalID string, error)`; job handler `handleRemoveFiles`;
  `RestoreRemoval(ctx, userID, rid)`; `PurgeRemoval(ctx, userID, rid)`; `Removal(ctx, rid)`; `HeldRemovals(ctx, libraryID)`.
  `RemovalPlan{Files, Extras []PlanFile{Path string; Size int64}; Dirs []string; Staying int; Bytes int64; Hash string;
  Listeners, Bookmarkers int; RootIsFolder bool}`.
- Library (`internal/library`): `FileMatches(ctx, sc Scope, libraryID string, pairs []SizeQuick) ([]FileMatch, error)`
  and `DetailMatches(ctx, sc, libraryID, title, author, narrator string, durMs int64) ([]string, error)` in
  `duplicates.go`, both via `visible(sc, true)`; `SameAudioCopies(ctx, sc, bookID) ([]string, error)` (dupSigSQL, same
  library, minus `book_not_duplicates`); `KeptOutRoots(ctx|tx, libraryID) map[string]bool` (removals keep_files held).

## 5. Steps (packages; P2 and P3 run in parallel after P1; P4 after P3; P0 first by lead/scribe)
P0 docs (scribe, exact text in section 7): `AGENTS.md`, `docs/decisions/0015-upload-into-library.md`,
`docs/decisions/0029-contributors.md`, `docs/vision.md`, new `docs/decisions/0045-manage-every-book.md`, `docs/roadmap.md` row.
P1 remove service + migration (Go):
1. `internal/db/migrations/0045_removals.sql` as section 3; migration test beside the 0044 one (from 0044 fixture and empty;
   an `uploads` row survives with `add_anyway=0`).
2. `internal/share/share.go`: `RemoveLibraryFiles Action = "book.remove_files"`, in `knownActions`, owner and admin only
   (same branch as `WriteFiles`); not in the contributor book branch at :211.
3. `internal/upload/root.go`: `MkdirAll(abs, perm)` via repeated `Mkdir` inside the root.
4. `internal/upload/removeany.go`: PlanRemoval (Lstat only; record vs disk size/mtime differ → `changed_since_scan`;
   missing files skipped and listed; symlinked root or file → `not_removable`). StartRemoval: re-plan, hash compare, one tx
   inserting `removals(planned)` + `removal_paths(planned)`, enqueue. Job: state moving; verify every file (Lstat regular,
   size+mtime, quick hash vs record for `file`, size+mtime for `extra`); any mismatch → failed `changed_since_scan`, nothing
   moved. Mkdir `.tendshelf-removed/<rid>` (EROFS/EACCES/EPERM → failed `read_only`); for each: MkdirAll parent in holding,
   `Rename`, mark held (EXDEV or any error → rollback all held in reverse, failed `cannot_move`). Then one tx:
   `DeleteBookAndFiles`, delete moved `ebook_files` rows, mark matching `upload_paths` rows `removed`, delete `upload_books`
   for the root (same SQL as remove.go:203), removals → held, purge_after, audit `book.remove_files`. Then rmdir dirs deepest
   first (kept on ENOTEMPTY). A job that starts on a `moving` removal rolls it back and fails it `interrupted`.
   keep_files: request-only tx (no job): removals row state held + `DeleteBookAndFiles` + audit `book.remove_keep_files`.
   Restore: per file, original absent (Lstat) → MkdirAll + Rename back, else kept in holding and named; state restored;
   keep_files restore just sets restored; then queue a scoped scan as remove.go:217-222; audit `removal.restore`.
   Purge: `RemoveAll` of `.tendshelf-removed/<rid>` through lroot, state purged, audit `removal.purge`.
5. `internal/upload/upload.go`: register `upload.remove_files` next to KindPlace.
6. `internal/upload/sweep.go` Sweep: purge held `files` removals past purge_after; roll back `moving` removals older than 1 h.
7. `internal/scan/scan.go` (:200, :251-254, :374): treat `KeptOutRoots` exactly like merged roots; count in new
   `BooksKeptOut int json:"books_kept_out"`.
8. Tests (section 6, P1 list).
P2 remove pages (web; touches only these): `internal/web/routes_remove_any.go` (new routes), `internal/web/remove_any_copy.go`,
`internal/web/pages/book_remove_any.templ`, `internal/web/pages/removal.templ`, `internal/web/pages/admin_removed.templ`,
`internal/web/routes_upload_pages.go` getBookRemove branch, `internal/web/book_more_menu.go` entry "Remove this book"
when `Can(RemoveLibraryFiles)` and not removableFor, `internal/web/routes_matrix_upload_test.go` rows, `bundle.css` `ts-remove-any__*`.
P3 duplicate check (Go): `internal/library/duplicates.go` (FileMatches, DetailMatches, SameAudioCopies, exported
`SameDetails(a, b DupKey) bool` used by groupDuplicates too), `internal/upload/manifest.go` (Quick, AddAnyway, check after the
copy-protected loop), `internal/upload/upload.go` (Error.Match, Finish add_anyway, `Config.ScopeFor func(ctx, userID string)
(library.Scope, bool)`), `internal/upload/place.go` (after `inspect`, before `build`, skipped when `add_anyway=1`: quick hash
each staged part, sum probe `DurationMs`, tags `title`/`artist|album_artist`/`composer|narrator` (verify keys in
`internal/metadata`), match → `jobFail(..., "already_in_library", <message>, true, nil)` and set dup columns).
P4 duplicate UI: `internal/web/static/js/upload.js` (quick hash: `crypto.subtle` over 8-byte BE size + first 1 MiB + last
1 MiB, whole file under 2 MiB; skipped without subtle or for a .zip), `internal/web/components/upload_row.templ` state
`already` + template row, `internal/web/upload_dup_copy.go`, `internal/web/routes_upload.go` (pass match JSON, finish flag),
book page notice (owner/admin, `SameAudioCopies`, link `/admin/duplicates/merge?keep=<older>&drop=<newer>`).

## 6. Copy (exact; sentence case, no exclamation marks)
Upload: "This book is already in %s: %s." / "Some of these files are already in %s." / "This looks like %s, already in %s."
Buttons "Open it", "Add anyway", "Do not add". Contributor on `same_files`: "Ask an admin if you need a second copy."
Confirm title "Remove this book and its files?". Body "Tendshelf found this book in the library folder. These files move
to a holding folder inside it, and are deleted for good after 30 days:". List "%s · %s" (path below folder, size), total
"%d files · %s". Staying "%d other files in the folder stay, and so does the folder." Loss "Everyone's listening places,
bookmarks and shelf entries for this book go too. Putting the files back brings the book back without them."
Checkbox "I understand these files leave the library." Buttons "Remove this book and its files", "Remove from Tendshelf,
keep the files", "Keep it". Keep-files help "The files stay where they are. Tendshelf stops showing the book until you show it again."
Status: "Moving the files…" / "Removed. The files are kept until %s." / "Put back" / "Delete now" / "Show again".
Errors: plan_changed "The files changed since you opened this page. Look at the list again."; changed_since_scan "Some
files changed since the last scan, so nothing was moved. Scan the library, then try again."; read_only "Tendshelf cannot
change files in this folder, so nothing was moved."; cannot_move "Tendshelf could not move every file, so it put them all
back. Nothing was removed."; not_confirmed "Tick the box to confirm."; busy "This book is already being removed."
Book notice: "Another copy of this book is in the library." link "Compare them".

## 7. Rule texts
- AGENTS.md :102-104 replace with: `"Remove this book" deletes only upload_paths files still unchanged since they were
  placed (size, mtime and quick hash), then only the empty folders it created. The owner and admins may also remove a book
  whose files were put in the library folder by hand (decision 0045): its scanned files, and listed cover and sidecar files,
  each checked against the scan's record, are moved into a hidden .tendshelf-removed folder after a warning that lists them,
  can be put back, and are deleted 30 days later. No other code path may write, rename or delete a library file.`
- 0015 and 0029: append `## Addendum 2026-11-15 (decision 0045)` stating point 11 now has a second, owner/admin path, and
  the "already in the library" check; 0029: contributors unchanged, may not Add anyway on an exact copy.
- vision.md:92: "...except for the jobs the owner starts: writing details into a file (with a backup and Undo), adding a
  book by upload, and removing a book after a warning (its files are kept for 30 days and can be put back)."
- New `docs/decisions/0045-manage-every-book.md`: context (the incident), D1-D11, rejected: outright delete, OS trash
  (XDG trash needs `$HOME` on the same filesystem, no stdlib), a second ignore table.

## 8. Tests and gates
P1 (`internal/upload/removeany_test.go`, real temp folders): plan lists files, extras, staying; root == folder (no extras,
no rmdir); nested other book (no extras); symlinked file → not_removable; record mismatch → changed_since_scan; file edited
between plan and job (same size, new mtime / same mtime new bytes) → failed, nothing moved; a `../` or absolute path outside
the folder in `audio_files` → not_removable; hardlinked file moves, other link intact; read-only dir (chmod 0555, skip as
root) → read_only, nothing moved; second rename fails (dest dir made unwritable) → rollback, all files back; crash simulation
(state moving with half held) → rolled back; restore with original recreated → that file kept; purge after 30 d via fake
clock; keep_files → scan skips the root, restore → scan brings it back; audit rows; unique active removal → busy.
P1 share: owner, admin allowed; contributor, member, guest refused for RemoveLibraryFiles.
P2 httptest: owner/admin GET page 200 with file list; contributor (own or not), member, guest 404/403 as the sweep;
POST without `understand` 422; stale plan 409; CSRF missing 403; htmx error → toast fragment; matrix rows for each new route.
P3: manifest with matching quick → 409 + match; book in a library the person cannot list → no match; other library → no
match; .zip → no pre-send match; stage 2 same_files / same_details / none; add_anyway owner ok, contributor same_files 403;
cancel deletes staging; table-driven SameDetails shared with groupDuplicates (existing duplicates tests unchanged).
P4: JS quick hash equals `media.QuickHash` for sizes 1 B, 2 MiB−1, 2 MiB, 3 MiB (empty files are refused earlier) (Go test with a fixture
vector file computed in Go, JS checked by the opt-in browser helper); row fragment states; captures at 390/768/1024/1440.
Existing tests: unchanged (Removable, RemovableBy, parts keep their meaning); `routes_matrix_upload_test.go` gains rows.
Gates per package: `go test -race` on changed packages and dependents, `make generate` clean, `make check`, `make build`.

## 9. Out of scope
Cross-library duplicate warnings; full SHA-256 of library files; restoring progress on Put back; holding area for upload-placed
removals (still direct delete); removing one part of a hand-added book; duplicates among concurrent in-flight uploads;
a setting for retention; Android/desktop surfaces; API v1 endpoints.
