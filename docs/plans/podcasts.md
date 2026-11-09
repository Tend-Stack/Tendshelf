# Podcasts and radio (decision 0038): the builders' contract

Reuses: `internal/metadata/online` (guarded-client pattern, `Error` with plain words; `podnet` is a sub-package), `jobs.Runner`,
`share.Can`, settings table, `serveCoverFile` resizing, the player fragment shape and `/progress/{bookID}` rules, `queue_items`, the
service worker's person cache, Android `Offline.kt`, `TsDownloadService`, `LibraryTree`, `TendWidget`, `SleepTimer`, `Resume`, `AutoNext`,
`SqliteOutbox`. Extends: `share` (actions, target kind), `queue_items` (P5 rebuild), `player.js` (an item kind), the nav list. New:
`internal/podcast` (store, jobs, scheduler), `internal/podcast/rss`, `internal/podcast/opml`, `internal/metadata/online/podnet`.
Nothing replaced. No new Go module. Each package below is one builder dispatch (target <= 250 tool calls); a scout maps it first.

## Invariants (all packages)
1. Off is off: with `podcasts.enabled` false every `/podcasts`, `/hx/podcasts`, `/api/v1/podcasts` route answers the plain 404, no job
   kind runs a fetch, the scheduler sleeps; same for `radio.enabled` and `/radio`.
2. Only `internal/metadata/online/podnet` dials out for podcasts and radio; every dial passes `checkAddr` on the resolved IP.
3. Authorization only through `share.Can`; lists (shows, episodes, history, favourites, stations) are filtered by `user_id` in SQL.
4. A person reaches an episode (page, audio, chapters, player fragment, progress) only if `share.Can(u, ListenEpisode, Target{Kind:
   TargetEpisode, ID})`: they follow its show, or own a `podcast_episode_state` row for it. Otherwise the same 404 as an unknown id.
5. No upstream address, header, cookie or error text reaches a page, a log line visible to a person, or a response header.
6. OndaCast is needed only by `/podcasts/search` and `/radio/search` (and following by slug). Everything else works with it unreachable.
7. Library files are never touched; the cache lives only under `<data>/cache/podcasts/` and `<data>/cache/podcast-art/`.
8. Positions, durations, times are INTEGER ms; ids `ps_` (show), `pe_` (episode), `rs_` (station) from `db.NewID`.

## Feature parity (Apple Podcasts / Pocket Casts reference)
| Feature | Tendshelf | Package |
| --- | --- | --- |
| Search directory, follow, add by RSS address | yes | P2 (Go), P3 (pages) |
| OPML import and export | yes | P4 |
| Episode list: newest/oldest, unplayed / in progress / played, search within show | yes | P3 |
| Show notes with safe links | yes | P1 (clean), P3 |
| Mark played/unplayed, favourites (star), history | yes | P2, P3; Android long-press A3 |
| Resume place synced across devices | yes | P2, P4, A1 |
| Per-show speed, skip intro/outro seconds | yes | P2 (data), P4 (web), A1 |
| Skip -15/+30, speed 0.5-3.5, sleep timer incl. end of episode | existing player | P4, A1 |
| Smart resume after a long pause | existing L2 rule | P4, A1 |
| Chapters (Podcasting 2.0 JSON) | yes | P2, P4, A1; ID3 `CHAP` later (P6) |
| Up next / queue, continue to next | yes, one queue with books | P5, A1 |
| New-episode badges | yes | P3 (web), A2 (notification) |
| Download for offline | yes | P5 (web), A2 |
| Auto-download new episodes, Wi-Fi only, keep latest N, remove after played, storage cap | yes | P5 (server), A2 (phone) |
| Background play, lock screen, notification controls | existing Media3 path | A1 |
| Android Auto: follows, new, downloads | yes | A3 |
| Home-screen widget shows the episode | yes | A3 |
| Home "New episodes" shelf | yes | P4 |
| Scheduled refresh | owner opt-in | P2 |
| Radio: search, add by address, favourites, history, live titles | yes | R1-R3 |
| Transcripts, clips, comments, ratings, social, cloud sync service | out: needs other services or later plans | - |
| Video podcasts | out: audio product; enclosure `video/*` listed as "not playable here" | - |
| Silence trimming, volume boost per show | later: existing sound modes cover the boost; trim needs DSP | - |
| CarPlay, iOS | out: iOS is a later platform | - |
| Private feeds behind a password, feeds on the home network | out: credentials storage and SSRF | - |

## Settings (table `settings`, JSON values; owner only via `ManageServer`, page `/admin/podcasts`)
`podcasts.enabled` bool false · `podcasts.refresh` "off"|"1h"|"6h"|"24h" default "off" · `podcasts.cache_bytes` int default
10737418240, 0..1099511627776 · `podcasts.server_auto` bool false (allows per-show server auto-download) · `radio.enabled` bool false.

---
## P1 (Go): decision, AGENTS, the guarded podcast client, the parser
Steps:
1. `AGENTS.md`, `docs/architecture.md`: apply 0038 "Changes to AGENTS.md" verbatim; architecture gets the package, the routes list
   (from P2/R1 below) and the cache folders. `docs/feature-map.md:17` unchanged until P3.
2. `internal/metadata/online/podnet/net.go`: `type Client`; `New(o Options) *Client`; `Options{Now func() time.Time; Resolver
   *net.Resolver; DialContext func(ctx, network, addr string) (net.Conn, error) /* tests only, still checked */; OndaCastBase string /*
   tests only; default "https://ondacast.com" */}`. Transport: own `http.Transport{Proxy: nil, DialContext: guardedDial,
   TLSHandshakeTimeout: 10s, ResponseHeaderTimeout: 15s, MaxIdleConnsPerHost: 2, ForceAttemptHTTP2: true}`; `http.Client{Jar: nil,
   CheckRedirect: max 5 hops, each hop through `checkURL`}`. `guardedDial` uses `net.Dialer{Timeout: 10s, Control: controlCheck}`;
   `controlCheck` parses the dialled `host:port` and calls `checkAddr(netip.Addr)`.
3. `podnet/addr.go`: `checkURL(u *url.URL, kind Kind) error` — scheme `http|https` (OndaCast: `https` and host `ondacast.com` only), no
   user info, port empty, 80, 443 or 1024-65535; host not an IP literal in a refused range. `checkAddr(a netip.Addr) error` — unmap
   4in6 and NAT64 `64:ff9b::/96`; refuse `IsLoopback, IsPrivate, IsLinkLocalUnicast, IsLinkLocalMulticast, IsMulticast,
   IsUnspecified, IsInterfaceLocalMulticast`, and prefixes 0.0.0.0/8, 100.64.0.0/10, 192.0.0.0/24, 192.0.2.0/24, 198.18.0.0/15,
   198.51.100.0/24, 203.0.113.0/24, 240.0.0.0/4, 255.255.255.255/32, 2001:db8::/32, fc00::/7. Error `&online.Error{Code: "refused"}`
   style: podnet defines its own `type Error struct{Code string}` with `Text()`; codes `refused offline not_found not_a_feed too_large
   slow busy bad_answer`.
4. `podnet/fetch.go`: `(c) Feed(ctx, rawURL, etag, lastMod string) (FeedResult{Body []byte; FinalURL, ETag, LastModified string;
   NotModified, Moved bool}, error)` (15 MiB, 30 s, 304 -> NotModified, 301/308 chain -> Moved+FinalURL, 404/410 -> not_found, 429 ->
   busy); `Picture(ctx, rawURL) ([]byte, mime string, error)` (5 MiB, image/jpeg|png|webp only, sniffed); `Chapters(ctx, rawURL)
   ([]byte, error)` (1 MiB, JSON); `OpenAudio(ctx, rawURL, rangeHdr string) (*Upstream, error)` where `Upstream{Status int; Header
   http.Header /* only Content-Type, Content-Length, Content-Range, Accept-Ranges, Last-Modified, ETag */; Body io.ReadCloser /* idle
   read timeout 60 s */}`; `OpenLive(ctx, rawURL) (*Upstream, metaint int, error)` (sends `Icy-MetaData: 1`). All bodies capped with a
   reader that returns `too_large`; every request sets the fixed UA and `Accept`, nothing else.
5. `podnet/ondacast.go`: `Search(ctx, q string, kind "podcasts"|"stations", limit int) ([]Hit{Slug, Title, Subtitle, ImageURL,
   StreamURL string}, error)` — `q` trimmed, 1..100 runes, limit 1..25; `PodcastFeedURL(ctx, slug) (string, error)` -> `data.feed_url`;
   `Station(ctx, slug) (StationHit{Name, StreamURL, Logo, Genre, Country string}, error)`. Slug must match `^[a-z0-9][a-z0-9-]{0,127}$`
   before any request. Answers cached in memory 10 min (map with size 500, oldest out).
6. `internal/podcast/rss/parse.go`: `Parse(body []byte, feedURL string) (Feed, error)`; `Feed{Title, Author, Description(clean HTML),
   Link, Language, ImageURL, NewFeedURL string; Explicit bool; Episodes []Item}`; `Item{GUID, GUIDKey, Title, NotesHTML, Link, AudioURL,
   AudioType string; AudioBytes, DurationMs int64; PublishedAt int64 /* ms, 0 unknown */; Season, Number int; Kind "full"|"trailer"|
   "bonus"; ChaptersURL, ImageURL string}`. `xml.Decoder` with `CharsetReader: charset.NewReaderLabel`, `Strict=false`, entity map
   `xml.HTMLEntity`. RSS 2.0 + `itunes:` (author, image href, duration `ss`, `mm:ss`, `hh:mm:ss`, block ignored, episodeType, season,
   episode, new-feed-url) + `podcast:chapters` (url, type `application/json+chapters`) + Atom (`entry`, `link rel="enclosure"`). Rules:
   relative URLs resolved against `feedURL`; items with no audio enclosure dropped (video enclosure kept with `AudioType` as given; UI
   says not playable); `GUIDKey` = hex SHA-256 of trimmed GUID, else of `AudioURL`, else of `Title|PublishedAt`; duplicate keys keep the
   first; dates tried in RFC1123Z, RFC1123, RFC822Z, RFC822, RFC3339, and the same with single-digit day, else 0; at most 3,000 items
   (newest by date); text fields trimmed and cut at 500 runes (title 300). Not a feed (no `rss`/`feed` root) -> `ErrNotFeed`.
7. `internal/podcast/rss/clean.go`: `CleanHTML(s string, max int) string` with `golang.org/x/net/html` tokenizer: allowlist of 0038
   point 8; `a` keeps only `href` (http, https, mailto) and gets the fixed `rel`; plain-text input gets `<p>` per blank-line paragraph and
   links auto-detected only for `https?://` runs; output capped at 64 KiB on a tag boundary.
8. `internal/podcast/opml/opml.go`: `Parse(r io.Reader) ([]Outline{Title, XMLURL string}, error)` (1 MiB, nested outlines flattened,
   `type="rss"` or any `xmlUrl`, max 200, duplicates removed); `Write(w, title string, []Outline) error` (OPML 2.0, UTF-8).
9. Existing test `TestOnlineNoOtherClient` already skips the `online` subtree; when `internal/archtest/httpclient_test.go` (0037 plan)
   exists, make its allowed-path match a prefix so `online/podnet` is covered; add a test row naming `podnet`.
Tests (no network, `httptest` + a fake resolver/dialer):
- `podnet/addr_test.go` table: 127.0.0.1, ::1, 10.1.2.3, 172.16.0.1, 192.168.1.1, 169.254.169.254, 100.64.0.1, 0.0.0.0, ::ffff:127.0.0.1,
  64:ff9b::7f00:1, fd00::1, fe80::1, 198.18.0.1, 240.0.0.1 refused; 93.184.216.34, 2606:4700::1 allowed; port 8000 allowed,
  ports 22 and 25 refused; `user:pass@`; `file:`, `ftp:`, `gopher:`.
- `podnet/fetch_test.go`: name resolving to 127.0.0.1 refused at dial (the httptest server is never reached: assert hit count 0);
  redirect from allowed to private refused; 6 redirects refused; 16 MiB feed -> too_large; slow body (1 byte/s) -> slow within the
  deadline (inject short timeouts through an unexported test hook); `Set-Cookie` never sent back; request carries only UA, Accept,
  Range; 304 handling; 301 sets Moved. The test transport is reached only through `guardedDial` with a fake resolver that maps test
  names to the httptest listener's public-looking address (`DialContext` override still calls `checkAddr` on the claimed IP).
- `podnet/ondacast_test.go`: fake OndaCast shapes copied from the context section of 0038; bad slug makes no request; cache hit.
- `rss/parse_test.go` table, synthetic fixtures under `rss/testdata/`: RSS 2.0 minimal, iTunes full, Atom, Podcasting 2.0 chapters,
  no guid, duplicate guid, relative enclosure, malformed dates, windows-1252 feed, HTML-entity titles, `<script>` and `onclick` in notes,
  `javascript:` link, 3,500 items cut to 3,000, not-a-feed, empty.
- `opml/opml_test.go`: nested, 201 outlines cut, round trip.
Gates: `go test -race ./internal/metadata/online/... ./internal/podcast/...`, `make check`, `make build`.

---
## P2 (Go): data, jobs, share actions, proxy, routes, API
Migration `internal/db/migrations/0040_podcasts.sql`; `internal/db/db.go` `SchemaVersion = 40` (0039 is OIDC; if OIDC has not landed,
stop: `PLAN GAP: 0039 missing`). Tables (STRICT):
```
podcast_shows(id TEXT PRIMARY KEY, feed_url TEXT NOT NULL UNIQUE CHECK(length(feed_url) BETWEEN 8 AND 2048), title TEXT NOT NULL
  DEFAULT '', author TEXT NOT NULL DEFAULT '', notes_html TEXT NOT NULL DEFAULT '', site_url TEXT NOT NULL DEFAULT '', language TEXT NOT
  NULL DEFAULT '', art_url TEXT NOT NULL DEFAULT '', art_path TEXT NOT NULL DEFAULT '', art_sha TEXT NOT NULL DEFAULT '', ondacast_slug
  TEXT, etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '', fetched_at INTEGER, ok_at INTEGER, fail_code TEXT NOT NULL
  DEFAULT '', fail_count INTEGER NOT NULL DEFAULT 0, next_refresh_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)
podcast_episodes(id TEXT PRIMARY KEY, show_id TEXT NOT NULL REFERENCES podcast_shows(id) ON DELETE CASCADE, guid_key TEXT NOT NULL,
  guid TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, notes_html TEXT NOT NULL DEFAULT '', link TEXT NOT NULL DEFAULT '', published_at
  INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER, audio_url TEXT NOT NULL, audio_type TEXT NOT NULL DEFAULT '', audio_bytes INTEGER,
  season INTEGER, number INTEGER, kind TEXT NOT NULL DEFAULT 'full' CHECK(kind IN ('full','trailer','bonus')), chapters_url TEXT NOT
  NULL DEFAULT '', chapters_json TEXT, cache_path TEXT, cache_bytes INTEGER, cached_at INTEGER, gone_at INTEGER, created_at INTEGER NOT
  NULL, updated_at INTEGER NOT NULL, UNIQUE(show_id, guid_key))
  INDEX podcast_episodes_show(show_id, published_at DESC, id); INDEX podcast_episodes_cached(cached_at) WHERE cache_path IS NOT NULL
podcast_subscriptions(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, show_id TEXT NOT NULL REFERENCES
  podcast_shows(id) ON DELETE CASCADE, followed_at INTEGER NOT NULL, seen_at INTEGER NOT NULL, speed REAL CHECK(speed IS NULL OR speed
  BETWEEN 0.5 AND 3.5), skip_intro_s INTEGER NOT NULL DEFAULT 0 CHECK(skip_intro_s BETWEEN 0 AND 600), skip_outro_s INTEGER NOT NULL
  DEFAULT 0 CHECK(skip_outro_s BETWEEN 0 AND 600), server_auto INTEGER NOT NULL DEFAULT 0 CHECK(server_auto IN (0,1)), keep_latest
  INTEGER NOT NULL DEFAULT 3 CHECK(keep_latest BETWEEN 1 AND 50), PRIMARY KEY(user_id, show_id)) WITHOUT ROWID
  INDEX podcast_subscriptions_show(show_id)
podcast_episode_state(user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, episode_id TEXT NOT NULL REFERENCES
  podcast_episodes(id) ON DELETE CASCADE, position_ms INTEGER NOT NULL DEFAULT 0 CHECK(position_ms >= 0), played INTEGER NOT NULL
  DEFAULT 0 CHECK(played IN (0,1)), played_at INTEGER, favourite INTEGER NOT NULL DEFAULT 0 CHECK(favourite IN (0,1)), favourite_at
  INTEGER, last_played_at INTEGER, device_id TEXT NOT NULL DEFAULT '', client_updated_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER
  NOT NULL, PRIMARY KEY(user_id, episode_id)) WITHOUT ROWID
  INDEX podcast_state_history(user_id, last_played_at DESC) WHERE last_played_at IS NOT NULL; INDEX podcast_state_fav(user_id,
  favourite_at DESC) WHERE favourite = 1; INDEX podcast_state_episode(episode_id)
radio_stations(id TEXT PRIMARY KEY, stream_url TEXT NOT NULL UNIQUE CHECK(length(stream_url) BETWEEN 8 AND 2048), name TEXT NOT NULL,
  site_url TEXT NOT NULL DEFAULT '', art_url TEXT NOT NULL DEFAULT '', art_path TEXT NOT NULL DEFAULT '', codec TEXT NOT NULL DEFAULT '',
  bitrate_kbps INTEGER, country TEXT NOT NULL DEFAULT '', genre TEXT NOT NULL DEFAULT '', ondacast_slug TEXT, checked_at INTEGER,
  fail_code TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)
radio_favourites(user_id ... CASCADE, station_id TEXT NOT NULL REFERENCES radio_stations(id) ON DELETE CASCADE, position INTEGER NOT
  NULL, added_at INTEGER NOT NULL, PRIMARY KEY(user_id, station_id)) WITHOUT ROWID
radio_plays(user_id ... CASCADE, station_id ... CASCADE, last_played_at INTEGER NOT NULL, plays INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(user_id, station_id)) WITHOUT ROWID; INDEX radio_plays_recent(user_id, last_played_at DESC)
```
Radio tables ship now so stations never need a schema change before R1. Feed URLs are stored normalized: lower-case scheme and host,
default port removed, fragment removed.

Steps:
1. Migration file + `internal/db/migrate_0040_test.go` (from the 0039 fixture and from empty; existing rows survive; CHECKs reject a
   speed of 4, a kind 'video', skip 601) + fixture `testdata/v40.db` per the house fixture recipe (scout names it).
2. `internal/share/share.go`: actions `UsePodcasts = "podcasts.use"`, `ListenEpisode = "episode.listen"`, `UseRadio = "radio.use"`
   in `knownActions`; `TargetEpisode TargetKind = "episode"`. In `Can`, before the role switch: `UsePodcasts`/`UseRadio` need
   `TargetServer` and role owner, admin or member; `ListenEpisode` needs `TargetEpisode`, role owner/admin/member, and one SQL `EXISTS`
   (subscription to the episode's show, or a state row of `u.ID`). `canContributor` returns `decided=false` for these. Tests in
   `share_test.go`: each role x action, guest refused, disabled refused, other person's state row refused.
3. `internal/podcast/store.go`: `type Store`; methods `Follow(ctx, userID, feedURL, slug string) (showID string, created bool, err)`
   (limit 200 -> `ErrTooMany`), `Unfollow`, `Shows(ctx, userID) ([]ShowRow{..., NewCount int})` (NewCount = episodes with
   `published_at > seen_at`, not played), `Show(ctx, userID, showID)`, `Episodes(ctx, userID, showID, q EpisodeQuery{Sort
   "newest"|"oldest"; Filter "all"|"unplayed"|"in_progress"|"played"; Text string; Limit 50; After string cursor})`, `Episode`,
   `SaveProgress(ctx, userID, episodeID, p Progress{PositionMs, ClientUpdatedAt int64; DeviceID string; Finished bool})` (same
   newer-wins rule as `/progress/{bookID}`; finished sets played), `SetPlayed(ctx, userID, episodeID, bool)`, `SetFavourite`,
   `History(ctx, userID, limit)`, `Favourites`, `NewEpisodes(ctx, userID, limit)`, `MarkSeen(ctx, userID, showID)`, `ShowSettings` /
   `SetShowSettings(ctx, userID, showID, s ShowSettings{Speed *float64; SkipIntroS, SkipOutroS int; ServerAuto bool; KeepLatest int})`,
   `ApplyFeed(ctx, showID, rss.Feed, fetch FetchMeta) (added int, err)` (upsert by guid_key; vanished episodes get `gone_at`; prune past
   3,000 those without state rows), `Orphans` (no subscription, no state rows) for the prune job. Text search: `title LIKE ? ESCAPE '\'`
   on the show's rows only.
4. `internal/podcast/jobs.go`: kinds `podcast.refresh` (`{show_id}`, Concurrency 2, MaxAttempts 1), `podcast.art` (`{show_id}` or
   `{station_id}`, Concurrency 2, MaxAttempts 2), `podcast.cache` (`{episode_id}`, Concurrency 1, MaxAttempts 2), `podcast.import`
   (`{user_id, urls[]}`, Concurrency 1, MaxAttempts 1), `podcast.prune` (Concurrency 1). Refresh: conditional GET, parse, `ApplyFeed`,
   on new art URL enqueue `podcast.art`; failure sets `fail_code`, `fail_count+1`, `next_refresh_at = now + min(2^fail_count h, 24h)`.
   `jobs.Error{Code, Text}` texts in `internal/podcast/copy.go` (below). Moved: if `Moved` and the new URL is not another show's,
   update `feed_url`; else keep.
5. `internal/podcast/schedule.go`: `Scheduler.Run(ctx)` (same lifecycle as the backups schedule; scout names it): every 5 min, if
   enabled and `podcasts.refresh != "off"`, enqueue refresh for followed shows with `next_refresh_at <= now` (at most 50 per tick), then
   server auto-download (P5), then prune once a day. `RefreshStale(ctx, userID)`: shows of that person fetched over 1 h ago (called by
   GET /podcasts).
6. `internal/podcast/cache.go`: `CacheDir`; `Put(episodeID)` via `OpenAudio` to `<dir>/<showID>/.part-<episodeID>` then rename to
   `<episodeID>.<ext>` (ext from type: mp3, m4a, ogg, opus, aac, else bin); `Evict(ctx, limit)` order: unfollowed show, then played by
   every follower, then oldest `cached_at`. Files only under the dir; names only from ids.
7. Web routes (new files `internal/web/routes_podcasts*.go`, gated by `s.podcastsOn` -> 404, then `s.allow(UsePodcasts)`):
   `GET /podcasts` · `GET /podcasts/search?q=` · `POST /podcasts/follow` (`feed_url` or `slug`; 303 to the show; 422 toast for a bad
   address) · `GET /podcasts/shows/{showID}` (`sort`, `filter`, `q`, `after`) · `POST /podcasts/shows/{showID}/unfollow|refresh|seen|
   settings` · `GET /podcasts/episodes/{episodeID}` · `POST /podcasts/episodes/{episodeID}/played` (`played=1|0`) and `/favourite`
   (`on=1|0`) · `POST /podcasts/progress/{episodeID}` · `GET /podcasts/audio/{episodeID}` (`download=1` adds attachment) ·
   `GET /podcasts/art/{showID}?w=` · `GET /podcasts/chapters/{episodeID}` · `GET /hx/player/episode/{episodeID}` (P4 fills it) ·
   `GET /podcasts/opml` · `POST /podcasts/opml` · `GET|POST /admin/podcasts` (ManageServer). Each method-less fallback answers 405 like
   `/stream`. Search and follow use a per-person limiter (20/min, 30/h) -> 429 toast.
8. `internal/web/routes_podcasts_audio.go` `getEpisodeAudio`: Can(ListenEpisode) before anything; cached -> `http.ServeContent`; else
   semaphore (16 server, 3 per person; busy -> 503 `Retry-After: 5`, plain text); validate client `Range` (one range, `bytes=N-` or
   `bytes=N-M`, else ignore it); `OpenAudio`; refuse a `text/html` or `application/xhtml` upstream type (502 plain); copy the allowed
   headers, `Cache-Control: private, no-store`, `X-Content-Type-Options: nosniff`; if a range was asked with N>0 and upstream answered 200,
   pass it through and enqueue `podcast.cache` once. Upstream 4xx -> 404 plain; network -> 502 plain.
9. API for the app (`internal/web/routes_api_podcasts.go`, bearer device token, `apiLimiter`): `GET /api/v1/podcasts` (follows with
   settings and new counts), `GET /api/v1/podcasts/shows/{showID}/episodes?since=<ms>&after=`, `GET /api/v1/podcasts/episodes/{id}`
   (playback document: `{id, showId, title, show, artUrl, durationMs, audioUrl:"/api/v1/podcasts/audio/{id}", mime, bytes,
   chaptersUrl, state{positionMs, played, favourite, updatedAt}, settings{speed, skipIntroS, skipOutroS}}`), `GET /api/v1/podcasts/audio/
   {id}`, `GET /api/v1/podcasts/art/{showID}`, `GET /api/v1/podcasts/chapters/{id}`, `GET /api/v1/podcasts/new?since=`,
   `POST /api/v1/podcasts/progress/batch` (same body shape as `/api/v1/progress/batch`, `episodeId` instead of `bookId`),
   `POST /api/v1/podcasts/episodes/{id}/played|favourite`. Follow/search stay web pages (the app shows them in the WebView).
10. `internal/web/routes_matrix_test.go`, `routes_matrix_granted_test.go`: a row per route, per role (guest 404, member own 200,
    member other person's episode 404, off -> 404). `internal/app` wiring: build `podnet.New`, `podcast.Store`, register kinds, start
    the scheduler, cache dirs.
Tests: store (follow limit, newer-wins progress, filters, search, new counts, ApplyFeed upsert/gone/prune keeps state rows), jobs with a
fake feed server (`httptest` via the podnet test dialer), proxy (`Range` 206 through, upstream ignoring Range, cookie stripped, HTML
refused, semaphore 503, refused before any upstream hit for a stranger), migration test, OPML import -> jobs, admin settings owner only.
Gates: `go test -race ./internal/podcast/... ./internal/share/... ./internal/db/... ./internal/web/...`, `make check`, `make build`.

---
## P3 (UI): pages, nav, copy
No podcast board exists. Follow: nav item like Library (`internal/web/components/nav.templ` `MainNav`, key `podcasts`, label
"Podcasts", icon `podcast` added to the icon sprite, after Library; drawn only when enabled and `UsePodcasts`); show page follows
`screens/browse/SeriesPage`/`SeriesPhone` (header with art, then a list); episode rows follow the chapter/book list rows; Podcasts home
follows `screens/core/Home*` shelves; search follows `screens/browse/Search*`; admin page follows `/admin/backups`. New `ts-` classes
in `bundle.css` only; all four themes; 44 px targets; 13 px minimum.
Pages: `internal/web/pages/podcasts.templ` (tabs: Shows, New, Favourites, History via `?tab=`; empty state), `podcast_search.templ`
(OndaCast results with Follow; "Add by RSS address" disclosure with one field), `podcast_show.templ` (art, title, author, notes,
Follow/Following, Refresh, a "Settings for this show" disclosure: speed, skip intro, skip outro; the filter chips All, Unplayed, In
progress, Played; the sort segmented control Newest/Oldest; search field; rows with play, played mark, star, duration, date; "Load
more" link `after=`), `podcast_episode.templ` (notes, play, played, star, download link `?download=1` labelled "Save the file"),
`admin_podcasts.templ`. Components: `ShowCard`, `EpisodeRow`, `EpisodeFilters`. Badge: nav shows the sum of new counts, "99+" cap,
with an accessible label.
Copy (`internal/web/podcasts_copy.go`; BRAND voice; list in the report): "Podcasts"; "Find a podcast"; "Search podcasts"; "Follow";
"Following"; "Stop following"; "Add by RSS address"; "Paste the show's RSS address."; "Refresh"; "Getting episodes…"; "New";
"Favourites"; "History"; "Unplayed"; "In progress"; "Played"; "Mark as played"; "Mark as not played"; "Star"; "Newest first";
"Oldest first"; "Search this show"; "Skip the first … seconds"; "Skip the last … seconds"; "Speed for this show"; "No episodes yet.";
"You're not following any podcasts yet. Search for one, or paste its RSS address."; "Search isn't working right now. You can still
paste a show's RSS address."; failure texts by code: refused "Tendshelf can't fetch from that address. It must be a public web
address."; offline "Tendshelf couldn't reach this show's website. It will try again later."; not_found "This show's feed is gone from
its address."; not_a_feed "That address isn't a podcast feed."; too_large "This show's feed is too large to read."; slow "This show's
website is too slow to answer. It will try again later."; busy "This show's website asked Tendshelf to wait. It will try again
later."; bad_answer "This show's feed couldn't be read."; "This episode is a video, which Tendshelf doesn't play."; "Too many
podcasts at once. Wait a minute and try again."; "You follow 200 shows, the most Tendshelf keeps."; admin: "Podcasts and radio",
"Let people follow podcasts", "Check for new episodes", "Only when someone looks", "Every hour", "Every 6 hours", "Once a day",
"Room for saved episodes on this server", "Saved episodes use … of …", "Remove saved episodes".
Tests: handler tests for every page (200, 404 off, guest 404), a11y checks via the existing capture helper, visuals at 390/768/1024/1440
in dark and light (seeded ids), `docs/feature-map.md:17` -> Yes. Gates: `make generate`, `make check`, `make build`, the CSS budget test.

---
## P4 (UI + Go): player, Home shelf, OPML page, history
1. `GET /hx/player/episode/{episodeID}`: the same data attributes as the book fragment plus `data-kind="episode"`, `data-progress-href=
   "/podcasts/progress/{id}"`, one file `<li data-id data-href="/podcasts/audio/{id}" data-offset-ms="0" data-duration-ms data-mime
   data-playable>`, chapters from `chapters_json` (or none), `data-speed` = show speed or the person's default, `data-skip-intro-ms`,
   `data-skip-outro-ms`, `data-show-href`. Duration unknown -> 0, and the player takes `audio.duration` once known.
2. `internal/web/static/js/player.js`: `parsed.kind`; `streamUrl(file)` returns `file.href` when set; `progressUrl` returns
   `parsed.progressHref` when set; `fetchBook` asks `/hx/player/episode/` for an id starting `pe_`; skip intro on a start at 0; at
   `duration - skipOutro` behave as ended; ended marks played (the save sends `finished=1`); sleep timer "end of chapter" reads "End of
   episode" for an episode; `/hx/player` (last played) may answer an episode (server: newer of last book and last episode). Play
   buttons: `data-ts-play="pe_..."` works unchanged. Media Session: artwork `/podcasts/art/{showID}?w=512`.
3. Home: "New episodes" shelf (`NewEpisodes`, 10, hidden when none or off).
4. `/podcasts/opml` export (attachment `tendshelf-podcasts.opml`), import form on Podcasts (file input, `podcast.import` job, polling).
5. Chapters: `GET /podcasts/chapters/{id}` fetches via `podnet.Chapters` on first ask, stores `chapters_json` (Podcasting 2.0
   `chapters[].startTime,title` only), maps to `{startMs,title}`.
Tests: fragment shape test, progress newer-wins, browser test (opt-in `TENDSHELF_TEST_BROWSER`): follow a fake feed, play an episode
through the proxy, seek to 50 %, check a 206, reload, place restored; visuals of the player bar with an episode at the four widths.
Gates as P3 plus the JS budget test.

---
## P5 (Go + UI): one queue, web downloads, server auto-download
1. `internal/db/migrations/0041_queue_episodes.sql`, SchemaVersion 41: rebuild `queue_items(user_id, item_id TEXT NOT NULL,
   book_id TEXT REFERENCES books(id) ON DELETE CASCADE, episode_id TEXT REFERENCES podcast_episodes(id) ON DELETE CASCADE, position,
   added_at, CHECK((book_id IS NULL) <> (episode_id IS NULL)), PRIMARY KEY(user_id, item_id))` with `item_id` = the book or episode id;
   copy rows; keep the indexes; migration test. Queue store and `/api/v1/queue*` answer `kind` (`book`|`episode`) per item; existing
   book fields unchanged. Older apps must not break: the builder checks the app's queue parser; if it fails on an item with no
   `bookId`, `/api/v1/queue` leaves episode items out unless the request sends `?kinds=book,episode` (A1 sends it). "Play next"/"Add to queue" on episode rows; continue to the next queue item after an episode.
2. Web offline: Download on an episode stores `/podcasts/audio/{id}`, `/podcasts/art/{showID}`, `/podcasts/chapters/{id}`,
   `/hx/player/episode/{id}` in the person's cache through the existing pocket path; `sw.js` answers those four only from that cache
   (0038 point 9); tests in the sw test set: other `/podcasts/` and `/radio/` addresses pass to the network; sign-out clears.
3. Server auto-download: when `podcasts.server_auto` and a follower set `server_auto`, the scheduler enqueues `podcast.cache` for the
   newest `keep_latest` unplayed episodes; eviction by `podcasts.cache_bytes`; the admin page shows the use and "Remove saved episodes".
Gates: migration test, queue tests, sw tests, browser offline test (existing pocket spec extended).

## P6 (later): ID3 chapters from cached files; episode transcripts stay out.

---
## Android (first-class; `docs/plans/podcasts.md` is the contract, a scout maps each package)
The app never calls OndaCast or a feed host: it reaches only `/api/v1/podcasts/*` and the WebView pages of its server.
- **A1 native playback** (≈220 calls): `Api.kt` playback document for `pe_` ids; `BookMedia.kt`/`BookPlayer.kt` treat an episode as a
  one-file item (`MediaItem` id `pe_...`, artwork from `/api/v1/podcasts/art/`); `native-audio.js` passes episode ids through unchanged;
  `ProgressWriter.kt` + `SqliteOutbox.kt` queue episode saves to `/api/v1/podcasts/progress/batch` (offline safe); per-show speed and
  skip intro/outro applied in the engine; chapters from `chaptersUrl`; `Resume.kt` smart rewind applies; `SleepTimer.kt` "end of
  episode"; `AutoNext.kt` continues with the next queue item of either kind (after P5). Lock screen and notification come from the
  existing Media3 session. Tests: `android/shared` JVM tests for the document parser and outbox rows. Manual check rows: screen-off
  hour, lock-screen seek, Bluetooth buttons, speed per show.
- **A2 downloads and auto-download** (≈240): `Offline.kt` rows gain `kind`; Download on an episode row (WebView bridge, existing
  pattern); per-show "Download new episodes on this phone" (Wi-Fi only, `AUTO_HOLD` rule of L8, quota shared with L8's
  `AUTO_TOTAL_BYTES`, keep latest N, remove after played, never removes a person's own download); plays with no server; new-episode
  notification posted when the app syncs while open or playing, and by a periodic `androidx.work` job (unmetered network, battery not
  low, at most every 6 hours, only while the person has a show with auto-download on) that asks only this server for new episodes and
  downloads them. `androidx.work` gets its `NOTICES.md` row and Gradle verification entry in this package (orchestrator decision,
  2026-11-09: auto-downloads are downloads, so the existing approval covers it). Manual checks: airplane mode play, Wi-Fi off holds,
  quota, a new episode arrives with the app closed.
- **A3 Android Auto, widget, actions** (≈180): `LibraryTree.kt` nodes "Podcasts" -> "New", "Downloaded", "Shows" -> episodes;
  `TendWidget.kt` shows the episode and show art; long-press menu on episode rows (Mark as played, Star, Download, Play next) through
  the existing bridge where the WebView allows. Manual checks: DHU browsing, widget after reboot.
- **R3 radio on Android** after R2: live item (no seek bar, Live badge, ICY title in the notification).

## Radio (after P5)
- **R1 (Go)**: `internal/radio` store over the 0040 tables; `POST /radio/stations` (`slug` via `podnet.Station`, or `stream_url`
  checked by one 10 s `OpenLive` probe reading the content type: `audio/mpeg|aac|aacp|ogg|opus|flac`, HLS `.m3u8` refused "Tendshelf
  can't play this kind of stream yet."; `.pls`/`.m3u` playlists read for the first http(s) entry); `GET /radio/live/{stationID}`
  (semaphore shares the 16, 1 live per person, no Range, strips ICY blocks every `metaint` bytes, latest `StreamTitle` kept in memory
  per station, cleaned and cut to 200 runes, 12 h max session); `GET /hx/radio/now/{stationID}` (polled every 15 s); favourites
  (100 per person), plays; `/api/v1/radio/*`. Tests: ICY parser table, private stream address refused, HLS refused.
- **R2 (UI)**: Radio tab under Podcasts ("Radio" in nav only when `radio.enabled`), search via OndaCast `type=stations`, "Add by stream
  address", favourites, recent; player live mode (no scrubber, no skip, Live badge, title line from now-playing).

## Manual checks (`docs/manual-checks.md`, new section "Podcasts and radio")
Web phone 390: follow by search, follow by RSS address, play and seek a 2-hour episode, per-show speed survives reload, skip intro,
offline Download then airplane mode, sign-out clears. Android emulator (`-gpu host`): A1-A3 rows above, Android Auto DHU, widget.
Radio: a plain-http station plays on an https server; title updates.

## Out of scope (with reason)
Transcripts and clips to notes (later plan); near-me and genre browsing (OndaCast surface, not needed for a player); HLS radio (needs a
segment proxy); video playback; private/authenticated feeds and home-network feeds (SSRF, stored credentials); Radio Browser fallback
(0038 point 10); uploading podcasts into a library (cache is not library); bandwidth throttling (concurrency caps suffice); comments,
ratings, social; push notifications (no Firebase by rule).

## Settled questions
1. A2 uses `androidx.work` for background new-episode checks and downloads (AGENTS.md approves it "if downloads need it"; they do).
