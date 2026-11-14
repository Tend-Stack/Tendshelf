# Plan: phone full-screen player (handoff 2026-11-14, as amended by the operator the same day)
Phone scope: `@media (width < 768px) and (height > 30rem)`, written below as PHONE. Landscape phones (height <= 30rem) keep player-fit.css's grid.
Operator amendments that override the handoff: (A1) the header (close, centred `full-chapter` line, bookmark, more) and its tests stay
exactly as they are. (A2) The cover block keeps its size, page edge and its scaling in player-fit.css; the only addition there is the scrub scrim.
(A3) Both bars keep today's behaviour: the wave scrubs as now (player-full.js), the book ruler stays `chapter-bar.js`'s picker (tap, drag, keys, tip,
role=slider, aria). They only move and are restyled. Chapters opens from the settings bar, as now. The redesign starts below the cover.

## 1. Invariants
1. No change to behaviour: player-full.js drag/keys/change/Undo, chapter-bar.js (file unchanged), peaks loading, sheets, sleep, swipe-to-close.
2. 768+ and landscape phones render exactly as today. Every new rule lives in `css/player-phone.css` inside PHONE. bundle.css, player.css and player-fit.css are not edited.
3. Header markup, its `full-chapter` text (`chapterWords().header`) and `chapter_names_browser_test.go` are untouched.
4. CSP: no `style=""` and no inline script. Per-element values go only through `el.style.setProperty("--x", ...)` (CSSOM; already used by `setVars` and the ruler's `--at` ticks).
5. Tokens only. Marks reach 3:1 and text 4.5:1 against the player background in dark, light, sepia and contrast. Text is at least 13 px, targets at least 44 px, large text works.
6. One element per piece of data, written by one code path. The two phone-only spans (`full-chapter-left`, `full-bookbar-left`) are written in the same `drawTimes` line as their 768+ twins.
7. Nothing changes in `android/`. The Android shell uses this page as it is.

## 2. Decisions (one-line reasons)
- D-a Clock: PHONE hides `.ts-player__times` (the clock and `full-left`). The scrim shows the target time while dragging. 768+ keeps the clock and the left line. Reason: as the handoff asks, and tablets are out of scope.
- D-b Book bar (A3): it stays the existing `.ts-ruler.ts-player__ruler[data-ts-chbar]` element. Its drag, tap, keys, `.ts-chtip` tip and `.ts-chmark` stay. PHONE hides the ticks, the line and the fill and draws segments.
  The segments are `span.ts-player__seg` elements inside a new `span.ts-player__segs[data-ts-ruler-segs]`. Each is positioned with `--from`/`--to` percentages (the same scheme as ticks and `.ts-chmark`).
  The widths follow chapter durations, not equal widths. Reason: chapter-bar.js maps x to time, so equal widths would make a tap land on a different chapter than the segment under the finger. This is a declared deviation from the handoff.
  Many chapters: a segment narrower than 4 px (2 px bar + 2 px gap) gets `.is-thin`: no gap, no radius. When the current segment is thin, the ruler gets `.is-thin-now` and shows `.ts-player__segmark` (ink, 2x12 px, at `--ts-pos`). The result is a continuous bar with the position marked.
  One segment per track chapter (`book.chapters.length`, 53 for the operator's book), while the header reads `chapterWords` ("Chapter 13 of 51"). That is correct: the header counts numbered chapters (e784d61).
- D-c Wave: the same `.ts-scrub` and the same transparent `<input type="range" aria-label="Position in chapter">` (it exists; reused).
  PHONE geometry: area 48 px; bars are 3 px stroked lines, round caps, a 5 px pitch, 8-46 px tall, vertically centred. Flat bars (`FLAT_HEIGHT`) show while peaks load or when there are none.
  Layers: `rest`, `played` and a new `jump`. Needle: 4x56 px, ink, 3 px ground outline. Dragging: 6x68 px plus a 12 px halo of ink at 16 %.
  Scrub state = `.ts-scrub.is-dragging`, which exists. It shows the scrim over the cover, dims the book row, transport and settings bar to .45, and puts "was M:SS" in the right slot under the wave.
  On release the existing `endDrag` seeks or cancels. Keys (arrows, PageUp/Down, Home/End) seek at once as today, with no scrim. Reason: a key press has no pending jump; aria-valuetext and the time row already tell the result.
  Reduced motion: the needle size change and the dimming are instant. The scrim fades only with no-preference.
  The fine-scrub rate stays. The `full-fine` hint is hidden on phones (no place for it on the artboard).
- D-d Unplayed grey, measured against `--ground`. `--wave-rest`: dark 2.84, light 2.02, sepia 2.03, contrast 4.89, so it fails 3:1 in three themes. `--line-strong`: dark 5.06, light 3.54, sepia 3.63 (contrast 21, but it equals `--ink`).
  The builder sets `--ts-rest: var(--line-strong)`, and `--wave-rest` under `[data-theme="contrast"]`. These are existing tokens, nothing new. Reported to the operator.
- D-d2 Played colour: a cover accent is only guaranteed 3:1 when the book has one (`media.Accents`). Without one, `--cover-accent` = `--accent-fill`, which measures 2.75 (light) and 2.69 (sepia).
  So marks use `--ts-mark`. It is set by `applyAccent` from the cover accent and falls back to `--wave-played` (5.38/5.32). Text in the accent ("6:33 left in chapter") uses `--accent` as `.ts-player__left` does today (min 5.32).
- D-e Settings bar: the same `.ts-player__bar`, restyled. `--raised` fill, no border, 4 rem tall, `--radius-lg`.
  Layout: `grid-auto-flow: column; grid-auto-columns: minmax(0,1fr)`, so hidden items take no column. With no ebook the bar has 3 equal columns; an episode has 2. Large text: 2 columns, flowing in rows.
  Sleep running: the moon stays and the label is replaced by the time left. The existing `full-sleep-left` value is moved under the icon by CSS order and the label is hidden. CSS only.
  The 4th label: `msgOpenReaderShort = "Read along"` (handoff). It is one constant in copy.go and the accessible name stays "Read this book". Safe-area padding moves from the bar to the body.
- D-f Spacing: on PHONE, `.ts-player__scrub` becomes `display: contents`, so its children are body flex items. Order: title/author (0), wave `.ts-scrub[data-ts-scrub=full]` (1), `.ts-scrub__times` (2), `.ts-player__booklabel` (3), ruler (4), transport (5), bar (6).
  `margin-top: auto` goes on the wave, the booklabel, the transport and the bar. The fixed minimum before each: 8 px (12 px before the bar).
  The free space splits equally. The cover keeps `flex: 0 1 16rem; min-height: 7.5rem` and shrinks first, so there is no dead band at any height.
- D-g Phone only. No `android/` change. The desktop bar's wave code path (`data-ts-wave="bar"`) is unchanged.
- D-h Packages run in sequence, none in parallel (they share player-full.js and player-phone.css): P1 layout and bars; P2 wave and scrub state; P3 decision, docs and captures.
- Book row label: right-aligned "13 h 29 m left in book" only, with an empty left side. Reason: the header already names the chapter (A1), a percent is not in the design, and the ruler's aria-valuetext speaks the chapter.

## 3. Markup and data shapes (`internal/web/components/player_full.templ`)
- `Scrubber`: `data-ts-wave` becomes `data-ts-wave={ which }` ("full"). waveform.js only matches `"bar"` today.
- Inside `.ts-player__cover`, after the img and the tile:
  `<span class="ts-player__jump" aria-hidden="true" data-ts-full-jump><span class="ts-overline">{ m.JumpTo }</span><span class="ts-num ts-player__jumptime" data-ts-full-jump-time></span><span class="time ts-player__jumpleft" data-ts-full-jump-left></span></span>`
- In `.ts-scrub__times`, after `data-ts-full-fine`, add `<span class="ts-player__chleft" data-ts-full-chapter-left></span>`. Phone shows elapsed + chapter-left. 768+ shows elapsed, fine and book-left as now.
- In `.ts-player__scrub`, after `.ts-scrub__times`: `<span class="meta ts-player__booklabel" data-ts-full-bookbar-left></span>`.
- In the ruler, after the ticks span: `<span class="ts-player__segs" data-ts-ruler-segs><span class="ts-player__segmark"></span></span>`. The segment spans are prepended by JS.
- Copy (`internal/web/copy.go`, `PlayerMsgs` in `components/player.templ`):
  - `msgJumpTo = "Jump to"` (shown uppercase by `.ts-overline`).
  - `msgScrubWas = "was {t}"`, added as `data-ts-msg-scrub-was` on the slot.
  - `msgOpenReaderShort = "Read along"`.
- JS (`player-full.js`):
  - `ui` list += `full-jump`, `full-jump-time`, `full-jump-left`, `full-chapter-left`, `full-bookbar-left`.
  - `ruler.segs = $("ruler-segs", ruler)`.
  - `buildSegments()`: called from drawBook after the ticks. Creates one `span.ts-player__seg` per chapter with `--from`/`--to` (toFixed(3)+"%"), plus `.is-last` on the last one. Then `fitSegments()`.
  - `fitSegments()`: W = `ruler.segs.clientWidth`; returns when 0. Toggles `.is-thin` where `(to-from)*W < 4`. Runs from a `ResizeObserver` on `ruler.segs` and on open.
  - `drawSegments(ms)`: called from `drawRuler`. idx = chapterAt(ms). Only when idx changes: `.is-done` for i < idx and `.is-now` for i == idx. Toggles `ruler.is-thin-now`.
  - `drawTimes`: also sets `full-chapter-left` = the same string as `full-left`, `full-bookbar-left` = the same as `full-book-left`, `full-jump-time` = the same as `full-clock`, and `full-jump-left` = the same as `full-left`.
  - `applyAccent`: its loop also sets `["--ts-mark", accent]`.
  - P2: on pointerdown, `setVars(s.root, {"--ts-from": clamp((d.from - chapter.start)/len, 0, 1).toFixed(4)})`. In `dragTo`, after `drawTimes`, set `full-chapter-left` = `fill(msg("scrubWas"), {t: clock(d.from - chapter.start)})` and add `.is-was`. In `endDrag`, remove `--ts-from` and `.is-was` before seeking.
- `waveform.js` (P2): `const PHONE = matchMedia("(width < 768px) and (height > 30rem)")`. `isFull = wave.dataset.tsWave === "full" && PHONE.matches`.
  - `paint()`, full mode: viewBox in CSS px, `x = j*5 + 1.5`, `half = max(0, (8 + h*38 - 3)/2)`, path `M x (H/2-half) V (H/2+half)`. Layers `["rest","played","jump"]`.
  - `render()`: n = `floor((width+2)/5)` in full mode. Flat bars when not ready, as the bar does. `"full"` is added to the drawn key.
  - `PHONE.addEventListener("change", () => render(true))`.

## 4. Steps
P1 (builder, sonnet). Files: player_full.templ (+_templ.go via `make generate`), components/player.templ, copy.go, components/layout.templ, static/static.go, new static/css/player-phone.css, static/js/player-full.js, the tests below, static/motion_test.go, perf_weight_test.go.
1. `components/layout.templ:129`: add `"css/player-phone.css"` after `player-fit.css`. `static/static.go:63`: add `base == "player-phone.css"` to the "pages" case.
2. Markup and copy as in section 3 (all P1 markup, including the scrim and `data-ts-wave={ which }`).
3. `player-phone.css`, PHONE sections: body (bottom padding `calc(var(--space-12) + env(safe-area-inset-bottom))`; top padding and header unchanged), hide list, order and spacing (D-f), wave row text, book label and segments, transport, settings bar, scrim (P2 fills it), contrast/forced-colours.
   - Hide list: `.ts-player__times`, `[data-ts-full-book-left]`, `[data-ts-full-fine]`, `.ts-scrub__bubble`, `.ts-player__ruler-ticks`, `.ts-player__ruler .ts-ruler__line`, `.ts-player__ruler .ts-ruler__fill`.
   - 768+ hides `[data-ts-full-chapter-left]`, `.ts-player__booklabel`, `.ts-player__segs` and `.ts-player__jump`, with rules in player-phone.css outside PHONE.
   - Times row: font `.time` (0.9375rem/1.25rem 500 tabular). Elapsed `--ink`; `.ts-player__chleft` `--accent`, right-aligned; `.is-was` `--ink-muted`.
   - Book label: `--ink-muted`, `text-align: end`.
   - Segments: 0.875rem box; seg 4 px `--ts-rest`; `.is-done` `--ts-mark`; `.is-now` 8 px `--ink`; 2 px radius; width `calc(var(--to) - var(--from) - 2px)` (`.is-last` and `.is-thin` without the -2px).
   - Defaults: `.ts-player { --ts-mark: var(--wave-played); --ts-rest: var(--line-strong) }` and `[data-theme="contrast"] .ts-player { --ts-rest: var(--wave-rest) }`.
   - Transport targets: prev/next 3rem with a 1.625rem icon, back/forward 3.5rem with 2.125rem, toggle 5rem with a 2rem icon.
   - Settings bar: D-e.
   - The ruler keeps its 44 px transparent border. Set its margins so the visible gap between label and segments is 8 px.
4. `player-full.js`: section 3 P1 items.
5. Fix the tests P1 breaks (selector or viewport updates only, listed in section 5). Budgets: section 6.
P2 (builder, sonnet). Files: waveform.js, player-full.js (drag lines only), player-phone.css (wave and scrim section), new tests.
6. `waveform.js` full mode (section 3).
7. CSS for the wave and the scrub state, PHONE:
   - `.ts-scrub` 3.5rem; `.ts-wave` top .25rem, height 3rem.
   - Paths: `fill:none; stroke-width:3; stroke-linecap:round`. rest `--ts-rest`; played `--ts-mark` with `clip-path: inset(-.25rem calc((1 - min(var(--ts-from),var(--ts-pos)))*100%) -.25rem 0)`; jump `--ts-mark`, opacity 0, .5 while dragging, clip from min to max. `.ts-scrub{--ts-from:var(--ts-pos)}`.
   - Hide `.ts-scrub__line` and `.ts-scrub__fill`, except under forced-colors, where the wave is hidden.
   - Needle sizes per D-c, overriding the `transform: scaleY(1.25)` with `transform: none`.
   - Scrim: `position:absolute; inset:0; border-radius:inherit; background: color-mix(in srgb, var(--ground) 82%, transparent)`, centred column, `container-type:size`, time `font: 500 min(3.5rem, 30cqi)/1.07 var(--font-sans)`, opacity 0 to 1 over `--duration-fast`, `pointer-events:none`. `.ts-player__cover` gets `position: relative` (no visual change).
   - Dimming: `.ts-player__body:has([data-ts-scrub=full].is-dragging) :is(.ts-player__ruler,.ts-player__booklabel,.ts-player__transport,.ts-player__bar){opacity:.45}`.
8. `player-full.js` drag lines (section 3 P2).
P3 (builder; scribe for the docs). Files: docs/decisions/0042-player-native-row.md, docs/roadmap.md, the changelog entry from 87a234a, a11y fixture if needed, capture test.
9. Rewrite 0042 in place (never shipped).
   - Title stays. Context: plus the handoff and A1-A3.
   - Decision 1, phones: wave and segmented ruler, raised settings bar, scrim, colours and contrast numbers, "Read along".
   - Decision 2, 768-1279 and landscape phones: the old points 1-2 verbatim (ink ruler, borderless row).
   - Decision 3, 1280+: unchanged. Tests list updated.
10. Roadmap status and changelog. Captures (test 10) are written to `.local/tests/player-phone/<run-id>/`; the folder path goes in the report.

## 5. Tests (new file `internal/web/player_phone_browser_test.go`, `playerApp`/`fullpOpen` helpers)
1. `TestPlayerPhoneBarsBehaveAsBefore` (390x844):
   - The ruler has role=slider and an aria-valuetext built by `chapterWords`.
   - A tap at the centre of segment 3 starts chapter 3 (position == chapter start ±1 s, Undo offered). ArrowRight on the focused ruler moves a chapter.
   - A drag on the wave moves the position inside the chapter.
   - Segment k's rect matches chapter k's duration share within 1 px.
   - The existing `TestFullPlayerBrowserChapterScrub`, `TestFullPlayerBrowserScrubber` and `TestChapterBar*` pass at 390 with selector updates only.
2. `TestPlayerPhoneClock`:
   - At rest: `[data-ts-full-clock]` not rendered (`checkVisibility()` false) and scrim opacity 0.
   - Mid-drag: scrim opacity 1, `full-jump-time` == `full-elapsed`, `full-chapter-left` == "was " + the start time, transport opacity .45±.02, needle width 6±.5, `.ts-wave__jump` opacity .5.
   - After release: all opacity 1 and the position at the target.
   - Arrow keys: no scrim.
3. `TestPlayerPhoneWave`: path segments == floor((w+2)/5), stroke-width 3, bar heights 8-46. Flat bars while peaks 404 with Retry-After.
4. `TestPlayerPhoneBookRow`:
   - The label shows only "… left in book" (no "Chapter"); `full-chapter` is unchanged.
   - `.is-now` is index chapterAt and taller (8 vs 4 px); `.is-done` count == idx.
   - A 300-chapter fixture at 360 px gives `.is-thin` segments, `.is-thin-now` and a visible segmark.
5. `TestPlayerPhoneSettingsBar` (360x640, 390x844):
   - 4 items (3 without an ebook) of equal width ±1 px; background `--raised`; border 0; height 64±1.
   - Each label has scrollWidth ≤ clientWidth and one line; the 4th label reads "Read along".
   - Sleep on: the label matches `^\d+:\d\d$` and the icon is visible.
   - Large text: 2 columns.
6. `TestPlayerPhoneSpacing` (360x640, 390x844, 430x932, dark):
   - Gaps author to wave, times to booklabel and segs to transport are equal ±1 px and ≥ 7.5. The gap from transport to the bar is the first gap + 4 ±1.
   - No overlap. The body does not scroll. The bar bottom is ≤ 12 px + inset above the viewport bottom.
7. `TestPlayerPhoneContrast` (4 themes, reusing `TestPlayerRowContrast`'s helper):
   - Marks ≥ 3: rest stroke, played `--ts-mark`, upcoming/done/now segments.
   - Text ≥ 4.5: ink-muted labels on `--raised`, `--accent` chapter-left, ink elapsed.
8. `TestPlayerPhoneThemesLargeText` (4 themes x normal/large, 390x844): no text node clipped and no block overlap.
9. `TestPlayerPhoneTabletUnchanged` (768x1024): clock visible, ticks visible, segs/booklabel/scrim not rendered, bar borderless as in 0042.
10. `TestPlayerPhoneCaptures` (opt-in `TENDSHELF_TEST_BROWSER`, seeded ids):
    - `phone-{rest,scrub}-{dark,light}-390x844.png`, plus `phone-rest-dark-360x640.png` and `-430x932.png`.
    - The scrub shots hold the pointer mid-drag.
Updated tests:
- `player_native_row_browser_test.go`: `TestPlayerNativeRow` 390 asserts the D-e bar; 768 is unchanged.
- `TestPlayerRulerColour` at 390 asserts the segment colours (done `--ts-mark`, now `--ink`, rest `--ts-rest`) and that they differ from the wave's played colour; the old assertion moves to 768.
- `TestPlayerRowContrast` ruler parts become segments at 390.
- `player_full_test.go TestFullPlayerMarkup` gains the new attributes.
- `visual_large_text_player_test.go` and `a11y_audit_test.go:548`: re-run; adjust only if a selector moved.
Gates: `make generate` (clean tree), `make check`, `make build`. Run the touched browser tests with `TENDSHELF_TEST_BROWSER`.

## 6. Budgets expected to move
- `static/motion_test.go`: new row `css/player-phone.css` (measured, rounded up to 256 B), plus the `js/player-full.js` and `js/waveform.js` rows.
- `perf_weight_test.go`: page CSS and JS rows on every shell page. Raise each to the measured size rounded up to 256 B, comment "phone player (handoff 2026-11-14)", report. Never minify.
- bundle.css, the core row and player.css should not move. If they do, it is a plan violation: report it.

## 7. Out of scope
- Header and cover (A1, A2) and the handoff's 280 px cover.
- Tablet, desktop, mini-player, desktop bar, Chapters sheet, read-along.
- Landscape phones.
- chapter-bar.js changes.
- Making the needle glide between ticks: the "no second loop" rule in waveform.js; the needle moves per tick as today.
- New tokens.
- The handoff's `GET /books/{id}/peaks`: the real route, `/peaks/{audioFileID}`, is unchanged.
- Translations: copy.go is English-only.
- `android/`.

## 8. Open questions (recommended answer in brackets; work is not blocked)
1. The unplayed grey uses `--line-strong` (`--wave-rest` in contrast) because `--wave-rest` is under 3:1 in three themes. Retune the `--wave-rest` tokens later? [Keep `--line-strong`; retune the token only if the boards must match the mock grey.]
2. The 4th label is "Read along" per the handoff, but read-along sync was dropped on 2026-11-04. [Switch `msgOpenReaderShort` back to "Read"; one line.]
3. Segment widths follow chapter length, not equal widths, so taps stay true (A3). [Accept.]
4. "Left in chapter" text uses `--accent`, not `--cover-accent`, because cover accents only guarantee 3:1. [Accept.]
