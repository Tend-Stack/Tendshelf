# M3c: accessibility audit (WCAG 2.2 AA, contrast theme 7:1) and performance budgets at four widths

Roadmap: `docs/roadmap.md` M3, third bullet. Widths 390x844, 768x1024, 1024x768, 1440x900 (`mobile` = width < 1280);
themes `dark`, `light`, `sepia`, `contrast`. No new module, no Node, no axe: the checks are our own JS run through
`internal/testutil/cdp` plus Chromium's own accessibility tree (`Accessibility.getFullAXTree`).

## Inventory (reused, extended, replaced)
- Reused: `cdp` (Start, NewTab, Eval, Call, Navigate, WaitFor); `chromium(t)`, `themes`, `visualDir`
  (`visual_motion_test.go`); seeds `bookVisualSeed`, `bookSeedProgress`, `shelfVisualSeed`, `queueVisualSeed`,
  `adminSeed`, `sharedHomeSeed`, `asServer`, `uploadVisualApp`, `playerInject`, `playerClickSel`; `seedVisualIDs`;
  perf helpers `sweepPerfApp`, `sweepMedian`, `sweepCPUModel`, `sweepPerfRate`, `sweepNavTimingJS`.
- Extended: the contrast maths of `nowPanelRatio` (canvas sRGB) becomes the audit's general checker; the 44 px idea
  of `downloads_visual_test.go` becomes a page-wide check; `TestPerfLibraryPage`'s measure loop becomes a shared helper.
- Kept as is: `TestNativeFocusVisible`, motion, large-text, CSS/JS gzip budgets (`static/motion_test.go`).
- Not added: no second browser driver, no second route list (the audit's page table is checked against the
  registered `"GET /..."` literals by a source-scan test).

## Global invariants
1. A threshold is never lowered to pass. AA: text 4.5:1, large text (>= 24 px, or >= 18.66 px at weight >= 700) 3:1,
   non-text (control boundary, focus indicator, icon-only control) 3:1. Contrast theme: text 7:1, large text 4.5:1
   (the WCAG AAA 1.4.6 meaning of "7:1"), non-text 3:1.
2. Every registered full-page GET route is audited or skipped with a written reason; a new route without either fails
   `make check`.
3. The allowlist starts empty. An entry needs a reason; an entry that matches nothing in a full run fails.
4. Findings are data (`.local/tests/a11y/<UTC run>/findings.json`), not log floods: the test fails with per-check counts
   and the first 20 lines.
5. Deterministic: no timing in a11y checks, seeded ids (`seedVisualIDs`), reduced motion on every audit tab.
6. Only the test tree changes in A1, A2, C; app code changes only in B packages; D changes docs and CI.

## Packages and parallelism
| Wave | Package | Model | Runs beside | Shared files (hunk owned) |
| --- | --- | --- | --- | --- |
| 1 | A1 harness, structure, text contrast | sonnet | C | `Makefile` (new `a11y` target after `perf`) |
| 1 | C perf at four widths, page weight | sonnet | A1 | `Makefile` (`perf` regex, new `page-weight` target), `perf_test.go` |
| 2 | A2 targets, focus, non-text contrast, reflow, 200 % text | sonnet | C | none beyond A1's files |
| 3 | B1 colour tokens and focus ring | sonnet | B2, B3 | `tokens.css` (sole owner) |
| 3 | B2 markup: names, labels, headings, landmarks | sonnet | B1, B3 | `*.templ`, `*_templ.go` (sole owner) |
| 3 | B3 sizes and layout: targets, reflow, clipping | sonnet | B1, B2 | `bundle.css`, `utilities.css`, `pages-*.css`, `static/motion_test.go` CSS rows |
| 4 | D docs, nightly CI, roadmap | sonnet | - | `.gitea/workflows/nightly.yml`, `docs/manual-checks.md`, `docs/roadmap.md` |

Test-name prefixes: A1 `TestA11y*` with helpers `a11y*` in `a11y_*_test.go`; A2 `TestA11yA2*` with helpers
`a11yFocus*`, `a11yTarget*`, `a11yReflow*`, `a11yText200*`; C `TestPerfPages*`, `TestPageWeight*`, helpers `sweep*`,
`weight*`; B packages add tests only with prefixes `TestB1*`, `TestB2*`, `TestB3*` (or extend existing tests in files
they own). Gate main after each merge (`make check`).

---

## Package A1: audit harness, structure checks, text contrast (builder, sonnet)

Invariants: global 1-6. The harness never mutates the app beyond seeding and `data-theme` on `<html>`.

Decisions
- JS lives in `internal/web/testdata/a11y/*.js` (readable, one statement per line), read with `os.ReadFile` and passed
  to `tab.Eval`; testdata is not shipped, so the "JS only in static/js" rule is not touched.
- Names and roles come from `Accessibility.getFullAXTree` (Chromium's real name computation), not a JS imitation.
- Files are `//go:build dev` (they use the dev-tagged visual seeds); `make check` already runs `-tags dev`.
- Structure checks run once per page per width in `dark`; text contrast runs per theme per width.
- Fast mode `TENDSHELF_A11Y_FAST=1`: themes `dark`,`contrast`; widths 390, 1440. `make a11y` is the full matrix.

Steps
1. `internal/web/a11y_pages_test.go` (new, dev): `type a11yPage struct{ Key, Pattern string; Path func(e *a11yEnv) string;
   Roles []string /* "owner","member","anon" */; Fixture string /* "main","fresh","upload" */; Shell bool }`;
   `type a11yState struct{ Key string; Role string; Path func(e *a11yEnv) string; Prepare func(t *testing.T, tab *cdp.Tab, e *a11yEnv) }`;
   `func a11yPages() []a11yPage`; `func a11yStates() []a11yState`; `var a11ySkip = map[string]string{pattern: reason}`.
   Table (Pattern without "GET "): `/{$}`; `/libraries`; `/libraries/{id}`; `/libraries/{id}/add` (fixture upload);
   `/uploads/{id}` (upload); `/search` twice (empty, and `?q=` a seeded title word); `/books/{id}`; `/books/{id}/{tab}`
   one row per tab value the handler accepts (read the switch); `/books/{id}/remove`; `/books/{id}/share`;
   `/authors/{id}`; `/narrators/{id}`; `/series/{id}`; `/shelves`; `/shelves/{id}`; `/shelves/{id}/delete`; `/queue`;
   `/me`; `/me/settings`; `/me/settings/{section}` one row per section; `/me/password`; `/me/downloads`;
   `/me/listening`; `/share`; `/welcome`; every `/admin/...` page pattern; `/login`, `/invite/{token}`, `/s/{token}`,
   `/offline` (anon; `/offline` also owner); `/setup`, `/setup/server`, `/setup/folder`, `/setup/done` (fixture fresh,
   reached by the form posts `visual_admin_test.go` uses); `/dev/kitchen-sink`, `/dev/kitchen-sink/b` (owner).
   Roles: owner on every page it gets 200 for; member on every non-admin page it gets 200 for (per `sweepRows` wants).
   States: `full-player` (owner, `/books/{id}`, `playerInject` then `playerClickSel` on the mini-player title),
   `eyes-free` (from `full-player`, open Eyes-free), `now-panel` (owner, `/`, `playerInject`, widths >= 1280 only).
   `a11ySkip` holds only non-page GETs (redirect, file, JSON) with the reason, e.g. `/progress/{bookID}`.
2. `internal/web/a11y_env_test.go` (new, dev): `type a11yEnv` (ids, tokens, servers per role and fixture);
   `func a11ySeed(t *testing.T) *a11yEnv`: `seedVisualIDs`, one `main` app with `bookVisualSeed`, `bookSeedProgress`,
   `shelfVisualSeed`, `queueVisualSeed`, `adminSeed`, a member holding a library grant (as `sharedHomeSeed` does), an
   invite token and a share link (as `TestShareBrowser` creates them); servers via `asServer` (owner, member) and
   `httptest.NewServer(a.h)` (anon); `fresh` = `newTestApp(t)` with no owner; `upload` = `uploadVisualApp(t)`.
3. `internal/web/a11y_audit_test.go` (new, dev): `a11yWidths = []struct{W,H int}{{390,844},{768,1024},{1024,768},{1440,900}}`;
   `a11yMatrix()` (fast mode); `type a11yFinding struct{ Check, Page, Role, Theme string; Width int; Selector, Text,
   Detail string; Measured, Required float64 }` (json tags lower camel); `a11yTheme(tab, theme)` sets `data-theme` and
   awaits two `requestAnimationFrame`s in one Eval promise; `a11yLoad(tab, url, shell)` = Navigate, then WaitFor
   `performance.getEntriesByName("ts-ready").length > 0` when Shell else `document.readyState === "complete"`;
   `func TestA11yAudit(t)`: `chromium(t)`, seed, then subtests `role/pageKey`, one tab per width
   (`NewTab(w,h,w<1280,true)`), collecting findings; at the end write `findings.json` (`run`, `mode`, `pages`,
   `states`, `findings`, `unmeasured`, `allowed`) and `summary.txt` under `.local/tests/a11y/<run>/` (`TENDSHELF_A11Y_OUT`
   overrides); fail with counts per check plus the first 20 un-allowed findings.
4. `internal/web/testdata/a11y/structure.js`: returns findings for `lang` (`html[lang]` matches `^[a-z]{2}`), `title`
   (non-empty), `heading-skip` (a heading more than one level deeper than the previous visible one), `nav-missing`
   (Shell page without a visible `nav` or `[role=navigation]`), `img-alt` (`img` without `alt` attribute), `label-missing`
   (visible `input` not hidden/submit/button/reset, `select`, `textarea` with no `labels`, no `aria-label`, no
   `aria-labelledby`), `duplicate-id`, `aria-ref-broken` (`for`, `aria-labelledby`, `aria-describedby`,
   `aria-controls` naming a missing id), `aria-hidden-focusable` (a focusable, non-`tabindex=-1` element inside
   `[aria-hidden=true]`), `viewport-zoom` (`user-scalable=no` or `maximum-scale` < 2), `autocomplete-missing`
   (`input[type=password]` without `current-password`/`new-password`; username/email fields without
   `username`/`email`), `live-region-missing` (Shell page: the toast container in `components/layout.templ` lacks
   `role=status|alert` or `aria-live`). Selector = `#id`, else tag plus up to two classes, up to four ancestors joined `>`.
5. `internal/web/a11y_ax_test.go` (new, dev): `a11yAX(t, tab) []a11yFinding`: `Accessibility.enable`, then
   `Accessibility.getFullAXTree`; non-ignored nodes give `h1-count` (heading level 1 count != 1), `main-count`
   (role `main` count != 1), `name-missing` (roles button, link, textbox, searchbox, checkbox, radio, switch, combobox,
   listbox, slider, spinbutton, tab, menuitem, menuitemcheckbox, menuitemradio, option, image with empty name).
   Selector: `DOM.resolveNode{backendNodeId}` then `Runtime.callFunctionOn` with the selector function from structure.js;
   if that fails, `role "name"` as the selector (guess: `DOM.getDocument` may be needed first; do it once per tab).
6. `internal/web/testdata/a11y/contrast.js`: for every visible text node (`checkVisibility({opacityProperty:true,
   visibilityProperty:true})`, rect > 1x1, not clipped 1 px sr-only, not `:disabled`, not inside `[aria-disabled=true]`
   or `[inert]`): text colour with alpha times the opacity product of its ancestors, composited over the ancestor
   background chain via canvas (as `nowPanelRatio`); if an `img`, `video`, `canvas`, `picture` or an element with
   `background-image != none` lies under the text rect centre (`document.elementsFromPoint`) above the first opaque
   ancestor background, push to `unmeasured` (check `contrast-text`, detail "over an image"). Required ratio from
   invariant 1 using the computed font size and weight; theme passed as argument. One finding per page, theme, width,
   selector, colour pair. Detail: `#rrggbb on #rrggbb 15px 400`.
7. `internal/web/testdata/a11y/allow.txt` (new, header comment only). Line form: `<check> <pageKey|*> <selector> -- <reason>`.
   `a11yAllow` parser in `a11y_audit_test.go`; matching ignores role, theme and width.
8. `internal/web/testdata/a11y/fixture-a1.html` + `fixture-a1.css` (served by a test `httptest` file server): planted
   cases listed in the tests below, each with `data-expect="<check>"` or `data-expect="none"`.
9. `Makefile`: after `perf`, add
   `A11Y_RUN ?= TestA11y` and `a11y:` -> `$(GO) test -count=1 -tags dev -timeout 45m -run '$(A11Y_RUN)' -v ./internal/web/`
   with a comment like the `perf` one (needs TENDSHELF_TEST_BROWSER; output path).

Acceptance tests (all dev-tagged)
- `TestA11yRouteCoverage` (no browser): scans non-test `internal/web/*.go` for `"GET (/[^"]*)"`, drops prefixes `/hx/`,
  `/api/`, `/static/`, `/stream/`, `/covers/`, `/peaks/`, `/sw.js`, `/healthz`, `/probe/` and literals ending in
  `/cover`, `/photo`, `/export`, `/cover/{n}`, `manifest`, `.png`, `.ico`; each remaining pattern is in `a11yPages()`
  or `a11ySkip` (non-empty reason); every `a11yPages` pattern exists in the source.
- `TestA11yAllowlistFormat` (no browser): every non-comment line parses, reason >= 10 characters, known check id.
- `TestA11yHarnessCatches` (browser): on fixture-a1, every `data-expect` check fires on its element and nothing fires
  on `none`. Plant at least: `#777` on `#fff` 16 px (4.48, fails), `#767676` on `#fff` (4.54, passes), 24 px `#949494`
  on `#fff` (3.03 large, passes AA), the same pair at 18 px (fails), white on `#595959` in contrast theme (7.0 passes),
  `#8a8a8a` on `#000` in contrast theme (6.08: fails 7, would pass AA), 50 % opacity text (fails), `color-mix`
  text, text over `background-image` (unmeasured), sr-only text (skipped), disabled button text (skipped), icon
  button with no name, input with only a placeholder, `img` without alt, two `h1`, `aria-labelledby` to a missing id,
  focusable link inside `aria-hidden`, `user-scalable=no` in a second fixture page.
- `TestA11yAudit` (browser): the matrix; findings file written even when it fails.

Gates: `go test -tags dev -count=1 -run 'TestA11yRouteCoverage|TestA11yAllowlistFormat' ./internal/web/`;
`TENDSHELF_TEST_BROWSER=/home/rubirosa/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome go test -tags dev
-count=1 -run 'TestA11yHarnessCatches' ./internal/web/`; one `TENDSHELF_A11Y_FAST=1 make a11y` and one full `make a11y`
(expected to FAIL on real findings: report the per-check counts and the findings path, do not fix); `make check`;
`make build`. A1 is done when the harness test passes and the full run completes, red or green.

Out of scope: fixing findings (B); targets, focus, reflow (A2); forced-colors; placeholder contrast; PDF/ebook text;
`/hx/` fragments not reachable as a state.

---

## Package A2: targets, keyboard focus, non-text contrast, reflow, 200 % text (builder, sonnet; after A1 merges)

Invariants: global 1-6; A1's runner, finding type and allowlist are extended, not copied.

Decisions
- Target size: 44x44 CSS px (AGENTS.md) at 390, 768 and 1024 (touch layouts); at 1440 the WCAG 2.5.8 minimum of 24 px.
  Operator decision Q1 below; the size per width is the one constant `a11yTargetMin(width)`.
- A hit area grown by a pseudo-element counts: when the box is short, probe `elementFromPoint` at the centre +-21.5 px
  (+-11.5 at 1440) on each short axis after `scrollIntoView({block:"center",inline:"center"})`; pass if every probe
  hits the element or a descendant.
- Focus is walked with real Tab key events (`Input.dispatchKeyEvent`, as `share_browser_test.go:69`), once per page per
  width at 390 and 1440; at each stop the ring contrast is read in all four themes without moving focus.
- Layout checks run in `dark` only (theme does not change geometry).

Steps
1. `internal/web/testdata/a11y/targets.js`: targets = `a[href]`, `button`, `input:not([type=hidden])`, `select`,
   `textarea`, `summary`, `[role=button|link|tab|switch|checkbox|radio|menuitem|option|slider]`,
   `[tabindex]:not([tabindex="-1"])`; a visually hidden input is measured through its `label`. Skip invisible, `[inert]`,
   `[aria-hidden=true]` subtrees, and inline text links (an `a` with `display:inline` whose nearest block ancestor has
   more than 1 character of text outside it). Finding `target-size`, detail `WxH`, plus `wcag258` true/false (>= 24).
2. `internal/web/testdata/a11y/focus.js`: `stop()` returns for `document.activeElement`: selector, `:focus-visible`
   match, outline style/width/colour, parsed `box-shadow` rings (colour, spread), background behind (ancestor chain),
   5-point `elementFromPoint` visibility test (centre and four corners inset 2 px). `tabbables()` lists visible
   tabbable elements for the reachability diff.
3. `internal/web/a11y_focus_test.go` (new, dev): `a11yFocusWalk(t, tab, page, width)`: Tab up to
   `min(len(tabbables)+5, 200)` times. Findings: `focus-not-visible` (no `:focus-visible`, or outline none/width < 2 px
   and no ring with spread >= 2 px), `focus-indicator-contrast` (per theme, best of outline colour or any ring colour
   vs the background behind < 3:1), `focus-obscured` (no sample point hits it, e.g. under the tab bar or player),
   `focus-trap` (same element after 3 consecutive Tabs while other tabbables exist), `focus-unreachable` (a visible
   tabbable never focused in the walk). Before the walk: click `body` at (1,1) so focus starts at the document.
4. `internal/web/testdata/a11y/noncontrast.js`: `contrast-control` (visible `input`, `select`, `textarea`,
   `[role=switch]`, checkbox, radio: border colour if border width >= 1 px else the field background, vs the background
   behind < 3:1); `contrast-icon` (button or link with no visible text whose `svg` uses `currentColor`: colour vs
   background < 3:1). Runs per theme per width.
5. `internal/web/testdata/a11y/reflow.js`: `scroll-x` when `document.scrollingElement.scrollWidth > innerWidth + 1`
   (every width); `text-200-clip`: elements with `overflow` not `visible` and text whose `scrollWidth`/`scrollHeight`
   exceed the client box by > 1 px, unless `text-overflow: ellipsis` or `-webkit-line-clamp` is set (those go to
   `unmeasured` as "truncated").
6. `internal/web/a11y_audit_test.go`: call targets (dark, every width), focus walk (390 and 1440), noncontrast (every
   theme and width), reflow at every width; then `reflow-320` = a 320x640 mobile tab, `scroll-x` renamed `reflow-320`;
   then `text-200-scroll-x`/`text-200-clip` = set `document.documentElement.style.fontSize = "200%"` (CSSOM, not an
   attribute; CSP allows it) at 390 and 1440 and rerun reflow.js. Keep fast mode working.
7. `internal/web/testdata/a11y/fixture-a2.html` + `.css`: planted cases for every A2 check.

Acceptance tests
- `TestA11yA2HarnessCatches` (browser): each planted A2 case fires; passing twins do not (a 32x32 button with a
  `::after` hit area grown to 44 passes; a bare 32x32 fails; an outline-none button with no ring fails; a button with
  the dark theme's `--focus-ring` passes in all four themes; a sticky bar covering a focused link fails
  `focus-obscured`; a `tabindex=-1`-only page has no `focus-unreachable`; a 400 px fixed-width box fails `reflow-320`).
- `TestA11yA2TargetMin` (no browser): `a11yTargetMin(390)==44`, `(1024)==44`, `(1440)==24`.
- `TestA11yAudit` keeps running all checks.

Gates: as A1, with `-run 'TestA11yA2'`; one full `make a11y` (expected red): report counts per check and the path.

Out of scope: fixes; `forced-colors`; pointer gestures (2.5.1/2.5.7, manual in D); timing of live announcements.

---

## Package C: performance at four widths and page weight (builder, sonnet; parallel with A1/A2)

Invariants
- Budgets are numbers in code with a dated comment; never raised without the operator.
- `TestPerfLibraryPage` keeps its fixture, budget, JSON file name and keys (one key added: `lcpMs`).
- Timing tests stay behind `TENDSHELF_TEST_PERF=1`; weight tests need only the browser (byte counts are deterministic).

Decisions
- Interactive budget everywhere = the house 300 ms median at 4x CPU (`sweepPerfRate`), all four widths.
- LCP (`largest-contentful-paint`, buffered observer) is logged and written, no threshold this milestone (headless
  LCP on localhost is mostly render time; operator may set one after a week of numbers).
- Weight budgets: the builder runs `TestPageWeight` three times, takes the max per page and width, sets bytes =
  ceil(max x 1.15) to the next 1 KiB and requests = max + 2, and lists the numbers in the report.
- HTML is measured as served (dynamic HTML is not gzipped today: no `Content-Encoding` outside static and `/sw.js`).
  Compressing HTML is Q2, not done here.

Steps
1. `internal/web/perf_test.go`: extract `sweepMeasureLoad(t testing.TB, b *cdp.Browser, url string, w, h int, mobile,
   cold bool) sweepLoad` from the `measure` closure (throttle, cache, Navigate, wait `ts-ready`, nav timing, LCP via
   new `sweepLCPJS`); add `LCPMs float64 \`json:"lcpMs"\`` to `sweepLoad`; `TestPerfLibraryPage` calls it and keeps its
   scroll step.
2. `internal/web/listening_perf_test.go`: extract the session-seeding loop of `TestPerfListeningYear` into
   `sweepPerfSessions(t testing.TB, st <the type db.Open returns>, userID string, books []string, days int)`; behaviour unchanged.
3. `internal/web/perf_pages_test.go` (new, no tag): `sweepPerfPagesApp` = `sweepPerfApp` plus progress on 20 books for
   the owner and 365 days of sessions (`sweepPerfSessions`). `sweepPerfWidths` = the four widths. `TestPerfPages`:
   subtests `home`, `library`, `book`, `listening`, `search` (`/search?q=Archive`) x width; warm-up + 5 loads; median
   interactive <= `sweepPerfBudget`; JSON `.local/tests/perf/<run>/pages.json` (cpu, throttle, per page/width loads,
   medians, LCP medians, pass).
4. `internal/web/perf_weight_test.go` (new, no tag): `TestPageWeight` (skips without the browser): same fixture, same
   five pages plus `/me/settings` and `/admin/users`, four widths; fresh tab, `Network.setCacheDisabled`, wait `ts-ready`
   then no new `resource` entries for 500 ms (cap 10 s); sum `encodedBodySize` by type (html = navigation entry,
   css, js = `script` + module, font, img) and count requests; compare to
   `weightBudgets map[string]weightBudget{HTML, CSS, JS, Font, Img int64; Requests int}` keyed by page, max over widths;
   JSON `.local/tests/perf/<run>/weight.json` with `htmlCompressed` per page.
5. `Makefile`: `perf` regex becomes `'TestPerf|TestPageWeight'`; add `page-weight:` ->
   `$(GO) test -count=1 -v -run 'TestPageWeight' ./internal/web/` with a comment.

Acceptance tests: `TestPerfPages`, `TestPageWeight`, `TestPerfLibraryPage` (unchanged result), plus
`TestSweepMedian` (no browser, table: odd, even, one value) if absent.

Gates: `go test -count=1 -run 'TestSweepMedian' ./internal/web/`; with the browser `make perf` and `make page-weight`
(report medians per page and width); `make check`; `make build`. A page over budget is reported with numbers, not
fixed (the Library message stands: no index tuning here).

Out of scope: HTML compression (Q2); server-side caching; image format changes; fixing any slow page.

---

## Packages B1, B2, B3: fix the findings (builders, sonnet; after A2 merges; parallel)

Step 0 (orchestrator): run full `make a11y` on main, split `findings.json` by check into three briefs: B1 gets
`contrast-text`, `contrast-control`, `contrast-icon`, `focus-indicator-contrast`, `focus-not-visible`; B2 gets the
structure and AX checks, `focus-trap`, `focus-unreachable`, `aria-*`, `label-missing`, `name-missing`; B3 gets
`target-size`, `focus-obscured`, `scroll-x`, `reflow-320`, `text-200-*`. Paste the per-check counts and the top 40
selectors into each brief.

Invariants (all three)
- Fix the cause at the shared level first (token, component, `ts-` class), page CSS last.
- Never lower a threshold; an allowlist line needs a reason and is listed for operator approval in the report.
- Every visible change is proven: `TENDSHELF_VISUAL_OUT=/tmp/m3c-before make visual` before, after into
  `/tmp/m3c-after`, `go run ./internal/testutil/capturediff`; list each changed capture with the finding it fixes.
- A change that alters the design (new layout, larger type, removed element, new copy not in the deck) is not made:
  list it as `DESIGN: <page> <what> <why>` for the operator.

B1 (colour): edit only the failing theme's block in `internal/web/static/css/tokens.css`; a new value keeps the hue and
moves only OKLCH lightness to the nearest value that passes, for every place the token is used (rerun the audit).
Write `docs/decisions/0016-contrast-tokens.md` (old -> new per token and theme, ratio before and after; it amends the
handoff tokens per decision 0002). Tests: `TestB1TokenContrast` in `internal/web/static/` (no browser): parses each
theme block and asserts ink tokens on `--ground`, `--surface`, `--raised` meet invariant 1 (resolve `#rrggbbaa` by
compositing; skip `color-mix` values with a comment).
B2 (markup): `aria-label`/visible labels with copy-deck words or BRAND voice (list new strings as NEW), one `h1`, one
`main`, toast `role="status"`, `alt=""` for decorative images; regenerate templ (`make generate`). Tests: extend the
page's existing httptest test with the attribute, test names `TestB2<Page><Fix>`.
B3 (size/layout): add one utility in `utilities.css`, `.ts-hit` = `position: relative` plus `::after { content: "";
position: absolute; inset: min(0px, calc((100% - 44px) / 2)); }` (grep first; reuse an existing hit-area class if
one exists), apply it to small controls; overflow fixes in the owning page CSS; update the CSS budget rows in
`static/motion_test.go` only if over, and report the overrun instead of squeezing. Tests: `TestB3HitArea` (static CSS
contains the rule) and the A harness.

Gates (each B): its own tests; `TENDSHELF_A11Y_FAST=1 make a11y` until its checks are at 0; `make visual` +
capturediff; `make check`; `make build`. Orchestrator runs the full `make a11y` on the merged tree: target 0 findings.

Out of scope: design changes (listed), copy outside the deck without listing, AAA beyond the contrast theme.

---

## Package D: record and CI (builder, sonnet; after B)
1. `docs/accessibility.md` (new): target (WCAG 2.2 AA; contrast theme 7:1), how it is tested (`make a11y`, the checks
   and widths/themes), the allowlist entries with reasons, known limits (out-of-scope lists above), manual checks link.
2. `docs/manual-checks.md`: section "M3c keyboard and screen reader" table: Orca with Firefox on Ubuntu, TalkBack on
   Android Chrome, VoiceOver on iOS Safari: sign in, Home to play a book by keyboard only, full player keys, a sheet
   opens with focus inside and returns focus on close, toasts announced, offline banner announced, a form error
   announced, browser zoom 200 % and 400 % at 1280 px, Windows/forced colours, pointer drag alternatives (scrubber by
   keys). Result column empty.
3. `.gitea/workflows/nightly.yml`: after `make visual-full`, steps `make a11y` and `make page-weight` (not `make perf`:
   timing on a shared runner is noise). Not in `ci.yml` (no browser there).
4. `docs/roadmap.md`: an `**M3c status**` paragraph in the house form (scope, checks run with counts, limits, next).
Gates: `make check` (docs-only change still gates); a nightly dispatch if the runner allows `workflow_dispatch`.

## Operator decisions (defaults apply if not answered)
- Q1 target size at 1440: default 24 px (WCAG 2.5.8) at desktop, 44 px at 390/768/1024. Alternative: 44 everywhere.
- Q2 gzip for dynamic HTML: default not in M3c (pages echo the search query beside the CSRF token, a BREACH shape over
  HTTPS); revisit with a per-response random padding or token masking.
- Q3 token changes in dark/light/sepia that move a brand colour: default allowed under B1's lightness-only rule and
  recorded in decision 0016; the operator reviews the capture diff before merge.
