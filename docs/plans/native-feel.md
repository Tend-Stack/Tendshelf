# Native feel (plan, 2026-11-03)

Decision: [0011](../decisions/0011-native-feel.md). One builder package, CSS + one templ head change + tests. Do not merge
until the operator approves the budget in step 10. Reused: P0c's native-feel block in `utilities.css`, the `.ts-press`
press look and `(hover: none)` block in `motion.css`, the theme blocks and their system copies (`static.go` `themeBody`),
the `touchstart` listener in `shell.js`, the `cdp` helper. Replaced: the `html:has(.ts-sheet-layer, ...)` overscroll rule
(made redundant by step 1). Nothing new is added beside them.

## Inventory (2026-11-03, before this package)
- Viewport `width=device-width, initial-scale=1, viewport-fit=cover`, meta `color-scheme` "dark light": `components/layout.templ:129-130` (Head, shared by Layout, Frame, LinkShell). No theme-color, format-detection, manifest, apple metas.
- `utilities.css:4-14`: tap highlight off, text-size-adjust, scrollbar-gutter; touch-action manipulation (a, button, [role=button], input, label); contain on `.ts-sheet, .ts-dialog, .ts-player__chapters`; `html:has(...)` y none; user-select none only on `.ts-tabbar__item, .ts-rail__item, .ts-side__item`. `:52-55` `.ts-safe-*` helpers, `100dvh`.
- Safe areas present: `bundle.css:568-581` (main bottom, tab bar bottom, player slot, toasts), `player.css:12,72,75,180-185`, `player-fit.css:13`, `player-bubble.css:5,21`, `now-panel.css:14-15`, `pages-bookmarks.css:26`, `pages-invite.css:92,106-107`. Missing: any top inset outside the full player, any left/right inset, `.ts-sheet` bottom (`bundle.css:310`), rail/sidebar, frame pages.
- `:active`: `bundle.css:33,36,39,62,70` (buttons, icon buttons), `motion.css:40` (`.ts-press`, used only in kitchensink). No pressed state on chips, segments, tabs, rows, cards, options, chapters, nav items, player-bar items.
- Focus: `:focus-visible` everywhere (`bundle.css:18-21` group); bare `:focus` only `pages-onboard.css:14` (programmatic target).
- No `color-scheme` property anywhere; no `-webkit-user-drag`. Inner scrollers without contain: `pages-library.css:38` (`.ts-chipmenu__body`), `pages-folders.css:3`, `pages-bookmarks.css:12`, `pages-admin.css:137` (`#users`).
- `pages-email.css:16` textarea 0.9375rem (iOS zooms on focus). `shell.js:160` document `touchstart` listener exists.

## Invariants
1. The document scrolls; no `overflow: hidden` on html/body. No bounce, no pull-to-refresh on the y axis.
2. Fields and readable content are always selectable; chrome never is.
3. Pinch zoom always works; no `user-scalable`/`maximum-scale` anywhere.
4. Every interactive class shows `:active` on touch-down; reduced motion drops the scale, keeps the colour.
5. No fixed/sticky edge element is under a notch, status bar or home bar, portrait or landscape.
6. Keyboard focus rings via `:focus-visible` only.
7. No new JS bytes, no new CSS file, no source-layer `@layer`/`!important`/inline styles; comments are kept.

## Steps (in order)
1. `internal/web/static/css/utilities.css`, "Native feel" block:
   - line 4 `html {...}`: append `overscroll-behavior-y: none;` with the comment `/* No bounce and no pull-to-refresh: a reload would stop the book that is playing. Inner scrollers contain their own. */`.
   - line 7: selector becomes `a, button, [role="button"], input, select, summary, label`.
   - delete lines 11-12 (the `html:has(.ts-sheet-layer, dialog.ts-player[open])` comment and rule).
   - replace line 14 with (comment first, as written in decision 0011 point 2):
     `.ts-nav-tabbar, .ts-nav-rail, .ts-nav-side, .ts-player-slot, .ts-player__body, .ts-btn, .ts-iconbtn, .ts-chip, .ts-seg, .ts-tabs, .ts-mode, .ts-option, .ts-chapter, .ts-card, .ts-row, .ts-selmenu, .ts-sheet__head, .ts-switch, .ts-check, label, summary, img { -webkit-user-select: none; user-select: none; -webkit-touch-callout: none; -webkit-user-drag: none; }`
     `input, textarea, [contenteditable], .ts-note__body, .ts-mark__body { -webkit-user-select: text; user-select: text; -webkit-touch-callout: default; }`
   - add a `/* Safe areas: notch, status bar, home bar, and the side notch in landscape. */` group:
     `.ts-shell { padding-inline: env(safe-area-inset-left) env(safe-area-inset-right); }`
     `.ts-main { padding-top: calc(var(--space-24) + env(safe-area-inset-top)); }`
     `.ts-nav-tabbar { padding-inline: calc(var(--space-4) + env(safe-area-inset-left)) calc(var(--space-4) + env(safe-area-inset-right)); }`
     `.ts-player-slot, .ts-toasts, .ts-sleepcard { margin-inline: env(safe-area-inset-left) env(safe-area-inset-right); }`
     `.ts-nav-rail { padding-block: calc(var(--space-16) + env(safe-area-inset-top)) calc(var(--space-16) + env(safe-area-inset-bottom)); }`
     `.ts-nav-side { padding-top: calc(var(--space-24) + env(safe-area-inset-top)); }` (not bottom: `bundle.css` sets the sidebar's bottom for the player bar)
     `.ts-sheet { padding-inline: max(var(--space-16), env(safe-area-inset-left)) max(var(--space-16), env(safe-area-inset-right)); padding-bottom: calc(var(--space-24) + env(safe-area-inset-bottom)); }`
   - Before writing, `grep -n` each selector above in `pages-*.css`, `player*.css`, `now-panel.css`: a pages-layer rule that sets the same property wins over utilities; add the env term to that rule too (expected: none for padding-top of `.ts-main`, none for `.ts-sleepcard` margin; report any found).
2. `internal/web/static/css/motion.css`:
   - lines 38-39 become `.ts-press, .ts-card, .ts-row { transition: transform ...same... }` and, after a comment `/* Feedback starts the moment a finger lands (no transition in) and eases out on release. */`, `.ts-press:active, .ts-card:active, .ts-row:active { transform: scale(0.98); transition-duration: 0s; }`.
   - In the `@media (hover: none)` block: `.ts-option:hover`, `.ts-player-bar__item:hover`, `.ts-selmenu__item:hover` each gain `:not(:active)`; the `:not(...)` lists of `.ts-chapter:hover`, `.ts-rail__item:hover`, `.ts-side__item:hover` gain `:active`.
   - After the `(hover: none)` block, before `prefers-reduced-motion`: comment `/* Press state for list items and controls: an instant state layer under the finger or the pointer. */` then
     `.ts-chip:active, .ts-seg__item:active, .ts-tabs__item:active, .ts-mode__item:active, .ts-option:active, .ts-chapter:active, .ts-selmenu__item:active, .ts-player-bar__item:active, .ts-tabbar__item:active, .ts-rail__item:active, .ts-side__item:active { background-color: var(--raised-hover); transition-duration: 0s; }`
   - Reduced-motion line 79 selector gains `.ts-card:active, .ts-row:active`.
3. `internal/web/static/css/tokens.css`: first line inside `:root, [data-theme="dark"] {` `color-scheme: dark;`; `light` and `sepia` blocks `color-scheme: light;`; `contrast` `color-scheme: dark;`. (The system blocks built by `static.go` copy them; no change there.)
4. `internal/web/static/css/player-fit.css`, first `@media (width < 1280px)` `.ts-player__body` rule: add `padding-inline: max(var(--space-20), env(safe-area-inset-left)) max(var(--space-20), env(safe-area-inset-right));`.
5. `internal/web/static/css/pages-onboard.css:12` `.ts-frame__main` padding becomes `calc(var(--space-20) + env(safe-area-inset-top)) max(var(--space-24), env(safe-area-inset-right)) calc(var(--space-32) + env(safe-area-inset-bottom)) max(var(--space-24), env(safe-area-inset-left))`; the `>= 768px` rule: `padding-top: calc(var(--space-48) + env(safe-area-inset-top)); padding-bottom: calc(var(--space-48) + env(safe-area-inset-bottom));`.
6. `internal/web/static/css/pages-book.css:12` `.ts-book__band` top becomes `calc((var(--space-24) + env(safe-area-inset-top)) * -1)`.
7. Add `overscroll-behavior: contain;` (axis-specific `-x` for horizontal ones) to: `pages-library.css` `.ts-chipmenu__body`, `pages-folders.css` `.ts-folders__body`, `pages-bookmarks.css:12` `.ts-bookmarks__chips` (x), `pages-admin.css:137` `#users` (x). `pages-email.css:16` textarea font `400 1rem/1.5rem`.
8. `internal/web/components/head_meta.go` (new): `var themeGround = map[string]string{"dark": "#14110e", "light": "#f4eee3", "sepia": "#e6d7bc", "contrast": "#000000"}`; `func colorSchemeMeta(theme string) string` ("dark light" for "", "light" for light/sepia, "dark" otherwise); `func themeColor(theme string) string` (map value, "dark" value for unknown). Doc comments say they mirror `--ground` and the theme blocks in `tokens.css`.
9. `internal/web/components/layout.templ` Head: `color-scheme` content `{ colorSchemeMeta(p.Theme) }`; then if `p.Theme == ""` two metas `<meta name="theme-color" media="(prefers-color-scheme: light)" content={ themeColor("light") }/>` and the same for dark, else one `<meta name="theme-color" content={ themeColor(p.Theme) }/>`; then `<meta name="format-detection" content="telephone=no, date=no, email=no, address=no"/>`. Viewport line unchanged. `make generate`; commit the `_templ.go`.
10. `internal/web/static/motion_test.go` TestCSSBudget: `coreBudget` `19*1024 + 512` with the comment "19.5 KB: native feel (decision 0011), operator approval <date>"; `css/utilities.css` single `{2*1024 + 512, 24*1024}`. Leave a `PLAN GAP`-style note in the report if the measured core exceeds 19.5 KB (estimate ~19,450 B). `docs/architecture.md` "CSS, motion and browser baseline": 18.5 KB to 19.5 KB.
11. `docs/roadmap.md`: a "Native feel status" paragraph under P0c (scope, checks, manual iPhone/Android checks pending). Do not edit AGENTS.md (the orchestrator adds the rule text from the decision).

## Tests
`internal/web/static/native_test.go` (package static; reuse `readCSS`, `rules`, `blocks`, `stripComments`):
- `TestNativeSelectScoped`: every rule whose body has `user-select: none` has no prelude part equal to `html`, `body`, `*`, `input`, `textarea`, `select`, `[contenteditable]`; a utilities rule whose prelude has `input`, `textarea`, `[contenteditable]` sets `user-select: text`; `html` rule has `overscroll-behavior-y: none`; no rule in any css sets `overflow: hidden` on `html` or `body`.
- `TestNativePressStates`: parse the class list of the `:focus-visible` group at the top of `bundle.css` plus `ts-option ts-chapter ts-selmenu__item ts-player-bar__item ts-mode__item`; minus exempt `ts-switch ts-input ts-select ts-check` (state is the feedback); each has a rule in bundle/utilities/motion whose prelude contains `.<class>:active`. Every `:active` rule in motion.css's state-layer and press rules has `transition-duration: 0s`.
- `TestNativeHoverKeepsActive`: in `blocks(motion, "@media (hover: none)")`, every rule that sets `background` and names a state-layer class from step 2 contains `:active` in its prelude.
- `TestNativeFocusVisible`: no selector with `:focus` not followed by `-visible` or `-within` except `.ts-frame__main:focus`; `outline: none|0` only in rules whose prelude has `:focus-visible`, or is `.ts-main` / `.ts-frame__main:focus`.
- `TestNativeFieldSize`: rules whose prelude names `textarea`, `.ts-input`, `.ts-select` or `input[type=` (not `::placeholder`) never set a font size below 1rem (check `font-size:` and the size in `font:` shorthand; px below 16 fails).
- `TestNativeSafeAreas`: table selector to edges, each must appear in some rule (any css) with `env(safe-area-inset-<edge>)`: `.ts-nav-tabbar` bottom/left/right; `.ts-player-slot` bottom/left/right; `.ts-toasts` bottom/left/right; `.ts-sheet` bottom/left/right; `.ts-main` top; `.ts-shell` left/right; `.ts-nav-rail` top/bottom; `.ts-nav-side` top; `.ts-player__body` top/bottom/left/right; `.ts-frame__main` all four.
- `TestNativeOverscroll`: every rule with `overflow(-x|-y)?: (auto|scroll)` also has `overscroll-behavior` in its body, except `.ts-player__body`, `.ts-player__chapters` and `.ts-sheet-layer--* .ts-sheet` / `.ts-details-sheet`, which must have contain in another rule (assert that).
- `TestNativeColorScheme`: each theme block in `tokens.css` has the step-3 value; the served `css/tokens.css` (`Load` + `bodyOf`) system blocks contain `color-scheme: light` (light copy) and `color-scheme: dark` (contrast copy).
- `TestNativeTouchListener`: `js/shell.js` contains `document.addEventListener("touchstart"` (iOS needs a touch listener for `:active`; the comment lives in the test, not in shell.js).
`internal/web/native_head_test.go` (package web): render `components.Head` for `"", dark, light, sepia, contrast` with `mustAssets(t)`; viewport content exact and no `user-scalable`/`maximum-scale`; color-scheme and theme-color metas per step 9; format-detection present; `themeGround` equals `--ground` of each block in `static/css/tokens.css` (dark from the `:root` block), regexp as in `browse_test.go:647`.
`internal/web/native_feel_browser_test.go` (`//go:build dev`, `chromium(t)`): call `tab.Call("Emulation.setSafeAreaInsetsOverride", {"insets":{"top":47,"topMax":47,"bottom":34,"bottomMax":34,"left":0,"leftMax":0,"right":0,"rightMax":0}}, nil)` (verify the parameter shape in the browser's `/json/protocol`; if the method is missing, `t.Skip("no safe-area override in this Chromium; manual check pending")` and say so in the report). Reload after the override. Use the player fixture (`playerApp`, `themedServer`, `playerOpen`, `playerInject`) so the mini player shows.
- 390x844 portrait: `#main` paddingTop 71px; tab bar paddingBottom ≥ 34 and rect.bottom = innerHeight; player slot rect.bottom ≤ tab bar rect.top; a canonical `?sheet=` page's `.ts-sheet` paddingBottom 58px; full player open: `.ts-player__body` paddingTop ≥ 47; `/login` `.ts-frame__main` paddingTop 67px.
- 844x390 landscape, insets left/right 47, bottom 21: rail rect.left ≥ 47; `#main` rect.right ≤ 797; player slot rect.left ≥ rail rect.right and rect.right ≤ 797; full player `.ts-player__body` paddingLeft and paddingRight ≥ 47.
- Computed: `html` overscrollBehaviorY "none"; userSelect "none" on `.ts-tabbar__item`, `.ts-card`, a `.ts-btn`; "text" on `input` and `.ts-note__body` if present; book page description element not "none"; `colorScheme` of `html` per theme for the four `data-theme` values.

## Gates
`make generate` (tree clean after), `go test -race ./internal/web/static/ ./internal/web/components/ ./internal/web/` (changed packages), `make check`, `make build`; `TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run 'NativeFeel' ./internal/web/`. Visual: `make visual` before and after into two folders and `capturediff`; expected differences only where the theme meta or press states cannot show (none expected: insets are 0 in captures); report "N of M identical" and name any that differ. Report the TestCSSBudget core number.

## Manual checks (record in the roadmap; not done by the builder)
iPhone (Safari and home-screen): no bounce, long-press on a card shows no callout, a button darkens on touch-down,
notch/home bar clear in both orientations, focusing the email textarea does not zoom. Android Chrome: no
pull-to-refresh, toolbar takes the theme colour.

## Out of scope
The Wails shell and `data-shell` (M6, reserved in 0011); native mobile apps; manifest, service worker, install,
`apple-mobile-web-app-*`, status-bar style, offline (M3); arrow cursor on controls (M6, shell only); custom
scrollbar colours (the platform's own, themed by `color-scheme`, is the native look); `interactive-widget`
keyboard resizing (needs device testing; M3); Firefox drag ghosts (no `draggable` attributes; not a shell engine);
the skip link's inset (keyboard only); theme-color following the full player's tint (needs JS).
