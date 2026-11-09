# 0036 Pages are the default, and a page has a number

Date: 2026-11-09. Status: accepted when package A lands (plan: `docs/plans/reader-pages-flip.md`). Amends [0018](0018-book-reader.md) point 9.

## Context

Books have pages. Scroll at 1280 px and up left people with no sense of progress and nothing to mark. Page numbers were hidden because a layout number is not a place.

## Decision

1. Pages at every width; `auto` reads as pages; a person who chose Scroll keeps it. No-JavaScript pages stay scroll.
2. A page is bounded (30em of text, 1.6 tall to wide). Two facing pages when both fit at 24em, from 1024 px.
3. The reader shows a page number that fits this device and look. It is display only, measured per part and kept on the device. Unseen parts are estimated from characters and the words say "about". The saved place stays a sentence and offset; a number is never stored on the server. The book's printed label stays as a second fact.
4. A bookmark stays a sentence id. A page is marked when a bookmarked sentence ends on it, at any size.
5. A turn is Flip (default), Slide or None, in `reader.v1`; reduced motion is None. The moving page is drawn from small `aria-hidden` slices outside `#reader`, and the real text is at its destination from the first frame.

## Not handled, and why

Paginating a whole book ahead (a phone would fetch and lay out every part). Numbers shared between devices (they differ by screen). Running heads (a line of page for what the bar says).

## Built in package A (2026-11-09)

Points 1 to 3 and the page itself. Points 4 and 5 are packages C and B.

- `auto` is read as `pages` by `parseReader`; `Validate` still accepts a posted `auto`. The sheet offers Pages and Scroll.
- Below 1024 px a page is full bleed and the bar and dock lie over it (the page takes their height); the reader opens with them hidden and a turn hides them. With the read-aloud strip open the dock goes back into the flow. From 1024 px the page is a sheet on a tinted desk (`--desk`, plain in the contrast theme) and the bar and dock keep their room.
- The page height cap (1.6 times the width) applies on the desk only; below 1024 px the page is as tall as the screen allows, snapped to whole lines.
- Numbers: `reader-pages.js` records `{b, c, n}` (characters before, characters in, columns) for each part it lays out, in `localStorage["ts.offline.pagecount.<edition>"]`, under the current look; a different look empties it. `c` is the part's own character count (`data-part-chars`), so a part boundary adds exactly one number. A page holds the average of parts of three pages or more, else an estimate from the column and line size. The total says "about", rounded to ten from 100 up and kept steady within 2 %, until the parts laid out here cover the book.
- The scrollbar gutter: `html:has(.ts-readerpage) { scrollbar-gutter: auto }`, because a reserved gutter left a strip beside the fixed reader layer that no sheet's scrim covered.
