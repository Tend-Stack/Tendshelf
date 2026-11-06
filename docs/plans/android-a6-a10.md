# A5b to A10 Android: the rest of the app (plan, 2026-11-06)

For a builder. Parent: [android.md](android.md), [android-a2-a5.md](android-a2-a5.md) section 0 (its rules still apply), decisions [0024](../decisions/0024-android-app.md),
[0026](../decisions/0026-app-sign-in.md), [0012](../decisions/0012-offline-pocket.md), [0019](../decisions/0019-offline-reading.md). Written from main at 2ef015f. "Guess" marks what is not measured.
Order: A5b, A6, A7, then A8 and A9 in parallel, then A10. A5b and A6 both change the bridge and `native-audio.js`, so they run one after the other.

## 0. Rules and measured facts for all packages

**Inherited.** Gates, parallel safety, shared stays pure, one emulator user at a time inside `flock -w 3600 /home/rubirosa/Projects/Tendshelf/.local/android-emulator.lock`, test tree `.local/tests/android/<pkg>/<run-id>/`, deleted after the run. Never port 8095, never `~/.local/share/tendshelf`. Ports: A5b 8101, A6 8102, A7 8103, A8 8104, A9 8105. Every new module needs a `NOTICES.md` row and `verification-metadata.xml` entries; this plan adds none (`media3-database` and the `offline` package already come with `media3-exoplayer`).

**Bridge rule.** New words extend `parseBridge` (same 128-character grammar, table tests for each). Protocol stays `hello 1`: unknown words are dropped on both sides, so a newer app on an older server, or the reverse, loses a feature and nothing else. The ceiling rule of A4 holds: a message gives the page nothing its session cookie could not already do.

**JS budgets, for the operator.** `native-audio.js` is 2,563 of 2,688 B, the Android bucket `jsAndroidTotalGzip` the same, `player.js` 13,755 of 13,824 B, `player-full.js` capped at 14,016 B. A5b and A6 cannot fit (see "For the operator"). Never squeeze a file to fit: report the overrun.

**The emulator, measured read-only on 2026-11-06.** Treat these as test conditions:
| Fact | Evidence | Consequence |
| --- | --- | --- |
| It sits in light Doze for long spells | `dumpsys netpolicy`: `Device idle: true`; `dumpsys deviceidle`: `mLightState=IDLE`; no `normal` entry in the idling history 07:18 to 07:49 | The roadmap's A0 line "the emulator was never in Doze" is wrong. A5b corrects it. |
| Waking the screen does not end Doze | `wait_for_unlock=true`, `mScreenLocked=true`; the keyguard credential is never entered | A woken emulator still has the Doze firewall on. |
| Deep Doze never comes | `mMotionSensor=null`, `mState=INACTIVE` | Only light Doze is tested here; deep Doze is a phone check. |
| Tailscale VPN runs on it since 2026-11-04 | `dumpsys vpn_management`; routes are 100.x hosts only | `10.0.2.2` does not enter `tun0`. Not the cause of anything below. |
| Default network flips | After an airplane cycle the fake cell network comes first, Wi-Fi 3 to 40 s later; Wi-Fi often stays down with the screen off | Wait for `Active default network` to settle. Do not assert on Wi-Fi. |
| The shell is exempt | UID 2000 is in the power allowlist | A probe from `adb shell` passes when the app is blocked. |
| `com.google.android.projection.gearhead` is a stub | `/product/priv-app/AndroidAutoStubPrebuilt`, version `1.2.551000-stub` | See A8. |

---

## A5b The page half of A5, and the book that would not start

**Reused**: `AskPrompt`, `Resume`, `Bridge`, `native-audio.js`, the toast in `player.js`, `DebugProbe`, `DebugHooks`. **Extended**: `BridgeProtocol`, `PlayerSnapshot`, `PlayerRemote`. **Replaced**: nothing.

### Step 0 first: diagnosis of the open bug (measured, three reproductions on 2026-11-06)

**Verdict: neither. It is not the emulator's network and not sockets, timeouts or the retry policy. The app's own process lost its network to the Doze firewall, because the system refused to let the service return to the foreground.**

The chain, from runs 1 and 3 (test server on port 8101, the A5 debug APK, no code changed):
1. `op stop` calls `stopAndClear`. Player idle, so Media3 drops the foreground service. Nothing else in the product does this except sign-out.
2. `startbook` comes from the shell, about 60 s after the last allow window. `keepAlive` holds `startForeground(4242)` for roughly 500 ms, which is why `settings` and `manifest` still succeed and `started book` is logged.
3. 5 ms later Media3 starts its own service: `Background started FGS: Disallowed [callingPackage: app.tendshelf.android ... uidState: SVC; code:DENIED]`. The service is then not foreground (no `isForeground` line). Why 4242 goes too is not traced (guess: Media3 stops foreground when its start fails).
4. `dumpsys netpolicy` shows `UID=10254 state={procState=SVC,cap=--------} blocked_state={blocked=DOZE|APP_BACKGROUND,allowed=NOT_IN_BACKGROUND,...}`. While playing it was `procState=FGS,cap=---NFU-T,allowed=FOREGROUND|...`.
5. `adb shell` reaches the server with `200 OK` at every sample of the wedge, because UID 2000 is exempt. SYNs from the app get nothing: `SocketTimeoutException` every 10 s (`Api.connectTimeout`), the stream stays `BUFFERING`.
6. **Why airplane mode "fixes" it**: the cycle restarts Bluetooth, whose media browser reconnects to our session, and the system logs `tempAllowlistTargetPkgIfPossible callingPackage:com.google.android.bluetooth ... [WIU] [FGS]`. The UID gets `POWER_SAVE_ALLOWLIST`, the stream connects 2 s later, Media3 re-promotes. The network change itself does nothing.
7. **Why `op dump` also "fixes" it**: every debug op runs `keepAlive`, a shell-allowed `startForeground`. Run 1 cleared 2.8 s after a `dump` (the service came back as 4242, then 1001). The three A5 wedges cleared 1 to 1.3 s after one (one from the log, two worked out from the position). The hook was hiding the bug.
8. **Why it looked random**: the self-start is allowed inside an allow window. Measured: allowed about 19 s after the last grant (run 2, no wedge), denied about 60 s after (runs 1 and 3). The exact window is not measured.

What each suspect turned out to be:
| Suspect | Finding |
| --- | --- |
| Emulator network after Doze | Healthy throughout (shell probe 200) |
| Stale pooled sockets | No. `DefaultHttpDataSource` closes after each stream, so the stream is a new connection; the Api GETs passed; the failures were new connections to a firewalled UID |
| `DefaultHttpDataSource` timeouts | 8 s default, working as designed. They only made the hang long |
| `RetryPolicy` | Correct, and it hid the hang: a blocked stream stays BUFFERING for up to 24 × (8 + 5) s, about 5 min, not the "2 minutes" of its comment, which holds only for an instant failure. Noted, not changed |
| A5 code | No: the same service path, hence the A3-only reproduction |

Controls: with no stop first (service still foreground), start after start plays in Doze (run 2, case 1). With the activity on top, stop then start is `Allowed ... uidState: TOP`, and playback continues 15 s after screen-off (run 2, case 4). Series-next with the screen off never hit this (A5 runs at 07:37 and 07:45): Media3 keeps the service foreground for 10 minutes after the end.

**Product impact.** The three proofs failed only because the harness calls `op stop` between scenarios. No listener path does stop-then-start in the background. The real product fault is narrower and still worth fixing: **when a foreground start is refused, the app hangs in silence** for minutes with `playWhenReady` on. The same refusal can reach A6 (none), A7 (`dataSync` from the background, designed out there), A8 (a car starting a book after 10 idle minutes, acceptance there) and the later widget and auto-download.

### Invariants
1. A refused foreground start is never a silent hang: within 1 s the player stops trying, `ts.error` says `background`, and the person gets a way back.
2. Nothing plays by itself after that; the way back is a tap.
3. A debug read never changes process state, so a probe cannot heal what it measures.
4. The place question shows in exactly one place: the page while the activity is started, the notification otherwise.
5. Nothing moves the place without a tap (A5 invariant 3, unchanged).

### Decisions
- **D1 Use Media3's `MediaSessionService.Listener`.** `setListener` and `onForegroundServiceStartNotAllowedException()` exist in 1.11.1 (checked with `javap`). No watchdog timer: the signal exists. Guess: Media3 calls it for this path; acceptance row 4 proves it, otherwise `PLAN GAP`.
- **D2 Harness brings the activity to the top before any stop or start** (`am start`, as case 4). Doze stays on, which makes the proofs stricter, and the screen-off part of each proof is unchanged. Not `dumpsys deviceidle disable`: that would hide this class of bug.
- **D3 Reads go by broadcast to `DebugProbe`**, not through a foreground-service start.
- **D4 Service learns page visibility by custom command**, because `AskPrompt` runs in the service and only the activity knows `onStart`/`onStop`.
- **D5 `RetryPolicy`, timeouts and `Api` stay as they are.** Fixing them would change no outcome here.
- **D6 `deviceLabel` crosses the bridge** as the one free-text field, through `jsonString`. The server already gives that label to the page, so the ceiling holds; the toast must set it as text, never HTML.

### Data shapes
```kotlin
// shared/PlayerState.kt: PlayerSnapshot gains
val ask: Ask? = null, val rewoundS: Int = 0   // error may also be `background`
// shared/BridgeProtocol.kt
data class Answer(val keepHere: Boolean) : BridgeMessage   // words: `answer here`, `answer there`
fun encodeAsk(a: Ask?): String      // {"t":"ask","bookId":"","positionMs":0,"deviceLabel":""}; empty bookId closes the toast
fun encodeRewound(s: Int): String   // {"t":"rewound","s":5}
```
Extras: `ts.ask.bookId`, `ts.ask.positionMs`, `ts.ask.device`, `ts.rewoundS` (exists). Commands, same package only: `app.tendshelf.ANSWER_ASK {keepHere}`, `app.tendshelf.PAGE_VISIBLE {visible}`. `encodeError` keeps its two codes: `background` crosses as `stream`, and only a stopped activity can see it anyway. String `keep_listening`: "Tap to keep listening" (new copy). Channel `playback`, notification id 4244.

### Steps
1. `app/.../PlaybackService.kt`: `setListener` in `onCreate`, `clearListener` in `onDestroy`. On the call: log `foreground start refused`, `engine.refused()`, post notification 4244 whose tap opens `MainActivity` (`FLAG_IMMUTABLE`). Cancel it on the next `STATE_READY`. `BookEngine.refused()` is `fail("background")`.
2. `app/src/debug/.../DebugProbe.kt`: action `app.tendshelf.DEBUG_READ --es op dump|outbox`; `DebugHooks` keeps `service` in a volatile field set by `onServiceStart`, and moves `dump` and `outbox` there. `android/tools/check-release.sh`: add `DEBUG_READ`.
3. `android/tools/emu.sh` (new, sourced; from the helpers proved today): `front` (`am start -n app.tendshelf.android/.MainActivity`, sleep 3), `read_dump`, `fgs`, `uid_state` (`pm list packages -U`, then the `UID=` line of `dumpsys netpolicy`), `shell_probe`.
4. `shared/BridgeProtocol.kt`, `shared/PlayerState.kt`, tests.
5. `AskPrompt.kt`: `pageVisible`; `show` publishes the extras and posts the notification only when not visible; visible to stopped posts it, stopped to visible cancels it and republishes; `answer` clears both. `PlaybackService.kt`: both commands, added for the own package in `onConnect`.
6. `PlayerRemote.kt`: `answer(keepHere)`, `pageVisible(v)`; `ControllerRemote.snapshot` reads the extras. `MainActivity.kt`: `pageVisible` from `onStart`/`onStop` and when the controller connects. `Bridge.kt`: `Answer` to `r?.answer`; `push` sends `ask` when it changes and `rewound` on the edge.
7. `internal/web/static/js/native-audio.js`: `nativeask`, `nativerewound`, `audio.answer(word)`. `player.js`: `maybeAskContinue`'s toast becomes `askContinue(remote, onThere, onHere)`; in `NATIVE` the buttons send the answer, and an empty `bookId` closes the toast. `hint(fill(msg("rewound"), ...))` at line 459 reused.
8. Docs: roadmap A5b note and the correction to the A0 line; `docs/manual-checks.md` gains an "Android app" table, one row per package from here on.

### Acceptance (port 8101, `android/tools/emu.sh`)
The three proofs, rerun. Each: `front`, reset progress, `startbook "<book>" 20000` (10 s from the end) with the activity on top, `input keyevent KEYCODE_SLEEP`, wait 16 s, read with `read_dump`, `dumpsys media_session` and `fgs`. Never `op stop` or `op dump` inside a proof.
1. **Queue over series**: queue Single, start Short 1. `next book: starting`; Single plays; `isForeground=true`; Short 1 finished on the server; queue empty.
2. **Last in series**: start Short 2. `next book: none`; paused state; samples every 30 s for 12 min show the service leaves the foreground and the notification goes (Media3's 600,000 ms; A5's sampling had a gap from +181 to +739 s).
3. **`nextInSeries` off**: PATCH it false, start Short 1. `next book: none`, nothing popped; PATCH it back.
4. **The refusal**: repeat run 3 (`startbook`, sleep, 60 s, `op stop`, `startbook`, no `front`). Within 1 s of `Disallowed`: `error=background`, `playWhenReady` false, notification 4244. `read_dump` leaves `uid_state` at `SVC`, proving invariant 3. `front`, press play: sound, notification gone. If the listener never fires: `PLAN GAP`.
5. **Page ask**: `PUT /progress` from another device at +5 min, pause 61 s, resume with the activity started: toast with device and time, position unmoved; **Stay here** sends `force`; repeat, **Continue there** seeks. Sleep the screen with the question open: notification instead; wake the activity: back to the toast, one prompt only. Rewound hint: pause 12 s, resume: "Rewound 2 s".
6. **A5's leftovers**: sign-out rows gone; sign-in with another device id rows gone; logcat has no token; 1 hour of Parts on repeat, screen off: 61 of 61 samples playing, no growth in `saves`.

### Out of scope, and why
`RetryPolicy` changes (no outcome changes). Keeping the foreground across `stopAndClear` (only sign-out reaches it). Unlocking the emulator or disabling Doze (see D2). Rows left unsent with the app closed (A5's accepted limit).

---

## A6 Sleep timer, fade, shake

**Reused**: the sheet, card, words and rules of `player-full.js`, `exo.volume`, session extras. **Extended**: `BridgeProtocol`, `Bridge`, `player-full.js`, `NextBook`. **Replaced in the shell only**: the page's `setInterval` timer, which dies with the screen off (the open A4 limit).

### Invariants
1. In the app the timer lives in the service. A frozen or absent page changes nothing.
2. Same behaviour as the web: counts only while sound plays; minutes are real minutes at any speed; the last 10 s fade; "+10 min" adds to what is left; end of chapter follows a skip ahead.
3. After the timer ends the next Play is at full volume.
4. A sleep timer that ends a book never starts the next one.
5. The sensor is on only during the fade.
6. Browsers: no change, proved by `make visual-compare` and the existing tests.

### Decisions
- **D1 Port the logic to `shared`, keep one sheet.** `SleepClock` is pure and takes elapsed time, so an iOS app reuses it. The page draws; it decides nothing in the shell.
- **D2 Tick 250 ms on the main `Handler`, only while the mode is not off**, like `SLEEP_STEP_MS`. ExoPlayer's wake lock keeps it alive while playing.
- **D3 Volume through `exo.volume`.** The fade multiplies 1.0; there is no in-app volume slider to respect (`player.js` hides it). Ducking is separate and stays.
- **D4 Shake uses `TYPE_LINEAR_ACCELERATION`, else `TYPE_ACCELEROMETER` minus 9.81**, exactly as `onMotion`. A shake is `plus()`.
- **D5 Sleep beats next book.** `NextBook.ended` asks `sleep.endedBook()` first: true means the timer was on, it is cleared, nothing is asked of the queue. This avoids a race between the tick and `STATE_ENDED`.
- **D6 End of chapter on the last chapter** rests at `duration - 1000`, not at the end, so the seek cannot reach `STATE_ENDED` and pop the queue. `finished` is not written.
- **D7 No persistence.** A killed process forgets the timer, as a reload does on the web.

### Data shapes
```kotlin
// shared/Sleep.kt
enum class SleepMode { OFF, MINUTES, CHAPTER }
data class SleepInfo(val mode: SleepMode, val choice: String, val leftMs: Long, val fading: Boolean)
sealed interface SleepStep { data class Run(val volume: Float) : SleepStep; data class Finish(val restAtMs: Long?) : SleepStep }
class SleepClock {                    // FADE_MS 10_000, PLUS_MS 600_000, SKIPPED_AHEAD_MS 1_500, CHAPTER_MIN_LEFT_MS 2_000
  fun set(choice: String, minutes: Int, chapterEndMs: Long); fun off(); fun plus(positionMs: Long, speed: Double)
  fun step(deltaMs: Long, playing: Boolean, positionMs: Long, speed: Double, chapterEndNow: Long): SleepStep
  fun info(positionMs: Long, speed: Double): SleepInfo
}
// shared/Shake.kt
class ShakeDetector(peak: Float = 12f, gapMs: Long = 150, windowMs: Long = 1000) { fun sample(size: Float, nowMs: Long): Boolean }
```
Words: `sleep off`, `sleep plus`, `sleep chapter`, `sleep <1..999>`. Native: `{"t":"sleep","mode":"off|minutes|chapter","choice":"30","leftMs":0,"fading":false}`, on a change and with each state push while active. Extras `ts.sleep.mode`, `.choice`, `.leftMs`, `.fading`. Command `app.tendshelf.SLEEP {choice, minutes}`, same package only; `choice` is `off`, `plus`, `chapter` or a number. `BridgeMessage.Sleep(val choice: String, val minutes: Int)`.

### Steps
1. `shared/Sleep.kt`, `shared/Shake.kt`, table tests mirroring `player-full.js`: step edges, 10 s fade ramp, speed 2 in both modes, paused time ignored, skip ahead at 1,499 and 1,501 ms past the end, `CHAPTER_MIN_LEFT_MS`, plus on a chapter timer, shake with one jolt, two jolts inside and outside the window, jolts under the gap.
2. `app/.../SleepTimer.kt`: holds `SleepClock`, tick, sensor lifecycle, sets `exo.volume`, publishes extras at most once a second, and on `Finish` does pause, seek to `min(restAtMs, duration - 1000)`, `exo.volume = 1f`.
3. `PlaybackService.kt`: build it, command, `engine.onBookChanging` keeps minutes mode and turns chapter mode to follow the new book's chapters. `NextBook.kt`: D5.
4. `BridgeProtocol.kt`, `PlayerState.kt` (`sleep: SleepInfo`), `PlayerRemote.kt` (`sleep(choice, minutes)`), `Bridge.kt`.
5. `native-audio.js`: `nativesleep`, `audio.sleep(words)`, `audio.sleepInfo`. `player-full.js`: under `window.tsNativeAudio`, `setSleep` and `sleepPlus` send; `sleepState` is filled from `nativesleep`; no interval, no `setFade`, no `devicemotion`; `drawSleep`, the card and `eyes-free.js` work unchanged through `sleepInfo()`.
6. `docs/roadmap.md`, `docs/manual-checks.md` (shake on a real phone).

### Acceptance (port 8102; `DEBUG_EVAL` to drive the sheet; never `op dump`)
- Sleep 1 min through the page, screen off, activity stopped: `dumpsys media_session` PAUSED at 60 s ±1 s of sound; volume ramp sampled by `read_dump` falls from 1.0 at -10 s; next play at 1.0.
- Pause for 30 s inside the timer: the pause does not count. Speed 2: still real minutes. End of chapter at speed 1.5 on Parts: rests at the chapter's end; Resume starts the next chapter.
- Chapter timer on the last chapter of Short 1 with Single queued: rests at -1 s, nothing popped, queue unchanged. Minutes timer running past the end of Short 1: D5, nothing popped.
- Shake: `adb emu sensor set acceleration 20:0:9.8`, then back to `0:0:9.8`, twice within a second, during the fade: +10 min, volume back to 1. Sensors off after (`dumpsys sensorservice` shows no active connection for the UID). If the emulator gives no sensor events with the screen off: drive `ShakeDetector` through a debug op and record the limit.
- The page reloads mid-timer: card shows the time left. `make visual-compare` on browsers: 0 changed. Eyes-free cycle works in the shell.

### Out of scope
Timer in Auto. Timer in the notification. Surviving a killed process. Sleep schedules.

---

## A7 Downloads and the offline page

**Reused**: Media3 `DownloadManager`, `DownloadService`, `SimpleCache`; `outbox.db`; `GET /api/v1/books/{id}` as the manifest; `?download=1`; `offline.html` and `StartPages`; the `decide` policy; `BookDownloadView` and its words. **Extended**: `BookEngine`, `BookMedia`, `SqliteOutbox`, `SignOut`, `Bridge`. **Replaced**: nothing. **Not used**: `downloads.js` and the service worker, which never run in the shell (decision 0024 point 9).

### Invariants
1. A book is offered offline only when every playable file is complete and its size matches. A half download is never played offline.
2. Sign-out, a change of person and a change of server remove every downloaded byte, row and cover, even if the process dies halfway.
3. The token is sent as a header only. It is never in a URL, a `DownloadRequest`, a cache key, a row or a log.
4. A download needs `permissions.download`; the server's 403 stands.
5. Listening offline obeys the outbox: places are durable first and sync later.
6. `http` and `https` behave alike: nothing here needs a secure context or a service worker.
7. A foreground `dataSync` start happens only from the visible activity, so it is never refused (A5b's class of bug, designed out).
8. Hitting the 6-hour limit loses no progress and never crashes.

### Decisions
- **D1 One `SimpleCache` under `noBackupFilesDir/offline/media`**, held by a process-wide `Offline` object, because a `SimpleCache` directory may have one owner. Bytes stay in Media3's format: no second copy to manage.
- **D2 `getScheduler()` returns null.** A scheduler restarts downloads from the background, which Android refuses for `dataSync`. Instead `MainActivity.onStart` calls `DownloadService.sendResumeDownloads`. Named limit: downloads interrupted with the app closed wait for the next launch.
- **D3 The 6-hour limit.** On Android 15 and later `dataSync` gets 6 hours in 24, then `Service.onTimeout`; Media3's own `onTimeout` already stops the service (read in the bytecode of 1.11.1). `TsDownloadService.onTimeout`: first `pauseDownloads()` and set pref `downloadsLimited`, post "Downloads paused. Open Tendshelf to finish.", then `super.onTimeout`. The timer resets when the app comes to the foreground, so `onStart` clears the pref and resumes. Finished bytes stay in the cache and the next run continues by `Range`. No WorkManager, no `RUN_USER_INITIATED_JOBS` (Android 14+ only, with minSdk 28).
- **D4 One download at a time** (`setMaxParallelDownloads(1)`), as the web keeps a home server calm. `setMinRetryCount(120)` rides out a `?compat=1` 503 for about 10 minutes (guess at Media3's backoff of up to 5 s; proved with Odd).
- **D5 A downloaded book starts without touching the network.** `BookEngine.start` uses the stored manifest and last settings, so airplane mode and a dead server start equally fast. After `begin`, a best-effort `checkRemote` asks the place question as usual.
- **D6 Place offline.** Outbox rows are deleted once sent, so a stored manifest's `progress` goes stale. `offline_books.place_ms`/`place_at` are written beside every outbox row for a downloaded book and passed to `startPlace` as the pending save.
- **D7 Bundled page, no script.** `offline.html` becomes the one list, for offline and for "Downloads" online. Actions are links that `decide` catches, as `/connect` is. Native renders every value escaped. While a download runs, `<meta http-equiv="refresh" content="5">`. Compose stays out (`android.md`).
- **D8 The page's Download button is driven by a new `native-downloads.js`**, Android bucket: it needs the same nodes as `downloads.js` but no chunks, cache or Web Locks, so a shell branch inside `downloads.js` would cost browsers bytes.
- **D9 Downloads returns to the phone's nav**, as a real navigation. A1 removed it because the app "has its own Downloads"; now it does. htmx navigations do not reach `shouldOverrideUrlLoading`, so the item needs `hx-boost="false"`.
- **D10 Daily check**, on `onStart`, at most once per book per 24 h, the rule of 0019 point 10: same files: update the manifest. Files changed: state `stale`, "Needs updating", still plays. 404: remove the book. Redirect or network failure: change nothing.
- **D11 Session expiry keeps downloads** (0012 point 4). Explicit sign-out wipes; a 401 only signs out, and the same person signing back in finds them.

### Data shapes
```sql
-- outbox.db, user_version 2 (SqliteOutbox.onUpgrade; the migration keeps `saves`)
CREATE TABLE offline_books (book_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, title TEXT NOT NULL, author TEXT NOT NULL,
  duration_ms INTEGER NOT NULL, size_bytes INTEGER NOT NULL, manifest TEXT NOT NULL, etag TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, added_at INTEGER NOT NULL, checked_at INTEGER NOT NULL DEFAULT 0,
  place_ms INTEGER NOT NULL DEFAULT 0, place_at INTEGER NOT NULL DEFAULT 0);
```
`state`: `queued`, `downloading`, `paused`, `ready`, `failed`, `stale`. Covers: `noBackupFilesDir/offline/covers/<bookId>`, never a blob (a 2 MiB row breaks a `CursorWindow`). Settings: pref `settingsJson`.
```kotlin
// shared/Manifest.kt: AudioFile gains size: Long; BookManifest gains mayDownload: Boolean
// shared/Offline.kt
enum class OfflineState { QUEUED, DOWNLOADING, PAUSED, READY, FAILED, STALE }
data class OfflineBook(val bookId: String, val userId: String, val title: String, val author: String, val durationMs: Long, val sizeBytes: Long, val state: OfflineState, val placeMs: Long, val placeAt: Long)
data class FilePlan(val fileId: String, val url: String, val cacheKey: String, val size: Long)
interface OfflineStore { fun all(): List<OfflineBook>; fun get(id: String): OfflineBook?; fun put(b: OfflineBook, manifestJson: String); fun manifest(id: String): String?; fun setState(id: String, s: OfflineState); fun notePlace(id: String, ms: Long, at: Long); fun remove(id: String); fun clear() }
fun cacheKey(fileId: String, compat: Boolean): String          // "<fileId>" or "<fileId>:c"
fun downloadPlan(m: BookManifest, paths: ApiPaths, freeBytes: Long): Plan   // Plan.Ok(files, total) | NotAllowed | NoRoom | NothingToPlay; needs total + 200 MiB free
fun checkVerdict(status: Int, old: BookManifest?, new: BookManifest?): Verdict   // Same | Update | Stale | Remove | Nothing
```
`ApiPaths.downloadUrl(fileId, compat)`: `.../api/v1/stream/{id}?download=1`, plus `&compat=1`. Words: `dl add <id>`, `dl remove <id>`, `dl ask <id>`. Native: `{"t":"dl","bookId":"","state":"none|queued|downloading|paused|ready|failed|stale","pct":0}`. Nav: `Nav.Downloads`, `Nav.PlayBook(id)`, `Nav.RemoveBook(id)`, `Nav.TogglePlay`, on bundled paths `/play?id=`, `/remove?id=`, `/toggle`; ids pass `validId` or the result is `Nav.Block`. `/me/downloads` on the origin, main frame: `Nav.Downloads`.
Refusals, in plain words: `NotAllowed` "Downloads of this book are turned off."; `NoRoom` "Not enough room on this phone for this book."

### Steps
1. `shared`: `Offline.kt`, `Manifest.kt`, `ApiPaths.downloadUrl`, `Navigation.kt`, bridge words; table tests including the 200 MiB edge, a book with a missing file, `needsCompat`, each `checkVerdict` row, id rejection on all bundled paths.
2. `SqliteOutbox.kt`: version 2, `SqliteOfflineStore`, migration test with a version 1 database.
3. `ApiJson.kt`/`Api.kt`: `size`, `permissions.download`; `manifest` split into `fetchManifestJson` and `parseManifest(json)`.
4. `app/.../Offline.kt`: lazily builds `StandaloneDatabaseProvider`, cache, own `DefaultHttpDataSource.Factory`, `DownloadManager` (D4, `Requirements(NETWORK)`); `add(id)`, `remove(id)`, `wipe()`, listener mapping Media3 states to rows; `READY` only after `cache.getCachedBytes(key, 0, size) == size` for every file (invariant 1), else `failed`. `wipe()`: pref `wipePending` with `commit()`, stop, `removeAllDownloads`, release, `SimpleCache.delete`, delete covers and rows, clear pref. `Offline.init` finishes a pending wipe before any use.
5. `app/.../TsDownloadService.kt` and manifest: `FOREGROUND_SERVICE_DATA_SYNC`, `android:foregroundServiceType="dataSync"`, `exported="false"`. Notification channel `downloads`, id 4245, with `DownloadNotificationHelper`. D3.
6. `BookMedia.kt`: wrap in `CacheDataSource.Factory().setCache(..).setUpstreamDataSourceFactory(http).setCacheWriteDataSinkFactory(null).setFlags(FLAG_IGNORE_CACHE_ON_ERROR)`; `MediaItem.setCustomCacheKey(cacheKey(...))`. A file being streamed is not written.
7. `BookEngine.kt`: D5. `ProgressWriter.kt`: D6.
8. `SignOut.kt`: `wipeLocal` and `leave` call `Offline.wipe()` after `stopPlayback`, delayed 500 ms so a loader is not reading a released cache (`wipePending` makes this durable). `ConnectFlow.kt`: a different `userId` at sign-in wipes first.
9. `StartPages.kt`, `offline.html`, `start.css`: rows from the store, per the boards `DownloadsPhone` and `StateOffline`; `decide` wiring in `ShellWeb.kt`, `pages.downloadsUrl`; `goOffline` keeps its address and Try again. `MainActivity.kt`: resume, D10 checks.
10. Web, one commit: `components/nav.templ` (`navFor` keeps the item with `NoBoost`; `TestA1ShellPage` changes on purpose); `layout.templ` loads `js/native-downloads.js` for `android`; `jsAndroidFiles`; new `native-downloads.js` (show nodes, `dl ask` after `htmx:after:swap`, label from the `data-ts-msg-*`), with tests in `internal/web`.
11. `Bridge.kt`: `dl` words, `dl add` only while started; `docs/roadmap.md`, `manual-checks.md`.

### Acceptance (port 8103, `http://10.0.2.2`, which is also the `http` no-worker proof)
- Book page shows Download only with the right; with `can_download` off the API answers 403 and no bytes stay.
- Download Parts (3 files): notification with progress, `state=ready`. Airplane mode, `am force-stop`, launch: the bundled page lists it, Play sounds within 2 s, chapters and skip work, screen off 5 minutes keeps playing, `saves` grows. Network back: rows flush in order; stats within 10 s.
- Finished elsewhere first: Continue there question appears after return; nothing moved before.
- Odd (`needsCompat`): waits through 503s, then ready. Kill the process mid-download: launch resumes without refetching finished ranges (server log). Screen off in Doze: the FGS keeps going, `uid_state` is FGS.
- **6-hour limit**: inside the lock `adb shell am compat enable FGS_INTRODUCE_TIME_LIMITS app.tendshelf.android` and `adb shell cmd device_config put activity_manager data_sync_fgs_timeout_duration 60000` (guess that the image allows it; else a debug op calling `onTimeout`), throttled test download: at 60 s no crash, `Disallowed` absent, notification shown, row `paused`, no ANR in `dumpsys activity exit-info`. `front`: resumes and completes. Restore both settings.
- **Sign-out**: while a downloaded book plays and another downloads: sound stops, `run-as app.tendshelf.android ls -R no_backup/offline` is empty, table empty. Kill between the pref and the delete: next launch finishes. A different person signing in: gone first. Same person after a revoked token: kept.
- `run-as ... grep -r` over the files and logcat for the token: none. Removing a book frees its bytes; removing the book being played stops it.

### Out of scope, and why
"Remove finished books", "Wi-Fi only" and "Next in series" (cheap here, but later; see "After M8"). Offline bookmarks (needs a queue and UI). Text (A9). Moving downloads to shared storage (library rules, and sign-out could not wipe). Encryption at rest (0012: as private as the device lock).

---

## A8 Android Auto

**Reused**: `MediaLibraryService`, the session, `BookPlayer` (next and previous are chapters), `BookEngine`, `/api/v1/continue`, `/queue`, `/shelves`, `/shelves/{id}`, `/search`, A7's store. **Extended**: `SessionCallback`, `BookPlayer`, manifest. **Changed in the plan**: `android.md` named a Series tab; `/api/v1` has no series list, so **Shelves** takes that place.

### Invariants
1. Only trusted controllers can browse. Today any installed app may connect and gets the default library commands; a family's titles must not reach them.
2. The driver never needs the phone: no question or prompt blocks the car.
3. Resume always uses our place (`startPlace`), never Media3's idea of position.
4. A signed-out or offline car gets words, not a spinner.
5. Covers reach the car without a token and without an open provider.
6. Nothing starts on connect; sound starts only on a command.

### Decisions
- **D1 `onConnect`**: `controller.isTrusted`, `session.isAutoCompanionController(c)`, `isAutomotiveController(c)` and the own package keep library commands. Others keep playback commands only, as today's notification and headset need; they can still read the current title, as with every media app. All names checked with `javap`.
- **D2 Four tabs**: Continue, Up next, Shelves, Downloaded. Auto shows four at most.
- **D3 A tree item is a book, not a file.** `MediaItem` id `book:<id>`, no URI. `onSetMediaItems` returns the items unchanged and `BookPlayer.setMediaItems` routes to `engine.start(id, null)`: a book stays one Media3 window built by `BookEngine`, with no second path.
- **D4 Voice and search.** An item with `requestMetadata.searchQuery` runs `/api/v1/search?q=&limit=1` and starts the first book; none: `sendError(SessionError(ERROR_NOT_SUPPORTED...))` with "I couldn't find that book."
- **D5 Conflict in the car**: the ask stays a phone notification, saves stay held, the book plays on. Nothing moves; the answer can wait until parked (invariant 2 and A5 invariant 3).
- **D6 Covers** through a small read-only `CoverProvider` (`content://app.tendshelf.android.covers/<bookId>`), `exported="true"`, which answers only callers for whom `isTrusted` held in `onConnect` (package set in memory). Reading from A7's file or a cache file. Binder-sized `artworkData` is rejected.
- **D7 Speed button** cycles 1.0, 1.25, 1.5, 1.75, 2.0, 0.75, with command `app.tendshelf.SPEED_CYCLE` for trusted controllers; saved with `PUT /books/{id}/speed`, best effort. It is the only custom command outside the own package.
- **D8 `onPlaybackResumption`** returns `book:<lastBookId>` from pref `lastBook`. That covers Media3's old report of resuming at the first track: D3 and invariant 3 ignore its position.
- **D9 Sign-out state**: `onGetLibraryRoot` answers normally; `onGetChildren` errors with `ERROR_SESSION_AUTHENTICATION_EXPIRED`, "Open Tendshelf on your phone to sign in." Offline: Downloaded only, Continue from A7's store.

### Data shapes
```kotlin
// shared/LibraryTree.kt
sealed interface Node { object Root; object Continue; object UpNext; object Shelves; data class Shelf(val id: String); object Downloaded; data class Book(val id: String) }
fun parseNode(id: String): Node?          // "root", "continue", "queue", "shelves", "shelf:<id>", "downloaded", "book:<id>"; null otherwise
fun nodeId(n: Node): String
data class Page(val page: Int, val size: Int)
fun slice(total: Int, p: Page): IntRange  // Media3's paging over a fetched list
```
`res/xml/automotive_app_desc.xml`: `<automotiveApp><uses name="media"/></automotiveApp>`. Manifest: `<meta-data android:name="com.google.android.gms.car.application" android:resource="@xml/automotive_app_desc"/>`. The name is a string only; no Google module is added. Extras: `MediaConstants.EXTRAS_KEY_CONTENT_STYLE_BROWSABLE` and `_PLAYABLE`: list for Continue and Up next, grid for Shelves and Downloaded.

### Steps
1. `shared/LibraryTree.kt`, tests. `Api.kt`/`ApiJson.kt`: `continueList`, `queue`, `shelves`, `shelf`, `search`, `putSpeed`, into `shared` cards.
2. `app/.../LibraryTree.kt`: `onGetLibraryRoot`, `onGetChildren`, `onGetItem`, `onSearch`, `onGetSearchResult`, on the `io` executor with Guava `SettableFuture`; 5 s budget, then a plain error.
3. `PlaybackService.kt`: D1; `SessionCallback` delegates; `onSetMediaItems`, `onPlaybackResumption`; `BookEngine` writes pref `lastBook`; button in `skipButtons()`. `BookPlayer.kt`: `setMediaItem`, `setMediaItems` overloads routed by D3; `prepare` and `play` with a book loading stay harmless (empty timeline, `bookId` guard already in `NextBook`).
4. `app/.../CoverProvider.kt` and manifest. `BookEngine.fail`: when a car is connected, `session.sendError` with the plain word.
5. Debug only: `DEBUG_READ --es op browse --es node <id>` runs a `MediaBrowser` and logs titles, so the tree is tested before any head unit exists.
6. `docs/manual-checks.md`: the DHU and car table below. Roadmap note.

### Acceptance
**On the emulator, no head unit (port 8104).** `op browse` for each node, online and in airplane mode; paging; a shelf; empty queue; search. Bad node id: error. A foreign app cannot be simulated without a second APK, so the `onConnect` choice is a pure function `allowedCommands(trusted: Boolean, auto: Boolean, own: Boolean)` in `shared`, table tested, and the emulator run is recorded as not covering it. Resume: `op browse` then play `book:<id>`: the place is the server's. After 11 minutes paused with the service out of the foreground (Doze on), a transport `play` through `cmd media_session dispatch play`: no silent hang; `Allowed`, or A5b's notice.

**Desktop Head Unit (needs the operator, see below).** Tabs, covers, chapter and speed buttons, day and night, voice query, resume after disconnect, signed-out words, airplane-mode Downloaded.

### What to install
- `sdkmanager --install "extras;google;auto"`. This machine lists it as `extras/google/auto`, version 2.0.0; use that spelling if the semicolon form is refused. It lands in `$ANDROID_HOME/extras/google/auto/desktop-head-unit`. Run `ldd` on it first: it may need `libsdl2`, `libsdl2-ttf` and `libportaudio2` from apt (guess).
- The binary alone is not enough. The AVD's Android Auto is a **stub**, so no head-unit server exists on it. Either (a) sign in to Play on the AVD and update Android Auto (operator's account, entered by the operator; guess that this image accepts it), or (b) **recommended**: a real phone on USB with this app installed. Then: Android Auto settings, tap Version ten times, Developer settings, **Unknown sources** on, **Start head unit server**; `adb forward tcp:5277 tcp:5277`; start `desktop-head-unit`.

### What a real car adds
Wireless and USB pairing; the phone locked in a pocket in real Doze, reconnecting at ignition; audio focus against the radio, calls and navigation prompts; steering-wheel buttons; microphone and Assistant routing for voice; tunnels with the network gone mid-chapter; OEM screen shapes; the driving-mode limits on list depth and length; and whether a sideloaded app keeps its place under "Unknown sources" across an Android Auto update. Only that last point decides distribution.

### Out of scope
Android Automotive OS (a different target). Car App Library templates. Sleep timer in the car. Series tab (needs a route; add it when asked). Browsing the full library (Shelves and search cover it).

---

## A9 Offline reading in the app

**Reused**: `GET /offline/books/{id}/manifest`, `GET /offline/books/{id}/read`, `reader-offline.js`, `flushReading`, `offline.js`, A7's store, wipe, daily check, `dl` words and bundled page, `shouldInterceptRequest`. **Extended**: the manifest (`assets`), `held()`, `NoOffline`. **Not built**: a second reader, a worker, or any native text renderer.

**The `http` case.** A worker never runs in the shell, and on plain `http` `caches` does not even exist. So native stores the same addresses 0019 lists, and `shouldInterceptRequest` plays the worker's part. Nothing depends on a secure context.

### Invariants
1. Same rights as 0019 point 2: `share.ReadText` and `share.DownloadBook`, enforced by the server's routes.
2. No shell, no offline book: `text_state` is `ready` only after the shell is written, last.
3. Interception applies to the exact stored origin (scheme, host, port) and only GET. No other origin is ever answered.
4. A stored response keeps its `Content-Type` and `Content-Security-Policy`. Offline is not weaker than online.
5. While the server is reachable the server answers; the store is used only when it is not.
6. The session cookie is read by native only to fetch the person's own text from the server's origin, and is never logged, stored or put in a row. The token stays under `/api/v1/`.
7. The wipe of A7 covers text.
8. A place is written before any send (0019 point 7, unchanged).

### Decisions
- **D1 Fetch with the WebView's cookie, not the bearer.** `/offline/...` and `/hx/books/...` are cookie routes, and a token "works only under `/api/v1/`" by rule. Extending the token to page routes would widen an authorization contract for one feature. `CookieManager.getCookie(origin)` gives the same session the page uses, and the stored shell comes out rendered for `ts_shell=android`. If the session is gone (redirect to `/login`), the state is `signin`, retried on the next page load. The alternative, mirroring the routes under `/api/v1/`, needs HTML under the JSON API; rejected.
- **D2 Reachability, not a per-request race.** 0019's 3 s network-first fits a worker; `shouldInterceptRequest` must return one answer. `OfflineNet.reachable()`: no default network: false; else `GET /healthz`, 1.5 s connect and read, no credentials, cached 10 s, invalidated by a main-frame error. Called only on WebView's worker thread and only for URLs the store could answer. Airplane mode with Tailscale up (0019's hang) is therefore fast.
- **D3 A stored part is not refreshed on read** (0019 point 6's freshness), because online reads go to the server. The daily check's `revision` covers the rest. Accepted: a highlight made on another device may be missing offline for a day.
- **D4 Static files**: the stored shell references hashed files, which browsers get from the precache. The manifest gains `"assets"`, from `s.assets.Precache()`: additive, and no new route. Fetched without credentials into `offline/static/<build>/`; the old build's folder goes once every shell has been refreshed.
- **D5 Load `offline.js` in the Android shell.** It already holds the reading flush and the bookmark queue, with the worker and downloads guarded by `data-shell`; a second flusher would be the parallel system the rules forbid. `server.go` lines 397 and 425 become `NoOffline: shell != "" && shell != "android"`. `/sw.js` still answers 404 (0026 D10 stands). `TestA1ShellPage` changes on purpose; note it in a new decision.
- **D6 `held(id)` in `reader-offline.js` is true in the shell**, because `ts.offline` does not exist there; a sent record then stays as the last local place. Without it the stored shell would reopen at an old place.
- **D7 One Download** (0019 point 1): `dl add` fetches audio and text per the person's rights. A text-only book has no audio row. Text fetching runs on `Offline`'s executor from the visible activity; small, so no foreground service. Interrupted: continues on the next start.

### Data shapes
```sql
-- outbox.db, user_version 3: added to offline_books
ALTER TABLE offline_books ADD COLUMN text_state TEXT NOT NULL DEFAULT 'none';  -- none|fetching|ready|signin|failed
ALTER TABLE offline_books ADD COLUMN text_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE offline_books ADD COLUMN edition_id TEXT NOT NULL DEFAULT '';
```
Files under `noBackupFilesDir/offline/text/<bookId>/`: `s<sha256(path+query)>`, `index.json` `{"<path+query>":{"file":"s...","type":"text/html","csp":"..."}}`, written to `.tmp` and renamed. Shell keys: `/books/{id}/read`, `/books/{id}/read?sheet=contents`, as 0019 point 4.
```kotlin
// shared/OfflineRoutes.kt: pure, tables tested
sealed interface OfflineRoute { data class Shell(val bookId: String, val key: String); data class Part(val bookId: String, val key: String); data class Image(val bookId: String, val key: String); data class Static(val key: String) }
fun offlineRoute(url: String, origin: Origin): OfflineRoute?      // null: other origin, other path, or `download=1`
fun storeKey(pathAndQuery: String): String
```
Go, additive: `offlineManifest` gains `Assets []string` `json:"assets"`. Test that every `src` and `href` of a stored shell for `android` is in `assets`, including `native-audio.js`; if the precache lacks it, the same list plus that file.

### Steps
1. Go: `routes_offline_read.go` `assets`, test; `server.go` D5; `TestA1ShellPage`; `reader-offline.js` D6; `docs/decisions/00NN-offline-script-in-the-app.md` (next number at build time).
2. `shared/OfflineRoutes.kt`, tests: query order preserved, `?edition=` kept, encoded ids, other port refused, `https` against an `http` origin refused, `/books/{id}/read?ch=2` maps to the shell key (the shell reads its own query).
3. `SqliteOutbox.kt`: version 3, migration test from version 2 with rows.
4. `app/.../TextStore.kt`: `fetch(bookId)`: manifest, parts, images, assets, contents shell, then shell; `lookup(route)`; `remove(bookId)`; size check from `Content-Length`; a 404 at the manifest means no text right, so `text_state` stays `none`, not failure.
5. `app/.../OfflineNet.kt`: D2.
6. `ShellWeb.kt`: `shouldInterceptRequest`: bundled pages first, as now; then `offlineRoute`; if the store has it and `!reachable()`, answer with stored bytes, status 200, type and CSP; otherwise null. `onReceivedError` invalidates reachability.
7. `Offline.kt`: `dl add` and the daily check cover text (0019 point 10: `revision` differs: refetch, shell last; 404: remove text; redirect or failure: nothing). `wipe()`: includes `text/` and `static/`.
8. `StartPages.kt`, `offline.html`: a Read button for rows with text (`origin + /books/{id}/read`), the word `/offline?text=` uses. One resume button per book: the format used last, the other a plain secondary button.
9. Roadmap, `docs/manual-checks.md`, `docs/architecture.md` (the shell as the worker's stand-in).

### Acceptance (port 8105, `http://10.0.2.2`)
- A synthetic EPUB with a picture and a TXT, generated by the test tool; no real book. Download; airplane mode; `am force-stop`; launch: bundled page, Read opens the reader; pages turn, contents works, chapter keys, links, images, copy, time left. `dumpsys` no crash; `DEBUG_EVAL` shows `navigator.serviceWorker.controller` null.
- CSP present on the stored shell (`DEBUG_EVAL` of a blocked inline script stays blocked). Highlight, search, settings: not drawn, or the plain toast (0019 point 9).
- Read offline, close, network back, open any server page: `POST /reading/{id}` arrives, time and characters counted once (replaying gives no extra), reading stats within limits. Reopen offline: the newest place.
- Server stopped with Wi-Fi up: same behaviour within 2 s (D2). Server back: online reader with its full tools.
- Rights: `can_download` off: manifest 404, no text stored, no failure. Revoked after download: daily check removes it. Session cookie removed (`DEBUG_COOKIES_CLEAR`): `signin`, then retried after sign-in.
- Another origin in the WebView is never answered: `offlineRoute` tests plus a hostile page on port 8099 fetching a stored path gets its own origin's answer.
- Sign-out: `offline/` empty, `ts.offline*` gone. Logcat holds no cookie or token.

### Out of scope, and why
Offline highlights, notes and bookmarks (0019, not handled). Offline search. A native reader (later, "Which pages go native first"). Scheme-specific behaviour: none exists; `https` is not separately run here because the test server is `http`. Recorded as a limit, with the code having no scheme branch.

---

## A10 Release

**Reused**: `make apk-debug`, `android/tools/check-release.sh`, Gradle verification, `release.yml`, `RELEASE_TOKEN`, the version scheme. **Extended**: `build.gradle.kts`, `Makefile`, CI. **New**: launcher icon (the manifest has none today), notification icon, metadata.

**No step here creates a keystore or holds a credential, even a throwaway one.** The operator makes the key, keeps it, and adds the secrets. The builder proves the unsigned path; the first signed build is the operator's.

### Invariants
1. A release APK is never signed with a debug key. No credentials: an unsigned APK, clearly named, or a failed job; never a silent fallback.
2. No secret in git, in Gradle properties committed to the repo, or in a log.
3. Same source, same JDK, same SDK, same bytes (unsigned APK), from any path.
4. `check-release.sh` passes on every release APK.
5. `versionName` is `YEAR.RELEASE.FIX`; `versionCode` rises with every release.
6. The server binary, `make build`, `make check` and `CGO_ENABLED=0` are untouched.

### Decisions
- **D1 Environment only**: `TS_ANDROID_KEYSTORE`, `TS_ANDROID_KEYSTORE_PASSWORD`, `TS_ANDROID_KEY_ALIAS`, `TS_ANDROID_KEY_PASSWORD`, read by `signingConfigs.release`, used only when all four are set.
- **D2 Version**: `-PtsVersion=2026.3.0`; `versionCode = year * 100000 + release * 1000 + fix`, which gives 202603000 and fits an `Int` for centuries, rejected if `release > 99` or `fix > 999`. Without the property: `0.0.0-dev`, code 1. Dev builds put `git describe` in a string resource only, so the manifest stays plain.
- **D3 Reproducibility settings**: `dependenciesInfo { includeInApk = false; includeInBundle = false }`, `vcsInfo.include = false`, `buildToolsVersion = "36.0.0"`, minify stays off for the first release (no R8 nondeterminism or keep-rule risk; size about 15 MB), ART profile tasks disabled if two builds differ only there (guess that they will).
- **D4 CI job on `[tend-ci, linux-amd64]`**, not the arm64 runners the other jobs use: Google's Linux build tools are x86-64 (guess at no arm64 `aapt2`; the job's first step proves the runner). SDK from a pinned command-line tools archive with its SHA-256, into the job. Runs when `android/**` or `internal/web/static/**` change, and on tags. Note the Surfshark caveat in memory: with the VPN up, the workstation runner's containers have no egress.
- **D5 Release upload is a separate job after `release`**, so a missing Android secret cannot block a server release. It fails visibly and publishes no APK.
- **D6 Metadata in fastlane layout** under `android/fastlane/metadata/android/en-US/`: the shape F-Droid reads and Play tools accept, so no channel gets its own copy.

### Data shapes
`tsVersionCode(v: String): Int` is a function in `android/app/build.gradle.kts`, because a build script cannot import `:shared`. Task `printVersionCode` prints it, and `android/tools/version-code-check.sh` runs that over a table. Artifacts: `tendshelf-<version>.apk`, `tendshelf-<version>.apk.sha256`, `tendshelf-<version>-unsigned.apk` when unsigned. Repository secrets, names only: `TS_ANDROID_KEYSTORE_B64`, `TS_ANDROID_KEYSTORE_PASSWORD`, `TS_ANDROID_KEY_ALIAS`, `TS_ANDROID_KEY_PASSWORD`.

### Steps
1. Icons: adaptive launcher from `img/tendshelf-app-icon.svg` as a vector drawable (monochrome layer too), `android:icon`, `android:roundIcon`; `ic_stat_tendshelf` for `AskPrompt`, downloads and Media3's `DefaultMediaNotificationProvider`; `android.R.drawable.ic_media_play` goes.
2. `android/app/build.gradle.kts`: D1, D2, D3. `versionName = "0.0.0-a2"` goes.
3. `Makefile`: `apk` target: `cd android && ./gradlew --no-daemon assembleRelease -PtsVersion=$(VERSION)`, then `android/tools/check-release.sh`, and name the output. `apk-debug` stays. Neither joins `check` or `build`.
4. `android/tools/repro-check.sh`: two fresh clones at different paths with different `GRADLE_USER_HOME`, same `SOURCE_DATE_EPOCH`, build unsigned, compare SHA-256; on a difference list the entries with `unzip -v`.
5. `.gitea/workflows/ci.yml`: job `android`: tests of `shared`, `assembleDebug`, `assembleRelease`, `check-release.sh`. `nightly.yml`: `repro-check.sh`.
6. `.gitea/workflows/release.yml`: job `apk`, `needs: release`: decode the secret to a `mktemp` file, build with D1, `apksigner verify --print-certs`, delete the file in an `always` step, upload through the release API.
7. `android/fastlane/metadata/...`: title, short and full description, changelog `<versionCode>.txt`, icon, phone screenshots from a synthetic library. `android/fdroid/app.tendshelf.android.yml`: a draft recipe (`subdir: android/app`, `gradle: yes`, `UpdateCheckMode: Tags`, `AllowedAPKSigningKeys` left empty for the operator).
8. `docs/android-release.md`: the operator's steps, no values. `README.md`, `CHANGELOG.md`, `AGENTS.md` (`make apk`), `docs/architecture.md`. Roadmap M8 row.

### Acceptance
- `make apk` with no `TS_ANDROID_*`: `tendshelf-<v>-unsigned.apk`; `check-release.sh` clean; `apksigner verify` reports unsigned; `aapt2 dump badging` shows `versionName` and `versionCode`, the icon, `targetSdk 36`, and no debug flag.
- `repro-check.sh`: identical SHA-256 twice. If not, the differing entries are reported, not patched over.
- `version-code-check.sh`: 2026.1.0, 2026.1.1, 2026.2.0, 2027.1.0 strictly rise; bad inputs fail the build.
- CI job green on a push; the release job fails visibly with secrets absent, and a server-only tag still publishes.
- The builder stops at the unsigned file and reports `CONTINUE: operator signs`. Installing a signed APK on the emulator is the operator's first run, with the commands in `docs/android-release.md`.
- A secret-scan over the build log: no `TS_ANDROID_` value (masked), no keystore in `git status`.

### Out of scope
Play Console setup, Play App Signing, F-Droid submission, and developer verification: all the operator's accounts. App bundles (`bundleRelease`) unless Play is chosen; then one line in the Makefile.

### Distribution: recommendation
**Recommended: a signed APK first (Gitea releases, one key held by the operator), Google Play as the second channel for family listeners and cars, F-Droid third once builds are reproducible.**
1. The APK ships now, with no account, and fits "one binary, nothing to keep up to date, self-hosted"; it needs no Google module in any channel.
2. Android Auto is why Play cannot be skipped: a sideloaded app shows in Auto only with developer "Unknown sources", an unfair ask for a family listener (third-party guides; confirm in A8's DHU run). The car is the vision's first listening place, so Play is a real need, not a convenience.
3. Developer verification reaches sideloading too: from 30 September 2026 in Brazil, Indonesia, Singapore and Thailand, global in 2027 (as of `android.md`, re-check at release). Register the APK key early, whichever channel comes first.
4. F-Droid fits the project, and A10's reproducibility makes it cheap, but its signing conflict with verification is open. Developer-signed reproducible builds would keep one key for APK and F-Droid. Keep that door open; do not depend on it.
5. Cost of two channels: Play App Signing means a Play install and an APK install differ in signature and cannot update each other, so a person picks one and stays. Guess: new personal Play accounts need a closed test before production; check before promising a date.
Nothing in A10's code depends on this answer, except `bundleRelease` if Play is chosen.

---

## After M8: natural additions, in this order

Each is its own package with its own plan, after the first release.
1. **Skip silence** (S): `ExoPlayer.setSkipSilenceEnabled`, from `L7`. First because it is one call behind a setting; check it against `listenedMs` and the silent-range logic in `BookEngine`.
2. **Icon quick actions** (S): dynamic shortcuts for Resume and the latest book. Needs A5b's refusal path proved first: a shortcut tap is an allowed start, and shortcuts carry no token.
3. **Home-screen widget** (M): cover, play, skip, chapter (`L9`). Reads the session and `PlayerRemote`'s snapshot; a tap is an allowed start.
4. **Auto-download of the next book** (M): the first download the person did not start, so A7's D2 no longer holds. It needs `androidx.work` (approved in 0024), a quota rule and Wi-Fi only. On Android 14 and later a user-initiated job may fit better; decide then.
5. **Native sound modes and volume boost** (L): Media3 `AudioProcessor`s for the four modes of decision 0022. Last, as a second implementation that must match the web by ear.
Also approved for later, unordered: Remove finished books, native offline library, read aloud with the device's text-to-speech.

## Which pages go native first

The operator's order stands: the player, then Home, then the reader's page turns; Settings and admin stay web pages. In practice:
1. **Full player screen**: `PlayerRemote` was built as the seam for this, and it has the most to gain from gestures and a bound session. The web bar remains until it is done.
2. **Home**: Continue and Up next read the same `/api/v1` routes A8 uses, so the tree's data layer is reused.
3. **Reader's page turns only**: the native gesture over the existing page, not a native renderer, which would be a second reader.
A native screen needs a UI decision first: `android.md` rejected Compose for "one look, and fewer modules", and using it means a new approved dependency, in place of plain Views. Decide that before the player screen is planned.

## For the operator

**Needed before the package can finish**
1. **A5b, A6, A7**: JS budgets. `native-audio.js` needs about +250 B (cap to 3,072 B), the Android bucket about +1 KB for `native-downloads.js` (to 4,096 B), `player.js` about +100 B, `player-full.js` about +200 B. Estimates; the builder reports measured numbers.
2. **A8**: a real phone on USB with Android Auto, or a Google sign-in on the AVD by you (the AVD's Android Auto is a stub). Install the DHU with the command in A8.
3. **A10**: the distribution choice above; create and hold the signing key; add the four repository secrets; decide on a Play account and developer verification.
4. **A10**: the CI job needs the `linux-amd64` runner reachable, which the Surfshark VPN can cut.

**Decisions to confirm**
5. A9 D5: `offline.js` loads in the Android shell, reversing A1's choice (`/sw.js` stays 404). A9 D1: native reads the WebView's session cookie for text.
6. A7 D2: downloads do not continue with the app closed. A7 D9: Downloads returns to the phone's nav.
7. A8: Shelves replaces the Series tab until a series route exists. A8 D5: no place question in the car.
8. Copy not in the deck: "Tap to keep listening", "Downloads paused. Open Tendshelf to finish.", "Not enough room on this phone for this book.", "Needs updating", "Open Tendshelf on your phone to sign in.", "I couldn't find that book."
9. Still open from the last plan: the bridge on plain `http` (A4). The no-hand-out window of A4 closes with A6.

**Real-phone checks only you can run**: a screen-off hour; deep Doze; shake; a real car.
