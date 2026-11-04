# 0012 Offline pocket (M3a): install, downloads, offline playback and sync

Status: accepted (2026-11-03). Operator decisions, 2026-11-03: JS budget total 38.5 KB to 50.5 KB as
proposed (sw.js 3 KB, offline.js 3.25 KB, downloads.js 5.5 KB, player-full.js up to 12.75 KB); phone
checks over Tailscale HTTPS (`tailscale serve`, set up by the operator); sign-out wipes everything
(`Clear-Site-Data: "storage"` plus the in-page wipe); show the iOS storage note; keep "Remove finished
books" (off by default), defer "Wi-Fi only" and "Next in series"; drop "Other apps" from the storage
bar. Plan: `docs/plans/m3-offline.md`. Accepting it applies the AGENTS.md text in
"Rule for AGENTS.md" below and the `docs/architecture.md` "PWA and offline (P4)" rewrite (plan, package A).

## Operator's words
- "Continue roadmap!" (2026-11-03), which after M4 Details is M3 Pocket.
- M3 in `docs/roadmap.md`: "Manifest, service worker, install prompt, offline downloads with persistent storage,
  offline queue, Downloads tab and offline banner (`screens/browse/DownloadsPhone`, `screens/states/StateOffline`)."
  Exit: "a downloaded book plays in airplane mode on Android Chrome and iOS Safari (recorded manual check), and
  offline progress syncs on reconnect."

M3a is that offline core. M3b (later) is the rest of the M3 list: listening statistics, car mode, shake to
extend sleep, scheduled backups, the accessibility and performance audit, the two-column player.

## Context (inventory, 2026-11-03)
Nothing offline exists. Reused as they are: `GET /stream/{id}` (Range, ETag, `?compat=1` with 503 Retry-After),
`GET /hx/player/{id}` (the player's book data), `GET /api/v1/books/{id}` (files with `size`, `needsCompat`,
`playable`, and `permissions.download`), `POST /api/v1/progress/batch` (newest `clientUpdatedAt` wins,
finished sticky), `POST /api/v1/books/{id}/bookmarks` (idempotent by `clientId`), the player's `ts.pending`
localStorage queue, the toast host (`tsPlayer.toast`), the `share.DownloadBook` action and the grants'
`can_download` flag (in the admin UI since M2, enforced nowhere until now), the `cdp` test helper.
`docs/architecture.md` "PWA and offline" sketched "caches the app shell and visited pages" and an IndexedDB
queue; this decision replaces both (points 3 and 5).

## Decision
1. **Installable web app.** `GET /manifest.webmanifest` and `GET /sw.js` are public routes on the outer mux
   (beside `/static/` and `/healthz`, outside the gate and CSRF). PNG icons (192, 512, maskable 512, apple
   180) are rendered once from `img/tendshelf-app-icon.svg` by an opt-in Chromium test and committed (no
   image tool, no Node). Chromium's own install UI plus one "Install Tendshelf" button on Downloads when the
   browser offers `beforeinstallprompt`. No iOS add-to-home instructions: no board has them; Downloads shows
   one plain line about Safari clearing storage instead (point 8).
2. **Book bytes live in Cache Storage, in 4 MiB chunks.** Each file is fetched with Range requests and stored
   as chunk entries plus a completion entry written last. Reasons: `cache.put` streams to disk without
   holding a 500 MB file in memory (IndexedDB needs a Blob first, which Safari may hold in memory); a chunk is
   the resume unit after an interruption; the service worker answers any Range by streaming only the chunks it
   needs; a half-downloaded file is never served (no completion entry, no answer).
3. **Small records stay in localStorage**: the download index, the bookmark queue and the existing `ts.pending`
   progress queue. They are small, page-only (the worker never needs them) and synchronous; IndexedDB would add
   code and bytes for nothing. The worker learns what exists only from Cache Storage.
4. **One cache per person, wiped on sign-out and on a change of person.** `ts-person-<userID>` holds that
   person's chunks, player fragments, covers, peaks and offline shell; `ts-static-<build>` holds the hashed
   static files. Sign-out answers `Clear-Site-Data: "storage"` and redirects to `/login?signed_out=1`, where
   `offline.js` also deletes `ts-person-*` and the offline records (two mechanisms because Safari support for
   the header is a guess). A signed-in page whose person differs from the stored one wipes before showing any
   download. Session expiry alone keeps the downloads (the same person signs back in; anyone else triggers the
   wipe). Limit, stated: while offline nothing can check who holds the phone; downloads are as private as the
   device lock.
5. **No page HTML is cached except one offline shell.** `GET /offline` renders the StateOffline board for the
   signed-in person: greeting, the downloaded books (ids passed by the page) and recent books marked "Needs a
   connection". It is stored in the person's cache and answered for any navigation or boosted request that
   cannot reach the server. Reason: on a shared family device, cached pages would keep titles, shelves and
   admin screens of whoever browsed last; one shell built from the person's own downloads holds nothing else.
6. **Downloads run in the page, one file and one chunk at a time**, under a Web Lock so two tabs never fetch
   twice; they resume by chunk when the app opens or the connection returns. Reasons: one request at a time
   keeps a home server calm and progress honest; Background Fetch exists only in Chromium. The person is told
   to keep Tendshelf open. Downloading needs `share.DownloadBook` (`/stream/{id}?download=1`), so the
   "Can download" grant switch finally means something.
7. **The device's variant is downloaded**: the same test as the player (`needsCompat` and
   `canPlayType(mime) === ""` takes `?compat=1`), stored under the URL the player will ask for, so the player
   needs no offline branch.
8. **Honest storage.** `navigator.storage.persist()` at the first download; `estimate()` shows Tendshelf's use
   and the room left for it. The board's "Other apps" figure is not knowable on the web and is dropped. When
   persistence is refused a plain line says the browser may clear downloads; in Safari outside a Home Screen
   app a line says Safari may clear them after a week without visits (WebKit's 7-day rule).
9. **Settings on the board**: "Remove finished books" is kept (on-device, checked once a day against the
   person's progress). "Download on Wi-Fi only" is deferred: Safari and Firefox expose no connection type, so
   the switch could not keep its promise on iPhone. "Download the next book in a series" is deferred to M3b: it
   needs series lookups and quota rules for downloads the person did not start.
10. **Sync from the page, not the worker.** `offline.js` flushes `ts.pending` through the batch endpoint and
    queued bookmarks through the API with the page's CSRF token, on load, on `online` and with backoff
    (5 s, 15 s, 60 s, then 5 min). A "Back online" toast follows a successful flush after an offline spell.
11. **Worker updates never interrupt a book.** A new build installs and waits; the page tells it to take over
    only while the audio is paused (on load, on pause, after a navigation), then re-seeds the offline shell so
    its asset URLs match the new build. A request for an older hashed asset falls back to the current file of
    the same name.
12. **Not in a Wails window, not on share links, not without a secure context.** `offline.js` registers
    nothing when `html[data-shell]` is set, on `LinkShell` pages, or when `isSecureContext` is false (plain
    http on a LAN address); Downloads then says downloads need a secure (https) address.

## Rule for AGENTS.md (added under "Security and privacy" when this decision is accepted)
- The service worker (`/sw.js`, source `internal/web/static/js/sw.js`) answers only hashed `/static/` files and,
  from the signed-in person's own cache, `/stream/{id}` (with or without `?compat=1`), `/covers/{bookID}`,
  `/peaks/{id}`, `/hx/player/{bookID}` and the `/offline` shell for a navigation that cannot reach the server.
  It never caches or answers `/s/...`, `/invite/...`, `/login`, `/setup`, `/logout`, `/api/...`, any request
  with `download=1`, any non-GET request, or any other page. It is never registered inside a Wails window, on
  share-link pages or outside a secure context. Sign-out and a change of person delete that person's caches
  and offline records.

## Consequences
- JS grows by three files (`sw.js`, `offline.js`, `downloads.js`); budgets in the plan need the operator's
  approval. Only `offline.js` loads on every page; `downloads.js` is imported when needed.
- The manual airplane-mode check needs HTTPS on the phone (a service worker does not run on
  `http://<LAN address>`).
- Offline notes, offline read-along, listening time made offline (`listened_ms`) and Background Fetch are not
  in M3a.

## Notes (budget decisions recorded by package D, 2026-11-03)
The operator approved these after the builders measured the served (gzip) sizes; the tests carry the numbers
(`internal/web/static/motion_test.go`):
- JS total 52.5 KB (the proposal in the plan said 50.5 KB; `downloads.js` came out larger than estimated).
- `downloads.js` 8 KB (estimate 5.5 KB; measured 7.8 KB).
- Core CSS 19.75 KB (the offline banner; two `ts-offline` rules were added by package D within it).
- Observed in package D: Chromium completes a worker takeover only once the old worker has nothing in
  flight, so right after a book played from the worker the takeover can wait until the page is left (about 3
  runs in 5 in headless Chromium). The invariant still holds (a waiting worker never takes over while
  `audio` plays); only the moment of takeover after the pause is browser-dependent.
