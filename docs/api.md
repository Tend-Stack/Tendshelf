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

## Sync endpoints (progress and bookmarks)

A person's own rows only. Every row is filtered to books the person may list, so a book that is hidden, unknown,
refused or missing never shows and answers the same 404. Times are Unix milliseconds. Unsafe requests take a JSON
body (`Content-Type: application/json`, else 415; too large 413; unreadable 400); unknown fields are ignored. For
a browser session send `X-CSRF-Token`; a device token needs none. Order of checks: sign-in, access to the book, 415, 413, 400.

**Change feeds** (`GET /progress`, `GET /bookmarks`, `GET /books/{id}/bookmarks?since=`) return
`{"items":[...],"next":string|null,"serverNow":ms}`, oldest change first, keyset-paged: follow `next` until it is
null. Keep `serverNow` (less a few seconds for clock drift) as the next `since`. `since` is exclusive, default 0;
a negative or non-numeric value is 400 `since`; a bad `cursor` is 400 `cursor`; `limit` 1..500, default 100.

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
Bookmark: `{"id","clientId"?,"bookId","positionMs","note","createdAt","updatedAt","deletedAt":ms|null}`. A deleted one
has `deletedAt` set and an empty note.

### POST /api/v1/books/{id}/bookmarks
Body `{"positionMs"*,"note","clientId"}`. `positionMs` past the end is clamped to the book's length. 201 with the bookmark.
`clientId` (1 to 64 of letters, digits, `-`, `_`; the app's own ULID) makes a retry safe: the same person sending the same
id for the same book gets the existing bookmark back with 200 and nothing new is saved. An id already used by another
person or another book is 409 `conflict` (field `clientId`). 409 `conflict` also when the book holds its most bookmarks; 400 `note`, `clientId`, `positionMs`.

### GET /api/v1/bookmarks?since=&cursor=&limit=
Every bookmark of the person on any listable book changed or deleted after `since`, in a change feed (limit 1..500).

### PATCH /api/v1/bookmarks/{id}
Body `{"note"*}` (empty clears it). 200 with the bookmark. Another person's, unknown or deleted bookmark: 404.

### DELETE /api/v1/bookmarks/{id}
Soft delete: 204; the id lives on as a tombstone in the feeds. 404 as for PATCH.
