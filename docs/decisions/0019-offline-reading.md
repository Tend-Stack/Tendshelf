# 0019 Offline reading: a downloaded book opens with no connection

Date: 2026-11-04. Status: proposed; the budgets and point 2 wait for the operator. Plan: [m5-great-reader.md](../plans/m5-great-reader.md),
packages G6a and G6b. Amends [0012](0012-offline-pocket.md) points 3, 5 and 10 and its rule for `AGENTS.md`. Builds on
[0018](0018-book-reader.md).

## Context (inventory, 2026-11-04)

The audio model works and is reused: `ts-person-<userID>` per person, `ts-static-<build>`, the `ts.offline` index, the Web Lock
queue in `downloads.js`, `reseed()`, the wipe in `offline.js` (`ts-person-*` plus every `ts.offline*` key), the page-side flush with
backoff, the `/offline` shell. Fragment and image addresses already carry `edition=` (`rsFragURL`, `rsImage`), so a stored copy
can never mix two editions. What is missing: nothing stores text; `pages-reader.css` is out of the precache; `/offline` skips a book
with no playable file; a failed reading save waits in `sessionStorage`, which dies with the tab and is not wiped; and
`navigator.sendBeacon` answers true while the request is lost. On a phone in airplane mode with Tailscale up, requests hang and
`navigator.onLine` stays true (`sw.js`, `PAGE_DEADLINE_MS`), so the page-hide beacon is exactly the save that vanishes.

## Decision

1. **One Download for a book.** The book page's Download keeps both formats the person may have: audio files as today, plus the
   text of the edition the person reads. A text book has only the second. Text is small, so there is no separate choice.
2. **Offline text needs the same right as a file download:** `share.ReadText` and `share.DownloadBook` together, the rule
   `routes_ebooks_download.go` already applies to the ebook file. The grant's "Can download" keeps one meaning. An admin who wants a
   person reading online only leaves it off.
3. **Text lives in the person's cache, stored under the exact addresses the reader asks for:** every chapter part
   (`/hx/books/{id}/read/chapters/{idx}?edition=...`, `&part=` past the first), every EPUB picture
   (`/books/{id}/read/images/{blockID}?edition=...`), plus the book's reader shell, written last. No shell, no offline book:
   a half-stored book is never opened.
4. **The reader shell is the one page besides `/offline` that is stored**, and this amends 0012 point 5. One per downloaded book,
   rendered for that person by `GET /offline/books/{id}/read`, kept under `/books/{id}/read`; a second copy with the contents
   sheet (`?sheet=contents`) kept under `/books/{id}/read?sheet=contents`. It holds the frame of the reader, the book's title and
   contents, the person's look and last place: nothing that is not already in that person's download. Reason for keeping the
   frame as HTML rather than drawing it in script: the same templ page, the same words, no second renderer.
5. **The shell holds no chapter text.** `reader-offline.js` picks the place, fetches the part through the worker, puts it in the page
   and only then mounts `reader.js`, so `reader.js` and `reader-pages.js` have no offline branch.
6. **The worker answers text network-first with a 3 s limit, and keeps a part it has stored fresh**, the way it treats
   `/hx/player/{id}`. It stores nothing the download did not store first. So highlights made online reach the stored copy as they are
   read; the daily check catches the rest through a `revision`. Pictures are cache-first (they cannot change inside an edition).
7. **Reading places have their own queue**, never `ts.pending`, which is audio only: `localStorage["ts.offline.reading"]`, one record per
   book (`form`, `chars`, `active`, `sent`). This amends 0012 point 3 by adding a record; the name is under the `ts.offline` prefix, so the
   wipe already removes it. The place is written **before** any send, because a beacon gives no answer. `offline.js` flushes it to
   `POST /reading/{bookID}` with the page's CSRF token, after the audio queues, on load, on `online` and with the same backoff. It
   replaces the `sessionStorage` save in `reader.js`. A sent record stays only for a downloaded book, as its last local place.
8. **Characters and time are counted once.** A page-hide beacon carries that save's characters and the queue keeps only its place, so a
   resend adds no time; a failed `fetch` moves its characters and time into the record, and the flush sends them in slices within
   the server's per-save caps (200,000 characters, 10 minutes). Reading time is dated by the save's `client_updated_at`, clamped to the
   last 30 days and never ahead of the server, so a flight counts on the day it happened.
9. **Offline is for reading.** Turning pages, contents, chapter keys, links, the place, the time left and copying work. Highlights, notes,
   bookmarks, search, the reading settings sheet and "Mark as finished" need the server. The shell does not draw them; on a page
   that was already open when the connection went, a plain toast says so. A footnote opens by its plain link, which the shell can serve.
10. **A daily check keeps downloads honest**, inside the existing `checkFinished`: `GET /offline/books/{id}/manifest` for each book with
    text. A new `revision` (the edition, the person's highlights and bookmarks) stores everything again and the shell last; a 404 means
    the right is gone or the book is, and the text is removed. A redirect or a network failure changes nothing.
11. `pages-reader.css` returns to the precache, so `Set.Precache()` has no exceptions left but the worker itself and the notices.

## Rule for AGENTS.md (replaces the service-worker bullet under "Security and privacy"; package G6a applies it)

- The service worker (`/sw.js`, source `internal/web/static/js/sw.js`) answers only hashed `/static/` files and, from the signed-in
  person's own cache, `/stream/{id}` (with or without `?compat=1`), `/covers/{bookID}`, `/peaks/{id}`, `/hx/player/{bookID}`,
  `/hx/books/{id}/read/chapters/{idx}`, `/books/{id}/read/images/{blockID}`, the `/offline` shell for a navigation that cannot reach
  the server, and the book's reader shell for a navigation to `/books/{id}/read` (or `?sheet=contents`) that cannot reach it. It
  never caches or answers `/s/...`, `/invite/...`, `/login`, `/setup`, `/logout`, `/api/...`, `/reading/...`, any request with
  `download=1`, any non-GET request, or any other page. It is never registered inside a Wails window, on share-link pages or
  outside a secure context. Sign-out and a change of person delete that person's caches and offline records (decisions
  [0012](docs/decisions/0012-offline-pocket.md), [0019](docs/decisions/0019-offline-reading.md)).

## Not handled, and why

- **Highlights, notes and bookmarks made offline.** Each needs its own queue, ids and merge with the server's copy. Separate work if it is
  wanted; the place is what must not be lost.
- **Settings changes offline.** The sheet is a server form. The shell draws the look stored with it, refreshed each time the book opens online.
- **Offline search.** It runs over the document on the server; a client index is a larger feature.
- **Share-link readers.** There is none (0018).
- **A save made offline against an edition the server has since replaced** answers 422 and its place is dropped; the book opens at the
  server's place. It needs a re-read to finish while the person is offline with unsent reading: rare, and the stored text is then
  stale anyway.
- **Several offline days in one batch** count on the day of the last save, because one record holds the book's place.
- **Revoked access while offline.** As for audio, nothing can check who holds the phone (0012 point 4); the next daily check removes the text.

## Consequences

- JS grows by two files (`reader-offline.js`, `downloads-text.js`); the numbers are in the plan and need approval.
- A manual airplane-mode check is recorded in the roadmap (the operator's installed phone app, Tailscale HTTPS, as for 0012).
