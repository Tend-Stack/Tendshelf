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
following). The other rows follow in "Second half" below.

## Second half (2026-11-15, "do the rest")

Measured in Chromium at 390, 768, 1024 and 1440 px, standard and large text (`TestFitRowsBrowser`), then fixed by the
lightest rule that keeps one line:

- **Fold into the three-dot menu** (the same script and `MoreMenu`, twins use the buttons' own words): the episode page
  and the Now playing panel's action row (Play stays; the played mark, star, Download and Save the file fold, last first,
  into the episode's own menu, which gets `data-ts-fit-menu`; with twins its Play next / Add to queue answer with a toast
  and do not replace the menu, so the twins stay); the pairing cards on Ebooks to pair (Make one book stays) and an
  edition's answers on the Text tab (Make this the default, Not the same book, Remove this edition).
- **Share one line, words cut or wrapped inside the button** (a pair needs no menu): Play latest and Follow on a show only
  opened; Play and Save on a station card (`ts-truncate`, the full word in `title`); Play and Save on a station search hit
  (beside the name on a wide row, under it on a phone, never stacked); Scan now and Add a folder on Admin libraries; Jump
  and Remove (and the two export links) on the Words page; the panel's Show and Episode page links; Download all and the
  three-dot button on a series page (they were a column; Download all keeps its own states and its progress words are cut
  with an ellipsis, it is not folded because it is a stateful widget).
- **Never wrap (guarded by the table test)**: the Home hero (Resume, Chapters), the author and narrator head (one button),
  a followed show's buttons (the first half).
- **Listed, not changed** (sections stacked by design or tools, not an action row of the page): the chapter editor's bars,
  the bookmarks page head, an admin user's added-by buttons and sign-in pages' stacked groups, duplicates merge and
  confirm pages (Confirm over Cancel at large text), the dev kitchen sink.

## Consequences

The three-dot menu can show the same words as a button that is hidden beside it; a test checks that every folded
button has exactly one visible twin and none shows twice. New words: none.
