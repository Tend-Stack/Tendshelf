# 0044 Action buttons that do not fit go into the three-dot menu

Date: 2026-11-15. Status: operator rule (2026-11-15).

## Context

On the desktop book page "Add to shelf" and "Mark as finished" each fell onto a row of their own when the window
was narrower than the row. A row of action buttons must never grow a second or third line to make room.

## Decision

A row of action buttons is one line. What does not fit moves into the row's three-dot menu, from the last button
backwards, as the window (or the room the page leaves, for example beside the Now playing panel) gets narrower, and
comes back as it gets wider. The play button and the three-dot button always show.

One mechanism, no second menu system:

- `internal/web/static/js/fit-row.js`, loaded through `data-ts-module` on the row (`data-ts-fit`). Each button that may
  fold carries `data-ts-fit-item="<key>"` (the Download button and its status link share one key). A row that is not a
  horizontal flex line itself (the book page at phone and tablet width) names the line to measure with
  `data-ts-fit-track`. The script measures the line with a `ResizeObserver` (the row, not the window), un-folds
  everything, and folds buttons from the end until the line fits. Folded buttons get `data-ts-fit-off`
  (display none, so they also leave the Tab order); `hidden` stays with the download script.
- `components.MoreMenu` takes `Fit []MoreMenuFitItem`: a hidden twin button per key, with the same icon and words,
  placed before the menu's own items in the row's order. The script shows the twin of each folded button; using it
  closes the menu and clicks the real control, so htmx, forms, the download script and sheets act as before. A menu
  that exists only for twins is drawn only while something is folded.
- Focus that was on a button that folds moves to the three-dot button.
- Without JavaScript the row wraps as it always did and every button stays reachable (`data-ts-fit-on` is what turns
  wrapping off).

Applied to: the book page (Read, Add to shelf, Download, Mark; when Read is the primary button the play button folds
last of these instead), the phone action bar (never a second row), and a followed show's buttons (Refresh, Stop
following). The series page header stacks the three-dot button and Download all in a column, never a row, so it is
unchanged. Other rows are scheduled separately (see the roadmap note).

## Consequences

The three-dot menu can show the same words as a button that is hidden beside it; a test checks that every folded
button has exactly one visible twin and none shows twice. New words: none.
