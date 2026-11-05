# M5 Great reader: implementation plan

The must-have reader is on main (497720b). Input: [research](../research/ebook-reader-features.md), notes G1-G7 in
[m5-book-reader.md](m5-book-reader.md). Decision for G6: [0019](../decisions/0019-offline-reading.md). A Sonnet builder executes this
literally. Anything unclear is `PLAN GAP: <one line>`, after committing what is green. Copy marked NEW follows `BRAND.md` and goes in
the report; words live in `*_copy.go`. Every package appends a status paragraph under M5 in `docs/roadmap.md` and updates `docs/reader.md`.

## Packages and order

Order by value, as briefed. G3 stays after G6 on purpose: its recipe bump is the first real run of G6's `revision` path.

| Wave | Package (~tool calls) | Parallel with | Shared files |
| --- | --- | --- | --- |
| 1 | G6a Offline reading: server, worker, place queue (~190) | nothing | `sw.js`, `reader.js`, `reader.templ`, `offline.js`, `AGENTS.md` |
| 2 | G6b Offline reading: downloads and surfaces (~170) | L | `downloads.js`, `routes_offline.go`, `play_link.go`, `motion_test.go` |
| 2 | L Leftovers: series across formats (~110) | G6b | `home_views.go`, `book_views.go`, `listening.go` |
| 3 | G4 Skim (~130) | G2 | `reader.templ`, `reader.js`, `pages-reader.css`, `reader_start.go`, `motion_test.go` |
| 3 | G2 Reading stats (~170) | G4 | `listening_views.go`, `listening.templ`, `stats_copy.go` |
| 4 | G5 Comfort (~120) | G3 | `reader.templ`, `pages-reader.css`, `reader_settings.templ`, `motion_test.go` |
| 4 | G3 Print pages (~130) | G5 | `reader.templ`, `reader_chapter.go`, `ebook/*` |
| 5 | G7 Series and shelves (~110) | G1 | `reader.templ`, `reader_chapter.go`, `routes_book.go` |
| 5 | G1 Look-up and word list (~200) | G7 | `reader.templ`, `reader-marks.js`, `ebook/merge.go`, `motion_test.go`, migration 0022 |

Each wave's packages each edit `motion_test.go` rows and `routes_matrix_test.go`; the orchestrator resolves them and the roadmap.
`internal/web` is one test package. Prefixes: G6a `TestOffRead*`, helpers `ofr*`; G6b `TestOffDownload*`, `ofd*`; L `TestSeriesFormats*`,
`ls*`; G4 `TestSkim*`, `sk*`; G2 `TestReadStats*`, `rst*`; G5 `TestComfort*`, `cf*`; G3 `TestPrintPages*`, `pp*`; G7 `TestSeriesNext*`,
`TestToRead*`, `ct*`; G1 `TestDefine*`, `TestWordList*`, `TestMigration0022*`, `lk*` (`TestDict*` in `internal/dict`). Checked free on main.
`TestLookup` is taken elsewhere: do not use it.

## Invariants (all packages)

1. Plan 0018's invariants hold, above all: files read-only; every text route 404 without `share.Can(user, ReadText, book)`; marks, places,
   sessions, words are read and written only for `user_id = current user`; a place is an anchor, never a page number; listening and reading never write each other.
2. No telemetry and no third-party request from Tendshelf. A web search is a link the reader opens; only the selected word goes in it.
3. Nothing to install. A missing dictionary is a plain sentence, not an error. No dictionary is bundled.
4. Lists filter by grants in SQL (`visible()`, `CardsByID` with `s.scope(r)`); a revoked book's title never shows.
5. New JS is a lazily loaded module; never minify, never squeeze; an overrun is reported. No `style=""`; CSSOM only.
6. Opening, skimming or looking up never writes a place. Only reading on does.
7. All themes, four widths, 44 px targets, 13 px text, `prefers-reduced-motion`. No hard-coded colour.

## Budgets (numbers needing approval)

Measured on main by `TestJSBudget` (the served bytes; `gzip -9` reads differently). The brief's "bytes left" were a little low; these rule:
`sw.js` 3,023 of 3,072; `offline.js` 3,081 of 3,328; `downloads.js` 8,011 of 8,192; `reader.js` 5,962 of 6,144; `reader-marks.js` 2,789 of
2,816; `reader-settings.js` 901 of 1,536; `shell.js` 5,700 of 5,760; `player.js` 13,261 of 13,312; **total 76,181 of 76,288 (107 left)**.
`pages-reader.css` 3,908 of 4,096. `TestPageWeight` measured: home 61 of 61 requests (no margin: nothing here may add an asset to Home),
reader 54 of 56 requests, 44,664 of 52,224 B HTML, 203,295 of 234,496 B JS. The precache does not count there (the page's Resource Timing
does not see the worker), so G6's precache change costs no budget; G6a confirms with `make page-weight`. Estimates, not measurements.

| Package | File | Budget | App JS total after (cap) |
| --- | --- | --- | --- |
| G6a | `sw.js` | 3 → 3.5 KB (3,584) | |
| G6a | `reader-offline.js` (new; one more request on the reader, 55 of 56) | 2.25 KB (2,304) | 77.25 KB (79,104) |
| G6b | `downloads-text.js` (new, lazy) | 1.5 KB (1,536) | |
| G6b | `downloads.js` | 8 → 8.5 KB (8,704) | 79 KB (80,896) |
| G4 | `reader-skim.js` (new, lazy); `pages-reader.css` 4 → 4.5 KB (4,608), enough for G5 and G1 too | 1.25 KB (1,280) | 80.25 KB (82,176) |
| G5 | `reader-comfort.js` (new, only when a comfort setting is on) | 1 KB (1,024) | 81.25 KB (83,200) |
| G1 | `reader-lookup.js` (new, lazy); `reader-marks.js` 2.75 → 2.875 KB (2,944) | 0.75 KB (768) | **82 KB (83,968)** |
| L, G2, G3, G7 | no JS or CSS file changes size by more than rounding | | unchanged |

`reader.js` stays 6 KB: G6a removes more than G4 adds (expected about 6,040 B). `offline.js` stays 3.25 KB (expected about 3,210 B).

## Operator decisions (defaults apply unless changed)

1. Approve the budget table: app JS 74.5 → 82 KB, `pages-reader.css` 4.5 KB, `sw.js` 3.5 KB, `downloads.js` 8.5 KB (default yes).
2. Offline text needs "Can download" as well as the read right (default yes; 0019 point 2).
3. Progress on cards: default **no**. See L4, a ready optional step (about 40 calls) if the answer is yes.
4. Family stats view: default **no**. Showing one person's reading to another is the one privacy-shaped choice in G2 and nobody has asked for it yet.
5. Web search link for look-up: DuckDuckGo, a constant in `lookup_copy.go` (default yes).

---

## G6a Offline reading: server, worker, place queue

Read decision 0019 first. **Reused**: `ts-person-*`, `offline.js` wipe and backoff, `freshOrCached`, `pageOrShell`, `readerCtx`, `readerView`, `rsParts`, `rsFragURL`, `rsImage`. **Extended**: worker list, `Precache()`, `postReading`. **Replaced**: the `sessionStorage` save in `reader.js`.

Invariants: (a) the worker adds no write path of its own; (b) a shell is written last, so a half-stored book never opens; (c) `ts.pending` is never read or written for text; (d) the place reaches `localStorage` before any send; (e) everything new is keyed in `ts-person-*` or `ts.offline*`, so the wipe needs no change; (f) no change to the PASS list.

1. `static/static.go`: remove `readerStyleName` and its test in `Precache()`; fix the comment. `static/build_test.go:42`: assert it **is** in the precache.
2. `progress/reading.go`: no change to `AddReadingTime`. `routes_reading.go` `postReading`: pass `at` instead of `s.now()`, where `now := s.now().UnixMilli(); at := min(cu, now); if now-at > 30*86_400_000 { at = now }`.
3. `routes_offline_read.go` (new), registered from `registerOffline`:
   - `GET /offline/books/{id}/manifest` and `GET /offline/books/{id}/read`. Both start with `s.readerCtx` (no `edition` in the address, so the person's default), then `s.can.Can(ctx, u, share.DownloadBook, target)` else `s.notFound`. Audio-only books answer 404 through `readerCtx`.
   - Manifest JSON, `Cache-Control: no-store`: `{"bookId","editionId","revision","title","author","cover","bytes","finishedAt","parts":[...],"images":[...]}`. `parts`: every `rc.rsFragURL(idx, part)` in order. `images`: `rsImageURL(rc, b)` (split out of `rsImage`) for each block `KindImg`, `Format == "epub"`, `Img != ""`. `cover`: `cardView(...).CoverSrc`. `bytes`: `3*ed.CharCount + 150_000*len(images)`, an estimate. `finishedAt`: from `reading_positions`, 0 if none. `revision`: first 16 hex of SHA-256 over the edition id, then each `Highlight.ID:UpdatedAt`, then each text bookmark id.
   - Shell: `chap := s.readerChapter(ctx, u, rc, 1, 1)`, `v := s.readerView(r, rc, chap, pl)` with `pl` from `s.rsStartPlace(ctx, u, rc, url.Values{})`; then set `v.Offline = &pages.ReaderOfflineView{Module: s.assetOr("js/reader-offline.js"), ReaderSrc: s.assetOr("js/reader.js"), Shell: true, Placed: <reading client_updated_at or 0>, StartCh: pl.Ch, StartPart: pl.Part, Frag: "/hx/books/{id}/read/chapters/{c}?edition=<id>", PartStarts: <JSON>}`, and clear `ReaderModule`, `SettingsModule`, `SearchHref`, `MarksHref`, `SettingsHref`, `BookmarkURL`, `Speaker`, `Marks.Swatches`. `PartStarts` is `{"<chapter>":[1,121,...]}`: `From+1` of each `rsPart`, only chapters with more than one part. `?sheet=contents`: `s.readerSheet(r, rc, "contents", 0)` with `CloseHref = "/books/{id}/read"`, so closing never moves the place. Any other `sheet` 404. Answer through `s.pageCached(w, r, http.StatusOK, "private, no-store", v.Title, "", pages.Reader(v), sheet, pages.ReaderStyles...)`.
4. `pages/reader.templ`: `ReaderView.Offline *ReaderOfflineView`. On every page: `<span hidden data-ts-module={ OfflineModule }>` beside the settings marker. `readerView` sets `v.Offline = &pages.ReaderOfflineView{Module: ...}` online too. Shell only: `data-offline-shell`, `data-reader-src`, `data-placed`, `data-start-ch`, `data-start-part`, `data-frag`, `data-part-starts` on that span. `@ReaderChapter` is drawn only when `!Shell`. Search, marks, Aa, bookmark links are drawn only when their href is set (an `if`), the Note button only when `Swatches` is set. No `data-ts-offline-shell` on the reader shell: the page must not reload itself mid-reading when the connection returns.
5. `components/offline.templ`: `data-ts-reader-offline-src={ p.Assets.URL("js/reader-offline.js") }`.
6. `static/js/sw.js`, in the fetch handler, before the navigate branch:
   ```js
   // at the top of the file
   const READER = /^\/books\/[^/]+\/read$/;
   // in place of the present navigate branch
   } else if (/^\/hx\/books\/[^/]+\/read\/chapters\/\d+$/.test(url.pathname)) {
     event.respondWith(freshOrCached(request, url, url.pathname + url.search));
   } else if (/^\/books\/[^/]+\/read\/images\/[^/]+$/.test(url.pathname)) {
     event.respondWith(cachedOrNetwork(request, url));
   } else if (request.mode === "navigate" || request.headers.get("HX-Boosted") === "true" ||
              (READER.test(url.pathname) && url.searchParams.get("sheet") === "contents")) {
     event.respondWith(pageOrShell(request, url));
   ```
   `freshOrCached(request, url, key = url.pathname)`: returns `cached` at once when `self.navigator.onLine === false`; writes under `key`. `pageOrShell(request, url)` passes `url` to `shell(url)`, which first tries `stored(url.pathname + (sheet === "contents" ? "?sheet=contents" : ""))` when `READER` matches, then `/offline`. The `X-Ts-Offline` header stays. Update the worker's header comment.
7. `static/js/reader.js`: delete `pending`, `resend()` and its `online` listener. New `tell(name, form)`: `root.dispatchEvent(new CustomEvent("ts:reader:" + name, { detail: String(form) }))`. `save(beacon)`: after `dirty = false`, `const form = body(); chars = active = 0; tell("place", form);`. Beacon: `if (!navigator.onLine || !navigator.sendBeacon(root.dataset.save, form)) tell("unsent", form); return;`. Fetch: on ok `tell("saved", form)`, then the answer as today; on any failure `tell("unsent", form)`.
8. `static/js/reader-marks.js`: `[href*="sheet=marks"]').click()` becomes `?.click()` (the shell has no such link).
9. `static/js/reader-offline.js` (new, ES module, one statement per line). Exports `mount(marker)` and `flushReading(csrf)`.
   - Queue `localStorage["ts.offline.reading"]`: `{ "<bookId>": { form, chars, active, sent } }`. `form` is the urlencoded save minus `_csrf`, `chars_read`, `active_ms`.
   - On `#reader`: `ts:reader:place` sets `{form, chars: old.chars||0, active: old.active||0, sent: false}`. `ts:reader:unsent` adds that form's `chars_read`, `active_ms` to the record. `ts:reader:saved`: when `record.form` equals the stripped form and there are no stats left, `sent: true` if the book is held (`ts.offline` index `.books[id].text`), else delete.
   - `flushReading(csrf)`: for each record with `!sent`, in a loop: slice `min(chars, 200000)` and `min(active, 600000)`, `POST /reading/{id}` with `X-CSRF-Token`. `res.redirected`, a network error, 403, 429 or 5xx: return false. 200, or 400, 404, 413, 415, 422 (answered, never retried): subtract the slice from the record as it is **now**, and finish as `saved` does when `form` is unchanged. Returns true.
   - `mount`, shell: place = `at` in the address (`off` beside it); else `ch` and `part`; else the record when its `client_updated_at` beats `data-placed`; else `#reader`'s `data-at`, `data-off`; else `data-start-ch`, `data-start-part`. A sid gives chapter and block by `/^c(\d+)\.p(\d+)/`, the part by `data-part-starts`. Fetch `data-frag` with `{c}` filled and `&part=N` when N > 1 (the same bytes as `rsFragURL`). Not ok: `copy.failed` in `#reader-text`, stop. Else fill `#reader-text`, `window.htmx?.process(section)`, set `data-at`, `data-off`, then `(await import(data-reader-src)).mount(root)`. In a fragment the previous link is gone: for the scroll flow prepend a `ts-btn ts-btn--quiet ts-reader__prev` link to `/books/{id}/read?ch=&part=` (from `data-prev-href`) with `copy.prev`. Listeners go on **before** `reader.js` mounts.
   - `mount`, online page of a held book: when `navigator.onLine`, fetch `/offline/books/{id}/read` and `put` under `/books/{id}/read` (place, look and speed stay fresh). `document` `htmx:error` (a request that got no answer, not `htmx:response:error`) from inside `.ts-readerpage`: `window.tsPlayer?.toast("hint", copy.needsNet)`.
   - Every listener stops once the marker has left the page, as `reader-settings.js` does.
10. `static/js/offline.js`: `flushReading()` returns true unless some record has `!sent`, then `import(banner.dataset.tsReaderOfflineSrc)` and calls its `flushReading(csrfToken())`. `flush()`: `flushPending` && `flushMarks` && `flushReading`. `queued()` counts unsent records. `wipe()` is untouched.
11. `routes_reader.go` `readerWords`: add `prev`, `needsNet`. `reader_copy.go` NEW: `readerNeedsNet = "That needs a connection."`.
12. `motion_test.go`: rows above. `AGENTS.md`: replace the service-worker bullet with the text in 0019 "Rule for AGENTS.md". `docs/architecture.md` "PWA and offline (P4)": say what 0019 changes. 0012 header: "Amended by 0019."

Tests. Go: `TestOffReadManifest` (table: owner; member without `ReadText` 404; with `ReadText`, without `DownloadBook` 404; other library 404; audio-only 404; signed out redirect; a 2 MB chapter's `parts` equal each `data-next-href` walked from part 1; SVG picture absent from `images`; `revision` changes after a highlight and a delete), `TestOffReadShell` (no chapter text, no search, marks, Aa, bookmark or speaker, no `data-ts-offline-shell`, `Cache-Control`, contents variant, `CloseHref`), `TestOffReadFragURL` (`data-frag` filled equals `rsFragURL` for parts 1 and 2), `TestOffReadPrecache`, `TestOffReadSessionDate` (2 days old counts then; future counts now; 31 days old counts now), matrix rows for both routes.
Browser (`TENDSHELF_TEST_BROWSER`, `-tags dev`, 390 and 1440; `ofrSeedCache` stores the manifest's addresses and the shell from page script, since G6b has not landed; `ofrOffline` uses CDP as `offline_browser_test.go`): `TestOffReadBrowserOpen` (shell, turn across a part edge, picture shows, contents opens and a chapter link opens, close keeps the place, both flows), `TestOffReadBrowserPlace` (read offline, close the tab, reopen at the place; online: exactly one place on the server, `ts.pending` untouched, characters arrive once), `TestOffReadBrowserHang` (requests hang, as `offline_hang_browser_test.go`: the lost beacon's place still lands), `TestOffReadBrowserSlices` (a record of 25 minutes sends three posts), `TestOffReadBrowserWipe` (sign-out and change of person clear the cache and `ts.offline.reading`), `TestOffReadBrowserPass` (with a stored book, offline `/s/x`, `/api/v1/me`, `/login`, `/reading/{id}`, `/books/{id}/read?sheet=marks` and `?download=1` are not answered from the cache), `TestOffReadBrowserNeedsNet` (highlight on an open page, connection gone: toast).
Gates: `make generate`; `go test -race ./internal/web/ ./internal/web/static/ ./internal/progress/` (new tests, `TestJSBudget`, `TestCSSBudget`); `make page-weight`; `make check`; `make build`.
Out of scope: everything in 0019 "Not handled"; the download itself (G6b).

## G6b Offline reading: downloads and surfaces

**Reused**: the queue, lock, `put`, `Halt`, `settle`, `patch`, `reseed`, `checkFinished`, `bookDownload`. Invariants: as G6a; the audio path of a book with no text behaves exactly as today.

1. Record shape: audio fields unchanged; `files` may be `[]`; new `text: { editionId, revision, parts: [...], images: [...], shell: "/books/{id}/read", bytes, have }`, absent for audio-only. `bytes` of the record = files + `text.bytes`.
2. `static/js/downloads-text.js` (new; `import` from `banner.dataset.tsDownloadsTextSrc`; `components/offline.templ` adds the attribute). Exports:
   - `plan(bookId)`: GET manifest; 404 or redirect null.
   - `run(id, record, signal, kit)`, `kit = {openCache, put, patch, Halt}`: for each of `parts`: `fetch(url, {cache: "no-store", signal})`; redirect or 403/404 `Halt("failed","gone")`; 5xx up to three waits of 5, 15, 60 s, then `Halt("paused","failed")`; `put(cache, url, res)`, skip a url already stored unless `fresh`; `r.done` grows by `bytes / count`, `r.text.have++`. Images the same, except a 404 is skipped. Then the shell: fetch `/offline/books/{id}/read` and put under `/books/{id}/read`; `/offline/books/{id}/read?sheet=contents` under `/books/{id}/read?sheet=contents`; contents first, read shell **last**. Then set `text.bytes` from the summed `Content-Length` or blob size.
   - `purge(cache, record)`: delete the shell first, then contents, parts, images; drop `ts.offline.reading[id]`.
   - `refresh(id, record, kit)`: GET manifest. 404: return `"gone"`. Redirect or network: `"keep"`. New `revision`: store with `fresh` true under the new addresses, shell last, then delete addresses not in the new list; state stays `ready`. A record with no `text` whose manifest answers gains it the same way. Returns `{finishedAt}`.
3. `static/js/downloads.js`: `start()`: `const text = await (await textModule()).plan(bookId)`; the API read stays; its failure is no longer fatal when `text` is set. Files empty and no text: return. `title`, `author` from `detail` or `text`; `cover` from `text.cover` until `keepPage` writes it. `runBook`: `if (record.files.length) await keepPage(record)`; after the files, `if (record.text) await run(...)`. `purge()` calls the module when `record.text`. `save()`: `r.bytes` and `r.done` add the text's share. `reseed()`: adds `&text=` with the ready ids that have `text`. `checkFinished`: for each ready record call `refresh`; `"gone"` with `text` removes the text part (and the record when `files` is empty); text-only books take `finishedAt` from it, so "Remove finished books" works for them. `renderRow`: link `/books/{id}/read` when `files` is empty.
4. `routes_downloads.go` / `play_link.go:186`: `bookDownloadView(r, book, v.Button != nil || mayRead)`; still needs `DownloadBook`.
5. `routes_offline.go` `getOffline`: new `?text=` list through `offlineIDs`. `offlineView`: a row is kept when it has a play link **or** its id is in `text` and `mayReadBook` holds; `!can` no longer skips such a row. `OfflineRow.Read *components.ReadButtonView` (`Secondary` true when `Play` is set, label `readOther`). Meta for text only: `offlineReadyPct` from `percent_bp`, else `offlineReady`. `pages/offline.templ`: Read, then Play, in the row's slot.
6. `docs/copy-additions.md`: NEW words. Roadmap: the manual airplane check on the operator's phone (Tailscale HTTPS) is a recorded step for the orchestrator.

Tests (browser): `TestOffDownloadText` (text book: Download, ready, airplane, opens from `/offline` and from Downloads), `TestOffDownloadBoth` (one Download stores both; both rows work; audio bytes as before), `TestOffDownloadPartial` (shell absent until the end; pause and resume; quota error via `debug.failNextPut` is `failed` with the existing sentence), `TestOffDownloadRemove` (nothing of the book left in the cache or the queue), `TestOffDownloadRefresh` (a new highlight online re-stores; new edition replaces addresses; 404 removes the text; network failure keeps it), `TestOffDownloadFinished` (text-only, setting on). Go: `TestOffDownloadView` (the control's rights table), `TestOffDownloadShellRows` (`?text=` ids: unreadable id left out; bad id 400; audio row unchanged). Run the existing `offline_*browser_test.go` as the audio regression. Page-parity captures: book page, Downloads, `/offline` at 390, 768, 1024, 1440.
Gates: as G6a plus `make visual` for those pages.
Out of scope: a choice of format per download, Wi-Fi only, next in series downloads.

## L Leftovers: series across formats

**Extended**: `SeriesInProgress`, `homeSeriesView`, `seriesTileState`, `bookRails`. **Reused**: `lastFormat`, `readButton`, `mayReadBook`. No migration. Invariant: the row keeps one control (one resume button per book); nothing writes a place. Home has no spare request.

1. `library/listening.go` `SeriesInProgress`: add `LEFT JOIN reading_positions rp ON rp.book_id = b.id AND rp.user_id = ?`; SELECT adds `rp.user_id IS NOT NULL, rp.finished_at IS NOT NULL, COALESCE(rp.percent_bp,0), COALESCE(rp.client_updated_at,0)`; WHERE is `(EXISTS progress ... OR EXISTS (SELECT 1 FROM book_series bs3 JOIN reading_positions r3 ON r3.book_id = bs3.book_id WHERE bs3.series_id = bs.series_id AND r3.user_id = ?))`. Args: user four times, then `w.args`. `entry`: `has` either; `finished` either (a book read to the end is done, whichever format); `updated` the larger. `SeriesNext` gains `ReadBP int`, `ReadLast bool` (reading newer than listening, or no listening). Fix its doc comment.
2. `home_views.go` `homeSeriesView`: `started := sn.Started && (sn.PositionMs > 0 || sn.ReadBP > 0)`. Percent: `ReadBP/100` when `ReadLast`, else as today. `progress.Service.ReadingForBooks(ctx, userID, ids) (map[string]ReadingPosition, error)` (new; on the `ForBooks` pattern). Control: `mayRead := s.mayReadBook(...)`; `lastFormat(p, rp, link != nil, mayRead)`; `formatText` gives `row.Read`, else `row.Play` as today. `components.ReadButtonView.Round bool`: icon-only, the same size classes as `PlayButton` Round. `HomeSeriesRowView.Read`; `home_listening.templ` draws it where `Play` goes. Aria: `homeSeriesStart`, `homeSeriesCont` with `Read`, and NEW `homeSeriesRead = "Read %s"` wording only if the listen words do not fit.
3. `book_views.go` `seriesTileState`: new parameter `read map[string]progress.ReadingPosition`. Finished in either; started takes the format used last: reading gives `bookTilePercent` of `PercentBP/100`, clamped 1 to 99. "Start series" skips a book finished in either. `bookRails` callers load `ReadingForBooks` with the person's visible ids.
4. **Optional: only if operator decision 3 is yes.** Cards: a 3 px hairline at the cover's foot on the library grid and series and shelf rows, only for a book started and unfinished, from the format used last; `CardView.Progress string`, in `AriaLabel` as NEW ", {n} % through". `ts-cover__line` in `bundle.css` via `ts-value`; one extra query per list page (`ForBooks`, `ReadingForBooks`). It has no board and changes every library capture, which is why the default is no: Home's hero, series rows and series tiles already answer "where was I".

Tests: `TestSeriesFormatsRepo` (table: started by reading only; finished by reading moves on; both, newest wins; hidden book's reading not shown; another person's reading ignored), `TestSeriesFormatsHome` (Read shown for a text book; hero's book no control; no right to read, no Read), `TestSeriesFormatsTile` (table). Gates: `go test -race ./internal/library/ ./internal/progress/ ./internal/web/`, `make generate`, `make check`, `make build`, `make visual` for Home and book.
Out of scope: series with near-identical names across formats (owner edits details), cards unless decided.

## G4 Skim

**Reused**: `ts.back.<book>` and the chip (`reader-marks.js`), `rsStartPlace`, the ruler. **Extended**: the dock. Invariants: a jump alone saves nothing; `reader-marks.js` is not touched; previews lay out nothing; works with a keyboard and a screen reader.

1. `reader_start.go`: a new rung right after the `at` block, before the `pl.Ch != 0` return, so it wins over `ch`: `bp` in the address, an integer 0 to 10000 (else ignored), when `part` is not given: the block holding `bp*total/10000` characters gives `pl.Ch`, `pl.Part`, `pl.At` (its first sentence), off 0. Helper `rsPlaceAtChars(doc, chars)`, using `rsCharsBefore` and `rsParts`.
2. `routes_reader.go` `readerView`: `Root.Skim` is JSON `[[chars_before, "title"], ...]`, a title cut to 60 runes, at most 300 entries (past that, every nth), on `data-skim` of `#reader`. `reader.templ`, dock, after the ruler: `<input type="range" id="reader-skim" class="ts-skim" min="0" max="10000" step="1" aria-label={ rulerLabel } data-src={ SkimModule } hidden>` and `<output id="reader-skim-tip" class="ts-skim__tip" hidden></output>`. The ruler stays the picture.
3. `reader.js`: mount removes `hidden` from the input; `paint()` sets its `value` from `percent`; first `pointerdown`, `keydown` or `focus` on it imports `data-src` once and calls `skim(root, input, {go, place: () => here})`. In `moved()`: three forward turns since arrival remove `.ts-reader__back`.
4. `static/js/reader-skim.js` (new). On `input`: the tip reads "{title} · {n} %" and `aria-valuetext` the same, placed by `--ts-value` through CSSOM. Commit on `pointerup`, `Enter`: `sessionStorage["ts.back.<book>"] = JSON.stringify([here.sid, here.off])` if not already set, then `go("/books/{id}/read?ch=" + chapter + "&bp=" + value)`. `Escape` restores the value. Arrow keys step 1 %, by `step` set to 100 while focused; Home and End are native. Offline `bp` is ignored and the chapter opens at its start (`reader-offline.js` reads `ch`): state it in `docs/reader.md`.
5. `pages-reader.css`: `.ts-skim` stacked over the ruler, 44 px tall, `touch-action: none`, transparent track, visible thumb (`::-webkit-slider-thumb`, `::-moz-range-thumb`, tokens only), `:focus-visible` ring; `.ts-skim__tip` above it, 13 px, `--surface`. The dock's height does not change.
6. `a11y_pages_test.go`: state `reader-skim`.

Tests: `TestSkimStart` (Go: each `bp`, 0, 10000, junk ignored, `at` wins), `TestSkimBrowserJump` (drag at 390 and 1440; no POST to `/reading` until a turn; the chip appears; it returns to the place; goes after three turns), `TestSkimBrowserKeys`, `TestSkimBrowserOffline` (chapter start, chip works), `TestSkimVisual`. Gates: `make generate`, `make a11y`, the tests, `TestJSBudget`, `TestCSSBudget`, `make check`, `make build`.
Out of scope: previews of text, thumbnails, a page-number target.

## G2 Reading stats

**Reused**: `pages.Listening`, `ListeningView`, `progress.PeriodOf`, `statsLocation`, `components.BarChartView`, `HeatGridView`, `ListeningDone`, the `reader.v1` settings pattern. **Extended**: `getListening` gives up its range parsing to a helper. No migration: `reading_sessions`, `reading_positions`, `user_settings`. Invariants: own rows only, even for an admin; a title only through `CardsByID` with scope; a finished book never counted without `finished_at`.

1. `routes_listening.go`: extract `listeningPeriod(w, r, loc) (progress.Period, time.Time, bool)`; behaviour unchanged (`listening_test.go` guards it).
2. `progress/reading_stats.go` (new): `type Reading struct { Period Period; Days []int64; TotalMs, Chars int64; DaysRead, DaysSoFar, Cpm, Finished, Streak int; Ever bool }`. `ReadingStats(ctx, userID, loc, p, now)`: `SELECT ended_at, active_ms, chars_read FROM reading_sessions WHERE user_id = ? AND ended_at >= ? AND ended_at < ?`, bucketed by local day of `ended_at`; `DaysRead` days of at least `StatsDayMinMs`; `Cpm` is chars per minute of the period, 0 under a minute. Streak: consecutive local days ending today or yesterday (the rule of `Stats`), walking back at most 400 days. `ReadingFinishes(ctx, userID, from, to) ([]ReadingFinish, error)`: `book_id, finished_at` from `reading_positions`, newest first, at most 12.
3. `users/reading_goal.go` (new), key `reading.v1`: `{"goalBooks": n}`, `LoadReadingGoal`, `SaveReadingGoal`, `0 <= n <= 365`, else `ErrInvalidSetting`.
4. `routes_reading_stats.go` (new): `GET /me/reading`, `POST /me/reading/goal` (`goal_books`; plain form, 303 back; htmx fragment; CSRF as usual; junk 422). `reading_stats_copy.go` NEW: title "Your reading", numbers "Time reading", "Days read", "Words a minute" (`Cpm/5`, rounded to 10, "About"), "Books finished"; cards "Time" (split `timeChart` in `listening_views.go` so its drawing takes the period and a `[]int64`; both pages call it), "Yearly goal", "Finished". Reuse `ListeningCard`; new optional body `Goal *ReadingGoalView` (`pages/listening.templ`): "{n} of {goal} books this year", a `ts-bar`, a number field and Save. Empty state on the listening pattern.
5. `stats_copy.go:104` beside `listenMore`: NEW link "Your reading" to `/me/reading`, shown when the person has `Ever` reading.
6. CSS: only classes that exist in the listening stylesheet. If a rule is needed, report it.

Tests: `TestReadStatsService` (table: midnight in two zones, empty, streak with a gap, another person's sessions ignored), `TestReadStatsPage` (ranges, 404 on a bad `at`, signed out), `TestReadStatsLeak` (admin sees own only; a revoked book's title gone, the count kept), `TestReadStatsGoal` (0, 1, 365, 366, CSRF), `TestReadStatsVisual`. `a11y_pages_test.go`: `reading-stats`. Gates: `go test -race ./internal/progress/ ./internal/users/ ./internal/web/`, `make generate`, `make a11y`, `make check`, `make build`.
Out of scope: family view (decision 4), notifications, per-book stats.

## G5 Comfort

**Reused**: `ReaderSettings`, `readerLook`, the settings sheet, `reader-settings.js`, `data-chrome`, the wake lock code shape of `eyes-free.js`. Invariants: all off by default; the contrast theme never tints; a hidden bar returns on focus (the rule exists); `reader.js` does not change.

**Brightness gesture: no.** No web platform API sets screen brightness, in a PWA or in Wails. A dim overlay would only lower contrast. Named here so nobody builds it.

1. `users/reader_settings.go`: `KeepAwake bool json:"keepAwake"`, `Warm string json:"warm"` (`off`, `evening`, `always`), `AutoHide bool json:"autoHide"`. Defaults false, `off`, false. `Validate`, `parseReader`.
2. `routes_reader_settings.go`: read the three; `routes_reader.go`: `data-keep-awake`, `data-warm`, `data-auto-hide` on `#reader`; `v.ComfortModule` set only when any is on; `data-comfort-src` on the settings marker always.
3. `reader_settings.templ`: under a "More" disclosure: `readerSwitch("keep_awake", ...)`, `readerChoices("warm", ...)`, `readerSwitch("auto_hide", ...)`. NEW: "Keep the screen on", "Warm light" (Off, In the evening, Always), "Hide the bar after a moment".
4. `reader-settings.js` `apply`: sets the three data attributes, and imports `data-comfort-src` once any is on.
5. `static/js/reader-comfort.js` (new): `mount(marker)`, reacting to `ts-reader-look`:
   - `.ts-readerpage` `data-warm="on"` when `always`, or `evening` and the device's local hour is 20 to 06; checked at mount and every 5 minutes.
   - Keep awake: `navigator.wakeLock.request("screen")` when visible, again on `visibilitychange`, released after 10 minutes with no input and when the page leaves. Refusal is silent.
   - Auto-hide: 3 s after the last pointer or key, `data-chrome="off"`, unless focus is in the chrome, a sheet or dialog is open, or text is selected.
6. `pages-reader.css`: `.ts-readerpage[data-warm="on"]::after { content: ""; position: fixed; inset: 0; pointer-events: none; background: color-mix(in srgb, var(--mark) 60%, transparent); mix-blend-mode: multiply; }`, none under `[data-theme="contrast"]`.
7. Native feel (decision 0011): `overscroll-behavior`, safe areas and scoped no-select are already in `pages-reader.css`. Check instant `:active` on the bar and report; add nothing else.

Tests: `TestComfortSettings` (Go: defaults, junk, old JSON), `TestComfortContrast` (`--read-ink` on `--ground` under the multiplied tint in dark, light, sepia, 4.5:1; lower the 60 % and report if it fails), `TestComfortBrowserWarm` (`warmAt(setting, hour)` is an exported pure function: table in the browser through `import()`; `always` shows the tint, the contrast theme does not; no test hook in the module), `TestComfortBrowserHide` (hides, focus shows, open sheet keeps it), `TestComfortBrowserWake` (lock taken, released on leaving; absent API silent), `TestComfortNotLoaded` (off: the module is not requested, requests 55). Gates: `go test -race ./internal/users/ ./internal/web/`, `make a11y`, `make page-weight`, `TestJSBudget`, `TestCSSBudget`, `make check`, `make build`.
Out of scope: sunset by location (it would need a place), a schedule picker.

## G3 Print pages

Answer to the open question: `SrcRef.Map` is filled by the builder for EPUB, and is **not needed**. A page target is a `part#id`, and `Block.anchors` already holds every id on or inside a block, inline ones too; `resolveLinks` resolves exactly this. Block-level is the grain: a page break inside a paragraph shows at the paragraph's start. **Reused**: `anchors`, `tocEntry`, `EnqueueStale`, `Reanchor`, `data-pg`. **Replaced**: the count `navData.Pages`. Invariants: a place stays an anchor; a page label is never stored in a place; ids do not change, so `Reanchor` is exact; the 1,800-character fallback is byte-for-byte as today.

1. `ebook/doc.go`: `Recipe = "tendshelf-ebook-v3"`; `Doc.PageList []PageMark` `json:"pageList,omitempty"` after `PageCount`; `PageMark{Label string json:"label"; Block string json:"block"}`. v2 documents load without it.
2. `ebook/epub.go`: `navData.Pages int` becomes `PageTargets []tocEntry`. Nav: `case "page-list"` appends `{Href: href, Text: txt}`. NCX: at `pageTarget` set `inPage`; its `text` is the label, its `content src` the href, appended at the end element. The part stays `part#frag` (`resolveRef` strips the fragment: rejoin it as `Anch` keys are built, `hb.Src.Part + "#" + id`). `parseEPUB`: `b.pageTargets = ...`, `Finish(meta, 0)`.
3. `ebook/builder.go`: `resolveLinks` returns `target`. `Finish`: each target resolved through it; unresolved dropped; label `normalizeString`, 1 to 16 runes, else dropped; at most 20,000. Sorted by chapter and block. Several on one block: the first wins. `pageCount` = the largest label that is a number, else `len(list)`. Empty list: exactly today's path.
4. `ebook/pdf.go`: after `Finish`, the first block of each `Src.Part == "page:N"` gives `PageMark{Label: N}`. `PageCount` stays `n`.
5. MOBI: nothing. `mbp:pagebreak` is a chapter split, not a print page.
6. `ebook.PageAt(d *Doc, chapter, block int) string` (pure): the last mark at or before the block, by binary search; `""` before the first.
7. `reader_chapter.go`: `pages.ReaderBlockView.Pg` and `ReaderChapterView.Pg` become strings. With `PageList`: `PageAt`. Without: `strconv.Itoa` of today's arithmetic. `v.Page` uses new `readerPageLabel = "p. %s of %d"`. `reader.templ`: `data-pg={ b.Pg }` in eight cases. `readerPageOf` stays for `readerWords`.
8. `reader.js:128`: `page: block.dataset.pg || ""`. `paint` already skips an empty page. `.replace("%d", ...)` takes "xii".
9. `docs/reader.md`: recipe v3. Operator note in the status: the first start re-reads every ebook through the normal queue, reading stays available meanwhile, and offline copies re-store at their next daily check.

Tests: `TestPrintPagesEPUB3`, `TestPrintPagesNCX` (labels, roman, unresolved, ids inside a sentence), `TestPrintPagesPDF`, `TestPrintPagesFallback` (no list: `pg` and `pageCount` as v2), `TestPrintPagesAt` (table), `TestPrintPagesDeterminism`, `TestPrintPagesReread` (v2 document upgraded, marks exact), `TestPrintPagesReader` (`data-pg`, the footer, a label past the first in a part), `TestPrintPagesBrowser`. Gates: `make generate`, `go test -race ./internal/ebook/ ./internal/web/`, `make check`, `make build`. Update goldens that name the recipe.
Out of scope: "go to page", inline `pagebreak` with no page list, page numbers in search or export.

## G7 Series and shelves

`browse.go visible()` already mixes formats in a series and `library/textbook.go` already fills series from ebooks. **Reused**: `SeriesInfo`, `SeriesSequences`, `BookSeries`, `CreateShelf`, `AddToShelf`, `postShelfAdd`, `seriesTileState` (after L). No migration. Invariants: scope on every list; a plain shelf stays a plain shelf.

1. `reader_series.go` (new): `s.nextInSeries(r, book) (*library.BookCard, library.SeriesRef, error)`: first of `BookSeries`, `SeriesInfo` with `s.scope(r)`, the book after this one. `pages.ReaderEnd` gains `NextHref, NextLabel`. `getReader` and `getReaderChapter` fill it when `chap.view.End != nil`, NEW "Next in {series}: {title}", a quiet link to `/books/{id}`, under the finished button. The book page picks the format.
2. `routes_book.go` `getSeries`: `c.Byline = joinNonEmpty(" · ", place, seriesTileState(i, -1, ...))`, with `ForBooks` and `ReadingForBooks`.
3. To read: a plain shelf. `library.ShelfNamed(ctx, ownerID, name)` get-or-create (`shelves_owner_name` is NOCASE-unique; a conflict re-reads). `routes_shelves.go`: `POST /books/{id}/to-read`, plain form and htmx, adds to the shelf `shelfToRead = "To read"` (NEW), idempotent. The add-to-shelf sheet shows it first, ticked if held, before the shelf exists. Removing a book uses the existing route; a person may rename or delete the shelf; a later tap makes a new one.
4. `docs/feature-map.md`: send a book stays later.

Tests: `TestSeriesNextEnd` (next; last in series none; hidden next skipped; no series none; fragment carries it), `TestSeriesNextPage` (bylines across formats), `TestToReadShelf` (created once; twice is one row; another person's shelf untouched; CSRF; rights 404), matrix rows. Gates: `go test -race ./internal/library/ ./internal/web/`, `make generate`, `make check`, `make build`, `make visual` for series and shelves.
Out of scope: **send a book by share link**. It means a reader with no session, no `share.Can` path and a place in the browser: a security contract of its own, for a separate package. Automatic next book, smart shelves.

## G1 Look-up and word list

Hard rules: definitions come only from files the owner adds; **no dictionary is bundled**; the web search is a link. **Reused**: `textmatch.Fold`, the Lstat and SameFile open of `routes_ebooks_download.go:81`, the data-folder idea of `routes_covers.go`, `<dialog class="ts-note">`, `readerCtx`, the export pattern of `reader_marks.go:290`. Invariants: `internal/dict` opens no network and writes nothing; definitions reach the page as escaped text only, whatever the dictionary holds; only the selected word, at most 64 runes, can reach a search link; nothing is sent until the person taps.

Migration `internal/db/migrations/0022_word_list.sql`:

```sql
-- 0022_word_list: words a person saved while reading. One row per person, book and word; edition SET NULL keeps the word.
CREATE TABLE word_list (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL,
  word       TEXT NOT NULL CHECK (length(word) BETWEEN 1 AND 64),
  folded     TEXT NOT NULL CHECK (length(folded) BETWEEN 1 AND 128),
  quote      TEXT NOT NULL DEFAULT '' CHECK (length(quote) <= 400),
  sid        TEXT CHECK (sid IS NULL OR length(sid) BETWEEN 6 AND 40),
  off        INTEGER NOT NULL DEFAULT 0 CHECK (off >= 0),
  created_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX word_list_person ON word_list(user_id, book_id, folded);
CREATE INDEX word_list_recent ON word_list(user_id, created_at);
CREATE INDEX word_list_book ON word_list(book_id);
CREATE INDEX word_list_edition ON word_list(edition_id) WHERE edition_id IS NOT NULL;
```

1. `db` test `TestMigration0022*` (from 0021's fixture and from empty; rows survive). `ebook/merge.go`: `UPDATE OR IGNORE word_list SET book_id=:to WHERE book_id=:from` at line 118; the split list at 338 the same with `back`. A word with no edition keeps its quote and has no jump.
2. `internal/dict` (new, standard library):
   - `Open(dir string) *Set`; `(*Set).Find(word string) []Entry`, `Entry{Dictionary, Word, Text string}`, at most 3 hits per dictionary; `(*Set).Count() int`.
   - `dir` is `<data>/dictionaries`, read at first use and again when its mtime changes. Only regular files by `Lstat`, no symlinks, at most 8 dictionaries.
   - StarDict: `.ifo` (`version`, `bookname`, `wordcount`, `idxfilesize`, `sametypesequence`, `idxoffsetbits`), `.idx` or `.idx.gz` (word, NUL, offset, size, big-endian, 32 or 64 bit), `.syn`, `.dict` or `.dict.dz` (dictzip: FEXTRA `RA`, `chlen`, chunk sizes, each chunk a `flate` reader on a `SectionReader`). Fields `m` and `t` as text; `h`, `g`, `x` tags stripped by a small `html` tokenizer; others skipped.
   - TSV: `<name>.tsv`, `word<TAB>definition`, UTF-8, sorted in memory.
   - Bounds: `.idx` 256 MiB, TSV 64 MiB, an entry read at most 256 KiB and cut to 4,000 runes. A damaged dictionary is skipped and logged, never fatal; a panic in a parser is recovered.
   - Match: `textmatch.Fold` on both sides. Then, for a book in English, one step of `-s`, `-es`, `-ed`, `-ing`, `-ly`. No stemming for other languages.
3. `lookup_copy.go`: NEW "Look up", "No dictionary has this word.", "No dictionary on this server yet.", "Search the web for “%s”", "This opens outside Tendshelf.", "Save word", "Word saved.", "Your word list", "Add a StarDict or TSV file to the dictionaries folder in the data folder." (admins only). `lookupSearchURL = "https://duckduckgo.com/?q="`.
4. `routes_lookup.go`: `GET /hx/books/{id}/read/lookup?q=&edition=`, `readerCtx`: q 1 to 64 runes, else 422 toast fragment. Answers `pages.ReaderLookup`: a `<dialog class="ts-note">` with the entries, the search link (`rel="noopener noreferrer" target="_blank"`, `q` only), the Save form. `POST /hx/books/{id}/words` (`edition_id`, `word`, `quote` cut to 400, `sid`, `off`): `INSERT ... ON CONFLICT DO NOTHING`, then the toast. Limit 5,000 words a person, toast NEW "Your word list is full."
5. `progress/words.go`: `AddWord`, `DeleteWord(ctx, userID, id)`. The listing is `library.WordList(ctx, sc, userID, bookID, cursor, limit)`, joined through `visible()`, so it filters by grants in SQL. A word of a book the person can no longer see stays stored and is not listed.
6. `routes_words.go`: `GET /me/words` (`?book=`, 50 a page, a jump link when `edition_id` and `sid` exist), `POST /me/words/{id}/delete`, `GET /me/words.md`, `GET /me/words.json` (own rows, `Content-Disposition: attachment`, `Cache-Control: no-store`). Link from the profile beside "Your reading". `pages/words.templ`.
7. `reader.templ`: `<button class="ts-selmenu__item" data-ts-mark="lookup">`, shown online only (`Swatches` set); `data-lookup-src` on `#reader`. `reader-marks.js` `act()`: `else if (kind === "lookup") import(reader.dataset.lookupSrc).then((m) => m.open(reader, marked))`; `else save(marked)` becomes `else if (kind === "highlight") save(marked)`.
8. `static/js/reader-lookup.js` (new): `open(reader, marked)`: more than 64 runes or a multi-sentence selection: first word. Fetch the fragment, show as `reader-notes.js` does (Escape, backdrop, focus back). The Save form posts the word, the sentence around the selection as quote, sid and off. Fails offline with the G6a toast.
9. `docs/reader.md`: how an owner adds a dictionary. `NOTICES.md`: nothing ported; the formats are read from their published descriptions. No new module.

Tests: `internal/dict`: `TestDictStarDict` (generated fixture; 32 and 64 bit; `.syn`; types), `TestDictDictzip` (a generated dictzip, entries across a chunk edge), `TestDictTSV`, `TestDictHostile` (truncated, offset past the end, huge size, symlink, HTML with `<script>`), `TestDictFold` (table). Web: `TestDefineRoute` (rights, 422, `<script>` in q and in a definition escaped, no dictionary sentence, admin sentence), `TestDefineSearchLink` (only the word, percent-encoded, `noopener noreferrer`), `TestDefineNoFetch` (a transport that fails the test: no outbound request), `TestDefineBrowser` (select, Look up, Escape, focus back, Save), `TestWordListRepo` (once per word, user B empty, limit), `TestWordListMerge`, `TestWordListPage` (revoked title gone, export bytes, user B empty, CSRF), `TestWordListMarksUntouched` (colour buttons still highlight; the Copy path saves nothing). `a11y_pages_test.go`: `reader-lookup`, `words`. Gates: `make generate`, `go test -race ./internal/dict/ ./internal/db/ ./internal/ebook/ ./internal/progress/ ./internal/library/ ./internal/web/`, `make a11y`, `TestJSBudget`, `TestCSSBudget`, `make check`, `make build`.
Out of scope: a bundled dictionary, downloading dictionaries, look-up offline (a client index), flashcards, an inline translate, phrase look-up.

## After the round

The orchestrator: full `make check` on the merged tree, `make visual-full`, `make a11y`, the manual airplane check on the operator's phone with "Rules for the Summer", a tag decision. `docs/vision.md` and `docs/feature-map.md` take the statements that became true. OPDS and KOReader sync stay "Later still" in the first plan.

## Operator answers (2026-11-04)

- Budgets approved as planned: app JS 82 KB (83,968 B); `sw.js` 3.5, `downloads.js` 8.5, `reader-marks.js` 2.875,
  `reader-offline.js` 2.25, `downloads-text.js` 1.5, `reader-skim.js` 1.25, `reader-comfort.js` 1, `reader-lookup.js`
  0.75 KB; `pages-reader.css` 4.5 KB.
- Progress on cards: yes, include optional step L4 (a thin bar on started books, audio and ebook alike).
- Web search link in look-up: DuckDuckGo.
- Defaults stand: offline text also needs "Can download"; no family stats view.
- Script ceiling raised to 86 KB (operator, 2026-11-05); per-file rows follow the measured sizes with comments kept, reported per package.
