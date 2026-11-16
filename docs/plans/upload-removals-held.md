# Plan: upload-placed removals go through the holding folder (follow-up 5 of decision 0045)
Read at a953762. **Needs migration 0046 (lead approves the number first; no 0046 on any local branch at a953762).** Fallback B (no migration) at the end.

## 1. Invariants
- I1 One holding implementation: `removeany.go` (`removals`, `removal_paths`, `moveOne`, `rollbackRemoval`, `putBack`, `PurgeRemoval`, sweep). No second mover.
- I2 Only `upload_paths` rows in state `present` inside the book's folder are moved; each is checked (lstatNoLinks, regular, size, mtime, quick hash vs the ledger) before the first move. A changed, linked or unreadable file stays and is named (`RemoveResult.Kept`); a ledger file already gone is marked `removed` with no move.
- I3 All-or-nothing per removal: rows recorded before the first rename; a failed rename rolls every earlier one back; a crash mid-move is rolled back by the sweep.
- I4 A contributor removes only what their uploads placed (`placedBy`, `allFilesAddedBy`, `pathOwned`, unchanged); share rules unchanged.
- I5 Put back never overwrites (hard link, then rename); it restores files, the ledger rows (`present`) and who-added-it (`upload_books`) only for what actually came back.
- I6 Every exported signature and error code of removeany.go, remove.go, remove_part.go stays; `RemoveResult` and `Removal` only gain fields.
- I7 Folders the upload created are rmdir'd only when empty; Put back recreates the folders it needs (MkdirAll 0755).
- I8 Text-page ebooks stored in `<data>/ebooks` (origin `upload`) are NOT in this package: still deleted at once (decision L5).

## 2. Decisions
- D1 Inline, not a job: upload removals record the removal and run the move in the request (`context.WithoutCancel`), because postBookRemove's redirects, `EnsureDefault` and the existing tests need the book/edition gone when the POST returns; renames are cheaper than today's deletes.
- D2 Same tables, `source` column: `removals.source = 'upload'` marks these; commit rules differ (book row kept when something stays), restore is shared.
- D3 Snapshot tables instead of re-deriving: `uploads` rows are pruned after 30 days, so `upload_books` cannot be rebuilt at Put back; ledger ids make re-presenting exact.
- D4 One removal per user action (whole combined book = one removal incl. owned ebooks outside the root), so one Put back.
- D5 Book rows still go at commit (as 0045): Put back = files + ledger + who-added-it + a scoped rescan; listening places, bookmarks, shelves, edited details do not come back (same as 0045; restoring them needs hidden rows in internal/library, out of scope).
- D6 Put back / Delete now stay owner and admin only (`share.RemoveLibraryFiles`, existing routes); the confirm page tells everyone so.
- D7 Sweep: an upload-source `moving` row is rolled back as soon as the sweep holds its lock (the inline mover holds `rmv:<rid>` for the whole move, so a lockable `moving` row has a dead mover); upload-source `planned` rows fail as `stuck` after `movingStale` (1 h), because no job will pick them up.

## 3. Data: `internal/db/migrations/0046_removal_ledger.sql` (lead approval)
```sql
-- 0046_removal_ledger: what a removal took from the upload ledger and from who-added-it, so Put back returns it (decision 0045 addendum).
ALTER TABLE removals ADD COLUMN source TEXT NOT NULL DEFAULT 'library' CHECK (source IN ('library','upload'));
CREATE TABLE removal_ledger (
  removal_id     TEXT NOT NULL REFERENCES removals(id) ON DELETE CASCADE,
  upload_path_id TEXT NOT NULL,
  PRIMARY KEY (removal_id, upload_path_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE removal_upload_books (
  removal_id TEXT NOT NULL REFERENCES removals(id) ON DELETE CASCADE,
  root_path  TEXT NOT NULL,
  upload_id  TEXT NOT NULL,
  library_id TEXT NOT NULL,
  added_by   TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (removal_id, root_path)
) STRICT, WITHOUT ROWID;
```
`internal/db/db.go`: `SchemaVersion = 46`. Test `internal/db/migration_removal_ledger_test.go`: `TestMigrationRemovalLedgerFromPrevious` (openUpTo 45, seed7, one removals row; after open: version 46, row has `source='library'`, both tables empty); `TestMigrationRemovalLedgerFromEmpty` (`source='trash'` refused; ledger and owner rows cascade with their removal).

## 4. Service shapes (internal/upload)
- removeany.go: `const SourceLibrary = "library"; SourceUpload = "upload"`; `Removal.Source string` (read in `removals()`, `COALESCE` not needed). `RemoveResult` (remove.go) gains `RemovalID string \`json:"removal_id"\`` ("" when nothing moved); `Deleted` now means "moved into the holding folder" (doc comment).
- `type holdScope string`: `scopeBook` (Remove), `scopeWhole` (combined whole), `scopeAudio`, `scopeText`. Table: audit action `upload.remove` / `upload.remove` / `upload.remove` / `upload.remove_text`; drop who-added-it when no file is left: yes / yes / no / no; unpair the rest of the text at commit: no / yes / no / no; book row may go: yes / yes / yes / never; queue scoped scan when the book stays: yes / yes / yes / no.
- `type uploadPlan struct { plan RemovalPlan; kept []string; keptIDs, goneIDs []string; gonePaths []string; dirIDs map[string][]string }`.
- Errors: unchanged codes; the move's failures come back as `*Error` 409 `read_only`, `cannot_move`, `changed_since_scan` (messages from `errReadOnly`/`errCannotMove`/`errChangedSinceScan`), 409 `busy` from `mapBusy`.

## 5. Steps (in order; builder touches only these files)
1. Migration files of section 3 (after lead approval).
2. `internal/upload/removeany.go`:
   a. Extract StartRemoval's `insert` closure into `func (s *Service) insertRemoval(ctx, tx, rid string, plan RemovalPlan, mode, source, userID, folderPath string, state string) error` (adds `source` to the INSERT); StartRemoval calls it with `SourceLibrary`. Behaviour unchanged.
   b. In `insertRemoval`, for mode `files` only, per item of kind `file`/`extra`/`dir`: `INSERT OR IGNORE INTO removal_ledger(removal_id, upload_path_id) SELECT ?, id FROM upload_paths WHERE path = ? AND kind = ? AND state = 'present'` (ledger kind `file` for file and extra, `dir` for dir). Applies to 0045 removals too (a mixed book's ledger rows come back on Put back).
   c. `func (s *Service) snapshotOwners(ctx, tx, rid, root string) error`: `INSERT OR IGNORE INTO removal_upload_books(removal_id, root_path, upload_id, library_id, added_by, created_at) SELECT ?1, root_path, upload_id, library_id, added_by, created_at FROM upload_books WHERE root_path = ?2 OR (root_path > ?2 || '/' AND root_path < ?2 || '0')`. Call it immediately before the existing `DELETE FROM upload_books` in removeFiles' commit.
   d. Rename the body of `removeFiles` to `moveRemoval(ctx, g, rid string, scope holdScope) (any, error)`; `removeFiles` returns `s.moveRemoval(ctx, g, rid, "")`. At "Everything is held": `if rm.Source == SourceUpload { return s.commitUpload(ctx, g, lr, rm, paths, moved, scope) }`; the library branch stays byte-for-byte.
   e. `commitUpload`: `left` = (scope == scopeText) OR any live `audio_files` path of the book that `lr.Lstat` finds OR (scope != scopeWhole AND any `ebook_files` row `book_id = ? AND origin = 'library' AND path IS NOT NULL` not in moved that `lr.Lstat` finds). One `repo.UpdateGuarded`: guard g; `dropBookRows(tx, bookID, moved, false)`; scopeWhole: `UPDATE ebook_files SET book_id = NULL, paired_by = NULL, book_default = 0 WHERE book_id = ?`; when `!left` and scope is book/whole: `snapshotOwners` + the same `DELETE FROM upload_books` as remove.go:205; `UPDATE removals SET state='held', error_code=NULL, updated_at=?, purge_after=now+30d`; `audit(tx, rm.RemovedBy, action, "book:"+bookID, {"deleted": len(moved), "title", "removal": rid})`. Library fn: `DeleteBookAndFiles` when `!left`, else `return nil`. On error: rollbackRemoval(cannot_move) as the library branch does. Then rmdir `dir` paths deepest first exactly like the library branch, and for each dir also `markPath` every id in `dirIDs[path]`: `removed` on success or ErrNotExist, `kept` on ENOTEMPTY/ErrExist, else warn. When `left` and scope != scopeText: queue the scoped scan (move remove.go:215-224 into `func (s *Service) queueRootScan(ctx, userID, libraryID string, folder library.Folder, root string, lr *lroot)` and call it from both).
   f. `func (s *Service) holdNow(ctx, userID string, up uploadPlan, scope holdScope) (string, error)`: rid `db.NewID("rmv")`; `s.write`: `insertRemoval(..., ModeFiles, SourceUpload, ..., RemovalPlanned)` → `mapBusy`; then `moveRemoval(context.WithoutCancel(ctx), nil, rid, scope)`; a `*jobs.Error` maps to `fail(409, je.Code, je.Text)`. commitUpload reads the dir ids itself: `SELECT p.id, p.path FROM upload_paths p JOIN removal_ledger l ON l.upload_path_id = p.id WHERE l.removal_id = ? AND p.kind = 'dir'` (`uploadPlan.dirIDs` serves only the zero-move path, step 3b).
   g. RestoreRemoval: after the file loop and before the write, list the ledger rows to re-present: files `kind='file'` whose path is in `res.Restored`; dirs whose path `lr.lstatNoLinks` finds as a directory. In the existing write tx: `UPDATE OR IGNORE upload_paths SET state = 'present', updated_at = ? WHERE id = ? AND state IN ('removed','kept')` per id (OR IGNORE: a newer upload's live row on the same path wins); when `len(res.Kept) == 0`: `INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) SELECT root_path, upload_id, library_id, added_by, created_at FROM removal_upload_books WHERE removal_id = ? ON CONFLICT(root_path) DO NOTHING`, then delete that removal's `removal_ledger` and `removal_upload_books` rows. Kept > 0: who-added-it is NOT restored (a foreign file now sits in that root).
   h. PurgeRemoval tx: also `DELETE FROM removal_ledger WHERE removal_id = ?` and `DELETE FROM removal_upload_books WHERE removal_id = ?`.
   i. sweepRemovals: moving query becomes `state = 'moving' AND (source = 'upload' OR updated_at < ?)`; add, before the existing planned query, upload-source `state = 'planned' AND source = 'upload' AND updated_at < now-movingStale` → `failRemoval(..., "stuck", RemovalPlanned)`; the existing planned query gains `AND source = 'library'`.
3. `internal/upload/remove.go`:
   a. `func (s *Service) planUpload(ctx, book library.Book, folder library.Folder, lr *lroot, only string, keep, extra map[string]bool) (uploadPlan, error)`: uploads = `uploadsOfPaths(live audio paths ∪ ebookPaths ∪ extra ∪ {only})`; for each `pathsOf` row with `State == pathPresent`, `within(folder.Path, r.Path)`, not the folder itself: files selected when `only != ""` ? `Clean(r.Path) == Clean(only)` : `(within(book.RootPath, r.Path) && !keep[r.Path]) || extra[r.Path]`; classify with `lr.lstatNoLinks`: ErrNotExist → gone; any other error, link, not regular, size/mtime differ, empty `QuickHash`, `QuickHashFile` differs → kept (path + id); else item `planItem{kind:"file", path, rel: relSlash(folder.Path, path), quick: r.QuickHash, size: r.Size, mtime: r.MtimeNs}` and `PlanFile`, `Bytes += Size`. Dedupe items by path. Dirs (only when `only == ""`): `kindDir` rows with `within(root, p) || within(p, root)` that lstatNoLinks finds as a dir → `planItem{kind:"dir"}`, ids into `dirIDs`. Fill `BookID, Title, LibraryID, FolderID, RootPath` (`only` when set, else `Clean(book.RootPath)`), `Hash = planHash(items)`.
   b. Rewrite `remove(ctx, userID, bookID, placedBy string, keep map[string]bool)` as `removeHeld(ctx, userID, bookID, placedBy string, scope holdScope, keep, extra map[string]bool) (RemoveResult, error)`: `removeChecks(..., scope == scopeWhole || scope == scopeAudio)`; openLroot; planUpload; in one `s.write`: `markPathTx` keptIDs `kept`, goneIDs `removed`, `DELETE FROM ebook_files WHERE book_id = ? AND path = ?` for gone paths. If no `file` item: run the no-move finish (book rows/audit/scan/rmdir by `dirIDs`, i.e. today's lines 164-226 logic, `left` as in 2e) and return with `RemovalID ""`. Else `holdNow`; `Deleted` = moved paths, `Kept` = kept. `Remove` = `removeHeld(..., scopeBook, nil, nil)`. Delete `removeFile` (unused).
4. `internal/upload/remove_part.go`:
   a. RemoveWhole combined: after `removeChecks(..., true)`, collect `extra` = paths of `parts.Text` with `Removable && !Uploaded`; call `dropUpload` for `Removable && Uploaded` (as today); then `removeHeld(..., scopeWhole, nil, extra)`. Drop the RemoveEbookFile loop and the separate unpair write (now inside commit).
   b. RemoveEbookFile: keep every check up to the folder; then `planUpload(only = path)`: gone → delete the row by id + audit `upload.remove_text` (today); kept or lstat error → `Kept = [path]`, row stays (today); else `holdNow(scopeText)`.
   c. RemoveAudio: unchanged order; last line `removeHeld(..., scopeAudio, keep, nil)`. removeTextBook: unchanged (its `Remove` is now held).
5. `internal/web/upload_copy.go`: strings of section 6. `internal/web/pages/book_remove.templ`: doc comments only ("will move into the holding folder"), then `make generate`.
6. Tests (section 7), then docs (section 8).

## 6. Copy (exact)
- `bookRemoveBody` = "This moves the %d files Tendshelf added for this book into a hidden holding folder. They are kept for 30 days, and the owner or an admin can put them back. Files changed since then stay."
- `bookRemoveBodyOne` = "This moves the file Tendshelf added for this book into a hidden holding folder. It is kept for 30 days, and the owner or an admin can put it back. Files changed since then stay."
- `bookRemoveCombinedBody` = "This book has audio and a text book. Pick what to remove. Files Tendshelf added are kept for 30 days, and the owner or an admin can put them back. Files changed since then stay."
- `bookRemoveAudioHelp` = "The text book stays, with your reading place, highlights and bookmarks. Your listening place goes with the audio and does not come back."
- `bookRemoveWholeHelp` = "Moves the audio and every text book Tendshelf added into the holding folder. Your listening progress, bookmarks and reading places on this book go, and putting the files back does not bring them back."
- `bookRemoveTextBody` = "This removes the book. Ebook files Tendshelf put in the library are kept for 30 days, and the owner or an admin can put them back. Your reading places, bookmarks and shelves for it go and do not come back."
- `bookRemoveEditionBody` = "This removes the ebook %s from this book. The other editions, the audiobook and your places stay."
- New, used only after lead agreement L2: `bookRemoveStoredGone` = "Ebooks uploaded on the Text page are deleted now and cannot be put back."; `bookRemoveEditionHeld` = "This moves the ebook %s into the holding folder for 30 days; the owner or an admin can put it back. The other editions, the audiobook and your places stay."

## 7. Tests and gates
New `internal/upload/remove_held_test.go` (names prefixed `TestUploadHeld`; helper `heldTree(t, root)` = `tree` minus `.tendshelf-removed/...`, `holdingFiles(t, root, rid)`):
- WholeBookHeldAndPutBack (owner): files under `.tendshelf-removed/<rid>/Ann Author/Great Title/`, removals row `source='upload' state='held' purge_after=now+30d`, book rows gone, ledger rows `removed`, `upload_books` gone; RestoreRemoval → files back byte-identical, ledger `present`, `upload_books` row back, scan queued; rescan → `Removable` and contributor `RemovableBy` true again.
- ContributorOwnHeld / ContributorOthersRefused (409 `not_theirs`, nothing moved, no removals row).
- ChangedFileStays: changed cover named in Kept, stays in place, not in holding; book gone; restore puts back only the held ones.
- ChangedAudioKeepsBook: book row stays, scan queued, held removal has the unchanged files.
- TextPartHeld (scopeText): ebook row gone, audio/progress stay, no scan queued, `upload_books` untouched; Put back + rescan returns the file.
- AudioPartHeld (scopeAudio): text books stay, `upload_books` untouched.
- WholeCombinedOneRemoval: exactly one removals row incl. an owned ebook outside the root; unowned library ebook stays unpaired.
- TextOnlyLedgerHeld; AlreadyGoneFile (ledger file missing on disk: marked removed, no removal row when nothing else moves, book gone).
- ReadOnlyFolder (chmod 0555, skip as root): 409 `read_only`, nothing moved, removal `failed`.
- CrashMidMove (`s.crash` hook `remove_files:after_move:0`, then Sweep): files back, removal `failed/interrupted`, book rows intact; stuck `planned` upload row older than 1 h fails `stuck`.
- PutBackPlaceTaken: a new file at the old path → Kept, `upload_books` not restored, ledger row of that file not re-presented.
- PurgeAfter30Days (fake clock + Sweep): holding gone, `removal_ledger`/`removal_upload_books` rows gone, ledger stays `removed`.
- Busy: second `Remove` while a `planned` row exists → 409 `busy`.
Changed: `remove_test.go` (every `tree` assertion becomes `heldTree`; "deleted" assertions become "in holding"; audit count unchanged), `remove_part_test.go`, `remove_textonly_test.go` likewise; `TestRemoveEbookFileKeepsRowWhenLstatFailsOtherwise` unchanged. Web: `routes_remove_whole_test.go`, `routes_remove_part_test.go`, `routes_remove_shapes_test.go`: library files now asserted in `.tendshelf-removed`; stored Text-page copies still asserted gone; one case per file posts `POST /removals/{rid}/restore` as admin (rid from `SELECT id FROM removals`) and asserts the files back; contributor GET `/removals/{rid}` 404; `routes_remove_menu_test.go` copy only. Per-role matrix (owner, admin, contributor own, contributor other, member, guest) for POST `/books/{id}/remove`: 303/303/303/404/404/404 with removals rows 1/1/1/0/0/0. Must pass unchanged (report PLAN GAP if not): `contributor_test.go`, `routes_upload_test.go`, `routes_matrix_upload_test.go`, `routes_remove_any_test.go`.
Gates: `make generate` clean, `make check`, `make build`; opt-in browser test covering the remove page (`grep -ln 'books/.*/remove' internal/web/*browser_test.go`) with `TENDSHELF_TEST_BROWSER=1`, captures at 390/768/1024/1440 (copy only).

## 8. Rule texts
- AGENTS.md, replace the sentence starting `"Remove this book" deletes only` through `deleted 30 days later.` with: "\"Remove this book\" moves only `upload_paths` files still unchanged since they were placed (size, mtime and quick hash) into a hidden `.tendshelf-removed` folder in the same library folder, then removes only the empty folders it created; a changed file stays and is named. The owner and admins may also remove a book whose files were put in the library folder by hand (decision 0045): its scanned files, and listed cover and sidecar files, each checked against the scan's record, are moved into the same holding folder after a warning that lists them. Either way the owner or an admin can put the files back, and they are deleted 30 days later or by \"Delete now\". Ebooks uploaded on a book's Text page are Tendshelf's own copies in the data folder and are still deleted at once."
- 0015: append `## Addendum 2026-11-15 (follow-up of decision 0045): removal is held` — point 11 and "Remove one part" move files into the holding folder instead of deleting; same checks; Put back restores files, ledger and who-added-it; progress etc. do not return.
- 0029: append the same heading: a contributor's removal is held; only the owner or an admin can put it back or delete it now; Put back gives the contributor their rights back only when every file returned.
- 0045: append `## Addendum 2026-11-15: upload-placed removals` — point 11's "keep today's direct path" is replaced; D1, D2, D3, D7; migration 0046.
- CHANGELOG Unreleased/Changed: "**Removing a book you uploaded can be undone.** Its files now wait in the holding folder for 30 days, and the owner or an admin can put them back from Removed books, like any other removed book. Text books uploaded on a book's Text page are still deleted at once."
- roadmap: status line under "Manage every book": scope, checks run with counts, limits (I8, D5), next step L5.

## 9. Needs the lead's agreement (not in the builder's steps)
- L1 Migration 0046 and its three files (number, schema).
- L2 `routes_upload_pages.go` getBookRemove: edition page `if !pf.Uploaded { body = fmt.Sprintf(bookRemoveEditionHeld, …) }`; text-only and whole help `+ " " + bookRemoveStoredGone` when any `parts.Text[i].Uploaded && Removable`.
- L3 `routes_upload.go` postBookRemove (optional): for owner/admin, answer `/removals/{rid}` instead of the library when `res.RemovalID != ""`, so Put back is one click away.
- L4 Contributor Put back of their own removal (`loadRemoval`: `rm.Source == upload && rm.RemovedBy == u.ID`); recommended no for now.
- L5 Package B: Text-page stored copies held under `<data>/removed/<rid>/` with an `ebook_files` row snapshot (internal/ebook, another migration).

## 10. Out of scope
Restoring progress, bookmarks, shelves, edited details or the book id; re-pairing a put-back text edition (left to the scan, unverified); Text-page stored copies (L5); contributor-facing removed list; UI changes beyond copy; Android/desktop; API v1.

## Fallback B (no migration, if the lead refuses 0046)
Skip step 1, 2b, 2c, 2g's who-added-it and ledger parts, 2h; no `source` column: pass the scope only inline (upload removals never commit from the job), keep the sweep's 1 h rule for all. Result: Put back returns files and the book, but the restored book counts as hand-added: only the owner or an admin can remove it again, and the contributor loses edit rights. Copy adds: "After it is put back, only the owner or an admin can remove it."
