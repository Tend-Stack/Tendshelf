# 0024 Android app
Status: accepted (operator pre-approved recommendations, 2026-11-05). Needs the operator's approval, which also approves the rule changes below. Plan:
`docs/plans/android.md`.

## Operator's words (2026-11-05)
"when ready, work on mobile app too, starting with android that can actually be built on linux."

## Context
AGENTS.md says "Native mobile is later; the PWA comes first", and the roadmap says "evaluate Wails v3 mobile when stable". The PWA
is built. What it cannot promise is the listening itself: a web page's audio depends on a page that Android may freeze with the screen
off, its lock-screen controls depend on JavaScript being awake, and Android Auto requires a native `MediaLibraryService`, because the car
draws its own interface from that service. So the car, the first place the vision's commuter listens, is out of reach from the web.

The app must build on Linux with no Node, no Android Studio and nothing proprietary in it. It must also connect to whichever server its
owner runs, including plain `http` on a home network.

## Decision
1. **A small Kotlin app: the existing pages in a WebView, native Media3 playback in a `MediaLibraryService`.** The pages stay the one
   interface. Native owns sound, position, saving, sleep, next book and downloads; the page is a remote control while the shell plays.
2. **Not a TWA.** Plain Gradle does build one without Node, but it gives no Android Auto, stays on the browser's audio, needs HTTPS and an
   asset-links file on every owner's server, and adds only an icon to the PWA.
3. **Not Wails on mobile.** In v3.1.0 Android is experimental, `setURL` does nothing so pages cannot load from the server, its one
   foreground service is `dataSync` (limited to 6 hours in 24 on Android 15 and later), it has no media session, and its bridge reaches every
   frame. Wails stays for desktop.
4. **Not Capacitor.** Its CLI is Node.
5. **Not a full native app, for now.** It would mean about 35 packages and two interfaces to keep alike. The native half built here is the
   part such an app would need, so the door stays open.
6. **`android/` is its own Gradle project**, as `desktop/` is its own Go module. The server binary never links it and `make build` and
   `CGO_ENABLED=0` do not change. Built by the Gradle wrapper with the command-line SDK tools. No Google Play services, Firebase, crash
   reporting or analytics, so no Cast SDK.
7. **Sign-in happens on the server's own page.** The native side gets its device token by a one-time code, so the token never reaches JavaScript
   and passwords, OIDC and passkeys need no second path.
8. **The bridge is `addWebMessageListener`, limited to the chosen server's origin.** Never `addJavascriptInterface`. The message list stays
   small and every message has a test.
9. **No service worker in the shell, and the service worker rules stay.** Offline audio is native; offline text is served by the shell.
10. **Browsers see no change.** Without `data-shell="android"`, pages stay identical, proved by `make visual-compare`. The CSP is unchanged.
11. **Written fresh, not ported.** The Audiobookshelf app (GPL-3.0) is read for understanding only; the `NOTICES.md` rules apply as they do to the server.

## Rules that change (AGENTS.md and docs), applied when this is approved
- **Stack constraints**: replace "Native mobile is later; the PWA comes first." with: "**Android** is a separate Gradle project under
  `android/`: Kotlin, AndroidX and Media3, built by the Gradle wrapper with no Android Studio and no Node. It never links into the server. No
  Google Play services, Firebase or analytics. Other mobile platforms are later."
- **Dependencies**: the Go module rule gains an Android line. Approved set: Kotlin (through AGP), `androidx.media3` (exoplayer, session,
  datasource), `androidx.webkit`, `androidx.core`, `androidx.work` if downloads need it. Every other module needs approval. Each gets a
  `NOTICES.md` row, and all are pinned through Gradle dependency verification. The "no build step" rule stays for the shipped server; Gradle
  is allowed under `android/` only, and fetches at build time only.
- **Security and privacy**: "the only package that creates an `http.Client`" stays a server rule. Add: the Android app talks only to the
  server the person typed in, with no other outbound request. Sign-out deletes the person's downloads.
- **JavaScript**: `native-audio.js` joins `internal/web/static/js/`, loaded only in the Android shell.
- **Design**: bundled start and offline pages use the same `tokens.css` and `bundle.css`, copied at build time.
- **Tests**: add `make apk-debug` and an emulator checklist; a CI job when A10 lands, gated on the Android SDK being on the runner.
- **docs/roadmap.md**: remove "(evaluate Wails v3 mobile when stable)" from Later; add a milestone M8 Android (M7 is the dropped Research). **docs/feature-map.md**:
  the "Native mobile app" row. **docs/architecture.md**: the module table and the approvals table.

## Consequences
- Android listening becomes as reliable as any native player, and Android Auto, skip silence and widgets become possible.
- Player behaviour now has two engines, web and native; sound modes will exist twice. Accepted, with the page as a thin remote in the shell.
- About 11 packages, one of them on the server, and a Kotlin code base the Go gates do not cover.
- The operator must choose distribution before release: Google's developer verification (from 30 September 2026 in four countries, global
  in 2027) and Android Auto's handling of sideloaded apps both push toward Play; F-Droid fits the project better.
- iOS is not decided here. The server work (settings API, token handoff) would serve it.
