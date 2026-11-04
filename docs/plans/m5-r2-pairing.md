# M5 R2 Pairing: plan

Sources: `docs/read-along.md` §1 "Pairing" and §7, roadmap R2, R1 plan (`m5-r1-ebooks.md`), handoff "Read-along reader",
boards `screens/reader/AddEbook{Phone,Tablet,Desktop}` ("Found in your folders" card) and `NoMatchPhone`, component
`design-system/components/PairingCard/README.md`. Exit: pairing table tests; an unpaired ebook never appears as a
book; no ebook file is ever written.

## Packages and order

| Package | Runs | Shared files it touches |
| --- | --- | --- |
| P1 Data and pairing engine (about 220 calls) | first, alone | `internal/db/db.go`, `internal/metadata/online/score.go`, `internal/ebook/{epub,job,reconcile,service}.go`, `NOTICES.md`, `docs/architecture.md`, `docs/read-along.md`, roadmap |
| P2 Text tab: pair, unpair, editions (about 230 calls) | after P1 | `routes_ebooks.go`, `ebook_copy.go`, `pages/book_text.templ`, new `components/pairing_card.templ`, `static/css/bundle.css`, `routes_matrix_ebooks_test.go`, `a11y_pages_test.go`, roadmap |
| P3 `/admin/ebooks` inbox (about 200 calls) | after P2's first commit | new `routes_ebook_inbox.go`, `ebook_inbox_copy.go`, `pages/admin_ebooks.templ`, `routes_admin.go`, `pages/admin_libraries.templ`, `bundle.css`, `routes_matrix_test.go`, `a11y_pages_test.go`, roadmap |

At most two run at once. P3 reuses P2's `PairingCard`, so P2 commits step P2.1 (component and its CSS) first as
`feat(web): pairing card`; the orchestrator merges that commit and may then start P3 beside the rest of P2. CSS:
P2 appends under `/* ebook pairing */`, P3 under `/* ebook inbox */` in `bundle.css`. Each appends its own roadmap
status paragraph under R2; the orchestrator resolves `a11y_pages_test.go` and roadmap merges. Test prefixes (unique,
also for package-level helpers): P1 `TestTextmatch*`, `TestPair*`, `TestMigration0017*` (helpers `pairT*`); P2
`TestTextPair*`, `TestTextEdition*` (helpers `textPair*`); P3 `TestEbookInbox*` (helpers `inbox*`).

## Invariants (all packages)

1. No ebook file is written: pairing changes only rows (`ebook_files`, `ebook_not_same`, `text_choices`, `audit`).
   No code in R2 opens a library or upload file for writing, renames or deletes one.
2. An ebook never becomes a book: no row in `books`, `books_fts`, shelves, home, search, counts or lists. Unpaired
   files are seen only on `/admin/ebooks` (ManageLibraries) and as suggestions on a Text tab (EditBook).
3. Automatic pairing never overrides a person: it only sets `book_id` where `book_id IS NULL AND hold IS NULL` and
   no `ebook_not_same` row names that (file, book). Every automatic write repeats those conditions in its `WHERE`.
4. Automatic pairing needs one candidate: folder (existing rule), then identifier, then title + author surnames.
   Two or more candidates at the deciding level are suggestions, never applied.
5. Authorization: inbox routes `share.ManageLibraries` on `TargetServer` (`s.allowLibraries`); pairing to a book
   also `share.EditBook` on that book (`s.loadBook`); pair, unpair, not-same, default on the Text tab `EditBook`;
   choosing an edition for oneself `ReadText`. Refused or mismatched (file not on that book) answers 404.
6. Candidate and search queries over books include `s.can.BookFilter(u, "b")` in SQL; matching then runs in Go only
   over rows the filter returned. The engine (no person) reads live books (`missing_since IS NULL`) of all libraries.
7. A person's edition is: their `text_choices` row if that file is still paired to the book and `ready`; else the
   book's default (`book_default = 1`, ready); else the oldest ready file (`created_at, id`). Never another book's file.
8. Each pairing change, its audit row and the clean-up of `text_choices`/`book_default` are one transaction.

## Decisions

- Pairing stays on `ebook_files` (R1 already does this; it survives "read again"). `docs/architecture.md` rows 102-103
  still describe the old editions-based design and are rewritten in P1 to the real schema.
- `hold` column (NULL, `'person'`, `'kept'`): one value says "a person took over" (unpaired by hand) or "kept on its
  own"; a boolean could not tell the inbox sections apart, and `paired_by='person'` with NULL book is ambiguous after a
  book is deleted.
- `ebook_not_same` table: "Not the same book" must stick for that one book (folder pairing would redo it on the next
  scan) without blocking other matches.
- Per-user choice in a new `text_choices` table keyed (`user_id`, `book_id`), like `progress` and `queue_items`:
  `user_settings` has no book dimension and cannot cascade when a book, file or person goes. read-along.md updated.
- Book default as `ebook_files.book_default` with a partial unique index: one flag per book, cleared in the same
  update that unpairs.
- Normaliser: new leaf package `internal/textmatch` (no imports outside the standard library). `fold`, `norm`,
  `ratio`, `splitNames` move there from `internal/metadata/online/score.go` (online calls them; its tests stay
  unchanged and must pass), and `validISBN` moves from `internal/ebook/epub.go`. `internal/ebook` must not import
  `internal/metadata` (it pulls in the online client), and one normaliser beats two. The two `cleanISBN` copies in
  `metadata` stay (out of scope).
- ASIN matches record `paired_by='isbn'` (0016's CHECK has no `'asin'`; SQLite cannot widen it). Documented in the SQL.
- Cross-library pairing is allowed (an "Ebooks" library may hold the text of an "Audiobooks" library's book).
- R2 has no ebook-only books: "Keep it as an ebook on its own" sets `hold='kept'`; the file stays in the inbox's
  "Kept on their own" section. The deck's sentence "It goes on the shelf as a book you can read" would be untrue, so a
  NEW sentence replaces it (operator decision 1).

## Migration `internal/db/migrations/0017_pairing.sql` (P1)

```sql
-- 0017_pairing: read-along R2 (docs/read-along.md §1 Pairing). hold: NULL = automatic pairing may pair the file;
-- 'person' = a person unpaired it; 'kept' = a person kept it as an ebook on its own. Automatic pairing touches only
-- hold IS NULL. book_default marks the book's edition (one per book). ebook_not_same records "Not the same book" for
-- one (file, book). text_choices is each person's edition of a book. An ASIN match is stored as paired_by 'isbn'.
ALTER TABLE ebook_files ADD COLUMN hold TEXT CHECK (hold IS NULL OR hold IN ('person','kept'));
ALTER TABLE ebook_files ADD COLUMN book_default INTEGER NOT NULL DEFAULT 0 CHECK (book_default IN (0,1));
CREATE UNIQUE INDEX ebook_files_book_default ON ebook_files(book_id) WHERE book_default = 1 AND book_id IS NOT NULL;
CREATE INDEX ebook_files_unpaired ON ebook_files(created_at, id) WHERE book_id IS NULL;

CREATE TABLE ebook_not_same (
  ebook_file_id TEXT NOT NULL REFERENCES ebook_files(id) ON DELETE CASCADE,
  book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  user_id       TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (ebook_file_id, book_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX ebook_not_same_book ON ebook_not_same(book_id);

CREATE TABLE text_choices (
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  ebook_file_id TEXT NOT NULL REFERENCES ebook_files(id) ON DELETE CASCADE,
  updated_at    INTEGER NOT NULL,
  PRIMARY KEY (user_id, book_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX text_choices_file ON text_choices(ebook_file_id);
```
`internal/db/migration_0017_test.go`: `TestMigration0017Pairing` opens with `ms[:16]`, `supported: 16`, `seed7`, inserts
`ebookFileSQL("ef_1", ...)` (helper in the 0016 test), closes, reopens at 17: the row survives with `hold IS NULL`,
`book_default = 0`; insert a not-same row and a choice; a second `book_default = 1` file on `bk_1` fails (unique);
deleting `bk_1` nulls `book_id` and removes both rows. `TestMigration0017FromEmpty`: `hold='x'` and `book_default=2`
fail their CHECKs; a duplicate (`user_id`, `book_id`) choice fails; deleting the user removes the choice.

## P1 Data and pairing engine

Steps:
1. `internal/textmatch/textmatch.go`: `Fold(r rune) string`, `Norm(s string) string`, `Ratio(a, b string) float64`,
   `SplitNames(s string) []string` (moved verbatim), `ISBN(s string) string` (moved `validISBN`), `ISBN13(s string)
   string` (valid ISBN-10 converted to 978-prefixed ISBN-13, else the valid 13, else ""), `Surname(name string) string`
   (one person's name: "Last, First" when exactly one comma uses the part before it; else the last word of `Norm`,
   skipping trailing `jr sr ii iii iv`), `TitleKey(title string) string` (drop trailing `(...)`/`[...]` groups unless
   that empties it, cut at the first ": " when the left part is not empty, drop a trailing "a novel" and the words
   "unabridged"/"abridged", then `Norm`). Header comment names the difflib port; `NOTICES.md` row 22 path becomes
   `internal/textmatch/textmatch.go` (`Ratio`).
2. `internal/metadata/online/score.go` and `internal/ebook/epub.go`: call `textmatch`; delete the moved functions.
3. Migration and test above; `internal/db/db.go`: `SchemaVersion = 17`.
4. `internal/ebook/store.go`: `File` gains `Hold string`, `BookDefault bool`; `fileCols` gains `COALESCE(hold,'')`,
   `book_default`; `scanFile` reads them.
5. `internal/ebook/pair.go` (pure, no SQL): `type Meta struct{ Title string; Authors []string; ISBN, ASIN string }`;
   `type BookKey struct{ ID, Title string; Authors []string; ISBN, ASIN string }`; `type Reason string` consts
   `ReasonISBN="isbn"`, `ReasonASIN="asin"`, `ReasonTitle="title"`, `ReasonAuthor="author"`, `ReasonSimilar="similar"`;
   `type Candidate struct{ BookID string; Reason Reason; Score float64 }`; `Match(m Meta, books []BookKey, notSame
   map[string]bool, limit int) []Candidate`: ISBN13 equal (non-empty) 1.0 `isbn`; ASIN equal (upper, trimmed) 1.0
   `asin`; TitleKey equal and surname sets equal and non-empty 0.9 `title`; surname sets intersect 0.5+0.4·Ratio
   `author`; Ratio(TitleKey) ≥ 0.75 0.6·Ratio `similar`; one candidate per book (best); skip `notSame`; sort score
   desc, then book title, then id; cut at `limit`. `Decide(c []Candidate) (bookID, pairedBy string, ok bool)`: exactly
   one `isbn`/`asin` candidate → (`isbn`); else none of those and exactly one `title` → (`title`); else false.
6. `internal/ebook/pair_store.go` (SQL): `bookKeys(ctx, f share.Filter) ([]BookKey, error)` (live books, authors from
   `book_people` role `author` by `idx`, filter AND-ed in; empty filter for the engine); `notSameFor(ctx, fileIDs)`;
   exported reads: `(*Store) Unpaired(ctx, limit int) (rows []UnpairedFile, total int, err error)` (`book_id IS NULL`,
   `ORDER BY created_at, id`; `UnpairedFile{File; Edition *Edition}`), `(*Store) BookDefault(ctx, bookID) (File, bool,
   error)`, `(*Store) EditionFor(ctx, userID, bookID) (File, Edition, bool, error)` (invariant 7), `(*Store)
   Choice(ctx, userID, bookID) (string, error)`; `(*Service) Candidates(ctx, fileID string, f share.Filter, limit int)
   ([]Candidate, error)`; `(*Service) SuggestionsForBook(ctx, bookID string, limit int) ([]File, error)` (unpaired,
   ready, hold not `'kept'`, at most the newest 5000 rows scanned, kept when `Match` against this one book gives any
   candidate); `Checks(m Meta, b BookKey, ebookCh []ChapterRow, bookChapterTitles []string) []Check` with `Check{Kind
   string; OK bool; Author, ISBN string; N, M int}`, kinds `same_title`, `different_title`, `same_isbn`,
   `chapters_same` (equal non-front count and equal `Norm` names), `chapters_count` (equal count), `chapters_differ`.
7. `internal/ebook/pair_write.go`: errors `ErrFileNotFound`, `ErrBookNotFound`, `ErrNotReady`, `ErrAlreadyOnBook`.
   `Pair(ctx, fileID, bookID, userID)`: file must be `ready` (else `ErrNotReady`), book live (else `ErrBookNotFound`),
   upload with the same `sha256` already on the book → `ErrAlreadyOnBook`; same book → no-op; one tx: set `book_id`,
   `paired_by='person'`, `hold=NULL`, `book_default=0`; delete its not-same row for that book and all its
   `text_choices`; audit `ebook.pair` target `ebook:<id>` detail `{book, from}`. `Unpair(ctx, fileID, bookID, userID)`:
   `WHERE id=? AND book_id=?` (0 rows → `ErrFileNotFound`), set `book_id=NULL`, `paired_by=NULL`, `hold='person'`,
   `book_default=0`; insert or ignore not-same; delete its choices; audit `ebook.unpair`. `NotSame(ctx, fileID,
   bookID, userID)`: file unpaired; insert or ignore; audit `ebook.not_same`. `Keep(ctx, fileID, userID)`: unpaired;
   `hold='kept'`; audit `ebook.keep`. `SetBookDefault(ctx, bookID, fileID, userID)`: clear the book's flags, set this
   ready paired file (0 rows → rollback, `ErrFileNotFound`); audit `ebook.default`. `Choose(ctx, userID, bookID,
   fileID)`: file paired and ready (else `ErrFileNotFound`); equal to the book default → delete the row, else upsert.
8. Engine: `(*Service) PairPending(ctx) error` loads keys once, then unpaired ready `hold IS NULL` files with their
   current edition in batches of 500, `Match`+`Decide`, writes `UPDATE ebook_files SET book_id=?, paired_by=?,
   book_default=0, updated_at=? WHERE id=? AND book_id IS NULL AND hold IS NULL AND NOT EXISTS (SELECT 1 FROM
   ebook_not_same WHERE ebook_file_id=? AND book_id=?) AND EXISTS (SELECT 1 FROM books WHERE id=? AND missing_since
   IS NULL)`. `pairFile(ctx, fileID)` is the same for one file. `job.go`: after the fenced write that made the file
   ready, call `pairFile` when `book_id` is empty (log a failure, never fail the job). `service.go` `Finisher`: call
   `PairPending` before `EnqueuePending`. `reconcile.go` `pairByFolder`: replace `(paired_by IS NULL OR
   paired_by='folder')` with `hold IS NULL`, and skip a (file, book) with a not-same row (same `NOT EXISTS` in the UPDATE).
9. Docs: `docs/architecture.md` rows 102-103 to the real schema plus the two new tables; `docs/read-along.md` line 93
   `user_settings` → `text_choices`; roadmap "R2 P1 status".

Tests (`internal/textmatch/textmatch_test.go`, `internal/ebook/pair_test.go`, `pair_write_test.go`):
`TestTextmatchTitleKey` (subtitle, "(Series #1)", "[Unabridged]", "A Novel", accents, empty stays empty),
`TestTextmatchSurname`, `TestTextmatchISBN13` (10→13, bad check digit, hyphens), `TestPairMatch` and `TestPairDecide`
(table: folder handled elsewhere; ISBN one book; ISBN two books → none; ASIN; title + author one; two candidates →
suggestion only; title equal, author differs → `similar`/`author`, no pair; not-same excluded; ISBN beats a title
match on another book), `TestPairChecks`, `TestPairEngineAuto` (ingest a library EPUB whose metadata matches one book →
`paired_by='title'`), `TestPairEngineRespectsHold` (hold `person`/`kept`, not-same, missing book: untouched),
`TestPairFolderRespectsNotSame` (rescan does not redo an unpaired folder pairing), `TestPairManual` (pair, re-pair
moves choices and default, not ready 409 error, upload duplicate), `TestPairUnpair`, `TestPairDefaultAndChoice`
(invariant 7 incl. a choice whose file was unpaired), `TestPairNeverWritesFiles` (library file and upload: size, mtime,
SHA-256 equal after every P1 write; the library dir made 0555 and file 0444 during the run), `TestPairRace` (`-race`:
engine and manual `Pair` on one file concurrently; the person wins or the engine never overwrites).
Gates: `go test -race ./internal/textmatch/... ./internal/ebook/... ./internal/db/... ./internal/metadata/...`,
`make check`, `make build`. Out of scope: any page or route, ebook-only books, the orphaned-bytes sweep, `metadata`'s
`cleanISBN` copies, re-running the engine when a book's details change (next scan does it).

## P2 Text tab: pair, unpair, editions

Routes (in `registerEbooks`, all forms with the hidden `_csrf`; success → `s.answerPage(w, r, ebookTextHref(id))`;
htmx error → `s.fragment(w, r, status, components.ErrorToast(msg))`; plain error → the tab rendered with that status
and the message in `role="alert"`): `POST /books/{id}/text/{fileID}/pair` (EditBook; 409 `ErrNotReady`,
`ErrAlreadyOnBook`), `.../unpair` (EditBook; file must be on this book else 404), `.../not-same` (EditBook; file
unpaired), `.../default` (EditBook), `.../choose` (ReadText; file on this book and ready). `ErrFileNotFound` and
`ErrBookNotFound` → 404.
Steps:
1. `internal/web/components/pairing_card.templ` `PairingCard(v PairingCardView)`: audio cover or the dashed headphone
   placeholder (`Audio == nil`), link icon, ebook cover tile, title, meta line, optional "Found in {path}", checks as a
   `<ul>` (check icon or warning icon plus the sentence; icon `aria-hidden`), optional warning paragraph, slot for
   action forms. CSS `ts-pair*` in `bundle.css`, tokens only, four themes, 44 px targets. Commit alone (see order).
2. `internal/web/ebook_copy.go`: the strings below. `routes_ebooks.go` `textView`: for editors, each paired file built
   by the automatic rules (`folder`, `isbn`, `title`) shows `PairingCard` with `Checks` and a "Not the same book" form;
   `upload`/`person` files show the same form under their row; up to 3 `SuggestionsForBook` under "Found in your
   folders" with checks, "Pair with this audiobook" (primary) and "Not the same book". When the book has two or more
   ready files: chips "The book's edition" and "Your edition" (from `EditionFor`), "Read this edition" (choose) on
   the others for anyone with ReadText, "Make it the book's edition" (default) for editors. Readers never see
   suggestions, paths or other people's choices.
3. `pages/book_text.templ`: render the above inside the existing `TextBody`; `make generate`.
4. Tests (`text_pair_test.go`): `TestTextPairCard` (checks sentences), `TestTextPairActions` (each route, htmx and
   plain, 303/200, errors 404/409), `TestTextPairRoles` (owner, admin 303; member and guest 404 for pair, unpair,
   not-same, default; choose 303 only with `can_read`), `TestTextPairSuggestionsEditorsOnly` (a member with
   `can_read` never sees the unpaired title), `TestTextEditionChoice` (choice, default, fallback, unpaired choice),
   matrix rows in `routes_matrix_ebooks_test.go`, a11y seed for `book-text` gains one folder-paired file, one
   suggestion and a second edition; opt-in `TestTextPairVisual` (390, 768, 1024, 1440; found card, suggestion, two
   editions; dark and light; seeded ids) beside AddEbook*.
Gates: `make generate` (tree clean), the touched web tests, `make check`, `make build`, CSS budget test, `make a11y`
0 findings, `TENDSHELF_TEST_BROWSER=1` capture run. Out of scope: "Pair and prepare read-along" starting alignment
(R3), the board's "author's note isn't narrated" check (R3), the read modes (R4).

## P3 `/admin/ebooks` inbox

Routes (`internal/web/routes_ebook_inbox.go`, registered from `registerAdmin`; every route `s.allowLibraries` first):
`GET /admin/ebooks` (first 100 unpaired by `created_at`, then "And {n} more."; sections waiting (`hold` NULL or
`person`) and "Kept on their own"; each ready row shows its best candidate with "Choose" and a link "Choose the
audiobook"; pending, refused, failed rows show their R1 sentence and no actions), `GET /admin/ebooks/{fileID}`
(board NoMatchPhone: eyebrow "Add an ebook", "Which audiobook is it?", `PairingCard` with `Audio == nil`, the no-match
warning when no `title`/`isbn`/`asin` candidate, search form `GET` same URL `?q=` through `s.lib.Search(ctx,
s.scope(r), q, 20)`, "Close matches" (5, `Candidates` with `s.can.BookFilter(u, "b")`), "Keep it as an ebook on its
own" form), `POST /admin/ebooks/{fileID}/pair` (field `book`; missing → 422; EditBook on that book via `s.loadBook`;
409 as P2), `POST /admin/ebooks/{fileID}/keep`. Success → `answerPage("/admin/ebooks")`; errors as P2.
Steps: `ebook_inbox_copy.go`; `pages/admin_ebooks.templ` (`AdminEbooks`, `AdminEbookPick`, sibling of
`admin_lookups.templ`); `prefsPage(..., "admin-libraries", "libraries", ...)` with `adminStyles`; `/admin/libraries`
shows "{n} ebooks to pair" (link, only when n > 0, counted in SQL) in `pages/admin_libraries.templ`; matrix rows in a
new `routes_matrix_ebook_inbox_test.go` registered at `routes_matrix_test.go:365`; a11y rows `admin-ebooks`,
`admin-ebook`. Tests (`ebook_inbox_test.go`): `TestEbookInboxList`, `TestEbookInboxPick` (close matches, search,
warning, count n uses the filter), `TestEbookInboxPair`, `TestEbookInboxKeep`, `TestEbookInboxRoles` (member and guest
404 on all four; admin 200/303), `TestEbookInboxNeverABook` (an unpaired ready ebook titled "Quillwort Unpaired": no
search hit, home and library counts equal before and after, no row in `/books` lists), opt-in `TestEbookInboxVisual`
(390, 768, 1024, 1440; empty, rows, pick with matches, pick with none; dark and light) beside NoMatchPhone.
Gates: as P2. Out of scope: bulk pairing, deleting unpaired upload bytes, a nav item (the Libraries page links it).

## Copy (deck words exact; NEW marked)

Deck: "Found in your folders", "Not the same book", "Same title and author: {author}", "{n} chapters in both, with
the same names", "Add an ebook", "Which audiobook is it?", "None of your {n} audiobooks has this title and author. It
may be listed under a different name.", "Close matches", "Choose", "same author", "similar title", "Keep it as an
ebook on its own", "Choose the audiobook", "Found in {path}", "This ebook is already on this book."
NEW: "Pair with this audiobook"; "Same ISBN: {isbn}"; "{n} chapters in both"; "{a} chapters in the ebook, {b} in the
audiobook"; "Different title or author: {title} by {authors}"; "same ISBN"; "same title and author"; "The book's
edition"; "Your edition"; "Read this edition"; "Make it the book's edition"; "This ebook is still being read, or
Tendshelf could not open it."; "Ebooks to pair"; "These ebooks are not paired with an audiobook yet. Only owners and
admins see them."; "Every ebook is paired with an audiobook."; "Kept on their own"; "It stays here, off your shelf,
until you pair it with an audiobook."; "1 ebook to pair" / "{n} ebooks to pair"; "Search your audiobooks"; "Search";
"Audiobooks matching “{q}”"; "No audiobook matches “{q}”."; "And {n} more."; "Back to Ebooks to pair" (aria);
"{authors} · {FORMAT} · {n} pages" (or "· {n} chapters" when no page count); "Choose a book first."

## Operator decisions (defaults apply if unanswered)

1. Ebook-only books: default none in R2; "Keep it as an ebook on its own" keeps the file in the inbox with the NEW
   sentence. An ebook on the shelf without audio would need books without audio (a later decision).
2. Primary button: default "Pair with this audiobook" (NEW) until R3, when it becomes the deck's "Pair and prepare
   read-along" and starts alignment.
3. Cross-library automatic pairing: default allowed.
