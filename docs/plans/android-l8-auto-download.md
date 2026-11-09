# L8: the next book is ready on the phone (builder's contract)

Reuses: `Offline.kt` (cache, one manager, rows, `enqueue` split out of `add`), `downloadPlan`/`SPARE_BYTES`, `TsDownloadService`, the skip-silence setting path, `ServerGate`, `seriesNextBook`. Extends: `/api/v1/queue/next` with a read-only GET. Replaces: A7 invariant 7 and the D2 wording only. No new dependency; no app database change.

## 1. Invariants
1. Off until the person turns it on. Off means no peek, no fetch, and unfinished automatic downloads are removed.
2. An automatic download never moves a byte unless the active network is unmetered. It carries `AUTO_HOLD` from the moment it exists; only `releaseHeld` clears it, and only with Wi-Fi and an allowed foreground start.
3. It is never the thing that fills the phone: at most `AUTO_TOTAL_BYTES` kept, at least `AUTO_SPARE_BYTES` free after the book, and an unfinished one gives way below `SPARE_BYTES`.
4. The peek pops nothing. `POST /queue/next` stays the only pop, and stays sent once.
5. The right comes from the server: `permissions.download` (`share.DownloadBook`, `routes_api_read.go:179`), through `downloadPlan`; `routes_stream.go:68` checks it again on every `?download=1` byte. A refusal is silent and fetches nothing.
6. A refused foreground start never crashes, never downloads without a foreground service, and loses nothing: the book stays held until the next allowed moment.
7. A person's own download always goes first, and pressing Download on an automatic book makes it theirs.
8. No automatic path deletes a finished book. The only automatic removals are unfinished automatic downloads (switch off, or room gone).

## 2. Decisions
- **D1 Default off, one switch, Wi-Fi always.** It is the first download nobody asked for; there is no mobile-data option, so no second switch.
- **D2 The switch is a server setting** (`playback.auto_download`, same path as `skip_quiet`, shown only in the app). The person's phones share it; each phone applies its own quota. A per-phone setting would need a native screen or a second settings store.
- **D3 Quota, values and units.** `AUTO_TOTAL_BYTES` = 5 GiB (5 × 1024³ B), the sum of `sizeBytes` of rows still marked automatic, finished or not. `AUTO_SPARE_BYTES` = 2 GiB free after the book. `downloadPlan` runs first, unchanged, so `SPARE_BYTES` still applies; 2 GiB is a stricter floor on top. `roomFor` and `dl room` do not change: they serve what a person starts. At the quota it stops and says so; it never deletes.
- **D4 Trigger: the last 2 hours of audio** (`AUTO_NEAR_END_MS`, book time, ignoring speed). A fixed time, not a percent, because it must allow for a slow home upload and for a Wi-Fi moment arriving before the commute. A book under 2 hours qualifies at once.
- **D5 Finished books are not removed.** The quota makes removal unnecessary. Removal stays the separately approved "Remove finished books"; `next-stage.md:54` is reworded.
- **D6 The honest result: it starts while the book is playing or the app is open, on Wi-Fi. A started download finishes with the screen off.** It does not start with the app closed and nothing playing.
- **D7 Start from the playback service while it is playing. No scheduler, no WorkManager.** Evidence below.
- **D8 WorkManager rejected.** A worker that outlives its 10-minute window needs `setForeground`, which is the same foreground start and is refused the same way from the background; a user-initiated job needs a person's action. It would add a scheduler and a module without getting one more byte through. `getScheduler()` stays null.
- **D9 Per-download stop reason, not `setRequirements(NETWORK_UNMETERED)`.** Requirements are manager-wide and would stall a person's own download on mobile data. A second manager or cache is a parallel system, and a `SimpleCache` directory has one owner.
- **D10 The Wi-Fi check runs in the existing 1 s `pollTick`**, which runs only while a file is downloading. No watcher lifecycle. Named limit: up to about 1 s of data after Wi-Fi drops.
- **D11 Marks live in prefs `offline`** (`autoBooks`), not a new column. The mark is written before the row; a stale mark without a row counts as nothing.
- **D12 Any loss of Wi-Fi holds.** Resuming needs an allowed moment, so a blip with nothing playing waits for the next play or app open.

### D7 evidence (read 2026-11-08, read-only)
- developer.android.com `restrictions-bg-start` (updated 2026-10-28): the exemption list does **not** name "already has a foreground service".
- AOSP `android16-release`, `ActiveServices.shouldAllowFgsStartForegroundNoBindingCheckLocked`: allowed when the uid has `PROCESS_CAPABILITY_BFSL` and a process is at or above `PROCESS_STATE_BOUND_FOREGROUND_SERVICE`. `OomAdjuster`: a regular (non-short) foreground service sets `PROCESS_CAPABILITY_BFSL` and FGS procstate. The `ActivityManager` comment says the same.
- Measured in this repo (`android-a6-a10.md:40`): while playing, `procState=FGS,cap=---NFU-T`; `F` is BFSL (`ActivityManager.printCapabilitiesSummary`). Denied at `uidState: SVC` (`:39`), i.e. with no foreground service, which is why this start is gated on `exo.isPlaying`.
- So this is platform behaviour, not a documented promise. Android 17 is unread (guess: unchanged). Invariant 6 makes a change harmless; E2 proves it on the emulator.
- A denial throws `ForegroundServiceStartNotAllowedException` (an `IllegalStateException`) at the caller, also when the `dataSync` time limit is used up, so one catch covers both. Media3 1.11.1 `DownloadManagerHelper.restartService` catches it too (bytecode read).

## 3. Steps, in order
Go first, then shared, then app, then docs. `A` = `android/app/src/main/kotlin/app/tendshelf/android`, `S` = `android/shared/src/commonMain/kotlin/app/tendshelf/shared`.

1. `internal/users/settings.go`: `AutoDownload bool` (comment: Android app only, decision 0035), `keyAutoDownload = "playback.auto_download"`, in the save list and the load switch beside `keyQuietSkip`. Default false.
2. `internal/web/routes_api_settings.go`: `AutoDownload bool` with `json:"autoDownload"` in `apiSettings`, `apiSettingsOf`, and a `try("autoDownload", apiSettingsBadBool, ...)` after `skipQuiet`.
3. `internal/web/me_views.go`: pass `AutoDownload` into `SettingsView` (:152); `flag("auto_download", &next.AutoDownload)` (:184). An absent key changes nothing, so a browser's form cannot turn it off.
4. `internal/web/pages/settings.templ`: `AutoDownload bool` in the view; inside `if v.InApp` (:225), after `st-quiet`: `@switchRow("st-auto", "auto_download", "Download the next book on its own", "When a book is nearly over and you're on Wi-Fi, the next one downloads so it's ready. Uses up to 5 GB. Works in this app only.", v.AutoDownload)`. Run `make generate`.
5. `internal/web/static/js/native-feel.js`: `else if (box.name === "auto_download") bridge.postMessage("autodl " + (box.checked ? "1" : "0"));`, and the word in the header comment.
6. `internal/web/routes_api_queue.go`:
   - Extract `func (s *server) nextPlayable(ctx context.Context, u users.User, b library.Book) bool` from the identical condition in `popQueue` and `seriesNextBook` (Can `ListenBook`, not refused, not missing, not `BookText`). Both call it.
   - Register `GET /api/v1/queue/next` to `getAPIQueueNext`. Query `ended`, optional; longer than `endedMaxLen` is 400 `apiBadField(w, "ended", queueErrEnded)`.
   - Walk `s.lib.Queue(ctx, s.scope(r), u.ID)` in order: skip `e.Book.ID == ended`; `s.lib.Book`, `ErrNotFound` skips, other errors 500; skip unless `nextPlayable`; `s.lib.BookDetail`, `ErrNotFound` skips; first hit answers 200 `{"book":card,"source":"queue"}`. None: `s.writeSeriesNext(w, r, ended, true)`. Set `Cache-Control: no-store`. Writes nothing.
7. `S/Manifest.kt`: `val autoDownload: Boolean = false` last in `PlayerSettings`. `S/ApiJson.kt` `settings`: `autoDownload = j.optBoolean("autoDownload", false)`. `S/StoredJson.kt`: encode and decode it the same way as `skipQuiet`.
8. `S/ApiPaths.kt`: `fun queuePeekUrl(ended: String): String`, `"$base/api/v1/queue/next?ended=${segment(ended)}"`.
9. `S/BridgeProtocol.kt`: `data class AutoDownload(val on: Boolean) : BridgeMessage`; `"autodl"` parsed exactly as `"quiet"`.
10. `S/Offline.kt`: add the section in part 4.
11. `A/Api.kt`: `fun queuePeek(token: String, ended: String): String?`, `val bytes = get(paths.queuePeekUrl(ended), token, 64 * 1024)`; empty bytes (204) is null, otherwise `ApiJson.nextBookId(String(bytes))`. A 404 or 405 from an older server reaches the caller as `ApiException`.
12. `A/Offline.kt`, in this order:
    - **Helpers.** Prefs keys `autoBooks` (StringSet; always write a new set), `autoWant` (String), `autoNoRoom` (Boolean). `fun isAuto(book)`; `fun unmetered() = Requirements(Requirements.NETWORK_UNMETERED).checkRequirements(app)`; `fun autoNoRoomNoted()`; `fun autoWant(book)`; `fun autoForget()` (removes `autoWant` only).
    - **Split `add`.** Move lines 309-334 into `private fun enqueue(book, conn, api, manifest, text, plan: Plan.Ok, auto: Boolean)`. When `auto`: add the mark before `store.put`, remove `autoNoRoom`; in the `main.post`, `requests.forEach { manager().addDownload(it, AUTO_HOLD) }` then `releaseHeld(requests.map { it.id })`. When not `auto`: remove any mark, read `val held = autoRequestIds()` before the `main.post`; in it `held.forEach { manager().setStopReason(it, AUTO_HOLD) }`, then the existing resume and `sendAddDownload` lines.
    - **`add`, the early-return branch (:292).** If `isAuto(book)` and the row is not `READY`: drop the mark, read its ids with `downloadsOf`, `main.post { ids.forEach { manager().setStopReason(it, Download.STOP_REASON_NONE) }; manager().resumeDownloads(); DownloadService.sendResumeDownloads(app, TsDownloadService::class.java, true) }`. If `isAuto(book)` and `READY`: drop the mark only. The existing lines stay after this.
    - **`private fun autoRequestIds(): List<String>`.** The `downloadsOf` request ids of `autoUnfinished(store.all(), marks)`.
    - **`private fun releaseHeld(ids: List<String>)`, main thread.** Return if `ids` is empty or `!unmetered()`. `try { DownloadService.sendResumeDownloads(app, TsDownloadService::class.java, true) } catch (e: IllegalStateException) { Log.i(TAG, "next book: start refused, held"); return }`. Then `ids.forEach { manager().setStopReason(it, Download.STOP_REASON_NONE) }`. The start comes first, so a refusal changes nothing.
    - **`fun autoSync()`, worker thread.** Prune marks with no row. If `!loadSettings().autoDownload`: `autoUnfinished` rows go through `remove`, clear `autoWant` and `autoNoRoom`, return. If nothing is unfinished, return. `val ids = autoRequestIds()`, `val wifi = unmetered()`, `main.post { if (wifi) releaseHeld(ids) else ids.forEach { manager().setStopReason(it, AUTO_HOLD) } }`.
    - **`fun autoFetch(ended: String)`, worker thread.** Return unless `tokens.current()` exists, the setting is on, `unmetered()`, and `autoUnfinished` is empty. Return if `!ServerGate.ok(conn.origin)`. `queuePeek`; on `ApiException` or `IOException` log the class name and return, keeping `autoWant`. Then remove `autoWant`. Null: return. `store.get(next) != null`: return. `fetchManifestJson`, `parseManifest`; return if `manifest.id != next`. `when (autoDownloadPlan(manifest, api.paths, freeBytes(), autoBytes(store.all(), marks)))`: `Go` calls `enqueue(next, conn, api, manifest, text, it.plan, auto = true)`; `NoRoom` sets `autoNoRoom`; `Skip` logs "next book: skipped". Never `refuse()`, no toast. Catch every exception as `add` does.
    - **`pollTick`, at the top.** `val autoRunning = running.filter { isAuto(it.request.id.substringBefore('|')) && it.state != STATE_COMPLETED && it.stopReason == STOP_REASON_NONE }`. If not empty and `!unmetered()`, `setStopReason(id, AUTO_HOLD)` for each. Add a counter; every 30th tick, if marks exist and `freeBytes() < SPARE_BYTES`, `safeIo` removes the `autoUnfinished` rows.
    - **`onForeground`.** Inside `safeIo`, call `autoSync()` before the existing `main.post` resume, so a hold is applied before a resume. Before `dailyCheck()`: `prefs.getString("autoWant", "")` not empty calls `autoFetch(it)`.
    - **`retryText`, first line.** `if (isAuto(book) && !unmetered()) return`.
    - **`remove`.** In its `safeIo`: drop the mark, remove `autoNoRoom`.
    - **`fun setAutoDownload(on: Boolean)`.** `saveSettings(loadSettings().copy(autoDownload = on))`; `if (!on) safeIo { autoSync() }`.
    - **`wipe` and `finishWipe`.** Both remove `autoBooks`, `autoWant`, `autoNoRoom`.
13. `A/AutoNext.kt` (new), main thread only:
    ```kotlin
    class AutoNext {
        private var lastTick = 0L; private var asked = ""; private var askedAt = 0L
        fun reset() { lastTick = 0L; asked = ""; askedAt = 0L; Offline.autoForget() }
        fun tick(book: String, positionMs: Long, durationMs: Long, playing: Boolean) { /* below */ }
    }
    ```
    `tick`: return if `!playing` or `book` is empty. `now = SystemClock.elapsedRealtime()`; return if `lastTick != 0L && now - lastTick < AUTO_TICK_MS`. Set `lastTick = now`. `Offline.io { Offline.autoSync() }`. `when (autoStep(Offline.loadSettings().autoDownload, nearEnd(positionMs, durationMs), Offline.unmetered(), if (asked == book) now - askedAt else null))`: `WANT` calls `Offline.autoWant(book)`; `ASK` sets `asked = book; askedAt = now` and `Offline.io { Offline.autoFetch(book) }`; `NOTHING` does nothing.
14. `A/PlaybackService.kt`: `val auto = AutoNext()` beside `next` (:185). In `onBookStarted` call `auto.reset()`. In `onPositionTick`, inside the `isNotEmpty` block: `auto.tick(book, exo.currentPosition, exo.duration, exo.isPlaying)`. A book is one window, so these are book milliseconds (`BookMedia.kt:16`); `TIME_UNSET` is negative, so `nearEnd` is false.
15. `A/Bridge.kt` (:197): `is BridgeMessage.AutoDownload -> Offline.setAutoDownload(msg.on)`. Leave the `add` gate at :205 as it is.
16. `android/app/src/main/res/values/strings.xml`: `downloads_auto_row` "Downloaded on its own"; `downloads_auto_waiting` "Waiting for Wi-Fi"; `downloads_auto_no_room` "Your next book wasn't downloaded because there isn't enough room. Remove a book to make room."
17. `A/StartPages.kt`: `stateWords`: if `Offline.isAuto(b.bookId)`, `READY` gives `downloads_auto_row`, and `QUEUED` or `PAUSED` with `!Offline.unmetered()` gives `downloads_auto_waiting`. `downloads()`: when `Offline.autoNoRoomNoted()`, put `<p class="start__note" role="status">` with the escaped string in front of the page's `note` value.
18. `A/TsDownloadService.kt` class comment, and the comment at `AndroidManifest.xml:80`: started by the visible activity, or by the player while it is playing, for the next book (decision 0035). `getScheduler` stays null.
19. Docs:
    - `docs/decisions/0035-next-book-download.md` (new): D1 to D12, the evidence, the limits.
    - `docs/api.md` after :202 and `docs/architecture.md:314`: the GET route. `docs/api.md`: `autoDownload` in settings.
    - `docs/plans/android-a6-a10.md:431`: "built without `androidx.work`, see 0035". `:177`, invariant 7: add the exception. `docs/plans/next-stage.md:54`: remove "and remove finished books".
    - `docs/copy-additions.md`: the five new strings. `docs/manual-checks.md`: E1 to E7. `docs/roadmap.md`: status.
    - `AGENTS.md` and `NOTICES.md`: no change.

## 4. Data shapes (`S/Offline.kt`)
```kotlin
const val AUTO_NEAR_END_MS = 2L * 60 * 60 * 1000
const val AUTO_SPARE_BYTES = 2L * 1024 * 1024 * 1024
const val AUTO_TOTAL_BYTES = 5L * 1024 * 1024 * 1024
const val AUTO_TICK_MS = 60_000L
const val AUTO_ASK_MS = 15 * 60_000L
const val AUTO_HOLD = 1 // the Media3 stop reason of an automatic download not allowed to run

sealed interface AutoPlan {
    data class Go(val plan: Plan.Ok) : AutoPlan
    data object NoRoom : AutoPlan // told on the Downloads page
    data object Skip : AutoPlan   // not allowed, nothing to play, or larger than the whole quota: silent
}
enum class AutoStep { NOTHING, WANT, ASK }

fun nearEnd(positionMs: Long, durationMs: Long): Boolean =
    durationMs > 0 && positionMs >= 0 && durationMs - positionMs <= AUTO_NEAR_END_MS
fun autoStep(enabled: Boolean, near: Boolean, unmetered: Boolean, askedAgoMs: Long?): AutoStep = when {
    !enabled || !near -> AutoStep.NOTHING
    !unmetered -> AutoStep.WANT
    askedAgoMs == null || askedAgoMs >= AUTO_ASK_MS -> AutoStep.ASK
    else -> AutoStep.NOTHING
}
fun autoBytes(rows: List<OfflineBook>, marks: Set<String>): Long =
    rows.filter { it.bookId in marks }.sumOf { it.sizeBytes }
fun autoUnfinished(rows: List<OfflineBook>, marks: Set<String>): List<OfflineBook> = rows.filter {
    it.bookId in marks && (it.state == OfflineState.QUEUED || it.state == OfflineState.DOWNLOADING || it.state == OfflineState.PAUSED)
}
fun autoDownloadPlan(m: BookManifest, paths: ApiPaths, freeBytes: Long, keptAutoBytes: Long): AutoPlan =
    when (val p = downloadPlan(m, paths, freeBytes)) {
        Plan.NotAllowed, Plan.NothingToPlay -> AutoPlan.Skip
        Plan.NoRoom -> if (autoTotalOf(m) > AUTO_TOTAL_BYTES) AutoPlan.Skip else AutoPlan.NoRoom
        is Plan.Ok -> when {
            p.total > AUTO_TOTAL_BYTES -> AutoPlan.Skip
            keptAutoBytes + p.total > AUTO_TOTAL_BYTES -> AutoPlan.NoRoom
            freeBytes - p.total < AUTO_SPARE_BYTES -> AutoPlan.NoRoom
            else -> AutoPlan.Go(p)
        }
    }
private fun autoTotalOf(m: BookManifest) = m.files.filter { it.playable }.sumOf { it.size }
```
HTTP: `GET /api/v1/queue/next?ended=<id>` gives 200 `{"book":BookCard,"source":"queue"|"series"}`, 204 none, 400 field `ended`. A FAILED automatic row is not retried and still counts until removed. No database migration on either side (settings are key-value rows; confirm in `users/settings.go`).

## 5. Acceptance tests and gates
**Go** (new `internal/web/api_queue_peek_test.go`, names prefixed `TestL8`; add to `internal/users/settings_test.go`; add to `internal/web/api_settings_test.go`):
- `autoDownload` off by default, PATCH round trip, a bad value is 400 with field `autoDownload`.
- Form: `auto_download=1` saves; a form without the key leaves it on.
- Settings page shows `st-auto` only with `InApp`.
- Peek: returns the head; `GET /queue` is identical before and after; skips `ended`; a hidden, refused, missing or text head is skipped and kept; falls to series with `source:"series"`; 204 with the series setting off, an empty queue, or a finished next book; 65-character `ended` is 400.
- A member without a grant never sees a hidden book.
- `routes_matrix_api_test.go`: add a row beside `:97`, `api(sweepGet("GET /api/v1/queue/next", sweepStatic("/api/v1/queue/next"), sweepSame(http.StatusNoContent)))`. Keep whatever status the fixture truly gives; report any change.

**Shared** (`OfflineTest.kt`, `BridgeProtocolTest.kt`, `ApiJsonTest.kt`, `StoredJsonTest.kt`):
- `nearEnd` at exactly 2 h, 2 h + 1 ms, an unset (negative) duration, a book under 2 h.
- `autoStep`, all branches including `AUTO_ASK_MS` exactly.
- `autoDownloadPlan`: not allowed is `Skip`; no playable file is `Skip`; book over 5 GiB is `Skip`, with room and without; kept + book at exactly 5 GiB is `Go`, one byte over is `NoRoom`; free after the book at exactly 2 GiB is `Go`, one byte under is `NoRoom`; free below `SPARE_BYTES` is `NoRoom`.
- `autoBytes` and `autoUnfinished`: a stale mark, every state.
- `autodl 0|1` parses; `autodl`, `autodl 2` are null. `queuePeekUrl` encodes. Old kept settings and an old server's answer decode to false.

**Gates** (the builder runs these; no Gradle while a CI run is using this machine): `make generate`, `make check`, `make build`; `cd android && ./gradlew :shared:jvmTest`; `android/tools/check-android.sh`; `make apk-debug`; the JS budget test (`native-feel.js` grows by about two lines; report an overrun, never squeeze). `android/app` has no unit tests; its logic is proved by E1 to E7.

**Emulator** (orchestrator, under `flock -w 3600 /home/rubirosa/Projects/Tendshelf/.local/android-emulator.lock`, AVD with `-gpu host`). Use a real start, never a `DEBUG` op: a debug op holds the process foreground. Book A has under 2 h left, book B is next in the queue:
- E1 Switch off, playing on Wi-Fi: no peek in the server log, no row.
- E2 Switch on, app in the background over 60 s, screen off, playing on Wi-Fi: `dumpsys activity services app.tendshelf.android` shows `TsDownloadService` with `isForeground=true`, no `Background started FGS: Disallowed` in logcat, and B reaches `ready` after A's pause. **If this is refused, report it**; the honest result then narrows to "app open" and D6 and the switch's words change.
- E3 `adb shell cmd netpolicy set metered-network "AndroidWifi" true` during E2's download: held within about 2 s (`bytesDownloaded` stops, server log quiet); the row says "Waiting for Wi-Fi". Back to false with the book playing: goes on by range within about 60 s.
- E4 Near the end on metered: no peek; `autoWant` set; unmetered, then open the app: B starts.
- E5 Press Download on a waiting automatic book on metered: it runs. Press on another book during an automatic download: that book finishes first.
- E6 Switch off during a download: row and partial bytes gone, a finished automatic book stays. Quota: a server book over the room gives the Downloads note and no bytes. A book with downloads refused for this person: no row, no toast.
- E7 Sign out: every book, mark and `autoWant` gone. `POST /queue/next` at the end of A still pops once, B plays from the phone with the network off. Later (decision 0035, "Playing the next book with no connection"): with no network at all at the end of A, B still starts from the phone (the remembered `autoNext` pair), and `DELETE /queue/{B}` follows when a connection returns.

## 6. Out of scope
- Removing finished books (D5; separately approved package).
- A mobile-data option, a quota setting, more than one book ahead.
- WorkManager, a scheduler, boot receivers, `RUN_USER_INITIATED_JOBS`, battery-exemption prompts (D8).
- Starting with the app closed and nothing playing (D6), and a per-phone switch (D2).
- Translations (none exist on Android; web copy is English), iOS, the web and desktop players, a native settings screen.
- A precise quota for browser-friendly renditions: `sizeBytes` is the manifest's size (named limit).
- A running book larger than the free space after other apps fill the phone: the 30 s room check removes it; there is no reservation.
