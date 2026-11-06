# A2-A5 Android shell, playback, bridge, sync (plan, 2026-11-06)

For a builder. Parent: [android.md](android.md), [android-a1.md](android-a1.md) section 8, decisions [0024](../decisions/0024-android-app.md),
[0026](../decisions/0026-app-sign-in.md), [0011](../decisions/0011-native-feel.md). A1 is merged (49ab0d2). "Guess" marks what only the emulator can settle.
Order: A2 and A3 in parallel; A4 after both; A5 after A3 (after A4 if the page prompt is to be seen; see A5 D5). Corrections to `android.md`: the web player
is **remote-driven at book level**, not a fake file-level `audio`; a book is **one Media3 window**, not one item per file.

## 0. Rules for all four packages

**Parallel safety (A2 and A3 must not meet).** A2 owns `MainActivity.kt`, `AndroidManifest.xml`, `app/build.gradle.kts`, `gradle/*`, `res/*`, `NOTICES.md`. A3 owns
`PlaybackService.kt`, `Api.kt`, `shared/ApiPaths.kt`, `shared/Manifest.kt`, `shared/Position.kt`, `SharedTest.kt`. New files only otherwise. `MainActivity` keeps its name (A3's
`PendingIntent` names it). Both add this file with **byte-identical** content, so git merges it without conflict:
```kotlin
package app.tendshelf.android

/** The one server and credential the native side uses. Never handed to the WebView. */
data class Connection(val origin: String, val token: String, val deviceId: String)

fun interface ConnectionSource { fun current(): Connection? }
```

**Emulator is shared.** Only one package on `emulator-5554` at a time; acceptance runs inside `flock -w 3600 .local/tests/android/emulator.lock`. Test servers: A2 port 8097, A3 8098, A4 8097, A5
8098, hostile pages 8099, all on 127.0.0.1 from the worktree's own build with `-addr`, under `.local/tests/android/<pkg>/<run-id>/`, deleted after the run. **Never port 8095, never `~/.local/share/tendshelf`.**
Reached from the emulator as `http://10.0.2.2:<port>`.

**Test tools (A2 creates, the others reuse).**
- `android/tools/test-server.sh <port> <dir>`: `go build` the server, generate synthetic audio with ffmpeg, start it, create owner `tester` and member `member`. Library: **Parts** (3 MP3 files of 5 min, 6 chapters, one
  chapter crossing a file boundary), **Single** (one M4B of 10 min, 4 chapters), **Short 1** and **Short 2** (30 s MP3, series "Shorts", 1 and 2), **Odd** (one Ogg Opus file, so `needsCompat` is true).
- `android/app/src/debug/.../DebugProbe.kt` plus `src/debug/AndroidManifest.xml`: an exported receiver, in the **debug source set only**. `am broadcast -a app.tendshelf.DEBUG_EVAL --es js '<expr>'` runs
  `evaluateJavascript` and logs the result under tag `TsProbe`; `DEBUG_LOAD --es url <u>` calls `loadUrl`, which passes the navigation policy on purpose, so hostile pages can be reached. Why not CDP: `internal/testutil/cdp`
  drives Chromium over a pipe and has no attach, and a WebSocket client would be a new Go import. Gate: `aapt2 dump xmltree` on `assembleRelease` output has no `DebugProbe`.

**Gates for every package:** `make apk-debug`, `android/gradlew -p android :shared:test`; Go or JS touched: `make check`, `make build`, `make generate` clean. Each new dependency: `verification-metadata.xml` and `NOTICES.md`.
Roadmap status note with each package. Commit what is green. A plan gap is reported as `PLAN GAP:`, never redesigned.

**Shared stays pure.** No Android import; pure logic takes values and returns values. No JSON reader in `shared`: page-to-native messages are plain words, native-to-page JSON is written with `jsonString`, and server JSON is read
with `org.json` in `app/ApiJson.kt` into `shared` data classes. Only that file changes if KMP later needs `kotlinx-serialization` (needs approval then). `java.net.URI` in `shared/Origin.kt` is the one JVM type, noted in a comment.

---

## A2 Shell

**Reused**: A0 WebView and insets idea, `network_security_config.xml`, `ApiPaths.shellUrl` idea, `desktopshell.CheckServer` rules (no redirects, 5 s, 4 KiB, JSON check). **Replaced**: hard-coded server, `setPadding` on the WebView.
**New**: connect flow, token store, bundled pages.

### Invariants
1. The WebView main frame stays on the chosen origin or the bundled origin. Anything else leaves the app or is refused.
2. The token lives only in `TokenStore`, is sent only by native HTTP, and never appears in a URL, a log, `loadUrl` headers, or script. The app never calls `evaluateJavascript` in release.
3. No script is injected into any page; `/login` above all. Release builds ignore start-intent extras.
4. A code is taken only for the exact origin, main frame, with a verifier pending; the verifier is used once.
5. No Google traffic: Safe Browsing and WebView metrics are off.
6. A plain-`http` address is warned once per origin, harder when not private. Never silently downgraded.
7. Sign-out leaves nothing: token, cookies, web storage, cache, history.

### Decisions
- **D1 Prove the intercept first.** Android's docs say `shouldOverrideUrlLoading` is not called for POST; the 303's follow-up is a GET with `isRedirect=true`, so it should fire (guess). Step 1 proves it. Fallback, same code path:
  `shouldInterceptRequest` on `/app/connect/done` answers an empty 200 with no network. Both failing is `PLAN GAP`; `done` ignores its query, so nothing leaks meanwhile.
- **D2 Bundled pages via `WebViewAssetLoader`** on `https://appassets.androidplatform.net`, rendered by native from a template with HTML-escaped values, **no JavaScript at all**. The form GETs `/connect?address=`, which the policy catches. `native-start.js`
  from `android.md` is dropped: no script means no second bridge.
- **D3 Insets by a parent `FrameLayout`, not CSS.** It pads `systemBars | displayCutout | ime`; the WebView never overlaps a bar, so `env()` stays 0 and works across WebView versions (`minSdk 28`). Cost: the tinted book band no longer reaches under the status bar. The parent's
  background is the ground colour (values and `values-night`; A4 makes it follow the page).
- **D4 Back through `OnBackInvokedDispatcher`** on API 33+ (targetSdk 36 ignores `onBackPressed` once predictive back is on) and `onBackPressed` below. Callback registered only while `web.canGoBack()`. A4 adds closing sheets.
- **D5 File saves by the Storage Access Framework**, not the system `DownloadManager`, so the session cookie stays in the app. `FileSaver` sends `Cookie` from `CookieManager` and **never** `Authorization` (`refuseMixed`). Same-origin URLs only.
- **D6 `http` is allowed; the bridge question is settled in A4.** Warn in a platform `AlertDialog`, once per origin, stored in prefs. Words: A1's line; non-private addresses get a second sentence, "Anyone along the way could see your password."
- **D7 Passkeys and OIDC: nothing to check.** Neither exists on the server yet (no `webauthn`/`oidc` code). When OIDC lands, the policy needs its issuer origin, for sign-in only.
- **D8 Address with no scheme:** try `https://`, then `http://`; an `http` answer goes through the warning. An address with a path, query or userinfo is refused: "Type only the address, like tendshelf.home:8095." Invites are accepted in a browser first (no deep links).

### Data shapes
```kotlin
// shared/Origin.kt
data class Origin(val scheme: String, val host: String, val port: Int?) { // host lowercased, IPv6 in brackets, default port null
  val text: String; val plainHttp: Boolean; val privateAddress: Boolean
  // private: 10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, 100.64/10, fc00::/7, fe80::/10, ::1, *.local, single-label names
  fun matches(url: String): Boolean; fun healthUrl(): String; fun meUrl(): String; fun connectUrl(challenge: String): String; fun homeUrl(): String
  companion object { fun parse(input: String): Origin? } } // http and https only
// shared/Pkce.kt: fun verifier(random32: ByteArray): String; fun challenge(sha256OfVerifierAscii: ByteArray): String  (kotlin.io.encoding.Base64.UrlSafe, no padding)
// shared/Navigation.kt
sealed interface Nav { object Stay; object External; object Block; data class TakeCode(val code: String); data class Connect(val address: String) }
fun decide(url: String, mainFrame: Boolean, origin: Origin?, codePending: Boolean): Nav
```
`decide`: `appassets.androidplatform.net` `/connect` gives `Connect`, others there `Stay`. Same origin and path `/app/connect/done`: `TakeCode` when main frame, pending and `code` present, else `Block`. Same origin: `Stay`. Other `http(s)` main frame: `External`. `mailto:`: `External`.
Every other scheme (`intent:`, `file:`, `content:`, `javascript:`, `data:`, unparsable): `Block`. Other-origin subframe: `Block`.
Prefs (`MODE_PRIVATE`): `origin`, `tokenCt`, `tokenIv`, `deviceId`, `userId`, `httpWarned.<origin>`. Keystore alias `tendshelf.token`, AES-256-GCM, no user-auth requirement (the service needs it with the screen locked). Decrypt failure counts as signed out.
Server **S1 (fits A2, additive)**: `/healthz` gains `"connect":"1"`; missing means "This server needs an update first."

### Steps
1. Before anything else, on the real A1 server: a throwaway `WebViewClient` logs `shouldOverrideUrlLoading` (url, `isRedirect`, method) and `shouldInterceptRequest` through a whole connect. Record the log in the status note. Choose D1's path.
2. `gradle/libs.versions.toml`, `app/build.gradle.kts`: `androidx.webkit:webkit:1.18.0` (latest stable on Google's Maven, 2026-11-06); `copyShellAssets` Gradle task copies `tokens.css`, `bundle.css`, fonts from `internal/web/static` into a generated assets dir (never into git). Verification metadata, `NOTICES.md`.
3. `shared/Origin.kt`, `Pkce.kt`, `Navigation.kt` plus tests. Pkce: RFC 7636 appendix B vector. `decide`: table test with `https://good@evil/`, `HTTP://GOOD.example:443`, trailing dot, other port, backslashes, `intent:` and the done path in a subframe.
4. `app/.../ConnectionSource.kt` (section 0), `TokenStore.kt` (implements it), `ApiJson.kt`, `AuthApi.kt` (`health`, `swap(code, verifier, bearer?)`, `me(token)`, `revoke(token)`: `HttpURLConnection`, `instanceFollowRedirects=false`).
5. `app/.../StartPages.kt` (`/start`, `/offline`, `/assets/*`), `assets/start.html`, `offline.html`, CSP meta `default-src 'self'; form-action 'self'`, `data-theme` from `uiMode`. Offline page: words and Try again (A7 lists downloads).
6. `app/.../ShellWeb.kt`: settings: JS and DOM storage on; file and content access off; `setSupportMultipleWindows(false)`; `javaScriptCanOpenWindowsAutomatically=false`; `overScrollMode=NEVER`; `textZoom` left alone. `setWebContentsDebuggingEnabled(BuildConfig.DEBUG)`. Client calls `decide`; `onReceivedError` and HTTP-level failure on the main frame open `/offline`; SSL errors cancel and show plain words. `WebChromeClient.onShowFileChooser` for `upload.js`. Long-press consumed for `IMAGE_TYPE`, `SRC_ANCHOR_TYPE`, `SRC_IMAGE_ANCHOR_TYPE` only.
7. `app/.../ConnectFlow.kt`: states `NoServer`, `NeedsSignIn`, `Connecting`, `Connected`, `Reconnecting`. Implements A1 section 8 exactly: `/login` seen with a token calls `me`: 401 sign-out path; 200 `Reconnecting`, and on leaving `/login` loads `connectUrl` with a new challenge, swaps with the bearer. Swap 401: back to start with its words. 409 with bearer (another person): sign-out path, start again. 409 without (device limit): load `/me/devices`. Launch calls `me` once: 401 sign-out path; network failure stays connected. `clearHistory` after the first page following a swap. Device name: `Build.MODEL`.
8. `app/.../SignOut.kt`: `run(reason)`: best-effort `revoke`, token cleared, cookies, `WebStorage.deleteAllData`, `clearCache(true)`, `clearHistory`, then `/start` with the address filled in. A4 adds the service stop, A7 the downloads.
9. `app/.../FileSaver.kt`, D5; `setDownloadListener`. `MainActivity.kt`: parent layout, insets, back, flow; start extras only under `BuildConfig.DEBUG`. Manifest: `configChanges="orientation|screenSize|screenLayout|smallestScreenSize|keyboardHidden|uiMode"`, `enableOnBackInvokedCallback="true"`, meta-data `android.webkit.WebView.EnableSafeBrowsing=false`, `android.webkit.WebView.MetricsOptOut=true`.
10. `internal/web/server.go` S1 with its test. `android/tools/test-server.sh`, debug probe.

### Acceptance (emulator)
- Intercept log from step 1 recorded; `logcat` and the server log show no `/app/connect/done` request and no code.
- Clean install, type `10.0.2.2:8097` by `adb shell input text`: https fails, http warning shows once, sign in, Connect tap, home loads. `/me/devices` shows "This phone", kind `mobile`. Second launch: straight home, no warning.
- Token: `run-as ... cat shared_prefs/*.xml` holds no token text; `DEBUG_EVAL` of `document.cookie`, a page-source search and `localStorage` dump hold no token; `logcat` grep for its first 8 chars: 0.
- Revoke in `/me/devices` from a desktop browser, relaunch: start page, cookies gone (`DEBUG_EVAL`). `/logout` in the app: same. Other person at `/login` after session expiry: 409 path ends at start, no mixed state.
- `DEBUG_EVAL location.href='https://example.org'`: external intent fired (`dumpsys activity` top), WebView stays. `location.href='intent://x#Intent;end'`, `file:///etc/hosts`: nothing happens. Release APK started with `--es server http://x --es book y`: ignored.
- Rotate, keyboard over the search field, cutout landscape: screenshots show no bar overlap. Back walks history, then leaves. Predictive back on API 36 works. Server stopped: `/offline`; Try again recovers. A bookmark export saves through the file picker; a book uploads from the picker.

### Out of scope
Bridge, theme following (A4), offline list (A7), token rotation, deep links and invites in the app, HTTPS enforcement, certificate pinning, passkeys.

---

## A3 Playback service

**Reused**: `PlaybackService`, `MediaLibrarySession`, foreground type, audio attributes, `Position`, `manifest`. **Extended**: `Api.kt`, `Manifest.kt`, `ApiPaths.kt`. **Replaced**: one `MediaItem` per file, per-call password sign-in (kept in debug until A4).

### Invariants
1. **Player time is book time.** The window position equals the book position; every client, present or future, speaks book milliseconds.
2. All control goes through a `MediaController` and `PlayerRemote`. The web bar and a later native player are clients of the same seam; nothing in the service knows about either.
3. Only this app may use custom commands. Other apps (Auto, System UI, headsets) get standard commands only.
4. Next and previous are chapters everywhere; skip uses the person's interval.
5. Bearer headers go only to URLs built from `ApiPaths` on the chosen origin. No token in a URL or log.
6. A lost network never loses the place or ends the session within the retry window.

### Decisions
- **D1 One window: `ConcatenatingMediaSource2`** (checked in the 1.11.1 AAR), not per-file items. Position, seek across files, duration and the notification bar are then whole-book with no mapping, and gapless is the player's job. `Builder.add(source, durationMs)` with manifest durations lets a seek land in a part not yet opened. A single-file book takes the same path.
- **D2 Missing parts become `SilenceMediaSource` of their length**, keeping invariant 1. `onPositionDiscontinuity` seeks past a silent period (auto transition or seek); at the end it goes to the end. Simpler than a time map in every client.
- **D3 `BookPlayer : ForwardingPlayer`** overrides `seekToNext`, `seekToNextMediaItem`, `seekToPrevious`, `seekToPreviousMediaItem`, `seekBack`, `seekForward`, `getSeekBackIncrement`, `getSeekForwardIncrement`, `isCommandAvailable`, `getAvailableCommands` (adds next and previous, which a one-item timeline would drop), `getMediaMetadata` (chapter as title, book as album). It keeps its own listener list to raise `onMediaMetadataChanged` when the chapter changes. Previous: more than 3 s into the chapter restarts it, as `player.js:jumpChapter`.
- **D4 Notification:** default provider. Slots previous chapter, play, next chapter; `setMediaButtonPreferences` adds Back and Forward (`COMMAND_SEEK_BACK/FORWARD`, `ICON_SKIP_BACK`/`FORWARD`, in `SLOT_OVERFLOW`). No custom notification code.
- **D5 Cover as bytes:** `Api.cover` (`w=512`, max 2 MiB, bearer) goes to `setArtworkData`. The session's bitmap loader has no bearer; bytes also serve Auto. Failure: no art.
- **D6 `needsCompat` files always stream `?compat=1`**, the browser's path. `RetryPolicy : DefaultLoadErrorHandlingPolicy`: 503 waits 5 s, up to 120 tries (the web's 10 minutes); 401 is fatal; IO errors wait 5 s, up to 24 tries; others default. `DefaultLoadControl`: max buffer 300 s, target bytes 32 MiB, time over size: a tunnel is ridden out of buffer.
- **D7 Merged ids:** `Api` handles one same-origin 308 by hand (`instanceFollowRedirects=false`). Media3 re-sends headers on a same-protocol redirect; `/stream` never redirects, cross-protocol stays off. Accepted: the chosen server already holds the token.
- **D8 Constant-bitrate seeking** on (`DefaultExtractorsFactory.setConstantBitrateSeekingEnabled(true)`) for MP3s with no Xing header. Manifest versus real durations may drift a little on later parts; measured in acceptance, not corrected.
- **D9 Commands.** Custom only: `START_BOOK {book, atMs}` (`atMs=-1`: best known place), `STOP_AND_CLEAR`. Speed, seek, play and pause are standard. State beyond `Player`: session extras `ts.bookId`, `ts.chapterIdx`, `ts.error` (`stream`, `book`, `signedOut`). `onConnect` adds custom commands only when `controller.packageName == packageName`. A0's `START_BOOK` password args stay under `BuildConfig.DEBUG` until A4.

### Data shapes
```kotlin
// shared/Manifest.kt additions (defaults keep A0 tests compiling)
AudioFile(..., val playable: Boolean = true)
BookManifest(..., val speed: Double = 1.0, val coverUrl: String = "", val author: String = "", val progress: RemoteProgress? = null)
data class RemoteProgress(val positionMs: Long, val finished: Boolean, val clientUpdatedAt: Long, val deviceId: String, val deviceLabel: String)
data class PlayerSettings(val speed: Double, val skipBackS: Int, val skipForwardS: Int, val autoRewind: Boolean, val nextInSeries: Boolean)
// shared/Position.kt additions, pure
fun nextChapterStart(chapters: List<Chapter>, bookMs: Long): Long?
fun previousChapterStart(chapters: List<Chapter>, bookMs: Long): Long  // the 3 s rule
fun startPlace(p: RemoteProgress?, atMs: Long?): Long                  // atMs, else stored, 0 when finished
// shared/PlayerState.kt
data class PlayerSnapshot(val bookId: String, val positionMs: Long, val durationMs: Long, val playing: Boolean, val buffering: Boolean,
  val ended: Boolean, val speed: Double, val chapterIdx: Int, val error: String)   // bookId and error "" when none
```
```kotlin
// app/PlayerRemote.kt: THE seam for the native player
interface PlayerRemote {
  fun start(bookId: String, atMs: Long?); fun play(); fun pause(); fun seek(bookMs: Long); fun setSpeed(speed: Double)
  fun snapshot(): PlayerSnapshot; fun observe(listener: (PlayerSnapshot) -> Unit); fun release() }
class ControllerRemote(controller: MediaController) : PlayerRemote
```

### Steps
1. Spike in `PlaybackService`: Parts through `ConcatenatingMediaSource2`, session attached; check `dumpsys media_session` shows the 15-minute duration and cross-file seek works. If the session or controllers misreport a single window, stop: `PLAN GAP`.
2. `shared/Manifest.kt`, `Position.kt`, `PlayerState.kt`, `ApiPaths.kt` (`coverUrl`, `settingsUrl`, `streamUrl(id, compat)`). Table tests, chapter crossing a file included.
3. `app/.../ConnectionSource.kt` (section 0). `Api.kt`: new fields, `settings()`, `cover()`, 308, `instanceFollowRedirects=false`.
4. `app/.../BookMedia.kt`: `build(manifest, factory): MediaSource`, D1, D2, D6.
5. `app/.../BookPlayer.kt`, D3. `app/.../BookEngine.kt`: `start(bookId, atMs)` on the `io` thread (settings, manifest, cover), then the main thread. A second start cancels the first by sequence number. Holds `settings`, `chapters`, publishes extras. Exposes `onPositionTick` and `onEnded` as plain callbacks for A5.
6. `PlaybackService.kt` thinned: session, `SessionCallback`, D4, D9, tag `Tendshelf`, `var connections = ConnectionSource { null }`. `STOP_AND_CLEAR`: stop, clear items, clear headers, `stopSelf`. `RetryPolicy.kt`.
7. `PlayerRemote.kt`.

### Acceptance (emulator; `cmd media_session dispatch`, `dumpsys media_session`, `dumpsys activity services`, debug start extras)
- Parts: duration 900 s. Next, 6 times: each lands on its chapter start (±1 s), title follows; at the last chapter next does nothing. Previous at 10 s in restarts; at 1 s in goes back one. Chapter crossing a file plays through; the boundary has no audible gap (logcat: no rebuffer).
- Seek to 14:30 from 0:10: audio from part 3 within 3 s; position error against a tone-marked fixture at most 1 s, recorded for each part.
- Single: same checks. Odd: plays via `?compat=1` (server log), first answer 503 waited out. A second `START_BOOK` while the first is loading: only the second plays.
- Speed 1.5: `dumpsys` speed 1.5, position advances 1.5 per second. Back and Forward honour `skipBackS=30`, set by `PATCH /api/v1/settings` beforehand.
- Notification: cover, chapter title, five controls (screenshot). Screen off 30 min on Parts, looping by restart: 31 of 31 samples playing. Foreground type `mediaPlayback`.
- Stop the server for 90 s while playing: sound runs on buffer, no error; restart: carries on. Stop it for 5 min: paused at the same place, `ts.error=stream`.
- Token revoked mid-book: on next read `ts.error=signedOut`, sound stops. A test APK with another package id (`app.tendshelf.probe`, debug-only module not committed) connects: `START_BOOK` refused, play and pause accepted. `logcat`: no token, no password.

### Out of scope
Saves and `onEnded` behaviour (A5), bridge (A4), sleep (A6), downloads (A7), `onPlaybackResumption` and `onGetChildren` (A8), sound modes, skip silence, chapter-relative progress bar.

---

## A4 Bridge: web player as remote control

**Reused**: `player.js`, `player-full.js`, `waveform.js`, `eyes-free.js`, `now-panel.js`, the A1 `native-audio.js` slot, `window.tsPlayer` and `ts:player:*`, `PlayerRemote`, toasts, copy. **Replaced**: `android.md`'s fake file-level `audio` (it would desynchronise `current.fileIdx`, since native crosses files alone).
**Removed**: A0's password path (`Api.signIn`, `tokenRequestBody`, `START_BOOK` password args).

### Invariants
1. **No token reaches script.** The bridge's vocabulary has no field that could carry one, and no route from a page string to a header, URL, path or file.
2. **Ceiling rule:** a message gives a page nothing its session cookie could not already do from that page. Declared exceptions, sound and chrome only: start sound on this phone, Back, bar colour.
3. Exactly one origin, main frame only, only in `Connected`.
4. Native is the truth for sound, position, saves. The page draws what native says; it never saves, never asks `/hx/player/next`, never uses Media Session.
5. No page, or a frozen one, changes nothing in playback. Native pushes nothing while the activity is stopped.
6. Browsers, and share links in the app, run the old path: `NATIVE` is false and no other byte of behaviour differs.
7. Without the bridge (`tsNative` missing) the page plays through `<audio>` as a browser does. It degrades; it does not break.
8. Every message has a parser test and an emulator test.

### Bridge scoping decisions
- **`WebViewCompat.addWebMessageListener`, object `tsNative`, one exact rule** (`Origin.text`). Never `addJavascriptInterface`, never `*`. Added before first load in `Connected`, removed on sign-out or origin change. Feature check `WEB_MESSAGE_LISTENER`; absent: invariant 7.
- **Every message re-checks** `isMainFrame`, `sourceOrigin` against `Origin` (normalised), and `ConnectFlow.state == Connected`. The rule stops injection; these stop what the rule cannot, such as a same-origin iframe.
- **No WebMessagePorts.** A port has no origin on each message and script can transfer it to a worker or frame. The listener's `JavaScriptReplyProxy` already gives a two-way channel bound to the speaking frame, with `sourceOrigin` per message. One mechanism, not two.
- **Plain `http`: the bridge is on, with the same list.** Refusing it would leave a LAN `http` server, the common self-hosted case and the operator's own phone, with no native playback, no Auto and no screen-off guarantee, while protecting almost nothing: on `http` a LAN attacker already reads the session and the token (A1 T10, decision 0026 point 8). The ceiling rule is what makes this safe: injected script gains only the declared exceptions. It departs from Google's HTTPS-only guidance, so it is **flagged for the operator**. Rule for A7 onward: a message that touches local storage or downloads must be argued against the ceiling and may be refused on `http`.
- **Words, not JSON, page to native.** A strict grammar in `shared`, tested with hostile input, and no parser to attack.
- **The seam for the native player** is `PlayerRemote`: `Bridge` is its first client. When the native player lands, it calls the same interface, CSS under `[data-shell]` hides `#player`, and `native-audio.js` plus the `NATIVE` lines go. Nothing is reserved in the protocol for it.

### Protocol (version 1), `shared/BridgeProtocol.kt`
Page to native: at most 128 ASCII characters; anything else is dropped and counted, with no reply. Token bucket 30 per second.
| Message | Grammar | Effect |
| --- | --- | --- |
| `hello 1` | exact | Replaces the reply proxy; native answers with `state` |
| `start <id> <ms or ->` | id `[A-Za-z0-9_-]{1,64}`, ms `[0-9]{1,12}` | `PlayerRemote.start`, `-` = best place; plays |
| `play`, `pause` | exact | Same on the remote |
| `seek <ms>` | as above | `PlayerRemote.seek` |
| `speed <x.xx>` | 0.50 to 3.00, step 0.05 | `setSpeed` |
| `back <0 or 1>` | | Page's answer to native `back`: 0 means not handled, so native walks history |
| `theme #rrggbb` | lowercase hex | Bar background, icon contrast |

Native to page, built only by `encode*` with `jsonString`, from fixed fields:
```json
{"t":"state","bookId":"","positionMs":0,"durationMs":0,"playing":false,"buffering":false,"ended":false,"speed":1.0,"chapterIdx":0}
{"t":"error","code":"stream|book"}
{"t":"back"}
```
`signedOut` is not sent: `SignOut` replaces the page. Pushes: on every change, 1 per second while playing and the activity is started, a full `state` on `onStart` and `hello`, none while stopped.

### `internal/web/static/js/native-audio.js` (S2, fits A4; replaces the no-op)
Same file, no token, only `tsNative`. If `window.tsNative` exists and `document.body.dataset.tsLink` is empty, builds `window.tsNativeAudio` and fires `ts:native:ready` on `document`. It is an `EventTarget` with:
- Read: `paused`, `ended`, `error` (`{code}` or null), `readyState` (4), `duration` (NaN, so `warmNextFile` stays idle), `currentTime` (book seconds), `bookMs` (extrapolated from the last push by `performance.now()` and speed while playing), `volume` (fixed 1, so `probeVolume` is false and `canSetVolume` hides the slider).
- Write: `playbackRate` (setter sends `speed`), `defaultPlaybackRate`, `preservesPitch`, `src` (ignored). No-ops: `load()`, `removeAttribute()`. `canPlayType()` answers "probably".
- Methods: `play()` and `pause()` send; `start(bookId)` sends `play` if native holds that book and has no pending aim, else `start id ms|-`; `seekTo(ms)` sets the optimistic `bookMs`, sends `seek` if native holds the page's book, else stores the aim; `aim(ms)` stores it.
- Events from pushes: `play`, `playing` (edge to playing), `pause`, `waiting` (buffering), `timeupdate` (every push), `ended`, `error`, `ratechange`, `nativebook` (pushed `bookId` differs from `tsPlayer.state().bookId` and is not empty; repeats at each push, which heals races). On `back` it closes in order: `#sheet .ts-sheet`, open `dialog`, else answers `back 0`.
- After load and after `htmx:after:swap`: sends `theme` from the matching `meta[name=theme-color]`.

### `player.js`: the only changes (S2)
`NATIVE` is `document.documentElement.dataset.shell === "android" && !!window.tsNative && !document.body.dataset.tsLink`. Both scripts are `async`, so order is not given:
1. Line 9: `const audio = NATIVE ? await nativeAudio() : document.getElementById("audio");`, where `nativeAudio()` resolves `window.tsNativeAudio` or waits for `ts:native:ready`.
2. `bookPosition`: `if (NATIVE) return book ? audio.bookMs : 0;`
3. `seek`: after clamping, `if (NATIVE) { audio.seekTo(clamped); emit("seek"); return; }`
4. `play`: first line `if (NATIVE) { if (book) audio.start(book.id); return; }` (no JS rewind).
5. `save`, `checkRemote`: first line `if (NATIVE) return;`
6. `startFromLink`: first line `if (NATIVE) return loadBook(data.tsPlay, { play: true });`
7. `loadBook`: before `play()`, `if (NATIVE && given !== undefined) audio.aim(given);`
8. `ended` listener: first line `if (NATIVE) { emit("pause"); return; }`
9. `error` listener: first line `if (NATIVE) { toast("err", msg("nativeFailed")); emit("error"); return; }`
10. `hasMediaSession`: `!NATIVE && "mediaSession" in navigator`.
11. `state()`: `fileIdx` under `NATIVE` is `book.files.findLastIndex((f) => f.offset <= position)`.
12. New: `if (NATIVE) { listen(audio, "nativebook", () => loadBook(audio.nativeBookId)); listen(audio, "ratechange", () => { if (book) { book.speed = audio.playbackRate; emit("speed"); } }); }`
Inventory done for the rest: the other `audio.` uses (`volume`, `readyState`, `defaultPlaybackRate`, `preservesPitch`, `removeAttribute`, `load`, `canPlayType`) are safe on the object above; `warmNextFile` stays idle; `setInterval`, `sendBeacon`, `seekSaveTimer` end in `save`. `player-full.js`, `waveform.js`, `eyes-free.js`, `now-panel.js` use only `tsPlayer` and `ts:player:*`: unchanged. `player-full.js` `saveSpeed` keeps its cookie POST; native reads it on the next start.
`clip.js` line 6: `const audio = window.tsNativeAudio || document.getElementById("audio");`. `internal/web/copy.go`, `components/player.templ`: `data-ts-msg-native-failed`, **only when `Shell == "android"`** (A1 invariant 8). Word: "This book can't play right now. Check your connection." (not in the deck; listed for review).
Budgets (`motion_test.go`): `player.js` is 13,367 of 13,440 B by `gzip -9`, so it needs about 300 B more; `native-audio.js` needs about 2 KB of its 640 B. Move `native-audio.js` out of the app total into its own `jsAndroidTotalGzip` bucket, as `desktopTotal` does: a browser never loads it. **Operator approval; never squeeze.**

### Threats
| # | Threat | Answer |
| --- | --- | --- |
| B1 | XSS on the origin calls `tsNative` | Ceiling rule; `script-src 'self'` and `nosniff` stand; worst case is unwanted sound |
| B2 | `http`: LAN attacker serves the page | Same as B1, plus A1 T10 accepted. Flagged |
| B3 | Foreign page in the main frame | Navigation policy; origin rule: object not injected; `DEBUG_LOAD` test |
| B4 | Iframe, same or foreign origin | `isMainFrame` and `sourceOrigin` checks; no iframes exist today |
| B5 | Origin confusion: port, case, trailing dot, userinfo, scheme, sandboxed document (`routes_reader_image` CSP: origin `null`) | One normalised `Origin`; exact compare; tests |
| B6 | Message smuggles a URL, path, header or newline | Grammar has none; ids are charset-limited and pass `ApiPaths.segment` |
| B7 | Token leaks in a reply or error text | Fixed fields; no exception text crosses; `encode*` tested; `logcat` and JS sweep |
| B8 | Hidden book by id | Bearer API 404, same `error book` as unknown; no existence signal beyond what the session has |
| B9 | Page of person B, token of person A | Listener acts only in `Connected`; `ConnectFlow` leaves it on `/login`, regains it only after a swap; 409 signs out |
| B10 | Stale proxy after navigation | Nulled in `onPageStarted`, replaced at `hello`; a stale post carries only state the person's session sees |
| B11 | Flood or oversize | 128 chars, 30 per second, no allocation before the check |
| B12 | Server text in JSON or toasts | `jsonString`; toasts are text nodes; `deviceLabel` arrives in A5 by the same path |
| B13 | Another app on the phone | Cannot reach a WebView object; A3 D9 for commands; A2 for intents; debug probe absent from release |
| B14 | `tsNative` replaced by page script | Only harms that page; native is unaffected |
| B15 | Script reads session state from the object | Object has `postMessage` and `onmessage` only; test lists its keys |

### Steps
1. `shared/BridgeProtocol.kt`: `parse(String): BridgeMessage?`, `encodeState`, `encodeError`, `encodeBack`. Table tests: each message, each boundary, unicode, NUL, newline, 129 chars, `seek -1`, `speed 3.05`, `start ../x 0`, `start a b c`.
2. `app/.../Bridge.kt`: `attach(web, origin)`, `detach()`, the checks, the bucket, the switch to `PlayerRemote`, pushes.
3. `MainActivity.kt`: `ControllerRemote` from the controller; attach when `Connected`, detach otherwise; `onStart` and `onStop` gates; back asks the page first, 150 ms timeout, then history. `SignOut.kt` sends `STOP_AND_CLEAR`. `PlaybackService.connections = TokenStore`.
4. Remove the password path, `tokenRequestBody`, `signIn` and their tests.
5. `native-audio.js`, `player.js`, `clip.js`, copy, `make generate`, budgets. `internal/web/shell_android_test.go`: `data-ts-msg-native-failed` absent for browsers.
6. Browser tests (Chromium, `TENDSHELF_TEST_BROWSER` per command) in `internal/web/native_audio_browser_test.go`: a fake `tsNative` in the test page drives the player: each push changes the bar; `start`, `seek`, `speed` strings exact; no `/progress` POST, no `/hx/player/next`, no `sendBeacon`; `nativebook` loads; link mode ignores `tsNative`.

### Acceptance (emulator, `DEBUG_EVAL`)
- Tap Play on Parts: `dumpsys` playing, `<audio>` has no `src`, server log shows `/api/v1/stream` by bearer and no `/stream` by cookie. Mini bar and full player show native position within 1 s. Chapter sheet, speed, Back and Forward, waveform seek all move native. Bookmark play seeks.
- Lock-screen pause and `dispatch next`: bar follows on return. Activity stopped: `logcat` shows no pushes. Screen on: `state` at once. Navigate five pages by `hx-boost` while playing: no gap, bar stays. Full reload: `hello`, bar correct. Task removed and reopened while playing: bar correct.
- Server log during 5 minutes of play: no page `POST /progress`.
- Hostile, all by `DEBUG_EVAL` or `DEBUG_LOAD`: `Object.keys(tsNative)` no secret; each bad message from the table: no effect, no reply; `10.0.2.2:8099` page: `typeof tsNative === "undefined"`; same-origin iframe posting `play`: ignored; `/books/{id}/read/images/{id}` sandboxed: no object; 1,000 messages in a loop: app alive, sound unchanged; `/login` state: `play` ignored.
- Sweep: token's first 8 chars absent from `document.documentElement.outerHTML`, `localStorage`, `document.cookie`, `logcat`.
- Bridge refused (debug flag off): page plays through `<audio>` by cookie, no crash.
- Browsers: `make visual-compare` identical; existing player browser tests green; `TestJSBudget` with approved numbers. Android Back closes the speed sheet, then the full player, then walks history. Theme change colours the bar.

### Out of scope
Sleep timer: the JS timer stays and **fails when the page is frozen until A6** (known gap). Clip stop has the same limit with the screen off. Volume, offline page, sound modes, `http` hardening beyond the warning, any native player screen.

---

## A5 Progress outbox, conflict prompt, smart rewind, next book

**Reused**: `BookEngine` callbacks, `POST /api/v1/progress/batch`, `GET /api/v1/progress/{id}`, `POST /api/v1/queue/next` with `series`, `merged ids` rules, the 60 s rule and copy of `player.js`, `rewindSteps`. **Extended**: `/api/v1/settings`, `BridgeProtocol`, `player.js`. **New**: `outbox.db`.

### Invariants
1. A position is durable on disk before any network attempt. At most 10 s of listening is lost to a process kill.
2. A page, frozen or absent, is never involved in saving, rewinding or the next book.
3. Nothing moves the place of a listening person without a tap, except the rewind they have enabled.
4. A retry never double-counts listening; `recordSession` drops `t <= endedAt`.
5. The queue is popped at most once for one finished book.
6. One person's rows never go out under another's token.
7. The outbox is bounded.

### Decisions
- **D1 SQLite through platform `SQLiteOpenHelper`, no module.** Survives kills, gives order. Behind `OutboxStore` in `shared`, so an iOS store can replace it.
- **D2 Append, coalesce only safely.** The server caps a save's listening at 10 minutes (`SessionMaxListenMs`), so one offline row must not swallow hours. A tick merges into the last unsent row of the book only if the sum stays at most 300,000 ms, speed is equal, and neither row has `finished` or `force`. Cap 2,000 rows (about a week): beyond it the oldest plain row goes; only stats are lost, the newest place stays.
- **D3 Results.** Item with no error, or `not_found` or `invalid`: row deleted (logged without data). HTTP 401: keep rows, sign-out path. 429: wait `Retry-After`. 5xx or IO: keep, backoff 5 s doubling to 5 min, reset on network return. Batches of 100, in order, until empty.
- **D4 Conflict is the web's rule, in `shared`.** Ask when the remote is not finished, not this device, not `server`, and more than 60 s away. Checked at resume after a break of 60 s or more, and on `applied:false`. Nothing moves until answered.
- **D5 Where the question shows.** Activity started: bridge `ask`, the existing toast. Otherwise, or A4 absent: a notification on channel `place` with two actions, `VISIBILITY_PRIVATE`. Same words as the deck; time from `Position`, device from `deviceLabel`. While open, ticks update one row with `held=1`, never sent. **Stay here**: released with `force`. **Continue there**: deleted, seek. Book change or process restart: released plain (newest wins), question dropped.
- **D6 Rewind runs on `onIsPlayingChanged(true)`,** not in `play()`: it also covers a resume after a call, which ExoPlayer does on its own. `idleAt` is set on the edge to not playing unless buffering, kept in prefs. `START_BOOK` with `atMs` clears it; best place sets it to the stored time, as `loadBook` does.
- **D7 Next book.** `onEnded`: take a 30 s `PARTIAL_WAKE_LOCK` (ExoPlayer drops its own at `ENDED`), write `finished`, flush. Flush failed: stop, nothing popped; the book is marked and next book waits for a tap. Otherwise `POST /queue/next {"ended":id,"series":true}`. 200: `START_BOOK` in the same session. 204: stay ended. **Never retried once the request was sent** (invariant 5); a lost answer costs one Up next row (the book stays in the library) and that autoplay; a retry would silently skip a second book. Media3's `DEFAULT_FOREGROUND_SERVICE_TIMEOUT_MS` is 600,000, so the service should stay foreground through the hand-off (guess for `ENDED`; acceptance proves it). If it demotes: `PLAN GAP` with the log.
- **D8 Rewind table from the server (S3).** `rewind.go` says it is the one source, so a Kotlin copy would be a second one for the first time. `shared` keeps a default only for a server without the field.
- **D9 Own device** is `Connection.deviceId`; the token defaults the save's `deviceId`, so the body omits it. A sign-in with a different `deviceId` deletes all rows; a rebind keeps them. A sign-out tries one flush, then deletes.
- **D10 Flush triggers:** after each enqueue, default-network callback (adds `ACCESS_NETWORK_STATE`), service start, activity start. No WorkManager: rows written and then left with the app closed go on the next launch. Accepted, named below.

### Data shapes
```sql
-- app: outbox.db, user_version 1
CREATE TABLE saves (seq INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, book_id TEXT NOT NULL, position_ms INTEGER NOT NULL,
  client_updated_at INTEGER NOT NULL, finished INTEGER NOT NULL DEFAULT 0, force INTEGER NOT NULL DEFAULT 0, speed REAL NOT NULL, listened_ms INTEGER NOT NULL DEFAULT 0, held INTEGER NOT NULL DEFAULT 0);
CREATE INDEX saves_book ON saves(book_id, seq);
```
```kotlin
// shared/Outbox.kt
data class Save(val seq: Long, val bookId: String, val positionMs: Long, val clientUpdatedAt: Long, val finished: Boolean, val force: Boolean, val speed: Double, val listenedMs: Long, val held: Boolean)
interface OutboxStore { fun last(bookId: String): Save?; fun add(s: Save); fun update(s: Save); fun firstBatch(n: Int): List<Save>; fun delete(seqs: List<Long>); fun trim(max: Int); fun clear() }
class Outbox(store: OutboxStore, now: () -> Long) { fun tick(...); fun pause(...); fun finish(...); fun force(...); fun newest(bookId: String): Save? }  // clientUpdatedAt = max(prev + 1, now)
data class BatchItemResult(val bookId: String, val applied: Boolean, val errorCode: String?, val progress: RemoteProgress?)
fun applyResults(sent: List<Save>, results: List<BatchItemResult>): Applied // deletes and conflict candidates; a short answer deletes nothing unmatched
// shared/SmartRewind.kt
data class RewindStep(val fromMs: Long, val seconds: Int)
val DEFAULT_REWIND: List<RewindStep>  // 10 s: 2, 60 s: 5, 1 h: 10, 24 h: 20
fun rewindMs(awayMs: Long, steps: List<RewindStep>, enabled: Boolean): Long
// shared/Conflict.kt
data class Ask(val bookId: String, val positionMs: Long, val deviceLabel: String)
fun conflict(localMs: Long, remote: RemoteProgress?, ownDeviceId: String): Ask?
```
`Position.startPlace` gains the outbox: the newer of `Outbox.newest` and `manifest.progress` wins, as `useNewerPendingSave` does.
Save triggers: tick 10 s while playing (skipped if position moved under 1 s), pause, 2 s after a seek while paused, chapter change, `finish`, `onTaskRemoved`, `onDestroy`. `listenedMs`: `elapsedRealtime` while `isPlaying`, as the page's `heardMs`.
Bridge additions: page `answer there|here` (ceiling: same as the toast's buttons, which a session may already answer). Native `{"t":"ask","bookId":"","positionMs":0,"deviceLabel":""}`, `{"t":"rewound","s":5}`.
**S3 (fits A5, Go, additive):** `GET`/`PATCH /api/v1/settings` gain read-only `"rewindSteps":[{"fromMs":10000,"seconds":2},...]`, written from `rewindSteps` in `internal/web/rewind.go`; ignored in PATCH bodies. Test and `docs/api.md`.

### Steps
1. S3 with its test in `internal/web/api_settings_test.go`.
2. `shared/Outbox.kt`, `SmartRewind.kt`, `Conflict.kt`, `Position.startPlace`, `BridgeProtocol` additions, with an in-memory `OutboxStore` for tests. Table tests: coalesce limits, `finished` never merged, `clientUpdatedAt` strictly rises with a clock going back, partial batch, `not_found`, trim, conflict boundaries at 60,000 and 60,001 ms, own device, `server`, finished, rewind at each step edge, `autoRewind` off, clamp at 0.
3. `app/.../SqliteOutbox.kt`, `ApiJson.kt` additions, `Api.kt`: `batch`, `progress(id)`, `queueNext(ended)`.
4. `app/.../ProgressWriter.kt`: one `io` thread; tick `Handler`; flush state, backoff, network callback. `AndroidManifest.xml`: `ACCESS_NETWORK_STATE`.
5. `app/.../Resume.kt`: D6, and the 60 s remote check on `io`. `app/.../AskPrompt.kt`: D5; extras `ts.ask.*`, `ts.rewoundS`; notification actions via `PendingIntent.getService`, `FLAG_IMMUTABLE`, handled in `onStartCommand` before `super`; custom command `ANSWER_ASK {keepHere}`, same package only.
6. `app/.../NextBook.kt`: D7, wired to `BookEngine.onEnded`. `PlayerRemote`: `answer(keepHere)`.
7. If A4 is merged: `Bridge.kt`, `native-audio.js` (`nativeask`, `nativerewound`), `player.js`: `maybeAskContinue`'s toast body becomes `askContinue(remote, onThere, onHere)` so both paths share it; `listen(audio, "nativeask", ...)`, `hint(fill(msg("rewound"), ...))`. Otherwise record the step as `CONTINUE:` for after A4. `SignOut.kt`: D9. Strings: `res/values/strings.xml`, deck words "You were at {time} on {device}. Continue there?", "Continue there", "Stay here", "another device".

### Acceptance (emulator; `cmd connectivity airplane-mode`, `svc wifi`, `run-as ... sqlite3` if present, else a debug `DEBUG_OUTBOX` log)
- Online 3 min: server `progress` moves about every 10 s; `listening_sessions` matches real time ±10 s. **Kill:** `adb shell run-as app.tendshelf.android kill -9 <pid>` while playing: rows survive; relaunch: flushed; loss at most 10 s.
- Airplane mode 4 min while playing Parts from its 300 s buffer (longer offline needs A7; coalescing and the cap are proved by unit tests): rows grow, sound continues. Network back: all sent in order within 15 s; stats within 10 s of real listening. Replaying the same batch body with `curl` and the token (lost-answer retry) leaves `listening_sessions` unchanged.
- Screen off for all of the following, with a frozen activity: chapter crossing saved; Short 1 ends: Short 2 starts within 5 s, `foreground` stays true in `dumpsys`, server shows Short 1 finished; with Single queued: queue wins over series; last in series: stops, notification leaves within the service's timeout; setting `nextInSeries` off: stops.
- Network off at the end of Short 1: book marked locally, no pop, no crash; network back: `finished` flushes, nothing plays on its own.
- Conflict: `PUT /progress` from another device at +5 min, pause 61 s here, resume: page toast (activity started) with device and time, position unmoved. Stay here: server takes this place with `force`. Repeat, Continue there: seek. Screen off: notification with both actions instead; both work; `held` row behaves. Own device far away: no ask. Finished remote: no ask.
- Rewind: pause 12 s: back 2; 61 s: 5; `autoRewind` off: 0; a simulated call (`cmd audio` focus or `am start` of a call-capable app; guess) rewinds on resume; toast shown when the page is visible. Sign-in as another device: old rows gone. Sign-out: rows gone. `logcat`: no token.
- 1 hour on Parts on repeat, screen off: 61 of 61 samples playing, rows sent, no growth in `saves`.

### Out of scope, and why
Flush with the app closed (needs WorkManager and a reason beyond one late row). Bookmarks outbox (the page saves them; offline ones with A7). Conflict in Auto (A8). Sleep (A6). Rewind on the web player (unchanged). Retrying an unanswered `/queue/next`, which would skip a book. Server-side idempotency for it: an additive `clientId` would allow a retry, but one lost autoplay does not justify it.

---

## Server changes in one place
| Id | Change | Package | Note |
| --- | --- | --- | --- |
| S1 | `/healthz` gains `"connect":"1"` | A2 | Additive, one line; desktop ignores it |
| S2 | `native-audio.js`, 12 lines in `player.js`, `clip.js`, `nativeFailed` copy, budgets | A4 | No route; browsers unchanged, proved by `make visual-compare` and tests |
| S3 | `/api/v1/settings` gains read-only `rewindSteps` | A5 | Additive to `/api/v1`; `docs/api.md` |

## Open for the operator
1. Bridge on plain `http` with the same list, against Google's HTTPS-only guidance (A4).
2. JS budgets: `player.js` about +300 B; `native-audio.js` about 2 KB in a new Android bucket (A4).
3. New copy, not in the deck: "This book can't play right now. Check your connection.", the D8 and D6 lines in A2, "This server needs an update first."
4. The web sleep timer in the app fails with the screen off between A4 and A6; do not hand out a build in that window.
