# Android feel (L7 skip silence, R3 volume keys and keep-awake): builder plan
Decision [0033](../decisions/0033-android-feel.md). Two packages, each green and committed on its own: **F1** server and web (Go, templ, JS),
then **F2** Android (Kotlin). F2 needs F1's page words; F1 is harmless without F2 (an app that drops the words keeps today's behaviour).

## Invariants
1. Volume keys change volume unless: the reader page is in front, `volumeKeys` is on, and the bridge is connected (all three).
2. `FLAG_KEEP_SCREEN_ON` is set only while the page holds `wake 1` and the activity is started; cleared on stop, new page, detach, sign-out.
3. Quiet skip never changes book positions: saved places, chapter math and `skipSilence()` (placeholder jump) are untouched.
4. Bridge words are fixed (`0`/`1`), parsed only in `parseBridge`; nothing new is free text or logged beyond lengths.
5. Web budget: `jsTotalGzip` unchanged (100 KB); `reader.js` unchanged; Android-only code lives in `native-feel.js`.
6. No migration, endpoint, module or `http.Client`. Settings defaults: `SkipQuiet false`, `VolumeKeys false`.

## Decisions (one line each)
- Code name "quiet skip" (`SkipQuiet`, `skipQuiet`, `quiet`): `skipSilence`/`silentRanges` already mean the missing-file jump.
- Switches render only when `shellFrom(r) == "android"`: the web cannot keep either promise (0022; no volume keys).
- Page turns reuse the reader's own key path (a synthetic `PageDown`/`PageUp` keydown on `document.body`), so `reader.js` is untouched.
- Keep-awake reuses `reader-comfort.js`'s rules through a `wakeLock` stand-in, not a second idle timer.
- `reader-comfort.js` hears input on `window` in the capture phase, so keys the reader consumes (arrows, volume turns) still reset the idle timer.
- Live apply of quiet skip through a session custom command (the `SLEEP_COMMAND` pattern), gated by `who.ownCommands`.
- Keys while audio plays still turn pages (0033 point 4). A held key turns one page (repeats swallowed).

## Names and shapes
- Go `users.Settings.SkipQuiet bool`; key `keyQuietSkip = "playback.skip_quiet"`; form field `skip_quiet`; API JSON `skipQuiet`.
- Go `users.ReaderSettings.VolumeKeys bool \`json:"volumeKeys"\``; form field `volume_keys`; reader root `data-volume-keys="true|false"`.
- `pages.ReaderLook.VolumeKeys bool`; `pages.ReadingSheetView.VolumeKeys, InApp bool`; `pages.SettingsView.SkipQuiet, InApp bool`.
- Page to app: `wake 0|1`, `pagekeys 0|1`, `quiet 0|1` (exactly two parts, second exactly `0` or `1`).
- App to page: `{"t":"pagekey","dir":"next"}` or `{"t":"pagekey","dir":"prev"}`.
- Kotlin: `BridgeMessage.Wake(on: Boolean)`, `PageKeys(on: Boolean)`, `Quiet(on: Boolean)`; `encodePageKey(next: Boolean): String`;
  `PlayerSettings.skipQuiet: Boolean = false` (last parameter, after `rewindSteps`); `PlayerRemote.setSkipQuiet(on: Boolean)`;
  `PlaybackService.QUIET_ACTION = "app.tendshelf.SKIP_QUIET"`, `QUIET_COMMAND`, bundle key `"on"`.
- Kotlin shared `Feel.kt`: `enum class PageKey { Pass, Swallow, Next, Prev }`; `const val KEY_VOLUME_UP = 24`, `KEY_VOLUME_DOWN = 25`,
  `KEY_ACTION_DOWN = 0` (the `android.view.KeyEvent` values; shared has no Android); `fun pageKeyFor(armed: Boolean, keyCode: Int, action: Int,
  repeatCount: Int): PageKey`: not armed or not a volume key → `Pass`; action not down or repeat > 0 → `Swallow`; down → `Next`; up → `Prev`.
  `class ReaderFeel` with private `keys`, `awake`, `front` (all false); `val keysArmed = keys && front`; `val screenOn = awake && front`;
  `fun keys(on)`, `fun awake(on): Boolean?`, `fun front(on): Boolean?`, `fun reset(): Boolean?` (each returns the new `screenOn` when it changed,
  else null; `reset` clears `keys` and `awake`, keeps `front`).
- Copy, exactly: Settings switch label "Skip silence", hint "Shortens long pauses in the reading. Works in this app only."; reading sheet switch
  "Turn pages with the volume buttons". No other new strings.

## F1: server and web
1. `internal/users/settings.go`: field `SkipQuiet` (comment: Android app only, decision 0033), const `keyQuietSkip`, entry
   `{keyQuietSkip, s.SkipQuiet}`, `apply` case decoding a bool (the `keyAutoRewind` pattern). Default stays false (not in `DefaultSettings`).
2. `internal/users/reader_settings.go`: field `VolumeKeys` after `AutoHide` (comment: Android app only); add `"volumeKeys": &out.VolumeKeys` to the
   bool map in `parseReader`. `Validate` unchanged.
3. `internal/web/routes_api_settings.go`: `SkipQuiet bool \`json:"skipQuiet"\`` in `apiSettings` after `NextInSeries`; set in `apiSettingsOf`;
   a `try("skipQuiet", apiSettingsBadBool, ...)` after `nextInSeries` (400 field `skipQuiet` on a non-boolean).
4. `internal/web/me_views.go`: `settingsView` copies `SkipQuiet`; `settingsFromForm` adds `flag("skip_quiet", &next.SkipQuiet)`.
5. `internal/web/routes_me.go` `settingsPage`: after `settingsView`, `v.InApp = shellFrom(r) == "android"`.
6. `internal/web/pages/settings.templ`: fields `SkipQuiet, InApp bool`; in the Playback default section's second `ts-group`, after
   `st-next`: `if v.InApp { @switchRow("st-quiet", "skip_quiet", "Skip silence", "Shortens long pauses in the reading. Works in this app only.", v.SkipQuiet) }`.
7. `internal/web/routes_reader_settings.go`: `rsetBool(form, "volume_keys", &next.VolumeKeys)` after `auto_hide`; `readerLook` sets
   `VolumeKeys: rs.VolumeKeys`; `readingSheet` sets `VolumeKeys: rs.VolumeKeys, InApp: shellFrom(r) == "android"`.
8. `internal/web/pages/reader_settings.templ`: `ReaderLook.VolumeKeys`; `ReadingSheetView.VolumeKeys, InApp`; in More, after `auto_hide`:
   `if v.InApp { @readerSwitch("volume_keys", "Turn pages with the volume buttons", v.VolumeKeys) }`; the `open?=` condition adds `|| (v.InApp && v.VolumeKeys)`.
9. `internal/web/pages/reader.templ`: after `data-auto-hide`, `data-volume-keys={ strconv.FormatBool(v.Look.VolumeKeys) }`.
10. `internal/web/static/js/reader-comfort.js`: in `take()`, `const api = window.tsNativeWake || navigator.wakeLock;` replaces both uses of
    `navigator.wakeLock` (comment: the Android app's stand-in, decision 0033). The input loop becomes
    `window.addEventListener(type, touched, { signal, capture: true })` (comment why: the reader stops keys it handles).
11. `internal/web/static/js/native-feel.js` (new; header comment in the `native-speech.js` style listing the words). Start only when
    `window.tsNative`, `data-shell="android"` and no `body.dataset.tsLink` (same guard as `native-speech.js`). Listen with
    `bridge.addEventListener("message", ...)`, never `onmessage`. Parts:
    - `window.tsNativeWake = { request(type) }`: `type !== "screen"` rejects; releases a held sentinel first; posts `wake 1`; resolves an
      `EventTarget` sentinel whose `release()` (once only) posts `wake 0` and dispatches `release`.
    - `syncKeys()`: `want = reader?.dataset.volumeKeys === "true"` where `reader = document.getElementById("reader")`; posts
      `pagekeys 1|0` only when `want` differs from the last sent (initially "none sent", so `0` is not sent at start). Called at start, on
      `htmx:after:swap`, on `visibilitychange` to visible. On `pagehide` post `pagekeys 0` if `1` was last sent.
    - `document` `change`: a checkbox named `volume_keys` sets `reader.dataset.volumeKeys = String(checked)` (when `#reader` exists) and
      calls `syncKeys()`; a checkbox named `skip_quiet` posts `quiet 1|0`.
    - message `{"t":"pagekey","dir"}`: ignored unless `#reader` exists and `dataset.volumeKeys === "true"`; else dispatches on `document.body`
      `new KeyboardEvent("keydown", { key: dir === "next" ? "PageDown" : "PageUp", bubbles: true, cancelable: true })`. Unknown `dir` ignored;
      JSON that does not parse ignored.
12. `internal/web/components/layout.templ`: in the `p.Shell == "android"` block, add `native-feel.js` after `native-speech.js`.
13. `internal/web/static/motion_test.go`: row `"native-feel.js": {1*1024 + 256, 8 * 1024}` with a comment (decision 0033, plan 1,280 B);
    add it to `jsAndroidFiles`; `jsAndroidTotalGzip = 7*1024 + 768` (7,936 B; 6,345 + about 1,100 + margin); `reader-comfort.js` row to
    `{1*1024 + 704, 8 * 1024}` (1,728 B). `jsTotalGzip` stays 100 KB (the comfort growth is about 60 B of 480 B headroom). Over a row:
    set the next 64 B, write `OVERRUN: <measured> B served, reported`, never squeeze or strip comments.
14. `make generate`; commit `feat(web): skip silence and volume key settings for the Android app (decision 0033, F1)`.

### F1 tests (new names start `TestFeel` / Go helpers `feel...` to avoid package-level collisions)
- `internal/users/settings_test.go` `TestFeelSkipQuiet`: default false; save true, load true; stored `"yes"` loads false.
- `internal/users/reader_settings_test.go` `TestFeelVolumeKeys`: default false; round trip; `{"volumeKeys":1}` parses false.
- `internal/web/api_settings_test.go` `TestFeelAPISkipQuiet`: GET has `"skipQuiet":false`; PATCH `{"skipQuiet":true}` 200 then GET true;
  PATCH `{"skipQuiet":"on"}` 400, field `skipQuiet`, message `apiSettingsBadBool`; no token 401 (the existing helper).
- `internal/web/me_test.go` `TestFeelSettingsSwitch`: `GET /me/settings/playback` with cookie `ts_shell=android`
  contains `name="skip_quiet"` and "Skip silence"; without the cookie (and with `ts_shell=desktop`) it does not; `POST /me/settings` with
  `skip_quiet=0&skip_quiet=1` saves true; a post without the key keeps it; `skip_quiet=2` is refused (400, the existing bad-form path).
- `internal/web/reader_settings_test.go` `TestFeelReaderVolumeKeys`: the sheet in the app has `name="volume_keys"` and the label, outside it does
  not; `POST /me/reader-settings volume_keys=0&volume_keys=1` saves; the reader page root has `data-volume-keys="true"`; signed out the POST saves nothing.
- `internal/web/shell_app_test.go` `TestFeelScriptOnlyInApp`: `js/native-feel.js` is in an app page and absent from a browser and a desktop page.
- `internal/web/native_feel_browser_test.go` (new, reuses `nsStub`, `nsOpen`, `nsSent`, `nsPush`; the `alVP` viewports at 390 and 1440):
  `TestNativeFeelBrowserPageKeys`: volumeKeys on → `pagekeys 1` sent; push `next` → the reader moved (the same probe the reader page tests use for
  `PageDown`); push `prev` → back; with volumeKeys off no `pagekeys 1` and a pushed `next` does nothing; a boosted link away sends `pagekeys 0`.
  `TestNativeFeelBrowserWake`: keepAwake on, `cfFakeLock` installed → `wake 1` sent and `__locks.taken` stays 0; leaving sends `wake 0`.
  `TestNativeFeelBrowserQuiet`: Settings, Playback in the app, click the switch → `quiet 1`, again → `quiet 0`.
- Existing to rerun: `TestComfortBrowserWake`, `TestComfortBrowserHide`, `TestComfortNotLoaded`, `TestNativeSpeechBrowser`, `TestReaderSettings*`, `TestA1Settings*`, `TestJSBudget`.

### F1 gates
`make generate` (tree clean after), `env -u TENDSHELF_TEST_BROWSER make check`, `make build`, and each browser test above with
`TENDSHELF_TEST_BROWSER=/home/rubirosa/.cache/ms-playwright/chromium-1248/chrome-linux64/chrome go test -tags dev -run '<names>' ./internal/web/`.
No golden or visual capture needed: the switches show only in the app; record the sheet and Settings at 390 px in the app via the existing capture
helper only if a visual test already covers the app sheet (report which).

## F2: Android
1. `android/shared/src/main/kotlin/app/tendshelf/shared/BridgeProtocol.kt`: the three messages (KDoc each); in `parseBridge`
   `"wake"`, `"pagekeys"`, `"quiet"` → `if (parts.size == 2 && (parts[1] == "0" || parts[1] == "1")) X(parts[1] == "1") else null`;
   `fun encodePageKey(next: Boolean) = "{\"t\":\"pagekey\",\"dir\":\"${if (next) "next" else "prev"}\"}"`.
2. `android/shared/src/main/kotlin/app/tendshelf/shared/Feel.kt` (new): `PageKey`, the key constants, `pageKeyFor`, `ReaderFeel` as above.
3. `android/shared/.../Manifest.kt`: `val skipQuiet: Boolean = false` last in `PlayerSettings` (positional callers keep compiling).
4. `android/app/.../Api.kt` `settings()`: pass `skipQuiet = j.optBoolean("skipQuiet", false)` (older servers lack it).
   `Offline.kt` `saveSettings` puts `skipQuiet`; `loadSettings` reads `optBoolean("skipQuiet", false)`.
5. `BookEngine.kt`: in `begin`, `exo.skipSilenceEnabled = settings.skipQuiet` before `exo.prepare()`; new `fun setSkipQuiet(on: Boolean)` (main
   thread): `exo.skipSilenceEnabled = on`, `settings = settings.copy(skipQuiet = on)`, `Offline.saveSettings(settings)`. Leave
   `onPositionDiscontinuity`'s reason filter as is and add a comment that `DISCONTINUITY_REASON_SILENCE_SKIP` is ignored on purpose.
6. `PlayerRemote.kt`: `fun setSkipQuiet(on: Boolean)` on the interface; `ControllerRemote` sends `QUIET_COMMAND` with `putBoolean("on", on)`.
   `PlaybackService.kt`: constants, `add(QUIET_COMMAND)` beside `SLEEP_COMMAND` (own commands only), `QUIET_ACTION -> engine.setSkipQuiet(args.getBoolean("on"))`.
7. `Bridge.kt`: constructor param `onKeepScreenOn: (Boolean) -> Unit` (after `onTheme`); `private val feel = ReaderFeel()`; handle `Wake` →
   `feel.awake(on)?.let(onKeepScreenOn)`, `PageKeys` → `feel.keys(on)`, `Quiet` → `r?.setSkipQuiet(on)`; `pageStarted()` and `detach()` call
   `feel.reset()?.let(onKeepScreenOn)`; `onStart`/`onStop` call `feel.front(true|false)?.let(onKeepScreenOn)`;
   `val pageKeysArmed get() = feel.keysArmed && proxy != null && flow() == ConnectFlow.State.Connected`;
   `fun pageKey(next: Boolean)`: when armed, `send(proxy, encodePageKey(next))`.
8. `MainActivity.kt`: pass `::keepScreenOn` (`window.addFlags`/`clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)`); override
   `dispatchKeyEvent`: `when (pageKeyFor(bridge.pageKeysArmed, e.keyCode, e.action, e.repeatCount))` Pass → `super`, Swallow → true,
   Next/Prev → `bridge.pageKey(...)`, true. `docs/android.md`: a "Reader feel" section after Read aloud listing the words, the key rule and the flag rule.
9. Commit `feat(android): skip silence, volume keys turn pages, screen stays on while reading (decision 0033, F2)`; roadmap status note (scope, checks, limits).

### F2 tests (`android/shared/src/test/kotlin/app/tendshelf/shared/`)
- `BridgeProtocolTest`: good `wake 0`, `wake 1`, `pagekeys 0`, `pagekeys 1`, `quiet 0`, `quiet 1`; bad `wake`, `wake 2`, `wake 01`, `wake  1`,
  `wake 1 `, `Wake 1`, `pagekeys on`, `quiet -1`, `quiet 1 1`; `encodePageKey(true|false)` exact strings.
- `FeelTest.kt` (new): `pageKeyFor` table (unarmed Pass for both keys; armed: other key Pass, down Next/Prev, repeat Swallow, up Swallow);
  `ReaderFeel`: awake before front → null then front(true) → true; front(false) → false; front(true) → true again; reset → false; same value twice → null; keysArmed needs front.
- Gates: `cd android && ./gradlew --no-daemon :shared:test`, `make apk-debug`, and `android/tools/check-android.sh` when the SDK variables are set.

### Emulator proof (only under `flock -w 3600 /home/rubirosa/Projects/Tendshelf/.local/android-emulator.lock`)
Record in the roadmap note: (a) reader with the switch on, `adb shell input keyevent KEYCODE_VOLUME_DOWN` turns a page (screenshots before/after)
and `dumpsys audio` shows STREAM_MUSIC unchanged; switch off, or on Home, the same key changes it; with a book playing, keys still turn pages.
(b) Keep the screen on: `dumpsys window` shows `KEEP_SCREEN_ON` on the activity with the setting on in the reader, not after leaving, not after
Home, back after returning. Note what `navigator.wakeLock` did in that WebView (the 0033 guess). (c) A generated tone/5 s silence/tone file plays
the gap shortened with Skip silence on, and the saved place after it matches the player position.

## Out of scope
Skip silence on the web; brightness swipe; offline reading in the app; key direction or swap settings; keys while a sheet or dialog is open
(swallowed, no action); desktop or iOS keys; a skip-silence control in the full player, notification or Android Auto; tuning Media3's silence
parameters; any change to `reader.js`, `native-audio.js` or `/api/v1` beyond `skipQuiet`; translations (the interface copy is English only today).
