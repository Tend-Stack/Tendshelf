# iPhone app, phase I3: downloads and offline (plan, 2026-11-19)

Decision [0048](../decisions/0048-iphone-app.md) (D2 shared logic, D4 bridge, D10 bundled pages, D12 window rules), parents
[0012](../decisions/0012-offline-pocket.md), [0019](../decisions/0019-offline-reading.md), [0035](../decisions/0035-next-book-download.md),
[0038](../decisions/0038-podcasts.md); phase plan [iphone-app.md](iphone-app.md); structure of [iphone-audio.md](iphone-audio.md).
Four builder packages, about 250 tool calls each, in order: **I3a** shared rules and the Downloads page renderer (Linux, then
`make ios-shared`); **I3b** download engine, store, bridge words, bundled Downloads and offline pages, sign-out (Go + Mac);
**I3c** offline playback, next book on its own, podcast auto-download, daily check (Go + Mac); **I3d** stored text and the
offline reader (shared + Go/JS + Mac). Between I3b and I3c a downloaded book still streams; the app is unreleased, so that
window is accepted.

**Reused**: `OfflineStore`/`OfflineBook`/`OfflineState`, `downloadPlan`, `episodePlan`, `fileComplete`, `episodeFileComplete`,
`episodeSize`, `downloadPercent`, `checkVerdict`, the `AUTO_*` rules, `autoDownloadPlan`, `autoEpisodePlan`, `autoEpisodeWanted`,
`autoEpisodeEvictions`, `offlineNextFor`, `queueDeleteDone`, `encodeKeptSettings`, `encodeDl`, `encodeRoom`, the `dl`/`autodl`
words, `offlineRoute`, `TextManifest`, `TextIndex`, `Nav.Downloads/PlayBook/RemoveBook/AutoShow/TogglePlay`, `offline.html`
and its strings, `native-downloads.js`, `downloads-all.js`, `PlayerApi`, `Words`, `BundledPages`, `SignOut.beforeWipe`.
**Moved to shared** (I3a; Android calls them, same output): the Downloads page renderer (`StartPages.kt:116-234`), the book
state fold (`Offline.kt:196-214`), `DlView`. **Extended**: `decide` (I3d), `BridgeProtocol` (I3d), `NativeScripts`, `navFor`,
`settings.templ`, `noOfflineScript`, `reader-offline.js`, `offline.js`, `test-server.sh`. **New**: the Swift files below.
**Replaced**: `ConnectFlow.loadFailed`'s start page for a connected person (the offline page instead).

## What of OfflineRoutes maps to iOS (decision)
- Android answers stored text **at the server's own address** from `shouldInterceptRequest` while `/healthz` fails. iOS cannot:
  `setURLSchemeHandler` throws for `http`/`https`, WKWebView has no request interception, its service workers run only for
  app-bound domains (fixed in `Info.plist`, so never a typed server), and its HTTP cache is not a store.
- So the iPhone answers the same `offlineRoute` table at **its own origin `tsread://app`** (a `WKURLSchemeHandler`, I3d): the
  stored reader shell, parts, pictures and hashed static files keep their paths. It is a separate origin from the server and from
  `tsbundle://app`: no `window.tsNative`, no bundled commands. Its `localStorage` is not the server's, so a reading place made
  there is kept natively and handed to the server's own page over the bridge (`rd`), whose `offline.js` sends it as today.
- **Android-only, with reasons**: answering at the server's address (no API); the new-episode notification (the iPhone app posts
  no notifications, 0048); the 6-hour WorkManager job (iOS: `BGAppRefreshTask`, timed by the system); the `PAUSED` state (it comes
  from Android's data-sync time limit; background `URLSession` has none, so the iPhone never sets it); skip silence and volume
  keys (unchanged from I2).

## I3 invariants (all packages)
1. The device token is only in the Keychain and in `Authorization` headers of native requests to `/api/v1/` of the pinned
   origin; never in a URL, a file name, a row, `taskDescription`, `UserDefaults` or a log. Resume data (it embeds the request) is
   kept in memory only.
2. Downloads live under `Application Support/offline/` (excluded from backup, `completeUntilFirstUserAuthentication`); rows in
   `offline/offline.sqlite`. Sign-out, leaving a server and another person's sign-in delete the folder, every row, every
   `offline.*` default and every background task, durably (a wipe interrupted by a kill finishes at launch).
3. A row is `READY` only when every playable file arrived whole (`fileComplete` / `episodeFileComplete`); a redirect, a non-2xx
   answer or a short file is never stored as whole.
4. An automatic download never moves a byte on an expensive or constrained network (`allowsExpensiveNetworkAccess = false`,
   `allowsConstrainedNetworkAccess = false` on its request) and stays inside `AUTO_TOTAL_BYTES`; a rule never removes a
   person's own download.
5. A downloaded book or episode starts from the phone with no request (stored answer, kept settings, stored cover and chapters).
6. Page to app stays I2's gate (main frame, `sameSource`, connected, `TokenBucket(30)`, `parseBridge`). `tsread://app` and
   `tsbundle://app` pages never get `window.tsNative`; bundled commands are obeyed only from bundled pages.
7. Browsers and the desktop shell get byte-identical pages; Android behaves as before (`:shared:jvmTest`, `check-android.sh`,
   `make apk-debug`).
8. The app requests nothing from anyone but the typed origin.

---
## I3a: shared rules and the Downloads page renderer (builder; Linux gates, then `make ios-shared`)
Decisions:
- The Downloads page is drawn by shared code: both apps would otherwise write the same escaped HTML twice (0048 D2 rule).
- Words go in raw from `strings.xml` (`%1$d`, `%%`); `fillWord` formats them, so Android's `getString(id)` (unformatted) and the
  iPhone's `Words` give the same text. Output must equal today's Android output byte for byte.
- iOS-only rules that need no platform API (retry, file names, the state fold) are shared, tested on the JVM and the simulator.

Steps (`android/shared/src/commonMain/kotlin/app/tendshelf/shared/`, Android in `android/app/src/main/kotlin/app/tendshelf/android/`):
1. `Offline.kt` add: `data class DlView(val state: String, val pct: Int, val known: Boolean = true)` (moved from
   `Offline.kt:244`; Android imports it); `enum class FileStep { QUEUED, RUNNING, DONE, BROKEN, FAILED }` (DONE = arrived and
   whole, BROKEN = arrived and not whole); `fun bookStateOf(steps: List<FileStep>, expected: Int, previous: OfflineState,
   paused: Boolean): OfflineState?` = `Offline.kt:198-209`: empty -> null; any FAILED -> FAILED; all DONE or BROKEN and
   `size >= expected` -> FAILED if any BROKEN, else STALE when `previous == STALE`, else READY; `paused` -> PAUSED; any RUNNING ->
   DOWNLOADING; else QUEUED. `fun dlView(row: OfflineBook?, p: DownloadPercent): DlView` = `Offline.kt:250-259` without the maps
   (null -> `none`,0; READY/STALE -> lowercase,100; else lowercase, `p.pct`, `p.known`).
2. `Offline.kt` add: `const val DOWNLOAD_MAX_TRIES = 5`; `fun downloadRetryDelayMs(status: Int, failuresBefore: Int,
   retryAfterS: Int): Long?`: null when `failuresBefore >= 5` or status not in {0, 408, 429, 500..599}; `retryAfterS in 1..3600`
   -> that times 1000; else `minOf(5_000L shl failuresBefore, 300_000L)`. `fun audioFileExtension(mime: String): String` on the
   part before `;`, lowercased: `audio/mpeg`,`audio/mp3` -> `mp3`; `audio/mp4`,`audio/x-m4a`,`audio/m4a`,`audio/x-m4b` -> `m4a`;
   `audio/aac`,`audio/aacp` -> `aac`; `audio/wav`,`audio/x-wav`,`audio/wave` -> `wav`; `audio/flac` -> `flac`; `audio/ogg` ->
   `ogg`; `audio/opus` -> `opus`; else `bin`. `fun offlineFileName(cacheKey: String, mime: String): String` =
   `cacheKey.replace(':', '.') + "." + audioFileExtension(mime)`; null-safe for a key outside `validId` plus `:c`: returns ""
   (callers refuse "").
3. New `DownloadsPage.kt`: `data class DownloadsWords` with one `String` field per key, in this order: `title`=downloads_title,
   `lead`, `empty`, `emptyHow`, `active`, `ready`, `percent`, `working`, `waiting`, `pausedRow`, `failedRow`, `staleRow`, `play`,
   `read`, `pause`, `remove`, `library`, `autoRow`, `autoWaiting`, `autoNoRoom`, `podcasts`, `podcastsLead`, `podcastOnRow`,
   `podcastOffRow`, `turnOn`, `turnOff` (all `downloads_*`/`downloads_podcast_*`), `offlineTitle`, `offlineBody`, `offlineRetry`.
   `data class DownloadRow(val book: OfflineBook, val view: DlView, val hasCover: Boolean, val readable: Boolean, val hasAudio:
   Boolean, val auto: Boolean)`; `data class AutoShowRow(val id: String, val title: String, val keepLatest: Int, val on: Boolean)`;
   `data class DownloadsInput(val rows: List<DownloadRow>, val shows: List<AutoShowRow>, val playingId: String, val playing:
   Boolean, val unmetered: Boolean, val autoNoRoom: Boolean, val offline: Boolean, val offlineAddress: String, val homeUrl:
   String, val readBase: String)`. `fun downloadsPage(input: DownloadsInput, w: DownloadsWords): Map<String, String>` with keys
   TITLE, LEAD, NOTE, LIST, LIBRARY, HOMEURL, REFRESH = `StartPages.kt:116-213` exactly (sections, shows, state words, auto words;
   Read link `"<readBase>/books/<id>/read"` only when `readable && readBase != ""`; Play only when READY/STALE and `hasAudio`).
   `fun fillWord(template: String, vararg args: String): String` (`%N$s`, `%N$d` -> args[N-1], then `%%` -> `%`);
   `fun htmlEscape(s: String): String` = `StartPages.kt:225-234`.
4. Android `StartPages.kt`: `downloads(offline)` builds `DownloadsInput` (rows from `Offline.store.all()` with
   `Offline.view`, `coverBytes != null`, `readable`, `hasAudio`, `isAuto`; shows from `EpisodeAuto.knownShows()`/`enabled()`
   skipping ids failing `validId`; `homeUrl.ifEmpty { startUrl }`; `readBase = homeOrigin`) and `DownloadsWords` from
   `context.getString(R.string.x)`, then returns `downloadsPage(...)`; delete `shows`, `section`, `stateWords`, `autoWords`,
   `plainWords`; `esc` calls `htmlEscape`. `Offline.kt` `recompute` maps Media3 states to `FileStep` (STATE_FAILED -> FAILED;
   COMPLETED -> DONE if `whole(it)` else BROKEN; DOWNLOADING -> RUNNING; others QUEUED) and calls `bookStateOf(steps,
   expectedFiles(book), row.state, m.getDownloadsPaused())`; `view` returns `dlView(row, p)` after its map bookkeeping.
5. `android/tools/test-server.sh`: `bind=${TS_TEST_BIND:-127.0.0.1}`; `-addr "$bind:$port"`; the health probe stays on
   `127.0.0.1` unless bind is not `0.0.0.0`/`127.0.0.1`, then on `$bind`. Header comment: the Mac's simulator reaches it with
   `TS_TEST_BIND=$(tailscale ip -4)`.
6. `commonTest`: `DownloadsPageTest` (a golden `LIST`/`NOTE`/`REFRESH` for: empty; one ready book with cover, playing; one
   downloading at 42 % known and one unknown; a failed, a stale and a queued auto row with `unmetered` false; two shows on/off;
   offline true with an address containing `"<&`; `readBase` empty hides Read; every value escaped), the goldens written from
   today's `StartPages.kt` before the move; `fillWord` (`%1$d%%`, `%2$s`, no args); `OfflineTest` additions: `bookStateOf`
   table (every branch, STALE kept, fewer steps than expected stays QUEUED), `dlView`, `downloadRetryDelayMs` (0/503/429/408
   retry; 401/403/404/302 null; 5th failure null; Retry-After 7 -> 7000, 7200 -> backoff), `audioFileExtension`,
   `offlineFileName` (`ab:c` + `audio/mpeg` -> `ab.c.mp3`; `../x` -> "").
Gates: `make check`, `android/tools/check-android.sh`, `make apk-debug`, `make ios-shared`. Commit
`refactor(shared): Downloads page and download rules shared for the iPhone (decision 0048, I3a)`.
Out of scope: any Swift; any Go; any page script.

---
## I3b: download engine, store, bridge words, Downloads and offline pages, sign-out (builder; Go on Linux, Swift on the Mac)
Decisions:
- **Background `URLSession`** (`app.tendshelf.ios.downloads`, `sessionSendsLaunchEvents`, not discretionary, no cookies:
  `httpShouldSetCookies = false`, `httpCookieStorage = nil`, `urlCache = nil`): downloads go on with the app suspended or killed
  by the system. The bearer is a request header (`setValue(_:forHTTPHeaderField: "Authorization")`); the OS keeps the request
  in its own daemon, which is outside the app's storage rule and is cleared by cancelling the tasks at sign-out.
- Every file's task is created at add time (no app-side queue): tasks created while the app is suspended are delayed by the
  system, so a book must not wait for the app to wake between files.
- Redirects are refused (`willPerformHTTPRedirection` -> nil); the 3xx then fails the file.
- The store is its own file `offline/offline.sqlite`, not the outbox: one folder removal is the whole wipe, and the outbox must
  flush before it clears. Swift over `SQLite3`, an `NSObject` conforming to the shared `OfflineStore`, main actor, as I2c.
- Room: `volumeAvailableCapacityForImportantUsage` of `Application Support` for a person's download and `dl room`
  (`...ForOpportunisticUsage` for automatic ones, I3c).
- A refusal ("Not enough room…") is an alert through `ShellHost.notice` with the Android words (no toast on iOS).

Steps, Go (commit 1, `feat(web): Downloads in the iPhone app's window (decision 0048, I3b)`):
1. `internal/web/components/shell.go` `NativeScripts`: ios -> `native-audio.js`, `native-downloads.js`, `native-feel.js`
   (Android's relative order); comment updated. `components/nav.templ` `navFor`: both `"android"` tests become `IsAppShell(shell)`.
   `static/js/native-downloads.js:11` -> `["android", "ios"].includes(document.documentElement.dataset.shell)`; header says
   "a phone app's window". `make generate`.
2. New `internal/web/shell_ios_downloads_test.go`, names `TestIOSDownloads*`: `Scripts` (ios home has `native-audio.`,
   `native-downloads.`, `native-feel.` in that order and no `native-speech`; android unchanged; desktop and browser none);
   `NavItem` (ios and android: the Downloads link has the NoBoost attribute; desktop and browser do not); `BookButton`
   (ios book page has `data-ts-download`); update `TestIOSAudioScripts`/`TestIOSAudioNativeScriptsList` to the new list.
   `make check`, `make build`; on `main` and the branch `TENDSHELF_TEST_BROWSER=1 make visual` into `.local/tests/i3b/{before,after}`,
   `capturediff` all identical (grep SKIP); remove `.local/tests/i3b`.
Steps, Swift (commit 2, `feat(ios): downloads and the Downloads page (decision 0048, I3b)`; all under `ios/Tendshelf/`):
3. New `AppDelegate.swift`: `final class AppDelegate: NSObject, UIApplicationDelegate`;
   `application(_:handleEventsForBackgroundURLSession:completionHandler:)` stores the handler in
   `BackgroundTransport.pendingCompletion` when the id is `BackgroundTransport.identifier`. `TendshelfApp.swift`:
   `@UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate`.
4. New `OfflinePaths.swift`: `struct OfflinePaths { let root: URL }` (default `Application Support/offline`), `audio(bookId)`,
   `audioFile(bookId, name)`, `cover(id)`, `chapters(id)`, `incoming`, `text(bookId)`, `staticDir`, `database`;
   `func prepare() throws` creates the root with `.completeUntilFirstUserAuthentication` and sets `isExcludedFromBackup` on it.
   Every id passes `validId`, every name is non-empty with no `/`, else nil.
5. New `SqliteOfflineStore.swift`: `final class SqliteOfflineStore: NSObject, OfflineStore`, `PRAGMA foreign_keys=ON`, WAL,
   `user_version` 1, prepared statements only:
   `offline_books (book_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, title TEXT NOT NULL, author TEXT NOT NULL, duration_ms
   INTEGER NOT NULL, size_bytes INTEGER NOT NULL, manifest TEXT NOT NULL, state TEXT NOT NULL, added_at INTEGER NOT NULL,
   checked_at INTEGER NOT NULL DEFAULT 0, place_ms INTEGER NOT NULL DEFAULT 0, place_at INTEGER NOT NULL DEFAULT 0, text_state
   TEXT NOT NULL DEFAULT 'none', text_revision TEXT NOT NULL DEFAULT '', edition_id TEXT NOT NULL DEFAULT '', kind TEXT NOT
   NULL DEFAULT 'book', show_id TEXT NOT NULL DEFAULT '', published_at INTEGER NOT NULL DEFAULT 0, played INTEGER NOT NULL
   DEFAULT 0)`; `offline_files (book_id TEXT NOT NULL REFERENCES offline_books(book_id) ON DELETE CASCADE, cache_key TEXT NOT
   NULL, url TEXT NOT NULL, planned INTEGER NOT NULL, mime TEXT NOT NULL, auto INTEGER NOT NULL, step TEXT NOT NULL, failures
   INTEGER NOT NULL DEFAULT 0, length INTEGER NOT NULL DEFAULT 0, bytes INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL DEFAULT
   '', PRIMARY KEY (book_id, cache_key))`; `reading_out (seq INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL, book_id
   TEXT NOT NULL, form TEXT NOT NULL, at INTEGER NOT NULL)` (used from I3d). State stored as `OfflineState.name`, step as
   `FileStep.name`. Extras: `checkedAt`, `touchChecked`, `setManifest(id, json, at)`, `text`, `setText`, `withText(state)`,
   `struct OfflineFile` + `putFiles`, `files(bookId)`, `allFiles()`, `setStep(bookId, key, step, failures)`,
   `setArrived(bookId, key, length, bytes, name)`, `setBytes`. `put` replaces the row and deletes its old file rows.
6. New `DownloadTransport.swift`: `protocol DownloadTransport: AnyObject { var events: DownloadEvents? { get set };
   func start(key: String, request: URLRequest, resumeData: Data?); func cancel(key: String) async; func cancelAll() async;
   func runningKeys() async -> Set<String> }`; `@MainActor protocol DownloadEvents: AnyObject { progressed(key:written:expected:);
   arrived(key: String, status: Int, length: Int64, mime: String?, staged: URL, bytes: Int64); failed(key: String, status: Int,
   retryAfterS: Int, resumeData: Data?) }`. `final class BackgroundTransport`: `static let identifier =
   "app.tendshelf.ios.downloads"`, `static var pendingCompletion: (() -> Void)?`, serial delegate queue; `taskDescription = key`
   (`<bookId>|<cacheKey>`). `didFinishDownloadingTo`: synchronously move to `incoming/<UUID>`; a non-2xx response deletes it and
   reports `failed(status, Retry-After)`; else `arrived`. `didCompleteWithError` with an error: `failed(0, 0,
   NSURLSessionDownloadTaskResumeData)` (cancelled-by-us errors are dropped). `didWriteData`: at most 4 events a second per key.
   `urlSessionDidFinishEvents` calls and clears `pendingCompletion` on the main queue.
7. `PlayerApi.swift`: `manifestText(token:bookId:) -> String` and `episodeText(token:id:) -> String` (same URLs and limits);
   `manifest`/`episode` parse them; `chaptersText(token:path:) -> String?` (512 KiB); add all three to `PlayerAPI`.
8. New `Downloads.swift`, `@MainActor final class Downloads: DownloadEvents` (port of `Offline.kt` without Media3): init
   `(store: SqliteOfflineStore, paths: OfflinePaths, transport: DownloadTransport, api: (Origin) -> PlayerAPI, connection: () ->
   Connection?, free: (Bool) -> Int64, notice: (String) -> Void, now: () -> Int64)`. `add(_ id)` = `Offline.kt:351-397` (episode ->
   `addEpisode(id, auto: false)` = `:816-888` for a person's download); a NothingToPlay book -> notice `download_nothing` (text-only
   is I3d); enqueue writes cover (`api.cover`), episode art and chapters file, `store.put` QUEUED, `putFiles` (url from the plan,
   `mime` from the manifest file or `doc.mime`, `planned` 0 for an episode), then one `URLRequest` per file: GET, bearer,
   `timeoutInterval` 60, `allowsExpensiveNetworkAccess`/`allowsConstrainedNetworkAccess` true. `remove(_ id, byPerson:)` =
   `:533-553` (cancel its keys, delete files, cover, chapters, row). `arrived`: wrong key or no row -> delete staged; compare
   with `fileComplete(planned, compat, length, bytes)` (episode: `episodeFileComplete(length, bytes, bytes)`); whole -> move to
   `audioFile(bookId, offlineFileName(cacheKey, mime ?? row mime))`, `setArrived`, step DONE; not whole -> BROKEN. `failed`:
   `downloadRetryDelayMs(status, failures, retryAfterS)`; resume data -> restart now with it (memory only); a delay -> step QUEUED,
   restart after it; null -> FAILED. After each event `recompute` = `bookStateOf(steps, playable count, row.state, false)`; READY
   episode -> `setSize(bytes)`; `onChange?(bookId)`. `view(id)` = `dlView(row, downloadPercent(...))` with the per-book floor.
   `reconcile()` (launch and foreground): `runningKeys()`; QUEUED/RUNNING files with no task restart (no resume data), a row with
   no file rows and an active state -> FAILED. `hasOtherPerson(userId)`, `playable(id)`, `hasAudio(id)`, `coverData(id)`,
   `localFile(bookId, cacheKey) -> URL?` (DONE and on disk). `wipe()` = `:950-995`: `UserDefaults` `offline.wipePending` true,
   `store.clear()`, `await transport.cancelAll()`, delete `paths.root`, remove every `offline.*` key, then false;
   `finishPendingWipe()` at init.
9. `Bridge.swift`: `Download` -> `downloads.add`/`remove`, then `pushDownload(id)`; `ask` -> `pushDownload`; `DownloadRoom` ->
   `BridgeProtocolKt.encodeRoom(freeBytes: free(true))`; `pushDownload(_ id)` sends `encodeDl(id, v.state, v.pct, v.known)`
   under I2's push guards; `downloads.onChange` pushes for that id at most once a second.
10. `BundledPages.swift`: paths `/downloads` and `/offline` render `offline.html` with `DownloadsPageKt.downloadsPage(input, words)`
   (`var downloadsInput: ((Bool) -> DownloadsInput)?`; words from `Words` keys of step I3a.3); `/covers/<id>` serves
   `coverData(id)` as `image/jpeg` (invalid id or none -> 404); `var offlineAddress = ""`; static URLs `downloadsUrl`, `offlineUrl`.
11. `ShellController.swift`: `Downloads` (built with `BackgroundTransport`) owned here; `NavDownloads` -> load `downloadsUrl`;
   `NavPlayBook` -> `playback.start(id, atMs: nil)`; `NavRemoveBook` -> `downloads.remove(id, byPerson: true)` then reload;
   `NavTogglePlay` -> play/pause, reload; `NavAutoShow` -> reload (I3c). `downloadsInput` from the store, `playback` snapshot,
   `readBase` = origin text when not offline, "" offline (I3d sets `tsread://app`). A main-frame failure on the origin, or a
   main-frame answer >= 500 on it (`decidePolicyFor navigationResponse`), while `flow.state == .connected` -> `offlineAddress =
   origin.text`, load `offlineUrl`; otherwise today's `flow.loadFailed`. Foreground -> `downloads.reconcile()`.
12. `ConnectFlow.swift` `SwapDone`: when not leaving and `downloads.hasOtherPerson(a.userId)` -> `downloads.wipe()` before saving
   (`ConnectFlow.kt:351`). `ShellController` `beforeWipe` also calls `downloads.wipe()` after `playback.signOut`.
13. `DebugDriver.swift`: `-TSTestDownload <id>` calls `downloads.add` after connect; the probe line adds `dl=<state>/<pct>`.
Tests (`TendshelfTests/`): `SqliteOfflineStoreTests` (put/get/all order, replace drops file rows, cascade, place, played, size,
clear, reopen). `OfflinePathsTests` (excluded from backup, protection, `../x` and `a/b` refused). `DownloadsTests` with a fake
`DownloadTransport` and fake `PlayerAPI`: a 2-file book makes 2 requests, each GET with `Authorization: Bearer`, no token in
`url`/`taskDescription`/any row/`UserDefaults`; not allowed, no room, nothing to play notice and add nothing; both arrive whole
-> READY, files named `<id>.mp3`; a short file -> FAILED; 503 then arrival -> READY after the delay (scale clock); 401 -> FAILED;
resume data used once; `remove` cancels and deletes; `reconcile` restarts a lost task; percent never goes back; an episode
with no length is whole by bytes and its size is set; `wipe` cancels all, deletes the folder and the `offline.*` keys, and a
pending wipe finishes at init. `BridgeDownloadsTests` (`dl ask x` gets a `dl` push; `dl room` gets `room`; `dl add` from a
sub-frame is ignored). `BundledPagesTests` additions (`/downloads` empty and with a row; `/covers/x` 404; `/covers/../a` 404).
`SignOutTests`: the wipe runs after the player stops and before the revoke.
Simulator check (never the operator's accounts): `TS_TEST_BIND=$(tailscale ip -4) android/tools/test-server.sh 8098
.local/tests/ios/i3b/<run>`; launch with `-TSTestAddress http://<ip>:8098 -TSTestLogin tester:<password> -TSTestDownload <Parts id>`.
Pass: `dl=ready/100` within 60 s; the server log has three `/api/v1/stream/` lines and no `token` in any URL; the Downloads page
lists the book with its cover; Home to background during a second download still finishes; stop the server: the next page load
shows the offline page with the list and Try again; start it: Try again reaches home; Sign out: the folder is gone (`xcrun simctl
get_app_container booted app.tendshelf.ios data`, then `ls Library/Application Support`). Stop the server by its pid, remove the
run folder. Gate: `make ios-sim`. Docs: `docs/ios.md` section "Downloads (I3)".
Out of scope: playing from the files (I3c), automatic downloads (I3c), text (I3d), a download notification, cellular settings.

---
## I3c: offline playback, next book on its own, podcast auto-download, daily check (builder; Go on Linux, Swift on the Mac)
Decisions:
- **A downloaded file plays from a file URL** (`AVURLAsset(url: fileURL, options: override MIME)`), not through `MediaLoader`:
  AVFoundation reads local files natively with exact seeks; the loader exists only for the bearer. Local first per file, so a book
  half downloaded plays its whole files locally and streams the rest.
- Person first is a priority hint (`task.priority` high for a person's, low for an automatic one): background tasks cannot be held
  and released as Media3 does. The Wi-Fi rule is the request's own network flags (invariant 4), so a "held" automatic download is
  simply one the system has not run yet; its row stays QUEUED with the "Waiting for Wi-Fi" words.
- Pressing Download on an automatic download cancels its tasks and starts them again as the person's (no resume data: the old
  request carries the Wi-Fi flags).
- Episode checks: on foreground (`syncIfDue`, `EPISODE_CHECK_MS`), while playing (`syncSoon`), and a `BGAppRefreshTask`
  `app.tendshelf.ios.episodes` asked for no earlier than 6 h (`UIBackgroundModes` `fetch`, `BGTaskSchedulerPermittedIdentifiers`).
  No announcement (Android-only).
- `unmetered` = `NWPathMonitor` path satisfied, not expensive, not constrained.
Steps, Go (commit 1, `feat(web): next-book switch in the iPhone app (decision 0048, I3c)`):
1. `pages/settings.templ`: `InApp` becomes `QuietSwitch` (skip silence; `routes_me.go:237` `shellFrom(r) == "android"`) and
   `AutoSwitch` (`isAppShell(shellFrom(r))`); the two rows render under their own field; `make generate`. `static/js/native-feel.js`
   header: on the iPhone `wake` and `autodl` are acted on. Rename the `TestIOSAudioSettings` expectation: ios has `auto_download`
   and no `skip_quiet`; android both; browser neither. `make check`, `make build`, visual identical as in I3b.
Steps, Swift (commit 2, `feat(ios): offline playback and downloads on their own (decision 0048, I3c)`):
2. New `OfflineSource.swift`: `@MainActor protocol OfflineSource { playable(_:) -> OfflineBook?; manifestJson(_:) -> String?;
   localFile(bookId:cacheKey:) -> URL?; coverData(_:) -> Data?; chaptersText(_:) -> String?; keptSettings() -> PlayerSettings;
   keep(_ s: PlayerSettings); notePlace(_ id: String, ms: Int64, at: Int64); markPlayed(_ id: String); var playingNow: String
   { get set } }`; `Downloads` conforms (`keptSettings` = `decodeKeptSettings(UserDefaults "offline.settingsJson")`).
3. `AudioPlayer.swift`: `offline: OfflineSource?`. `startBook`: `playable(id)` -> `startDownloaded` = `BookEngine.kt:195-213`
   (parse stored manifest with `parseManifest(body:paths:)`, a newer row place replaces `progress`, `keptSettings()`, stored
   cover, `pendingFor`), no request. Network path: `offline?.keep(settings)` after the settings call. `fileItem`: `localFile(bookId,
   cacheKey(f.id, f.needsCompat))` first. Episodes: `startDownloadedEpisode` = `BookEngine.kt:252-270`, chapters from
   `chaptersText` via `parseEpisodeChapters`; the file local first. `playingNow` set at begin, cleared at stop (`:302,343,387,522`).
4. `ProgressWriter.swift` `record`: `offline?.notePlace(id, pos, now)` when the row exists (`ProgressWriter.kt:157,178`).
   `Playback.swift`: an ended episode -> `markPlayed` (`PlaybackService.kt:237`).
5. `NextBook.swift`: the phone fallback = `NextBook.kt:84-95`: no answer from the server -> `downloads.offlineNext(ended)`,
   start it, `autoDrop(next)`. `Downloads`: `offlineNext`, `autoDrop`, `tidyQueue` (`Offline.kt:734-762`; keys `offline.autoNext`,
   `offline.autoDrop`); `PlayerAPI.queuePeek(token:ended:)` and `queueRemove(token:bookId:) -> Int` (status).
6. New `AutoNext.swift` = `AutoNext.kt` (monotonic clock `ProcessInfo.systemUptime`), ticked from `Playback` ticks. `Downloads`:
   `setAutoDownload(on)` (Bridge `AutoDownload`), marks `offline.autoBooks`, `autoWant`, `autoFetch(ended)`, `autoSync()` =
   `Offline.kt:654-800` with the iOS deltas: automatic requests carry the Wi-Fi flags and low priority; `autoSync` cancels and
   removes unfinished automatic rows when the switch is off; `autoNoRoom` notes; free bytes `ForOpportunisticUsage`.
7. New `EpisodeAuto.swift` = `EpisodeAuto.kt:57-253` without the notification, keys `offline.episodes.*` (`shows`, `marks`,
   `declined` max 300, `showsCache`, `primed`, `notified` max 600, `checkedAt`); `PlayerAPI.podcastShows`, `showEpisodes`
   (`ApiPaths.showsUrl`, `showEpisodesUrl`; parse with the shared `PodcastJson`). `Downloads.addEpisode(id, auto:, nextItem:)`,
   `tidyEpisodes`, `releaseShow`, `markPlayed`. `ShellController`: `NavDownloads` first `refreshShows()` (2 s cap) then loads;
   `NavAutoShow` -> `setShow(id, on)` then reload. Downloads page rows get `auto`, `unmetered`, `autoNoRoom`, shows.
8. `Info.plist`: `UIBackgroundModes` += `fetch`; `BGTaskSchedulerPermittedIdentifiers` = [`app.tendshelf.ios.episodes`].
   `AppDelegate` registers the task at launch; handler: `EpisodeAuto.sync()` under the task's expiration, reschedules.
9. `Downloads.dailyCheck()` = `Offline.kt:588-622` (books only; `checkVerdict`; 404 removes), on foreground after `reconcile`.
Tests: `OfflinePlaybackTests` (fake `OfflineSource` + `URLProtocol` stub that fails every request: a ready book reaches
`.playing` from a synthetic WAV file with zero requests; a newer row place wins; half-downloaded book: file 1 local, file 2 a
`tsmedia` handle); `AutoNextTests`, `EpisodeAutoTests` (fake API: keep latest N, declined never fetched, played removed, quota,
a person's never removed, Wi-Fi flags false on automatic requests, priority low), `NextBookTests` additions (offline fallback
starts the remembered ready book once; queue tidied later), `DailyCheckTests` (same/update/stale/404).
Simulator check: I3b's server; download Parts, stop the server, kill the app (`simctl terminate`), launch: the offline page lists
it; Play starts sound with the server still down; play 30 s, start the server: the place reaches it. Episodes: new opt-in
`internal/web/ios_offline_serve_test.go` `TestIOSOfflineServe` (skipped unless `TENDSHELF_IOS_OFFLINE_SERVE=<host:port>`), on
`newPodWorld` with a show whose fake publisher serves a 10 s silent-MP3 episode (add the case to `podFix` like `radioFix`'s
`/paced.mp3` if missing); prints origin, user, password, show and episode ids; every 10 s `audio_requests=<n>
header_names=<sorted, /api/v1/podcasts/audio/ only>`. Pass: Download the episode -> ready; Turn on the show on Downloads ->
its newest episodes download on the simulator's Wi-Fi; header names include `Authorization`, never `Cookie`. Gate `make ios-sim`.
Out of scope: text (I3d); notifications; per-show cellular choice; CarPlay's Downloaded node (I4).

---
## I3d: stored text and the offline reader (builder; shared + Go/JS on Linux, Swift on the Mac)
Decisions:
- `tsread://app` is the reader's offline origin (see "What of OfflineRoutes maps to iOS"); the stored shell, its CSP and its
  hashed files are served unchanged, so `'self'` means `tsread://app`.
- Text fetches use the WKWebView session cookie (`WKWebsiteDataStore.default().httpCookieStore`) for the origin's host only, plus
  `ts_shell=ios`, as Android's D1; read for the request, never kept or logged. Native never POSTs with the cookie.
- A reading place made at `tsread://app` is answered there (`applied: true`), kept in `reading_out`, and handed to the server's
  page by the bridge, which merges it into `ts.offline.reading` so `offline.js` sends it with that page's CSRF token.
Steps, shared + Go/JS (commit 1, `feat(web): reading places made offline in the iPhone app reach the server (decision 0048, I3d)`):
1. Shared `Navigation.kt`: `BundledSite.IOS_READER = BundledSite("tsread", "app")`; `Nav.Online(val url: String)`; `decide(...,
   reader: BundledSite? = null)`: a URL matching `reader` (same `bundledMatch`): not main frame or no origin -> Block;
   `offlineRoute(origin.text + path + (query?), origin)` is `Shell` -> Stay; else `Nav.Online(origin.text + path + query)`.
   Android passes nothing (unchanged). `OfflineRoutes.kt`: `fun textFetchList(m: TextManifest, origin: Origin): List<String>?`
   = `TextStore.kt:184-189` (null when any address is not its route or not this book's); Android's `fetch` calls it.
2. Shared `BridgeProtocol.kt`: `rd ask` -> `BridgeMessage.ReadingAsk`; `rd done <seq>` (`millis` digits) ->
   `BridgeMessage.ReadingDone(seq: Long)`; `data class ReadingItem(val seq: Long, val bookId: String, val form: String)`;
   `fun encodeReading(items: List<ReadingItem>): String` = `{"t":"rd","items":[{"seq":1,"bookId":"..","form":".."}]}` (`jsonString`,
   at most 100 items). Android `Bridge.kt`: both new messages do nothing. Tests in `BridgeProtocolTest`, a new `NavigationReaderTest`,
   `OfflineRoutesTest`.
3. `internal/web/server.go:609` `noOfflineScript`: `shell != "" && !isAppShell(shell)`. `static/js/reader-offline.js`: `export
   function adopt(items)` (skip ids failing `/^[A-Za-z0-9_-]{1,64}$/` and `seq <= localStorage["ts.offline.reading.seq"]`; per
   item in seq order: `form` = the newer by `client_updated_at` of the record's and `strip(item.form)` with `device_id` set to
   `localStorage["ts.deviceId"]` when present; `chars += chars_read`, `active += active_ms`, `sent = false`; write the queue,
   then the seq; return the highest seq seen, 0 for none). `native-downloads.js`: post `rd ask` at start and on `ts:native:ready`;
   on `t == "rd"`: import `#ts-offline` `data-ts-reader-offline-src`, `adopt`, post `rd done <seq>` when > 0, dispatch
   `ts:reading:adopted` on `document`. `offline.js`: on `ts:reading:adopted` -> `flush()`.
4. Go tests `internal/web/shell_ios_reading_test.go`, `TestIOSReading*`: ios and android pages load `offline.js`; desktop does
   not (unchanged); a stored-shell answer (`/offline/books/{id}/read` under ios) carries `data-shell="ios"` and its CSP.
   Visual identical as in I3b.
Steps, Swift (commit 2, `feat(ios): offline reading (decision 0048, I3d)`):
5. New `TextStore.swift` = `TextStore.kt` (paths `text/<bookId>/`, `static/`, `index.json` via shared `TextIndex`, SHA-256 names
   with CryptoKit, 32 MiB answer cap, 50 MiB spare, redirect = signin, ephemeral session with no cookie store and no redirects,
   the cookie header built per request). `Downloads`: `fetchText`, `retryText`, `addTextOnly`, `checkText`, `readable`, the
   `signin` retry on a server page load (`Offline.kt:457-523,625-639`); `remove` deletes the text; `dailyCheck` checks text.
6. New `OfflineReader.swift`, `WKURLSchemeHandler` for `tsread`: GET -> `offlineRoute` -> `TextStore.lookup` -> 200 with the
   stored `Content-Type`, stored CSP when present, `Cache-Control: no-store`; anything else 404. `POST /reading/<validId>` for a
   row with text `ready`, body (`httpBody` or the stream) at most 64 KiB -> `reading_out` insert for the current user ->
   200 `application/json` `{"applied":true,"reading":{"clientUpdatedAt":<client_updated_at or 0>}}`; else 404.
7. `Bridge.swift`: `ReadingAsk` -> up to 100 rows of the current user as `encodeReading`; `ReadingDone(seq)` deletes that
   user's rows with `seq <=`. `ShellController`: register the handler; `decide(..., reader: BundledSite.companion.IOS_READER)`;
   `NavOnline` -> `load(url)`; a main-frame failure whose route is `Shell` with stored text -> load `tsread://app` + path and query
   instead of the offline page; `readBase` = `tsread://app` on the offline page.
Tests: `TextStoreTests` (URLProtocol stub: cookie sent with `ts_shell=ios`, not to static files; a 302 -> signin; a foreign
address in the manifest -> failed, nothing fetched; shell stored last). `OfflineReaderTests` (real `WKWebView` on
`tsread://app/books/<id>/read` from a fixture store: the part is fetched from the handler, `window.tsNative` is undefined, a
`/static/x.js` not stored 404s, a POST lands in `reading_out` and answers `applied`); `NavigationReaderTest` covers the policy.
Simulator check: `WITH_EBOOKS=1` test server; download an EPUB book; stop the server; Downloads -> Read opens at `tsread://app`,
turn 3 pages; start the server, open any page: the server's reading place moved (GET `/reading/<id>` as the tester). Gate
`make ios-sim`. Docs: `docs/ios.md` "Offline reading (I3)", `docs/roadmap.md` M9 status, the AGENTS.md line and the 0048
addendum below, `docs/manual-checks.md` IOS-11 (a device in airplane mode plays a downloaded book and reads its text).
Out of scope: read aloud offline (I4); highlights and lookups offline (they need the server, as on Android).

## 0048 addendum and AGENTS.md wording (applied with I3d; the D12 lines with the package that flips them)
"## Addendum 2026-11-19 (I3)" in `docs/decisions/0048-iphone-app.md`:
- The Downloads item and `native-downloads.js` (I3b), the next-book switch (I3c) and `offline.js` (I3d) now include `ios`. Skip
  silence, volume-key page turns and the new-episode notification stay Android-only.
- Downloads use a background `URLSession` with the bearer as a header; files in `Application Support/offline/`, excluded from
  backups; automatic downloads only on an unexpensive, unconstrained network.
- The stored reader answers at `tsread://app`, because WKWebView cannot answer at the server's own address.
AGENTS.md, Security and privacy, a new bullet after the cast address token bullet:
"- In the iPhone app, a stored reader page opens only at `tsread://app` (decision 0048, addendum I3): it answers GET only from the
  signed-in person's stored text, through the shared `offlineRoute`, and one POST, `/reading/{id}`, which is kept on the phone
  until the server's own page takes it through the bridge (`rd`). That origin sends nothing to the network, never gets
  `window.tsNative` and is never a bundled page."

## Risks
- A background session may drop a hand-set `Authorization` header (Apple lists it as reserved). I3b's simulator check proves
  it first; if downloads answer 401, commit what is green and report `PLAN GAP: bearer on background downloads` (never a token
  in a URL).
- `sendBeacon` from `tsread://app` may not reach the scheme handler; then the last few seconds before leaving the reader
  are kept only in that origin's storage (the place still opens there next time). Guess.
- CSP `'self'` and ES modules on a custom scheme are expected to work (WebKit treats `tsread://app` as an origin); I3d's
  `OfflineReaderTests` prove it before the rest is built.
- Automatic downloads can trail the person's own (priority is a hint). Only iOS 26 simulators; iOS 17 untested.
