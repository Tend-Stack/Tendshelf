# Accessibility

Target: WCAG 2.2 level AA in the `dark`, `light` and `sepia` themes; the `contrast` theme meets 7:1 for text
(the WCAG AAA 1.4.6 meaning). Plan: [plans/m3c-audit.md](plans/m3c-audit.md). Token changes:
[decision 0016](decisions/0016-contrast-tokens.md).

## Thresholds (never lowered to pass)
| Kind | AA themes | Contrast theme |
| --- | --- | --- |
| Text | 4.5:1 | 7:1 |
| Large text (24 px, or 18.66 px at weight 700+) | 3:1 | 4.5:1 |
| Non-text (control boundary, focus indicator, icon-only control) | 3:1 | 3:1 |
| Target size | 44 px at 390, 768 and 1024 px; 24 px (WCAG 2.5.8) at 1440 px | same |

Operator defaults applied (2026-11-04): the sizes above; page HTML is not compressed (BREACH: pages echo the
search query beside the CSRF token); colour changes move OKLCH lightness only.

## How it is tested
`make a11y` (needs `TENDSHELF_TEST_BROWSER`) drives Chromium through `internal/testutil/cdp` and its own
accessibility tree (`Accessibility.getFullAXTree`). It audits every registered full-page GET route (a route
without an audit entry or a written skip fails `make check`) at 390, 768, 1024 and 1440 px in four themes.
Checks: structure (landmarks, one h1, heading order, names and roles, labels, autocomplete, duplicate ids,
`aria-hidden` focus, zoom), text contrast per theme and width, target size, keyboard focus walk with real Tab
events and focus-ring contrast, non-text contrast, reflow at 320 px and 200 % text. Page scripts live in
`internal/web/testdata/a11y/`. Findings are data: `.local/tests/a11y/<UTC run>/findings.json`.
`TENDSHELF_A11Y_FAST=1` runs dark and contrast at 390 and 1440 only. `make page-weight` checks what a cold
load sends. Both run in the nightly workflow, not in `ci.yml` (no browser there).

Latest full run (2026-11-04, main 7b111a2): 75 pages, 3 states, 37042 text nodes measured; findings 0,
allowed 0, unmeasured 2378 (text over images or gradients that a flat colour read cannot judge).

## Allowlist
One line (RD4): `text-200-clip` on `#reader-ch-2`. The reader in Pages lays a chapter part out in CSS columns inside a box, so at 200 % text the later columns lie beyond the box until a page turn (or Scroll, which has no such box). An entry needs a written reason, and an entry that matches nothing in a full run fails the audit.

## Known limits
Not covered by the audit: forced-colors mode, placeholder contrast, PDF and ebook text, `/hx/` fragments not
reachable as a page state, pointer-gesture alternatives (2.5.1, 2.5.7), timing of live announcements, and
the 2378 unmeasured text nodes. These are manual:
[M3c keyboard and screen reader](manual-checks.md#m3c-keyboard-and-screen-reader).
Book tabs wrap at 768 px (a design change, operator approved 2026-11-04).
