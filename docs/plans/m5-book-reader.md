# M5 Book reader: implementation plan

Decision [0018](../decisions/0018-book-reader.md). Input: [research](../research/ebook-reader-features.md). A Sonnet builder
executes this literally. Anything unclear is `PLAN GAP: <one line>`, after committing what is green. Copy marked NEW follows
`BRAND.md` and goes in the report. Translations do not apply (no `messages/`); copy lives in `*_copy.go`.

## Packages and order

| Package (~tool calls) | When | Parallel with | Shared files |
| --- | --- | --- | --- |
| P4 merge (orchestrator) | **first**, branch `worktree-agent-a5ebb3e97578730ce` as it stands | nothing | see "P4 and X" |
| X Remove alignment (~200) | after P4 merge, alone | none | nearly everything in `internal/web` |
| B1 Ebook books: data, life, merge, metadata, covers (~250) | after X | RD1 | `db.go`, `ebook/job.go`, `cmd/tendshelf/hooks.go`, `architecture.md` |
| RD1 Document v2 and reader server (~250) | after X | B1 | `ebook/job.go`, `ebook/doc.go`, `routes_reader.go`, `server.go` |
| B2 Book surfaces and player icon (~240) | after B1 | B3, RD2 | `book.templ`, `bundle.css`, `a11y_pages_test.go`, `player*.templ`, `motion_test.go` |
| B3 Add books and `/admin/ebooks` (~200) | after B1 | B2, RD2 | `bundle.css`, `a11y_pages_test.go` |
| RD2 Paginator client (~250) | after RD1 | B2, B3 | `motion_test.go`, `pages-reader.css`, `reader.templ` |
| RD3 Marks, footnotes, search, export, errors (~220) | after RD2 | B2 or B3 | `motion_test.go`, `reader.templ`, `pages-reader.css` |
| RD4 Captures, a11y, contrast, layout time (~180) | after everything above | none | `a11y_pages_test.go`, visual matrix |

At most three at once. B1 merges before RD1 when both run (migration 0021 is B1's; RD1 adds none). Each package appends its
status paragraph under M5 in `docs/roadmap.md`; the orchestrator resolves that merge. Test and helper prefixes
(`internal/web` is one package): X `TestMigration0020*`, `TestX*`, helpers `xr*`; B1 `TestMigration0021*`, `TestTextBook*`,
`TestMerge*`, `TestSplit*`, `TestReadingRepo*`, helpers `tb*`; B2 `TestFormats*`, `TestPlayerRead*`, helpers `fmt2*`;
B3 `TestAddEbook*`, `TestEbookAdmin*`, helpers `eadm*`; RD1 `TestDocV2*`, `TestReaderServe*`, `TestReaderSpeaker*`, helpers
`rs*`; RD2 `TestPaginator*`, `TestReaderInput*`, helpers `pg*`; RD3 `TestReaderNote*`, `TestReaderSearch*`, `TestMarksExport*`,
helpers `rn*`; RD4 `TestBR*`, helpers `br*`.

## Invariants (all packages)

1. Every ebook file stays read-only. Merge, split, pairing and re-reading change rows only. Add books (B3) is the one writer, inside `internal/upload`.
2. Every text route answers 404 unless `share.Can(user, ReadText, book)`. Marks, positions and sessions are read and written only with `user_id = current user`; admins see only their own.
3. An automatic merge never widens access: it is skipped when any grant or share link could reach the audiobook because of it (B1 guard).
4. Automatic pairing never overrides a person: only `hold IS NULL` files, never a pair with an `ebook_not_same` row.
5. No publisher markup, CSS, font or script reaches the browser. Text is escaped from the canonical document; URLs are ours or pass the scheme allowlist.
6. A place is an anchor (`sid`, `off`) plus quote plus percent. No screen page number is ever stored.
7. Listening and reading positions never write each other.
8. `/progress`, `/stream`, `/hx/player` and queue routes treat a `kind='text'` book as not found (one gate, `playableFor`).

## Decisions

- **`books.kind`, explicit, not inferred.** It names the row's owner, so the scanner, `metadata.local`, the cover job and the
  library filter branch in one comparison; "no audio" cannot tell a text book from an audiobook whose files went missing.
- **`reading_positions` table, not `progress` columns.** `progress` is a millisecond clock with a `duration_ms` CHECK and every
  listening query reads it; the formats must not share one newest-wins row (invariant 7).
- **Upload to the library folder, not `<data>/ebooks`.** `books.folder_id` and `root_path` are NOT NULL; rebuilding `books`
  touches 20 foreign keys. It also matches 0015's reason (files where the owner's tools see them), reuses resume, staging and
  Remove, and the book survives a lost database because a scan finds it.
- **No `covers.source` rebuild** (the brief expected one). `embedded` already means "the picture inside the file". Its consumers
  (`ours()`, `BooksWithUntintedCover`, the cover picker) want exactly that. A rebuild of a table `books.cover_id` points at
  buys nothing. Only the picker's label changes if it says audio.
- **Automatic merge moves data, and needs a safety guard.** Operator: confident match means one book. Guard in B1.
- **Speaker resumes audio at its own place; no chapter start.** Equal chapter titles are common ("Chapter 1") and audio and
  text chapters often split differently, so a match proves little, and jumping would overwrite a real listening place.
- **Keep sentence spans and SIDs.** `rdrSegments`, `reader-marks.js` and the highlights schema already rest on them, and the
  future `speechSynthesis` feature will want them. Removing them is work with no reader benefit.
- **Chapter parts, not whole chapters.** A TXT or PDF can be one 2 MB chapter. Parts of about 40,000 UTF-16 units, cut at
  block edges, keep column layout fast. Server URLs drive next and previous, so the client does no arithmetic.
- **`jobs.Error.Hold` is removed** with its one user, `sqlHoldHandler` and its test cases. `HoldOnRecovery` stays. Dead paths cost reading.
- **Align folders are removed by the operator, not by start-up.** R3 is in no tag (`git tag --contains` the first align commit
  is empty; only `v2026.1.0` exists), so these folders exist only where someone ran `main` in the last days. Code that deletes
  folders from a person's disk, kept forever for that, is the worse risk. Repair script below.
- **Reader CSS leaves the service-worker precache and the settings CSS folds in.** See "P4 and X".

## P4 and X

- The orchestrator merges P4 before X starts. Reason: P4 carries `users/reader_settings.go`, `POST /me/reader-settings`,
  `?sheet=reading`, OpenDyslexic and tests, all kept; X then strips its voice rows once, on one tree. P4 fails `make page-weight`
  by 2 requests until X step 2. `page-weight` is not in `make check`, so main stays green.
- Reuse of branch `worktree-agent-a0b783acabb8910b8` (d02e83e), not merged: RD2 takes `pages-reader.css` by `git show`, RD3 takes
  `reader-marks.js`; both minus the voice rules. `readalong.js` is not taken; its code for `Range` and scroller lookup may be read.
  `readalong_browser_test.go` is not taken. After RD3 the orchestrator asks before deleting that branch.
- Page weight: `css/pages-reader-settings.css` folds into `pages-reader.css` (one file for one page). `Set.Precache()`
  (`static/static.go:290`) skips `css/pages-reader.css`: no other page uses it, and offline reading (G5) adds it back. That removes
  both requests. Every page keeps today's request budget.

## Repair after X merges (the orchestrator runs it, server stopped)

```sh
D="$HOME/.local/share/tendshelf"   # or the instance's --data
test -f "$D/tendshelf.db" || exit 1
[ "$(sqlite3 "file:$D/tendshelf.db?mode=ro" 'PRAGMA user_version')" -ge 20 ] || exit 1   # X has migrated
rm -rf -- "$D/runtimes/align" "$D/cache/align" "$D/align"
rmdir -- "$D/runtimes" 2>/dev/null || true
```
Checked read-only on this machine: `runtimes/align` 2.5 GB, `cache/align` 29 MB, no `<data>/align`, schema 19, 0 highlights, 2
ready ebooks (both paired), `align.book` 1 cancelled and 1 failed, `align.check` 2 done. Rollback: none needed for the data (it
is derived); the database rollback is the automatic `backups/tendshelf-v19-*.db`.

## X Remove alignment

**Reused**: `internal/textmatch`, `ebook.*`, highlights and bookmarks. **Removed**: everything below. Compile errors finish the list.

Migration `internal/db/migrations/0020_drop_alignment.sql`, `SchemaVersion = 20`:

```sql
-- 0020_drop_alignment: read-along is dropped (decision 0018). Highlights keep their text range; the audio times go.
-- The foreign key to alignments forces a table rebuild; it must happen before alignments is dropped.
CREATE TABLE highlights_new (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id      TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  edition_id   TEXT REFERENCES editions(id) ON DELETE SET NULL,
  chapter_idx  INTEGER NOT NULL CHECK (chapter_idx >= 1),
  start_sid    TEXT NOT NULL CHECK (length(start_sid) BETWEEN 6 AND 40),
  start_off    INTEGER NOT NULL CHECK (start_off >= 0),
  end_sid      TEXT NOT NULL CHECK (length(end_sid) BETWEEN 6 AND 40),
  end_off      INTEGER NOT NULL CHECK (end_off >= 0),
  quote        TEXT NOT NULL CHECK (length(quote) <= 4000),
  quote_prefix TEXT NOT NULL DEFAULT '' CHECK (length(quote_prefix) <= 32),
  quote_suffix TEXT NOT NULL DEFAULT '' CHECK (length(quote_suffix) <= 32),
  colour       TEXT NOT NULL DEFAULT 'yellow' CHECK (colour IN ('yellow','green','blue','pink')),
  note         TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 2000),
  client_id    TEXT CHECK (client_id IS NULL OR length(client_id) BETWEEN 1 AND 64),
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  deleted_at   INTEGER
) STRICT;
INSERT INTO highlights_new (id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote,
  quote_prefix, quote_suffix, colour, note, client_id, created_at, updated_at, deleted_at)
SELECT id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote,
  quote_prefix, quote_suffix, colour, note, client_id, created_at, updated_at, deleted_at FROM highlights;
DROP TABLE highlights;
ALTER TABLE highlights_new RENAME TO highlights;
CREATE INDEX highlights_live ON highlights(user_id, book_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX highlights_chapter ON highlights(user_id, edition_id, chapter_idx) WHERE deleted_at IS NULL;
CREATE INDEX highlights_sync ON highlights(user_id, updated_at);
CREATE INDEX highlights_book ON highlights(book_id);
CREATE INDEX highlights_edition ON highlights(edition_id) WHERE edition_id IS NOT NULL;
CREATE UNIQUE INDEX highlights_client ON highlights(user_id, client_id) WHERE client_id IS NOT NULL;
DROP TABLE alignment_chapters;
DROP TABLE alignments;
DROP TABLE runtime_components;
-- A job kind with no handler is never claimed, and a running one would be held forever. These jobs have no children.
DELETE FROM jobs WHERE kind IN ('align.book','align.check');
```
0018 and 0019 stay untouched. Why the jobs are deleted: the runner claims only registered kinds, so a queued row would sit
forever, `recoverExpired` holds a running row of an unknown kind, and `/admin/jobs` has no view for it. The migration runs on
a stopped server, so no lease is live.

Steps:
1. Delete the inventory: `internal/align/` (with `runtime/`, `aligntest/`), `runtimes/align/`, `cmd/tendshelf/align.go`, `align_test.go`,
   `main.go` 68, 127-131, 142, 156-158; `web/routes_runtimes.go`, `runtimes_copy.go`, `pages/admin_runtimes.templ`,
   `css/pages-runtimes.css`, `routes_ebooks_align.go`; `routes_jobs.go` 9-10, 62-63; `server.go` 27, 97-101, 132, 253, 289; `prefs.go:27`;
   `routes_me.go:96`; `pages/prefs.templ:118`; `pages/profile.templ:41,177-178`; `pages/book_text.templ` 27-30, 67-68, 199-200, 247-277;
   `ebook_copy.go` 77-108; Makefile `test-align-real`; NOTICES rows 22-27 and 40-43; `media.PCM16k`, `media.LowerPriority`;
   `jobs.Error.Hold`, `sqlHoldHandler`, the Hold cases in `jobs/hold_test.go`; `.gitignore` `__pycache__` lines and `runtimes/align`.
   Remove any embed or test reference `go build ./... && go vet -tags dev ./...` then reports.
2. Fold CSS: move `pages-reader-settings.css` into `pages-reader.css` (`ReaderStyles` loses it); `Set.Precache` skips `css/pages-reader.css`, with a
   test row in `static_test.go`; `motion_test.go` row `"css/pages-reader.css": {4 * 1024, 32 * 1024}` (operator approval pending, see below).
3. Reader voice strip, leaving a plain working reader (scroll only, no client beyond marks, which RD3 brings):
   - `routes_reader.go` 21, 32-39, 76-89, 112, 176-186, 208-240, 316-330, 446-452, 501: no `align`, `mode`, timing route, `alongOK`, `rdrSIDAt`.
     `GET /books/{id}/read` query is `ch`, `at`, `off`, `sheet`; `mode=` is ignored, not a 404 (old links keep working).
   - `reader_render.go` 11, 162-222: no `rdrTiming`; `.ts-s` has `data-sid`, `data-o`; no `data-s`, no `ts-s--gap`. `reader.templ` 25-30, 100, 166-183,
     255-256: no mode nav, voice pill, focus, dock speed or play, Eyes-free. Footer: one line with `p. {n} of {total}`. `reader_copy.go` 11.
   - `routes_bookmarks_text.go`: drop `rdrStartAt`; a text bookmark stores `position_ms = 0`. Marks sheet: no "Play at"; lead NEW "Your highlights, notes
     and bookmarks in this book."
   - `users/reader_settings.go`, `pages/reader_settings.templ`, `reader-settings.js`: remove Highlight, Word, Follow, FollowStyle, ReturnAuto, View
     (fields, radios, `ts:reader:view`, `data-ts-no-word`). Stored `reader.v1` JSON with extra keys still loads (unknown keys ignored); test that.
   - Text tab: `ReadLabel` is always "Read".
4. Docs: `AGENTS.md` 19-30, 102; `architecture.md` rows and invariant 9-10 (the reader rows, no worker); `feature-map.md` 47-65; `git mv docs/read-along.md docs/reader.md`
   and cut it to under 60 lines (what the canonical document is, formats, refusals, the reader). Fix links. `vision.md` 27, 51-52, 71-72 reworded in B2.
Tests: `internal/db/migration_0020_test.go`: `TestMigration0020FromPrevious` (fixture `ms[:19]`: a highlight with `alignment_id`, `start_ms`, `end_ms`,
a queued and a running `align.book`; after: quote, note, colour survive; columns and three tables gone; both jobs gone; an `ebook.ingest` job untouched; `PRAGMA foreign_key_check`
empty), `TestMigration0020FromEmpty`; update pins of version 19; `TestXNoAlignLeft` (repo-text scan under `internal/`, `cmd/`: no `internal/align`, `readalong`, `/admin/runtimes`);
`TestXReaderOldModeLink` (`?mode=readalong` 200); `TestXReaderSettingsOldJSON`. Remove the tests in the inventory: `routes_matrix_align_test.go`, `routes_matrix_test.go`
243-244, 687-688, `text_align_test.go`, `text_align_visual_test.go`, `text_tab_test.go` align cases, `admin_runtimes_test.go`, `a11y_env_test.go:183`, `a11y_pages_test.go:46,121`, `reader_test.go` 130, 168-211.
Gates: `make generate` clean; `go test -race ./internal/db/ ./internal/jobs/ ./internal/web/... ./internal/media/ ./internal/ebook/ ./internal/progress/ ./internal/users/ ./cmd/...`; `make page-weight`; `make check`; `make build`.
Out of scope: any new reader behaviour; `bundle.css` voice classes (RD4 sweeps them, it is the handoff's file).

## B1 Ebook books: data, life, merge, metadata, covers

**Reused**: `pair.go` (`Match`, `Decide`, `Checks`), `pair_write.go` (`PairPending`, `pairFiles`, `Pair`, `Unpair`, `NotSame`, `Keep`), `hold`, `ebook_not_same`,
`reconcile.go`, `Covers.Run`, `metadata.Local`, `Follower`. **Extended**, not replaced.

Migration `0021_ebook_books.sql`, `SchemaVersion = 21`:

```sql
-- 0021_ebook_books: an ebook with no audiobook is a book (decision 0018). kind names who owns the row: audio = the scanner,
-- text = the ebook service. reading_positions is one text place per person and book; reading_sessions measures speed.
ALTER TABLE books ADD COLUMN kind TEXT NOT NULL DEFAULT 'audio' CHECK (kind IN ('audio','text'));
CREATE INDEX books_kind ON books(library_id, kind);
-- What the file says beyond editions' columns: description, publisher, year, series, series_index.
ALTER TABLE editions ADD COLUMN meta_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta_json) AND json_type(meta_json) = 'object');
-- The text book this file came from when it was merged; a split restores that id, so old links work again.
ALTER TABLE ebook_files ADD COLUMN merged_from TEXT;
CREATE INDEX ebook_files_merged ON ebook_files(merged_from) WHERE merged_from IS NOT NULL;
-- A merged-away id still answers: pages redirect, a reader's position save lands on the book that took over.
CREATE TABLE merged_books (
  from_id   TEXT PRIMARY KEY,
  to_id     TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  merged_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX merged_books_to ON merged_books(to_id);
CREATE TABLE reading_positions (
  user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id           TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  edition_id        TEXT REFERENCES editions(id) ON DELETE SET NULL,
  chapter_idx       INTEGER NOT NULL CHECK (chapter_idx >= 1),
  sid               TEXT NOT NULL DEFAULT '' CHECK (sid = '' OR length(sid) BETWEEN 6 AND 40),
  off               INTEGER NOT NULL DEFAULT 0 CHECK (off >= 0),
  quote             TEXT NOT NULL DEFAULT '' CHECK (length(quote) <= 64),
  char_pos          INTEGER NOT NULL CHECK (char_pos >= 0),
  percent_bp        INTEGER NOT NULL CHECK (percent_bp BETWEEN 0 AND 10000),
  finished_at       INTEGER CHECK (finished_at IS NULL OR finished_at > 0),
  client_updated_at INTEGER NOT NULL CHECK (client_updated_at > 0),
  server_updated_at INTEGER NOT NULL,
  device_id         TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  device_label      TEXT NOT NULL DEFAULT '' CHECK (length(device_label) <= 64),
  PRIMARY KEY (user_id, book_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX reading_positions_recent ON reading_positions(user_id, client_updated_at);
CREATE INDEX reading_positions_book ON reading_positions(book_id);
CREATE INDEX reading_positions_edition ON reading_positions(edition_id) WHERE edition_id IS NOT NULL;
CREATE TABLE reading_sessions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  device_id  TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  started_at INTEGER NOT NULL,
  ended_at   INTEGER NOT NULL CHECK (ended_at >= started_at),
  chars_read INTEGER NOT NULL CHECK (chars_read >= 0),
  active_ms  INTEGER NOT NULL CHECK (active_ms >= 0)
) STRICT;
CREATE INDEX reading_sessions_current ON reading_sessions(user_id, book_id, device_id, ended_at);
CREATE INDEX reading_sessions_user ON reading_sessions(user_id, started_at);
CREATE INDEX reading_sessions_book ON reading_sessions(book_id);
```

Steps:
1. `internal/library`: `Book.Kind`, consts `BookAudio`, `BookText`; `bookCols` reads it; `InsertBook` writes it (empty = audio); `UpdateBook` never changes it.
   `Repo.CreateTextBook(ctx, TextBookIn{LibraryID, FolderID, RootPath, Title, ID})`.
2. `scan/scan.go:603`: at the top of the sweep loop `if b.Kind == library.BookText { continue }`, with the reason in a comment. Audio groups never claim a text book.
3. `ebook/textbook.go`:
   - `ensureBook(ctx, f File, ed Edition) (string, error)`: one tx, repeats `book_id IS NULL AND state='ready' AND missing_since IS NULL`. Root: `f.Path` for a library file; for
     an upload file `filepath.Join(s.ebooksDir(), f.ID)` (absolute, unique, no file there) with folder = the library's oldest folder. No folder at all: leave `book_id` NULL and log; it shows on `/admin/ebooks`.
     Title `ed.Title`, else the file name stem. Sets `book_id`, `paired_by=NULL`, `book_default=1`. The id is `f.MergedFrom` when set and free, else new. Then followers (`OnBookCreated`).
   - `syncTextBooks(ctx, libraryID)`: after `Reconcile`: `kind='text'` with no file `missing_since IS NULL AND state IN ('ready','pending')` gets `missing_since`; with one, cleared.
   - `Dismiss` of the last file of a text book deletes the book.
4. `pair_write.go`: `job.go` after a ready write: `pairFile`, then `ensureBook` if `book_id` is still NULL (`hold` rows too). `Service.Finisher`: `PairPending`, `ensureBooks` (the catch-up for existing unpaired files: this is how today's inbox rows become books), `EnqueuePending`.
   Candidates: Decide over `kind='audio'` keys first, then over `kind='text'` (an EPUB and a MOBI of one book are one book). `pairByFolder`: only `kind='audio'` books; a `.pdf` uses the same-folder rule only, not the parent rule.
5. **Automatic merge.** `PairPending` second pass: for each text book, its default file with `hold IS NULL AND paired_by IS NULL`: `Match` against audio keys (with not-same), `Decide`. Folder: same-folder rule of `pairByFolder` counts as confident. Confident and guard passes → `mergeBook(ctx, from, to, "")`. Otherwise nothing: it is a suggestion on `/admin/ebooks` (B3). `Finisher` runs it; new hook `metadata.Service.OnLocal(func(ctx, bookID))` after a commit that changed a book calls `ebook.PairBook(ctx, bookID)`, because an audiobook's title is only right once its tags are read.
   Guard (all must hold, in SQL, inside the merge tx):
   ```sql
   NOT EXISTS (SELECT 1 FROM grants WHERE book_id = :from)
   AND NOT EXISTS (SELECT 1 FROM share_links WHERE book_id = :from)
   AND NOT EXISTS (SELECT 1 FROM grants g JOIN shelf_items s ON s.shelf_id = g.shelf_id WHERE s.book_id = :from)
   AND NOT EXISTS (SELECT 1 FROM shelf_items i JOIN shelves v ON v.id = i.shelf_id WHERE i.book_id = :from AND v.shared = 1)
   AND ( (SELECT library_id FROM books WHERE id = :from) = (SELECT library_id FROM books WHERE id = :to)
         OR NOT (EXISTS (SELECT 1 FROM reading_positions WHERE book_id = :from)
              OR EXISTS (SELECT 1 FROM highlights WHERE book_id = :from)
              OR EXISTS (SELECT 1 FROM bookmarks WHERE book_id = :from)) )
   ```
   Reasons: a book grant would start to cover audio; a shelf item moved onto the audiobook would extend that shelf's grant; across libraries a person may see one side only. Failing the guard means a suggestion, never an error.
6. **`mergeBook(ctx, from, to, userID)`**: one tx. Requires `from` is `kind='text'`, `to` is `kind='audio'` and live (else `ErrBookNotFound`). Never opens a file.
   ```sql
   UPDATE ebook_files SET book_id=:to, merged_from=:from, paired_by=COALESCE(:reason,'person'), book_default=0, updated_at=:now WHERE book_id=:from;
   INSERT INTO reading_positions SELECT user_id, :to, edition_id, chapter_idx, sid, off, quote, char_pos, percent_bp, finished_at,
     client_updated_at, server_updated_at, device_id, device_label FROM reading_positions WHERE book_id=:from AND true
     ON CONFLICT(user_id, book_id) DO UPDATE SET edition_id=excluded.edition_id, chapter_idx=excluded.chapter_idx, sid=excluded.sid, off=excluded.off, quote=excluded.quote,
       char_pos=excluded.char_pos, percent_bp=excluded.percent_bp, finished_at=excluded.finished_at, client_updated_at=excluded.client_updated_at,
       server_updated_at=excluded.server_updated_at, device_id=excluded.device_id, device_label=excluded.device_label
     WHERE excluded.client_updated_at > reading_positions.client_updated_at;
   UPDATE reading_sessions SET book_id=:to WHERE book_id=:from;
   UPDATE highlights SET book_id=:to WHERE book_id=:from;
   UPDATE bookmarks SET book_id=:to WHERE book_id=:from;
   UPDATE OR IGNORE shelf_items SET book_id=:to WHERE book_id=:from;
   UPDATE merged_books SET to_id=:to WHERE to_id=:from;
   INSERT OR REPLACE INTO merged_books(from_id, to_id, merged_at) VALUES (:from, :to, :now);
   DELETE FROM books WHERE id=:from AND kind='text';  -- cascades: positions, choices, covers, metadata, labels, leftover shelf rows, grants, links
   ```
   Then audit `ebook.merge` with `{from, to, by: auto|person, positions, highlights, bookmarks, shelves}`. `progress` is never touched: a text book has no such row. The audiobook's details, people and cover win; a text book's typed details are dropped (the audit keeps the id). If the audiobook has no cover, enqueue its cover job (step 9 picks the ebook's). A person's merge (`MergeBook`, needs `EditBook` on both; B3) skips the guard, and grants on `from` go with it; the page says so first.
7. **Split ("Not the same book"), `Unpair` extended**, same tx as today: the file, and every file with the same `merged_from`, leave the audiobook. Their new book: `kind='text'`, id `merged_from` when set (old links work again; delete its `merged_books` row), else new. `hold='person'`, `ebook_not_same` row for each (so no automatic merge and no suggestion ever returns; `Pair` or a person's merge clears it, as today). Move back rows whose `edition_id` belongs to those files: `reading_positions` (`UPDATE OR REPLACE`), `highlights`, `bookmarks`. Shelf rows stay with the audiobook. Sessions stay. Audit `ebook.unpair` gains the new id.
8. **Alias.** `library.Repo.MergedInto(ctx, id) (string, bool)`. `s.loadBook`'s not-found path: for `GET` on `/books/{id}` and `/books/{id}/read` answer 308 to the same path on `to_id` only when `to_id` passes the same `share.Can`; otherwise the normal 404. `/reading/{bookID}` (RD1) resolves the alias before its gate.
9. **Metadata.** `internal/metadata/values.go`: `SourceFile = "file"`, precedence `user, json, opf, txt, nfo, file, tags, folder, online`. `local()` branches on `b.Kind`: a text book needs a live file instead of live audio, reads `library.Tx.TextMeta(bookID)` (default edition's columns plus `meta_json`) into title, subtitle, authors, language, isbn, asin, description, publisher, year, series; `FromPath(..., single=true)` gives the `folder` values only when the root is inside the folder. Sidecars and a folder picture are read only when `filepath.Dir(root)` holds exactly one ebook name stem (Calibre's `Title (id)/`); a flat folder of EPUBs never lends one book's sidecar to another. Paired audiobooks: unchanged, the ebook feeds nothing.
10. **Covers.** `ebook.CoverBytes(ctx, f File, lim Limits) ([]byte, error)`: EPUB through `safezip`: OPF `properties="cover-image"`, then `<meta name="cover">`, then the first spine image of a `cover` page. FB2 `coverpage` and MOBI/AZW3 EXTH 201 when `mobi.go` already reaches the record; otherwise none. 20 MiB cap; JPEG, PNG or WebP by sniffing, SVG refused. `mediajobs.Covers` gains `TextCover func(ctx, bookID) ([]byte, error)`, wired in `cmd/tendshelf/hooks.go`; `pick`: folder picture (only under step 9's rule), audio embedded, then `TextCover`, stored by `check(data, library.CoverEmbedded)`. An audiobook without a cover takes its paired ebook's.
11. `internal/progress/reading.go`: `ReadingPosition`, `Reading(ctx, user, book)`, `SaveReading(ctx, ReadingSave) (ReadingPosition, bool, error)` (newest `client_updated_at` wins, same rule as `Save`; future stamp clamped the same way), `SetReadingFinished`, `AddReadingTime(ctx, user, book, device string, chars, activeMs, now int64)` (extends the session with `ended_at` within 30 min, else a new one), `ReadingSpeed(ctx, user) (cpm int, learned bool)` (sum over the last 30 sessions with `active_ms >= 60000`; `learned` from 3 sessions; clamp 300..3000; default 1000).
12. Docs: `AGENTS.md` "Audio files are the user's" and architecture rows for the new tables.

Tests: `migration_0021_test.go` (`TestMigration0021FromPrevious`: books are `audio`; `TestMigration0021FromEmpty`: bad kind, `percent_bp` 10001, duplicate key). `TestTextBookCreated` (ingest, no match → book, title, default, listed, in search), `TestTextBookHoldRows` (existing `kept` and `person` rows get books on Finisher, no merge), `TestTextBookScanKeeps` (full and scoped scan neither deletes nor marks it; the audio sweep ignores it), `TestTextBookMissingAndBack`, `TestTextBookFlatFolder` (three EPUBs in one folder are three books, sidecar ignored), `TestTextBookTwoFormats` (EPUB and MOBI of one title join), `TestTextBookMetadataPrecedence` (user beats opf beats file beats folder), `TestTextBookCover` (EPUB 3, EPUB 2, SVG refused, hostile size), `TestMergeConfident` (ISBN; title plus surname; folder), `TestMergeUnsureStays` (two audiobooks match: nothing moves), `TestMergeMovesData` (position newest wins both ways, highlights, bookmarks, shelf with and without the audiobook on it, sessions, alias), `TestMergeGuard` (table: book grant, shelf grant, shared shelf, share link, other library with data, other library without data), `TestMergeAfterTags` (OnLocal path), `TestMergeNeverWritesFiles` (0555 dir, 0444 file, size, mtime, SHA-256; pattern of `TestPairNeverWritesFiles`), `TestMergeRace` (`-race`: person's `Unpair` against the engine), `TestMergeNoWiden` (a member with `can_read` on the library still 404 on the audio of a book in a library they lack), `TestSplitRemembered` (same id back, marks back, next `PairPending` leaves it, no suggestion), `TestSplitTwoFiles`, `TestReadingRepoNewestWins`, `TestReadingRepoSpeed` (table incl. clamp and the 3-session rule), `TestReadingRepoPrivate`.
Gates: `go test -race ./internal/{db,ebook,library,scan,metadata,mediajobs,progress}/...`, `make check`, `make build`.
Out of scope: pages (B2, B3), PDF scanning (B3), `meta_json` filling (RD1 writes it; B1 tests set it directly).

## B2 Book surfaces and player icon

**The rule for mixed formats.** One resume button per book on any screen. `lastFormat(p, rp)` compares `progress.client_updated_at` and `reading_positions.client_updated_at`; no rows: `audio` when the person may listen, else `text`. Primary: that format ("Resume" and the existing play labels; "Continue reading" with `Chapter {n} · {pct} %`, "Start reading" with no position, "Read again" when finished). Secondary (`ts-btn`, no place, no time): "Read" or "Listen" for the other format, only with the right. Home, series rows and cards show only the primary.

Steps:
1. `components/read_button.templ` `ReadButton(ReadButtonView{Href, Label, AriaLabel string; Secondary bool})`: a plain link, boosted, full page. `iconset.go`: `book-open`, `speaker` (1.5 px line set, like its siblings).
2. `play_link.go` `bookPlayView`: `kind='text'` builds `BookPlayView{Read}` with no `Button`, `Queue`, or Bookmarks; mark control posts `/hx/books/{id}/finished`, which sets `SetReadingFinished` for text books. Audio books apply the rule. Ruler for text: percent, no ticks. `playableFor`: `b.Kind == BookText` → `ErrNotFound` (invariant 8).
3. `routes_book.go`: text book has no Chapters, Bookmarks tabs, write sheet, or Download; the Text tab is first after About. `bookHeader`: format lines per ReaderBook boards: "Audiobook · {duration}", "Ebook · {n} pages", with no Read-along tile. A person with no `ReadText` on a text book sees NEW "You can see this book, but reading it needs permission." and no button.
4. `/books/{id}/share`: no link section for text books (a link page has no reader); grant checkbox copy "Also let them read"; a new grant ticks it by default (operator decision 3).
5. Home: `library.ContinueBooks(ctx, sc, user, limit)` unions `ContinueListening` and text rows by client time, one row per book with its last format, text only when not finished and `percent_bp > 0`. `homeHeroView`: text hero: eyebrow "Continue reading", `Where` "Chapter {n} · about {t} left in chapter · about {t} left in book", `Read` button. `homeSeriesView`: rows with no playable file have `Play == nil`; check for a nil deref.
6. `library/browse.go`: The four `Show` predicates (`browse.go:277-285`: `ShowUnfinished`, `ShowNotStarted`, `ShowNotFinished`, `ShowFinished`) also read `reading_positions`, through one helper. `MinDurationMs`/`MaxDurationMs` add `b.kind='audio'`. Sort by duration stays as it is, so text books (duration 0) come last descending and first ascending; a kind term would break the keyset cursor. New query `Format` (`audio|text`), URL `format=`, in the filter header (decision 0008). Cards: `Duration` slot "Ebook"; progress bar from `percent_bp`. `BookRow.Kind`.
7. `/api/v1/books/{id}`: `kind`, `formats: ["audio","text"]`, `reading: {percentBp, updatedAt}`. Existing fields unchanged. `downloads.js` needs no change: the Download button is hidden on text books.
8. `GET /books/{id}/text/{fileID}/file` (`DownloadText`, ready file on this book, else 404): the original bytes, `Content-Disposition: attachment`, `no-store`; on the Text tab "Download this ebook". Research mistake 10. A refused file never.
9. **Player book icon (operator requirement 1).** `PlayerDataView.ReadHref`, set in `getPlayer` only when `ReadText` and the book has a ready edition, else empty; `data-read-href` on `#player-data`. `player.js` `parseBook`: `readHref: d.readHref || ""` (about 25 B; row 13,312, used 13,232; a real overrun is reported, not squeezed). `player.templ`: right after `data-ts-bar-chapters`, `<a class="ts-iconbtn" data-ts-bar-read href="/" hidden aria-label={ m.OpenReader }>` with `book-open`. `player_full.templ`: after `data-ts-open-chapters`, `<a class="ts-player-bar__item" data-ts-full-read href="/" hidden>` with the icon and label. `player-full.js` (row 13,568, used 13,087): on `ts:player:state` sets `href` and `hidden` on both from `tsPlayer` book copy; add `readHref` to the copy. Copy NEW `OpenReader` "Read this book". Link mode (share links) leaves it empty. The tools group exists only from 1280 px, so on a phone the icon is in the full player and the mini row is unchanged (operator decision 4). At 1280 px check that the bar still fits.
10. Queue: `QueueMenuView` needs a button, so a text book has none; `POST /api/v1/queue` hits `playableFor`.
11. `vision.md` lines 27, 51-52, 71-72 reworded; `AGENTS.md` "One resume button" line gets the rule.

Tests: `TestFormatsResumeRule` (table: no rows, listened last, read last, finished, no `ListenBook`, no `ReadText`; exactly one primary), `TestFormatsTextBookPage` (no Chapters, Bookmarks, write, download; role matrix owner, admin, member with and without `can_read`, guest), `TestFormatsTextNotPlayable` (`/progress`, `/hx/player`, queue 404), `TestFormatsHomeContinue`, `TestFormatsFilters` (each Show, duration, format; a member's counts exclude hidden books), `TestFormatsAPI`, `TestFormatsDownload` (rights, refused 404), `TestPlayerReadIcon` (`data-read-href` with `ReadText` and ready edition; absent without either; absent for link visitors), `TestPlayerReadIconBrowser` (opt-in, 1280 and 390: bar and full player show it, right of chapters, name announced, opens the reader, hidden after loading a book with no ebook). Matrix rows, a11y rows, visual beside `ReaderBook*`. Page parity per AGENTS at 390, 768, 1024, 1440.
Gates: `make generate`, touched tests, `TestJSBudget`, `make check`, `make build`, `make a11y` on the touched rows.
Out of scope: sending a book by link, a reading dashboard.

## B3 Add books and `/admin/ebooks`

Steps:
1. **Scanner and PDF.** `scan.splitEbooks`: a `.pdf` is an ebook only when its directory holds no audio file. `walk` drops `KindOther`, so add `media.KindLooseEbook` for `pdf`; `splitEbooks` takes such a file out of the walk result always, and hands it on as an `EbookFound` only when no audio file sits in its directory. A PDF beside audio is a booklet and stays ignored. Operator decision 2.
2. `upload/manifest.go`: `kept` adds `media.KindEbook` and `.pdf`, with `.kfx`, `.kfx-zip` refused `copy_protected` by extension (KFX is always refused). `audio == 0 && ebooks == 0` → `no_audio` text NEW "There is no audio or ebook file in it." `split.go` and `zip.go`: an ebook in the top level counts like audio for "each subfolder is one book"; several ebooks with no audio make one folder, each its own book by the scanner's rule. TXT, MD, HTML and DOCX stay Text-tab uploads (no scanner rule can tell them from notes).
3. `upload/place.go`: staged ebooks run `ebook.Read(ctx, part, ext, lim)` in the placement job. `drm` → `copy_protected`, staged bytes deleted (AGENTS rule). `damaged`, `no_text`, `unsupported`, `too_large` → refused NEW "Not added. Tendshelf could not open {file}." Nothing reaches the library folder. Name source: first audio's tags, then the first ebook's `Meta`, then the file name. Then the existing publish and scoped `scan.library`.
4. `upload.Removable` and `Remove`: count live `ebook_files` with a path in `upload_paths` as well as audio; `live > 0 && live == owned`. `Remove` deletes the `ebook_files` rows of deleted paths in its tx, before the book row, so `ensureBooks` cannot revive them. `upload.js`: accept list and the refusal copy; `say("refused-none")` text. Row stays 5 KB. Page copy NEW "Audio files, ebooks, pictures and book details files. Up to 50 GB a book."
5. **`/admin/ebooks`** (`routes_ebook_inbox.go`, `pages/admin_ebooks.templ`; keep URLs; nav label "Ebooks"). Sections: **"Maybe the same book"**: text books whose default file has `hold IS NULL` and a `Match` candidate that `Decide` would not take or the guard refused (`ebook.Store.Suggestions(ctx, filter, limit)`, 100 rows, 5,000 scanned; ever filtered by `BookFilter`). Each row: `PairingCard` with `Checks`, "Make one book" (`POST /admin/ebooks/{fileID}/pair`, field `book`, runs `MergeBook`), "Not the same book" (`.../not-same`), "Keep it as an ebook on its own" (`.../keep`). When the guard refused, NEW "People who can see this ebook today lose it, or may gain the audiobook. Check who has access first." **"Could not be opened"**: pending, refused, failed files with the R1 sentence; `failed` and `damaged` with a download link (`ManageLibraries`). "Kept on their own" is gone: those are ordinary books. `hold='kept'` now only means "no suggestions".
6. `GET /admin/ebooks/{fileID}` works for a file on a text book; heading stays "Which audiobook is it?"; search limited to `kind='audio'`. Text tab of a text book, for editors: "Find the audiobook" link to it. Text tab of a merged book: "Not the same book" as today, now splitting. `/admin/libraries`: "Ebooks" link, no count.
7. Copy: deck "It goes on the shelf as a book you can read" returns under Keep; R2's NEW replacement goes. Primary button NEW "Make one book".

Tests: `TestAddEbookManifest` (ebook only, mixed, kfx, no book file), `TestAddEbookPlace` (fake: name from `Meta`, publish, scoped scan, book appears), `TestAddEbookRefusals` (ADEPT, damaged: staged bytes gone, folder untouched), `TestAddEbookPDFRule` (beside audio ignored; alone a book), `TestAddEbookRemove` (changed file kept; row gone; book not revived), `TestEbookAdminSections`, `TestEbookAdminMerge` (needs EditBook on both, moved, audit), `TestEbookAdminNotSame`, `TestEbookAdminRoles` (member, guest 404), `TestEbookAdminFilter` (a hidden audiobook never appears as a candidate). Visual beside `NoMatchPhone`, `AddEbook*`.
Gates: as B2 plus `go test -race ./internal/upload/ ./internal/scan/`.
Out of scope: bulk merge, ebook uploads through the API.

## RD1 Document v2 and reader server

**Reused**: `reader_render.go`, `routes_reader.go`, `reader.templ`, `readerDocCache`, the canonical document, `ebook.ParseSID`. Chapter HTML is sanitised by not existing: the parsers already reduce every part to blocks, and the renderer escapes every byte (`html.EscapeString`, templ).

1. `ebook/doc.go`: `Recipe = "tendshelf-ebook-v2"`. New fields, `omitempty`, so v1 documents still hash and load: `Block.Links []Link`, `Block.ImgW, ImgH int`; `Link{Start, End int; To string; URL string; Note bool}`; `Meta.Description, Publisher, Series, SeriesIndex string; Year int; CoverPart string`.
2. `htmlblocks.go`: track `a href`, `id` and `name` attributes on blocks and inline elements, `epub:type="noteref"`, `<sup>` ancestors. `builder.Finish` resolves `part#id` to a block ID: `To`, `Note` true when `noteref`, the target is `KindNote`, or the anchor sits in `sup`. External URLs keep only `http`, `https`, `mailto`. An unresolved link is dropped, its text stays. `epub.go`: `dc:description`, `dc:publisher`, `dc:date`, `calibre:series`, `belongs-to-collection`; image dimensions through `image.DecodeConfig` (JPEG, PNG, GIF; WebP header read by hand).
3. `job.go`: write `meta_json`. Re-read: `EnqueueStale`, in `Finisher` and at start-up: `UPDATE ebook_files SET state='pending', job_id=NULL WHERE state='ready' AND sha256 IS NOT NULL AND NOT EXISTS (edition with this file, sha256 and Recipe)`. While pending, the reader keeps serving the old edition: `EditionFor` falls back to the newest edition of a pending file.
4. `ebook.Reanchor(d *Doc, quote, prefix, suffix string, near Anchor) (Anchor, bool)` (pure): exact text at `near`; else a unique match in the same chapter; else unique in the book; else false. Ingest tx, when an older edition of this file exists: re-home `highlights`, `bookmarks`, `reading_positions` through it. A mark that fails keeps its row with `edition_id` NULL (it stays in the marks list with its quote and no jump; a position falls back to `percent_bp`). Then delete the old edition and its `<data>/text` document if no edition shares it. Doc read for this is capped by `maxDocBytes`.
5. `reader_render.go`: `rsParts(ch) []rsPart`: split at block edges past 40,000 UTF-16 units. `Link` runs become `<a class="ts-reader__link" href="?ch=&part=&at=#...">` with `data-note-href` when `Note`; external `rel="noopener noreferrer" target="_blank"`. Links join the styles in `rdrSegments`; invariant "text nodes inside `.ts-s` equal the text" holds. Each block carries `data-c`, its chars before it in the chapter (RD2 needs it).
   Images: EPUB `KindImg`: `<img src width height alt loading="lazy" class="ts-reader__image">`; other formats keep alt text. `lang={doc.Meta.Language}` on the reader root (hyphenation).
6. Routes in `routes_reader.go`, all behind `readerCtx`:
   - `GET /books/{id}/read`: query `ch`, `part`, `at`, `off`, `sheet`. Start place: `at`, else `reading_positions` on this edition, else `Reanchor` of its quote, else `percent_bp`, else first non-front chapter. Root data: `data-at`, `data-off`, `data-cpm`, `data-learned`, `data-total-chars`, `data-before` (chars before this part), `data-chapter-chars`, `data-save="/reading/{id}"`, `data-flow` from settings.
   - `GET /hx/books/{id}/read/chapters/{idx}?part=`: section with `data-prev-href`, `data-next-href`, `data-before`, plain links for no-JS. Scroll mode sentinel: `hx-trigger="ts-reader-next"` (reader.js fires it through an `IntersectionObserver`; without JS the link shows).
   - `GET /books/{id}/read/images/{blockID}?edition=`: block found in the doc, its `Img` read by `ebook.OpenImage` through `safezip` (the URL never names a zip path). JPEG, PNG, GIF, WebP by sniff; SVG and others 404. `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `Cache-Control: private, max-age=86400`. 20 MiB cap.
7. `routes_reading.go`, on the `/progress` pattern (same CSRF and token rules, works with `sendBeacon`): `GET /reading/{bookID}`, `POST /reading/{bookID}` fields `edition_id, chapter_idx, sid, off, quote, char_pos, client_updated_at, device_id, chars_read, active_ms`. `ParseSID` and ranges else 422; `char_pos` clamped to `char_count`; `percent_bp` computed here. Alias resolved first (B1 step 8). Text is the only format; a book with no `ReadText` is 404. This is the API other clients read (research mistake 5).
8. **Speaker (operator requirement 2).** Reader bar: when the book has playable audio and `ListenBook`: `components.PlayButton` Round with new `Icon` field (`speaker`), from the same `playLinkIn(book, files, p)`, `AriaLabel` NEW "Play the audiobook". It starts at the book's own `progress` place; `player.js` needs no change and shows pause while it plays. No chapter jump. The reader page keeps the player elements in the layout, so audio survives; stopping it uses this same button. Nothing here writes `reading_positions` or `progress` beyond normal playback.
9. `reader.templ` bar: Close link, title, speaker, Contents, Marks, Search (RD3), Aa. Footer: progress ruler, `p. {n} of {total}`, time-left line target. `edition` chips unchanged.
Tests: `TestDocV2Links` (internal, noteref, sup, unresolved, `javascript:` dropped), `TestDocV2Determinism`, `TestDocV2MetaImages`, `TestDocV2StaleReread` (v1 doc still served, then v2, marks moved), `TestDocV2Reanchor` (table), `TestReaderServeHostile` (`<script>`, `onerror`, `javascript:`, CSS, SVG with script in a fixture EPUB: no such byte in any answer; CSP header unchanged), `TestReaderServeParts` (one 2 MB chapter: every part under 60 KB of text, continuous, `at` finds its part), `TestReaderServeImage` (rights, other edition 404, traversal block ID 404, SVG 404, headers), `TestReaderServeResume` (each start-place rung), `TestReaderServeReading` (newest wins, alias, 422s, member without `can_read` 404, user B unaffected), `TestReaderSpeaker` (shown with audio and `ListenBook`; absent for a text book or without the right; links to the audio place, not the reading place). Matrix rows for every route. Gates: `make generate`, `go test -race ./internal/ebook/ ./internal/web/ ./internal/progress/`, `make check`, `make build`.
Out of scope: client, search, notes, publisher CSS, tables, page lists.

## RD2 Paginator client

Files: `static/js/reader.js`, `reader-pages.js`, `css/pages-reader.css`; `reader-settings.js`, `users/reader_settings.go`, `pages/reader_settings.templ`. Modules mount through `data-ts-module`; each stops when its element disconnects. CSSOM only (`setProperty`), no inline style attributes; banned as AGENTS says.

1. **`reader.js`** `mount(root)`: flow = setting, `auto` = pages below 1280 px, else scroll. Imports `reader-pages.js` only for pages. An adapter interface: `{anchor(), show(sid, off), next(), prev(), atEnd(), atStart()}`; the scroll adapter (about 40 lines) lives here. Handles: save, input, progress, resume.
2. **Anchor** = first text visible. Binary search over the part's `.ts-s` by `getBoundingClientRect`, then bisect text offset with a `Range`; about 25 layout reads. Off = `data-o` + offset. Quote = next 64 chars. `char_pos` = `data-before` + block's `data-c` (RD1 adds it) + off.
3. **Save**: debounced 3 s after a turn, and on `visibilitychange` hidden and `pagehide` through `sendBeacon` to `data-save`. Offline or failed: keep the last in `sessionStorage`, resend on `online`. `chars_read` counts only forward turns of at most 3 pages; `active_ms` counts visible time per turn, capped at 120 s.
4. **Another device**: on `visibilitychange` visible, `GET /reading/{id}`; newer with another `device_id` shows NEW "You read further on {device}." with "Go there" and "Stay here" (`ts-toast` pattern). Never jumps by itself.
5. **`reader-pages.js`**: `.ts-reader__text` gets `column-width`, `column-gap`, `height` from custom properties `--ts-col`, `--ts-gap`, `--ts-page-h`; columns per screen = `floor((w + gap) / (45ch + gap))`, clamped to 1..2, and at most 1 below 1024 px; measure capped by `--size-read-measure` (68ch; phone width minus 24 px a side). Page width = column plus gap. Turn: `scrollLeft = page * step`. Page count: `ceil(scrollWidth / step)`. Show: `Range` rect left to page. Past the end: fetch `data-next-href` (via `fetch` of the fragment, replacing the section), first page; before the start: last page. Prefetch the next part when two pages remain.
   Layout changes (resize debounced 150 ms, `document.fonts.ready`, setting change, highlight swap): take the anchor, re-lay, `show`. Motion: 180 ms slide with `transform` on swipe, none with reduced motion. CSS caps: `img { max-width: 100%; max-height: var(--ts-page-h); break-inside: avoid }`, `pre { white-space: pre-wrap }`, headings `break-after: avoid`.
6. **Input** in `reader.js`: tap zones outer 25 % left and right, middle toggles chrome (`data-chrome`); `tapSwap` mirrors them. Ignored on a link, a mark, or with a selection. Keys through a `window` capture listener, so `player.js` does not see them while the reader is open and no field or dialog has focus: ArrowRight, PageDown, Space next; ArrowLeft, PageUp, Shift+Space previous; `[` `]` chapter; `t` contents; `/` search. Swipe: pointer events, 48 px, `touch-action: pan-y`. Scroll mode: tap middle only; keys scroll a screen minus two lines.
7. **Progress**: ruler `percent`; `p. {n} of {total}` from `data-pg`; time left chapter and book from `data-cpm`, chars remaining; "about" until `data-learned`; under 1 h in 5 min steps. Ruler has chapter ticks.
8. **End of book**: last page shows NEW "That's the end of the book." with "Mark as finished" (form to `/hx/books/{id}/finished`). Marked by the person, never by itself.
9. **Settings** (`reader.v1`, same key, extra fields): `Margin` narrow|normal|wide, `Justify` bool (default off, per HANDOFF), `Hyphens` bool (default on), `Flow` auto|pages|scroll, `TapSwap` bool. Sheet rows with these labels: "Margins", "Justify text", "Hyphens", "Page or scroll", "Swap tap sides". The existing theme, size, leading, font rows stay. Each row is a class or data attribute on `.ts-reader`.
10. No-JS: the canonical page is one part with next and previous links; settings form posts; all reading works.
`motion_test.go`: rows below. Tests (opt-in `TENDSHELF_TEST_BROWSER`, `-tags dev`, 390 and 1440; helpers `pgSeed`): `TestPaginatorTurn` (each page's visible text differs; nothing clipped between pages), `TestPaginatorResumeAcrossSizes` (anchor at 390, reopen at 1440, then 18 to 22 px: the anchored sentence is on screen every time), `TestPaginatorSwitchFlow` (pages to scroll and back keep the place), `TestPaginatorChapterEdge` (forward and back across a part edge), `TestPaginatorLongChapter`, `TestPaginatorImage` (tall image fits the page), `TestPaginatorSaveBeacon`, `TestPaginatorOtherDevice`, `TestReaderInputTaps` (incl. swap; a link tap follows the link), `TestReaderInputKeys` (player keys silent; focus in a field leaves keys alone), `TestReaderInputSwipe` (touch emulation), `TestReaderInputTimeLeft` (default then learned speed). Always run: `TestJSBudget`, `TestCSSBudget`. Gates: those, `go test -race ./internal/users/ ./internal/web/static/`, `make check`, `make build`.
Out of scope: right-to-left, two-page spreads below 1024 px, the skim scrubber (G4), WebKit automation (RD4 records a manual check).

## RD3 Marks, footnotes, search, export, errors

1. `reader-marks.js` from d02e83e: drop "Listen from here", `H`/`N` events; add four colour swatches (`colour` exists) and a bookmark-this-page button on the chrome (`POST /books/{id}/bookmarks` with `sid`). Offsets unchanged.
2. Bookmarks: `progress.Bookmarks` for the Bookmarks tab excludes `sid IS NOT NULL`; the marks sheet lists them. `/api` bookmark JSON gains `sid`, `editionId`. Sync of highlights stays later.
3. **Footnotes**: `reader-notes.js`, imported on the first `[data-note-href]` click. `GET /hx/books/{id}/read/note/{blockID}?edition=` returns the target block plus following `KindNote` blocks until the next anchor, at most 10, as a fragment. `<dialog class="ts-note">`; "Go to the note" jumps; Escape and backdrop close; focus returns to the link. Non-note internal link: jumps and shows NEW "Back to where I was" (chip in `reader.js`, reused by G4). Without JS it is a link.
4. **Search**: `?sheet=search&q=` full page and `GET /hx/books/{id}/read/search?q=&edition=`. Over the cached `Doc`: `textmatch.Fold` per block with an offset map to UTF-16, whole phrase, at least 2 characters, at most 200 hits, chapter title and snippet of 80 characters, in order, `q` escaped. Link: `?at=&off=`, and the hit is marked with `::highlight(ts-hit)` when supported. Needs no client beyond htmx.
5. **Export**: `GET /books/{id}/read/marks.md` and `.json` (`ReadText`; own rows; `Content-Disposition: attachment`; book title, chapter, quote, note, colour, created date; deleted rows never). Marks sheet link NEW "Export highlights and notes". A person's whole-library export is later.
6. **Plain errors** in the reader: document missing or corrupt → page with NEW "Tendshelf cannot open this book right now." and the download link; `ErrDocTooLarge` the same; a part that fails to load: "This part did not load." with Try again. A protected file keeps the existing refusal sentences.
Tests: `TestReaderNoteFragment` (rights, chain limit, other edition), `TestReaderNoteBrowser` (focus return, Escape), `TestReaderSearch` (accents, case, emoji offsets, 200 cap, `<script>` in `q`, no hits sentence, one edition only), `TestMarksExport` (md and json bytes; user B empty; deleted gone; title with a quote mark in the header), `TestReaderMarksBrowser` (colour, note, offsets across a part edge, swap keeps the page), `TestReaderErrors`. Gates as RD2.

## RD4 Captures, a11y, contrast, layout time

1. `a11y_pages_test.go`: reader rows `reader-pages`, `reader-scroll`, `reader-contents`, `reader-marks`, `reader-settings`, `reader-search`, `reader-note`, text book page; 0 findings at four widths in four themes. Keys and swipe have button equivalents. Screen readers get real headings and `<p>`; page turns announce nothing.
2. `TestBRContrast` (`--read-ink` on `--mark` in each of four colours, `--selection`, each theme; 4.5, or 7 in contrast). Fix per R4's rule: token lightness, listed.
3. `TestBRVisual` beside ReadPhone, ReadTablet, ReadDesktop, ContentsPhone, MarksPhone, ReadSettingsPhone, RosaReadPhone, ReaderBook*, seeded ids. Differences declared in the report (see below).
4. `TestBRLayoutTime` (`TENDSHELF_TEST_PERF=1`, CPU 4x): a 40,000-unit part lays out under 300 ms; turn under 16 ms p95. JSON under `.local/tests/perf/<run>/`.
5. `perf_weight_test.go`: reader row. Sweep dead `bundle.css` classes (`.ts-voicepill`, `.ts-focus`, `.ts-mode`, `.ts-s--gap`, `.ts-readdock__speed`).
6. Manual, for the orchestrator: WebKit (Epiphany or a Wails build) with one book; the operator's phone with `~/Books` "Rules for the Summer". Recorded in the roadmap.
Gates: `make a11y`, `make visual-full`, `make perf`, `make page-weight`, `make check`, `make build`.

## Budgets (numbers needing approval)

Basis: P4 branch total 64,099 B gzip, `reader-settings.js` 1,279 B, so all other app JS is 62,820 B. `reader-marks.js` measures 2,533 B, `readalong.js` 6,133 B (gone).

| File | Budget | Note |
| --- | --- | --- |
| `reader.js` | 4.5 KB (4,608) | new: anchors, save, input, progress, scroll |
| `reader-pages.js` | 3 KB (3,072) | new, lazy: pages only |
| `reader-notes.js` | 1 KB (1,024) | new, lazy: first note |
| `reader-marks.js` | 2.75 KB (2,816) | was 2.5: colours, bookmark |
| `reader-settings.js` | 1.5 KB (1,536) | was 1.25: new rows |
| Reader total | 12.75 KB (13,056) | was 9.75 |
| **App total, final** | **74.25 KB (76,032)**; expected 75,876 + about 100 for the player | The approved 71.25 KB (72,960) does **not** hold: 3 KB over. |

Without `readalong.js` and with the marks and settings as measured the app is at 66,632 B, which leaves 6,328 B of the old cap for a paginator. These are estimates from `readalong.js` (509 lines, 6.1 KB): if measured files fit 72,960, no raise is taken. If not, report it; never squeeze.
CSS: `pages-reader.css` row 4 KB (default 3 KB); today 2,076 + 972 B, about 3.6 KB after pagination and minus voice rules. Core CSS unchanged.
Fonts: P4 raised `TestFontBudget` from 1.3 to 1.6 MB (1.57 MB used). **Unapproved: operator to accept**, or keep OpenDyslexic out.
Later packages (G) ask for their own rows.

## Later packages (one note each; planned when reached)

- **G1 Look-up and word list.** Select a word: definition from a StarDict or plain dictionary file the owner adds under the data folder, read server-side; else a web search the reader chooses, opened by the browser, never fetched by us. Saved words go in a per-person `word_list` table with the sentence quote, exportable. No bundled dictionary.
- **G2 Reading stats.** `reading_sessions` already holds time and characters. A private page beside the listening dashboard (decision 0014 layout): time, speed, streak, yearly goal, finish dates. Family view opt-in, off by default.
- **G3 Print pages or locations.** Parse the EPUB `page-list` into `doc`, mapping each `pagebreak` to a block offset; show the real number. Without it, keep 1,800-character locations, the same on every device. Requires a recipe bump.
- **G4 Skim.** A scrubber with chapter ticks over the footer; any jump keeps the "Back to where I was" chip from RD3 until the person reads on. Previews use chapter titles, no layout of other parts.
- **G5 Comfort.** Warm tint on a schedule (a token overlay), hidden chrome after 3 s, keep the screen on (`navigator.wakeLock`), brightness gesture only where the platform allows; native-feel rules (decision 0011).
- **G6 Offline reading.** Service worker gains `/hx/books/{id}/read/chapters/*`, images and `pages-reader.css`, per person, same model as audio (decision 0012); `downloads.js` downloads an edition; positions queue like progress. `AGENTS.md` SW list changes with it.
- **G7 Collections and what next.** Shelves already exist: series order across formats, "next in series" for text books, a to-read shelf. Sending a book by share link gets a real reader page here.

## Later still

- **OPDS feed**: a read-only catalogue under `/opds`, with app tokens, reusing `BookFilter`.
- **KOReader sync**: a kosync-shaped endpoint with an app key, never the login password; percent to `reading_positions`, and its partial MD5 per file.
- Fixed-layout EPUB and comics (a separate renderer), right-to-left and vertical text, owner-imported dictionary packs, publisher CSS as an option, tables as tables.

## Operator decisions (defaults apply unless changed)

1. Raise app JS to 74.25 KB, `pages-reader.css` to 4 KB, and accept fonts at 1.6 MB (default yes).
2. A PDF in a folder with no audio counts as an ebook in scans (default yes). A library with stray PDFs gains books; a scanned PDF with no text is listed under "Could not be opened" on `/admin/ebooks`.
3. New grants tick "Also let them read" by default; existing grants unchanged (default yes).
4. Phone mini-player gets no book icon: it has no chapters icon or spare room; the icon is in the full player (default yes).

## Boards that no longer fit

Gone: `ReadAlong*` (10), `FocusPhone`, `PreparingPhone`, `AlignFailPhone`, `EyesFreePhone` as a reader mode. Changed: mode switcher on `Read*` and `ReaderBook*`; dock replaced by a footer; Highlight, Follow and View rows on `ReadSettings*`; "text only, not narrated" on `Contents*`; "Play at" and the "same moment in both" lead on `Marks*`; "Pair and prepare read-along" on `AddEbook*`; "Also let them read along" on `ShareReadPhone`; "Ready offline: listen, read, read along" on `ReaderBookOffline`; HANDOFF 209-268 keys and motion. No board: pages, tap zones, footnote dialog, search, end of book, speaker icon, player book icon. Built from BRAND rules and listed for a design pass.

## Operator answers (2026-11-04)

- Budgets approved: app JS total 74.25 KB (76,032 B); `reader.js` 4.5, `reader-pages.js` 3, `reader-notes.js` 1,
  `reader-marks.js` 2.75, `reader-settings.js` 1.5 KB; `pages-reader.css` 4 KB; `TestFontBudget` 1.6 MB.
- Phone: the book icon sits in the full player only; the mini row stays as it is.
- The other decisions take their defaults: PDFs in folders with no audio count as ebooks; new grants tick "read".
- JS budget revised (operator, 2026-11-04): `reader.js` 6 KB, app JS total 74.5 KB (76,288 B).
