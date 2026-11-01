# Tendshelf: design handoff

This folder is everything needed to build the Tendshelf interface: the design system, every screen as a picture and as reference markup, and the written rules. Start here, then read `docs/HANDOFF.md`.

Tendshelf is a self-hosted, open-source audiobook app. One person runs the server at home and owns the books; their household listens on phones, tablets and computers, and can follow along in the text when a book also has an ebook. The look is called **Walnut & Ember**: a dark walnut study, serif titles, one ember colour, and each book's cover lending its colour to the player.

## Stack the design assumes

- **Go + templ + HTMX.** Pages are rendered on the server. Regions swap with HTMX; they are listed in `docs/HANDOFF.md` under "Server-rendered partials".
- **Plain CSS, no framework.** `design-system/tokens.css` (custom properties) and `design-system/components/bundle.css` (classes prefixed `ts-`). There is no JavaScript component library.
- **One small client module**, the only JavaScript: it drives the scrubber, times, progress saving, Media Session, the sleep timer and the read-along highlight from the `<audio>` element's `timeupdate`.

## What's in here

| Path | What it is | How to use it |
| --- | --- | --- |
| `docs/HANDOFF.md` | The developer handoff: grid per breakpoint, persistent elements, motion spec, HTMX partials, accessibility per component, copy deck, cover-accent rules, the reader | **The rules.** When a picture and this doc disagree, the doc wins. |
| `design-system/BRAND.md` | Brand book: voice, colour, type, layout, shape, motion, icons, accessibility, the Research lab, the reader | Read once before writing any CSS. |
| `design-system/tokens.json` | Every token, per theme (`dark`, `light`, `sepia`, `contrast`), with a usage note | Source of truth for values. |
| `design-system/tokens.css` | The same tokens as CSS custom properties, generated from `tokens.json` | Load first. Set `data-theme` on `<html>`. |
| `design-system/components/bundle.css` | All `ts-` component classes and their states | Load after `tokens.css`. |
| `design-system/components/<Name>/` | `README.md` (rules, roles, behaviour) and `preview.html` (markup for every state) for each of 36 components | Copy markup from the previews into templ components. Open a preview in a browser to see it. |
| `design-system/assets/logos/` | The mark in ember, cream and walnut, and the app icon (SVG) | |
| `screens/INDEX.md` | Every one of the 114 design boards, by area and device | Find the screen you're building. |
| `screens/<area>/<Name>.png` | What the screen should look like | Match it. |
| `screens/<area>/<Name>.html` | A static snapshot of that board's markup, using the real classes | Reference only, see below. |
| `docs/htmx-regions.png` | Diagram: what swaps, what stays, and the client module | |
| `docs/briefs/original-brief.txt` | The original product and design brief | Background on people and goals. |

## How to read the screen snapshots

The `.html` files are exported from the design canvas. They use the real tokens and `ts-` classes, the real copy and the real ARIA roles, so they're the fastest way to see the intended structure. But they are **not production code**:

- Layout spacing is often written as inline `style` attributes. Turn repeated patterns into templ components and CSS in `bundle.css` (or a page stylesheet), never inline styles in the app.
- Covers are drawn with placeholder art; in the app a cover is an `<img>` inside `.ts-cover`.
- Some screens show a state frozen in time (a sheet open, a toast showing, an element given `is-focus` or `is-hover`). `is-focus`, `is-hover`, `is-pressed` exist only so previews can show states; the app uses the real `:focus-visible`, `:hover` and `:active`.
- Every name, book, author and number is fictional demo data. Daniel owns the server, Maya is the everyday listener, Rosa (71) uses large text and the light theme, Inés is a pending invite.

## Build order that works

1. **Foundation.** Load `tokens.css` and `bundle.css`, the Google Fonts link from `BRAND.md`, and the theme switch on `<html>`. Build the three app shells: the phone tab bar (72px), the tablet rail (88px) and the desktop sidebar (264px), each with the persistent player outside the HTMX swap region (`hx-preserve`).
2. **Player and audio.** The `<audio>` element, the mini-player, the full player on each breakpoint, and the client module. Follow the "one resume" rule in HANDOFF.md: no screen shows two buttons that resume the same book.
3. **Core screens.** Home, Book detail, Player (`screens/core/`), in dark and light.
4. **Browse.** Library (grid, list, filters, empty, scanning), Search, Series, Author, Narrator, Bookmarks, Downloads (`screens/browse/`).
5. **People.** First-run setup, accepting an invite, Share & invite, Settings, Profile (`screens/people/`).
6. **Admin.** Users, Libraries and scan problems; the Research lab, which is off by default and admin only (`screens/admin/`).
7. **Edge states.** Loading, offline, server error, no results, player hints and the sleep timer (`screens/states/`).
8. **Reader.** Pairing an ebook, preparing read-along, Read along, Read, Focus line, Eyes-free, reading settings, contents, highlights and notes, and the reader error states (`screens/reader/`). Read the "Read-along reader" section of HANDOFF.md first.

`screens/directions/` holds the two early moodboards. Direction B was chosen and is what everything else uses; don't build from direction A.

## Rules that are easy to miss

- **Never hard-code a colour.** Use tokens. Every screen must work in all four themes, and the contrast theme must reach 7:1.
- **The cover accent** (`--cover-accent`, `--cover-tint`) is set per book on the player root only, and only the play button, scrubber, "left in chapter" label and background fade use it. Everything else stays ember. The sampling rules are in HANDOFF.md.
- **Copy is specified.** Use the words in the copy deck exactly: sentence case, verbs on buttons, no exclamation marks, no emoji, "book" and "chapter" never "item" or "track".
- **Accessibility:** 44px minimum targets, one focus ring (`--focus-ring`), real elements and roles (`role="switch"`, `role="radiogroup"`, `role="tablist"`), `aria-label` on every icon-only button, toasts as `role="status"` and errors as `role="alert"`, and never colour alone.
- **13px is the smallest text** anywhere in the product.
- **Reduced motion** replaces every movement with a short fade.

## Not in this package

- **TV.** It's decided that the TV app gets its own layouts and look, in the spirit of Apple TV and LG apps, not a scaled-up phone or web page. It hasn't been designed; don't improvise it from these screens.
- **The read-along brief itself.** Its requirements are covered by the "Read-along reader" section of HANDOFF.md and the `screens/reader/` boards.

## Open questions for the product owner

- Is the cover accent worked out when a book is scanned (assumed) or the first time it plays?
- Should the 10-second progress save be configurable for big households?

## Links

The handoff doc and design system also live online as private claude.ai pages. They're only reachable with the owner's access; everything they contain is copied into this folder.
