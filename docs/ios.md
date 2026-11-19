# iPhone app: building on a Mac

Decision [0048](decisions/0048-iphone-app.md), plan [iphone-app.md](plans/iphone-app.md). Phase I1 is built: I1a the
shared Kotlin module for iOS, the server's `ts_shell=ios` rules and the Mac scripts; I1b the Swift shell, bundled pages and
connect; I1c sign-in, sign-out and links (see "The sign-in flow" below). Nothing here is part of `make check` or `make build`; Linux and CI never touch an iOS target.

## What builds where

- `android/shared` is a Kotlin Multiplatform module with `jvm()` always, and `iosArm64()` and `iosSimulatorArm64()` only when
  Gradle runs on a Mac (`os.name` starts with `Mac`). Both iOS targets go into one static XCFramework, `TendshelfShared`.
- On the Mac Gradle runs with `-Ptendshelf.sharedOnly=true`, which leaves `:app` out of the build, so no Android SDK is
  needed there.
- `commonMain` stays platform-neutral: no `java.*` or `javax.*` import and no `Math.*` (`android/tools/check-android.sh`
  fails on them). Platform pieces sit behind `expect`/`actual`: `parseRawUri` (`java.net.URI` on the JVM, `NSURLComponents`
  on iOS, held to the same answers by `RawUriTest`, which runs on both) and `SynchronizedOnJvm` (a lock on the JVM, nothing
  on iOS, where the app calls an `Outbox` from one serial queue).

## Scripts

All three are in `ios/tools/`.

- `ios-build.sh [shared|build|test|all|clean]` runs on the Mac (CI will call it). Steps: check Xcode's first-launch
  components (exit 3 if missing), pick JDK 21 with `/usr/libexec/java_home -v 21`, find the simulator's UDID (exit 4 if
  none). `shared` runs `:shared:iosSimulatorArm64Test` and `:shared:assembleTendshelfSharedDebugXCFramework`; `build` and
  `test` run `xcodebuild` on `ios/Tendshelf.xcodeproj` (exit 5 until I1b adds it). Logs go to `ios/build/logs/`, result
  bundles to `ios/build/results/` (newest three kept). `clean` removes `ios/build` and `android/shared/build`.
- `mac-build.sh [step]` runs from Linux: it syncs the tree to the Mac with `rsync` (excludes in `rsync-exclude`), takes a
  lock (`<dir>.lock`, exit 6 when another build holds it), runs `ios-build.sh` over `ssh` and prints its output.
  `mac-build.sh clean` also removes the synced tree on the Mac. It needs `TS_MAC_HOST` (exit 2 otherwise).
- `make ios-shared` is `mac-build.sh shared`; `make ios-sim` is `mac-build.sh all`.

| Variable | Meaning | Default |
| --- | --- | --- |
| `TS_MAC_HOST` | the Mac's ssh host (key login, `BatchMode`) | required |
| `TS_MAC_DIR` | directory on the Mac, relative to its home | `tendshelf-ios` |
| `TS_IOS_SIM` | simulator name | `iPhone 17` |
| `TS_IOS_SIM_OS` | iOS runtime of the simulator | newest installed |
| `TS_BUILD_DESCRIBE` | version string baked into the build | `git describe --tags --long --dirty`, else `unknown` |

The Gradle property `tendshelf.iosSimulator` (set by `ios-build.sh` to the UDID) names the simulator that the shared
Kotlin/Native tests boot; it falls back to `iPhone 17`.

## First launch

A fresh Xcode needs `sudo xcodebuild -runFirstLaunch` once (it installs components that need administrator rights).
`xcodebuild -checkFirstLaunchStatus` exits 0 afterwards, and `ios-build.sh` refuses to run until it does. The shared Kotlin
build and its simulator tests ran on a Mac where it still exited 69; `xcodebuild` for the Swift shell is what needs it.
`xcrun` prints `Install Failed: Authorization is required` lines until then; they are harmless noise.

## What is pinned

Gradle checks every dependency against `android/gradle/verification-metadata.xml`, which only gained entries for the iOS
artifacts of kotlinx-serialization and the Kotlin/Native distribution `kotlin-native-prebuilt` (so the compiler itself is
pinned by sha256). Kotlin/Native then downloads its own helper packages (LLVM and libffi) from
`download.jetbrains.com/kotlin/native` into `~/.konan/dependencies` (about 1.5 GB with the compiler). Those are not covered
by Gradle's verification and carry no checksum in Kotlin's own configuration: they are the one unpinned download of the iOS
build, fetched at build time on the Mac only, never at runtime and never in the shipped app.

## The sign-in flow (I1c)

- **Connect.** The start page's address form reaches `ConnectFlow.onAddress` through the shared `decide` (a bundled command from
  a bundled page only). Each candidate address gets `/healthz`; plain http asks once per address. A fresh PKCE verifier is held
  in memory and `/app/connect?shell=ios` opens in the window. The person signs in on the server's own page; the password never
  touches native code.
- **The code.** The consent page's redirect to `/app/connect/done` is caught by the navigation policy (exact origin, main frame,
  verifier held), cancelled, and the code is swapped for a device token by native HTTP. The token goes to the Keychain
  (readable after first unlock, this device only), the ids to `UserDefaults`, and the verifier is cleared by that one swap.
  A 409 with a held token signs out ("This sign-in belongs to someone else"); without one it opens `/me/devices`.
- **Reconnect and launch.** `/login` with a held token asks `/api/v1/me`: 401 signs out, 200 starts a reconnect. Launch asks
  once; a network failure leaves the person where they are.
- **Another server.** Typing a different address while signed in asks "Change server?"; confirming revokes the old device,
  wipes the web data and starts the new sign-in. A token never follows the person to another address.
- **Links.** `tendshelf://open?link=` and the join page go through `parseHandoff`, then a confirmation naming the address,
  then `/healthz`, then the invitation page or the pairing page (the verifier is held for pairing). Nothing is requested
  before the tap; a link to the connected server only gets a sentence.
- **Sign-out.** `SignOut.run`: the token and ids go first, then every cookie, storage and cache entry, then the `WKWebView` is
  replaced (no history), then the start page shows the address and the reason, then the device is revoked in the background.
  The page's own Sign out is a POST to `/logout`; the server revokes the device, the next `/login` check finds a 401 and the same
  path runs.
- **Provider pages.** `/login/oidc/origins` (public) gives the sign-in provider origins the window may stay on; refreshed at
  launch and when `/login` finishes.
- **Limits.** Browser history is not cleared after a connect (WKWebView has no API; sign-out replaces the view). Audio is the
  web `<audio>` and stops when the screen locks (I2). No downloads, QR scanning, icon or signing yet.
- **Debug driver.** Debug builds only (`DebugDriver.swift`): launch arguments `-TSTestAddress`, `-TSTestLogin user:pass`,
  `-TSTestSignOut 1`, `-TSTestAutoYes 1` fill the page forms so a headless simulator run can sign in. Release builds contain none
  of it: check with `strings` on a Release binary (`xcodebuild -configuration Release -arch arm64`; an all-architecture Release
  build fails to link because the shared framework is arm64 only).

## Native audio (I2b)

- **Loader.** `MediaLoader` is the `AVAssetResourceLoaderDelegate`: AVPlayer sees only `tsmedia://m/<n>` handles; each loading
  request becomes one GET to the registered `/api/v1/` address with `Authorization: Bearer` and a `Range`, no cookies, only while the
  connection is on the handle's origin. A busy server (503) is retried by the shared rule. The token is never in a URL.
- **Player.** `AudioPlayer` (an `AVQueuePlayer`, one item per playable file, the current and the next) ports `BookEngine`; book time
  comes from `BookTimeline`. `Playback` owns it with `AudioSession` (category `.playback`, interruptions, route changes) and
  `NowPlaying` (lock screen card and remote commands, by hand).
- **Bridge.** `Bridge` carries the shared bridge grammar over a `WKScriptMessageHandler` (`tsShell`) and `window.tsNative`
  (`NativeScript`); main frame only, connected origin only, 30 messages a second.
- **Stations (I2r, decision 0049).** The loader models a file of known length, so an endless stream never settled in it
  (AVFoundation probed far offsets). A station instead plays from a live address: `POST /api/v1/radio/live/{id}/address` with the
  bearer answers a path `/cast/<token>/live`, which the app checks (one shape), joins with its pinned origin and gives to a plain
  `AVPlayerItem` with no cookies; `allowsExternalPlayback` is off. The address is a one-station, one-device, one-hour cast token, a
  new one for every connect, and it is never logged or kept. Books and episodes stay on the loader, which no longer has a live mode.
  Simulator check: `TENDSHELF_IOS_RADIO_SERVE=0.0.0.0:8097 go test ./internal/web -run TestIOSRadioServe -v` serves a paced station
  and prints the `/cast/` request counts.
- **Limits.** No saving, smart rewind, sleep or next book yet (I2c). Ogg, Opus and FLAC are not played.
- **Debug driver.** `-TSTestStart <id>` starts playback after sign-in; `-TSTestScript "pause;play;seek 20000;speed 1.50"` sends those
  messages from the page; a probe line each 2 s shows the snapshot, the lock-screen card and the last state the page got.

## Native audio (I2)

- **Store.** `SqliteOutbox` (Swift over the system `SQLite3`) implements the shared `OutboxStore`: `Application Support/outbox.sqlite`, WAL, excluded from backups, readable after first unlock, the table of `SqliteOutbox.kt`. Rows of another device id are invisible and cleared at the next save under a new sign-in. A failed step sets `lastWriteFailed` and the writer keeps the place in memory until the next tick.
- **Writer.** `ProgressWriter` writes a place before any send (every 10 s while playing, on pause, chapter, book change, a settled seek, sign-out), then sends through one serial loop: 401 keeps rows and fails `signedOut`, 429 waits `Retry-After`, other failures back off 5 s doubling to 5 min; a network that returns, and app launch, flush. Flushes and the end-of-book hand-off run inside `beginBackgroundTask`.
- **Resume, question, sleep, next book.** `Resume` (smart rewind, remote place check for books), `AskState` (the page shows the question; while hidden it waits and is repeated on return), `SleepTimer` (shared rules, 250 ms tick only while on, shake from device motion during the fade), `NextBook` (finished sent first, then the queue asked once, started only while still ended).
- **Sign-out.** `SignOut.beforeWipe` runs before the token is cleared: playback stops, the last place is written and sent once with the connection that is going, every row is cleared, the bridge is cut.
- **Android only / limits.** No notification for the question, no downloads or offline next book (I3), the radio stream is unresolved (see the roadmap).
- **Debug driver.** Script steps `wait N` and `signout`; the probe line shows `ask`, `rewound`, `sleep` and `rows`.

## Downloads (I3)

Plan: [plans/iphone-offline.md](plans/iphone-offline.md). I3b is the engine, the store, the Downloads and offline pages, and the
sign-out wipe; a downloaded book still streams until I3c plays it from its files.

- **Transport.** `BackgroundTransport` is one background `URLSession` (`app.tendshelf.ios.downloads`, launch events on, not
  discretionary, no cookies, no cache), so downloads go on with the app suspended or ended by the system; `AppDelegate` hands the
  system's completion handler back once the events are delivered. The device token is an `Authorization` header of each request to
  the pinned origin's `/api/v1/` (checked on the simulator: a background session keeps the header; the files arrive). It is never in
  an address, in `taskDescription` (that is `<bookId>|<cacheKey>`), a row or a default. A redirect is refused, so a 3xx fails the
  file. Resume data embeds the request, so it lives in memory only and is used once.
- **Engine.** `Downloads` ports `Offline.kt` without Media3: manifest text, `downloadPlan` or `episodePlan`, the cover and chapters,
  one task for every file at once, `fileComplete` before a file counts, `bookStateOf` for the book's state, `downloadRetryDelayMs`
  for a busy server, `downloadPercent` for a percent that never goes back. A refusal is an alert in the Android words. `reconcile`
  (launch and every return to the front) restarts a file the system holds no task for.
- **Store.** `offline/offline.sqlite` under `Application Support`, in a folder of its own (`OfflinePaths`: excluded from backup,
  readable after the first unlock, ids through `validId`, names with no `/`). `SqliteOfflineStore` implements the shared
  `OfflineStore` over `SQLite3`; its `offline_files` rows cascade with the book.
- **Wipe.** Sign-out, leaving a server and another person's sign-in delete the rows, cancel every task, remove the folder and every
  `offline.*` default; `offline.wipePending` makes an interrupted wipe finish at launch.
- **Pages.** `tsbundle://app/downloads` and `/offline` are drawn by the shared `downloadsPage` (the same HTML as Android);
  `/covers/<id>` serves the stored cover. A main-frame failure or a 5xx from the connected server shows the offline page with the
  list and Try again. The Downloads nav link, Play, Remove and Pause are the shared `Nav` commands.
- **Debug driver.** `-TSTestDownload <id>` adds a book after sign-in and prints `TSDRIVE dl=<state>/<pct>`; `-TSTestGo <path>`
  opens one page of the server after sign-in.
- **Limits.** No playing from the files, automatic downloads or podcast shows (I3c), no stored text (I3d), no download
  notification, no cellular setting.

## Cleanup

`make ios-shared` leaves the synced tree and its build output on the Mac. `ios/tools/mac-build.sh clean` removes that tree;
`ios/tools/ios-build.sh clean` (on the Mac) removes only the build output. `~/.konan` is a cache Gradle reuses and is kept.
