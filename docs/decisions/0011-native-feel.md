# 0011 Native feel in every WebView
Status: accepted (2026-11-03; operator approved core CSS 19.5 KB, utilities.css 2.5 KB and the Android stretch trade-off). Plan: `docs/plans/native-feel.md`.

## Operator's words
- "Let's not forget this app will be served on Wails v3 too, and eventually makes its way to mobile; we want the
  end user not to notice this is not a fully native app. From ground 0 we must make sure that the UI/UX behaviors
  act like a native app, the same goes for desktop Wails on mac, windows, linux. For mobile it is important to
  apply standard CSS and configuration tweaks to our templ layouts to make the WebView behave like an iPhone or
  Android screen."
- Examples, "just examples, apply the necessary tweaks as needed for our use case": disable scrolling bounces
  and text selection (`body { user-select: none; -webkit-user-select: none; overflow: hidden; }`); remove click
  delays and clean `:active` states so buttons highlight the instant a thumb touches them; safe areas with
  `env(safe-area-inset-top)` and the others so headers are not cut off by the notch or the home bar.
- "With Wails v3 + Go + htmx + templ we could have the ultimate solo developer dream stack ... but only if we
  apply it smartly and efficiently."

## Context
P0c (roadmap) already shipped part of this in `utilities.css` ("Native feel"): no tap highlight,
`text-size-adjust`, `touch-action: manipulation` on controls, `overscroll-behavior: contain` on sheets, dialogs
and the chapter list, no selection on the three navigation item classes, `viewport-fit=cover`, safe-area
padding on the tab bar, mini player, toasts, full player (top and bottom) and `100dvh`. Gaps: the document still
rubber-bands and pulls to refresh (a reload stops the book), most controls can be long-pressed into a selection
or a callout, many controls have no pressed state, nothing clears the top inset or the landscape side notch,
sheets ignore the home bar, form controls and scrollbars ignore the chosen theme (`color-scheme` is a fixed
"dark light" meta), the email textarea is 15 px (iOS zooms the page when it gets focus), and there is no
`theme-color`.

## Decision
1. **The document stays the scroller.** No `overflow: hidden` on body: pages scroll the document and the bars
   are `position: fixed`. Bounce and pull-to-refresh go with `overscroll-behavior-y: none` on `html`; every
   inner scroller gets `overscroll-behavior: contain`. Only the y axis: a browser keeps its horizontal
   back/forward swipe. Trade-off: Android loses the stretch effect at the page ends; a pull-to-refresh that
   reloads the page and stops playback is worse.
2. **Selection off on chrome, on for content.** `user-select: none`, `-webkit-touch-callout: none` and
   `-webkit-user-drag: none` on navigation, the player, controls, list rows and cards, labels, summaries and
   images. Fields (`input`, `textarea`, `[contenteditable]`) and readable text (descriptions, notes, bookmark
   text, error text, paths) stay selectable, with an explicit `user-select: text` rule for fields and notes.
   Screen readers are not affected by `user-select`.
3. **Pressed state in the motion layer, instant in, eased out.** Rows and cards scale to 0.98 (the existing
   `.ts-press` look); chips, segments, tabs, menu items, chapters, player-bar items and the navigation items get a
   `--raised-hover` state layer. `:active` sets `transition-duration: 0s`, so feedback appears on touch-down and
   the resting transition eases it out. The `(hover: none)` resets skip `:active` so a tap is not hidden by
   them. No JS: `shell.js` already listens to `touchstart` on the document, which WebKit on iOS needs before
   it applies `:active` (guess, recorded as a manual iPhone check); a test keeps that listener.
4. **Zoom stays.** The viewport keeps `width=device-width, initial-scale=1, viewport-fit=cover`; never
   `user-scalable=no` or `maximum-scale` (WCAG 1.4.4; iOS ignores them anyway). Double-tap zoom and its delay
   go through `touch-action: manipulation` on controls. Text fields are at least 16 px so iOS never zooms on
   focus.
5. **Safe areas on every edge element**, including landscape left and right: the shell pads left and right;
   `#main`, the rail and the sidebar pad the top; fixed bars, toasts and sleep cards take the side insets as
   margins (one rule that works at every breakpoint); sheets, the full player and the sign-in frame pad all
   edges they touch. The book page's tinted band reaches up under the status bar.
6. **The platform's own scrollbars and controls, in the right scheme.** `color-scheme` is set in each theme
   block of `tokens.css` (dark and contrast: dark; light and sepia: light), so the system-theme copies in
   `static.go` carry it too; the meta follows the theme. No custom scrollbar colours: matching the OS is the
   native look.
7. **Browser chrome matches the app.** `theme-color` meta is the theme's `--ground` (two metas with
   `prefers-color-scheme` when the person follows the system); `format-detection` is off so iOS does not
   turn ISBNs and durations into phone links.
8. **Cursor stays as the handoff draws it in a browser.** The arrow cursor on controls is a desktop-shell
   rule and lands with M6 (below), not now.
9. **Budget.** The rules stay in the existing native-feel home (`utilities.css`, `motion.css`, `tokens.css`),
   not a new always-loaded file: a new file would be core in all but name, cost an extra request and compress
   worse. They add about 600 gzip bytes to the core. Proposed: core CSS 18.5 KB to **19.5 KB** and
   `utilities.css` 2 KB to **2.5 KB** (approved by the operator 2026-11-03; JS budget unchanged).

## Reserved for M6 (desktop shell) and native mobile, not built now
- `<html data-shell="desktop|ios|android">`, rendered by the server from a `ts_shell` cookie that the shell
  sets by opening `/?shell=<value>` once (whitelisted values; anything else is ignored). Server-rendered, so no
  flash and no script under the CSP. Every shell-only rule is scoped under `[data-shell]`.
- Under `[data-shell="desktop"]`: `cursor: default` on controls, rows and cards (links in text keep the hand);
  window-drag regions (`--wails-draggable: drag`, with `no-drag` on controls inside) only if the window uses a
  hidden title bar; Wails' default context menu hidden on chrome and kept on fields and content
  (`--default-contextmenu`, guess: check the property names against the pinned Wails v3).
- The manifest, `apple-mobile-web-app-*` metas and the status-bar style stay in M3; the top-inset work here is
  what makes a translucent status bar safe then.

## Rule text for AGENTS.md
Add under "Accessibility and layout":

> **Native feel ([decision 0011](docs/decisions/0011-native-feel.md)).** The app must not feel like a web page
> in a WebView. The document is the scroller; never `overflow: hidden` on `html` or `body`. `html` has
> `overscroll-behavior-y: none` and every element that scrolls on its own has `overscroll-behavior: contain`.
> Chrome (navigation, player, controls, list rows, cards, labels, images) is not selectable, has no touch
> callout and no drag; fields and readable content (descriptions, notes, errors, paths) stay selectable.
> Every interactive class has an `:active` state that appears on touch-down (`transition-duration: 0s`).
> Every fixed or sticky element at a screen edge clears `env(safe-area-inset-*)` on each edge it touches,
> landscape included. Text fields are at least 16 px. Never set `user-scalable=no` or `maximum-scale`.
> Focus rings use `:focus-visible`. Each theme sets `color-scheme`. Rules for the desktop or mobile shell
> only are scoped under `html[data-shell]`. Native-feel rules live in `utilities.css` (state layers in
> `motion.css`); `TestNative*` in `internal/web/static` checks them.
