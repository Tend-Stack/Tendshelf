# 0047 Casting: play on a Chromecast or Google TV, only for a person who turns it on
Status: accepted (operator decision, 2026-11-17). Operator answers to the plan's open questions (2026-11-17), all as recommended: (1) no casting from the Android app for now; (2) if the Cast API cannot set the speed, Speed is disabled while casting; (3) the Settings copy says casting needs an https address the TV can reach, with no workaround; (4) a token covers a whole book and lives at most 72 hours. Plan: `docs/plans/casting.md`. Parents: podcast feeds
([0031](0031-podcast-feeds.md), the token-in-the-path surface this copies), the player row ([0042](0042-player-native-row.md)),
podcasts and radio ([0038](0038-podcasts.md)), the Android app ([0024](0024-android-app.md)) and AGENTS.md "Security and
privacy". **Reopens** the "Chromecast" exclusion in `docs/plans/next-stage.md` ("Not to do").

## Context
The operator: "Chromecast: but as an option the user can activate themself and we explain what will happen from settings."
Casting needs Google's sender code, which breaks two rules as they stand: no third-party assets (the Web Sender SDK is a
Google script) and no Google Play services (the Android Cast SDK is proprietary). A Cast receiver also cannot send a
cookie, so it needs a credential in the address, which is a new token class. The answer keeps every rule true for anyone
who has not turned casting on, and keeps the exception narrow for those who have.

## Decision

1. **Off by default, per person.** A new `user_settings` key `playback.cast` (JSON `true`/`false`, default `false`) in
   Settings → Playback. The explanation sits above the switch, always visible (it is the consent, so not behind a
   disclosure). With it off, nothing changes: no Google script, the CSP is byte-identical, no `/casting/` address answers,
   and every `/cast/` address answers 404.
2. **Web only. No Android Cast for now.** The Android app keeps its no-Play-services build. See "Android" below.
3. **Web sender, Default Media Receiver.** The Cast Web Sender SDK
   (`https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1`) with
   `chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID`; no custom receiver and no Google Cast developer registration.
   A small first-party ES module, `internal/web/static/js/cast.js`, is the only code that touches it.
4. **When the script may load.** All of these must hold: the person turned casting on; the page is a full `Layout` page
   (not a share link `/s/`, not the sign-in frame); the window is a plain browser (`shellFrom(r) == ""`, so never the Wails
   window or the Android or iOS WebView); the page is a secure context (the SDK refuses otherwise); the browser is
   Chromium-based (`navigator.userAgentData.brands` names Chromium or Google Chrome); and the person opened the full player
   on this page load. Loading on the first open of the full player, not at page load, means Google is contacted only when
   the person is about to use a player. `Referrer-Policy: same-origin` (unchanged) means Google is not sent the page address.
   No Subresource Integrity: Google changes the file in place, so a pin would break casting without warning.
5. **CSP per response.** `pageCached` (the one place `Layout` is rendered) replaces the fixed policy with
   `contentSecurityPolicyCast` when rule 4's server-side half holds (on, browser). It differs only in
   `script-src 'self' https://www.gstatic.com`. From knowledge (to confirm in the P2 manual check): `cast_sender.js` loads
   `cast_framework.js` from `www.gstatic.com/cast/sdk/libs/sender/1.0/`, and Chrome on Android may load a script from
   `www.gstatic.com/eureka/clank/`, both covered. The sender talks to devices through Chrome's built-in Media Router, not
   page network requests, so no `connect-src`, `frame-src` or `img-src` change is expected. The `<google-cast-launcher>`
   element may inject a style, so Tendshelf draws its own button and never uses it. Any further source a real browser
   reports is added only with an addendum to this decision naming it.
6. **The Cast button** joins the full player's bottom row (0042: equal-width, icon over a 13 px label, "Cast"), after
   Sleep, `hidden` until the framework reports a device (`CastState` other than `NO_DEVICES_AVAILABLE`). From 1280 px it
   sits where the desktop bar carries Speed and Sleep. While casting it is `aria-pressed="true"`, its icon is in the
   accent with the label "Casting" (text changes too, never colour alone), and the meta line under the times reads
   "Playing on {device name}".
7. **Cast addresses: a new token class.** Random 32 bytes (`internal/token.New`), only the SHA-256 stored, in
   `cast_tokens` (migration `0048_cast_tokens.sql`). One token names one person and one target: a book (all its playable
   files), an episode, or (P3) a station. Lifetime: the target's length plus 6 h, at least 6 h, at most 72 h; a station
   12 h. At most 8 live tokens per person (minting a ninth deletes the oldest); every mint deletes expired rows. Deleting
   the row is revocation.
   - It reaches only `GET`/`HEAD /cast/{token}/...` on the outer mux, outside the cookie chain like `/feeds/`: nothing
     there reads or sets a cookie, calls `currentUser` or accepts a bearer token. It never becomes a session and never
     reaches a page, `/hx/`, `/api/`, `/stream/`, `/feeds/` or `/me`.
   - `share.Can` is checked at mint (as the signed-in person) and again on every fetch (as the token's owner), along with:
     owner exists and is not disabled, owner still has `playback.cast` on, the token's kind matches the route, and for
     episodes podcasts are on. Every refusal is a plain 404; an unknown token costs the address one failure on the existing
     key limiter and one `cast.failed` audit row without the token, as feeds do.
   - Revoked: on sign-out (all the person's cast tokens, any session), when casting is turned off, on user delete
     (cascade), and by expiry.
8. **Kinds in the first package: books and episodes.** Stations follow as P3 because the live relay holds a per-person
   stream slot and runs without end, so the hand-over from the browser to the receiver needs its own tests.
9. **A book is cast as one queue of its files.** `QueueLoadRequest` with every playable file in order, starting at the
   file that holds the place. One file at a time would need the sender to reload at every file boundary, which fails once
   the sender tab sleeps. Chapters are not sent: the Default Media Receiver cannot show them, and the page keeps drawing
   chapters from the book position as it does now.
10. **Progress is saved by the page, from the receiver's position.** The page posts the existing progress endpoints
    (`POST /progress/{bookID}`, an episode's `ProgressHref`) through player.js's own save path, fed with the remote
    player's position. The receiver cannot save (no cookie, and the cast token is GET only). Limit, named in the copy: if
    the tab is closed while the TV plays, the place saved is the last one before it closed.
11. **Reachability.** The receiver fetches `https://{the host in the browser's address bar}/cast/...` itself, so that host
    must resolve and be reachable from the TV with a publicly trusted certificate. A loopback or `localhost` host
    (`reachableHost`, decision 0028) or an insecure request is told so on the settings page and answers the mint with 409.
    A tailnet name (`*.ts.net`) is not reachable from a TV that is not on the tailnet; the settings copy says the TV must
    reach "this address".

## Android
- **(a) Two product flavors**, `full` with `play-services-cast-framework` and `media3-cast`, `fdroid` without. Costs:
  operator approval for at least two proprietary modules plus their transitive Play services and `androidx.mediarouter`
  modules, each with a `NOTICES.md` row and Gradle verification entries; an amendment to the AGENTS rule "No Google Play
  services"; a second APK and a channel for it (the GitHub APK must stay the `fdroid` build so F-Droid's reproducible
  build matches it, docs/android.md "Store metadata"); a Cast `MediaRouteButton` and session code beside the existing
  Media3 session; and every Android test run twice. The `full` APK links Google's code whether or not the person turns
  casting on, which is not the "option the user activates" the operator asked for.
- **(b) No Android Cast for now.** Cost: none in code. A person on Android can cast from Chrome on the phone (the web sender
  is meant to work in Chrome for Android; a guess until the P2 manual check) or use Bluetooth.
- **Chosen: (b).** It stays inside what the operator said yes to and needs no new module. The app's settings page shows the
  switch with one extra line: "Casting works in Chrome, not in this app."

## Consequences
- AGENTS.md gains: the cast token rule beside the feed token rule; "no third-party assets" names this one exception; the
  service worker's never list gains `/cast/` and `/casting/`. `docs/architecture.md` gains the routes and the token class.
- No server outbound request is added; no `http.Client` is created. No module is added, so no `NOTICES.md` row (Google's
  script is fetched by the person's browser, not shipped).
- Out of scope: Android Cast, AirPlay (Safari's own audio element already offers it), a custom receiver, casting from share
  links, casting speed changes if the sender API cannot set them (see the plan), queueing the next book on the receiver.
