Tendshelf is a self-hosted audiobook app for a household: one person runs the server, the family listens on phones, tablets, laptops and TVs. The design is **Walnut & Ember**: a study with a well-kept shelf, editorial serif titles, a ruler for time, and one ember of color. Covers carry the rest of the color; inside the player, the current cover lends its own accent.

## Voice and copy

Plain-spoken, warm and confident. Write for Rosa, who is 71 and has never used an audiobook app.

- Sentence case everywhere. Verbs on buttons: "Resume", "Start listening", "Invite someone", "Scan now", "Look up details online".
- Say what happened and what still works: "Couldn't reach your server. Downloaded books still play."
- No jargon in the listening UI: say "book", "chapter", "folder" and "invite", never "item", "track", "library root" or "token". Technical words live in admin pages, and even there with a plain sentence beside them.
- Numbers for time are tabular and specific: "18 min left in chapter", "6 h 40 m left in book", "−6:40:12".
- Where-you-are always reads: `Chapter 12 · 18 min left in chapter · 6 h 40 m left in book`.
- People are named: "Daniel invited you to listen to *The Lighthouse Keeper's Daughter* and 120 other books."
- No exclamation marks, no emoji, no "Oops". Celebrate with the covers flowing in, not with words.
- Privacy is stated plainly: "Tendshelf sends nothing about you anywhere. No telemetry."

## Color

Four themes: `dark` (default), `light`, `sepia` and `contrast` (high contrast, 7:1 text). Never hard-code a hex in a screen; use the tokens.

- Grounds: `ground` for the page, `surface` for cards and sheets, `raised` for anything floating or selected (mini-player, active segment, secondary buttons). `raised-hover` for hover.
- Text: `ink` for primary text, `ink-muted` for authors, narrators and labels, `ink-subtle` only for tertiary metadata at 13px or larger. All three meet 4.5:1 on `ground`, `surface` and `raised` in every theme.
- Rules: `line` for decorative hairlines; `line-strong` for anything that marks a control (inputs, toggles, chips, the scrubber ruler), at 3:1 or better.
- **Ember** is the brand voice in the chrome: `accent` for text and icons (links, active tab, overlines, focus), `accent-fill` for primary buttons with `on-accent` text. Never white text on ember. `accent-soft` for selected chips and the read-along sentence wash.
- **Status:** `success` is a teal, kept off the red–green axis; `danger` is a rose, kept clear of ember; `warning` is a gold. Each always comes with an icon and a word.
- **Material colors:** `brand-walnut` for the shelf plank, `brand-paper` for page edges behind covers. Use them for objects, never for UI text.

### The cover accent (inside the player only)

The player root sets two variables from the current cover: `--cover-accent` (a muted accent, chroma ≤ 0.10 in OKLCH; lightness 0.75–0.85 in the dark theme with charcoal `on-cover-accent`, 0.38–0.46 in light and sepia with white `on-cover-accent`, so the play button reads at 4.5:1 and the scrubber at 3:1 on its ground; white in the contrast theme) and `--cover-tint` (the cover's dominant color, at 70% fading out in dark, about 25% in light and sepia, none in contrast). Only the play/pause button, the scrubber fill, needle and current-chapter line, the "left in chapter" label and the background fade use them. The background fades from `cover-tint` at 70% to `ground` by 60% of the height. The mini-player, tabs and buttons stay ember, so each book feels like its own room while the walnut frame stays constant. With no usable cover color, both fall back to ember and `surface`.

## Type

- **Fraunces** (serif, optical sizes) for book titles, page titles, section headings, chapter names (`chapter`, always italic) and the wordmark. Styles: `display`, `title-1`, `title-2`, `title-3`, `section`, `chapter`.
- **Fira Sans** for everything else: `body-lg`, `body`, `button-lg`, `label`, `caption`, `meta`, `overline`. `meta` (13px) is the floor: nothing in the product is smaller.
- **Numbers:** `time-display`, `time`, `time-sm`, always with `font-variant-numeric: tabular-nums` (`ts-num`).
- Load both from Google Fonts: `family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,500;0,9..144,600;1,9..144,400&family=Fira+Sans:wght@400;500;600`. Self-hosting the same files is fine for an offline server.
- Respect Dynamic Type and browser zoom: sizes are tokens, layouts reflow, and nothing truncates the time left.

## Space and layout

8px grid (`space-8` … `space-64`, with 4, 12 and 20 as half steps).

| Breakpoint | Width | Navigation | Columns | Gutter | Player |
| --- | --- | --- | --- | --- | --- |
| Phone | 360–767 (design at 390) | Bottom tab bar, 72px | 4 | `space-20` | Full screen; mini-player above the tabs |
| Tablet | 768–1279 (design at 1024) | Side rail, 88px | 8 | `space-24` | Full screen or split; mini-player at the bottom of content |
| Desktop | 1280+ (design at 1440) | Sidebar, 264px | 12, max 1200px content | `space-32` | Right-side panel (400px) or two-column full player |

Cover grids: 3 columns on phone, 5–6 on tablet, 7–8 on desktop. Reading text and descriptions stop at `size-measure` (68ch).

## Shape and depth

- Books keep crisp corners: every cover and spine uses `radius-cover` (4px). Hero and player covers sit on `brand-paper` page edges (`ts-book-block`).
- Controls are soft: `radius-md` (12) for inputs, the mini-player and the player's settings bar; `radius-lg` (16) for cards, dialogs and sheet tops; `radius-pill` for buttons, chips, segmented controls and avatars.
- Shadows only where something floats or is a physical book: `shadow-cover`, `shadow-float` (mini-player, toasts), `shadow-sheet`. Everything else separates with `line` hairlines.
- Focus is one ring everywhere: `focus-ring`, a 2px `ground` gap then 2px of ember (white in contrast), following the control's radius.

## Motion

Quick and calm: `duration-fast` (150) for hovers and selections, `duration-base` (200) for sheets, toasts and HTMX settles, `duration-slow` (250) for the big transitions. Default easing `easing-standard`; arrivals `easing-enter`; departures `easing-exit`. Nothing bounces for long.

- **Mini-player → full player (250ms, `easing-expand`):** the cover is a shared element. It grows from 48px in the mini-player to the hero size, gaining page edges and `shadow-cover` as it lands. The background fades from `raised` to the cover-tint fade; title and controls fade and rise 12px in after the first 80ms. Swipe down reverses it, tracking the finger, and snaps back below 30% travel.
- **Page transitions:** HTMX swaps cross-fade content over `duration-base`, with a 4px rise for new content. Navigation, the mini-player and the audio element never move (`hx-preserve`).
- **Progress:** rings and bars ease to new values over `duration-ring` (600). The scrubber needle moves continuously while playing; time labels update once per second.
- **Read-along highlight:** the sentence wash cross-fades in `duration-highlight` (120); the word emphasis slides word to word with no jitter.
- **Hints:** "Rewound 5 s after your break" fades in over 150ms, holds 1.6s and fades out.
- **Reduced motion:** transitions become 150ms cross-fades, shared-element growth becomes a fade, shimmer and equalizer bars stop, and the read-along highlight snaps instead of sliding.

## Iconography

One in-house outline set on a 24px grid with a 1.75px round stroke, inlined as SVG so it takes `currentColor` (see the Icons card for every path). Active states fill. Icons never stand alone without an accessible name on their button. No emoji.

## Logo

The mark is a book spine whose sound runs off its edge as a fading wave (`assets/Logos`). Ember on dark, walnut on paper, cream on photography. The wordmark is "Tendshelf" in Fraunces 600.

## Accessibility

- WCAG 2.2 AA in every theme; the `contrast` theme meets 7:1 for text.
- 44px minimum targets (`size-tap`); the player's play button is 80px.
- Real elements: `<button>`, `<a>`, `<input type="range">` under the scrubber, `role="switch"` for toggles, `role="radiogroup"` for segmented controls and option sheets.
- Every icon-only control has an `aria-label` naming the action ("Back 30 seconds"). Live regions: toasts are `role="status"`; errors are `role="alert"`.
- Never color alone: selected chips get a check, statuses get an icon and a word, played chapters get a check.
- Keyboard on desktop: Space play/pause, ←/→ skip, B bookmark, / search, G then L library, G then H home. Focus is always visible.
- Large-text mode switches spines to grid cards and stacks the player's settings bar into two rows.

## Server-rendered partials (HTMX)

Each of these is its own templ component and swaps alone: the Continue listening hero, each Home row, the library grid (filters and sort target it), search results, a book's tab panels (About, Chapters, Bookmarks, Details), the compare sheet, download progress rows, scan progress, the users list, and toasts (out-of-band into `#toasts`). The audio element, mini-player and navigation sit outside the swapped region with `hx-preserve`; a small client module drives the scrubber, times and read-along highlight from the audio's `timeupdate`.

## Research lab

The admin-only Research area uses its own cooler tokens (`lab-ground`, `lab-surface`, `lab-line`, `lab-line-strong`, `lab-ink`, `lab-ink-soft`, `lab-muted`, `lab-accent`) and ignores the reading themes, so it never looks like the listening app. Its data marks are `lab-data` (selected clip, coverage ramp at 25/50/75/100%) and `lab-moment` (marked moments, always with a numbered flag); the pair passes colour-vision separation on `lab-surface`. Waveform bars use `lab-wave` and `lab-wave-dim`. Times, figures and file paths in the lab are set in Fira Mono; corners are 4–8px instead of the listening app's soft pills, and there are no covers' page edges, tints or shadows. Nothing from the lab appears in the listening UI. Every lab page carries its fair-practice note: owned files only, analysis only, protected files refused.

## Reader: read along and read

A book can hold an audiobook, an ebook, or both. When both are paired and aligned, the same book and the same player offer three modes that share one position: **Listen**, **Read along** (the voice plays and the sentence being spoken is highlighted) and **Read** (text only, the voice paused). Two ways to follow sit under the view setting: **Focus line** shows one sentence at a time, large and centred; **Eyes-free** turns the screen into three big controls for walking and driving.

- **Column:** reading text stops at `size-read-measure` (68ch), never narrower than `size-read-measure-min` (45ch) on tablet and desktop. Phone padding is 24px each side. Left-aligned, hyphenated, `text-wrap: pretty`, paragraphs indented 1.4em with no gap.
- **Type:** Fraunces for reading at `read-sm` 18/29, `read-md` 20/32 (default) or `read-lg` 22/36; Fira Sans is the alternative. Line spacing `leading-compact` 1.45, `leading-normal` 1.6, `leading-airy` 1.8. Focus line uses `read-focus` 24/36. Large-text mode adds 26px and turns on Airy.
- **Highlight:** `read-wash` behind the sentence, `read-word` (ember) 2px under the word. Three styles: Wash (default), Underline, and Brighten, which steps the rest back to `read-rest` (still 6:1 or better). The word underline is on by default in every style, so the highlight never relies on the wash alone.
- **Your marks:** highlights use `mark` with a 2px `mark-edge` underline; notes add an ember note dot; selection is a cool `selection` blue. Highlights, bookmarks and notes are one set for the book, whichever mode made them, and each shows both the audio time and the page.
- **Back to the voice:** when the sentence being spoken leaves the view, auto-follow stops and the pill appears above the dock. Tapping it (or F) scrolls back and follows again.
- **Player:** in read-along the player folds into the read-along dock; the play button and scrubber keep the cover accent, skip buttons move by sentence.
- **Motion:** the sentence wash cross-fades in `duration-highlight` (120ms) and the word underline moves word to word with no easing jitter. Auto-follow scrolls over `duration-follow` (400ms) to keep the current sentence in the top third. The pill fades and rises 8px in over `duration-pill` (200ms). Switching modes cross-fades the text in over `duration-base` while the cover shrinks into the dock. Reduced motion: the highlight snaps, auto-follow jumps instead of scrolling, the mode change is a plain fade.
- **Keys (desktop):** Space play/pause, J / K previous and next sentence, ← / → back and forward 30 s, H highlight the current sentence, N add a note, F back to the voice, R switch mode.

## Using this system

Load `tokens.css`, the Google Fonts link above, then `components/bundle.css`, and set `data-theme` on `<html>` (`dark`, `light`, `sepia` or `contrast`). Components are plain CSS classes prefixed `ts-` for server-rendered HTML; each card shows the markup. There is no JavaScript bundle; the player's small client module is the app's own.
