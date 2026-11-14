# Plan: right-to-left text in the reader (next-stage row 25, R4)

Scope: Arabic, Hebrew, Persian, Urdu and other right-to-left scripts, in both flows, online and offline, browser, Wails
(WebKitGTK) and the Android WebView. Interface language stays English. No new module, no migration, no per-book switch.

## 1. Invariants

1. A left-to-right book renders exactly as today: no `dir` attribute anywhere in its reader page or chapter fragments, the
   same edge markup, the same JS arithmetic (the sign is `1`), and `make visual` before/after reports all captures identical.
2. Direction is a property of the document (`Meta.Dir`, `Block.Dir`), made by the parser, and absent for left-to-right.
3. Sentence and word spans of any block with no right-to-left letter and none of `؟ ۔ ׃` are unchanged (ids stable).
4. One sign `S` (`1` LTR, `-1` RTL) carries the mirror in the paginator; every physical x read or write goes through it.
5. Only book content and page progression mirror; the bar, sheets, settings, dialogs and the Aa panel stay left to right.
6. No font is downloaded or added; right-to-left glyphs come from fonts already on the device.
7. Copy-protection, read-only library and service-worker rules are untouched (no new routes, no new cached addresses).

## 2. Decisions (settled)

- **D1 Source, in this order.** (1) The text itself when decisive: at least 200 strong letters and one direction holding
  2:1 (tags lie: converters write a default language; letters cannot). (2) EPUB `<spine page-progression-direction>`
  `rtl`/`ltr`. (3) `dir` on the first `<html>`/`<body>` that has one (EPUB spine parts, the HTML importer). (4) The
  language tag (`LangDir`). (5) Whichever letters are more. Values other than `rtl`/`ltr` (`default`, `auto`) are ignored.
  Detection is then trustworthy, so **no manual override** in this package.
- **D2 Stored, not render-time.** `Meta.Dir` (`"rtl"` or omitted) and `Block.Dir` (`"rtl"`/`"ltr"`, only when it differs
  from the book), both `omitempty`. Reason: the script count needs the whole text, which the reader never scans.
- **D3 Recipe bumps to `tendshelf-ebook-v4`** (and `Segmenter` to `tendshelf-segment-v2`). Reason: the same file and
  recipe must give the same bytes, and segmentation changes. Cost (existing machinery, as v2 and v3): at start-up
  `EnqueueStale` re-reads every ebook once in the background; the old edition serves until the new one lands; marks move
  by `Reanchor` (exact text at the old place, so Latin books keep every id); old document files are deleted. No migration
  (next free number would be `0045`; nothing outside the document needs the direction).
- **D4 Existing books, before the re-read lands:** `ebook.Direction(doc)` falls back to `LangDir(Meta.Language)` for
  documents of recipe v1-v3, so a tagged Arabic or Hebrew book turns right to left on the first start of the new binary.
- **D5 Per block: majority, not `dir="auto"`.** `dir="auto"` uses the first strong letter, so an Arabic paragraph that
  opens with "iPhone" would flip left to right. The parser counts each block: own direction is the one holding 2:1 with at
  least one letter; stored only when it differs from the book. Inline mixing inside a block is left to the Unicode bidi
  algorithm, which handles an English quote inside Arabic correctly under a right-to-left base.
- **D6 Book text in left-to-right chrome** (bar title, contents titles, mark quotes, search hits, footnote and look-up
  text, skim tip, search field) gets `unicode-bidi: plaintext` in CSS: no markup change, so LTR bytes stay identical.
- **D7 Mirrors:** column order, scrollLeft sign, tap zones, swipe, ArrowLeft/ArrowRight, edge buttons (left = next), slide
  offset, flip (hinge on the right), skim bar and ruler, the page-stack edges, the bottom progress line, spread ribbons,
  blockquote bar, list indent. **Does not mirror:** top bar, dock buttons, sheets, settings, dialogs, hint chevrons (they
  point the way the page goes), PageUp/PageDown/Space, `[`/`]`, Android volume keys (`Feel.kt` stays: logical next/prev).
- **D8 Flip** computes the whole flight in a mirrored frame: stage `scale: -1 1`, each face `scale: -1 1` back, every x
  (geometry, rects, `clientX`) mapped by `mx(x) = axis - x` with `axis = shell.left + shell.right`. One code path.
- **D9 Edge buttons** in an RTL book are emitted in logical order with swapped meaning; CSS untouched.
- **D10 Fonts:** inside `.ts-reader[dir="rtl"]` override the tokens (system fonts by name only, no `@font-face`):
  `--font-serif: "Fraunces", "Noto Naskh Arabic", "Noto Serif Hebrew", "Geeza Pro", "Times New Roman", serif;`
  `--font-sans: "Fira Sans", "Noto Sans Arabic", "Noto Sans Hebrew", "Geeza Pro", Arial, system-ui, sans-serif;`
  and `.ts-reader[dir="rtl"]:lang(ur)` puts `"Noto Nastaliq Urdu"` first in both.
- **D11 Verified by experiment (2026-11-14):** `direction: rtl` multi-column box, Chromium 156 and WebKitGTK 4.1
  (MiniBrowser under `scripts/isolated-display.sh`): columns run leftward, `scrollLeft` is 0 at the start and negative as
  you advance (`-320` sets `-320`, `+320` clamps to 0, minimum `-(scrollWidth - clientWidth)`). WebKit reports the same
  extra trailing column in `scrollWidth` in both directions (an existing quirk, symmetric).

## 3. Packages (sequential: P2 needs P1's fields, P3 needs P2's markup; files are disjoint per package)

### P1 Go: detection, model, segmentation (`internal/ebook`)
1. `internal/ebook/direction.go` (new): `isRTLLetter(r rune) bool` = `unicode.IsLetter(r) && unicode.In(r, unicode.Arabic,
   unicode.Hebrew, unicode.Syriac, unicode.Thaana, unicode.Nko, unicode.Samaritan, unicode.Mandaic, unicode.Adlam)`;
   `letterDirs(rs []rune) (nR, nL int)` (nL: other `unicode.IsLetter`); `LangDir(tag string) string` (trim, lower, split on
   `-`/`_`; a script subtag `arab hebr syrc thaa nkoo adlm rohg samr mand` gives `"rtl"`, any other 4-letter subtag gives
   `""`; else primary in `ar arc azb bal bqi ckb dv fa glk he iw ji khw ks lrc mzn nqo prs ps sd syr ug ur yi` gives
   `"rtl"`); `decideDir(hint, lang string, nR, nL int) string` exactly D1 with consts `dirMinLetters = 200`,
   `dirRatio = 2`; `blockDir(nR, nL int) string` (D5); `Direction(d *Doc) string` (D4).
2. `doc.go`: `Recipe = "tendshelf-ebook-v4"`, `Segmenter = "tendshelf-segment-v2"`; `Meta.Dir string `json:"dir,omitempty"``
   after `CoverPart` (comment: recipe v4); `Block.Dir string `json:"dir,omitempty"`` after `ImgH`; unexported `nR, nL int`
   on `Block` beside `anchors`.
3. `builder.go`: field `dirHint string`, `nR, nL int`; `func (b *builder) HintDir(v string)` keeps the first `rtl`/`ltr`.
   In `block()` after the text: `blk.nR, blk.nL = letterDirs(rs)`; add to `b.nR, b.nL`. In `Finish`: `meta.Dir =
   decideDir(b.dirHint, meta.Language, b.nR, b.nL)`; book := `"ltr"` when empty; for every block, `own := blockDir(..)`;
   `if own != "" && own != book { Block.Dir = own }`.
4. `epub.go`: OPF `case "spine"` also reads `attr(se, "page-progression-direction")` into `o.PPD`; before the spine walk
   `b.HintDir(o.PPD)`; after each part's `walkHTML`, `b.HintDir(hd.Dir)` (HintDir keeps the first, so PPD wins).
5. `htmlblocks.go`: `htmlDoc.Dir string`; at the start-tag handling, before the name switch, for `html` or `body` with
   `doc.Dir == ""`, set it from a lower-cased trimmed `dir` that is `rtl`/`ltr`. Flow otherwise untouched.
   `html.go`: `b.HintDir(hd.Dir)` after `walkHTML`. MOBI, FB2, DOCX, PDF, text: nothing (language and count suffice).
6. `segment.go`: `terminators = ".!?…؟۔׃"`; `startsSentence` adds `|| isRTLLetter(r)`; `abbreviationBefore` also returns
   true for a one-letter RTL token, or one RTL letter plus `U+0640` (د. م. هـ.). Comment the v2 change.
7. `ebooktest/epub.go`: `EPUBOptions.Dir` (writes ` page-progression-direction="<v>"` on `<spine>` when set) and
   `HTMLDir` (writes ` dir="<v>"` on each chapter `<html>`); empty fields leave the bytes as today. Add
   `ebooktest/rtl.go`: `ArabicParas(n int) []string`, `HebrewParas(n int) []string`: deterministic synthetic sentences
   from fixed word lists (ar: كتاب الولد ذهب إلى المدرسة في الصباح ثم عاد البيت؛ he: הילד הלך אל בית הספר בבוקר ואז
   חזר הביתה), sentence i ends with `.`, every 3rd with `؟` (ar) or `?` (he); no real text.
8. Tests (`internal/ebook/rtl_test.go`): `TestRTLLangDir` (table incl. `ar-EG`, `iw`, `az-Arab`, `az-Latn`, `pa-Arab`,
   `ku`, `AR`, `und`, ``), `TestRTLDecideDir` (each D1 branch and the 199/200 boundary), `TestRTLDetectEPUB` (ar text +
   `ar`; he text + no lang; ar text + `en` tag → rtl; English + PPD `rtl` → ltr; 150 letters + PPD rtl → rtl; 150
   letters + `HTMLDir` rtl → rtl; English → JSON has no `"dir"`), `TestRTLBlockDir` (English paragraph in Arabic book →
   `ltr`; Arabic paragraph opening with "iPhone" → none; Hebrew paragraph in English book → `rtl`), `TestRTLDetectText`
   (Arabic `.txt` → rtl), `TestRTLDirectionLegacy` (v3 + `he` → rtl; v4 + `ar` + no Dir → ""), `TestRTLSegment`
   (Arabic 3 sentences with `؟`; Urdu `۔`; `د. أحمد وصل. ثم ذهب.` → 2; Hebrew 2), `TestRTLRecipeV4Reread` (modelled on
   `printpages_test.go:173`: a v3 Arabic edition with a highlight and a position is re-read; new Meta.Dir rtl, marks moved).
   Update pinned `"tendshelf-ebook-v3"` in `printpages_test.go:44` and `docv2_test.go:101` to v4. Regenerate
   `testdata/golden/*.json`: `git diff` must show only the `recipe` line per file (put that in the report).
9. `docs/reader.md`: a "Recipe v4" paragraph (meta `dir`, block `dir`, D1 order, the segmenter change).
Gates: `go test -race ./internal/ebook/... ./internal/web/...`, `make check`, `make build`.

### P2 Rendering and CSS (`internal/web`, no JS)
1. `pages/reader.templ`: `ReaderRoot.Dir string`; on `#reader` after `lang`: `if v.Root.Dir != "" { dir={ v.Root.Dir } }`.
   Edges (`:288-289`): `if v.Root.Dir == "rtl"` emit first `<button type="button" class="ts-edge ts-edge--next"
   data-edge="-1" aria-label={ v.PrevPageLabel }>` then `class="ts-edge ts-edge--prev" data-edge="1" aria-label={
   v.NextPageLabel }`; else the two existing lines unchanged. `ReaderBlockView.Dir`; every case of `ReaderBlock` adds
   `if b.Dir != "" { dir={ b.Dir } }`. `.ts-readdock__ruler` div (`:345`) gets the same conditional `dir`. `make generate`.
2. `routes_reader.go:272`: `Dir: ebook.Direction(rc.doc)`. `reader_chapter.go`: fill `ReaderBlockView.Dir` from
   `Block.Dir`. The offline shell reuses this view (check `routes_offline_read.go`; if it builds its own Root, set it there).
3. `static/css/pages-reader.css` (append, no LTR rule edited): D10 font tokens; `.ts-reader__text blockquote:dir(rtl)
   { padding-left: 0; padding-right: var(--space-16); box-shadow: inset -2px 0 0 var(--line-strong); }`;
   `.ts-reader__text .ts-reader__li:dir(rtl) { padding-left: 0; padding-right: 1.4em; }`; ruler `.ts-readdock__ruler[dir="rtl"]
   > .ts-ruler { scale: -1 1; }` and `... .ts-skim__tip { transform: translateX(calc(var(--ts-value, 0) * 1%)); }`; bottom
   line `.ts-readerpage[data-chrome="off"]:has(> .ts-reader--pages[dir="rtl"])::before { inset: auto 0 0 auto; }`; inside
   `@media (width >= 1024px)`: `.ts-reader--spread[dir="rtl"] .ts-ribbon[data-page="0"]` back to `left: auto; right: 0`
   (and its `::before/::after` to `left: auto; right: max(...)` as the base rule), `[data-page="1"]` to `right: auto; left: 0`
   (pseudo-elements likewise); D6 rule on `.ts-readerbar__title, .ts-reader__toctitle, .ts-note__quote,
   .ts-reader__hits a > span, .ts-readernote--note p, .ts-lookup__title, .ts-skim__tip, #reader-q`.
4. Tests (`internal/web/rtl_markup_test.go`, seeded like `pgSeedEPUB`): `TestRTLReaderMarkup` (Arabic book: `#reader` has
   `lang="ar" dir="rtl"`; edge order and attributes as step 1; ruler `dir="rtl"`; the English paragraph has `dir="ltr"`;
   chapter fragment `/hx/books/{id}/read/chapters/1` and the offline shell carry the same), `TestRTLLTRMarkupUnchanged`
   (English book: no ` dir=` in the page or the fragment; edge markup equals the current two literal lines).
   `static/motion_test.go`: CSS row only if measured over (estimate +450 B gzip on 9,472; report OVERRUN, never squeeze).
5. Evidence: `TENDSHELF_VISUAL_OUT=/tmp/before make visual` on the P1 tip, again after, `capturediff` "N of N identical".

### P3 JS paging, flip, skim and browser tests
1. `reader-pages.js`: `const S = root.dir === "rtl" ? -1 : 1;` `const mx = (r) => (S > 0 ? r : { left: -r.right, right:
   -r.left, top: r.top, bottom: r.bottom });` Use at `:164` (`mx(last).right - mx(own).left`), `:207` (`dir * S * 24`),
   `:212` (`S * page * step`), `:357` (`across = S * (event.clientX - from[0])`), `:372` (`mx(section rect).left`),
   `:383`, `:410` (`mx(rect).left`), `:478` geometry adds `right: own.right, rtl: S < 0`, `:501-502` (both sides `mx`),
   `:508` (`mx(rect).left - mx(boxRect).left + S * box.scrollLeft + 2`). `:123`, `:533` stay 0.
2. `reader.js`: same `S`; tap `turn((across > 0.5 !== swapped ? 1 : -1) * S)`; keys `ArrowRight` → `turn(S)`, `ArrowLeft`
   → `turn(-S)` (PageDown/PageUp/Space unchanged); `:282-283` stack: in RTL `--ts-stack-r` gets `stack`, `--ts-stack-l`
   gets `8 - stack`. Edge handler unchanged (meaning is in `data-edge`).
3. `reader-flip.js`: `rtl = root.dir === "rtl"`; `axis` from `shell.getBoundingClientRect()` at `begin()` and pointerdown;
   `mx`; `begin`: `geo = rtl ? { ...g, left: axis - g.right, frame: mirrored frame } : g`; stage class `ts-flip--rtl`;
   `plan`: `origin = geo.left - S * section.scrollLeft`, `columnOf` takes `rtl ? axis - rect.right : rect.left`; `draw`:
   `wrap.dir = job.dir` (from `live.dir` in `prepare`, like `lang`), settle `S * (...) * step`; every `event.clientX` →
   `mx(event.clientX)`. CSS (pages-reader.css, append): `.ts-flip--rtl { scale: -1 1; } .ts-flip--rtl .ts-flip__face
   { scale: -1 1; } .ts-flip--rtl .ts-reader--pages .ts-reader__col { inset: 0 0 0 auto; padding: var(--flip-y, 0)
   var(--flip-x, 0) 0 0; }`. `reader-skim.js`: no change (native RTL range; value meaning unchanged).
4. Budgets (estimates; measure, raise rows with OVERRUN notes): reader-pages.js +600 B raw/+200 B gzip, raw ceiling is
   24 KiB with 87 B left → raise to 26 KiB; reader-flip.js +900 B/+300 B gzip (row 8,448); reader.js +250 B/+100 B
   (row 8,448, 8,238 measured: likely +256); `jsTotalGzip` +~600 B; `perf_weight_test.go` reader JS +~2 KiB.
5. Browser tests (Chromium, `TENDSHELF_TEST_BROWSER`, Arabic fixture 3 chapters × 30 `ArabicParas`, Lang `ar`):
   `TestRTLPagesTurn` at 390 and 1440 (start `scrollLeft` 0; ArrowLeft → `-step`, page line advances, `data-at` moves
   on; ArrowRight back; tap at 10 % forward, 90 % back; touch swipe +120 px forward; at 1440 the left edge turns forward
   and the right edge is `aria-disabled` at the start; reload resumes at the same `scrollLeft`), `TestRTLSlideOffset`
   (record `Element.prototype.animate` keyframes: RTL forward `translateX(-24px)`, LTR `24px`), `TestRTLFlipTurn` (1440,
   flip: stage has `ts-flip--rtl`; after the flight `scrollLeft` is `-step`; the face's first sentence equals the old page's),
   `TestRTLSpreadRibbons` (ribbon 0 right of ribbon 1), `TestRTLSkimMirrored` (input `direction` rtl; value 10000 puts the
   thumb left of centre; ruler fill grows from the right), `TestRTLScrollFlow` (Arabic block right-aligned, English
   block left-aligned, ArrowLeft scrolls down), `TestRTLHebrewMixed` (an English phrase inside Hebrew: its range rects run
   left to right; paragraph right-aligned), `TestRTLOfflineShell` (as `offline_read_browser_test.go`: shell `dir` rtl and
   a turn works). WebKit: `TestVisualWebKitRTLPages` (390, 1440, ArrowLeft → negative `scrollLeft`), run with
   `scripts/isolated-display.sh make visual-webkit`. Existing paginator, flip, skim and edges suites must pass unchanged.
6. Evidence: captures of the Arabic fixture at 390/768/1024/1440 both flows (the handoff has no RTL board: say so);
   Android emulator check (Arabic book, swipe, volume keys) in the roadmap status note; `docs/roadmap.md:655` limits
   replaced by the limits below.

## 4. Out of scope
- Vertical text (tategaki): it needs `writing-mode: vertical-rl` on the reader, a paginator on `scrollTop` or block-axis
  columns, flip and skim on the vertical axis, ruby, upright digits (`text-combine-upright`), a CJK system-font stack and
  OPF `primary-writing-mode`/`ppd` together. Later, its own design.
- Mirroring the interface (Arabic/Hebrew UI copy); a per-book direction switch; DOCX `w:bidi`; PDFs whose text layer
  is in visual order (shown as extracted); search and look-up folding of harakat, niqqud and tatweel; inferring a
  language for untagged books (read aloud then offers no matching voice first; the person picks one); kashida
  justification; per-element `dir` inside a block; Arabic-specific line height.

## 5. Risks and open questions
- Flip mirror (`scale` on a perspective stage) is reasoned, not run: P3 step 3 proves it by `TestRTLFlipTurn` and a capture.
- An offline shell cached before the upgrade lacks `dir` until it is cached again (guess: on the next online open; P2 checks).
- The v4 re-read is one CPU pass over every ebook at first start; offline copies follow the existing edition-change path.
- Range-input arrow keys under RTL follow the visual side in Chromium (guess); `TestRTLSkimMirrored` presses them.
- System fonts vary: a device without Arabic or Hebrew fonts shows boxes; listed as a roadmap limit.
