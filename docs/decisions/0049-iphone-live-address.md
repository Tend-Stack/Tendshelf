# 0049 iPhone radio: a live address bound to one device and one station
Status: accepted (operator pre-approved the recommendation, 2026-11-19). Builds on the iPhone app
([0048](0048-iphone-app.md)), casting's token-in-the-path surface ([0047](0047-casting.md), table `cast_tokens`, migration
0048), radio ([0038](0038-podcasts.md)) and HLS stations as one stream ([0039](0039-hls-radio.md)). Plan: I2r in
[docs/plans/iphone-audio.md](../plans/iphone-audio.md).

## Context
0048 sends every iPhone media request through `AVAssetResourceLoaderDelegate` with opaque `tsmedia://` handles, so the
device token stays in an `Authorization` header. I2b found books and episodes play that way, but a live station does not:
the resource loader models a file of known length. With a guessed length AVFoundation probes offsets in the billions and
opened 69 to 82 upstream connections in 50 s without reaching `.playing`; with length 0 it stops after the two-byte probe.
Stub-served ADTS AAC and MP3 passed, so the failure is the loader's file model meeting an endless stream, not the codec.
AVPlayer itself plays an endless progressive HTTP stream (the server's relay answer: 200, no length, `Accept-Ranges: none`)
when it fetches the URL itself; it then needs the credential in the URL.

## Options weighed
- **(a) A scoped live address in the URL, reusing cast tokens (chosen).** The relay route `/cast/{token}/live` already
  exists, reaches one station, is GET/HEAD only, sits outside the cookie chain, is rate limited per address, audited on a
  guess, left out of the access log and the service worker, and runs the same slot, guarded dial and radio-off stop as
  the player. The change is one column (the device), one mint endpoint for the device token and one gate branch. No new
  route, table, token class or relay.
- **(b) Stations as HLS for the app.** Needs a server segmenter for MP3/AAC/ICY streams (0039 goes the other way: HLS in,
  one stream out), segment storage and timing. The resource loader is meant for playlists and keys; media segments behind
  a custom scheme are, by wide report, not served through the delegate (guess, not tested), so segments would need
  https URLs, and so a token in the URL anyway. Most code, same rule change.
- **(c) A loopback HTTP proxy in the app.** Another app on the phone can connect to a loopback port (guess for iOS, true
  on every Unix), so the proxy needs a secret in its URL too; plus a listener that must survive backgrounding, a second
  HTTP server to harden, and request parsing in Swift. More code, a new local attack surface, same kind of URL secret.
- **(d) Decode ourselves** (`URLSession` with the bearer, `AudioFileStream` into `AVAudioEngine`): keeps the current rule,
  but rebuilds buffering, stall recovery, interruption and route handling AVPlayer gives for free, for MP3 and AAC
  separately. Kept as the fallback if (a) shows a gap; not chosen because it is the most code for one surface.
- **(e) Fix the loader's live mode.** I2b tried the lengths that exist (huge, 0); the delegate has no "unbounded" answer.

## Decision
1. **A live address is a `cast_tokens` row with `device_id` set** (migration `0049_cast_tokens_device.sql`: nullable
   `device_id` referencing `devices(id)` ON DELETE CASCADE, allowed only when `kind = 'station'`). Random 32 bytes,
   SHA-256 stored, as every cast token.
2. **Minted only by `POST /api/v1/radio/live/{stationID}/address`** with a `mobile` device token (a cookie session or any
   other caller gets 403), after the same checks as `GET /api/v1/radio/live/{id}`: radio on, `share.UseRadio`, the
   person's own station. The answer is the path only, `{"path": "/cast/<token>/live", "expiresAt": <ms>}`; the app joins it
   with its pinned origin, so no `Host` header is trusted.
3. **One per device, one hour.** A mint deletes that device's earlier address in the same write; life is
   `share.LiveAddressLife = time.Hour`. A stream already open keeps flowing (as for every cast token, only the start of a
   request is checked); the app mints a fresh address for every connect, so expiry only bounds a leaked address.
4. **Reaches only `GET`/`HEAD /cast/{token}/live`.** The kind is `station`, which the cast audio, episode and art routes
   already refuse. It is never a bearer token, a cookie or a session, and never reaches `/api/`, `/hx/`, a page,
   `/stream/`, `/feeds/` or `/me`.
5. **Refused once its device token is revoked, by any path**: the lookup joins `devices` and treats a revoked device (or
   one with no token hash) as expired (404, no guess penalty). The app's own sign-out (`DELETE /api/v1/auth/token`) and a
   revoke on the Devices page also delete the device's rows; removing the device or the person cascades.
6. **Casting stays separate.** A live address does not need casting on; turning casting off and a browser sign-out delete
   only the person's rows without a device (the casting tokens), as before.
7. **The iPhone plays a station with a plain `AVPlayerItem`** on that URL, no cookies (`AVURLAssetHTTPCookiesKey` empty),
   `allowsExternalPlayback = false` (the URL is never handed to an AirPlay video receiver; AirPlay audio still routes
   through the audio session). The device token still never reaches a URL. Books and episodes stay on the resource loader;
   its live branch is removed. Android is unchanged (it keeps the bearer on `GET /api/v1/radio/live/{id}`).

## Rules that change (AGENTS.md), exact wording
- **Security and privacy**, the cast line "A cast address token (decision 0047) reaches only `/cast/{token}/` (GET, HEAD),
  never a page, `/hx/`, `/api/`, `/stream/`, `/feeds/` or `/me`, never becomes a session, and is deleted at sign-out and
  when casting is turned off." becomes: "A cast address token (decision 0047) reaches only `/cast/{token}/` (GET, HEAD),
  never a page, `/hx/`, `/api/`, `/stream/`, `/feeds/` or `/me`, never becomes a session, and is deleted at sign-out and
  when casting is turned off. The iPhone app's live address (decision 0049) is a cast token for one station bound to one
  device token: minted only by that device token at `POST /api/v1/radio/live/{stationID}/address`, it reaches only
  `/cast/{token}/live` (GET, HEAD), lives one hour, is one per device (a new one deletes the old), is refused as soon as
  its device token is revoked and deleted when the app signs out; it does not need casting on."
- **Security and privacy**, the token sentence "Session, invite, sign-in link, podcast feed, cast and API tokens are random
  32-byte values" is unchanged (a live address is a cast token).

## Consequences
- 0048's "the token never reaches a URL" stays true; what reaches a URL is a one-station, one-device, one-hour address,
  which is the same exposure casting already accepted, narrower (bound to a device, shorter life).
- A leaked live address lets its holder hear one station through this server for at most an hour, until the next connect
  or sign-out; it reveals no title, account or other station. Exposure points: the URL lives in AVFoundation's memory and
  possibly in the system log as `<private>` (guess); the server's access log already drops `/cast/` paths.
- The person's one live place (`radio.Acquire`) applies: if AVPlayer opens a second request while its first is still held,
  that request gets 503. I2r measures it first.
