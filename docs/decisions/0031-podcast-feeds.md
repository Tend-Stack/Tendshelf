# 0031 Podcast feeds: listen to a book or a series in any podcast app
Status: proposed (operator request, 2026-11-07). Plan: `docs/plans/podcast-feeds.md`. Parents: share links (`/s/{token}`,
`internal/share/links.go`), app keys ([0025](0025-app-keys.md)), the download right (`share.DownloadBook`), and the rules in AGENTS.md
"Security and privacy". **Amends 0025** for one surface: 0025 keeps audio out of the OPDS catalogue and lists "audio in feeds" as out of
scope. That stays true for `/opds`; this decision adds a separate surface, `/feeds/`, that carries audio and nothing else.

## Context
People already use a podcast app on their phone (AntennaPod, Pocket Casts, Apple Podcasts, Overcast). Those apps subscribe to an RSS address,
download the enclosures and remember the place; they do not send HTTP Basic reliably (least of all for enclosures and artwork) and cannot sign in
to a page. Tendshelf has two credential shapes that work without a cookie: the share link (32 random bytes in the path, one book, listen only,
anonymous) and the app key (typed, Basic, one surface). Neither fits: a share link is anonymous and made by staff, an app key needs Basic.

What exists and is reused: `token.New`/`token.Hash` (32 bytes, SHA-256 stored), the per-address failure limiter `keyLimiter` and the per-device
`apiLimiter`, `share.Can` and `BookFilterFor`, `SeriesInfo`'s order, `serveAudio`/`streamable` (Range, refused, copy-protected, inside the root,
regular file), `serveCoverFile`, `qrView`/`reachableHost`, `linkHeaders`, the App keys section on `/me/devices`, `Sessions.EndAllForUser`, and
the audit log. Nothing here adds a parallel login or streaming path.

## Decision
1. **A podcast feed belongs to one person and names one book or one series.** Its own table, `podcast_feeds`, not `devices`: a feed has a
   target, a device has none, and a device row would show in the sign-in list and be counted by the API. A person may have several feeds for the
   same target (one per app), so turning one app off does not break another; at most 50 live feeds per person.
2. **The credential is a random 32-byte token in the URL path**, `/feeds/{token}/...`; only its SHA-256 is stored, and the address is shown
   once, in the answer to the POST that made it (with a QR code when a phone can reach the host). Everything a feed serves lives under that
   prefix: `feed.xml`, `audio/{fileID}.{ext}`, `cover/{bookID}.{ext}`, `chapters/{fileID}.json`. The token reaches nothing else: no page, `/hx`,
   `/api`, `/stream`, `/me`, `/opds`. It never becomes a session; no cookie is read or set under `/feeds/`, and the routes sit on the outer mux
   beside `/opds`, outside the cookie chain. GET (and HEAD) only, so CSRF does not apply.
3. **Every request re-checks as the person, now.** The feed's owner must be enabled and hold `share.DownloadBook` on each book served (a podcast
   app keeps the files, so listening is not enough). A series feed lists only the books the person may download, filtered in SQL with
   `BookFilterFor(DownloadBook)` (which agrees with `Can`), in the series page's order, at most 100 books and 1,000 files. Removing a grant,
   disabling the account, revoking the feed or removing the book stops it on the next request.
4. **One episode per audio file, the original bytes.** A single-file book is one episode; a multi-file book is one episode per file. The
   enclosure is served by `serveAudio` with byte ranges under the same `streamable` rule as `/stream`: refused and copy-protected files are never
   listed or served, and nothing is transcoded. Chapters go out as Podcasting 2.0 `<podcast:chapters>` JSON per file, mapped from the book
   timeline with each file's offset. `itunes:block` is `yes` so no directory lists it.
5. **Failures look the same.** Malformed, unknown, revoked, owner disabled, no right, gone: a plain `404 Not found.` Malformed and unknown
   tokens cost the address one failure on the shared app-key limiter (10 in 15 minutes) and write `feed.failed`; a feed that merely ended
   (revoked, target gone, right removed) does not, so an app still polling an old feed cannot lock a household out. A limited address gets
   `429` with `Retry-After` before the database is read. A working feed counts against the per-feed request limit.
6. **Absolute URLs come from the request** (`proxies.Secure(r)` and `r.Host`), as the OPDS search description does.
7. **Made and turned off by the person.** "Podcast feed" is a quiet action on the book page and the series page for people who may download;
   it opens `/books/{id}/feed` or `/series/{id}/feed`. Live feeds are listed on Your devices beside app keys, with the target and when an app
   last used it (written at most once an hour), and each can be turned off. "Sign out everywhere" style resets (`EndAllForUser`, the
   password reset) also turn off every feed of the person, as they do app keys.
8. **Audit:** `feed.create`, `feed.revoke`, `feed.failed`. No audit row, log line or `Location` header ever holds the token.

## Security invariants
- The raw token exists only in the one response that shows it and in the person's podcast app; only its SHA-256 is stored.
- A feed token opens only paths under `/feeds/{token}/` and only GET or HEAD; it is never accepted as a cookie, bearer token or app key.
- Every answer is decided from fresh rows: the feed is live, its owner is enabled, and `share.Can(owner, DownloadBook, book)` holds for every book
  listed or served. Series listings filter in SQL.
- Only `streamable` files are listed or served; copy-protected and refused files never are.
- All failures answer the same 404; guesses are limited per address before the database is read.
- The service worker never answers `/feeds/` (it is not in its list of answered paths).

## Privacy
A podcast feed sends a book's title, author, description, cover and audio to whatever app holds the address. Some apps (Pocket Casts,
Overcast, Apple Podcasts) fetch feeds through their company's servers, so that company sees the address and the titles. This is a choice the
person makes for one book or series at a time, never on by default, and the page says so in plain words. Tendshelf itself makes no outbound
request.

## Not handled, and why
- Transcoding or a compatible copy for apps that cannot play a format (Opus, FLAC on Apple): it needs a job and storage per feed; the original
  file is what the person may download anyway.
- Feeds for a shelf, an author or "everything": a series or a book is a bounded, nameable thing; a whole-library feed would be a large, quiet leak.
- An admin view of other people's feeds: disabling the person, removing a grant or a password reset stops them all.
- Following a book that was merged into another: the feed then answers 404, and the person makes a new one.
- Playback position back from the app: podcast apps have no standard way to report it.

## Changes to AGENTS.md (applied by the plan, P1 step 13)
In "Security and privacy": read "Session, invite, sign-in link, podcast feed and API tokens are random 32-byte values". After "A device token
never becomes a session" add: "A podcast feed token reaches only `/feeds/{token}/` (GET), never a page, `/hx/`, `/api/`, `/stream/` or `/me`, and
never becomes a session (decision 0031)." In the service worker bullet, add `/feeds/...` after `/signin/...` in "It never caches or answers".
