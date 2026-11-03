# Up next (queue): Play next, Add to queue, autoplay (plan, 2026-11-03)

Operator request 2026-11-03: split play button on the book page (Play next / Add to queue), queued books
autoplay in order when the current book ends, a third Now playing tab "Up next" with move up/down/remove.
Three packages, in order: **Q1 Go data + API**, then **Q2 UI** and **Q3 JS** (they can run in parallel after Q1;
Q3 merges after Q2). Q3 cannot merge until the operator approves the JS budget numbers in Q3.

## Shared invariants (all packages)
1. The queue is per account: rows `(user_id, book_id)`, one row per book. A person only ever reads or changes their own rows.
2. Every read joins `books` + `libraries` through the scope SQL (`visible(sc, true)`, `Listable("b")`): a book the
   person cannot see or that is refused/missing never shows, never counts, and is never popped. Hidden rows stay in the
   table (they reappear if access returns); a deleted book or user cascades away.
3. Adding needs `share.ListenBook` on the book (`s.loadBook(w, r, id, share.ListenBook)`), so hidden or unknown is 404.
4. Order is `ORDER BY q.position, q.book_id`. Play next = `min(position) - 1`; Add to queue = `max(position) + 1`.
   At most `QueueMax = 100` rows per person (all rows, hidden included).
5. A book leaves Up next when it is removed, when it is popped to play, or when it ends (the pop's `ended`).
6. No GET mutates. Every unsafe request carries CSRF (`X-CSRF-Token` header or `_csrf` field).
7. Every htmx error response is safe to swap: it re-renders the same target fresh plus an OOB `components.ErrorToast`.
8. No new module, no inline script/style, no `hx-on`. JS stays inside the budgets the operator approves.

Reused, not duplicated: shelf ordering (`library/shelves.go` `MoveInShelf` swap-with-visible-neighbour), `BookRow`,
chip-menu `<details data-ts-chipmenu>` handling in shell.js, `ts-player__menubody`/`ts-player__menuitem` styles,
`ToastOOB`/`ErrorToast`, `PlayerData` fragment + `playerView`, the route-matrix sweep, the `/api/v1` helpers.

---------------------------------------------------------------------------------------------------------------
## Q1: Go data, autoplay endpoint, JSON API (builder, sonnet, ~150 tool calls)

### Decisions
- Lives in `internal/library` like shelves (reads and writes need the scope helpers); no new package.
- Migration **0013** (AL-3 owns 0012). If AL-3 is not on `main` when Q1 is green, report it; do not renumber.
- Positions are signed integers so Play next never renumbers rows.
- Autoplay is one `POST /hx/player/next` returning the existing `PlayerData` fragment (204 when empty), so player.js
  parses it with the code it already has.

### Steps
1. `internal/db/migrations/0013_queue.sql`:
   ```sql
   -- 0013_queue: Up next, a person's ordered list of books to play after the current one.
   CREATE TABLE queue_items (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
     book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
     position INTEGER NOT NULL, added_at INTEGER NOT NULL CHECK (added_at > 0),
     PRIMARY KEY (user_id, book_id)) STRICT, WITHOUT ROWID;
   CREATE INDEX queue_items_order ON queue_items(user_id, position, book_id);
   CREATE INDEX queue_items_book ON queue_items(book_id);
   ```
   Bump whatever `TestMigrationsContiguous` / `SchemaVersion` / `tablesAt` need, exactly as 0012 did.
2. `internal/db/migration_0013_test.go`: copy the 0012 test shape (`ms[:12]`, `seed7`): old rows survive, `queue_items`
   empty; duplicate `(user_id, book_id)` refused; deleting the book and the user each cascade the row.
3. `internal/library/queue.go` (doc comment names the invariants above):
   - `const QueueMax = 100`; `var ErrQueueFull = errors.New("library: up next is full")`.
   - `type QueueEntry struct { Book BookCard; AddedAt int64 }`.
   - `func (r *Repo) Queue(ctx, sc Scope, userID string) ([]QueueEntry, error)`: visible+listable, play order, cards via `r.cards`.
   - `func (r *Repo) QueueCount(ctx, sc Scope, userID string) (int, error)`: same filter, `count(*)`.
   - `func (r *Repo) QueueAdd(ctx, userID, bookID string, front bool) (added bool, err error)`: in `WriteTx`.
     Already queued: `front` moves it to `min-1` (added=false), else no change (added=false). New: `ErrQueueFull` when
     the person has `QueueMax` rows; else insert at `min-1`/`max+1` (`COALESCE(...,0)`), `added_at = r.nowMs()`.
     FK violation (`db.IsForeignKeyViolation`) is `ErrNotFound`.
   - `func (r *Repo) QueueMove(ctx, sc Scope, userID, bookID string, delta int) error`: port `MoveInShelf` logic
     (delta not ±1: `ErrInvalid`; not queued/not visible: `ErrNotFound`; at an end: nil, no change).
   - `func (r *Repo) QueueRemove(ctx, userID, bookID string) error`: `ErrNotFound` when no row deleted.
   - `func (r *Repo) QueuePop(ctx, sc Scope, userID, ended string) (string, error)`: one `WriteTx`: when `ended != ""`
     delete that row; select the first visible+listable row; delete it; return its book id; `ErrNotFound` when none.
4. `internal/library/queue_test.go` (table-driven): add end/front order; re-add end keeps place; Play next on a queued
   book moves it first; cap at 100 → `ErrQueueFull` (101st), re-adding a queued book at the cap still works; move
   up/down incl. stepping over a hidden book and no-op at ends; remove missing → `ErrNotFound`; pop order, pop with
   `ended` deletes that row wherever it is, pop skips hidden and refused/missing rows without deleting them, empty →
   `ErrNotFound`; a member without a grant sees 0 rows and count 0 while the owner sees them (count does not leak).
5. `internal/web/queue_copy.go`: every Q1 string (API messages), e.g. `queueErrFull = "Up next holds 100 books. Remove one to add another."`,
   `queueErrUnplayable = "This book has no file Tendshelf can play."`, `queueErrWhere = "where must be next or end."`,
   `queueErrDir = "dir must be up or down."`.
6. `internal/web/routes_player.go`: register `POST /hx/player/next` → `s.postPlayerNext`. Parse form; `ended` optional,
   when set must be 1..64 chars else 400 (plain text, not htmx). Loop at most `library.QueueMax` times:
   `QueuePop(ctx, s.scope(r), u.ID, ended)` (pass `ended` only on the first call); `ErrNotFound` → 204; load with
   `s.lib.Book`; skip unless `ListenBook`, `RefusedReason == ""`, `MissingSince == 0`; build `s.playerView`; on
   `library.ErrNotFound` continue; else `Cache-Control: no-store` and `components.PlayerData(v)` 200.
7. `internal/web/routes_api_queue.go`, registered beside `registerAPISync` (same auth/CSRF/rate-limit middleware):
   - `GET /api/v1/queue` → 200 `{"items":[{"book":BookCard,"addedAt":ms}],"next":null,"total":n}`.
   - `POST /api/v1/queue` body `{"bookId"*,"where":"next"|"end"}` (default `end`) → 200 with the list shape. 400
     `bookId`/`where`; 404 hidden/unknown; 409 `conflict` field `bookId` for full (`queueErrFull`) or refused/missing/
     no playable file (`queueErrUnplayable`); 413 (8 KiB); 415.
   - `POST /api/v1/queue/{bookID}/move` body `{"dir":"up"|"down"}` → 200 list; 400 `dir`; 404 not queued or hidden.
   - `DELETE /api/v1/queue/{bookID}` → 204; 404 not queued or hidden (check `ViewBook` first, then `QueueRemove`).
   - `POST /api/v1/queue/next` body `{"ended":id}` (optional) → 200 `{"book":BookCard}` or 204; same pop loop as step 6.
   Order of checks as the sync endpoints: sign-in, access to the book, 415, 413, 400. Use `apiCardOf`, `api.ErrorField`.
8. Tests: `internal/web/routes_api_queue_test.go` (every endpoint, each error code, a bearer device and a session with
   and without CSRF, a member without a grant gets 404 and an empty list); `postPlayerNext` httptest (204 empty, 200
   fragment with `id="player-data"` of the head, `ended` removal, hidden head skipped, bad `ended` 400, no CSRF 403);
   add rows to `apiSweepRows()` and to the `routes_matrix_test.go` sweep for `POST /hx/player/next` (every role).
9. `docs/api.md`: new section "## Up next (queue)" after the sync endpoints with the five endpoints above, in the same
   style. `docs/roadmap.md`: status line "Up next Q1: table, API, autoplay endpoint".

Gates: `go test -race ./internal/db/... ./internal/library/... ./internal/web/...`, `make check`, `make build`.

---------------------------------------------------------------------------------------------------------------
## Q2: UI: split button, Up next list, panel tab, page, menu link (builder, sonnet, ~220 tool calls)

### Decisions
- Split button = existing `PlayButton` `<a>` + a `<details data-ts-chipmenu>` toggle (no new JS; shell.js already does
  one-open, outside click, Escape). The toggle is ~1/4 of the combined width (min 44 px), the menu drops under it,
  right-aligned (`ts-player__menubody` already positions `right:0; top:100%`).
- `#book-play` re-renders on every `progress-saved` (every 10 s while playing), which would slam an open menu shut, so
  the `<details>` has `id="book-queue-menu"` and `hx-preserve`. A form response re-renders the details *without*
  `hx-preserve`, closed, with `autofocus` on the summary (htmx 4 preserves by the new content's attribute and focuses
  `[autofocus]` after a swap; verified by reading the vendored file, confirm in the browser test).
- Up next on desktop ≥1280 px: third panel tab. Phone and tablet have no panel tabs, so the full player's ⋯ menu gets
  an "Up next" link to a full page `/queue` (the board's ⋯ label already says "More: queue, share, go to book").
- One list component for both places, ids differ (`now-up-next` in the panel, `up-next` on the page).

### Steps
1. `internal/web/components/move_arrow.templ`: move `shelfArrow` from `pages/shelves.templ` as `MoveArrow(up bool)`
   (identical markup); `shelves.templ` calls it. Shelf captures must stay pixel-identical (capturediff).
2. `internal/web/components/queue_menu.templ`: `type QueueMenuView struct { BookID, CSRF, Next, Toggle, PlayNext, AddEnd string; Preserve, Focus bool }`;
   `templ QueueMenu(v)`: `<details id="book-queue-menu" class="ts-split__more" data-ts-chipmenu hx-preserve?={v.Preserve}>`;
   `<summary class="ts-btn ts-btn--primary ts-btn--lg ts-split__toggle" aria-label={v.Toggle} autofocus?={v.Focus}>`
   with `@Icon("expand", false)`; `<div class="ts-player__menubody" role="group" aria-label={v.Toggle}>` holding two
   forms: `method="post" action="/queue" hx-post="/queue" hx-target="#book-queue-menu" hx-swap="outerHTML"`, hidden
   `_csrf`, `book_id`, `where` (`next` | `end`), `next`; `<button type="submit" class="ts-player__menuitem">`.
3. `internal/web/pages/book.templ`: `BookPlayView.Queue *components.QueueMenuView`; in `BookPlay`, when `v.Button != nil
   && v.Queue != nil` wrap as `<div class="ts-split" role="group">@PlayButton @QueueMenu</div>`, else the button alone.
4. `internal/web/play_link.go` `bookPlayView`: set `Queue` (Preserve true) when `v.Button != nil` and a signed-in
   person (not a share-link view: grep how link mode renders, gate on it).
5. `internal/web/static/css/bundle.css` (core budget 18,944 B, ~615 B free): `.ts-split{display:inline-flex}`,
   main `<a>` end corners 0, `.ts-split__more{position:relative;display:flex}`, summary marker hidden (both selectors as
   `.ts-chipmenu`), toggle start corners 0, `min-inline-size:44px`, `border-inline-start:1px solid
   color-mix(in srgb, var(--on-accent) 40%, transparent)`. Visible focus ring on the summary. Four themes.
6. `internal/web/components/up_next.templ`: `type UpNextRow struct { Card CardView; UpLabel, DownLabel, RemoveLabel string; First, Last bool; Focus string }`
   (`Focus` = `up|down|remove|`); `type UpNextView struct { ID, In, CSRF, Next, EmptyText, Count string; Rows []UpNextRow }`.
   `templ UpNext(v)`: `<section id={v.ID} class="ts-upnext">`; empty → `<div class="ts-np__empty" tabindex="-1"
   autofocus?=...>`; else `<ol class="ts-upnext__list">` rows: `@BookRow(r.Card)` + tools like the shelf row (gap span
   at First/Last), each a form `method=post action="/queue/{id}/move"|"/queue/{id}/delete"`, `hx-post`, `hx-target="#"+v.ID`,
   `hx-swap="outerHTML"`, hidden `_csrf`, `dir`, `in`, `next`; icon buttons 44×44 with `aria-label`, `@MoveArrow`/`@Icon("close")`.
   When `v.In == "panel"` append `<span id="now-panel-queue-count" hx-swap-oob="outerHTML">{v.Count}</span>`.
7. `now_panel.templ`: `NowTab.CountID string`; render the count as `<span id={t.CountID}>` (bookmarks keeps
   `now-panel-count`, markup unchanged). Header comment says three tabs. `now_panel_views.go` `nowPanelView`: when
   `listen`, append `{Key:"queue", Label:"Up next", Count: n, CountID:"now-panel-queue-count", Href:"/hx/queue?in=panel"}`
   (pass the count in; the handler reads `QueueCount`).
8. `internal/web/routes_queue.go` (copy in `queue_copy.go`):
   - `GET /hx/queue` → `UpNext` fragment (`in=panel` → ID `now-up-next`, else `up-next`).
   - `GET /queue` → full page `pages.QueuePage` (title "Up next", the list with In `page`, Next `/queue`).
   - `POST /queue` (add): `loadBook(ListenBook)`; `where` must be `next|end` else 400; refused/missing/no playable
     file → 409 `queueErrUnplayable`; `QueueAdd`; htmx → `QueueMenu` (closed, Focus) + `ToastOOB("ok", msg)`: end+added
     "Added to Up next", end+already "Already in Up next", next "Added to the top of Up next"; full → 409 + menu + ErrorToast.
     Plain → 303 to `next` (`auth.SafeNext`) else `/books/{id}`.
   - `POST /queue/{bookID}/move` (`dir` up|down else 400) and `POST /queue/{bookID}/delete`: `loadBook(ViewBook)` first;
     htmx → fresh list with Focus (moved row: same direction, or the other arrow when it reached an end; delete: next
     row's remove, else previous, else the empty state); errors re-render the list + ErrorToast at the error status.
     Plain → 303 to `/queue#item-{id}`.
9. `player_full.templ`: in the ⋯ menu body after Go to book: `<a class="ts-player__menuitem" href="/queue" if m.Link {hidden}>{m.UpNext}</a>`;
   `PlayerMsgs.UpNext = "Up next"`. It must close the dialog on navigation like the existing extra links (verify).
10. CSS for `.ts-upnext*` in `now-panel.css` (loaded on every page; 3,072 B budget, ~1,850 used).
11. Tests: `routes_queue_test.go` (each route: htmx and plain, every error, forbidden per role, hidden book 404, the OOB
    count only with `in=panel`, focus attribute placement); `now_panel_test.go` third tab and count ids; book page shows
    the split only when the play button shows and never in link mode; route-matrix rows for the 5 routes; browser test
    `queue_browser_test.go` at desktop-1440 and mobile-390: open menu, Add → toast + menu closed + focus on summary,
    menu stays open across a `progress-saved` refresh, panel tab move/remove in place without a page load (player keeps
    playing), keyboard-only path. Visual captures at 390/768/1024/1440 of the book page (menu closed and open), the
    panel Up next tab, `/queue` empty and filled, four themes for the split; shelf captures identical before/after step 1.
12. `docs/roadmap.md` status line. List every new string in the report.

Gates: changed-package `go test -race`, `make generate` clean, `make check`, `make build`, CSS budget test.

---------------------------------------------------------------------------------------------------------------
## Q3: JS: autoplay and three-tab keys (builder, sonnet, ~80 tool calls). Needs the operator to approve the budget first.

### Decisions
- Autoplay hooks the existing last-file branch of `audio` `ended` in player.js, after `save("end")` resolves.
- Found by reading (not yet reproduced): after a book ends, switching books calls `save("pause")` with no `finished`,
  and `progress.save` sets `finished_at = NULL` on a newer non-finished save, so the ended book is un-finished.
  Autoplay would hit this on every hand-over. One guard in `save()` fixes both paths.
- now-panel.js arrow keys assume two tabs (`find(t => t !== tab)`); three tabs need next/previous with wrap.

### Steps (exact, measured in a scratch copy)
1. `player.js` `fetchBook(id)` → `fetchBook(id, init)`; `fetch(url, init)`. `loadBook`: `fetchBook(id, options && options.init)`.
2. `save()`: after the first guard add `if (audio.ended && kind !== "end") return; // a book that ended keeps its finished mark`.
3. `ended` handler: comment "the last file ended: the book is finished, then Up next plays its first book";
   `save("end").then(playNext);`.
4. New `playNext()` above `listen(audio, "error", ...)`, with a 3-line comment (server pops the first book and drops the
   ended one; 204 when empty; nothing in link mode or when the person started something else):
   `if (LINK || !audio.ended || !book) return;` then `loadBook("next", { play: true, init: { method: "POST", headers:
   { "X-CSRF-Token": csrfToken(), "Content-Type": FORM_TYPE }, body: "ended=" + encode(book.id) } });`
5. `now-panel.js` keydown: `const list = tab.parentElement; const other = event.key === "ArrowRight" ?
   tab.nextElementSibling || list.firstElementChild : tab.previousElementSibling || list.lastElementChild;`; comment
   "arrows move to the next or previous one, wrapping round".
6. `internal/web/static/motion_test.go`: the approved numbers with a comment line each (below).
7. Tests: browser `queue_autoplay_browser_test.go` (two synthetic 2 s books, B queued, play A near its end → B loads and
   plays, `GET /api/v1/queue` empty, A `finished_at` set); regression test that a book that ended stays finished after
   starting another book (fails before step 2); empty queue → bar stays on A, paused; link mode → no request; arrow keys
   cycle About → Bookmarks → Up next → About and back.

### Budget proposal (gzip bytes as served, BestCompression; measured 2026-11-03)
| File | Now | After | Budget now | Proposed |
| --- | --- | --- | --- | --- |
| player.js | 12,737 | 12,950 (+213) | 12,800 | 13,056 (12.75 KB) |
| now-panel.js | 1,020 | 1,086 (+66) | 1,024 | 1,152 (1.125 KB) |
| all js/*.js | 38,776 | 39,055 (+279) | 38,912 | 39,168 (38.25 KB) |
Trims checked: dropping the `ended` body saves 7 B (not worth the less explicit rule); the cheaper index-free arrow
code is already used (22 B under the first draft); one `save()` guard instead of two call-site guards. Nothing else
without squeezing.

Gates: `go test -race ./internal/web/static/...`, the touched browser tests (TENDSHELF_TEST_BROWSER), `make check`, `make build`.

---------------------------------------------------------------------------------------------------------------
## Out of scope
Shuffle, repeat, cross-user or shared queues, drag reordering, Play next/Add to queue on cards, Home hero or series
rows, a play button inside Up next rows, live refresh of an open `/queue` page when autoplay pops a book, a change feed
for the queue (apps re-read the list), toasts inside the modal full player on phone (errors go to `#toasts`),
offline queueing, freeing the cap from rows hidden by lost access.
