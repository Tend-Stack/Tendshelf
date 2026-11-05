# 0022 Sound modes
Status: proposed (2026-11-05). Direction approved by the operator; the phone spike and the budget in point 9 are still
open. Plan: `docs/plans/sound-modes.md`.

## Operator's words (2026-11-05)
Modes: Natural (default, no processing at all), Voice clarity, Even volume, Night. Stereo to mono as a separate switch.
Placed in the player's speed sheet, remembered per person. A full equalizer and skip silences are explicitly later.

## Context
The player is one `<audio id="audio">` with nothing connected to it: `player.js` sets `src`, `playbackRate`,
`preservesPitch` and `volume`, and Media Session reads that element. The only way to shape its sound in a browser is
`createMediaElementSource`, which has three properties that drive this decision:
- it cannot be undone for the life of the page;
- from then on the element makes sound only while the `AudioContext` runs, while its clock runs on regardless;
- a context can stop when a phone locks or hides the page. That is the known risk on iOS (guess until the spike ran).

So a broken mode is not a wrong sound. It is a book that keeps moving with nothing to hear, on a locked phone.

## Decision
1. **Natural means untouched.** Nothing is fetched, no `AudioContext` exists, `<audio>` is connected to nothing.
   `player.js` does not change by one byte. A share link, a device where modes are off, and everyone on Natural all
   run exactly today's code path.
2. **Built on first use, then never undone.** `sound.js` loads and the chain is built at the first tap or key press
   with a mode or mono on. `#audio` is connected only once the context reports `running`, because a suspended context
   would swallow the sound. Until then the book plays as always. So a first start from a headset or the lock screen
   after a reload is unprocessed until the next tap: the safe side.
3. **Back to Natural in the same page** is a pass-through: `#audio → mono → master → destination`, all at unity. It
   sounds the same (level measured equal, -13.5 dB both ways) but still depends on the context, so the safety checks
   of point 5 stay on. **A reload** with Natural saved is really Natural: point 1. No banner asks for a reload.
4. **One chain, native nodes only.** `source → mono (GainNode) → highpass → peaking → peaking → highshelf →
   DynamicsCompressor → trim (GainNode) → master (GainNode) → destination`, plus an `AnalyserNode` on `master`.
   No AudioWorklet (nothing here needs custom sample code, and it would be a second script path under the CSP). No
   channel merger: a node forced to one channel mixes `(L + R) / 2` by the spec's speakers rule and the destination
   copies it to both ears, which is less code and cannot clip, where a two-input merger would need its own -6 dB.
5. **A silent book is stopped, never left running.** Once a second while the book plays: a context that stays
   stopped for 3 s gets the book paused and rewound 4 s, with a plain toast. Exact zeros for 6 s before anything was
   ever heard (an engine whose tap does not work) does the same and sets `ts.sound.off` in `sessionStorage`, so a
   reload in that tab is plain instead of failing again.
6. **The phone decides what ships.** Modes do not ship until the spike has run on the operator's iPhone and Android.
   If iOS fails, modes are hidden on iPhone and iPad with one plain sentence in the sheet. An audiobook is mostly
   heard with the screen off, so a mode that stops on lock is worse than no mode. iOS is recognised by
   `tsPlayer.canSetVolume === false`, which already exists and also catches iPads that report as a Mac. The saved
   choice is kept for the person's other devices, and that phone plays Natural.
7. **One setting per person, not per device.** `playback.sound_mode` and `playback.sound_mono` in `user_settings`,
   saved like speed. Headphones do vary by device, but a per-device value needs device identity and a second lookup,
   and the operator asked for per person. Changing mode takes two taps. Bad stored values fall back to Natural.
8. **Share links have no modes.** A guest has no settings; the section is not rendered and `sound.js` never loads.
9. **Budget, for approval.** `player.js` 0 B. App JS **89,088 → 92,160 B** (90 KB); `player-full.js` row 13,568 →
   14,080 B; new `sound.js` row 2,816 B; `player-fit.css` row 1,024 → 1,280 B. Measured from drafts with comments
   intact: 91,830 B total, `player-full.js` 13,833 B, `sound.js` 2,535 B, `player-fit.css` 1,130 B.

## Mode values
Measured in Chromium (Playwright build 1248) with `OfflineAudioContext`. Speech-like test signal: 18 dB between quiet and loud stretches. The compressor adds
make-up gain that cannot be turned off (about +5, +12 and +17 dB here), so `trim` sets the final level.

| | Voice clarity | Even volume | Night |
|---|---|---|---|
| Highpass (Q 0.707) | 80 Hz | 80 Hz | 120 Hz |
| Peaking 250 Hz, Q 1 | -2 dB | 0 dB | -3 dB |
| Peaking presence | 3000 Hz, Q 0.9, +4 dB | 3000 Hz, Q 0.9, 0 dB | 2500 Hz, Q 1, +2 dB |
| Highshelf | 8000 Hz, 0 dB | 8000 Hz, 0 dB | 6000 Hz, -6 dB |
| Compressor threshold, knee, ratio | -10, 6, 12 | -32, 12, 4 | -38, 12, 8 |
| Attack, release (s) | 0.003, 0.15 | 0.008, 0.25 | 0.005, 0.3 |
| Trim | -4 dB | 0 dB | -4 dB |
| Quiet / loud out (in: -37.7 / -19.7 dB) | -35.9 / -20.3 | -27.5 / -20.7 | -31.2 / -27.1 |
| Peak out | -5.6 dB | -5.7 dB | -11.2 dB |

Why, for speech: 80 Hz sits under the lowest speaking voices, so only rumble goes, and rumble no longer drives the
compressor. The 250 Hz cut takes out boxiness. 2.5 to 4 kHz carries consonants, which is what "clearer" means. Voice
clarity's compressor is only a ceiling for that boost. Even volume's 8 ms attack keeps consonants' edges and its 250
ms release spans a syllable (about 4 a second), so it does not pump; 18 dB between stretches becomes 6.8 dB and normal
narration stays within 1 dB of Natural. Night is about 7 dB under normal narration with only 4 dB of range, softens
"s" sounds with the shelf, and drops the low end that carries through a wall. These are starting values: the ear
check in `docs/manual-checks.md` can move them, and a change there updates this table.

## What was checked, and how far it goes
Real `<audio>` through a draft of the chain in Chromium (Playwright build 1248): mono sends a left-only tone to both ears at -6 dB;
`playbackRate` 2 with `preservesPitch` keeps 440 Hz at 439 Hz and gives 879 Hz without it, so speed happens before the
graph; `audio.volume` applies before the graph (0.25 = -12 dB, 0 = silence); a new `src` keeps the graph playing.

Limits found: because volume comes first, the compressor pushes back on the sleep timer's fade in Even volume and
Night. 10 % volume drops the output 8.5 dB instead of 20 dB; it still falls steadily and 0 is silent, so this is
accepted. Lifting quiet stretches also lifts room noise: a -60 dB floor rises about 12 dB in Even volume. Nothing about
lock screens, iOS or WebKitGTK is proven by this. Those are the spike and the manual checks.

## Other players and engines
- **Desktop (WebKitGTK/GStreamer):** same origin on `http://127.0.0.1` (decision 0020) or the server's https, so no
  CORS. Whether GStreamer's tap feeds the graph is unproven (guess that it does); manual check, with point 5 as the
  net. If it fails, hide under `html[data-shell="desktop"]` as in point 6.
- **Eyes-free:** drives `tsPlayer` only and never touches `#audio`, so the chosen mode simply applies.
- **Compat copies and file changes:** the same element, a new `src`, the same origin. The graph is untouched.
- **Offline:** `sw.js` answers `/stream/` with its own same-origin `Response`, not an opaque one, so the graph is not
  fed zeros; a Chromium test proves it. `sound.js` is in the worker's precache like every asset, so it loads offline.
- **Media Session:** untouched; still the element's. Whether the lock-screen controls stay alive with a graph is what
  the spike tests.
- **CSP:** no change. `import()` of a hashed `/static/` file is `script-src 'self'`; no worklet, no blob, no inline
  style. `/stream/` is same-origin, so `crossOrigin` stays unset.

## Not chosen
- A second `<audio>` element to escape the "cannot undo": the one-element rule exists so playback survives navigation.
- Server-side filtered copies through ffmpeg, the only route that is safe on lock by construction: a whole new
  cache and job path. If iOS fails, this is the option to come back to.
- Suspending the context during a pause, to save battery: a lock-screen Play could not wake it (guess). The spike's
  pause step shows whether that matters; platforms already freeze idle pages (guess).
- Per-device values and a local override: see point 7.

## Rule text for AGENTS.md
Add under "Stack constraints", after the HTMX and JavaScript rule:

> **Sound modes ([decision 0022](docs/decisions/0022-sound-modes.md)).** `internal/web/static/js/sound.js` is the only
> module that connects `#audio` to Web Audio, and it loads only when a mode or mono is on. On Natural nothing is
> fetched and no `AudioContext` exists. No AudioWorklet. A running book with no sound must stop, never run on.
> `TestSound*` checks these.

## Spike result
Not run yet. Fill in from the table in the plan: dates, devices, versions, outcome, what was done about it.
