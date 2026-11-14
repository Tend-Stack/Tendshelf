# Plan: format-switch-start (reader contents fix, choosing where to start in the other format)

## Invariants
1. When someone picks a chapter in the contents sheet, the page goes to that chapter and the sheet closes. Closing the sheet never navigates to an address rendered earlier. The picked chapter becomes the saved reading place through the existing `POST /reading/{bookID}`.
2. The chapter mapping is one pure, deterministic function. When it is unsure, it returns no match, and a chapter it did not match is never offered.
3. Opening a choice never stops or changes playback. Going to read keeps today's behaviour: a boosted swap of `#main`, so the player keeps playing.
4. A choice is shown only when at least two options remain. With one option the person goes straight there.
5. Authorization uses only the existing checks: `loadBook(share.ViewBook)`, `mayReadBook`, `canPlay`. No new tables and no new write endpoints. Places are written by the existing reader save and player save.
6. No inline scripts or styles. Every control is at least 44 px, keyboard reachable and labelled. The design works in all four themes and one-handed at 390 px.
7. The offline reader shell behaves as it does today; the existing test at `offline_read_browser_test.go:194-216` passes unchanged.

## Decisions
- **D1, cause of bug (1), checked.** Contents links are plain anchors under the body's inherited boost. They swap `#main` only and push `?ch=N`, and `#sheet` stays open. The sheet's close and scrim anchors (`reader.templ:497,502`) are `hx-boost="false"`, have no `data-ts-close`, and point to `CloseHref: rc.href("ch", cur)` (`routes_reader.go:306-307`). Clicking close therefore does a full GET of the old chapter. Escape and drag already close in place, which is why only the close button and scrim jump back. A second cause: `reader.js` only saves after a move (`dirty` is set in `moved()`), so a picked chapter that is never scrolled is never saved, and a reload goes back.
- **D2, fix.** (a) Online reader sheets get `data-ts-close`, the existing pattern from `cover_sheet.templ:31,36`. JavaScript closes in place; without JavaScript the href is still correct. (b) Contents links get `data-ts-sheet-go`, and `shell.js` calls `closeSheet()` on click without `preventDefault`, so the boosted navigation still happens. (c) The server sets `data-moved="1"` on `#reader` when the address names a place (`ch`, `at` or `bp`) and has no `sheet` parameter. `reader.js` then saves at once after layout. Without JavaScript, a contents link is a full GET of `?ch=N` with no sheet, so it already works. The offline shell keeps its bare close href and gets no `data-ts-close` (invariant 7).
- **D3, mapping.** Match in passes, each needing a unique key on both sides and skipping front matter: (1) equal `rdrTitleKey`; (2) equal own number (`rdrOwnNumber` on `textmatch.Norm`); (3) equal opening word for unnumbered titles (`rdrIsUnnumbered`, for example prologue). If the matched pairs do not increase in both indexes, the result is empty. There is no matching by position or count; it is better to offer nothing than a wrong chapter.
- **D4, primary option.** The first option is the place used most recently, compared by `ClientUpdatedAt` exactly as `lastFormat` does (decision 0018). "From the beginning/start" is always last.
- **D5, user interface.** One shared bottom sheet, `components.FormatChoice`. It uses the `ts-sheet-layer`/`ts-sheet` layer from `utilities.css`/`bundle.css`, which load on every page, and `ts-btn` rows. It is fetched into `#sheet` with `htmx.ajax`, which already has precedents in `now-panel.js:12` and `upload.js:186`. I chose this over the chip menu because it holds rows with two lines of text and focus handling already exists (`shell.js` `focusSheet`).
- **D6, live place.** The listening chapter and the reading chapter change while the person uses the book, so JavaScript adds the live value when the control is tapped: `ms=` (book position) from the player, `at=` (`#reader` `dataset.at`, which `reader.js` keeps current) from the reader. Anything missing or invalid falls back to the stored place.
- **D7, player read control.** I do not try to intercept the boosted anchor, because htmx 4 installs its listener on the element itself. The anchor becomes a `<button>` that keeps the same data attribute. The fragment answers with the sheet, or, when only one option remains, with `HX-Location: {"path":…,"target":"#main","select":"#main","swap":"outerSync"}`. htmx 4.0.0 supports `HX-Location`: I read `#de` in the vendored file, which pushes by default and returns handled. Still a guess until the browser test passes.
- **D8, reader speaker.** It stays a `PlayButton`, so pause while the book plays is unchanged. When a choice is possible it carries `data-ts-choose`, and `player.js` opens the choice instead of starting playback when the book is not playing. Options that play use the existing `[data-ts-play-at]` path in `player.js:1182-1197`: it seeks if the book is loaded and loads it otherwise. A plain `PlayButton` would just resume a book that is already loaded.

## Package P1: bug fix, mapping, reader speaker (about 180 calls)
1. `internal/web/format_switch.go` (new):
   - `type fmtChapter struct{ Title string; Front bool }`
   - `type chapterMap struct{ AudioToText []int /*1-based text chapter, 0 = none*/; TextToAudio []int /*0-based audio chapter, -1 = none*/ }`
   - `func mapChapters(audio []string, text []fmtChapter) chapterMap`, implementing D3.
   - `func audioChapterAt(chs []library.Chapter, ms int64) int`: the last chapter with `StartMs <= ms`, or -1. Refactor `currentChapterIdx` (`book_views.go:364`) to call it.
   - `type formatOption struct{ Label, Where, Href string; PlayAtMs int64; Play bool; at int64 }`, where `at` is the client time used for ordering.
   - `func orderOptions(places []formatOption, start formatOption) []formatOption`: newest place first, then start, and the first option marked primary.
2. `internal/web/format_rule.go` const block: the strings listed under Copy.
3. `internal/web/pages/reader.templ`:
   - `ReaderSheetView` gets `CloseInPlace bool`. Close and scrim render `data-ts-close` when it is true.
   - Contents `<a>` gets `data-ts-sheet-go`.
   - `ReaderRoot` gets `Moved bool`, rendered as `data-moved="1"` when true.
4. `internal/web/routes_reader.go`:
   - `readerSheet` sets `CloseInPlace: true`.
   - `readerView` builds the speaker:
     - Load `chs := s.lib.Chapters` and `m := mapChapters(...)` from `rc.doc.Chapters`.
     - "Listening place" means `p != nil && !p.Finished() && p.PositionMs > 0`.
     - A choice is possible when there is a listening place, or when any text chapter maps to an audio chapter with `StartMs > 0`. Then set `btn.Choose = rc.href("sheet","listen")` and `btn.Href = rc.href("ch",cur,"sheet","listen")`. Otherwise the speaker is unchanged from today.
   - `routes_offline_read.go` needs no change: it sets `Speaker = nil`, and its `CloseInPlace` stays false.
5. `internal/web/reader_chapter.go` `getReader`:
   - Accept `sheet=listen`. Return 404 when `playLinkFor` is nil, meaning no listen right or no audio.
   - Set `v.Root.Moved = sheet == "" && (q.Has("ch") || q.Get("at") != "" || q.Get("bp") != "")`.
   - For `listen`: the reading chapter is `pl.Ch`. Build the options:
     - **Chapter you are reading:** label `listenFromReading`, `Where` = the audio chapter's title, `PlayAtMs = chs[a].StartMs`. Drop it if it is unmapped, if `StartMs == 0`, or if the listening place falls in the same audio chapter.
     - **Where you left off listening:** label `listenContinue`, `Where` = the title of the audio chapter at `p.PositionMs`, `PlayAtMs = p.PositionMs`.
     - **Start:** label `listenFromStart`, `PlayAtMs = 0`.
     - Every play option gets `Href = playLink.Href()` at that time, through `playTarget`.
   - Render with `pageSheetStyled(..., components.FormatChoice(v))`, `CloseHref = rc.href("ch",cur)`.
6. `internal/web/components/play_button.templ`: `PlayButtonView.Choose string`, rendered as `data-ts-choose` when not empty.
7. `internal/web/components/format_choice.templ` (new):
   - `FormatChoiceView{Title, CloseLabel, CloseHref, BookID string; Options []FormatChoiceOption}`
   - `FormatChoiceOption{Label, Where, Href string; PlayAtMs int64; Play, Primary bool}`
   - Markup:
     - Layer: `div.ts-sheet-layer`.
     - Scrim: `a.ts-scrim` with `data-ts-close` (`hx-boost="false"` when `CloseHref` is set), else a `div` with `data-ts-close`.
     - Sheet: `div.ts-sheet.ts-choice-sheet` with `role="dialog"`, `aria-modal="true"`, `aria-labelledby="format-choice-title"`, `tabindex="-1"`, plus grab, head and close. Close is an anchor or a button, either way with `data-ts-close` and `aria-label`.
     - Body: `ul.ts-choice`, one `li` per option holding `a.ts-btn` (`--primary` or `--secondary`, `ts-btn--lg ts-choice__btn`).
     - Each row has `<span>`Label`</span>` and, when set, `<span class="caption ts-choice__where">`Where`</span>`, plus `data-ts-sheet-go`.
     - Play rows also get `hx-boost="false" data-ts-play-at data-ts-book`.
8. `internal/web/static/css/bundle.css`, four rules:
   - `.ts-choice` (grid, gap `--space-12`, no list style)
   - `.ts-choice__btn` (`width:100%`, column, align start, `min-height: 44px`)
   - `.ts-choice__where`
   - `.ts-choice-sheet` (`max-height: 85dvh`, scrolls)
9. `internal/web/static/js/shell.js`: a click on `[data-ts-sheet-go]` inside `#sheet` calls `closeSheet()` without `preventDefault`.
10. `internal/web/static/js/reader.js`, in `setFlow().then` after `paint()`: `if (root.dataset.moved === "1") { dirty = true; save(false); }`
11. `internal/web/static/js/player.js`, play handler after the "playing → pause" branch: when `data.tsChoose` is set, call `htmx.ajax("GET", data.tsChoose + "&at=" + encodeURIComponent(document.getElementById("reader")?.dataset.at || ""), {target:"#sheet", select:"#sheet > *", swap:"innerHTML", source: el})` and return.
12. Tests and gates, then commit.

## Package P2: the player's read control (about 150 calls; starts after P1 is merged)
1. `internal/web/routes_reader.go` `registerReader`: `GET /hx/books/{id}/read-from` → `s.getReadFrom` in `format_switch.go`.
   - `loadBook(share.ViewBook)`, then `mayReadBook`, else 404. Then `readerLoad(ctx,u,book,"")`; not found gives 404.
   - Find the listening chapter:
     - Only when `canPlay`. Use `ms` (an integer ≥ 0) if given, otherwise the listening place.
     - Then `audioChapterAt`, then `m.AudioToText`.
   - **Chapter option:** label `readFromListening`, `Where` = the text chapter's title, `Href = readerHref(book.ID, rc.ed.ID, "ch", k)`. Drop it if k is the first chapter that is not front matter, or if `rp.ChapterIdx == k`. Check in `progress/reading.go` that `ChapterIdx` is 1-based.
   - **Left-off option:** label `readContinue`, `Where = readWhere`, `Href = readHref(id)`. Only when `rp != nil && !rp.Finished() && rp.PercentBP > 0`.
   - **Start option:** label `readFromStart`, `Href = readerHref(id, rc.ed.ID, "ch", <first chapter that is not front matter>)`.
   - Fewer than two options: answer 200 with an empty body and `HX-Location` JSON for `readHref(id)`, built with `encoding/json`. Otherwise `s.fragment(w, r, 200, components.FormatChoice(v))` with `CloseHref` empty.
2. `internal/web/player_view.go:103-107`: also set `v.ReadChoose = "/hx/books/" + id + "/read-from"`. Add `ReadChoose` to `components/player_data.templ`, rendered as `data-read-choose`.
3. `components/player.templ:252` and `components/player_full.templ:121`:
   - Turn the `<a href="/">` into `<button type="button" … aria-haspopup="dialog">` and keep the `data-ts-bar-read` / `data-ts-full-read` attributes, icon and label.
4. `static/js/player.js:217`: add `readChoose: d.readChoose || ""`, and add it to the snapshot at `:1485`.
5. `static/js/player-full.js`:
   - `drawBook`: set `hidden = !book.readChoose` and stop setting `href`.
   - One click handler for both buttons:
     - If `!navigator.onLine`, call `location.assign(book.readHref)` (the offline reader shell).
     - Close the full player first when it is open and not the side panel (as at `:1041`).
     - Then `htmx.ajax("GET", book.readChoose + "?ms=" + Math.round(<the position source used near :1022>), {target:"#sheet", select:"#sheet > *", swap:"innerHTML", source: button})`.
6. Tests and gates, then commit.

## Copy (`format_rule.go`)
| Constant | Text | Status |
| --- | --- | --- |
| `chooseReadTitle` | "Read from" | NEW |
| `chooseListenTitle` | "Listen from" | NEW |
| `readFromListening` | "Read from where you're listening" | NEW |
| `readFromStart` | "Read from the beginning" | NEW |
| `listenFromReading` | "Listen from where you're reading" | NEW |
| `listenContinue` | "Continue listening" | NEW |
| `listenFromStart` | "Listen from the start" | NEW |
| `readContinue` + `readWhere` | "Continue reading" and its caption | existing |
| `readerCloseSheet` | the close button's label | existing |
| `readerSpeaker` | the speaker's label | existing |

## Tests
**P1**
- `format_switch_test.go` `TestMapChapters`, table rows:
  - exact titles
  - "Chapter 3" against "Chapter 3: The Wind"
  - accents and punctuation
  - prologue and epilogue
  - front matter is never matched
  - chapter numbers that restart in Part Two stay unmatched
  - an order inversion gives an empty map
  - "Track 01" gives nothing
  - empty inputs
- `TestOrderOptions`: places ordered by newest first, start last, the first marked primary.
- `reader_sheet_test.go`:
  - `TestReaderSheetClosesInPlace`: close and scrim carry `data-ts-close` with href `ch=cur`; contents links carry `data-ts-sheet-go`; the offline shell has neither and keeps its bare close.
  - `TestReaderMovedFlag`: `?ch=2`, `at` and `bp` set the flag; bare, `?ch=2&sheet=contents` and `?edition=` do not.
  - `TestReaderListenSheet`: options and their order, the duplicate cases, 404 without the listen right, 404 for a member without access to the book, and a straight speaker with no `data-ts-choose` when no choice exists.
- Browser tests (`-tags dev`, desktop-1440 and mobile-390):
  - `reader_toc_browser_test.go` `TestReaderContentsPickBrowser`: pick ch 3 and the sheet is gone with ch 3 shown; open again and close with the X, still ch 3; Escape, still ch 3; wait for `POST /reading`, reload `/books/{id}/read`, opens ch 3.
  - `TestReaderSpeakerChoiceBrowser`: the sheet's first row plays at the mapped audio chapter's start; tapping the speaker while it plays pauses it with no sheet.
- Visual and a11y rows for the "listen" sheet state at 390, 768, 1024 and 1440 px, using the seeded-id helpers.

**P2**
- `TestReadFromFragment`:
  - 404 when the book is not visible
  - 404 for a member who may listen but has no ReadText right
  - `ms` maps to a chapter; a bad `ms` is ignored
  - order by recency
  - with one option, `HX-Location` and an empty 200
- A route matrix row for `/hx/books/{id}/read-from`.
- Update `TestPlayerReadIconBrowser` (`formats_browser_test.go:38`): the button, and with one option the reader opens while the audio keeps playing.
- New `TestPlayerReadChoiceBrowser`: mini and full player; the full player closes and the sheet opens; picking a row opens the mapped chapter, the sheet is gone and the audio still plays.
- Visual and a11y rows for the "read" sheet.

**Gates for both**
- `go test -race` on `./internal/web/...`
- `make check`, `make build`, `make generate` with a clean tree
- `TestCSSBudget`, the JS budget tests (`motion_test.go:297-399`), `perf_weight_test.go`

## Out of scope
- No alignment and no voice sync. No matching inside a chapter: chapter starts only.
- No Android native code. The WebView uses the same markup, and `data-ts-play-at` already works with `NATIVE`.
- No change to the offline shell's close behaviour.
- `data-ts-sheet-go` on search and marks result links: they may have the same issue, but that is not verified, so it is a follow-up.
- `replaceState` to drop `?ch=` from the address after the save.
- Back and forward to a `?ch=` address will now save that place (a move); accepted.

## Decisions on the open questions (orchestrator, 2026-11-13)
1. The newest place is first (the `lastFormat` rule); "from the beginning/start" is always last.
2. A sheet that would hold only "Listen from the start" is accepted in the rare case described in D8's conditions; no extra logic.
3. Without JavaScript the reader still does not save a place; unchanged.

