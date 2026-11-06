# Tendshelf JSON API v1

The reference for native and mobile clients (hand-written, no generator). Everything lives under `/api/v1`.
The read side is documented here (P3-9a); the sync side (progress, bookmarks) is added with P3-9b.

## Authentication

- **Device token** (apps): `POST /api/v1/auth/token` with JSON `{"username","password","name","kind"}`
  (`kind` is `web`, `pwa`, `desktop` or `mobile`; body at most 4 KiB, `Content-Type: application/json`).
  Answers `201 {"token","device","user"}`. The token is shown once; send it as `Authorization: Bearer <token>`.
  `DELETE /api/v1/auth/token` signs the calling device out (204). A token works only under `/api/v1/`, is checked
  in the database on every request, and ends when the device is signed out, the person is disabled or the
  password changes. `GET /api/v1/me` answers `{"user","device"}`.
- **Sign-in with a code** (the phone app, [decision 0026](decisions/0026-app-sign-in.md)): the app opens the server's
  `/app/connect?challenge=` page inside its window; after the person signs in and taps Connect, the window takes a one-time
  code from the redirect. `POST /api/v1/auth/token` with `{"code","verifier","name","kind":"mobile"}` (optionally with a bearer
  token) trades it. The challenge is base64url (no padding) of the SHA-256 of the verifier; the verifier is 32 random bytes in
  base64url, 43 characters (PKCE S256). A code lives 2 minutes, works once, and any wrong try burns it.

  | Case | Answer |
  | --- | --- |
  | Good code, no bearer | `201 {"token","device","user"}`; the new window session is bound to the new device |
  | Good code, bearer of the same person, kind `mobile` | `200 {"device","user"}` (no `token`): the session is bound to that device |
  | `code` together with `username` or `password` | `400 invalid`, field `code` |
  | `kind` is not `mobile` | `400 invalid`, field `kind` |
  | Unknown, used, expired, wrong verifier, malformed, session gone, account disabled | `401 unauthorized`, always "This sign-in has expired. Start again from the app."; counts against the address |
  | Bearer of another person, or not a `mobile` device | `409 conflict`; the code is burned |
  | Dead bearer | `401` from the bearer check; the code is not used |
  | The person already holds 50 apps | `409 conflict` (a reconnect still works) |
  | 10 failures in 15 minutes from one address | `429 rate_limited` with `Retry-After`, before any lookup |

  Ending the device (sign-out, admin revoke, password change, disabling the person, `DELETE /api/v1/auth/token`) also ends the
  bound window session on its next request. A session that merely expires leaves the token alive.
- **Browser session**: the `ts_session` cookie plus the CSRF token on unsafe methods. GET needs no token.
- A request with both a bearer token and a session cookie is refused: `400 invalid`.
- No credential: `401 unauthorized` (JSON, `WWW-Authenticate: Bearer`), never a redirect.

## Conventions

- Success bodies are the resource itself. Times are Unix milliseconds UTC (`...At`), durations and positions
  are milliseconds (`...Ms`), ids are text, names are camelCase. Arrays are `[]` when empty, never `null`; an
  absent single object is `null`.
- Clients must ignore fields they do not know. New fields are not a version change; `/api/v1` changes only for
  breaking changes. JSON request bodies tolerate unknown fields.
- Lists are `{"items":[...],"next":string|null,"total":int}`. `next` is an opaque cursor: pass it back as
  `?cursor=`. `total` is present where the server counts it. A cursor that was altered or belongs to another sort
  answers `400 invalid` with `field: "cursor"`; start again from the first page.
- Errors: `{"error":{"code","message","field"?}}`. `message` is plain words; `field` names the query parameter or
  body key at fault.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid` | Bad parameter (`field` says which), mixed credentials, or a method the address does not take on a media alias (405 uses the same code). |
| 401 | `unauthorized` | No or unknown credential. |
| 403 | `forbidden` | Cross-site request refused (CSRF). |
| 404 | `not_found` | Unknown, hidden or unreachable. A book the person may not see answers exactly as one that does not exist. A wrong method on any other `/api/` address is also 404. |
| 405 | `invalid` | Wrong method on `/api/v1/covers`, `stream`, `peaks` (`Allow: GET, HEAD`). |
| 409 | `conflict` | Device limit reached on sign-in. |
| 413 | `too_large` | Body too large. |
| 415 | `unsupported` | Not JSON. |
| 429 | `rate_limited` | See rate limit; `Retry-After` in seconds. |
| 503 | `unavailable` | Server fault, or a media copy still being made (`Retry-After`). |

- **Rate limit**: bearer requests, 600 per minute per device (every request counts). Browser sessions are not
  limited.
- **Caching**: `GET /libraries`, `GET /books/{id}` and `GET /shelves` carry `ETag` (a hash of the body),
  `Cache-Control: private, no-cache`, `Vary: Authorization, Cookie`, and answer `304` to a matching
  `If-None-Match`. All other answers are `Cache-Control: no-store`.
- **Merged ids** (S4): when two copies of one audiobook are merged, the dropped copy's id keeps answering.
  `GET /api/v1/books/{id}` answers `308` with `Location: /api/v1/books/{keptId}`, but only when the person may
  see the kept book; otherwise it is the usual 404. A progress save (`PUT /progress/{bookID}`, an item of
  `POST /progress/batch`) or a bookmark (`POST /books/{id}/bookmarks`) sent to the old id is stored on the kept
  book, under the kept book's right, so an offline outbox loses nothing. A batch result names the id the client
  sent. Additive: a client that never meets a merged id sees no change.
- **No CORS**: there are no `Access-Control-*` headers. The web app is same-origin; native and desktop clients
  call from native HTTP code and do not need them. Adding them would only widen what a hostile page could read.
- **Visibility**: every endpoint answers for the signed-in person only. Hidden books never appear in a list, a
  count, a search result, a cover, a stream or a peaks file.

## Shapes

`BookCard`: `{"id","libraryId","title","subtitle","authors":[{"id","name"}],"narrators":[...],"series":{"id","name","sequence"}|null,"durationMs","addedAt","publishedYear":int|null,"cover":{"url","width","height","tint"}|null}`.
`cover.url` is `/api/v1/covers/{bookId}?v=<hash>`; append `&w=<px>` for a thumbnail (`url` is empty when the cover
has no image). Refused or missing books are not listed.

## Endpoints

### GET /api/v1/libraries
The libraries the person may see: `{"items":[{"id","name","books"}]}` (`books` counts what the person may open). ETag.

### GET /api/v1/books and GET /api/v1/libraries/{id}/books
A page of `BookCard` with the Library page's filters, as a `List` with `total`.
Query: `sort` (`added` newest first, the default; `title`, `author`, `duration`), `genre` (repeat, up to 5), `author`,
`narrator`, `series` (ids), `length` (`short`, `medium`, `long`), `title` (contains), `show` (`unfinished`,
`not_started`, `finished`, `shared`), `cursor`, `limit` (1..120, default 48). An unknown filter value is ignored,
as on the page. Errors: 400 `limit` or `cursor`; 404 for a library the person cannot see.

### GET /api/v1/books/{id}
`{"book":BookCard,"description","publisher","language","abridged","explicit","series":[...],"genres":[],"tags":[],"chapters":[{"idx","startMs","endMs","title"}],"files":[{"id","idx","offsetMs","durationMs","size","mime","needsCompat","playable","streamUrl","peaksUrl"}],"parts","missingParts","refused","missing","permissions":{"listen","download"},"progress":{...}|null}`.
`files` and `progress` are empty/null unless the person may listen and the book is neither refused nor missing.
`progress` is `{"bookId","positionMs","durationMs","finished","finishedAt","clientUpdatedAt","serverUpdatedAt","deviceId","deviceLabel"}`.
`speed` is the person's own speed for this book, else their usual speed.
`streamUrl` is `/api/v1/stream/{fileId}` (supports `Range`; `?compat=1` asks for a browser-friendly copy and
answers 503 with `Retry-After` while it is made); `peaksUrl` is `/api/v1/peaks/{fileId}`. ETag. Errors: 404.

### GET /api/v1/search
Query `q`, `limit` (1..200, default 20). `{"books":[BookCard],"total","suggestion":string|null,"people":[{"id","name","role":"author"|"narrator"}],"series":[{"id","name"}]}`. A blank `q` answers 200 with empty arrays.

### GET /api/v1/shelves
`{"own":[Shelf],"shared":[Shelf]}` with `Shelf` = `{"id","name","ownerName","shared","books","createdAt","updatedAt"}`;
`books` counts the books the person may open. `shared` holds other people's shared shelves with at least one such
book. Guests have no shelves: 404. ETag.

### GET /api/v1/shelves/{id}
`{"shelf":Shelf,"books":List<BookCard>}`; query `cursor`. 404 for a shelf the person may not read; 400 `cursor`.

### GET /api/v1/shared
Books shared with the person. Query `limit` (1..50, default 24). `{"items":[{"book":BookCard,"sharedAt","sharerName"}],"total","sharers":[string]}`.

### GET /api/v1/covers/{bookID}, /api/v1/stream/{audioFileID}, /api/v1/peaks/{audioFileID}
The page routes `/covers`, `/stream` and `/peaks` under the one prefix where a token works; same handlers, same
parameters (`w` for covers, `compat` for stream), same visibility. Errors are JSON. Other methods: 405 with
`Allow: GET, HEAD`.

`GET /api/v1/stream/{audioFileID}?download=1` (also `/stream/...`) marks a request that saves a copy for offline
listening. It needs `book.download` on top of `book.listen` (a grant with `can_download` off answers 403, the page
route with the plain text "Downloads of this book are turned off."), answers as without the parameter and adds
`Cache-Control: private, no-store`. Any other value of `download` is 400. Clients that keep audio themselves use
`GET /api/v1/books/{id}` (`files[].size`, `needsCompat`, `playable`, `permissions.download`) as the manifest and
fetch each file in `Range` chunks with `If-Range` set to the first answer's ETag.

## Sync endpoints (progress and bookmarks)

A person's own rows only. Every row is filtered to books the person may list, so a book that is hidden, unknown,
refused or missing never shows and answers the same 404. Times are Unix milliseconds. Unsafe requests take a JSON
body (`Content-Type: application/json`, else 415; too large 413; unreadable 400); unknown fields are ignored. For
a browser session send `X-CSRF-Token`; a device token needs none. Order of checks: sign-in, access to the book, 415, 413, 400.

**Change feeds** (`GET /progress`, `GET /bookmarks`, `GET /books/{id}/bookmarks?since=`) return
`{"items":[...],"next":string|null,"serverNow":ms}`, oldest change first, keyset-paged: follow `next` until it is
null. Keep `serverNow` (less a few seconds for clock drift) as the next `since`. `since` is exclusive, default 0;
a negative or non-numeric value is 400 `since`; a bad `cursor` is 400 `cursor`; `limit` 1..500, default 100.
When the owner merges two copies of an audiobook, the places, bookmarks and highlights that move to the kept book get a new server time (now), so a phone asking with `since` receives them under the kept book's id; an offline phone's queued saves for the dropped id still land on the kept book.

### GET /api/v1/progress?since=&cursor=&limit=
Progress rows whose server time is after `since`. Item shape as `progress` under the book endpoint (with `bookId`).

### GET /api/v1/progress/{bookID}
`{"progress":{...}|null,"serverNow"}`. The same handler as the player's `GET /progress/{bookID}`.

### PUT /api/v1/progress/{bookID}
Body `{"positionMs"*,"clientUpdatedAt"*,"deviceId","finished","force","speed","listenedMs","durationMs"}` (* required;
`positionMs` >= 0, `clientUpdatedAt` > 0; `durationMs` is ignored, the book's own is used). A device token defaults
`deviceId` to its own id and labels the row with the device's name; a session must send `deviceId`. The rules are the
player's: a client time later than the server clock plus 60 s is clamped, the newest `clientUpdatedAt` wins, `force`
makes this write win. 200 `{"applied":bool,"progress":{...},"serverNow"}`, applied or not. Errors: 400 with `field`, 404, 413, 415.

### POST /api/v1/progress/batch
Body `{"updates":[{"bookId"*, ...as PUT}]}`, 1 to 100 updates (else 400 `updates`), at most 64 KiB (413). Items run in
order, so a repeated book is applied in sequence. 200 `{"results":[{"bookId","applied","progress":{...}|null,"error":{"code","message","field"?}|null}],"serverNow"}`
in request order; a failed item does not stop the others; a hidden or unknown book is `not_found`.

### GET /api/v1/books/{id}/bookmarks?notes=1&since=&cursor=
Without `since` and `cursor`: the live bookmarks, newest first (`notes=1` keeps those with a note), `next` null.
With either: the changes of this book after `since`, deleted ones included, up to 1000 a page.
Bookmark: `{"id","clientId"?,"bookId","positionMs","note","endMs":ms|null,"kind","createdAt","updatedAt","deletedAt":ms|null}`. A clip is a bookmark with `endMs` (a moment in the audio after `positionMs`; `null` for a plain bookmark). `kind` is one of `bookmark` (the default), `favourite`, `quote`, `note`, `revisit`. A deleted one
has `deletedAt` set and an empty note.

### POST /api/v1/books/{id}/bookmarks
Body `{"positionMs"*,"note","clientId","endMs","kind"}`; `endMs` past the end of the book is clamped to it. Unknown fields are ignored, so a body without `endMs` and `kind` makes a plain bookmark. `positionMs` past the end is clamped to the book's length. 201 with the bookmark.
`clientId` (1 to 64 of letters, digits, `-`, `_`; the app's own ULID) makes a retry safe: the same person sending the same
id for the same book gets the existing bookmark back with 200 and nothing new is saved. An id already used by another
person or another book is 409 `conflict` (field `clientId`). 409 `conflict` also when the book holds its most bookmarks; 400 `note`, `clientId`, `positionMs`, `endMs` (not after `positionMs`), `kind`. On a retry the existing bookmark is returned as it is.

### GET /api/v1/bookmarks?since=&cursor=&limit=
Every bookmark of the person on any listable book changed or deleted after `since`, in a change feed (limit 1..500).

### PATCH /api/v1/bookmarks/{id}
Body `{"note","endMs","kind"}`, any of them (empty `note` clears it; `endMs` 0 clears the end). A body with none of them is 400 `note`; 400 `endMs` when the end is not after the position, 400 `kind` for an unknown kind. 200 with the bookmark; `updatedAt` moves, so the change feeds carry it. Another person's, unknown or deleted bookmark: 404.

### DELETE /api/v1/bookmarks/{id}
Soft delete: 204; the id lives on as a tombstone in the feeds. 404 as for PATCH.

## Up next (queue)

The person's own ordered list of books to play after the current one. Up to 100 books, each book once. Every read
filters by what the person may open, so a hidden, refused or missing book never shows. Adding needs access to listen
to the book (`ListenBook`); moving and removing need access to view it. Unsafe requests take the same JSON body rules
as the sync endpoints (a browser session sends `X-CSRF-Token`; a device token needs none). Order of checks: sign-in,
access to the book, 415, 413, 400.

### GET /api/v1/queue
`{"items":[{"book":BookCard,"addedAt":ms}],"next":null,"total":int}`, in play order. `next` is always `null` (the list is
short and whole).

### POST /api/v1/queue
Body `{"bookId"*,"where":"end"|"next"}` (`where` defaults to `end`; `next` puts the book first, for Play next).
200 with the whole queue, as the GET answers. A book already queued stays where it is and the answer is still 200.
Errors: 400 `bookId` or `where`; 404 for a book the person may not open; 409 `conflict` (field `bookId`) when the queue
holds 100 books or the book has no file Tendshelf can play (refused, missing or without audio).

### POST /api/v1/queue/next
Autoplay: takes the first playable book off the front of the queue and answers `{"book":BookCard}`. Body optional
(an empty body is fine), `{"ended":"<book id>"}` names the book that just finished; it is removed from the queue first.
Books the person can no longer open or that cannot play are skipped. The answer carries `"source":"queue"`. Nothing left to play: 204.
With `"series":true` (and `ended`), an empty queue falls through to the next book of `ended`'s first series that the person may
listen to, when their "next in series" setting is on: `{"book":BookCard,"source":"series"}`. The queue is not touched by this
branch. The last book, a text book, a next book already finished, a hidden next book (it is skipped without a trace), the setting off, or no `series`: 204. Errors: 400 `ended`
(longer than 64 characters), 415, 413, 400.

### POST /api/v1/queue/{bookID}/move
Body `{"dir":"up"|"down"}`. Swaps the book with its neighbour that the person can open, so a hidden book is stepped
over; at either end nothing changes. 200 with the whole queue. 400 `dir`; 404 when the book is not queued or not visible.

### DELETE /api/v1/queue/{bookID}
Takes the book off the queue: 204. 404 when the book is not queued or not visible.

## App settings, per-book speed and Continue

### GET /api/v1/settings, PATCH /api/v1/settings
`{"speed","skipBackS","skipForwardS","autoRewind","nextInSeries","rewindSteps"}`, `no-store`. `rewindSteps` is read-only, the
smart-rewind table the web player uses: `[{"fromMs":10000,"seconds":2},{"fromMs":60000,"seconds":5},...]` (a pause of at least
`fromMs` steps back `seconds`); a PATCH body's value is ignored. PATCH takes any of the same keys (body at most
4 KiB), keeps the rest, saves and answers the same shape. Unknown keys are ignored; the theme, text size and account settings are
not here. A bad value is `400 invalid` with `field` set to the first bad key in the order above (nothing is saved): `speed` 0.5 to 3
in steps of 0.05, `skipBackS` and `skipForwardS` 10, 15, 30 or 60, the two flags true or false. An empty body object is 200.

### PUT /api/v1/books/{id}/speed
`{"speed":1.5,"asUsual":false}`: remembers the person's speed for one book (204). With `asUsual` it becomes the usual speed and
the book's own is dropped. 400 `speed`; 404 for a book the person cannot listen to (checked before the body).

### GET /api/v1/continue?limit=
`limit` 1 to 24 (default 12), otherwise `400 invalid` field `limit`. `{"items":[{"book":BookCard,"positionMs","durationMs","updatedAt"}]}`,
`no-store`: audiobooks the person started and did not finish, most recent first. Books they may not listen to never appear.

## The Connect page (not `/api/v1`)
Adding a phone by QR (decision 0028) adds pages only, no JSON route: `/me/devices/add...` for the signed-in person and the public `/pair/{token}`, `/pair/wait`, `/pair/finish` for the phone's window. A QR holds only `https://host/pair/{token}`; the token alone claims and gives no session, code or name. The last leg is the unchanged `303 /app/connect/done?code=` and `POST /api/v1/auth/token`. No token route changed.

`GET /app/connect?challenge=`, `POST /app/connect` (hidden `challenge` and `_csrf`) and `GET /app/connect/done` are pages for the
Android app's window, behind the sign-in gate and for a browser session only (a bearer token is refused: 401 "Device tokens work only
for /api/v1"). Without the `ts_shell=android` mark they answer 403 with words and no form. A bad challenge is 404. The POST answers
`303 /app/connect/done?code=...` (`no-store`); the page at that address never reads its query.

## OPDS catalogue and app keys (not `/api/v1`)

Reading apps use `/opds` ([decision 0025](decisions/0025-app-keys.md)). It is not part of `/api/v1`, is GET only, and sits outside the cookie chain.

- **Key**: made on Your devices > App keys (`POST /me/app-keys`, fields `name`, `kind` = `opds` or `koreader`; 409 at the limit of 50 live
  devices). The answer shows the 24-character key once, with `Cache-Control: no-store`. Turn it off with the device sign-out.
- **Sign in**: HTTP Basic, the person's user name and the key. The login password, a bearer token or a session cookie never works; an `opds`
  key never works anywhere else. Failure: `401` with `WWW-Authenticate: Basic realm="Tendshelf", charset="UTF-8"`; 10 failures in 15 minutes from one
  address: `429` with `Retry-After`.
- **Feeds** (OPDS 1.2, Atom, `application/atom+xml;profile=opds-catalog;kind=navigation|acquisition`, `Cache-Control: no-store`):
  `/opds` (front page), `/opds/all?page=` (by title), `/opds/new?page=` (newest first), `/opds/search?q=&page=`, `/opds/search.xml` (OpenSearch).
  50 entries a page with `first`, `previous` and `next` links. A person sees only books they may read and download; each entry has one
  acquisition link per ready ebook file and an image link.
- **Files**: `/opds/books/{id}/file/{fileID}` (the ebook as stored), `/opds/covers/{id}?w=`. Anything not allowed is a plain `404`.

## KOReader progress sync (not `/api/v1`)

`/kosync` speaks the protocol of KOReader's `kosync` plugin ([decision 0025](decisions/0025-app-keys.md), plan S6). It sits in front of the outer mux so a
server address typed with a trailing slash (`/kosync//users/auth`) is served, not redirected.

- **Sign in**: headers `x-auth-user` (the user name, any case) and `x-auth-key` (the MD5 of a `koreader` app key, which KOReader sends for the "password").
  The login password, an `opds` key, a revoked key or a key of another person is `401 {"code":2001,"message":"Unauthorized"}`. The shared failure limit
  applies (10 in 15 minutes per address, then `429` with `Retry-After`).
- `POST /kosync/users/create`: always `402 {"code":2002,...}`; nobody is created. Make the key in Tendshelf, then choose Login in KOReader.
- `GET /kosync/users/auth`: `200 {"authorized":"OK"}`.
- `PUT /kosync/syncs/progress`: JSON `document` (32 hex), `progress` (1 to 512 bytes), `percentage` (0 to 1), `device`, `device_id` (at most 64 each); body at most
  8 KiB; otherwise `400 {"code":2003,"message":"Invalid fields"}`. The place is kept for that person and document. When a ready ebook file with that digest is on a book the
  person may read, the percentage also becomes their reading place (device `KOReader`, `ko-` plus a hash of `device_id`); the usual newest-wins rule applies.
  Answer `200 {"document":...,"timestamp":<seconds>}`.
- `GET /kosync/syncs/progress/{document}`: the stored place (`progress`, `percentage`, `device`, `device_id`, `timestamp`). If the person's Tendshelf reading place for
  the matching EPUB or PDF is newer, that is answered instead (`device` `Tendshelf`, `device_id` `tendshelf`): EPUB as `/body/DocFragment[N]/body` (the start of chapter N), PDF as the
  page number. Nothing known: `200 {}`.
- A document is named by KOReader's `partialMD5` (MD5 over 1024-byte samples at offset 0 and `1024<<2i`, i = 0..10), computed for each ready file by the ingest job and a
  catch-up job (`ebook.digests`). At most 10,000 documents a person; the least recently updated are dropped.
