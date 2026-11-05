# Sound modes (plan, 2026-11-05)

Decision [0022](../decisions/0022-sound-modes.md) holds the reasons and the mode values: read it first. Two packages in
order: **S0 spike** (about 25 tool calls), then, after the operator's result and budget word, **S1 feature** (at most
200 tool calls). No translation package: the app is English only. No board: the words below describe the sheet.

Reused: `POST /me/settings`, `settingsFromForm`, `user_settings`, the speed sheet, `.ts-option`, `.ts-switch`,
`.ts-switch-row__*`, the sheets' arrow keys, `tsPlayer.canSetVolume/pause/seek/toast/state`, the `data-ts-src` lazy
import, `fullpOpen`, `offlineApp`, `playerClickSel`. Extended: `Settings`, `PlayerMsgs`, `PlayerFull`, `player-full.js`
(`saveSpeed` splits out `saveSetting`). New: `sound.js`. Replaced: nothing. No migration, no new Go module.
Test prefix `TestSound`, helpers `snd`, captures `sound-*.png`, new files named `sound*`.

## 1. Invariants
1. Natural with nothing built: `sound.js` not requested, no `AudioContext`, `<audio>` connected to nothing.
2. `player.js` gains 0 bytes and is not edited.
3. Only `sound.js` calls `createMediaElementSource`, and only on a context whose state is `running`.
4. `#audio` is never connected twice; the graph is never torn down in the page.
5. The book never runs on in silence: see "Safety checks".
6. Media Session, `preservesPitch`, `playbackRate`, `audio.volume` and the sleep fade keep their owners; `sound.js`
   sets none of them.
7. Share links render no controls and load no sound code.
8. A saved value outside the set means Natural, on the server and in `sound.js`.

## 2. Decisions
- Mono is a `GainNode` forced to one channel, not a merger: simpler, no clipping, proven in Chromium.
- All JS for the sheet stays in `player-full.js` so the first tap can start the context before any `await`.
- The sound section sits in a closed `<details>`: the sheet looks as it does today until opened, and it is tall.
- A saved mode wakes on the first tap or key anywhere, not on Play: play buttons are many, iOS needs the tap.
- Choosing a mode leaves the sheet open, so the person hears the difference. Speed presets still close it.
- Per person, saved at once. Offline, a save fails quietly and lasts for the page, like speed.

## 3. S0 spike (before any feature code; merges to main, S1 deletes it)
Not linked into the server binary. `spike.js` sits outside `static/js` as a throwaway test tool, deleted in S1.
- `internal/testutil/soundspike/main.go`: `go run ./internal/testutil/soundspike -file <audio> [-addr 127.0.0.1:8096]`.
  Wraps its mux in `web.SecurityHeaders` (the app's real CSP). Routes: `GET /`, `GET /spike.js`, `GET
  /manifest.webmanifest`, `GET /audio/a` and `/audio/b` (both `http.ServeFile` of `-file`, so Range works), `POST /log`
  (appends to `.local/tests/soundspike/<run>/log.jsonl`). It serves only that one file. Never commit an audio file.
- `internal/testutil/soundspike/page.html` (embedded, with `apple-mobile-web-app-capable`), `spike.js` (embedded):
  - One `<audio>`, Media Session metadata and play, pause, seekbackward and seekforward handlers, as `player.js`.
  - Buttons: **Plain** (control), **With sound chain** (`new AudioContext({latencyHint:"playback"})` inside the tap,
    `resume()`, connect when running, the Night chain from 0022 with mono on), **Speed 1 / 1.5**, **Keep playing on
    silent** (sets `navigator.audioSession.type = "playback"` where it exists; off by default).
  - After 60 s of playing, `src` moves from `/audio/a` to `/audio/b` at the same place: a file boundary with no tap.
  - Every 5 s, and on `visibilitychange`, `pagehide`, `play`, `pause` and `statechange`, a `sendBeacon` to `/log` with
    time, device, mode, `visibilityState`, `ctx.state`, `audio.paused`, `audio.currentTime`, `ctx.currentTime` and
    analyser RMS. A gap in the log means the page was frozen; that is evidence too.
- Gates: `make check`, `make build`. A run in desktop Chromium over `tailscale serve`, before handing it to the operator.

Running it (the lead or operator, on the workstation; port 443 stays with Tendshelf):
`go run ./internal/testutil/soundspike -file <one of your own books>`, then
`tailscale serve --bg --https=8443 http://127.0.0.1:8096`; open
`https://rubirosa-nucbox-evo-x2.coati-rainbow.ts.net:8443`. Afterwards `tailscale serve --https=8443 off`.

| # | On each phone: Safari or Chrome tab, then installed to the Home Screen | Pass |
|---|---|---|
| 1 | With sound chain, lock the phone, listen 5 minutes. | Never stops; the book's place matches what you heard |
| 2 | While locked: pause on the lock screen, wait 1 minute, play. Then again with 5 minutes. | Sound returns at once |
| 3 | Unlocked and playing: switch to another app for 2 minutes. | Never stops |
| 4 | Take a call or set off an alarm, end it, press play on the lock screen. | Sound returns |
| 5 | Stay locked past the 60 s mark. | Plays on past the change, no silence |
| 6 | Speed 1.5 with the chain. | Normal voice, no chipmunk, no stutter |
| 7 | iPhone only: ring switch on silent, with **Keep playing on silent** off, then on. | Plays with it on at least |
| 8 | Lock-screen artwork, title and buttons with the chain. | Same as Plain |

| Result | What S1 does |
|---|---|
| All pass, iPhone and Android | Offer everywhere: `soundWorksHere` does not check `canSetVolume` |
| iPhone fails 1 to 6 or 8, tab or installed (**default expectation**) | Hide on iPhone and iPad, show the note. Keep the `canSetVolume` check |
| iPhone fails only 7 with the switch off | Set `audioSession.type = "playback"` in `sound.js`'s `apply` when wiring, then offer everywhere |
| Android fails | Also hide there: `&& !/Android/.test(navigator.userAgent)`. Same note |
| Both fail | Stop. Report to the operator; S1 would serve desktop only |

Record the result in 0022 ("Spike result"). Plain-control failures mean the spike itself is wrong: fix it, rerun.

## 4. S1 data and endpoints
- Keys: `playback.sound_mode` (JSON string: `natural`, `voice`, `even`, `night`), `playback.sound_mono` (JSON bool).
- `users.Settings`: `SoundMode string`, `SoundMono bool`. `DefaultSettings.SoundMode = "natural"`.
- `POST /me/settings` fields: `sound_mode`, `sound_mono` (`0`/`1`). `HX-Request` answers 204; a bad value 422 with the
  existing error toast, nothing saved.

## 5. S1 steps, in order
1. Delete `internal/testutil/soundspike/`.
2. `internal/users/settings.go`: add the fields, `keySoundMode`, `keySoundMono`, `validSoundMode` (accepts `""` like
   `validTextSize`), `Validate`, `entries` (`""` becomes `natural`), `apply`. Add `func (r *Repo) Sound(ctx, userID)
   (mode string, mono bool, err error)`: one query, `key IN (?, ?)`, `mode` defaults to `natural`, bad values ignored.
3. `internal/web/me_views.go` `settingsFromForm`: `flag("sound_mono", &next.SoundMono)`; `sound_mode` assigned like
   `text_size` (Validate refuses bad ones).
4. `internal/web/copy.go`: constants below, wired in `playerMsgs()`. `internal/web/sound_copy.go`: `func (s *server)
   withSound(r *http.Request, m components.PlayerMsgs) components.PlayerMsgs` sets `SoundJS = s.assets.URL("js/sound.js")`
   and the person's mode and mono from `users.Sound`; a read error is logged and means Natural.
5. `internal/web/server.go:371`: `Player: s.withSound(r, s.withEyesFree(playerMsgs()))`. `routes_link.go` stays as is,
   so `SoundJS` is empty on a link.
6. `internal/web/components/player.templ`: `PlayerMsgs` gains `SoundJS`, `SoundMode`, `SoundMono bool` and the strings;
   add `data-ts-msg-sound-lost` and `data-ts-msg-sound-failed` to the attribute map.
7. `internal/web/components/player_full.templ`, speed sheet: add class `ts-sheet--sound` to its `<section>`. After
   `.ts-player__fine`, inside `if m.SoundJS != ""`:
   - `<details class="ts-sound" data-ts-sound data-ts-src data-ts-mode data-ts-mono hidden>`.
   - `<summary class="ts-option">`: title span, then `<span class="ts-option__meta" data-ts-sound-now>`.
   - `<ul class="ts-options" role="radiogroup" aria-label={ m.SoundTitle }>`, four `button.ts-option role=radio
     aria-checked=false data-ts-sound-choice data-ts-name` in the order natural, voice, even, night. Each holds
     `.ts-switch-row__text` with `__label` and `__hint`, then the `.ts-option__check` icon, as the speed choices do.
   - `.ts-switch-row`: `.ts-switch-row__text` with an `id` on the label, then `<button type="button" role="switch"
     aria-checked="false" aria-labelledby class="ts-switch" data-ts-sound-mono>`, as `pages/downloads.templ:62`.
   - After `</details>`: `<p class="ts-sound__note" data-ts-sound-note hidden>`.
8. `internal/web/static/css/player-fit.css`, tokens only: `.ts-sheet--sound { max-height: 85vh; max-height: 85dvh;
   overflow-y: auto; }`, `.ts-sound > summary { list-style: none; }`, its `::-webkit-details-marker` hidden,
   `.ts-sound__note` (muted, 0.875rem/1.25rem, `--space-8 --space-16` padding, margin 0).
9. `internal/web/static/js/sound.js`: new; see the contract below.
10. `internal/web/static/js/player-full.js`: split `saveSetting(body, leaving)` out of `saveSpeed`; add `"sound-now",
    "sound-mono"` to the `ui` list; add a "Sound modes" section before "Sleep timer" with `soundBox = $("sound")`,
    `soundState`, `soundWorksHere`, `drawSound`, `soundWake`, `changeSound`, `startSound` as the contract below; call
    `startSound()` right after `player = window.tsPlayer` in `start()`.
11. `internal/web/static/motion_test.go`: rows and ceilings from section 8, each with the operator's date in a comment.
    Update the header comment of `player-full.js` to name sound.
12. Docs: rule text from 0022 into `AGENTS.md`; `docs/architecture.md` Web player paragraph; `docs/manual-checks.md`
    (section 9); `docs/roadmap.md` status; 0022 status to accepted; add `TestSound` to the `player:` target's `-run`.

## 6. JS contracts
**`player-full.js`.** `soundWorksHere()`: `window.AudioContext`, no `sessionStorage` `ts.sound.off` (try/catch), and
`player.canSetVolume` per the spike table. `soundWake()` is synchronous up to `import()`: return if mode is `natural`,
mono is off and no context exists; `context ||= new AudioContext({ latencyHint: "playback" })`; `context.resume()`;
`module ||= import(soundBox.dataset.tsSrc)`; then `module.then(s => s.apply(context, mode, mono))`, errors swallowed.
`startSound()`: return when `soundBox` is null; when `!soundWorksHere()` unhide the note and return; else read mode and
mono, unhide, draw, add `click` and `keydown` listeners on `window` with capture calling `soundWake`, and one `click`
listener on `soundBox`. That one sets the state, then `drawSound()`, `soundWake()` and `saveSetting("sound_mode=…&
sound_mono=0|1")`. The state must change before `soundWake`, inside the same tap.

**`sound.js`** (only export `apply` and `debug`):
- `MODES` holds 0022's table. `apply(ctx, mode, mono)`: `graph ||= build(ctx)`; `values = MODES[mode]`; if `values`,
  set them with `.value`; `setMono(mono)`; `await ctx.resume()` when not running; return if still not running; if not
  wired, set `wired`, `ctx.createMediaElementSource(audio).connect(graph.mono)` and start the check interval;
  `reroute(values ? "shaped" : "plain")`.
- `setMono`: `mono.channelCount = 1`; `mono.channelCountMode = on ? "explicit" : "max"`.
- `reroute`: the first time connect at once. On a change, `master.gain.setTargetAtTime(0, now, 0.01)`, after 40 ms
  `mono.disconnect()`, connect to `master` (plain) or the first filter (shaped), ramp back to 1.
- `debug = { graph: () => graph, wired: () => wired, route: () => route }`, for tests.

**Safety checks** (`setInterval` 1000 ms, skipped while `audio.paused`):
1. `ctx.state !== "running"`: `ctx.resume()`, count; at 3, `giveUp("tsMsgSoundLost")`. Running clears the count.
2. Until `heard`: analyser samples all exactly 0 counts; any non-zero sets `heard`. At 6: set `sessionStorage`
   `ts.sound.off` = `"1"`, stop the interval, `giveUp("tsMsgSoundFailed")`.
- `giveUp`: read `positionMs`, `tsPlayer.pause()`, `tsPlayer.seek(max(0, pos - 4000))`, `tsPlayer.toast("err", text)`.

## 7. New strings (`internal/web/copy.go`), voice rules from `BRAND.md`
`msgSoundTitle` "Sound". `msgSoundNatural` "Natural", hint `msgSoundNaturalHint` "As the book was recorded".
`msgSoundVoice` "Voice clarity", "Clearer words". `msgSoundEven` "Even volume", "Quiet and loud parts closer together".
`msgSoundNight` "Night", "Softer, for a quiet room". `msgSoundMono` "Same sound in both ears", hint "For one earbud, or
if you hear better on one side". `msgSoundLost` "The sound stopped, so the book paused. Press play to carry on."
`msgSoundFailed` "Sound modes do not work here. Reload the page to hear your book." `msgSoundNotHere` "Sound modes are
not available on this device yet." "Mono" appears nowhere on screen.

## 8. Tests and gates
Budget, from a current main of 88,615 B: `jsTotalGzip` 92,160; `player-full.js` 14,080; `sound.js` {2816, 16 * 1024};
`css/player-fit.css` 1,280. Never minify. An overrun is reported, not squeezed. Use only after the operator's approval.

Go, always run: `internal/users/settings_sound_test.go` `TestSoundSettingsDefaultNatural`, `TestSoundSettingsRoundTrip`,
`TestSoundSettingsBadStoredValueIsNatural` (table), `TestSoundRepoRead`. `internal/web/sound_test.go`
`TestSoundFormSaves`, `TestSoundFormRefusesBadValue` (422, nothing saved), `TestSoundFormNeedsCSRF`,
`TestSoundFormSignedOutRefused`, `TestSoundSheetRendered` (attributes carry the person's values),
`TestSoundLinkHasNoControls` (no `data-ts-sound`, no `sound.js` on `/s/{token}`).
`internal/web/static/sound_test.go` `TestSoundSourceRules`: only `sound.js` has `createMediaElementSource`; no
`audioWorklet`, `createScriptProcessor` or `blob:` in it; `player.js` has no `AudioContext` and does not name `sound.js`.

Chromium, `internal/web/sound_browser_test.go` (`//go:build dev`), by `make player`. `sndStereoWav(path, ms, leftHz,
rightHz, levelDb)` writes the fixture; `sndTap(tab)` adds a splitter and two analysers on `debug.graph().master`, from
`import(src)` of the same hashed URL; `sndLevels`, `sndCountContexts` (wraps the constructor before the first tap).
Thresholds are below measured values, so they hold on any machine:
- `TestSoundNaturalLeavesNoGraph`: play, seek, speed, sleep sheet: 0 contexts, no `sound.js` resource entry.
- `TestSoundBuiltOnFirstUse`: Voice clarity while playing builds once; `wired` true; a second change builds no more.
- `TestSoundSavedModeWiresOnFirstTap`: reload with Night saved: no context before a tap, wired after Play.
- `TestSoundMonoBothEars`: left-only tone: right silent with mono off, within 1 dB of left with it on.
- `TestSoundVoiceShape`: 3 kHz at least 2.5 dB over 1 kHz; 60 Hz at least 3 dB under.
- `TestSoundEvenLiftsQuiet`: -30 dB tone at least 8 dB up on Natural. `TestSoundNightSoftensTop`: 10 kHz 2 dB under 1 kHz.
- `TestSoundNoClipping`: 0 dB tone peaks at or under -1 dB in each mode.
- `TestSoundBackToNaturalPassThrough`: route `plain`, within 0.5 dB of the plain level, still running.
- `TestSoundReloadOnNaturalIsPlain`: after that, a reload: 0 contexts, no `sound.js`.
- `TestSoundSpeedKeepsPitch`: rate 2, peak bin within 10 Hz of 440. `TestSoundFadeReachesSilence`: `setFade(0)` silent,
  `setFade(0.5)` under `setFade(1)`.
- `TestSoundFileBoundary`: two files, level holds after the change. `TestSoundOfflineChunks` (`offlineApp`): downloaded
  book, server off, Night: `heard` true. `TestSoundEyesFreeKeepsMode`: mode survives opening Eyes-free.
- `TestSoundContextLostPauses`: stub `resume` to reject, `suspend()`: paused within 5 s, about 4 s back, toast.
  `TestSoundContextRecovers`: `suspend()` alone: still playing, no toast.
- `TestSoundSilentEngineFails`: stub the analyser to zeros: paused, toast, `ts.sound.off` set; reload: note shown,
  section hidden, 0 contexts.
- `TestSoundHiddenWhenNoVolume`: only if the spike hides iOS. Make `HTMLMediaElement.volume` ignore writes through
  `tab.Call("Page.addScriptToEvaluateOnNewDocument", ...)`, as iOS does: section hidden, note shown.
- `TestSoundSheetKeys`: arrows move between modes, Enter chooses, the sheet stays open, focus stays, Escape closes.
- `TestSoundTouchTargets`: every control and the switch hit area at least 44 by 44 at 390 px.
- `TestSoundSheetVisual`: open sheet, details open, at 390, 768, 1024, 1440 in `dark`, `light`, `sepia`, `contrast`:
  `sound-*.png`, seeded values; no horizontal overflow; last control reachable by scrolling at 360 by 640.

Chromium cannot prove the lock screen, iOS, Android power rules, the ring switch or GStreamer, or that a mode sounds
good. That is S0 and section 9.

Gates: `make generate` leaves the tree clean, `make check`, `make build`, `make player`, `make page-weight` (the sheet
adds HTML to every page; if a budget breaks, report it, do not raise it), `make a11y`.

## 9. `docs/manual-checks.md`: new section "Sound modes (decision 0022)"
Same form as the sections around it. Rows: (1) rerun spike steps 1 to 8 in the real app on each phone it is offered on,
with a real book, one mode and mono on, then offline too; (2) Ubuntu desktop app: each mode audible, mono, media keys,
sleep fade; (3) ear check, same chapter in each mode on earbuds and on a speaker: words clearer, no pumping, no hiss
worse than Natural, Night comfortable beside someone asleep; (4) a mode on, then Natural, then reload: plays as always;
(5) iPhone, if hidden: the note shows, the book plays, the saved mode still applies on another device.

## 10. Out of scope
Full equalizer, skip silences, a volume boost above 100 %, per-book or per-device values, a Settings page row, the
sleep fade or volume on iOS through Web Audio, server-side filtered copies, modes on share links, macOS and Windows
checks. Suspending the context during a pause, unless the spike's step 2 shows a need.

## Operator answers (2026-11-05)

- Budget approved as planned, on condition that the phone spike passes: app JS 90 KB (92,160 B), `sound.js` 2,816 B, `player-full.js` 14,080 B, `player-fit.css` 1,280 B.
- Defaults stand: a failed phone hides modes there with one plain sentence; share links get no modes.
