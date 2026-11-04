# 0016 Contrast tokens and the focus ring

Status: accepted (2026-11-04). Plan: `docs/plans/m3c-audit.md`, package B1. Amends the handoff tokens of
[decision 0002](0002-design-handoff.md) (`tokens.css`).

## Context
The M3c accessibility audit (`make a11y`) found colour failures against WCAG 2.2 AA (text 4.5:1, non-text 3:1)
and, in the contrast theme, 7:1 text. A value that moves keeps its hue and chroma and changes only OKLCH
lightness, to the nearest value that passes on every background it sits on.

## Token changes
| Token | Theme | Old | New | Ratio before -> after | Reason |
| --- | --- | --- | --- | --- | --- |
| `--line-strong` | light | `#8a7f70` | `#877c6d` | 2.99 -> 3.12 on `--raised`; 2.97 -> 3.09 on a tinted card | switch and check borders under 3:1 |
| `--line-strong` | sepia | `#7e6e58` | `#7b6b55` | 2.94 -> 3.07 on a tinted card | same |
| `--success` | sepia | `#1e6458` | `#1a6155` | 4.35 -> 4.55 on `--raised` | text token under 4.5:1 on the raised ground (found by `TestB1TokenContrast`) |
| `--avatar-c5` (new) | contrast | `#5a6b73` (was hard-coded in `bundle.css`) | `#3e4e56` | 4.57 -> 7.12 under `#f1e8da` | initials under 7:1 |

Other themes keep `.ts-avatar--c5` at `#5a6b73` (4.57:1, passes AA).

## Rule changes (no token moves)
- Focus ring kept where a marker is an inset `box-shadow`: the current side item, selected tabs, the checked mode
  item, the book page's home tab, the phone settings list and the mini-player play button over its halo now list the
  marker and `--focus-ring` together. Before, the marker replaced the ring, leaving only a transparent outline.
- The share choice cards draw the ring (with the transparent outline for forced colours); the radio inside draws
  none (`outline: none`), so the audit and the eye read one ring.
- A checked switch or check uses `--accent` for its border, not `--accent-fill`. In light and sepia the fill is
  2.7-2.97:1 on the card; `--accent` is the same hue, darker. In dark and contrast `--accent` equals
  `--accent-fill`, so nothing changes there.

## Not changed (needs the operator)
- `#st-speed` (Usual speed, Me > Settings): a select drawn with no border or fill, so it has no 3:1 boundary.
  Giving it a border is a design change; it is listed, not made.
- CSS budgets raised by 0.6 KB core, 128 B for `pages-book.css` and `player-bubble.css`, for the ring rules.
