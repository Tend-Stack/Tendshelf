# 0048 iPhone app: a thin Swift shell around the server's pages, with the Android app's shared Kotlin
Status: accepted (operator pre-approved the recommendations, 2026-11-19). Plan: `docs/plans/iphone-app.md`. Parents:
[0024](0024-android-app.md) (Android app), [0026](0026-app-sign-in.md) (app sign-in), [0028](0028-qr-sign-in.md) (links
and `tendshelf:`), [0012](0012-offline-pocket.md) (downloads). No new dependency.

## Operator's words
2026-11-19: "start iphone app development, which should share most of the android." 2026-11-08: build so iOS reuses as
much as possible; screens are server pages so the native layer stays thin; new logic goes into `android/shared`
`commonMain` unless it touches platform APIs.

## Context
The Android app is a WebView on the server's pages plus native playback, downloads and sign-in glue. Its pure logic
(origins and links, the navigation policy, the bridge grammar, PKCE, positions, the outbox, sleep, JSON) already sits in
`android/shared`, a Kotlin Multiplatform module that targets only the JVM. Three files in it still lean on the JVM
(`Trust.kt` imports `javax.crypto`; `Sleep.kt` and `BridgeProtocol.kt` call `Math`), the start and connect rules live in
the Android app (`ConnectFlow.kt`, `AuthApi.kt`), and some shared URLs say `shell=android`. The server already knows
`ts_shell=ios` (`internal/web/shell.go`) but gives the app's window rules to `android` only. A Mac (Xcode 26.2, iOS 26.2 SDK,
simulators on iOS 26.0 and 26.1) is reachable over ssh; there is no macOS CI runner yet.

## Decision
1. **A native Swift shell, not a port.** SwiftUI app lifecycle, one `WKWebView` showing the server's pages with
   `data-shell="ios"`; native owns sign-in glue, the token, and (from I2) sound, lock screen and saving. Same split as
   0024 D1. Not Capacitor (Node), not Kotlin/Compose Multiplatform UI (the pages are the interface), not a PWA wrapper
   (no lock-screen or CarPlay control without native audio).
2. **`android/shared` is the iPhone's logic too.** It gains `iosArm64()` and `iosSimulatorArm64()`, declared only on a
   macOS host so Linux builds, the APK and F-Droid reproducibility do not change. It is built as one static
   `TendshelfShared.xcframework`. Swift calls it for: origin parsing and matching, `decide` (navigation), `parseHandoff`,
   PKCE encoding, the swap and `/healthz` answers, the connect rules, the bridge grammar and encoders, and everything later
   phases need (positions, outbox, sleep, manifests, podcasts). Swift does only what needs Apple APIs: `WKWebView`,
   `URLSession`, Keychain, random bytes and SHA-256 (CryptoKit), and later AVFoundation, MediaPlayer, CarPlay, WidgetKit.
   Rule: new logic goes into `commonMain` unless it needs a platform API; code that both apps would write twice moves there.
3. **The one `expect` function gets an iOS `actual` that wraps `NSURLComponents`**, with a filter that returns null wherever
   it would disagree with `java.net.URI`. A shared parity table (`RawUriTest`, in `commonTest`) runs on the JVM and on the
   simulator, so both parsers answer the same or iOS refuses. Not a hand-written parser (the "iOS later" rule in
   `docs/android.md`); failing closed only makes a link or a navigation refused, never trusted.
4. **The bridge keeps its words.** Web to app: `WKScriptMessageHandler` named `tsShell`, accepted only from the main frame
   whose `securityOrigin` passes `Origin.sameSource`, body a string, through the shared `parseBridge` and `TokenBucket`.
   A `WKUserScript` at document start (main frame only, page world, origin baked in) defines `window.tsNative` as an
   `EventTarget` with `postMessage` and `onmessage`, the object Android's `addWebMessageListener` gives, so page scripts do
   not change. App to web: `evaluateJavaScript` of one delivery call whose argument is the shared encoder's JSON written as
   a JS string with `jsonString`. Page scripts test "a phone app" (`android` or `ios`), not `android`; the server loads
   each `native-*.js` only for a shell that implements its words (I2 onwards).
5. **Sign-in is 0026, unchanged, inside the `WKWebView`.** Not `ASWebAuthenticationSession`: its cookies are Safari's, not
   the WebView's, the same reason 0026 D1 refused Custom Tabs. The navigation delegate takes `/app/connect/done?code=` for
   the exact origin and main frame and cancels it; Swift swaps code plus verifier at `POST /api/v1/auth/token` (kind
   `mobile`) over an ephemeral `URLSession` with no cookie store, no cache, no redirects and a 4 KiB answer cap. Provider
   sign-in (0037) stays in the window as on Android.
6. **The token lives in the Keychain** (generic password, service `app.tendshelf.ios`, account `device-token`,
   `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`: never in a backup, readable for background playback). The origin,
   pin, device and person ids are in `UserDefaults`. Keychain items outlive an uninstall, so a launch without the
   app's first-run mark deletes the item first.
7. **The project is a hand-kept `ios/Tendshelf.xcodeproj`**, kept small: Xcode 16 synchronized folders
   (`PBXFileSystemSynchronizedRootGroup`), so adding a Swift file never touches `project.pbxproj`; build settings in
   `ios/Config/*.xcconfig`; a hand-written `ios/Config/Info.plist`; a shared scheme. No XcodeGen or Tuist (tools from
   Homebrew, and a generator to keep up), no Swift package app (SwiftPM builds no iOS app bundle).
8. **Minimum iOS 17.0**; bundle id **`app.tendshelf.ios`** (tests `app.tendshelf.ios.tests`), the family of
   `app.tendshelf.android`. iOS 17 runs on every iPhone from 2018 on (XS, XR) and gives the Observation framework; nothing
   needs 18. A bundle id cannot change after the first App Store upload, so it is fixed now.
9. **Plain `http` stays possible** (0026 D8): `NSAllowsArbitraryLoads` and `NSLocalNetworkUsageDescription`, with the
   same one-time warning before connecting over `http`. App Review may ask; the answer is that people run their own server.
10. **Bundled pages are the Android ones.** `start.html`, `choose.html`, `join.html`, `offline.html`, `start.css`, the
    server's `tokens.css`, `bundle.css`, `fonts.css` and fonts, and the words in `android/app/src/main/res/values/strings.xml`
    are copied into the app at build time and served by a `WKURLSchemeHandler` at `tsbundle://app/`. The shared `decide`
    takes the bundled site as a parameter (Android `https://appassets.androidplatform.net`, iPhone `tsbundle://app`). One
    copy of the pages and their words for both apps.
11. **`tendshelf://open?link=` is the one way in from outside**, read by the shared `parseHandoff`; the scheme carries only a
    link (0028). A QR code read by the system camera opens Safari, not the app; in-app scanning and the Safari-to-app
    handover of a pairing link are later (I4) and need their own check, because any iPhone app can claim a scheme.
12. **The server treats `ios` as a phone app's window** where the rule is about the window, and as Android-only where it is
    about a feature the iPhone does not have yet. A helper `isAppShell(shell)` (android or ios) governs: the Connect
    page (`routes_appconnect.go`), QR pairing in the app (`routes_pair.go`), provider sign-in in the window
    (`routes_oidc.go`) and `/sw.js` answering 404 (`routes_offline.go`). Stay `android` until their phase: the native
    scripts in `layout.templ` and the player's words (`player.templ`, I2), the skip-silence and next-book switches
    (`routes_me.go`, I2/I3), the Downloads item (`nav.templ`, I3) and `offline.js` (`server.go`, already off for `ios`,
    I3). The volume-key page turn (`routes_reader_settings.go`) stays Android-only: iOS has no public volume-button API.
13. **Builds run on the Mac from a script.** `ios/tools/ios-build.sh` (runs on macOS: Gradle XCFramework and the shared iOS
    tests, then `xcodebuild build` and `test` on a simulator; what a CI job will call) and `ios/tools/mac-build.sh` (runs on
    Linux: rsync the tree to `TS_MAC_HOST`, then run the first over ssh). `make ios-sim` calls the second. No CI job until
    the macOS runner exists. Simulator builds are ad hoc signed; device signing, TestFlight and the App Store are the
    operator's, from a git-ignored `ios/Config/Signing.local.xcconfig`.

## Privacy and security carried over
The app talks only to the server the person typed, with no other outbound request; no analytics, crash reporting,
iCloud or push. The token never reaches page script, a URL, a log or the WebView's cookie store, and never becomes a
session. Sign-out (seen as the page's POST to `/logout`, or a 401 from `GET /api/v1/me`) deletes the token, the web data
(`WKWebsiteDataStore` all types) and, from I3, the downloads; the revoke call runs after the local wipe. No service worker
runs in the window (`/sw.js` 404 under `ts_shell=ios`; WKWebView also keeps workers to app-bound domains). The Web
Inspector is on in Debug builds only.

## Later phases
I2 native audio (AVPlayer, AVAudioSession, MPNowPlayingInfoCenter, MPRemoteCommandCenter, sleep, speed, saves through the
shared outbox). The bearer header reaches AVPlayer through `AVAssetResourceLoaderDelegate`; the undocumented
`AVURLAssetHTTPHeaderFieldsKey` is not used. I3 downloads and offline (URLSession background downloads, the bundled
Downloads page, sign-out deletes them). I4 CarPlay (needs Apple's CarPlay audio entitlement, which the operator requests),
a WidgetKit widget, device read aloud, in-app QR scanning, the privacy manifest, app icon and TestFlight archive.

## Rules that change (AGENTS.md), exact wording
- **Stack constraints**, the Android paragraph's last sentence "Other mobile platforms are later
  ([decision 0024](docs/decisions/0024-android-app.md))." becomes: "**iPhone** is a Swift app under `ios/`
  ([decision 0048](docs/decisions/0048-iphone-app.md)): a `WKWebView` on the server's pages plus native glue, built by
  `xcodebuild` on a Mac from the hand-kept `ios/Tendshelf.xcodeproj` (synchronized folders, settings in
  `ios/Config/*.xcconfig`). Apple system frameworks only: no CocoaPods, no Swift packages, no project generators, no
  Homebrew. Its logic is `android/shared`, built as `TendshelfShared.xcframework` (targets `iosArm64` and
  `iosSimulatorArm64`, declared only on a macOS host); new logic goes into `android/shared` `commonMain` unless it needs a
  platform API. It never links into the server. Other mobile platforms are later."
- **Stack constraints**, after the Android dependency list add: "iPhone (decision 0048), under `ios/` only: Apple system
  frameworks, and the Kotlin/Native toolchain the Kotlin Gradle plugin fetches at build time for the iOS targets; what
  Gradle resolves is pinned in `android/gradle/verification-metadata.xml`."
- **Stack constraints**, "`native-audio.js` is loaded only in the Android shell (`data-shell="android"`)." becomes (when I2
  lands): "The `native-*.js` files are loaded only in a phone app's window (`data-shell` `android` or `ios`), each only for
  a shell that implements its bridge words."
- **Security and privacy**: "The Android app talks only to the server the person typed in" becomes "The phone apps
  (Android and iPhone) talk only to the server the person typed in"; and "inside the Android app's window (`/sw.js` answers
  404 there)" becomes "inside the Android or iPhone app's window (`/sw.js` answers 404 there)".
- **Design**: "The Android app's bundled start and offline pages use the same `tokens.css` and `bundle.css`, copied at build
  time." becomes "The phone apps' bundled start, join and offline pages (one copy, `android/app/src/main/assets/`) use the
  same `tokens.css` and `bundle.css`, copied at build time."
- **Tests and checks**, add: "iPhone: `make ios-sim` (needs `TS_MAC_HOST`) syncs the tree to the Mac and runs
  `ios/tools/ios-build.sh`: the shared module's iOS tests, the XCFramework, then `xcodebuild build` and `test` on a
  simulator. A CI job comes with the macOS runner."
- Docs, applied with I1: `docs/roadmap.md` gains "M9 iPhone (decision 0048)"; `docs/feature-map.md` "Native mobile app" row
  names the iPhone app; `docs/architecture.md` module table gains `ios/`; `docs/android.md` "iOS later" points to
  `docs/ios.md`.

## Consequences
- One logic base for two apps; a change to `commonMain` is checked on the JVM on Linux and on the simulator on the Mac.
  Until the macOS runner exists, `make ios-sim` is a manual gate and `check-android.sh` gains a grep that refuses
  `java.`, `javax.` and `Math.` in `commonMain`.
- Android changes only by moves (connect rules, health and swap answers into `shared`) and default parameters; its
  behaviour and its tests stay the same.
- The Mac needs Xcode's first-launch components installed once (`xcodebuild -checkFirstLaunchStatus` exits 69 today).

## Addendum 2026-11-19 (I2)
- Skip silence and the next-book download switch stay Android-only: AVFoundation has no silence skipping, and downloads
  are I3. The native scripts loaded for `ios` are `native-audio.js` and `native-feel.js`.
