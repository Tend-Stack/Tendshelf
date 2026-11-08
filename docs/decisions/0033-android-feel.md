# 0033 Android feel: skip silence, volume keys turn pages, the screen stays on while reading
Status: accepted (operator pre-approval, 2026-11-08). Plan: `docs/plans/android-feel.md`. Rows L7 and R3 of `docs/plans/next-stage.md`
(R3's brightness swipe and offline reading in the app are not part of this decision). Parents: the Android app ([0024](0024-android-app.md)),
its bridge ([0026](0026-app-sign-in.md), [0032](0032-read-aloud.md)), sound modes ([0022](0022-sound-modes.md)) and reader comfort (G5).

## Context
Three things a phone app does that a web page cannot do reliably. Media3 has silence skipping built in (`ExoPlayer.setSkipSilenceEnabled`); the web
would need sample code, which 0022 ruled out. Reading apps let the volume buttons turn pages; a page never sees those keys. The reader's "Keep the
screen on" uses `navigator.wakeLock`, which Android System WebView does not grant (guess, checked in the emulator proof); the window flag
`FLAG_KEEP_SCREEN_ON` always works.

What exists and is reused: `users.Settings` and `GET/PATCH /api/v1/settings` (the `autoRewind` boolean pattern), `reader.v1` with `KeepAwake`,
`reader-comfort.js` (when to hold the screen: setting on, page visible, 10 minutes of idle), the reader's own key path (`PageDown`/`PageUp` in
`reader.js`), the `tsNative` bridge with its strict grammar, the session's custom commands, and `data-shell="android"` with its Android-only scripts.
No migration: `user_settings` is JSON rows.

## Decision
1. **Skip silence, in the app only.** A per-person setting `playback.skip_quiet`, off by default, shown under Settings, Playback only inside the
   Android app. The web keeps 0022's deferral; a switch on the web would promise what the page cannot do. The app applies it at every book start
   (from `/api/v1/settings`, cached for downloaded books) and at once when the switch changes (bridge word `quiet`). In code it is "quiet skip",
   because `skipSilence()`/`silentRanges()` already name the jump over missing files.
2. **Positions stay book time.** Media3 skips inside the media clock, so saved places, chapters and the sleep timer's chapter end are unchanged;
   `listenedMs` is wall-clock listening, so it honestly counts less. The jump over placeholder silence keeps working: its listener ignores
   `DISCONTINUITY_REASON_SILENCE_SKIP`, and Media3 shortening a placeholder before that jump is harmless.
3. **Volume keys turn pages, opt-in.** A reader setting `volumeKeys` (default off), shown in the reading sheet's More only inside the app. Volume
   down is the next page, volume up the previous one; a held key turns one page. The keys are taken only while the reader page is in front, the
   setting is on, and the bridge is connected; everywhere else, with the screen off, or on the lock screen, they change the volume.
4. **When a book is playing, the keys still turn pages.** The person chose it for this screen; a key whose meaning changes with playback is
   unpredictable, and a turn that sometimes jumps the volume is worse. Volume stays reachable in the notification, quick settings and every other
   screen, and the lock screen keeps the keys for volume.
5. **The screen stays on through the window flag.** In the app, `native-feel.js` gives `reader-comfort.js` a stand-in for `navigator.wakeLock`
   (`window.tsNativeWake`) that sends `wake 1`/`wake 0`, so the existing rules (setting, idle, leaving) decide, with no second copy of them. The app
   sets `FLAG_KEEP_SCREEN_ON` only while the page holds it and the app is in front; it clears it when the app goes to the background, a new page
   loads, or the bridge detaches, and sets it again on return if the page still holds it.
6. **Three new fixed bridge words and one answer.** `wake 0|1`, `pagekeys 0|1`, `quiet 0|1`; the app answers `{"t":"pagekey","dir":"next|prev"}`.
   No free text. An older app drops them: nothing breaks, the screen is simply not held.

## Invariants
- Volume keys change volume whenever the reader is not in front, the setting is off, or the screen is off.
- `FLAG_KEEP_SCREEN_ON` is never set while the app is in the background or after the page that asked for it is gone.
- No new endpoint, table, migration, module or outbound request; the browser's web total grows by at most 96 gzip bytes.
- Book positions and saved places never depend on whether quiet skip is on.

## Not handled, and why
- Skip silence on the web: 0022.
- Choosing the key direction, keys while a sheet is open (they are swallowed and do nothing), keys in the desktop app: later, if asked.
- Brightness swipe and offline reading in the app (the rest of R3): their own packages.
- Showing skip silence in the full player or the notification, a per-book setting, the strength of the skipping: Media3's defaults, later if asked.

## Changes to AGENTS.md
None. `docs/android.md` gains the three bridge words (plan step F2.8).
