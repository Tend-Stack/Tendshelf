# 0038 Podcasts and radio: subscribe, listen, keep it all on this server
Status: proposed (operator request, 2026-11-09). Plan: `docs/plans/podcasts.md`. Parents: the outbound rule and the service-worker
rule in AGENTS.md "Security and privacy", `internal/metadata/online` (the guarded client), `share.Can`, the jobs runner, the player
(`player.js`, `native-audio.js`), the queue (`queue_items`, 0013), the Android offline system (L8, `Offline.kt`). **Amends** AGENTS.md
"No telemetry" (new outbound kinds, below) and the service-worker list. Not related to 0031: 0031 *publishes* books as feeds under
`/feeds/` and `podcast_feeds`; this decision *subscribes* to other people's podcasts under `/podcasts/` and `podcast_shows`.

## Context
The feature map lists podcasts as Later; the operator wants a full podcast app (Apple Podcasts, Pocket Casts, AntennaPod as reference)
and, after it, radio. OndaCast (the operator's catalogue, `https://ondacast.com`) is the search directory. Checked read-only on
2026-11-09: `GET /api/v2/search?q=&limit=&type=podcasts|stations` answers `{"success","data":[{id,type,title,subtitle,slug,image_url,
stream_url,rank}]}` (no feed address); `GET /api/v2/podcasts/{slug}` answers `data.feed_url` (the publisher's RSS address);
`GET /api/v2/embed/stations/{slug}` answers `data.streamUrl`, `name`, `logo`, `genre`, `country`. Some station streams are plain `http`.

What exists and is reused: `internal/metadata/online` (one guarded client package, plain-words `Error`), `jobs.Runner` (kinds, plain-words
`jobs.Error`, `/hx/jobs/{id}` polling), `share.Can` (new actions), the settings table, `serveCoverFile`-style resized pictures, the player
fragment shape (`/hx/player/{bookID}` data attributes), `/progress/{bookID}` save rules, the queue, the person's offline cache in the
service worker, Android `Offline.kt`/`TsDownloadService`/`LibraryTree`/`TendWidget`/`SleepTimer`/`Resume`. Not reusable as is:
`book_settings`, `listening_sessions` and `queue_items` have a foreign key to `books`, so a show's speed lives on the subscription row and
the queue is rebuilt to hold either a book or an episode (one queue, not two).

## Decision
1. **Tendshelf owns its podcast data.** Shows, episodes, subscriptions, artwork, favourites, history, progress and stations live in
   Tendshelf's database and cache. A show is keyed by its feed address, an episode by its feed `guid` (or a hash of enclosure address,
   title and date when there is none), a station by its stream address. An OndaCast slug is kept only as an optional hint. If OndaCast is
   down or gone, everything except the search box keeps working; refresh always goes straight to the publisher.
2. **Off until the owner turns it on.** `podcasts.enabled` and `radio.enabled` (settings, default off, owner only through
   `ManageServer`). Off means no route answers (404), no nav item, no job runs, no outbound request.
3. **Who.** Owner, admin and member (a contributor is a member) may use podcasts and radio: new actions `share.UsePodcasts`,
   `share.ListenEpisode` (target kind `episode`) and `share.UseRadio`. A guest may not (a guest is a limited visitor; podcasts make the
   server fetch from the internet). Shows are stored once per server; subscriptions, progress, favourites and history are per person and
   never shown to anyone else. A person may play an episode only of a show they follow, or one already in their own history or
   favourites; that relation is read inside `share.Can`, never from the request.
4. **Network rules (one package).** All podcast and radio traffic goes through `internal/metadata/online/podnet`, a sub-package of the
   guarded client package. OndaCast: `https` to exactly `ondacast.com`, only the typed query (or the picked slug) is sent, no account, no
   identifiers, no cookies. Feeds, enclosures, artwork, chapter files and station streams: `http` or `https`, ports 80, 443 or 1024-65535,
   no user info in the address. **Every connection is checked at dial time**, after DNS, on the address actually dialled: loopback,
   private, link-local, CGNAT (100.64/10), unspecified, multicast, benchmarking (198.18/15), 0/8, 240/4, IPv6 ULA and link-local, and
   NAT64 or IPv4-mapped forms of those are refused. Environment proxies are ignored. At most 5 redirects, each re-checked. No cookies,
   no Referer, no client header passed through except `Range`. User-Agent is fixed: `Tendshelf (self-hosted podcast player)`, no
   version. Caps: feed 15 MiB and 30 s, picture 5 MiB, chapters 1 MiB, OndaCast answer 2 MiB and 10 s, server-cached episode 1 GiB.
5. **Playback goes through this server.** The browser and the app ask `/podcasts/audio/{episodeID}` (and `/radio/live/{stationID}`);
   the server streams the publisher's file with `Range` passed through, so seeking works, the CSP stays `media-src 'self' blob:`, plain
   `http` enclosures never cause mixed content, and the phone only ever talks to its own server. If the episode is in the server cache it
   is served from disk. Limits: 16 upstream streams per server, 3 per person, 1 live station per person; over the limit answers 503 with
   plain words. Downloading everything first was rejected: it delays the first sound by the whole file and doubles disk use.
6. **Server cache, not library files.** Episodes kept on the server (auto-download, or a file whose host ignores `Range`) go to
   `<data>/cache/podcasts/<showID>/<episodeID>.<ext>`; artwork to `<data>/cache/podcast-art/`. They are Tendshelf's own copies of
   public downloads, not the person's files: the "audio files are the user's" rule does not apply, Tendshelf creates and deletes them
   freely, they never appear in a library, and they are never scanned. The owner sets a size limit (`podcasts.cache_bytes`, default
   10 GiB, 0 = keep nothing); over it, the oldest played or unfollowed episodes go first, then the oldest unplayed. Backups copy the
   database (subscriptions, progress, favourites, history) and never the cache; a restored server downloads again.
7. **Refresh.** Following a show, pressing Refresh, and opening Podcasts (for that person's shows last fetched over an hour ago) fetch
   feeds. Scheduled refresh runs only when the owner picks an interval (`podcasts.refresh`: off, 1 h, 6 h, 24 h; default off). Each
   refresh is a job; failures are stored as a code and shown in plain words. Conditional GET (ETag, Last-Modified) always.
8. **Feeds parsed.** RSS 2.0 with iTunes and Podcasting 2.0 tags, and Atom. Show notes are cleaned to a short list of tags (`p br a ul
   ol li strong b em i blockquote`); links keep only `http`, `https` and `mailto` and get `rel="nofollow noopener noreferrer ugc"`;
   pictures and everything else are dropped. Chapters: Podcasting 2.0 JSON, and chapters inside the MP3 or M4A (ID3 `CHAP`, built in P6) read
   from the server cache only with the existing ffprobe wrapper, only when the publisher names no chapters file.
9. **Offline.** Nothing is kept on a device unless the person presses Download (web: the person's own cache in the service worker,
   like a book; Android: the existing downloads system), or turns on auto-download for a show on the phone (Wi-Fi only, quota, keep the
   latest N, remove after played). The service worker answers `/podcasts/audio/{id}`, `/podcasts/art/{showID}`,
   `/podcasts/chapters/{id}` and `/hx/player/episode/{id}` only from the person's own cache, and never caches any other `/podcasts/` or
   `/radio/` address. Sign-out deletes them with the rest of the person's caches.
10. **Radio** (after podcasts): stations found by OndaCast or added by stream address, favourites and history stored locally, live audio
    through the server with ICY titles read out of the stream, no seeking, a Live badge. Radio Browser is not a fallback: it would be a
    second outside directory and host, OndaCast already draws on it, and saved stations play without any directory.
    The now-playing title of a station is shared by everyone who saved it, so it shows whenever anyone is listening (accepted).
11. **Limits.** 200 followed shows per person; 3,000 newest episodes kept per show (older ones without a person's history or favourite
    are removed); 100 stations per person; search 20 a minute per person; follow and add 30 an hour per person; Refresh once per show
    per 5 minutes; OPML import 1 MiB and 200 shows.

## Changes to AGENTS.md
Replace the "No telemetry" bullet's second sentence onward with:

> The only outbound requests are user-initiated metadata lookups and cover downloads through `internal/metadata/online`, and podcasts
> and radio through its sub-package `internal/metadata/online/podnet`, both off until the owner turns podcasts or radio on: a search
> sends only the typed words to `ondacast.com` (no account, no identifiers); following a show, Refresh, opening Podcasts, and a
> scheduled refresh only when the owner picks an interval fetch the publishers' feeds; playing, downloading or auto-downloading an
> episode fetches its audio, artwork and chapters; playing a station fetches its stream. Every podcast and radio connection is checked
> after DNS and refused for private, loopback, link-local and CGNAT addresses, sends no cookies, and carries a fixed User-Agent. These
> two are the only server packages that create an `http.Client` (besides `internal/auth/oidc`, decision 0037).

Add to "Audio files are the user's": "Podcast episodes Tendshelf downloads into `<data>/cache/podcasts/` are its own cache, not library
files; they are never scanned or listed in a library, and backups leave them out (decision 0038)." Add the four `/podcasts/` addresses
of point 9 to the service-worker answer list and `/podcasts/...` (other) and `/radio/...` to its never list. Allow
`golang.org/x/net/html/charset` in `internal/podcast/rss` (feed character sets) and `golang.org/x/net/html` there for show notes.

## Consequences
- The server sees which shows a person follows and when they listen; publishers see the server's address, not the listener's.
- A tracking redirect in an enclosure is followed by the server, so a publisher may count a seek as a play. Not worked around.
- Feeds on the home network (a private address) cannot be followed. Allowing them would open the server to SSRF; not offered.
- Feature map row "Podcasts" moves from Later to Yes when P3 lands.

## Rejected
Fetching feeds through OndaCast (a runtime dependency and a profile of the household on another server); letting the browser or the app
load enclosures directly (CSP, mixed content, the phone talking to other hosts); a second queue for episodes; reusing `book_settings`
with fake book ids; storing OndaCast ids as the key.

## Addendum 2026-11-10: pictures of search results
The operator decided that search results show artwork. A result's picture lives on its publisher's site, which the page's CSP
(`img-src 'self' data:`) never loads, so the server fetches it and serves it from its own address.
- When a podcast or radio search answers, each hit's picture address is stored in memory under a random 128-bit key that belongs to the
  signed-in person and expires after 20 minutes (at most 400 keys). `GET /podcasts/search/art/{key}` and `GET /radio/search/art/{key}`
  take only that key; an unknown, expired, foreign or wrong-kind key is a 404. The route is not a proxy: no address is ever in a request.
- The fetch is `podnet.Client.Picture`, the same guarded path the show-art and station-logo jobs use: address checked after DNS, no
  cookies, fixed User-Agent, 5 MiB cap, JPEG, PNG or WebP decided by the bytes. At most four pictures load at once; a small in-memory cache
  (40 pictures, 32 MiB, same 20 minutes) means paging back fetches nothing. Nothing is written to disk.
- Answers carry `Cache-Control: private`, `X-Content-Type-Options: nosniff` and the sniffed image type. The service worker never
  answers these addresses (they are "other `/podcasts/...`" and `/radio/...` addresses). Following a show still queues its own art job.
- A failed picture is a 404 and the page keeps its plain tile. Only the owner turning podcasts or radio on makes the routes exist for people.
