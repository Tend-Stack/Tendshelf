# Plan: casting (decision 0047)
Builder contract. Decision: `docs/decisions/0047-casting.md` (read it first). Precedent to copy: podcast feeds,
`internal/share/feeds.go`, `internal/web/routes_feeds.go` (outer mux, `feedRoute`, `feedGuess`, `feedsFront`).

## Invariants
1. Casting off (the default): CSP byte-identical to `contentSecurityPolicy`, no `cast.js`, no gstatic URL in any answer, `POST /casting/address` and every `/cast/` path answer 404.
2. A cast token reaches only `GET`/`HEAD /cast/{token}/...`; never a page, `/hx/`, `/api/`, `/stream/`, `/feeds/`, `/me`; never sets or reads a cookie; never becomes a session.
3. `share.Can` at mint (signed-in person) and at every fetch (token owner); a refusal is a plain 404 with no existence leak.
4. Only the SHA-256 is stored; the raw token is never logged, audited or put in a redirect `Location`.
5. Sign-out, turning casting off and user delete remove every cast token of that person.
6. Never in a shell: `shellFrom(r) != ""` means the base CSP and no `cast.js`, whatever the setting.
7. Library files only read; no new server outbound request; no new module.

## Decisions (one line each)
- Token scope book/episode/station, not one file: a book's queue needs all files, and `share.Can` is per book.
- Delete-is-revoke, no `revoked_at`: there is no listing UI, and tokens live 72 h at most.
- Sender gets only `{base, expiresAt}`; it builds file URLs from the player data it already has (`Files`, `Mime`, `MayNeedCompat`).
- player.js keeps saving progress; casting only feeds it the remote position (no second save path).
- `feedsFront` is extended to `/cast/`, not copied.
- Stations wait for P3 (stream slot hand-over); Android Cast is not built (0047 "Android").

## Data shapes
`internal/db/migrations/0048_cast_tokens.sql` (lands after 0047; if 0047 is not on main yet, stop and report `PLAN GAP`):
```sql
-- cast_tokens: a short-lived address a Cast device uses to fetch one book, episode or station (decision 0047). Only the SHA-256 is stored.
-- target_id has no foreign key: a target that is gone answers 404 until the row expires.
CREATE TABLE cast_tokens (token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('book','episode','station')), target_id TEXT NOT NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, CHECK (expires_at > created_at)) STRICT, WITHOUT ROWID;
CREATE INDEX cast_tokens_user ON cast_tokens(user_id, created_at);
CREATE INDEX cast_tokens_expires ON cast_tokens(expires_at);
```
- Setting: `users.Settings.Cast bool`, key `keyCast = "playback.cast"`, default `false`, form name `cast` (`"1"`/`"0"`).
- `internal/share/cast.go`: consts `CastBook="book"`, `CastEpisode="episode"`, `CastStation="station"`, `CastMalformed`, `CastUnknown`;
  `castMaxLive = 8`, `CastMargin = 6*time.Hour`, `CastMaxLife = 72*time.Hour`, `CastStationLife = 12*time.Hour`;
  `type CastToken struct{ UserID, Kind, TargetID string; CreatedAt, ExpiresAt int64 }`;
  `var ErrCastUnknown, ErrCastExpired`; `type CastTokens`; `NewCastTokens(st *db.Store, now func() time.Time) *CastTokens`;
  `CastLife(lengthMs int64) time.Duration` (= clamp(length + margin, margin, max));
  `(*CastTokens) Mint(ctx, userID, kind, targetID string, life time.Duration) (tok string, expiresAt int64, err error)`: one write tx: delete expired rows (all users), insert, delete this user's rows beyond the newest 8;
  `Lookup(ctx, tok) (CastToken, reason string, err error)` (malformed/unknown -> `ErrCastUnknown`; past `expires_at` -> `ErrCastExpired`);
  `DeleteForUser(ctx, userID) error`.
- Mint: `POST /casting/address` (cookie chain, CSRF header), form `kind` (`book`|`episode`), `id`. 200 JSON, `Cache-Control: no-store`:
  `{"base":"https://{r.Host}/cast/{token}","expiresAt":<ms>}` (scheme from `s.proxies.Secure(r)`). Errors through `jsonError`:
  404 `not_found` (casting off, no right, unknown target, podcasts off), 400 `invalid` (kind or id), 409 `unreachable` (`!reachableHost(r.Host)` or `!s.proxies.Secure(r)`), 415 `unsupported` (not form-encoded).
- Fetch routes (outer mux, `GET` and `HEAD`; any other method on them 405 with `Allow: GET, HEAD`; any other `/cast/` path plain 404):
  `/cast/{token}/audio/{fileID}` (book; `?compat=1` allowed via `compatParam`; file must belong to the book and be `streamable`),
  `/cast/{token}/episode` (episode audio), `/cast/{token}/art` (book cover or the episode's show art), P3: `/cast/{token}/live`.
  Headers on every answer: `Cache-Control: private, no-store`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`,
  `Content-Security-Policy: default-src 'none'; sandbox`, `Access-Control-Allow-Origin: *` (no credentials header; guess that a receiver may need it; harmless because the address is the credential).
- CSP: `contentSecurityPolicyCast` = `contentSecurityPolicy` with `script-src 'self' https://www.gstatic.com`, nothing else.
- JS events (cast.js <-> player.js, on `document`): `ts:cast:begin` {device}, `ts:cast:position` {positionMs, paused}, `ts:cast:end` {positionMs}, `ts:cast:command` {action: "play"|"pause"|"seek", positionMs}.

## P1: tokens, `/cast/` routes, setting (Go + templ; about 200 tool calls)
1. `internal/db/migrations/0048_cast_tokens.sql`: as above. Migration test in the existing migrations test file, pattern of 0046: `TestMigration0048CastTokens` (from the 0047 fixture and from empty, data survives, checks reject a 31-byte hash and an unknown kind).
2. `internal/share/cast.go` + `cast_test.go`: as above. Tests: `TestCastMintLookup`, `TestCastExpired`, `TestCastPruneKeepsNewestEight`, `TestCastMintSweepsExpired`, `TestCastDeleteForUser`, `TestCastStoresHashOnly` (no row column equals the raw token), `TestCastLifeClamp` (0 -> 6 h, 40 h -> 46 h, 100 h -> 72 h).
3. `internal/users/settings.go`: `Cast` field, `keyCast`, load/save like `keyQuietSkip`. Test `TestSettingsCastDefaultOff`.
4. `internal/web/me_views.go`: `flag("cast", &next.Cast)`; settings view fields `Cast`, `CastSecure bool` (`s.proxies.Secure(r)`), `CastReachable bool` (`reachableHost(r.Host)`).
5. `internal/web/routes_me.go` `postSettings`: when `cur.Cast != next.Cast`, set `HX-Refresh: true` (the CSP belongs to the document); when it goes off, `s.casts.DeleteForUser` before saving answers. Error -> `serverError`.
6. `internal/web/pages/settings.templ` Playback pane: a new `ts-group` after the night-sleep group with the copy below and `@switchRow("st-cast", "cast", ...)`. `make generate`.
7. `internal/web/routes_auth.go` `postLogout`: `s.casts.DeleteForUser(ctx, sess.UserID)` after `s.auth.Logout`; a failure is logged, sign-out still completes.
8. `internal/web/routes_podcasts_audio.go`: extract the body after `loadEpisode` into `serveEpisodeAudio(w, r, ep podcast.Episode, download bool)`; `getEpisodeAudio` calls it (no behaviour change). Same for the show art handler -> `serveShowArt(w, r, ownerID, showID string)`. If either reads `currentUser` deeper than the loader, report `PLAN GAP`.
9. `internal/web/routes_cast.go` (new): `registerCast(outer)`, `castRoute(kind, h)` = linkHeaders + cast headers, `s.keyLimiter.Allow(ip)` (429), `Lookup` (unknown: `castGuess` = `keyLimiter.Fail` + audit `cast.failed` {ip, reason}; expired: 404, no fail), owner by id (missing/disabled 404), owner `Cast` on, kind matches, then the handler with `castReq{tok share.CastToken; owner users.User}`. Handlers: `getCastAudio` (book via `s.lib.Book`, `share.Can(owner, ListenBook)`, `f.BookID == TargetID`, `streamable`, `MissingSince == 0`, `s.serveAudio(w, r, f, compat, "private, no-store")`), `getCastEpisode` (podcasts on, `share.Can(owner, ListenEpisode)`, `s.podcasts.Episode(ctx, owner.ID, id)`, `serveEpisodeAudio(..., false)`), `getCastArt`. `postCastAddress` on the cookie mux. Every 404 is `http.Error(w, "Not found.", 404)`; never `s.notFound` (it can render a page).
10. `internal/web/server.go`: field `casts *share.CastTokens`; `s.registerCast(outer)` next to `registerFeeds`; extend `feedsFront` to also clean `/cast/` (rename `tokenPathsFront`, mechanical); `contentSecurityPolicyCast`; `func (s *server) castOn(r) bool` (signed in, `shellFrom(r) == ""`, settings `Cast`); in `pageCached` set the cast CSP when `castOn`, and `components.Page.Cast = castOn`. One settings load per page: reuse `lookFor` if it already loads settings.
11. `internal/web/static/js/sw.js`: add `"/cast/", "/casting/"` to `PASS`.
12. Tests (`internal/web/routes_cast_test.go`, new, plus named extensions):
    `TestCastTokenOnlyOnCastPaths` (the raw token as path segment, `?token=`, `Authorization: Bearer` and cookie value at `/stream/{id}`, `/hx/player/{bookID}`, `/api/v1/books`, `/me`, `/`, `/podcasts/audio/{id}`, `/feeds/{tok}/feed.xml`: never 200 with audio, never `Set-Cookie`),
    `TestCastNoCookieEitherWay` (a valid session cookie plus a bad token is 404; a good token answers with no `Set-Cookie`),
    `TestCastMintForbiddenRoles` (guest and member without a grant, contributor on another's book, disabled user, casting off: 404; owner with right: 200),
    `TestCastFetchRechecksGrant` (grant removed after mint: 404), `TestCastRevokedOnSignOut`, `TestCastRevokedWhenTurnedOff`, `TestCastOffAtFetch` (setting flipped in the DB directly: 404),
    `TestCastExpired`, `TestCastWrongKind`, `TestCastOtherBooksFile`, `TestCastMethods` (POST 405 + Allow), `TestCastPathClean` (`/cast/x/../audio/y`: 404, no Location),
    `TestCastUnreachableHost` (Host `127.0.0.1:8095` and `localhost`: 409; insecure: 409), `TestCastGuessLimited` (429 after the limit; audit row has no token), `TestCastRangeAndCompat` (206 with Range; `?compat=1` routes to the compat copy with the fake ffmpeg).
    `routes_stream_test.go`: `TestStreamRefusesCastToken`. `podcasts_test.go`: `TestCastEpisode` (+ podcasts off 404) and the existing episode tests stay green after the refactor.
    `server_test.go` (beside the CSP test at :34): `TestCSPCastOnlyWhenOn` (off: exact base string; on in a browser: cast string; on with shell `desktop`, `android`, `ios`: base), `TestCastScriptOnlyWhenOn` (`js/cast.js` and `gstatic` absent from every Layout page when off or in a shell).
    `oidc_routes_test.go` pattern -> `TestServiceWorkerNeverCast` in `server_test.go` (regexp on `PASS` for both prefixes).
    Settings: `TestSettingsCastSwitch` (copy present; save on -> `HX-Refresh`; save off -> tokens gone).
13. Docs (same commit as the code): AGENTS.md (see Doc edits), `docs/architecture.md`, `CHANGELOG.md`, `docs/plans/next-stage.md` lines 69-70 (replace the "Chromecast" bullet with "Chromecast: opt-in per person, web only, decision 0047"), roadmap status note. Gates: `make check`, `make build`.

## P2: web sender and the Cast button (JS + templ; about 200 tool calls)
1. `internal/web/components/icons`: a `cast` icon drawn for Tendshelf (screen outline, three arcs, 24 px grid, `currentColor`); no copied glyph.
2. `internal/web/components/player_full.templ`: after the Sleep button, `<button type="button" class="ts-player-bar__item" data-ts-cast hidden aria-pressed="false" aria-label={ m.CastLabel }>` with `@Icon("cast", false)` and `<span class="ts-player-bar__label" data-ts-cast-label>{ m.Cast }</span>`; a `<span class="meta" data-ts-cast-where hidden></span>` under the times. Same button where the desktop bar carries Speed and Sleep from 1280 px (find the Sleep button's sibling there). Messages in `playerMsgs()`.
3. `internal/web/components/layout.templ`: when `p.Cast`, `<script type="module" async src={ p.Assets.URL("js/cast.js") }></script>` after `chapter-bar.js`. Not in `LinkShell` or `Frame`.
4. `internal/web/static/js/cast.js` (new, readable, commented): exit unless `isSecureContext` and a Chromium brand; on the first full-player open set `window.__onGCastApiAvailable` then insert the gstatic script; init `CastContext` with `DEFAULT_MEDIA_RECEIVER_APP_ID`, `autoJoinPolicy: ORIGIN_SCOPED`; show the button on `CAST_STATE_CHANGED` != `NO_DEVICES_AVAILABLE`; click -> `requestSession()`; on `SESSION_STARTED`: POST `/casting/address`, build a `QueueLoadRequest` (book: one `MediaInfo` per playable file, `contentId = base + "/audio/" + id` (+`?compat=1` when the local player uses compat), `contentType` = file mime, metadata title/author/`images:[base+"/art"]`, start at the file holding the place, `startTime` = place − `OffsetMs`; episode: one item `base + "/episode"`), `playbackRate` = current speed; fire `ts:cast:begin`. Map `RemotePlayerController` `CURRENT_TIME_CHANGED`/`IS_PAUSED_CHANGED` + current queue index to `ts:cast:position`; answer `ts:cast:command`; on session end or `MEDIA_STATUS` idle/error fire `ts:cast:end` with the last position. On a 409 or load failure, end the session and toast.
5. `internal/web/static/js/player.js`: on `ts:cast:begin` pause local audio and set `casting`; while casting, transport, scrub, skips and the sleep timer send `ts:cast:command`, the clock and the existing progress save read the `ts:cast:position` value; on `ts:cast:end` seek local audio to the place, stay paused, save once. No other change.
6. CSS in `bundle.css` (`ts-` classes, tokens only): pressed state = accent icon. Row fits at 390 with six items (0042 equal width, at least 44 px); large text keeps its two-column grid.
7. Speed: if `playbackRate` on load or a `SET_PLAYBACK_RATE` media message does not work on a real device (uncertain), Speed is disabled while casting with the label "1×", and the report says so.
8. Tests: `static_gzip_test.go:20` list gains `js/cast.js`; `perf_weight_test.go` budgets unchanged (perf pages run with casting off; assert no `cast.js` request); `motion_test.go` passes for `cast.js`; `play_buttons_test.go` and `TestPlayerNativeRow` with the cast button shown (forced via a test hook attribute) fit at 390/768/1024/1440; `player_browser_test.go`: `TestPlayerCastHiddenWithoutSDK` (on, gstatic unreachable in the test: button stays hidden, no page error, local playback unchanged), `TestPlayerCastEventsDrivePlayer` (synthetic `ts:cast:*` events: local audio pauses, clock follows, a progress POST carries the cast position, end seeks local). Browser gates with `TENDSHELF_TEST_BROWSER` set; grep SKIP.
9. Manual check (record in roadmap): Chrome desktop and Chrome Android, a Chromecast or Google TV, server on an https name the TV resolves: single-file m4b, multi-file mp3, episode; play/pause/seek/skip/sleep; place saved; stop -> local paused at the place; zero `securitypolicyviolation` reports in the console (any report becomes a 0047 addendum, not a silent CSP edit); casting off mid-play stops the TV.

## P3: stations (Go + JS; about 120 tool calls)
`/cast/{token}/live`: `share.Can(owner, ListenStation)` (use the action `loadStation` uses), the owner's station, `s.radio.Acquire(owner.ID, st.ID)` after cast.js has stopped local play (it waits for the local stream to close), the existing relay. Mint `kind=station`, life `CastStationLife`; MediaInfo `streamType: LIVE`. Tests in `radio_test.go`: `TestCastStationLive`, `TestCastStationBusy` (503 + Retry-After while local holds the slot), `TestCastStationRadioOff`.

## Settings copy (Playback pane; exact words)
Group heading: "Cast to a TV or speaker". Lead: "Play on a Chromecast, a Google TV or a speaker with Google Cast. This is off until you turn it on. When it is on:"
- "Google's casting code runs in this browser when you open the player."
- "Your TV or speaker gets the sound straight from this server, through a private address that works only for that book or episode, and only for a short while."
- "Google may learn that you cast something. The title and cover go to the TV so it can show them."
- "Your TV must be able to reach this server at the address you use now, and it works in Chrome and browsers built on it."
Switch: label "Cast to a TV or speaker", hint "Off: nothing from Google loads." After the list: "Turning it off stops Google's code loading and ends every casting address at once. Anything playing on a TV stops soon after."
Conditional notes (`caption`): not secure: "This address is not secure (https), so casting can't start here." Not reachable: "This server is open only on this computer, so a TV can't reach it." In a shell: "Casting works in Chrome, not in this app."
Player words: button "Cast", aria "Cast to a TV or speaker"; casting: "Casting", "Playing on {device}". Toasts: 409 "Your TV can't reach this server at this address. The book still plays here."; failure "Couldn't start casting. The book still plays here."; TV error "The TV couldn't play this. It still plays here." Tab-close limit (hint under the meta line, first cast only): "Keep this page open so your place is saved."

## Doc edits (P1)
- AGENTS.md "Security and privacy": add "A cast address token (decision 0047) reaches only `/cast/{token}/` (GET, HEAD), never a page, `/hx/`, `/api/`, `/stream/`, `/feeds/` or `/me`, never becomes a session, and is deleted at sign-out and when casting is turned off." In "No telemetry...": "...third-party assets (one exception: Google's Cast sender script, loaded in a browser only for a person who turned casting on, decision 0047)". Service worker never list: add `/cast/...`, `/casting/...`. Session-token sentence: add "cast" to the 32-byte list.
- `docs/architecture.md` "HTTP routes": rows for `POST /casting/address` and the four `/cast/{token}/...` routes; "Auth and sharing": default-deny list gains `/cast/{token}/`, the token class paragraph, the CSP exception; "Data model": `cast_tokens`.
- `CHANGELOG.md` `## Unreleased`: "Cast to a Chromecast or Google TV from Chrome. It is off until you turn it on in Settings → Playback, which explains what changes."

## Out of scope
Android Cast (0047 "Android"); AirPlay; a custom receiver; casting from share links `/s/` or the desktop/Android windows; chapters on the TV; queueing the next book on the receiver; a token list or revoke-one UI; casting with the tab closed saving progress.
