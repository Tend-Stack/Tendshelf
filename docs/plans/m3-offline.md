# M3a Offline pocket (plan, 2026-11-03)

Decision: [0012](../decisions/0012-offline-pocket.md) (read its points 1-12 first; they are not repeated). Boards:
`screens/browse/DownloadsPhone`, `screens/states/StateOffline`, the book page's "Download" item. Reused, not rebuilt:
`/stream` (Range, ETag, compat 503), `/hx/player/{id}`, `GET /api/v1/books/{id}` (the download manifest: `files[].id,
size, mime, needsCompat, playable`, `permissions.download`), `POST /api/v1/progress/batch`, `POST /api/v1/books/{id}/
bookmarks` (`clientId`), `ts.pending`, `tsPlayer.toast`, `share.DownloadBook`, `components.EmptyState`, `BookRow`,
`PlayButton`, `cdp`. Extended: `/stream` (`download=1`), `/logout`, Layout/Frame/Head, the Downloads page, the book
page actions. New: `/sw.js`, `/manifest.webmanifest`, `/offline`, `js/sw.js`, `js/offline.js`, `js/downloads.js`, icons.

## Invariants
1. The worker answers only what AGENTS.md (0012 "Rule") lists; everything else goes to the network untouched.
2. A file is served offline only after its completion entry exists; that entry is written after its last chunk.
3. One `ts-person-*` cache exists at a time and it is the signed-in person's; a change of person or a sign-out
   deletes it and `ts.offline`, `ts.offline.marks`, `ts.pending` before anything is shown or sent.
4. No playback is interrupted by a worker update; a waiting worker activates only while `audio.paused`.
5. Downloading requires `share.DownloadBook`; streaming still requires only `share.ListenBook`.
6. Offline saves are never lost by sync: a queued entry is deleted only after the server answered it, and only if
   it is unchanged since it was sent. The flush runs even where no worker can register.
7. Nothing registers in `html[data-shell]`, on `LinkShell` pages, or when `!isSecureContext`.
8. No inline script or style, no new Go module, no minifying; budgets below are proposals until approved.

## Data shapes
- Caches: `ts-static-<build>` (every Set asset except `*.txt` and `js/sw.js`, by hashed URL); `ts-person-<userID>`.
  Keys in the person cache: `/offline`; `/hx/player/<bookID>`; the exact cover URL from the fragment's
  `data-cover-url`; `/peaks/<fileID>` (best effort); per file, with `<k>` = `/stream/<fileID>` or
  `/stream/<fileID>?compat=1`: chunks `<k>?chunk=<n>` (or `&chunk=<n>`), body = bytes, header `Content-Type`; and the
  completion entry `<k>`: empty body, headers `Content-Type`, `X-Ts-Size`, `X-Ts-Chunk`, `X-Ts-Chunks`, `ETag`.
- `/sw.js` body = `self.TS_BUILD = {"build":"<12 hex>","assets":["/static/..."]};\n` + `js/sw.js` source. `build` =
  first 12 hex of SHA-256 over `opts.Version + "\n" + sorted hashed names joined by "\n"`.
- `localStorage["ts.offline"]` = `{"v":1,"books":{"<bookID>":{"title","author","cover","bytes","done","state":
  "waiting|downloading|paused|failed|ready","error":""|"space"|"gone"|"failed","addedAt","readyAt","finishedAt",
  "files":[{"id","key","mime","size","chunks","have","etag"}]}}}`. `ts.offline.person` = userID.
  `ts.offline.marks` = `[{"bookId","positionMs","clientId","at"}]` (max 200, oldest dropped).
  `ts.offline.prefs` = `{"removeFinished":false}`; `ts.offline.checkedAt` = ms.
- Manifest: `{"id":"/","name":"Tendshelf","short_name":"Tendshelf","start_url":"/","scope":"/","display":
  "standalone","background_color":<themeColor("dark")>,"theme_color":<same>,"launch_handler":{"client_mode":
  "focus-existing"},"icons":[192 any png, 512 any png, 512 maskable png, svg "any" "image/svg+xml"]}` (hashed URLs).
- DOM contracts. Layout `<html data-ts-person="<userID>" data-ts-chunk="<bytes>">`. Before `.ts-shell`:
  `<div id="ts-offline" class="ts-offline" role="status" hidden data-ts-downloads-src="<hashed js/downloads.js>"
  data-ts-msg-offline data-ts-msg-unreachable data-ts-msg-back><p class="ts-offline__text" data-ts-offline-text></p>
  <a class="ts-btn ts-btn--quiet ts-offline__retry" href="/" data-ts-offline-retry hidden>Try again</a></div>`.
  Shell root `<div class="ts-home ts-offline-home" data-ts-offline-shell>`. Book page: `<button type="button" class=
  "ts-btn ts-btn--quiet ts-book__download" data-ts-download="<id>" data-ts-msg-downloading="Downloading · {pct}%"
  data-ts-msg-waiting="Waiting" hidden>` + icon `download` + `<span class="ts-book__label">Download</span>`, and
  `<a class="ts-btn ts-btn--quiet ts-book__download" href="/me/downloads" data-ts-download-status hidden>` + icon +
  `<span class="ts-book__label" data-ts-download-label></span>`. Downloads root `[data-ts-downloads]` with every
  `data-ts-msg-*` string of the copy table, children `[data-ts-storage]` (`[data-ts-storage-free]`,
  `[data-ts-storage-used]`, `.ts-bar[data-ts-storage-bar] > .ts-bar__fill`), `[data-ts-persist-note]`,
  `[data-ts-safari-note]`, `[data-ts-insecure]`, `[data-ts-install]`, `[data-ts-keep-open]`, the settings section
  (`aria-label="Download settings"`, switch `data-ts-pref="removeFinished"`), `[data-ts-active]`
  (`[data-ts-active-count]`, `ul[data-ts-active-list]`), `[data-ts-ready]` (`[data-ts-ready-count]`,
  `[data-ts-ready-size]`, `ul[data-ts-ready-list]`), `[data-ts-empty]` (the EmptyState), and `<template
  data-ts-row-active>` / `<template data-ts-row-ready>` rows per the board with `[data-ts-cover]`, `[data-ts-title]`,
  `[data-ts-meta]`, `.ts-bar[data-ts-row-bar]`, buttons `data-ts-act="pause|resume|cancel|remove"`. All JS-only
  nodes start `hidden`; without JS the page is the empty state plus the board text.

## Routes (all GET answer HEAD; other methods 405 `Allow: GET, HEAD`)
| Route | Auth | Answer |
| --- | --- | --- |
| `GET /sw.js` | public, outer mux | 200 `text/javascript; charset=utf-8`, `Cache-Control: no-cache`, ETag `"sw-<build>"`, 304, gzip when accepted |
| `GET /manifest.webmanifest` | public, outer mux | 200 `application/manifest+json`, `Cache-Control: no-cache` |
| `GET /offline?books=a,b` | any signed-in role | the shell page (`Current: "home"`), `Cache-Control: private, no-store`; `books` 0-200 ids matching `^[0-9A-Za-z_-]{1,64}$`, else 400 error page; ids failing `ListenBook` or `streamable` are skipped silently |
| `GET /stream/{id}?download=1[&compat=1]`, same on `/api/v1/stream` | ListenBook then DownloadBook | as today with `Cache-Control: private, no-store`; no DownloadBook: 403 plain "Downloads of this book are turned off."; other `download` values 400 "Bad download value." |
| `POST /logout` | unchanged | adds `Clear-Site-Data: "storage"`; Location `/login?signed_out=1` |

## Worker (`js/sw.js`) lifecycle and routing
- install: `caches.open("ts-static-"+build).addAll(assets)`; no `skipWaiting`. activate: delete other `ts-static-*`,
  `clients.claim()`. message `{type:"activate"}`: `skipWaiting()`.
- fetch, same origin, GET or HEAD only; pass through (no `respondWith`) for paths starting `/s/`, `/invite/`, `/api/`,
  `/login`, `/setup`, `/logout`, `/healthz`, `/sw.js`, and any URL with `download=1`. Then:
  `/static/`: cache-first in `ts-static-*`; miss: network, else the current asset of the same name with the
  `.<10 hex>.` part stripped. `/stream/{id}` (+`?compat=1`): completion entry in the person cache: answer from
  chunks (below), else pass through. `/covers/`, `/peaks/`: cache-first when the exact URL is cached, else pass through.
  `/hx/player/{id}`: when cached: network-first, put fresh non-redirected 200s, network error: cached; else pass through.
  `/hx/player` (no id) is never cached. Navigations and `HX-Boosted: true`: network; on a network error or 502/504:
  the person cache's `/offline` with header `X-Ts-Offline: 1`; no shell: `Response.error()`.
- Ranges from chunks: parse `bytes=a-b`, `a-`, `-n`; multi-range or none: 200 whole file. `a >= size`: 416 with
  `Content-Range: bytes */size`. 206 headers `Content-Type`, `Content-Length`, `Content-Range`, `Accept-Ranges: bytes`,
  `Cache-Control: no-store`; body a `ReadableStream` whose `pull` reads one chunk at a time, slicing the first and last.
  HEAD: same headers, no body. The person cache is the one `caches.keys()` entry starting `ts-person-`.

## Download manager (`js/downloads.js`, exposes `window.tsDownloads = {start, pause, resume, cancel, remove, list,
debug: {failNextPut}}`, dispatches `ts:downloads:change` on `document` with `{bookId}`)
- start(bookId): `GET /api/v1/books/{id}`; refuse when `!permissions.download`. Files = `playable` only; key per file:
  `needsCompat && audio.canPlayType(mime) === ""` takes `?compat=1`. Cache `/hx/player/{id}` and its cover. Call
  `navigator.storage.persist()` once. Pre-check `estimate()`: `quota - usage < remaining + 16 MiB`: failed "space".
- Run under `navigator.locks.request("ts-downloads")` (absent: run anyway). Books in `addedAt` order, files in order,
  one chunk request at a time: `fetch(url + "download=1", {headers: {Range: "bytes=a-b", "If-Range": etag after the
  first chunk}, cache: "no-store", signal})`. Chunk size from `html[data-ts-chunk]` (4 MiB; tests 64 KiB).
  206 with `Content-Range` total = size: `cache.put`. 200: whole file when `a == 0 && size <= chunk`, otherwise the
  file changed: delete its chunks, restart once, then failed. 503: wait `Retry-After` (3 s), meta "Preparing a version
  this phone can play", give up after 10 min (failed). 403/404: failed "gone". 416: restart the file. Other 5xx:
  retry after 5/15/60 s, then paused. Network error: paused, resume on `online`. `QuotaExceededError` from `put`:
  delete the book's chunks, failed "space". After the last chunk: write the completion entry, `ready`, toast
  "{title} is ready offline.", reseed the shell.
- Resume on load and on `online` for `waiting`, `downloading`, `paused` books; recount `have` per file by matching
  chunk keys from 0. pause: abort, `paused`. cancel/remove: abort, delete completion entries first, then chunks,
  fragment, cover, peaks, the record; reseed. Rate is an EMA of the last chunks for "about {n} min" (minimum 1).
- reseedShell(): online only: `fetch("/offline?books=" + ready ids)`; a non-redirected 200 goes to `cache.put("/offline")`.
  Runs after ready/remove, 3 s after load when anything is ready, and on `controllerchange`.
- Remove finished: pref on, online, at most once per 24 h: `GET /api/v1/progress/{id}` per ready book; `finished`
  and `finishedAt < now - 7 days`: remove. Store `finishedAt` for the meta "finished {n} days ago".
- Sizes: decimal units, "291 MB" below 1000 MB, "1.6 GB" above.

## Page module (`js/offline.js`, every Layout and Frame page; never LinkShell)
1. Frame page with `signed_out=1` in the URL, or Layout person differs from `ts.offline.person` (absent: adopt without
   wiping): delete `ts-person-*`, `ts.offline*` keys, `ts.pending`, `ts.offline.marks`; then set the person. Also
   delete every `ts-person-*` other than the current one.
2. Flush (always, secure context or not): `ts.pending` as one batch (form body to `{bookId, positionMs,
   clientUpdatedAt, deviceId, speed, finished, force}`), then `ts.offline.marks` one by one (201, 200, 404, 409 drop;
   other answers stop). Header `X-CSRF-Token` read as player.js reads it. Drop an entry only if unchanged since sent.
   On load, on `online`, backoff 5 s, 15 s, 60 s, then every 5 min while visible. Successful flush after an offline
   spell: `tsPlayer.toast("hint", msg back)`.
3. Banner: show `msg offline` when `!navigator.onLine`; `msg unreachable` plus Try again (href = current URL) when the
   shell is in `#main`; on `online` with the shell showing, click the retry link (boosted, no reload).
4. Register `/sw.js` (scope `/`) unless invariant 7; activate a waiting worker per invariant 4 on registration, audio
   `pause`/`ended` and `htmx:after:swap`; `reg.update()` at most hourly on navigation.
5. Import `downloads.js` (from `data-ts-downloads-src`) when `[data-ts-downloads]` or `[data-ts-download]` is on the
   page or `ts.offline` has a non-ready book; re-check after `htmx:after:swap`.
- player.js: remove `flushPending` and its call; after `parseBook` in `fetchBook` (not LINK), when `ts.pending[id]` has
  `client_updated_at` newer than `stored.updatedAt`, use its position and finished. player-full.js: when the bookmark
  fetch throws (network error only), push `{bookId, positionMs, clientId: crypto.randomUUID(), at}` to
  `ts.offline.marks` and toast "Bookmarked at {time}. It saves when you're back online." (format with the file's
  existing time formatter; none: `PLAN GAP`).

## Packages (each ≤ 250 tool calls; scoped gates per AGENTS.md; `make check` and `make build` before commit)
- **A, server (Go, templ).** `server.go` (outer routes, `/offline` registration, chunk option default 4 MiB, build id),
  `routes_offline.go` (new: sw, manifest, offline handlers), `offline_copy.go` (new), `pages/offline.templ` (new, the
  StateOffline board with `BookRow`/`PlayButton`; ready rows "Ready offline · Chapter {n}" / "· {pct}%" (no chapters) /
  plain; then up to 5 recent unfinished books not downloaded, "Needs a connection", no play control; ready first,
  each group by last played), `components/layout.templ` + `frame.templ` + LinkShell (`Page.NoInstall`; manifest,
  `apple-touch-icon`, `apple-mobile-web-app-title`; html attrs; `#ts-offline`; `offline.js` script), `static/static.go`
  (`.png` type), `static/img/icon-192.png`, `icon-512.png`, `icon-maskable-512.png`, `apple-touch-icon.png` +
  `static/icons_render_test.go` (opt-in `TENDSHELF_RENDER_ICONS=1`, Chromium, transparent background for "any";
  maskable = full-bleed `#E2703A`, glyph in the central 80 %) + a always-on size test, `routes_stream.go`,
  `routes_auth.go`, `routes_matrix_test.go` rows, `docs/api.md`, `docs/architecture.md` (PWA section, routes),
  AGENTS.md rule (only after the operator accepts 0012). Tests: `offline_routes_test.go`.
- **C, pages (templ, CSS; parallel with A, no shared file).** `pages/downloads.templ`, `routes_downloads.go`,
  `downloads_copy.go` (new), `downloads_test.go`, `pages/book.templ` + `book_views.go` (Download pair only when
  `DownloadBook` and at least one playable file) + `book_test.go`, CSS `pages-me.css` (Downloads), `pages-home.css`
  (shell), `bundle.css` (`.ts-offline` banner: under the top safe area, `--radius-control`, every theme).
- **B1, worker and page module (after A and C merge).** `js/sw.js`, `js/offline.js`, `js/player.js`,
  `js/player-full.js`, `static/motion_test.go` (all budget lines, including `downloads.js`), browser tests T1, T3, T5, T6.
- **B2, downloads (after B1).** `js/downloads.js`, browser tests T2, T4, T7, T8.
- **D, proof (after B2).** T9, captures, `docs/manual-checks.md`, `docs/copy-additions.md`, `docs/roadmap.md`.

## Acceptance tests
Go (A, C): `TestServiceWorkerScript` (anonymous 200, headers, `TS_BUILD` lists every hashed asset but `*.txt` and
`js/sw.js`, build differs for two Sets, 304, 405, CSP header, no `Set-Cookie`); `TestManifest` (exact JSON, icon URLs
200 `image/png`, PNG sizes 192/512/512/180); `TestHeadInstallLinks` (Layout, Frame yes; LinkShell no);
`TestOfflineShell` (each role 200 and headers; forbidden, refused and unknown ids render nothing and no title; 201
ids 400; bad id 400; anonymous to login; a TestGrantLeaks row); `TestStreamDownload` (owner 206; member grant
`can_download=0` 403 body exact, `=1` 206; `download=2` 400; `compat=1&download=1` 503 while preparing; `/api/v1/stream`
same); `TestLogoutClearsSiteData`; `TestDownloadsPage` (board words, new empty copy, one switch, no Wi-Fi or series
rows, every `data-ts-msg-*`, templates, nav current); `TestBookDownloadButton` (owner yes, granted without download no,
refused book no). Budgets: `TestJSBudget` with the new lines.
Browser (`TENDSHELF_TEST_BROWSER`, file `offline_browser_test.go`, fixture: two synthetic files ≥ 3 chunks each at
64 KiB; "down" = a handler switch that hijacks and closes the connection, plus `Network.emulateNetworkConditions
{offline:true}` on the tab): T1 worker controls the page and `ts-static-<build>` holds every asset. T2 Download from the
book page, Downloads shows "Ready offline", 0 partial chunks. T3 down: boosted Library click shows the shell and the
banner; play from "On this phone"; `currentTime` advances; seek into the middle of file 2; `audio.error` null.
T4 pause after one chunk, reload, resume: byte count equals the sizes; cancel leaves no key of that book. T5 offline
play 5 s, pause, bookmark; up: "Back online" toast, DB position within 1.5 s of the client, one bookmark; a second
reconnect adds nothing. T6 sign out: no `ts-person-*`, no `ts.offline`; another person signs in: empty Downloads.
T7 `debug.failNextPut = "quota"`: failed row with the space copy, no chunks left. T8 compat file (`canPlayType` stubbed
empty): stored under `?compat=1`, 503 first answer waited out. T9 (D) worker update: change the build while playing,
no takeover; pause: `controllerchange`, the shell is re-seeded; on `/s/{token}` with the server down the browser's error
page shows, not the shell. Captures (D): Downloads (seeded index) and the shell at 390, 768, 1024, 1440 beside the
boards, every theme at 390.

## Manual check (D writes it into `docs/manual-checks.md`; needs HTTPS to the phone)
Android Chrome and iOS Safari (Home Screen app and a Safari tab): 1 install; 2 download a multi-file book (check the
progress row, pause, resume); 3 Downloads shows the storage numbers and the persist line state; 4 airplane mode; open the
app cold: the shell, the banner, the book in "On this phone"; 5 play, seek across a file, lock the phone 2 min, unlock;
6 bookmark, pause; 7 airplane off: "Back online", the place and bookmark on another device; 8 sign out: Downloads empty
after the next sign-in. Record the `estimate()` quota and whether `persist()` was granted (iOS figures are guesses now).

## Copy
Board and deck words: Downloads · Books saved here play without a connection. · Storage on this phone · {size} free ·
Tendshelf · {size} · Remove finished books · Frees space; your progress stays. · After 7 days · Downloading · {n} ·
{done} of {total} · about {n} min · Waiting · {size} · Ready offline · {n} · Ready offline · {size} · finished {n} days
ago · Pause download · Cancel download · Remove download of {title} · Downloading {title} · No downloads yet · Download
a book to listen without a connection. · Download · the offline banner and "Couldn't reach your server…" lines · Try
again · Welcome back, {name} · Showing what's on this phone. Everything else comes back when you're online. · On this
phone · Ready offline · Chapter {n} · Needs a connection · Bookmarks and progress you make now sync the moment you're back.
New (BRAND voice, D lists them in `copy-additions.md`): Resume download · Paused · {done} of {total} · Waiting for a
connection · {done} of {total} · Preparing a version this phone can play · Keep Tendshelf open until downloads finish. ·
Downloading · {pct}% · There isn't room on this phone for {title}. Remove a download, then try again. · {title} is no
longer available to download. · {title} didn't download. Try again. · {title} is ready offline. · This browser may clear
downloads when the phone runs low on space. · Safari may clear downloads after a week without a visit. Add Tendshelf to
your Home Screen to keep them. · Downloads need a secure (https) address. Ask the person who runs Tendshelf to set one
up. · Install Tendshelf · Back online. Your place and bookmarks are saved. · Bookmarked at {time}. It saves when you're
back online. · Tendshelf uses {used}; {free} free (storage bar label) · Downloads of this book are turned off.

## JS budgets (gzip served, proposal for the operator; estimates, builders report measured bytes)
`sw.js` 3 KB (raw 24 KB) · `offline.js` 3.25 KB (raw 24 KB) · `downloads.js` 5.5 KB (raw 40 KB) · `player.js` stays
12.75 KB (flush moves out, newer-pending check comes in) · `player-full.js` 12.5 → 12.75 KB · total 38.5 → 50.5 KB.
Per page load only `offline.js` is new; `downloads.js` loads on demand; `sw.js` once per build.

## Out of scope
Listening statistics, car mode, shake to extend sleep, scheduled backups, the accessibility audit, the two-column player,
Wails (and any offline in a Wails window), Wi-Fi only, next in series, offline notes and the note sheet, offline
read-along, `listened_ms` for offline listening, Background Fetch, "Other apps" storage, the server-unreachable
"Details to show {admin}" expander, iOS add-to-home instructions, push notifications.
