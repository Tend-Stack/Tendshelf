# Android app (plan, 2026-11-05)

Decision: [0024](../decisions/0024-android-app.md). Order of work: [next-stage.md](next-stage.md). Not started; the
AGENTS.md changes listed in the decision come first.

## Recommendation

A small Kotlin app with two halves: **the existing pages in a WebView** for everything you look at, and **native Media3
playback in a `MediaLibraryService`** for everything you hear. The server's pages stay the one interface. The native half
owns sound, the lock screen, Bluetooth, Android Auto, downloads, sleep and saving your place. Built with the Gradle wrapper
and the Android SDK command-line tools; no Android Studio, no Node, no Google Play services.

About **11 packages** (A0 to A10); A1 is the server's.

## The options, judged

| Option | Builds with no Node? | Screen off | Android Auto | Fits one server per owner? | Packages | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| (a) TWA | Yes: plain Gradle with `androidbrowserhelper` works, Bubblewrap is optional | The browser's audio, so no better than the PWA | No | Poorly: needs HTTPS and `assetlinks.json` with our key on each server, and the stock launcher compiles in one host (a custom launcher could choose it; unproven) | 2 | No. It adds an icon, which the PWA already has. |
| **(b) Kotlin shell + Media3** | **Yes** | **Native foreground service** | **Yes** | **Yes: the address is typed in** | **11** | **Yes** |
| (c) Full native app | Yes | Native | Yes | Yes | 35+ | Best feel, but about 40 pages and the reader rebuilt and two interfaces forever. (b) keeps this open: its service layer is what (c) would need. |
| (d) Wails v3 mobile | Lists npm; needs cgo and the NDK | `dataSync` service only | No | No: pages load only from the embedded assets | 8 plus a fork | No, see below. |
| (e) Capacitor | No: its CLI is Node | Needs plugins | Needs native code anyway | Yes | Banned | No. A change that needs `package.json` is rejected. |

**Audiobookshelf's own app supports this shape.** It has a web interface in Capacitor, but its manifest declares a native
`PlayerNotificationService` with `foregroundServiceType="mediaPlayback"`, the `MediaBrowserService` action and the Android
Auto entry. The web half never played the audio in the car, and that is the part we would take.

### Wails v3.1.0, read from the source

- Status: "experimental", "outside the stable desktop compatibility contract" (`README.md:9`, `release_notes.md:53`).
- More real than the roadmap assumed: emulator and device builds, bindings, events, biometrics and secure storage all exist (`ANDROID.md`).
- **Blocker 1, remote pages.** `setURL` is an empty function (`webview_window_android.go:367`), and `MainActivity.loadApplication`
  loads only `https://wails.localhost/` or a dev server. Tendshelf's rule is that pages load from the server's origin.
- **Blocker 2, playback.** `WailsForegroundService` runs as `dataSync` (hard-coded), and no `MediaSession` exists anywhere in the template.
  `dataSync` is limited to 6 hours in 24 on Android 15 and later and is the wrong type for audio. There is no
  `MediaLibraryService`, so no Android Auto.
- **Blocker 3, security.** `window.wails` reaches every frame (`release_notes.md:94`).
- Also: Go becomes a cgo shared library, NDK r29 is listed (this machine has 30), and `ANDROID.md:47` lists npm.

Getting audio right would mean writing the same Java service by hand beside a Go library that does nothing useful on the
phone. Desktop keeps Wails, as a pure wrapper. The phone needs a playback program, which is another job.

## Architecture

```
android/  (its own Gradle project, like desktop/ is its own Go module; the server never links it)
  app/
    PlaybackService   MediaLibraryService + ExoPlayer: one session, foreground type mediaPlayback
    BookPlayer        ForwardingPlayer: next/previous mean chapters, skip means the person's interval
    LibraryTree       onGetChildren for Auto: Continue, Up next, Downloaded, Series, Search
    ProgressWriter    10 s saves, a local outbox, POST /api/v1/progress/batch on reconnect
    SleepTimer        minutes or chapter, fade, shake through SensorManager
    Downloads         Media3 DownloadManager
    ShellActivity     the WebView, the connect page, the offline page, back button, safe areas
    Bridge            addWebMessageListener, allowed for the chosen server's origin only
    Token store       Android Keystore-wrapped device token
  assets at build time: the Gradle task copies tokens.css, bundle.css, fonts and native-start.js from internal/web/static
```

**Native owns**: sound, position, saving, sleep, next book, downloads. A page that is frozen with the screen off must not be able
to lose anything. **The page owns**: everything drawn. In the shell, the web player is a remote control.

The seam is small. `player.js:9` takes `audio` from one line, and player events go through `listen(audio, ...)`. A new
`native-audio.js`, loaded only when `data-shell="android"`, provides an object with the same methods and events, driven by
state from the bridge. `player.js` then stands down in the shell: no progress saves (they would race the native writer under a
second device id), no Media Session, no JS sleep timer, no queue call. `player.js` has 47 `audio.` uses, so A4 must inventory
them. Browsers keep today's path: `make visual-compare` proves no change.

Sound modes (decision 0022) and skip silence become Media3 audio processors, which removes the AudioContext risk on Android
but means a second implementation of the four modes.

**Sign-in**: the person signs in on the server's own page in the WebView, so passwords, OIDC and passkeys all work once. Then the
page asks for a short one-time code, and the native side swaps it at a new API route for a `mobile` device token. The token never
reaches JavaScript. This is a new way to issue a token, so it goes through `architect`, with forbidden-case tests. Sign-out
revokes the device and deletes downloads, like the PWA (decision 0012).

**No server reachable**: the WebView has nothing to load, so the app opens a bundled page built from the same CSS listing the
downloaded books, driven by the bridge. No Compose: one look, and fewer modules.

**Network**: self-hosters run plain `http` on a LAN and private certificates, so the network config allows cleartext and user
CAs. A person's address is their own choice, and the app warns once on `http`. The bridge's origin rule is exactly that one
origin. Decide in A2 whether the bridge is allowed on `http` at all; Google's guidance says HTTPS only, and a LAN attacker
could otherwise reach it.

**Versions**: `minSdk 28` (Android Auto needs Android 9), `targetSdk 36`, Media3 1.11.1 (latest stable found), AGP 9 with built-in
Kotlin, JDK 21. AGP 9.0 stops at API 36.1, so use platform 36 here, not 37.0. All Gradle dependencies pinned with
`gradle/verification-metadata.xml`, and each gets a `NOTICES.md` row.

## What the server already has, and what it needs

Exists in `/api/v1` ([api.md](../api.md)): device tokens with `kind: "mobile"`, `GET /books/{id}` as a download manifest with chapters,
files, `needsCompat` and permissions, `Range` streaming with `?download=1`, covers, peaks, progress with `force` and batch, bookmarks with
`clientId` for safe retries, the queue with autoplay, shelves, search, ETags. `ts_shell=android` is already accepted (`shell.go`) and
`offline.js` already skips the service worker when `data-shell` is set.

New in **A1**:

| Need | Why |
| --- | --- |
| `GET/PATCH /api/v1/settings` | Speed, skip lengths, auto rewind, next in series, sound. Only the HTML settings form exists. |
| Per-book speed in the API | From L1, so the app reads one value. |
| `GET /api/v1/continue` | Auto's first row. Only the progress change feed exists. |
| Next book answer | `POST /queue/next` covers the queue only; next in series lives behind `POST /hx/player/next`. Native must get both with the screen off. |
| One-time code route and swap | The sign-in above. Handles `409` at the device limit. |
| Shell behaviour for `android` | Load `native-audio.js`, hide Downloads where native replaces it, keep the CSP unchanged. |

Later: ebook read and highlight API routes only if offline reading needs them (A9).

## Packages

| # | Package | Size | Note |
| --- | --- | --- | --- |
| A0 | Skeleton and playback proof | M | Below |
| A1 | Server for the app | M | Go, then templ. Security plan by `architect`. |
| A2 | Shell: connect page, WebView, back, safe areas, links, file saves | M | Native feel rules (decision 0011) on the shell |
| A3 | Playback service: book as a playlist, chapters as next and previous, notification, speed | M | Multi-file books |
| A4 | Bridge: web player as remote control | M | Highest-risk package |
| A5 | Progress outbox, conflict prompt, smart rewind, queue and series next with the screen off | M | Needs A3 |
| A6 | Sleep timer, fade, shake | S | |
| A7 | Downloads and the offline page | M | Media3 `DownloadService`, its 6-hour `dataSync` limit handled |
| A8 | Android Auto: tree, resume, search, chapter and speed buttons | M | Needs the Desktop Head Unit, then a real car |
| A9 | Offline reading in the app | M | Serve stored parts through `shouldInterceptRequest`; `http` servers have no service worker |
| A10 | Release: signing, `make apk`, CI job, reproducible build, F-Droid metadata | M | Operator's distribution choice first |

Parallel safe: A2 + A3 after A0. A4 waits for both. Then skip silence, widget and auto-download ([next-stage.md](next-stage.md)).

## First package: A0

Goal: prove the three risky things on this machine before anything else is built.

1. `android/` Gradle project with the wrapper; `make apk-debug` runs `./gradlew assembleDebug`. The root `go build`, `make check`
   and `CGO_ENABLED=0` stay untouched. No Node anywhere.
2. A hard-coded server address and test-only sign-in via `POST /api/v1/auth/token` (replaced in A1).
3. `PlaybackService` plays one book from `/api/v1/stream/{id}` with the bearer header.
4. A WebView showing the server's page, with a shell mark through `/?shell=android&next=/`.

Acceptance, on the running emulator and recorded in the status note:
- The APK builds from a clean checkout with `ANDROID_HOME` set and no Android Studio.
- Lock the screen for 30 minutes (`adb shell input keyevent 223`): `dumpsys media_session` shows playing and the position moves.
  Then the same with the WebView's activity stopped.
- `dumpsys activity services` shows the foreground type `mediaPlayback`.
- Media buttons sent by `adb shell media dispatch` change chapter or play state.
- Kill the activity: sound continues. Tap the notification: the page returns.
- Test server only, synthetic audio from a test fixture. **Never `~/.local/share/tendshelf`, never port 8095.**
- A short note on what the emulator could not prove; the operator repeats the screen-off test for an hour on a real phone.

Out of scope: the bridge, downloads, Auto, sign-in, signing.

Stop and report if: Gradle cannot reach its repositories (the VPN on this machine can cut traffic), or the bearer stream needs a
server change.

## What this machine has

**Has**: JDK 21, `~/Android/Sdk` with cmdline-tools (`sdkmanager`, `avdmanager`), platform-tools (`adb`), `emulator`, platforms 36 and 37.0,
build-tools 36.0.0 and 37.0.0, NDK 30, cmake. AVD `Pixel_9_Android_16` is running as `emulator-5554`: Android 16 (API 36), x86_64,
Play image with Chrome, the Android Auto package and WebView 133.

**Lacks**: `gradle` and `~/.gradle` (the wrapper fetches Gradle, but the wrapper files must be seeded once), `kotlinc` (not needed:
AGP's built-in Kotlin), the Android Auto Desktop Head Unit (`extras` is absent, needed by A8), an arm64 image, a release keystore. Android
Studio is absent and not needed. Nothing was installed for this plan.

## Distribution, the operator's call (before A10)

- **Google's developer verification**: from 30 September 2026 in Brazil, Indonesia, Singapore and Thailand, and global in 2027, certified
  devices check that apps come from a verified developer, with an advanced flow for others. A family APK will soon need a
  verified key, a limited-distribution account or that flow.
- **Google Play**: the simplest for family listeners and the cleanest for Android Auto, but it needs a developer account.
- **Android Auto and sideloading**: third-party guides say a sideloaded app shows only with Auto's developer "Unknown sources" setting,
  and that updates can reset it. An unfair ask for a family listener, so Play matters more than it first seems. Check in A8.
- **F-Droid**: fits the project, but its signing model and verification are in open conflict. Reproducible builds help either way.

## Risks

| Risk | Answer |
| --- | --- |
| The bridge gets large and every player change costs twice | Keep native as the truth and the page as a remote; A4 sets a small message list and a test for each. |
| The WebView feels less than native | Decision 0011 work applies. If it falls short, replace the worst screens natively; A3 onward is reusable. |
| Media3 resumes from the first track in Auto (a report from 2023) | The maintainer promised a fix in #156; not confirmed here. A8 tests resume at a saved position, and our position store is the truth. |
| Passkeys inside a WebView | Not assumed. Check in A2. OIDC and passwords work. |
| Two sound-mode implementations | Accepted. The modes are named settings, not shared code. |

## Sources

[Wails v3.1.0 source] local module cache, `ANDROID.md`, `release_notes.md`, `MainActivity.java`, `WailsForegroundService.java`, `webview_window_android.go`.
[TWA integration guide](https://developer.chrome.com/docs/android/trusted-web-activity/integration-guide) ·
[Media3 serve content](https://developer.android.com/media/media3/session/serve-content) ·
[Android for Cars](https://developer.android.com/media/implement/surfaces/cars) ·
[FGS timeouts](https://developer.android.com/develop/background-work/services/fgs/timeout) ·
[WebView bridge guidance](https://developer.android.com/develop/ui/views/layout/webapps/native-api-access-jsbridge) ·
[Media3 releases](https://github.com/androidx/media/releases) ·
[Media3 downloading](https://developer.android.com/media/media3/exoplayer/downloading-media) ·
[androidx/media #236](https://github.com/androidx/media/issues/236) ·
[AGP 9 notes](https://developer.android.com/build/releases/agp-9-0-0-release-notes) ·
[audiobookshelf-app](https://github.com/advplyr/audiobookshelf-app) and its [manifest](https://raw.githubusercontent.com/advplyr/audiobookshelf-app/master/android/app/src/main/AndroidManifest.xml) ·
[Capacitor background audio](https://capawesome.io/blog/how-to-play-audio-in-the-background-in-capacitor/) ·
[developer verification](https://developer.android.com/developer-verification) ·
[timeline report](https://thehackernews.com/2026/06/google-sets-sept-30-deadline-for.html) ·
[Auto sideloading guide](https://www.androidauthority.com/sideload-apps-on-android-auto-3681820/) ·
[F-Droid thread](https://forum.f-droid.org/t/google-will-require-developer-verification-to-install-android-apps-including-sideloading/33123).
