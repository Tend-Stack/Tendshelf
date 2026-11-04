# M5 R4 Read-along reader: implementation plan

Sources: roadmap R4 bullet; `docs/read-along.md` §3, §4, §7; `docs/architecture.md` data rows 92/101/110, routes 236-246 and
407; handoff `docs/HANDOFF.md` "Read-along reader"; boards `screens/reader/*`; components `ReaderPage`, `ReadDock`,
`ReadingSettings`, `SentenceHighlight` (their classes are **already in `bundle.css`**: `.ts-reader*`, `.ts-s`, `.ts-hl`,
`.ts-notedot`, `.ts-readdock*`, `.ts-mode`, `.ts-readset`, `.ts-selmenu`, `.ts-voicepill`, `.ts-focus`).
**Precondition**: R3 P5 is merged (it adds `web.Options.Align` and the Text tab alignment states). P1 may run before it.

## Packages and order

| Package (~tool calls) | When | Shared files |
| --- | --- | --- |
| P1 Data: migration 0019, repo (~110) | now; beside R3 P5 | `db.go`, `architecture.md` rows, roadmap |
| P2 Server: page, chapter fragment, timing, marks routes (~250) | after R3 P5 and P1 merged | `routes_bookmarks.go`, `book_text.templ`, `routes_ebooks.go`, `server.go`, `a11y_pages_test.go`, architecture routes |
| P3 Client: `readalong.js`, `reader-marks.js`, `pages-reader.css` (~250) | after P2; **beside P4** | `motion_test.go` (all three JS rows and the total, P3 only) |
| P4 Reading settings and OpenDyslexic (~180) | after P2; **beside P3** | `pages/reader.templ` (Aa link, setting classes, `ReaderStyles`), `NOTICES.md` |
| P5 Captures, a11y rows, contrast, one-frame measurement (~200) | after P3 and P4 | `a11y_pages_test.go`, visual matrix, `perf_weight_test.go` |

At most two run at once (P3 with P4). Test name prefixes and package-level helper prefixes (internal/web is one package):
P1 `TestMigration0019`, `TestHighlight`, `TestBookmarkText`, `TestParseSID`; P2 `TestReaderPage|Chapter|Timing|Render|Highlight|Bookmark`, helpers `rdr*`;
P3 `TestReadAlong*`, `TestReaderMarks*`, helpers `ra*`; P4 `TestReaderSettings*`, `TestReaderFont*`, helpers `rset*`; P5 `TestR4*`, helpers `r4*`.
Every package appends its own status paragraph under R4 in `docs/roadmap.md` (the orchestrator resolves the text merge).

## Invariants (all packages)

1. Every text route (page, fragment, timing, marks, text bookmark) answers 404 `not_found` unless `share.Can(user, ReadText, book)`;
   playing from text also needs `ListenBook`. A share-link visitor gets 404 on every reader route in R4 (R5 adds `can_read` links).
2. Highlights and reader settings are read and written only with `user_id = current user`; admins see only their own.
3. Inside a `.ts-s` the concatenated text nodes equal `block.Text[o:e]` exactly (UTF-16), so JS string offsets are canonical offsets.
4. No inline `style=""` or script; size, spacing and font are classes; JS may use CSSOM `setProperty` (as `player.js` does).
5. The highlight touches the DOM only when the sentence or word index changes; while playing it is checked every animation frame.
6. `player.js` is not changed (13,232 of 13,312 bytes). The reader reads `window.tsPlayer` and its `ts:player:*` events only.
7. The timing file is served byte-for-byte from `<data>/align/<chapter_key>.json.gz`; the key comes from the database, never the URL.
8. Library and ebook files stay read-only; R4 writes only `highlights`, `bookmarks` and `user_settings` rows.

## Decisions

- **A highlight's note is `highlights.note`, no `notes` table.** Notes already live on their mark (`bookmarks.note`); one note per mark,
  same 2,000-character limit and soft-delete sync. A `notes` table would be a second note store. `architecture.md` row 92 is removed
  and row 110 extended in P1. (Operator decision 1.)
- **Word highlight uses the CSS Custom Highlight API over a `Range`** (read-along.md §3), not per-word spans (HANDOFF table): a chapter
  stays one span per sentence; `architecture.md` wins on stack. Without `CSS.highlights` only the sentence highlights.
- **Timing format = the stored file, gzip pass-through** (`Content-Encoding: gzip`); the client builds typed arrays. No second format.
- **rAF loop in `readalong.js`** reading `tsPlayer.state().positionMs` while playing; started and stopped from `ts:player:state`.
  `timeupdate` (about 4 Hz) cannot meet one frame; an `onTime` in `player.js` does not fit its budget.
- **Panels use the house `?sheet=` pattern** (`contents`, `marks`, `reading`) on the canonical page, so they work without JS; the planned
  `/hx/books/{id}/read/toc` and `GET /hx/books/{id}/highlights` are not added.
- **Focus line is the `view` setting** (ReadingSettings README: "the view: Page or Focus line"), not a URL mode; modes are `readalong`
  and `read`; Listen is the book page `/books/{id}`.
- **Mode switcher is a `<nav aria-label="How to follow this book">` of links with `aria-current="page"`**, not a radiogroup: they are
  navigations with URLs (handoff says radiogroup; deviation listed for the operator).
- **Reader settings route is `POST /me/reader-settings`** (htmx → 204, plain → 303 `next`), one route for both; theme is `ui.theme`.
- **Three lazy modules** through shell.js's `data-ts-module` mount (`export function mount(el)`): `readalong.js` on `#reader`,
  `reader-marks.js` on `#reader-marks`, `reader-settings.js` on the settings form. Each stops when its element is disconnected.

## Migration `internal/db/migrations/0019_highlights.sql` (P1) — `SchemaVersion = 19` in `internal/db/db.go`

```sql
-- 0019_highlights: read-along R4 (docs/read-along.md §3). A highlight is a text range plus the time range taken from the
-- alignment when it was made; its note is the note column (a note on a moment stays bookmarks.note). Bookmarks made in the
-- text also record the edition and sentence. edition_id is SET NULL so a forgotten edition keeps the person's marks (quote kept).
CREATE TABLE highlights (
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
  start_ms     INTEGER CHECK (start_ms IS NULL OR start_ms >= 0),
  end_ms       INTEGER,
  alignment_id TEXT REFERENCES alignments(id) ON DELETE SET NULL,
  colour       TEXT NOT NULL DEFAULT 'yellow' CHECK (colour IN ('yellow','green','blue','pink')),
  note         TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 2000),
  client_id    TEXT CHECK (client_id IS NULL OR length(client_id) BETWEEN 1 AND 64),
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  deleted_at   INTEGER,
  CHECK ((start_ms IS NULL) = (end_ms IS NULL)),
  CHECK (end_ms IS NULL OR end_ms >= start_ms)
) STRICT;
CREATE INDEX highlights_live ON highlights(user_id, book_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX highlights_chapter ON highlights(user_id, edition_id, chapter_idx) WHERE deleted_at IS NULL;
CREATE INDEX highlights_sync ON highlights(user_id, updated_at);
CREATE INDEX highlights_book ON highlights(book_id);
CREATE INDEX highlights_edition ON highlights(edition_id) WHERE edition_id IS NOT NULL;
CREATE UNIQUE INDEX highlights_client ON highlights(user_id, client_id) WHERE client_id IS NOT NULL;
ALTER TABLE bookmarks ADD COLUMN edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL;
ALTER TABLE bookmarks ADD COLUMN sid TEXT CHECK (sid IS NULL OR length(sid) BETWEEN 6 AND 40);
```
Tests (`internal/db/migration_0019_test.go`, pattern of `migration_0018_test.go`): **`TestMigration0019Highlights`**: open with
`ms[:18]`, `seed7`, an ebook file, edition `ed_1` and a bookmark; reopen at 19; the bookmark survives with NULL `edition_id`/`sid`;
insert a highlight and set the bookmark's `edition_id='ed_1', sid='c1.p1.s1'`; `DELETE FROM editions` sets both `edition_id` to NULL
and keeps the rows; deleting the user removes the highlight. **`TestMigration0019FromEmpty`**: rejects colour `'red'`, a 33-char
prefix, `start_ms` without `end_ms`, `end_ms < start_ms`, `chapter_idx = 0`, a second row with the same `(user_id, client_id)`.

## P1 Data (repo `internal/progress/highlights.go`, beside `bookmarks.go`; reuses `Service`, `cleanNote`, `ErrLimit|ErrInvalid|ErrNotFound|ErrClientIDTaken`)

1. Migration and `SchemaVersion` as above; update any test that pins version 18 (grep `18` in `internal/db/*_test.go`).
2. `internal/ebook/docread.go`, beside `SentenceID`: `func ParseSID(sid string) (ch, p, s int, ok bool)` for
   `^c[1-9][0-9]{0,4}\.p[1-9][0-9]{0,5}\.s(0|[1-9][0-9]{0,4})$` (chapters and blocks are 1-based in `builder.go`; the sentence index `k` is the
   0-based range index passed to `SentenceID`); `func CompareSID(a, b string) int` (doc order: chapter, block, sentence).
3. `highlights.go`: `type Highlight struct{ ID, UserID, BookID, EditionID, AlignmentID, StartSID, EndSID, Quote, Prefix, Suffix, Colour, Note, ClientID string;
   Chapter, StartOff, EndOff int; StartMs, EndMs *int64; CreatedAt, UpdatedAt, DeletedAt int64 }`; `var MaxHighlightsPerBook = 2000`; ids `db.NewID("hl")`
   (match the id helper's prefix form used by bookmarks).
   - `AddHighlight(ctx, h Highlight) (Highlight, bool, error)`: validates both SIDs parse, same chapter as `h.Chapter`, start ≤ end
     (`CompareSID`, and `StartOff < EndOff` when the SIDs are equal), `1 ≤ len([]rune(Quote)) ≤ 4000`, prefix/suffix ≤ 32 runes, colour in set
     (empty → `yellow`), note via `cleanNote`; else `ErrInvalid`. Live count ≥ max → `ErrLimit`. `ClientID` repeats the bookmark rule
     (same id, same user → return existing, `created=false`; `ValidClientID`). One transaction for count + insert.
   - `Highlights(ctx, userID, bookID string) ([]Highlight, error)` (live, all editions); `ChapterHighlights(ctx, userID, editionID string, ch int)`;
     `Highlight(ctx, userID, id)` (`ErrNotFound` for another user's or deleted); `SetHighlight(ctx, userID, id string, note, colour *string)`;
     `DeleteHighlight(ctx, userID, id)`: soft delete, clears `note`, `quote`, `quote_prefix`, `quote_suffix` (private text goes, like bookmarks).
4. `bookmarks.go`: `Bookmark` gains `EditionID, SID string`; every SELECT and `scanBookmark` read them (`coalesce(...,'')`);
   new `AddTextBookmark(ctx, userID, bookID, editionID, sid string, positionMs int64, note, clientID string) (Bookmark, bool, error)` sharing the
   insert of `AddBookmarkWithClientID` (factor one private `insertBookmark`); `editionID` and `sid` both set and `ParseSID` ok, else `ErrInvalid`.
   The `/api` JSON shapes are unchanged in R4 (R5 adds the fields).
5. `docs/architecture.md`: row 92 (`notes`) removed; row 110 gains `chapter_idx`, `note`, `client_id`; bookmarks row says 0019.

Tests (`internal/progress/highlights_test.go`): `TestHighlightAddAndList`, `TestHighlightInvalid` (table: bad sid, cross-chapter, end before start,
equal sid with start_off ≥ end_off, empty quote, 4001 runes, bad colour), `TestHighlightLimit` (max lowered to 2 → `ErrLimit`),
`TestHighlightPrivate` (user B: `Highlight` → `ErrNotFound`, lists empty, `SetHighlight`/`DeleteHighlight` → `ErrNotFound`),
`TestHighlightDeleteClearsText`, `TestHighlightClientID`; `TestBookmarkTextPlace` (stored and scanned; sid without edition → `ErrInvalid`);
`TestParseSID` (table, in `internal/ebook`). Gates: `go test -race ./internal/db/ ./internal/progress/ ./internal/ebook/`, `make check`, `make build`.
Out of scope: sync endpoints for highlights (`highlights_sync` is the R5/P4 hook), re-anchoring by quote.

## P2 Server (`internal/web/routes_reader.go`, `reader_render.go`, `reader_copy.go`, `pages/reader.templ`)

Steps:
1. `server.go`: keep R3 P5's `Options.Align`; add `s.readerDocs` = a mutex-guarded cache of at most 4 `*ebook.Doc` keyed by `doc_sha256`
   (`ebook.ReadDoc(filepath.Join(dataDir,"text"), sha)` re-hashes the whole file; documents are content-addressed so caching is safe).
2. `internal/align/timing.go`: `func OpenTiming(dir, key string) (*os.File, error)` (same `cleanName` check as `ReadTiming`).
3. `routes_reader.go` `registerReader(mux)` and the gate `readerCtx(w, r) (rc readerCtx, ok bool)`: book visible, `ReadText` else 404; edition
   = `?edition=` if it is the `CurrentEdition` of a `ready` file paired to this book, else 404; absent → `EditionFor(user, book)`, none → 404;
   alignment = `Align.Store().Latest(edition.ID)` (may be absent); chapters from it (`Chapters`, by `Idx`, **1-based = doc `Chapter.Idx`**).
4. `GET /books/{id}/read` → full page. Query: `mode` ∈ `readalong|read` (absent: `readalong` if an alignment exists and `ListenBook`, else `read`;
   `readalong` without either → `read`; other value → 404); `ch` 1..len(chapters) else 404; `at` a SID of this edition (`ParseSID`, exists) else 404;
   `sheet` ∈ `contents|marks` (P4 adds `reading`) else 404. Start chapter: `ch`, else `at`'s, else the aligned chapter whose `[start_ms,end_ms)`
   holds the person's `progress.position_ms`, else the first non-front chapter, else 1. Renders `pages.Reader(v)` with `pages.ReaderStyles`
   (`[]string{"css/pages-reader.css"}`; P3 fills the file, P2 commits it with a header comment only).
5. `GET /hx/books/{id}/read/chapters/{idx}` (`edition`, `mode` as above) → `pages.ReaderChapter(cv)` fragment only.
6. `GET /books/{id}/read/timing/{idx}`: chapter row `state='aligned'` with a key, else 404. `Content-Type: application/json`, `ETag: "<key>"`,
   `Cache-Control: private, no-cache`, `Vary: Accept-Encoding`; `static.MatchesETag` → 304; `static.AcceptsGzip` → stream the file with
   `Content-Encoding: gzip`, else gunzip through `io.LimitReader` (64 MiB). Not under `/hx/` (it is JSON).
7. `reader_render.go` (pure, tested): `rdrSegments(block ebook.Block, timing map[sid]int, hls []progress.Highlight) []rdrPiece` splits block text at
   sentence edges, run edges and highlight edges (UTF-16 → byte offsets via one table per block). Markup contract (P3 and P4 rely on it exactly):
   - root `<article id="reader" class="ts-reader" data-ts-module={readalong.js} data-book data-edition data-mode data-hl="wash" data-word="on"
     data-view="page" data-follow="on" data-follow-style="steady" data-return="off" data-chapters="s,e;s,e;…">` (`-1,-1` for an unaligned chapter);
     `data-ts-msg-left="{n} min left in chapter"`, `data-ts-msg-page="p. {n} of {total}"`.
   - chapter `<section id="reader-ch-{idx}" data-ch data-start data-end data-timing="/books/{id}/read/timing/{idx}?edition=…">` (no `data-timing`
     when unaligned), meta line, `<h2 class="ts-reader__chapter">`, `<div class="ts-reader__text">`, then the sentinel
     `<div class="ts-reader__next" hx-get="/hx/books/{id}/read/chapters/{idx+1}?edition=…&mode=…" hx-trigger="revealed, ts-reader-next" hx-swap="outerHTML">`
     containing a plain link `?ch={idx+1}` (absent after the last chapter).
   - blocks: `p`→`<p id="b-{blockID}" data-pg>`, `h`→`<h3>`, `quote`→`<blockquote><p>`, `li`→`<p class="ts-reader__li">`, `note`→`<p class="ts-reader__note">`,
     `pre`→`<pre>`, `hr`→`<hr>`, `img`→ its text as `<p class="ts-reader__img">` or nothing. `data-pg` = `min(PageCount, cumChars*PageCount/totalChars + 1)`.
   - sentence `<span class="ts-s" id="s-{sid}" data-sid data-o="{blockOffset}" data-s="{timingIndex}">`; gap sentence (`Gap[i] != nil` or absent from
     timing): `class="ts-s ts-s--gap"` and no `data-s`; unaligned chapter: no `data-s`. Inside: text, `<em|strong|code>`, `<span class="ts-hl" data-hl="{id}">`;
     the first sentence of the chapter's first `p` wraps its first letter in `<span class="ts-reader__drop">`. Nothing else inside a `.ts-s`.
   - after a sentence span: `<button type="button" class="ts-notedot" data-hl-note="{id}" aria-label="Your note">` where a highlight with a note ends;
     `<span class="ts-reader__bm" role="img" aria-label="Bookmark">` where a bookmark falls (text bookmark by `sid`; listening bookmark by binary search
     of `SS` in that chapter's timing).
   - page chrome: close link `/books/{id}` "Close reader"; the mode nav; `<button type="button" class="ts-iconbtn" data-ts-reader-eyes-free data-ts-src data-ts-css>`
     "Eyes-free" (only with `ListenBook`, URLs from `withEyesFree`); `<button class="ts-voicepill" data-ts-voice hidden>` "Back to the voice";
     `<div class="ts-focus" data-ts-focus hidden>` with three `<p>` slots, "Exit focus line", Back/Forward one sentence buttons, hint line;
     dock `<div class="ts-readdock" id="reader-dock">`: speed button "Playback speed, {n} times" (`data-ts-reader-speed`), "Back one sentence"
     (`data-ts-sent="-1"`), the play button `data-ts-play={bookID}` (player.js toggles it and hides the bar), "Forward one sentence" (`data-ts-sent="1"`),
     "Contents and notes" link `?sheet=contents`; ruler `aria-hidden`, page and left-in-chapter lines. Read mode: the dock is the thin footer with one
     `data-ts-play` "Continue listening from {time}". Selection toolbar `<div id="reader-marks" class="ts-selmenu" role="toolbar" aria-label="Selected text"
     data-ts-module={reader-marks.js} hidden>`: Highlight, Note, Listen from here, Copy; plus the hidden form `#reader-hl-form`
     (`hx-post="/hx/books/{id}/highlights" hx-swap="none"`, fields below, `_csrf`).
   - banners: partial alignment "Read along works up to chapter {n} for now"; read mode with no alignment: nothing.
8. Sheets: `?sheet=contents` (chapters, current marked, "text only, not narrated" for front chapters, links `?ch=n`); `?sheet=marks` (MarksPhone:
   "Highlights and notes", lead "One set for this book, whether you were listening or reading. Each one opens at the same moment in both.",
   filter links `&show=all|highlights|notes|bookmarks` with counts; item: kind line, quote, note, "Play at {time}" as
   `data-ts-play-at={start} data-ts-book data-ts-play-until={end}`, "Open p. {n}" `?ch=&at={sid}#s-{sid}`, `hx-delete` Delete, note form `hx-patch`).
9. Highlight routes (all `readerCtx`; htmx only; a non-htmx request → 400):
   - `POST /hx/books/{id}/highlights` fields `edition, start_sid, start_off, end_sid, end_off, colour?, note?, client_id?`. Server reads the doc and
     timing: both SIDs in one chapter of that edition (else 400 "Highlight inside one chapter."), quote = canonical text between them, prefix/suffix =
     32 characters each side, `start_ms` = `W0` of the first timed word overlapping the start (else `SS`), `end_ms` = `W1` of the last (else `SE`), only
     over timed sentences; none timed → NULL; `alignment_id` = the alignment used. 200 with OOB: each touched block `<p id="b-…" hx-swap-oob="outerHTML">`,
     `#reader-marks-count`, toast "Highlighted.". `ErrLimit` → 409 toast "This book has 2,000 highlights. Remove some to add more."; `ErrInvalid` → 400 toast.
   - `PATCH /hx/highlights/{id}` (`note`, `colour`): owner only (`ErrNotFound` → 404), plus `ReadText` on its book. OOB blocks (when its edition is shown) and the marks item.
   - `DELETE /hx/highlights/{id}`: same gate; OOB blocks, the removed marks item, toast "Highlight removed.".
10. `routes_bookmarks.go` `postBookmark`: optional `edition_id` and `sid` (both or neither, else 400). With them: `ReadText` and the edition gate (404),
    `sid` must exist; `position_ms` is replaced by the sentence start, in a gap by the previous timed sentence's end, with no alignment 0; `AddTextBookmark`.
11. Text tab (`routes_ebooks.go` `textFileView` ready, `book_text.templ`): `ReadHref` `/books/{id}/read?edition={ed}` and `ReadLabel` "Read along"
    (alignment exists and `ListenBook`) or "Read". Copy in `reader_copy.go`; every string not in the deck is listed in the report.
12. `a11y_pages_test.go` `a11ySkip`: `/books/{id}/read` "audited in R4 P5"; any other route the coverage test reports, with a reason.
    `routes_matrix_reader_test.go` `readerSweepRows()` added to the matrix list; granted rows in `routes_matrix_granted_test.go` (member with and without `can_read`).
13. `docs/architecture.md` routes 236-246 and the 407/408 rows: the real names above.

Tests (`reader_test.go`, `reader_render_test.go`; fixture helper `rdrSeed(t, a, book)` writes a synthetic 2-chapter doc with `ebook.WriteDoc`, the ebook/edition
rows, two timing files via `align.WriteTiming`, alignment `done` with chapter 1 aligned and chapter 2 `unmatched`): `TestReaderPageModes` (defaults, `read`
without alignment, bad mode/ch/at/sheet → 404), `TestReaderPageRoles` (owner, admin 200; member/guest with `can_read` 200, without 404; link visitor 404),
`TestReaderChapterFragment` (no layout; sentinel; unaligned has no `data-s`), `TestReaderTimingGzip` (bytes equal the file, headers, 304, plain fallback, unaligned 404,
other book's edition 404), `TestReaderRenderSentenceText` (invariant 3, table incl. emoji, accents, em run across a sentence edge, highlight across two sentences),
`TestReaderRenderGap`, `TestReaderHighlightCreate` (quote, times from words, gap → NULL, cross-chapter 400, limit 409, OOB ids), `TestReaderHighlightPrivate`
(user B: PATCH/DELETE 404, A's marks absent from B's page and sheet; admin too), `TestReaderBookmarkFromText` (sentence start; gap → previous end;
sid without edition 400; no `can_read` 404), `TestReaderTextTabLink`. Gates: `make generate` clean, `go test -race ./internal/web/ ./internal/align/ ./internal/progress/`, `make build`.
Out of scope: images inside the text, page lists from EPUB nav, pruning loaded chapters, `can_read` links, text position (R5).

## P3 Client (`internal/web/static/js/readalong.js`, `reader-marks.js`, `internal/web/static/css/pages-reader.css`)

Steps:
1. `readalong.js` `mount(root)`: read `data-chapters`; for each `section[data-timing]` fetch the JSON once (`credentials: same-origin`) into
   `Float64Array` `ss`, `se`, `w0`, `w1`; `Int32Array` `ws`, `wo0`, `wo1`; per sentence `wFirst/wEnd`; `timed` = indices with `ss >= 0` and no gap, and
   `starts` = their `ss`. `els[i]` = `section.querySelector('[data-s="i"]')`, rebuilt after `htmx:after:swap`.
2. Loop: on `ts:player:state` with `playing` and `bookId === data-book` start `requestAnimationFrame`; stop on pause, other book, or `!root.isConnected`.
   Each frame: `pos = tsPlayer.state().positionMs`; if `pos < curStart || pos >= nextStart` binary-search `starts` (largest `ss <= pos`; keep it until the next
   start, so pauses between sentences do not flicker); on change swap `is-now`. Words: search that sentence's `w0`; on change build a `Range` over the
   sentence's cached text nodes (TreeWalker once per sentence) from `wo0 - data-o` to `wo1 - data-o`; `CSS.highlights.set("ts-word", hl)` with one reused
   `Highlight`; skipped when `data-word="off"`, `data-hl` none, or no `CSS.highlights`. Record `{i, lag: pos - ss[i], dt}` in a 256-entry ring at
   `window.tsReader.debug.lags()`.
3. Next chapter: when `pos >= start + 0.9*(end-start)` dispatch `ts-reader-next` on the chapter's sentinel once. Voice in a chapter not loaded: the pill links
   to `?mode=readalong&ch={k}`.
4. Follow: scroller = nearest ancestor with `overflow-y: auto|scroll`, else `document.scrollingElement` (computed once). Steady: after a change, if the
   sentence top is outside `[0.2, 0.55]` of the visible height (minus the dock) tween it to one third over 400 ms with `easing-standard`'s cubic-bezier;
   gentle: only outside `[0.15, 0.85]`. Reduced motion: jump. Never while a pointer is down or the selection is not collapsed. User `wheel`, `touchstart`,
   scroll keys, or a `scroll` outside our tween → stop following, show the pill; pill click or `F` → jump back and follow; when the current sentence
   comes back into view by user scroll → follow; `data-return="on"` → after 8 s without user scroll.
5. Tap: a click on `.ts-s[data-s]` (not gap, not inside `.ts-hl`/`.ts-notedot`, selection collapsed, `data-mode="readalong"`) → `tsPlayer.seek(ss)` and
   `play()` when this book is loaded, else `tsPlayer.load(book, {positionMs: ss, play: true})`. Keys (no modifier, not in fields, no open dialog/sheet):
   `J`/`K` previous/next timed sentence (seek), `H` highlight current sentence, `N` note on it (events to reader-marks), `F` back to the voice, `R` next mode link.
   Space and arrows stay `player.js`'s. Dock: `[data-ts-sent]` seek ±1 sentence; `[data-ts-reader-speed]` clicks `[data-ts-open]` (the full player);
   ruler `--ts-progress`, page and left-in-chapter lines on `ts:player:tick` from the `data-ts-msg-*` templates.
6. `data-ts-play-until`: capture clicks on `[data-ts-play-at][data-ts-play-until]`; the loop pauses at `pos >= until` once.
7. Focus line (`data-view="focus"`): hide `.ts-reader__text`, show `[data-ts-focus]`, fill its three slots with the previous, current and next sentence
   `textContent` (follows the voice while playing, else Back/Forward buttons, ArrowUp/ArrowDown, vertical swipe ≥ 48 px); "Exit focus line" sets
   `data-view="page"` and dispatches `ts:reader:view` (P4 saves it). No word highlight in focus line.
8. Eyes-free: click on `[data-ts-reader-eyes-free]` → book loaded (else `tsPlayer.load(book)` first) → `import(data-ts-src).then(m => m.openEyesFree(button))`.
9. `reader-marks.js` `mount(menu)`: on `selectionchange` inside `.ts-reader__text` with a non-collapsed range whose ends are inside `.ts-s`, position the
   toolbar (CSSOM `setProperty`) and show it; offsets = UTF-16 length of the text before the end point within its `.ts-s`. Highlight → fill
   `#reader-hl-form` and `requestSubmit()`; Note → open the note dialog (`<dialog>` in P2 markup) then submit with `note`; Listen from here → seek to the
   start sentence; Copy → `navigator.clipboard.writeText`. `H`/`N` events act on the current sentence (whole sentence range). Note dots open the note
   dialog; closing returns focus to the dot. Escape hides the toolbar.
10. `pages-reader.css`: page grid, sticky dock, column widths (45ch min on tablet/desktop, 24 px phone padding), `::highlight(ts-word)` (underline
    `--read-word`, 2 px, offset 0.3em), `.ts-s--gap` (`--ink-subtle`), cursor sentence outline, pill motion (200 ms, 8 px; reduced motion fade),
    focus line, selection toolbar placement, `.ts-reader__bm`, at 390/768/1024/1440. Under 3 KB gzip (no row needed; estimate 2.75 KB).
11. `motion_test.go` `jsBudgets`: `"readalong.js": {6 * 1024, 32 * 1024}`, `"reader-marks.js": {2*1024 + 512, 16 * 1024}`,
    `"reader-settings.js": {1*1024 + 256, 8 * 1024}` (P4 writes it; skipped until present), `jsTotalGzip = 71*1024 + 256` (72,960), each with the
    approval comment. If a file comes out over, report it; never squeeze.

Tests (opt-in browser, `-tags dev`, `TENDSHELF_TEST_BROWSER`; seed with `rdrSeed` plus a `testutil/wav` file whose length matches the timing):
`TestReadAlongHighlightBrowser` (play: `is-now` advances in order; seek back re-highlights; word highlight set when supported), `TestReadAlongFollowBrowser`
(user scroll shows the pill; pill returns; reduced motion jumps), `TestReadAlongTapBrowser` (tap seeks; gap tap does nothing; read mode tap does nothing),
`TestReadAlongKeysBrowser` (J/K/F), `TestReadAlongFocusBrowser`, `TestReadAlongNextChapterBrowser`, `TestReaderMarksSelectBrowser` (highlight created, block swapped,
`is-now` survives the swap), `TestReaderMarksNoteBrowser`. Always run: `TestJSBudget`, `TestCSSBudget`. Add the new names to `make player`'s `-run` list.
Gates: the above at 390 and 1440, `go test -race ./internal/web/static/`, `make check`, `make build`. Out of scope: sentence announcements for screen readers
(handoff: never announced), swipe between chapters, offline.

## P4 Reading settings and OpenDyslexic

1. `internal/users/reader_settings.go`: `type ReaderSettings struct{ Font string; Size int; Leading, Highlight string; Word, Follow bool; FollowStyle string;
   ReturnAuto bool; View string }`, JSON keys `font,size,leading,hl,word,follow,followStyle,returnAuto,view`, stored as one `user_settings` row `reader.v1`.
   Allowed: font `serif|sans|dyslexic`, size `18|20|22|26`, leading `compact|normal|airy`, hl `wash|underline|brighten`, followStyle `steady|gentle`, view `page|focus`.
   `DefaultReader` = serif, 20, normal, wash, true, true, steady, false, page. `LoadReader` (missing or invalid field → that default), `SaveReader` (invalid → `ErrInvalidSetting`).
2. `POST /me/reader-settings` (`routes_reader_settings.go`): any signed-in person, not a link visitor (404); fields optional, merged over the stored value;
   `theme` → `LoadSettings`/`SaveSettings` (`ui.theme`). htmx → 204; plain → 303 to `next` if it starts with `/books/` and has no `//`, else `/`.
   Invalid → 400 toast "That setting is not one Tendshelf knows.".
3. `GET /books/{id}/read?sheet=reading`: `pages.ReadingSheet` (`reader_settings.templ`), rows per ReadingSettings README: Theme (Dark, Light, Sepia, Contrast),
   Text size stepper (18, 20, 22; 26 only when `ui.text_size` is large), Line spacing, Font (Serif, Sans, OpenDyslexic; below the fold), Highlight (Wash,
   Underline, Brighten), "Underline each word" switch, "Follow the voice" switch plus Steady/Gentle and "Return automatically", View (Page, Focus line).
   Radiogroups are native radios in `fieldset`s (`role="radiogroup"` on the fieldset), switches `role="switch"` checkboxes; aria-hidden preview.
   `<form method="post" action="/me/reader-settings" hx-post="/me/reader-settings" hx-trigger="change" hx-swap="none" data-ts-module={reader-settings.js}>`
   with `next` and a `<noscript>` Save button. A line "Your browser shows the sentence only." with `data-ts-no-word hidden`.
4. `pages/reader.templ`: the Aa link `?sheet=reading` "Reading settings"; root classes from `LoadReader`: `ts-reader--sans`, `ts-reader--dyslexic`,
   `ts-reader--size-{n}`, `ts-reader--leading-{x}`; `data-hl`, `data-word`, `data-follow`, `data-follow-style`, `data-return`, `data-view`.
   `ReaderStyles` gains `"css/pages-reader-settings.css"`.
5. `reader-settings.js` `mount(form)`: on `change` apply at once to `#reader` and `<html data-theme>`; listen for `ts:reader:view` and set the View radio
   (its `change` saves); unhide `[data-ts-no-word]` when `!("highlights" in CSS)`.
6. `pages-reader-settings.css`: the size and leading classes (`--read-size`, `--read-leading`), `.ts-reader--dyslexic .ts-reader__text { font-family: "OpenDyslexic", var(--font-sans) }`,
   sheet glue, and the `@font-face` rules (URLs `/static/fonts/OpenDyslexic-*.woff2`, rewritten to hashed URLs by `buildCSS`; the font downloads only when used).
7. Font (last step; see the font question): `internal/web/static/fonts/OpenDyslexic-Regular.woff2`, `-Bold.woff2`, `-Italic.woff2`, `OFL-OpenDyslexic.txt`;
   `NOTICES.md` rows like the Fraunces rows (SHA-256, bytes, release tag and commit, licence read from the release). Until the files exist, step 6 omits
   `@font-face` and step 3 omits the option; the font lands as follow-up P4f (about 20 tool calls).

Tests: `TestReaderSettingsLoadSave` (users; defaults, invalid stored JSON field, round trip), `TestReaderSettingsRoute` (204, 303 `next`, bad `next` → `/`, invalid 400,
theme saved, link visitor 404, user B unaffected), `TestReaderSettingsSheet` (26 only in large text), `TestReaderSettingsBrowser` (opt-in: change size and highlight style
apply without reload and persist after reload), `TestReaderFontVendored` (files present, `NOTICES.md` names each SHA-256, OFL text present). Gates: `go test -race ./internal/users/ ./internal/web/`,
`make generate`, `make check`, `make build`. Out of scope: margins setting, local cache of `reader.v1` (R5 offline), the sentence announcement option.

## P5 Captures, a11y, contrast, one-frame

1. `internal/web/static/r4_reader_contrast_test.go` `TestR4ReaderContrast` (b1 helpers; resolve `var(--x)` one level): per theme, `--read-ink` on `--read-wash`,
   `--mark`, `--selection`, and `--mark` over `--read-wash`, each over `--ground`: ≥ 4.5 (7 in contrast); `--read-rest` on `--ground` ≥ 6 (7 contrast; handoff
   "Brighten keeps the rest at 6:1"); `--read-word` and `--mark-edge` on `--ground` ≥ 3. A failure is reported with the pair (operator decision 4).
2. `a11y_pages_test.go`: move `/books/{id}/read` from skip to pages: `reader-readalong`, `reader-read`, `reader-focus`, `reader-contents`, `reader-marks`,
   `reader-settings` (0 findings at four widths in four themes; allowlist only with a reason).
3. `TestR4ReaderVisual` (`-tags dev`): ReadAlongPhone/Light/Sepia/Underline/Brighten/Away/Select, ReadPhone, FocusPhone, ReadSettingsPhone, ContentsPhone, MarksPhone,
   RosaReadPhone, the Tablet and Desktop rows, side by side with the boards at 390, 768, 1024, 1440; seeded ids; text from a public-domain synthetic chapter.
4. `TestR4ReaderFrameBrowser` (`TENDSHELF_TEST_PERF=1`, in `make perf`): 390 px mobile emulation, CPU 4x (`sweepPerfRate`), 60 s WAV, 40 sentences; play 45 s;
   pass when ≥ 30 changes, p95 `lag` ≤ 17 ms and max ≤ 34 ms; JSON to `.local/tests/perf/<run>/reader-frame.json`, CPU model logged.
5. `perf_weight_test.go`: a reader page row at the four widths from the measured bytes. Roadmap R4 status with every count.
Gates: `make a11y` (A11Y_RUN on the reader rows), `make visual-full` (VISUAL_RUN=TestR4ReaderVisual), `make perf`, `make check`, `make build`.

## Operator decisions (defaults apply unless changed)

1. Highlight notes in `highlights.note`, no `notes` table (default yes).
2. JS budgets: `readalong.js` 6 KB, `reader-marks.js` 2.5 KB, `reader-settings.js` 1.25 KB; total 61.5 → 71.25 KB (72,960 bytes). CSS: both new
   files within the default 3 KB `pages-*.css` limit; core CSS unchanged.
3. OpenDyslexic: operator downloads the release woff2 files (or approves one pinned `curl` by the builder, SHA-256 recorded); default: ask before P4.
4. Contrast failures: adjust the failing token's lightness in `tokens.css` (same hue, no added bytes) and list it (default), or hold for design.
5. Mode switcher as links with `aria-current` (default) rather than the handoff's radiogroup; Eyes-free as a top-bar button (default).

## Out of scope (R4)

Cross-format position, Read mode driving the position, edition switching by quote, `can_read` share links, offline text (R5); Media Overlays (R6);
images in text; shared or public highlights; highlight colours other than yellow in the UI (the column allows four); search in text; the book page
redesign of `ReaderBook*` boards ("Continue reading along" on the hero); the Car mode.
