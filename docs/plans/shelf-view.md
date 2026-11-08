# Shelf view: builder's contract (decision 0034)
Execute literally. A gap is `PLAN GAP: <one line>`, after committing what is green. No module, no migration (schema stays 37), no new JS file, no `shell.js` change.

## 1. Invariants
- One thickness function (`spineWidth`) and one component (`components.Spine`) on Home and Library. No inline style; CSS classes only.
- Every `a.ts-spine` is at least 44 x 44 CSS px; text at least 13 px; a real `ul`/`li`/`a` with title, author and length in the link's name.
- Height never changes between renders (hash of series or book id). No random value reaches the page.
- Card query stays one SQL statement, no per-book query; grants are untouched (`cards()` only receives ids already filtered in SQL).
- Large text (`textSize == "large"`) never draws a spine: Home keeps its cards, `?view=shelf` renders the grid and offers no Shelf option.
- Grid and List output is byte-compatible: the default view and every existing address keep their meaning.
- Tokens only in `pages-spines.css`; no literal colour, no `!important`. Tints stay the existing `data-ts-tint` path.

## 2. Decisions
- Reused: `spineInk`, `applyTints`, `fitShelf`, `withCardProgress`, `.ts-more` sentinel, `Icon("library")`, `Icon("check")`, `spineTitle`. Replaced: `spineWidth`/`spineHeight` bodies, the three width and three height classes, the plank `div`.
- 12 steps, square root, 1 h to 40 h clamp (decision 0034). Width = `--size-tap + --w * --spine-w-inc`, so one rule per step and two values per breakpoint.
- Slot model: `li` carries size classes, `a` fills it with 3 px inline padding, `.ts-spine__book` is the visible book. The gap is part of the target, so a thin book has no dead space around it.
- Fixed line height: `height + margin-top = --shelf-zone` for every slot, so each flex line is one zone. Plank = one repeating gradient. Probed in Chromium: drift at most 0.074 px over 8 rows at scale 1.1, 0 at 1, 1.25, 2.625, 3.
- `<a role="listitem">` is replaced: that role removes the link semantics. Real `ul role="list"` (kept because Safari drops list semantics with `list-style: none`) with `li`.
- Author from step 4 up (4.9 h; the room for it starts there): surname only, full name is in the link's name. Same ink as the title (no opacity: ink is chosen at 4.5:1).
- Signature details, and only these: length-true thickness; title size growing with the book; double rule bands from step 7 (13.4 h); ember ribbon for in progress; quiet check for finished. Series share a height.
- Rejected: facing covers, leaning books, shelf-wall shading (tried; smudgy in light), per-theme token additions, stored view preference.
- Home keeps `data-ts-fit`: with JavaScript it shows whole spines and hides the rest (commit b262578); without, it scrolls. Not a scrolling change.
- New file `pages-spines.css` (default `pages-*` budget 3 KB; 1,921 B gzip measured on the CSS below), so `motion_test.go` is untouched. Remove the moved rules from `bundle.css` and `pages-home.css`.

## 3. Thickness and sizes
Function: `t = (sqrt(clamp(h,1,40)) - 1) / (sqrt(40) - 1)`; `step = 1 + int(math.Round(11*t))`; `ms <= 0` gives `w4`.
| Step | Starts at (h) | Examples (h to step) | Slot px phone / tablet / desktop | Title | Extras |
|---|---|---|---|---|---|
| 1 | 0 | 0.5, 1, 1.5 | 44 / 44 / 44 | 13 px | |
| 2 | 1.54 | 2 | 48.8 / 49.6 / 50.4 | 13 | |
| 3 | 2.98 | 3, 4 | 53.6 / 55.2 / 56.8 | 13 | |
| 4 | 4.88 | 5, 6, 7 | 58.4 / 60.8 / 63.2 | 13 | author |
| 5 | 7.26 | 8, 10 | 63.2 / 66.4 / 69.6 | 15 | author |
| 6 | 10.1 | 12 | 68 / 72 / 76 | 15 | author |
| 7 | 13.41 | 15 | 72.8 / 77.6 / 82.4 | 15 | author, bands |
| 8 | 17.19 | 18, 20 | 77.6 / 83.2 / 88.8 | 15 | author, bands |
| 9 | 21.44 | 25 | 82.4 / 88.8 / 95.2 | 18 | author, bands |
| 10 | 26.16 | 30 | 87.2 / 94.4 / 101.6 | 18 | author, bands |
| 11 | 31.34 | 35 | 92 / 100 / 108 | 18 | author, bands |
| 12 | 37 | 40, 60, 100 | 96.8 / 105.6 / 114.4 | 18 | author, bands |
The visible book is slot minus 6 px. Heights h1 to h5: phone 148 to 180 px (zone 188), tablet 160 to 192 (zone 200), desktop 168 to 204 (zone 212). Plank 8 px, air 28 px; a phone row holds about 5 books.
Themes: `dark`, `light` and `sepia` all use the rules below (walnut plank, paper light, deep-brown shade, all derived from `--brand-walnut`, `--brand-paper`, `--spine-ink`). `contrast`: plank `--line-strong`, no cast, no shading, spines `--surface` + `--ink` + `--line-strong` (existing `.is-tinted` overrides); bands, ribbon and check stay.

## 4. Data shapes
- `library.BookCard`: add `TextWords int64 // words of a text book's newest edition, or pages * 300; 0 for audio or unknown`.
- `components.CardView`: add `SpineLabel string` ("{title} by {byline}, {length}"), `SpineAuthor string` (empty below step 4), `Finished bool`.
- `browseQuery.View`: "grid", "list" or "shelf". `pages.LibraryView`: add `ShelfHref string`, empty under large text. No endpoint is added; `/libraries`, its `/hx/` fragment and the library routes take `view=shelf`.

## 5. Steps, in order
1. `internal/library/browse.go`: add the field. In `cards()` add this as the last select column, scan into `&c.TextWords`:
   `COALESCE((SELECT CASE WHEN e.word_count > 0 THEN e.word_count ELSE e.page_count * 300 END FROM ebook_files f JOIN editions e ON e.ebook_file_id = f.id WHERE b.kind = 'text' AND f.book_id = b.id AND f.state = 'ready' ORDER BY f.book_default DESC, e.created_at DESC, e.id DESC LIMIT 1), 0)`
2. `internal/web/browse_views.go`: add `const spineWordsPerHour = 9300`. Add `spineLengthMs(c library.BookCard) int64` (text: `c.TextWords * hourMs / spineWordsPerHour`; else `c.DurationMs`). Rewrite `spineWidth(lengthMs int64) string` to return `ts-spine--w{step}` per section 3 (`spineStep(lengthMs) int` holds the formula). Rewrite `spineHeight(seed string)`: `""` gives `ts-spine--h3`; else `hash/fnv` New32a of seed, `% 5 + 1`. Add `spineAuthor(authors []library.PersonRef) string`: first author; before a comma if it has one, else its last word, skipping a trailing `Jr`, `Jr.`, `Sr`, `Sr.`, `II`, `III`.
3. Same file, `cardView`: `step` from `spineLengthMs(c)`; `WidthClass: spineWidth(...)`; `HeightClass: spineHeight(seed)` with seed `c.Series.ID` when the book has a series, else `c.ID`; `SpineAuthor` only when step >= 4; `SpineLabel` = AriaLabel base + `", " + Duration` when Duration is not empty. `parseBrowseQuery`: `switch v.Get("view")` accepts "list" and "shelf". `href`: `if q.View != "grid" { v.Set("view", q.View) }`, same parameter position. Fix the comment above `lengthShortH` (spine widths no longer share those edges).
4. `internal/web/components/cover.templ`: the three `CardView` fields, comment updated.
5. `internal/web/components/spine.templ`: replace with `spineSlotClass` ("ts-shelf__slot" + WidthClass + HeightClass), `spineBookClass` ("ts-spine__book" + InkClass + `is-finished` when Finished), and:
   `<li class=slot><a class="ts-spine" href aria-label={v.SpineLabel}><span class=book data-ts-tint?>` then, if `v.Progress != ""`, `<span class="ts-spine__ribbon"></span>`; then `<span class="ts-spine__text"><span class="ts-spine__title">{SpineTitle}</span>` plus `<span class="ts-spine__author">{SpineAuthor}</span>` if set, `</span>`; then if Finished `<span class="ts-spine__done">@Icon("check", false)</span>`; close all.
6. `internal/web/book_copy.go`: `cardFinished = "finished" // NEW: hidden words of the check on a finished book's spine`.
7. `internal/web/card_progress.go`, `withCardProgress`: after `standIn`, if `st.Finished` set `views[i].Finished = true`, `views[i].SpineLabel += ", " + cardFinished`, continue. Where it appends to AriaLabel, also append ProgressLabel to SpineLabel.
8. `internal/web/routes_browse.go`: add `func (s *server) libraryQuery(r *http.Request) browseQuery`: `parseBrowseQuery(r)`, then if View is "shelf" and `lookFor` says large, View = "grid". Use it at the three `/libraries` call sites (lines 295, 320, 663), not in `routes_api_read.go`. `libraryView`: `ShelfHref` like `ListHref`, left empty when large. `getHome`: after building `v.New`, call `s.withCardProgress(ctx, currentUser(r).ID, page.Books, v.New)`; on error `s.serverError(w, r, "home progress", err)`.
9. `internal/web/pages/library.templ`: `titleForm` hidden field when `v.View != "grid"` with `value={ v.View }`. Grid's `aria-current` when `v.View == "grid"`. After List, when `v.ShelfHref != ""`, a third `.ts-seg__item`: `@components.Icon("library", false)`, `<span class="ts-seg__text">Shelf</span>`, `aria-current` when "shelf". In `Library`: if shelf, `<ul id="library-grid" class="ts-lib-items ts-shelf ts-shelf--wrap" role="list">`, else the existing `div`. In `libraryItems`: if shelf, `@components.Spine(c)` for each, and the sentinel as `<li class="ts-shelf__more"><a class="ts-more" ... hx-target="closest li" hx-swap="outerHTML" hx-push-url="false">` with the same `href`, `hx-get`, `hx-trigger`, label and loading spans. Update the `View` comment.
10. `internal/web/pages/home.templ`: `<ul class="ts-shelf ts-shelf--scroll" role="list" data-ts-fit>`; delete the `.ts-shelf__plank` div. `BrowseStyles`: add `"css/pages-spines.css"` after `pages-home.css`.
11. Create `internal/web/static/css/pages-spines.css` from section 6 verbatim, under a header comment. `pages-home.css`: delete lines 34-48 (scroll rule, spines), the `.ts-shelf--scroll` and `.ts-spine--*` lines in both media blocks (74-80, 86-92), and lines 95-97; keep covers and `.is-tinted`; fix its header comment. `bundle.css`: delete "Spines on a shelf" (lines 438-442); it comes off every page. `pages-library.css`: comment "Grid/List" becomes "the view choices".
12. `make generate`; update existing tests (section 7). Append one row to `docs/copy-additions.md`: `Shelf (the third view on the Library) · finished (hidden words on a finished book's spine)`. Add the status slice to `docs/roadmap.md`. Set decision 0034 to accepted only if the lead says so.

## 6. CSS, verbatim (this CSS viewed at dark 390, light 390, contrast 1440; hover, focus and reduced motion are not yet seen, the browser test covers them)
```css
.ts-shelf {
  --spine-w-inc: 0.3rem; --spine-h-min: 9.25rem; --spine-h-inc: 0.5rem;
  --shelf-zone: calc(var(--spine-h-min) + 4 * var(--spine-h-inc) + 0.5rem); /* tallest book, plus room for the lift and the ribbon */
  --shelf-plank: 0.5rem; --shelf-air: 1.75rem;
  --plank: var(--brand-walnut);
  --plank-light: color-mix(in oklab, var(--brand-walnut) 72%, var(--brand-paper));
  --plank-dark: color-mix(in oklab, var(--brand-walnut) 70%, var(--spine-ink));
  --plank-cast: color-mix(in oklab, var(--spine-ink) 24%, transparent);
  --book-cast: color-mix(in oklab, var(--spine-ink) 30%, transparent);
  display: flex; align-items: flex-end; margin: 0; padding: 0 0 var(--shelf-plank); list-style: none;
  -webkit-user-select: none; user-select: none; -webkit-touch-callout: none;
  background-image: linear-gradient(to bottom, transparent var(--shelf-zone),
    var(--plank-light) var(--shelf-zone), var(--plank-light) calc(var(--shelf-zone) + 1px),
    var(--plank) calc(var(--shelf-zone) + 1px), var(--plank) calc(var(--shelf-zone) + var(--shelf-plank) - 1px),
    var(--plank-dark) calc(var(--shelf-zone) + var(--shelf-plank) - 1px), var(--plank-dark) calc(var(--shelf-zone) + var(--shelf-plank)),
    var(--plank-cast) calc(var(--shelf-zone) + var(--shelf-plank)), transparent calc(var(--shelf-zone) + var(--shelf-plank) + 0.75rem));
  background-size: 100% calc(var(--shelf-zone) + var(--shelf-plank) + var(--shelf-air)); background-repeat: repeat-y;
}
.ts-shelf--wrap { flex-wrap: wrap; row-gap: calc(var(--shelf-plank) + var(--shelf-air)); }
.ts-shelf--scroll { margin-top: var(--space-8); padding-inline: var(--space-4); overflow-x: auto; overflow-y: hidden; overscroll-behavior-x: contain; }
.ts-shelf__slot { --book-h: calc(var(--spine-h-min) + var(--h) * var(--spine-h-inc)); flex: none; width: calc(var(--size-tap) + var(--w) * var(--spine-w-inc)); height: var(--book-h); margin-top: calc(var(--shelf-zone) - var(--book-h)); }
.ts-shelf__slot[hidden] { display: none; }
.ts-shelf__more { flex: 0 0 100%; height: var(--shelf-zone); display: flex; align-items: center; justify-content: center; }
.ts-shelf .ts-more.htmx-request .ts-more__loading { display: none; }
.ts-spine { display: block; box-sizing: border-box; height: 100%; padding-inline: 0.1875rem; text-decoration: none; -webkit-user-drag: none; -webkit-tap-highlight-color: transparent; }
.ts-spine:focus-visible { position: relative; z-index: 1; outline: none; }
.ts-spine__book { position: relative; box-sizing: border-box; display: flex; align-items: center; justify-content: center; height: 100%; border: 1px solid transparent; border-radius: var(--radius-cover) var(--radius-cover) 1px 1px; box-shadow: 1px 0 3px var(--book-cast); transition: transform var(--duration-fast) var(--easing-standard); }
.ts-spine__book:not(.is-tinted) { background: var(--surface); color: var(--ink); border-color: var(--line-strong); }
.ts-spine__book::before { content: ""; position: absolute; inset: 0; border-radius: inherit; pointer-events: none; background-image: linear-gradient(to right, color-mix(in oklab, var(--brand-paper) 16%, transparent), transparent 6%, transparent 88%, color-mix(in oklab, var(--spine-ink) 30%, transparent)); }
.ts-spine:focus-visible .ts-spine__book { box-shadow: var(--focus-ring); transform: translateY(-0.25rem); }
.ts-spine:active .ts-spine__book { transform: scale(0.98); transition-duration: 0s; }
@media (hover: hover) { .ts-spine:hover .ts-spine__book { transform: translateY(-0.25rem); } .ts-spine:active:hover .ts-spine__book { transform: scale(0.98); } }
@media (prefers-reduced-motion: reduce) { .ts-spine__book { transition: none; } .ts-spine .ts-spine__book, .ts-spine:hover .ts-spine__book, .ts-spine:focus-visible .ts-spine__book, .ts-spine:active .ts-spine__book { transform: none; } }
.ts-spine__text { position: relative; display: flex; flex-direction: column; align-items: center; max-height: calc(100% - var(--space-16)); overflow: hidden; writing-mode: vertical-rl; transform: rotate(180deg); }
/* An ellipsis would stand on its side here, so a title too long for two lines stops at the end of a word; the link's name has all of it. */
.ts-spine__title { max-width: 2.3em; overflow: hidden; font: 400 var(--spine-title, 0.8125rem)/1.15 var(--font-serif); text-align: center; text-wrap: balance; overflow-wrap: anywhere; }
.ts-spine__author { font: 400 0.8125rem/1.15 var(--font-sans); white-space: nowrap; }
.ts-spine__ribbon { position: absolute; top: -0.25rem; left: 0.375rem; width: 0.4375rem; height: 1.625rem; background: var(--accent-fill); box-shadow: 0 0 0 1px var(--ground); }
.ts-spine__book.is-finished { padding-bottom: var(--space-20); }
.ts-spine__done { position: absolute; bottom: var(--space-4); left: 50%; width: 1rem; height: 1rem; margin-left: -0.5rem; }
.ts-spine__done .ts-icon { display: block; width: 1rem; height: 1rem; }
.ts-spine--w1 { --w: 0; } .ts-spine--w2 { --w: 1; } .ts-spine--w3 { --w: 2; } .ts-spine--w4 { --w: 3; }
.ts-spine--w5 { --w: 4; --spine-title: 0.9375rem; } .ts-spine--w6 { --w: 5; --spine-title: 0.9375rem; }
.ts-spine--w7 { --w: 6; --spine-title: 0.9375rem; } .ts-spine--w8 { --w: 7; --spine-title: 0.9375rem; }
.ts-spine--w9 { --w: 8; --spine-title: 1.125rem; } .ts-spine--w10 { --w: 9; --spine-title: 1.125rem; }
.ts-spine--w11 { --w: 10; --spine-title: 1.125rem; } .ts-spine--w12 { --w: 11; --spine-title: 1.125rem; }
.ts-spine--h1 { --h: 0; } .ts-spine--h2 { --h: 1; } .ts-spine--h3 { --h: 2; } .ts-spine--h4 { --h: 3; } .ts-spine--h5 { --h: 4; }
/* Bands: a long book (step 7 up, 13.4 hours) wears a pair of rules at head and foot. */
:is(.ts-spine--w7, .ts-spine--w8, .ts-spine--w9, .ts-spine--w10, .ts-spine--w11, .ts-spine--w12) .ts-spine__book::after { content: ""; position: absolute; inset: var(--space-8) 0; pointer-events: none; border-block: 3px double color-mix(in oklab, currentColor 40%, transparent); }
html[data-theme="contrast"] .ts-shelf { --plank: var(--line-strong); --plank-light: var(--line-strong); --plank-dark: var(--line-strong); --plank-cast: transparent; --book-cast: transparent; }
html[data-theme="contrast"] .ts-spine__book::before { display: none; }
@media (width >= 768px) { .ts-shelf { --spine-w-inc: 0.35rem; --spine-h-min: 10rem; } }
@media (width >= 1280px) { .ts-shelf { --spine-w-inc: 0.4rem; --spine-h-min: 10.5rem; --spine-h-inc: 0.5625rem; } }
```

## 7. Acceptance tests (test names prefixed `shelfview`, or `TestShelfView...`; synthetic public-domain titles)
- Update: `browse_test.go` lines 101, 105, 663-672; `home_shelf_fit_browser_test.go` selectors if needed.
- `internal/web/shelf_view_test.go`. Table `spineWidth`: 0 and -1 give w4; 30 m, 1 h, 1.5 h give w1; 2 h w2; 3, 4 h w3; 5, 7 h w4; 8, 10 h w5; 12 h w6; 15 h w7; 20 h w8; 25 h w9; 30 h w10; 35 h w11; 40, 100 h w12. Sweep every 10 minutes to 100 h: never decreases, 12 distinct. `spineLengthMs`: 9,300 words w1, 93,000 w5, 558,000 w12; audio with duration ignores `TextWords`. `spineHeight`: stable, 5 values over 200 ids, `""` is h3; two books of one series match. `spineAuthor`: "Jane Austen", "Homer", "Austen, Jane", "Martin Luther King Jr.", none.
- Handler tests: `?view=shelf` has `<ul id="library-grid"`, `ts-shelf--wrap` and `aria-current` on Shelf; grid and list links, filter and sort links, the title form's hidden field and the sentinel's `MoreHX` all carry `view=shelf`; `/libraries` has no `view=` and stays grid; `?view=nonsense` is grid. Fragment returns only `<li>`s. Filter, sort and cursor return the same books as grid. Large: `?view=shelf` is 200 with the grid, no `ts-spine`, no Shelf option, no `view=shelf` link. Member sees only granted books (forbidden book's title absent from body); anonymous redirects as `/libraries?view=list` does. Finished and started labels: `Title by A, 12 h 30 m, 42 % through`, `..., finished`; Home's New carries progress.
- `internal/library/browse_test.go`: `TextWords` from the default ready file's newest edition; `page_count * 300` when `word_count` is 0; 0 for audio; `EXPLAIN QUERY PLAN` of the subquery uses an `ebook_files` index, no scan.
- `internal/web/static/spines_css_test.go`: `pages-spines.css` has no hex, `rgb`, `hsl` or `!important`, and `w1`..`w12`, `h1`..`h5` all exist.
- `internal/web/shelf_view_browser_test.go` (`TENDSHELF_TEST_BROWSER`), library seeded with 1 h, 10 h and 40 h books and more than one page, at 390, 768, 1024, 1440: slot widths match section 3 within 0.5 px; every `a.ts-spine` is at least 44 x 44; every slot's bottom equals `zone + k * tile` within 1 px; no horizontal overflow, also at 195 and 160 px wide (zoom 200 %); title and author at least 13 px; `user-select` none; the fragment loaded by scrolling joins the same `ul`, leaves no nested list and keeps the row rule; a partial last row has its plank; Tab focuses a spine with a non-`none` box-shadow; reduced motion gives `transform: none` on hover and focus (`cdp` has the emulation; see `visual_motion_test.go`). Home: the same `ts-shelf`, same widths for the same book, `data-ts-fit` still hides what does not fit.
- `visual_browse_test.go`: add `{"library-shelf", seededBase, "/libraries?view=shelf"}` to `pgs` and to the full `visualMatrix` condition. Add one large-text capture. `visual_webkit_test.go`: the same address. `a11y_pages_test.go`: a row `libraries-shelf`, `/libraries?view=shelf`. Seed text must be fixed, per the visual-id helpers.
- Record side-by-side at 390, 768, 1024, 1440 against `screens/core/HomePhone`, `HomeDesktop`: same plank and spine language, thicker variety is intended.
- Manual, listed in the report whether run or not: Android emulator (hold a spine: no selection or callout) and the Wails window.
- Gates: `make generate` leaves the tree clean; `gofmt`, `go vet`; `go test -race ./internal/web/... ./internal/library/...`; `make build`; the browser tests above. The lead runs `make check` on the merged tree.

## 8. Out of scope
Facing covers; a stored view preference or changing the default view; a series sort; `site-shots` changes; thickness on the book page, series page or Android native screens; shelves (`/shelves`) drawn as planks; the onboarding celebration (not built); a new height rule tied to length; forced-colors styling beyond what the tokens give.

## 9. Shared files
`docs/copy-additions.md` is also edited in worktree `agent-a08dd0c46eedff0e9`, and `docs/roadmap.md` by every package: append only. `motion_test.go` is edited by the bulk-downloads worktree, so this plan does not touch it. No overlap with its `internal/library/download_list.go`.
