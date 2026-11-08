# Plan: Podcast feeds (decision 0031)
Two builder packages, in order: **P1 server and feed XML** (steps 1-13), **P2 pages** (steps 14-24); together they exceed one ~250-call package.
Read `docs/decisions/0031-podcast-feeds.md` first; it is binding. A gap is reported as `PLAN GAP: <one line>` after committing what is green.
Reused: `token.New`/`token.Hash`, `keyLimiter`, `apiLimiter`, `share.Can`, `BookFilterFor`, `SeriesInfo`'s query, `serveAudio`/`streamable`,
`serveCoverFile`, `audioContentType`, `linkHeaders`, `qrView`/`reachableHost`, `AppKeys` section and `pages-appkeys.css`, `EndAllForUser`,
`db.Audit`, `agoText`/`msgNotUsedYet`. Extended: `BookFilterFor` (DownloadBook), `notFound` (plain under `/feeds/`). Replaced: nothing.

## Invariants
1. Only `SHA-256(token)` is stored; the raw token appears only in the POST response that made it, never in a log, audit row or `Location`.
2. `/feeds/` is on the outer mux, outside the cookie chain; no handler there reads a cookie, calls `currentUser`, or sets a cookie. GET/HEAD only.
3. Per request: feed row live (`revoked_at IS NULL`), owner enabled, and for every book listed or served `s.can.Can(owner, DownloadBook, book)`
   is true (series listing: SQL filter `BookFilterFor(owner,"b",DownloadBook)`, never filtered in Go). Book listable (not refused, not missing).
4. Only files with `streamable(book, f) && f.MissingSince == 0` are listed or served; a file of another book or outside the series is 404.
5. Every failure answers `404` text/plain `Not found.` with `linkHeaders`. A limited address answers `429` + `Retry-After` before any DB read.
   Malformed/unknown token: `keyLimiter.Fail(ip)` + audit `feed.failed`. Revoked, disabled, no right, gone: no count, no audit.
6. The feed handlers receive the owner as an explicit argument (`feedReq`), never through the request context, so no `/feeds/` request can
   satisfy a cookie or bearer check.
7. `last_used_at` is written only when NULL or older than 1 h (checked on the read row, then one UPDATE).

## Decisions
- New table, not `devices`: a feed has a target; a device row would appear in the sign-in list and the 50-device count.
- Many feeds per target, cap `maxLiveFeeds = 50` per person (409): one per app, so turning one off breaks no other.
- Target columns carry **no foreign key**: a deleted or merged book leaves a row whose feed answers 404 quietly (not a counted "unknown
  token" that could lock out an address); the row shows on Your devices as gone and can be turned off. `user_id` cascades.
- Episode titles: one file -> the book title. N > 1 files -> the title of the chapter containing the file start (`StartMs <= OffsetMs+1000 &&
  EndMs > OffsetMs`), trimmed; if empty or already used by an earlier file of the book: `Part {n} of {N}`. Series feed prefixes multi-file
  episodes with `{Book title} · `.
- `pubDate` = `feed.CreatedAt - 7 days + (bookRank*1000 + fileRank) seconds` (bookRank = position in series order, 0 for a book feed;
  fileRank = position in `AudioFiles` order). Stable per feed, in the past, ordered like the series; "added time" would not follow series order.
- Caps: `feedMaxBooks = 100`, `feedMaxFiles = 1000` (stop adding items at the cap).
- `guid` = `urn:tendshelf:audio:{audioFileID}`, `isPermaLink="false"`. `itunes:explicit` from `book.Explicit` (`true`/`false`).
- Cover: original file (`width 0`), URL `cover/{bookID}.{jpg|png|webp}` from the cover mime (Apple wants a `.jpg`/`.png` suffix). In every
  `{file}` segment the part after the last `.` is ignored by the handler. Audio ext = lowercased ext of `f.Path`, else `f.Container`, else `audio`.
- Chapters link only when a file maps to >= 2 chapters. No transcoding, no `?compat`.
- Per-feed request limit: `apiLimiter` key `"feed:"+feed.ID` (600/min covers podcast apps' Range bursts).
- Service in `internal/share/feeds.go` (it is about whose books reach whom, beside `links.go`). No new CSS unless a check fails.

## Data: migration `internal/db/migrations/0036_podcast_feeds.sql`
```sql
-- podcast_feeds: a private RSS address for one person and one book or series (decision 0031). Only the SHA-256 of the token is stored.
-- book_id and series_id have no foreign key on purpose: a feed whose target is gone answers 404 and stays listed until turned off.
CREATE TABLE podcast_feeds (id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL DEFAULT '' CHECK (length(name) <= 64),
  book_id TEXT, series_id TEXT,
  created_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER,
  CHECK ((book_id IS NULL) <> (series_id IS NULL))) STRICT;
CREATE INDEX podcast_feeds_user ON podcast_feeds(user_id) WHERE revoked_at IS NULL;
```
Go: `share.Feed{ID, UserID, Name, BookID, SeriesID string; CreatedAt, LastUsedAt, RevokedAt int64}`; `share.FeedRow{Feed; TargetName string}`.
Errors: `ErrFeedUnknown` (malformed or no row), `ErrFeedEnded` (revoked), `ErrTooManyFeeds`. Audit (exact): `feed.create` and `feed.revoke`
(UserID = owner, Target = owner id, Detail `{"feed": id, "target": "book:<id>"|"series:<id>"}`); `feed.failed` (UserID "", Target "",
Detail `{"ip": ip, "reason": "malformed"|"unknown"}`).

## P1 steps (server and feed XML)
1. `0036_podcast_feeds.sql` as above; `internal/db/db.go` `SchemaVersion = 36`; `internal/db/open_test.go:216` `+29`, append `podcast_feeds (0036)`.
2. `internal/db/migration_podcast_feeds_test.go`: `TestMigrationPodcastFeedsFromPrevious` (mirror `migration_sign_in_links_test.go`:
   `openUpTo(t, 35, dir)`, seed, users survive, table empty, index exists) and `TestMigrationPodcastFeedsFromEmpty` (both-targets and
   no-target rows fail the CHECK; deleting the user deletes the row).
3. `internal/share/share.go` `BookFilterFor`: add `case DownloadBook: rights = []string{"share_g.can_download = 1"}`; update its doc comment.
   `filterfor_test.go`: add `DownloadBook` to `actions` in `TestBookFilterForAgreesWithCan`.
4. `internal/share/feeds.go` (new): `type Feeds struct{st *db.Store; now func() time.Time}`, `NewFeeds(st, now)`, and
   - `Create(ctx, tx *sql.Tx, userID, name, bookID, seriesID string) (tok string, f Feed, err error)`: name trimmed, cut to 64 runes; exactly one
     target else `ErrFeedUnknown`; delete the user's rows revoked > 30 days ago; count live -> `ErrTooManyFeeds` at 50; `token.New()`; insert.
     Caller checks rights and writes the audit row.
   - `Lookup(ctx, tok string) (Feed, error)`: `token.Hash` false -> `ErrFeedUnknown` with reason malformed (export `FeedMalformed(err) bool` or
     return `(Feed, reason string, error)`; pick the latter: reasons `"malformed"`, `"unknown"`, `"ended"`).
   - `Touch(ctx, f Feed)`: `UPDATE ... SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)` (now-1h), only when the read
     row is stale. `Revoke(ctx, tx, userID, id string) (Feed, error)`: own live row only, else `ErrFeedUnknown`.
   - `Own(ctx, userID) ([]FeedRow, error)`: live rows, `TargetName` = `COALESCE(b.title, s.name, '')` via LEFT JOINs, order
     `COALESCE(last_used_at, created_at) DESC, id`.
   `internal/share/feeds_test.go`: create/lookup/revoke/own/touch-once-an-hour (fake clock)/limit 50/prune/foreign revoke refused/hash stored not token.
5. `internal/library/browse.go`: extract SeriesInfo's id query into `seriesBookIDs(ctx, sc, id, extra string, limit int)` (SeriesInfo passes
   `""`, `maxSeriesList`; behaviour unchanged) and add `SeriesFeedBooks(ctx, sc Scope, seriesID string, limit int) ([]string, error)` with extra
   `EXISTS (SELECT 1 FROM audio_files af WHERE af.book_id = b.id AND af.missing_since IS NULL)`. Test in `browse_test.go` (or the series test
   file): order matches SeriesInfo, text-only books and invisible books absent, limit honoured.
6. `internal/auth/sessions.go` `EndAllForUser`: after `revokeAllDevices`, `UPDATE podcast_feeds SET revoked_at = ? WHERE user_id = ? AND
   revoked_at IS NULL`. Test: after the call the user's feeds are revoked; another user's are not.
7. `internal/web/server.go`: field `feeds *share.Feeds`, `share.NewFeeds(opts.Store, opts.Now)`; `s.registerFeeds(outer)` beside `registerOPDS`.
8. `internal/web/access.go` `notFound`: first branch `if strings.HasPrefix(r.URL.Path, "/feeds/") { linkMediaNotFound(w); return }`.
9. `internal/web/routes_feeds.go` (new): `registerFeeds(outer)` builds a sub-mux with `GET /feeds/{token}/feed.xml`,
   `GET /feeds/{token}/audio/{file}`, `GET /feeds/{token}/cover/{file}`, `GET /feeds/{token}/chapters/{file}`, and `"/feeds/"` -> 404 plain;
   `outer.Handle("/feeds/", sub)`. `type feedReq struct{feed share.Feed; owner users.User; base string}` (base = scheme + `r.Host`).
   `s.feedRoute(h func(http.ResponseWriter, *http.Request, feedReq)) http.HandlerFunc` runs invariant 5 in order: `linkHeaders`; `keyLimiter.Allow`;
   `feeds.Lookup` (malformed/unknown: Fail + audit + 404; ended: 404); `users.ByID` disabled or missing -> 404; `apiLimiter.Allow("feed:"+id)`
   (429 `Too many requests from this app. Wait a little and try again.`) then `apiLimiter.Fail`; `feeds.Touch`; call h.
   Helpers: `s.feedBooks(ctx, fr) ([]library.Book, error)` (book feed: `lib.Book` + listable + Can; series: `SeriesFeedBooks(feedScope(owner), id,
   feedMaxBooks)` then `lib.Book` each); `s.feedBookFor(ctx, fr, bookID)` (book feed: id must equal; series: `lib.BookSeries` contains the series,
   then listable + Can); `feedFiles(book, files)` (invariant 4). Handlers: feed.xml; audio -> `s.serveAudio(w, r, f, false, "private, no-store")`;
   cover -> `s.serveCoverFile(w, r, book.CoverID, 0, "private, no-store")`; chapters -> JSON. Server errors: `500` plain `Something went wrong on
   our side.` and `slog.Error` without the token.
10. `internal/web/feeds_xml.go` (new): encoding/xml structs; colon tags are literal (`xml:"itunes:author"`). `<rss version="2.0"
    xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0">`; channel: `title` (book
    title or series name), `link` (base+"/"), `description` (book/series description, else the title), `language` (book only, when set),
    `itunes:author` (book authors from `BookPeople` role author, joined ", "; series: first book's), `itunes:image href` (book or first book cover;
    omitted with no cover), `itunes:block` `yes`, `itunes:explicit`, `itunes:type` `serial`. Item: `title`, `guid`, `pubDate` (RFC1123Z, UTC),
    `enclosure url length(f.Size) type(audioContentType)`, `itunes:duration` (whole seconds), `itunes:episode` (1-based), `itunes:image` (series),
    `itunes:explicit`, `description` (book description, first 4000 runes), `podcast:chapters url type="application/json+chapters"`.
    `writeFeedXML(w, v)`: `MarshalIndent`, `xml.Header`, `Content-Type: application/rss+xml; charset=utf-8`, `Cache-Control: no-store`.
    Chapters JSON `{"version":"1.2.0","chapters":[{"startTime":<sec, 3 decimals>,"title":...}]}` for chapters overlapping
    `[OffsetMs, OffsetMs+DurationMs)`, startTime `max(0, StartMs-OffsetMs)`; `Content-Type: application/json+chapters`.
11. `internal/web/routes_matrix_test.go` `sweepRegistered`: skip `path == "/feeds/" || strings.HasPrefix(path, "/feeds/")` with the comment
    `// feed token in the path, not a cookie: feeds_test.go covers it`.
12. `internal/web/feeds_test.go` (new; `newFeedLab` mirrors `newOPDSLab`: books with `Files: 1` and `Files: 3` + chapters, a series of 3 books
    in two libraries, one refused book in the series, member with download grant, member without download, guest with download on one library).
    Tests: `TestFeedXMLBook` (parse with encoding/xml: items, titles, enclosure lengths/types, guid, pubDate order, block, absolute https URLs
    behind a trusted proxy), `TestFeedXMLSeriesFiltersByGrant` (no-download member sees 404; partial grant lists only granted books; refused and
    text-only absent; caps via small test constants or 101 seeded books), `TestFeedAudioRange` (206 with `Range`, HEAD works, file of another
    book 404, file of an ungranted series book 404, copy-protected 404), `TestFeedCoverAndChapters`, `TestFeedFailuresLookAlike` (malformed,
    unknown, revoked, disabled owner, grant removed, book deleted: identical status and body; only malformed/unknown write `feed.failed` and count),
    `TestFeedLimiter` (11th bad token from one address -> 429 before DB; revoked token polled 20 times -> still 404, never 429),
    `TestFeedTokenOpensNothingElse` (token as cookie `ts_session`, as `Authorization: Bearer`, as Basic password on `/opds`, and the paths `/`,
    `/books/{id}`, `/stream/{fileID}`, `/me/devices`, `/hx/player/{id}`, `/api/v1/books` with the token: never 200; a valid session cookie on
    `/feeds/` with a bad token: 404), `TestFeedNoCookieSet` (no `Set-Cookie` on any `/feeds/` answer), `TestFeedTouchOncePerHour`,
    `TestFeedsNotInServiceWorker` (`static/js/sw.js` does not contain `feeds`), `TestFeedTokenNotLogged` (capture slog; token absent).
13. Docs: `AGENTS.md` (the three edits in 0031 "Changes to AGENTS.md"); `docs/architecture.md`: default deny line (~465) add `/feeds/{token}/`
    (decision 0031), routes table rows for the four feed routes, tables row `podcast_feeds`, and fix the stale `devices` row (~65) kind list to
    `'web','pwa','desktop','mobile','opds','koreader'`; `docs/api.md`: new section "Podcast feeds (not `/api/v1`)" after the OPDS section.
Gates P1: `make generate` clean, `make check`, `make build`.

## P2 steps (pages)
14. `internal/web/feeds_copy.go` (new) with the constants below (copy list).
15. `internal/web/pages/feeds.templ` (new): `FeedMakeView{CSRF, Title, Lead, Privacy, NameLabel, NameHint, Make, ListNote, DevicesLabel,
    BackHref, BackLabel, Action string}`, `FeedShownView{Title, Lead, Note, AddressLabel, Address, QR *components.QRView, QRNote, BackHref,
    BackLabel string}`, `FeedsView{CSRF, Heading, Lead, None, TurnOff string; Lines []AppKeyLine}`; components `FeedMake`, `FeedShown`
    (readonly `ts-input` with the address, `@components.QR` in `ts-pair__qr` like `InviteLinkPanel`), `Feeds` (section markup of `AppKeys`, ids
    `feeds-h`). Use `ts-me`, `ts-akey`, `ts-group` classes; no new CSS.
16. `internal/web/routes_feeds_pages.go` (new; cookie mux): `GET/POST /books/{id}/feed`, `GET/POST /series/{id}/feed`,
    `POST /me/feeds/{id}/revoke`. Book: `loadBook(DownloadBook)` + listable + >= 1 `feedFiles` else `s.notFound`. Series: `SeriesFeedBooks(feedScope(u),
    id, 1)` empty -> `notFound`. POST: same checks, `WriteTx` { `feeds.Create`; audit `feed.create` }; `ErrTooManyFeeds` -> 409 `feedLimit`
    (htmx: toast as `keyError` does); answer 200 `FeedShown` with `Cache-Control: no-store` (never a redirect). QR only when
    `reachableHost(r.Host)`. Revoke: `WriteTx` { `feeds.Revoke`; audit `feed.revoke` }; unknown or foreign -> 404; success 303 `/me/devices`.
17. `internal/web/routes_me.go` `getDevices`: add `Feeds` to `DevicesView` from `s.feedsView(r)`: Title = TargetName or `feedGone`; Meta = name
    (if any) · `feedKindBook`/`feedKindSeries` · (`msgNotUsedYet` or `agoText(last_used_at)`); Action `/me/feeds/{id}/revoke`; Aria `Turn off {Title}`.
    `pages/devices.templ`: render `@Feeds(v.Feeds)` right after the App keys section.
18. `internal/web/play_link.go` `bookPlayView` (audio only, not `textPlayView`): `if v.Download != nil && v.Button != nil { v.FeedHref =
    "/books/"+book.ID+"/feed" }`; `BookPlayView.FeedHref`; `book.templ` after `@bookDownload`: `<a class="ts-btn ts-btn--quiet ts-book__feed"
    href=...>Podcast feed</a>`.
19. `getSeries` + `pages.SeriesView.FeedHref`: set when `SeriesFeedBooks(feedScope(u), id, 1)` is non-empty; `people.templ` quiet link after
    the count line.
20. `routes_matrix_test.go`: rows for the five patterns: GET book/series feed `sweepStaff(200, 404)`; POST book/series feed (empty form)
    `sweepStaff(200, 404)`; `POST /me/feeds/{id}/revoke` with `zz-none` `sweepSame(404)`. Granted app: member/guest follow the fixture's
    `can_download` (read `:104-106`, `:226`); set `Granted` accordingly.
21. `internal/web/feeds_pages_test.go`: make-and-use round trip (POST shows address once; GET of the page later does not contain it; DB holds
    only the hash); forbidden per role (listener without download 404 on GET and POST; member of another library 404; contributor on own
    upload 200); text-only book 404; 51st feed 409; CSRF missing 403; revoke own 303 and the feed then 404; revoke another person's feed 404;
    `EndAllForUser` via the admin sign-out revokes feeds; book page shows "Podcast feed" only with download right; series page likewise.
22. `internal/web/visual_feeds_test.go`: mirror `visual_appkeys_test.go` for FeedMake, FeedShown, and Your devices with two feeds, at 390,
    768, 1024, 1440, all themes it covers; keep the address stable as the app-keys test keeps the key stable (PLAN GAP if it cannot).
23. `internal/web/a11y_audit_test.go`: add `/books/{id}/feed`, the shown page and `/me/devices` with feeds (mirror commit `6397d9e`).
24. Docs: `docs/copy-additions.md` row for `feed*`; `docs/roadmap.md` status note (scope, checks, limits, next).
Gates P2: `make generate` clean, `make check`, `make build`, visual capture recorded.

## Copy (exact; constants in `feeds_copy.go`)
`feedAction` "Podcast feed" · `feedTitle` "Listen in a podcast app" · `feedLeadBook` "Make a private address for this book, add it to a
podcast app such as AntennaPod, Pocket Casts, Apple Podcasts or Overcast, and listen there." · `feedLeadSeries` same with "this series" ·
`feedPrivacy` "Anyone with the address can listen and keep copies, so keep it to yourself. Some apps fetch it through their own servers, so
that company sees the address and the titles." · `feedNameHint` "So you know which app it is, for example Pocket Casts." (label reuses
`akeyNameLabel`) · `feedMake` "Make a feed address" · `feedListNote` "Your feed addresses are on Your devices, where you can turn them off." ·
`feedDevicesLink` "Your devices" · `feedBackBook` "Back to the book" · `feedBackSeries` "Back to the series" · `feedShownTitle` "Your feed
address" · lead reuses `akeyShownLead` · `feedAddress` "Feed address" · `feedNote` "In your podcast app, choose to add a podcast by address
(it may say URL or RSS) and paste this address." · `feedQRLabel` "QR code with the feed address" · `feedQRNote` "Scan it with your phone to
take the address there." · `feedLimit` "You have as many feed addresses as one person may have. Turn one off on Your devices first." ·
`feedsHeading` "Podcast feeds" · `feedsLead` "Each address lets one podcast app play a book or a series. Turn one off and that app stops
getting it." · `feedsNone` "No feeds yet." · `feedKindBook` "Book" · `feedKindSeries` "Series" · `feedGone` "A book or series that is no
longer here" · turn-off reuses `akeyTurnOff`. Feed answers (plain text): `Not found.`; per address `Too many tries. Wait a little and try again.`; per feed `Too many requests from this
app. Wait a little and try again.`

## Out of scope
Transcoding or `?compat`; shelf, author or whole-library feeds; admin view of other people's feeds; following merged books; position sync back
from apps; podcast-app-specific schemes (`podcast://`, `pcast:`); Android app changes; service worker changes; editing a feed's name.
