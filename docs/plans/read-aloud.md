# Plan: Read aloud (decision 0032)
Two builder packages, in order: **R1** web reader (browser `speechSynthesis`), then **R2** Android bridge (needs R1 merged). Read
`docs/decisions/0032-read-aloud.md` first; it is binding. A gap is reported as `PLAN GAP: <one line>` after committing what is green.
Reused: sentence spans (`reader_render.go:204`), `here`/`moved()`/`save()` and both flows (`reader.js`), `cross()` (`reader-pages.js`), the sentinel
`ts-reader-next`, `.ts-s.is-now` and `--read-wash`/`--read-word`, `ts-reader-look` (re-lay the reader), `window.tsPlayer`, `tsNative`. Replaced: the
dead `data-hl`/`.ts-w` read-along rules in `bundle.css`. Nothing else changes.

## R1 web reader

### Invariants
1. Only voices with `localService === true` are listed or used; every utterance sets `utterance.voice` to one of them (never null).
2. No server endpoint, table or `reader.v1` field is added. The place moves only through `reader.js` `moved()`.
3. One utterance pending at most: `speechSynthesis.cancel()` before every `speak()`; every handler checks its utterance number and ignores stale ones.
4. Audiobook and speech never sound together; leaving the reader (root disconnected, `pagehide`) cancels speech.
5. `reader.js` grows by at most 256 gzip bytes; `reader-aloud.js` is imported only on the first press of the control.
6. No inline script/style, no `hx-on`, no smooth scrolling; every control 44 x 44 px, labelled, keyboard reachable; nothing by colour alone.
7. The fake speech engine lives only in `internal/web/aloud_browser_test.go`.

### Decisions
- Control placement: header before Aa at >= 768 px; dock row end below 768 px (8 header buttons overflow 390 px: 8x44 + gaps + padding = 412).
  Two server-rendered buttons, one shown per width by CSS. Reason: one-handed reach, no overflow.
- Visibility check lives in `reader.js` (about 10 lines); everything else in the lazy module. Cheapest way that needs no extra request per page.
- Pause = `cancel()` and remember the sentence; Play speaks that sentence again from its start. Reason: `speechSynthesis.pause()` is unreliable.
- Engine seam: `reader-aloud.js` uses `window.tsNativeSpeech || browserEngine()`; R2 provides `tsNativeSpeech`, so R2 does not touch this module.
- Keys: `j` back one sentence, `k` forward one sentence, only while the strip is open (handled in `reader-aloud.js`; no clash: reader.js uses
  arrows, PageUp/Down, Space, `[`, `]`, `t`, `/`). No key opens it (that would grow reader.js); the control is in the Tab order.
- Following: before each sentence, if the sentence just finished was on screen, follow (call `kit.follow` when the new one is not fully on screen);
  else stop following and show "Back to the voice". Rule needs no scroll bookkeeping.

### Engine interface (comment it at the top of `reader-aloud.js`; R2 implements the same)
`voices() -> [{id, name, lang, isDefault}]` (local only) · `speak(text, {voiceId, rate}, {start, end, fail(code), stopped})` · `cancel()`.
`stopped` means the engine stopped by itself (R2: the app went to the background); the module shows Paused, as the Pause button does.
Browser: `id` = `voiceURI`; listen with `addEventListener("start"|"end"|"error")` (the fake relies on it); error `interrupted`/`canceled` -> nothing,
`not-allowed` -> `fail("blocked")`, anything else -> `fail("voice")`.

### Steps
1. `internal/web/reader_copy.go`, NEW block "Read aloud (decision 0032)", exact words:
   `readerAloud = "Read aloud"`, `readerAloudPlay = "Play"`, `readerAloudPause = "Pause"`, `readerAloudBack = "Back one sentence"`,
   `readerAloudFwd = "Forward one sentence"`, `readerAloudStop = "Stop reading aloud"`, `readerAloudMore = "Voice and speed"`,
   `readerAloudVoice = "Voice"`, `readerAloudDefault = "Default voice"`, `readerAloudOther = "Other languages"`, `readerAloudSpeed = "Speed"`,
   `readerAloudFollow = "Back to the voice"` (BRAND), `readerAloudEnd = "You have reached the end of the book."`,
   `readerAloudNoPart = "Couldn't load the next part, so reading aloud stopped here. Press Play to try again."`,
   `readerAloudFailed = "The voice on this device stopped. Press Play to try again."`, `readerAloudBlocked = "Press Play to start reading aloud."`.
   Speed option text reuses `msgSpeedValue` (`"{v}×"`: "0.8×", "1×", "1.75×"), values `0.8 1 1.2 1.5 1.75 2`.
2. `internal/web/pages/reader.templ`: `ReaderView` gains `Aloud *ReaderAloudView`; new struct `ReaderAloudView{Src, Label, Play, Pause, Back, Fwd, Stop,
   More, Voice, Default, Other, Speed, Follow string; Speeds []ReaderAloudSpeed{Value, Text string}; Copy string}`. Render when `v.Aloud != nil`:
   - header, before the Aa link: `<button type="button" class="ts-iconbtn ts-aloud-open ts-aloud-open--bar" data-aloud-src={Src}
     aria-controls="reader-aloud" aria-expanded="false" aria-label={Label} hidden>@components.Icon("aloud", false)</button>`;
   - `.ts-readdock__row`, after `#reader-left`: the same button with `ts-aloud-open--dock`;
   - first child of `footer#reader-dock`: `<div id="reader-aloud" class="ts-aloud" role="group" aria-label={Label} data-copy={Copy} hidden>` with
     row 1: buttons `data-aloud="back"` (icon `skip-back`, label Back), `data-aloud="play"` (icon `pause`, label Pause; JS swaps to `play`/Play),
     `data-aloud="fwd"` (icon `skip-forward`, label Fwd), `<button data-aloud="follow" class="ts-btn ts-btn--quiet" hidden>{Follow}</button>`,
     `data-aloud="stop"` (icon `close`, label Stop); `<p class="ts-aloud__status" role="status"></p>`; row 2 `<details class="ts-aloud__more">
     <summary>{More}</summary>` with `<label for="aloud-voice">{Voice}</label><select id="aloud-voice" class="ts-select">` (empty; JS fills) and
     `<label for="aloud-rate">{Speed}</label><select id="aloud-rate" class="ts-select">` with the six speeds, `1` selected.
3. `internal/web/routes_reader.go` `readerView`: set `v.Aloud` for every reader page (`Src: s.assetOr("js/reader-aloud.js")`, words from step 1,
   `Copy`: JSON `{"end","noPart","failed","blocked","play","pause","default","other"}`). `routes_offline_read.go:159` keeps it (do not clear it).
4. `internal/web/components/iconset.go`: add `"aloud"`: `<path d="M4 5.5h10a1.5 1.5 0 0 1 1.5 1.5v6a1.5 1.5 0 0 1-1.5 1.5H9l-3.5 3v-3H4A1.5
   1.5 0 0 1 2.5 13V7A1.5 1.5 0 0 1 4 5.5z"/><path d="M18.5 8.5a4 4 0 0 1 0 5M21 6a7.5 7.5 0 0 1 0 10"/>` (speech bubble with sound; NEW icon,
   listed in the report). Update any name list in `icons_test.go`.
5. `internal/web/static/js/reader.js`, after the skim block, a section `// ---- Read aloud (decision 0032) ----`:
   `const aloud = shell.querySelectorAll("[data-aloud-src]")`; `voiced = () => { const n = window.tsNativeSpeech ? tsNativeSpeech.voices().length :
   (window.speechSynthesis?.getVoices().filter((v) => v.localService).length || 0); for (const b of aloud) b.hidden = !n; }`; call it, `on(speechSynthesis,
   "voiceschanged", voiced)` when present, `window.tsNativeSpeech?.ask()`, `on(document, "ts:native:voices", voiced)`; on click of either button:
   `loading ??= import(src).then((m) => m.aloud(root, shell, kit)); loading.then((api) => api.toggle(button))`. `kit = { place: () => here,
   flow: () => flow, turn, follow(sid) { const el = sentence(sid); if (el && view.show(sid, Number(el.dataset.o))) moved(); } }`. Nothing else.
6. New `internal/web/static/js/reader-aloud.js` (header comment: what, decision 0032, the engine seam), export `aloud(root, shell, kit)` returning
   `{ toggle(button) }`. Behaviour:
   - `toggle`: closed -> show strip, both buttons `aria-expanded="true"`, dispatch `ts-reader-look` on `document` (re-lays the reader), focus Pause,
     `start()`; open -> `stop()`, hide strip, `aria-expanded="false"`, `ts-reader-look`, focus the button that was pressed.
   - `start()`: `window.tsPlayer?.state().playing && window.tsPlayer.pause()`; sentence = `.ts-s[data-sid=kit.place().sid]` or the first `.ts-s`.
   - `speakAt(el)`: move `is-now` to `el`; follow rule (Decisions); text = `el.textContent` with whitespace collapsed and trimmed; empty -> next;
     longer than 3,000 chars -> chunks split at the last space before 3,000, spoken in turn; `end` of the last chunk -> next sentence.
   - next sentence: the `.ts-s` after `el` in `root` document order. None: if `el`'s `section` has `data-next-href`: pages -> `await kit.turn(1)`
     until `el.isConnected` is false (at most 64 tries, 300 ms apart), then the first `.ts-s` in `root`; scroll -> `kit.follow(el.dataset.sid)`
     and wait for `htmx:after:swap` inside `root` (15 s timeout -> status `noPart`, paused). No next part -> status `end`, paused.
   - back / fwd: previous / next `.ts-s` in DOM (back stops at the first one in the DOM; no fetch backwards); cancel and speak it.
   - play/pause button: label and icon swap; paused keeps `is-now`; `stop()` removes it, cancels, clears status.
   - `fail("blocked")` -> status `blocked`, paused; `fail("voice")` -> status `failed`, paused.
   - voices: fill `#aloud-voice`: first option "Default voice" (value ""), then voices whose `lang` primary subtag equals `root.lang`'s, then
     `<optgroup label="Other languages">` with the rest; each sorted by name; option text = `name` (as text). "" resolves to the voice with
     `isDefault`, else the first of the book's language, else the first. Refill on `voiceschanged` / `ts:native:voices`.
   - storage: `ts.aloud.voice` (id), `ts.aloud.rate` (string), read and written inside `try/catch`; an unknown stored id falls back to "".
     A change applies from the next sentence (cancel and re-speak the current one).
   - player: on `ts:player:state` with `event.detail.playing` true -> pause. Leaving: on `htmx:after:swap` if `!root.isConnected` -> stop and
     remove listeners (one AbortController); on `pagehide` -> cancel.
   - keys: `j`/`k` (see Decisions), same guards as reader.js (no modifier, not in a field/dialog/open sheet); `preventDefault`.
7. `internal/web/static/css/bundle.css`: replace the three `.ts-reader[data-hl=...]` rules and the three `.ts-w` rules with one:
   `.ts-s.is-now { background: var(--read-wash); box-shadow: 0 0 0 0.12em var(--read-wash); text-decoration: underline; text-decoration-color:
   var(--read-word); text-decoration-thickness: 0.09375rem; text-underline-offset: 0.3em; }` (wash plus underline: never colour alone).
8. `internal/web/static/css/pages-reader.css`: `.ts-aloud` (flex column, gap `--space-8`, padding-bottom `--space-8`), `.ts-aloud__row` (flex,
   centred, gap `--space-8`), `.ts-aloud__status` (`caption`, `--ink-soft`, min-height 0), `.ts-aloud__more` (summary is a 44 px target);
   `.ts-aloud-open--bar` `display:none` below 768 px, `.ts-aloud-open--dock` `display:none` from 768 px (the `hidden` attribute still wins);
   `.ts-aloud-open[aria-expanded="true"]` colour `--accent`. Tokens only.
9. `internal/web/static/motion_test.go`: row `"reader-aloud.js": {3*1024 + 512, 24 * 1024}` (3.5 KB, comment "decision 0032, operator
   pre-approved 2026-11-08"); `reader.js` row to `{6*1024 + 608, 32 * 1024}` (+256 B, comment); `jsTotalGzip` to `99 * 1024` (from 97,280; measured
   97,110 before R1); `css/pages-reader.css` row to `{5*1024 + 128, 32 * 1024}` (measured 4,767 before). Report measured numbers; never squeeze.
10. Tests (below). 11. `docs/reader.md`: a "Read aloud" section (what it does, local voices only, keys j/k, limits). 12. `docs/vision.md:66`: add
   "The reader can read a book aloud with the device's own voice (decision 0032)." 13. `docs/decisions/0018-book-reader.md` point 2: append
   " Taken up by [0032](0032-read-aloud.md)." 14. `AGENTS.md`: the two edits in decision 0032. 15. `docs/roadmap.md` status note.

### Acceptance tests (R1)
- `internal/web/reader_test.go` `TestReaderAloudMarkup`: both buttons with `data-aloud-src` = the hashed `reader-aloud.js`, `hidden`,
  `aria-controls="reader-aloud"`, `aria-expanded="false"`, `aria-label="Read aloud"`; `#reader-aloud` hidden with five labelled buttons and the
  two selects with labels; six speed options; same on `/offline/books/{id}/read`; for owner, member and contributor alike (it adds no right).
- `internal/web/aloud_browser_test.go` (skips without `TENDSHELF_TEST_BROWSER`; `chromium(t)`, `rdrSeed`). Seam: Go func
  `fakeSpeech(voicesJSON string, auto bool) string` returns a script installed with `tab.Call("Page.addScriptToEvaluateOnNewDocument")` before
  `Navigate`: `class FakeUtterance extends EventTarget {constructor(text){super(); this.text = text; this.rate = 1; this.voice = null}}`;
  `window.SpeechSynthesisUtterance = FakeUtterance`; `Object.defineProperty(window, "speechSynthesis", {configurable: true, value: fake})` where
  `fake` extends EventTarget with `getVoices()`, `speak(u)` (push `{text, rate, voice: u.voice?.voiceURI}` to `window.__tts.log`; when auto,
  `setTimeout` start then end), `cancel()` (count, error `interrupted` on the current one), and `window.__tts = {log, cancels, end(), fail(code),
  setVoices(list)}` (`setVoices` dispatches `voiceschanged`). Voices: `{voiceURI, name, lang, localService, default}`. Cases, at desktop-1440
  (scroll) and mobile-390 (pages):
  1. hidden with no voices; hidden with only `localService:false` voices; shown after `setVoices` adds a local one; remote never in `#aloud-voice`.
  2. press -> first log entry is the text of the sentence `kit.place()` gave (`#reader` `data-at`); that span has `is-now`; voice is set.
  3. auto mode reads past the end of chapter 2 into chapter 3; in pages the section changed and the page turned; 3.5 s later `GET` the save URL:
     its `sid` is inside `#reader-ch-3*`.
  4. manual mode: Fwd -> next sentence text; Back -> previous; Pause -> `cancels` grew, label "Play"; Play -> same sentence again; `j`/`k` keys.
  5. a stubbed playing `window.tsPlayer` is paused on start; dispatching `ts:player:state` `{playing:true}` pauses speech.
  6. speed 1.5 and a second voice -> next entry has rate 1.5 and that voice; `localStorage` holds both; after reload the selects show them.
  7. the Close reader link (boosted) -> `cancels` grew and the log stops growing.
  8. `fail("not-allowed")` -> status "Press Play to start reading aloud."; end of book -> "You have reached the end of the book."
  9. layout: every strip control >= 44 x 44; `.ts-readerbar` and `.ts-readdock__row` `scrollWidth <= clientWidth` at 390; only the dock button
     shows at 390, only the bar button at 1440.
- `internal/web/a11y_pages_test.go` `a11yStates`: `reader-aloud` (owner, reader fixture, `ch=2`; Prepare installs `fakeSpeech` and navigates
  again, presses the control, waits for `#reader-aloud:not([hidden])`) and `reader-aloud-more` (same plus `details` open). Contrast must pass on
  the marked sentence in all four themes.
- Visual: `TestAloudVisual` in the same file (pattern `TestSkimVisual`), strip open with a marked sentence, at 390, 768, 1024, 1440, dark and
  light; fixed fake voice names, no random values. No board exists: compare against BRAND "Reader" and the dock; say so in the report.
- Gates: `make check`, `make build`, `go test ./internal/web/... -run 'Aloud|ReaderAloud|JSBudget|CSSBudget|A11y'` with the browser set.
- Manual (report): Chrome and Firefox desktop with real voices; desktop app (WebKitGTK) shows or hides the control correctly.

### Out of scope (R1)
Word marking, a key to open the strip, starting from a tapped sentence, pitch, speech sleep timer, reading backwards across parts, the Android
engine (R2).

## R2 Android bridge (after R1 is merged)

### Invariants
1. Only voices with `isNetworkConnectionRequired == false` and without `KEY_FEATURE_NOT_INSTALLED` are sent or used.
2. `say` text: 1 to 4,000 UTF-16 units, no code point < 0x20 or 0x7f to 0x9f, no unpaired surrogate; never logged (log the length only), never echoed.
3. Native -> page JSON is built only by `encode*` functions from fixed fields; voice ids pass `validId`, languages `[A-Za-z0-9-]{2,35}`.
4. Speech stops on `Bridge.onStop()`, `pageStarted()` and `detach()`; `TextToSpeech.shutdown()` in `MainActivity.onDestroy`.
5. No new Gradle dependency; an old app with a new server just shows no control (unknown verbs are dropped, no voices arrive).

### Grammar (protocol version 1, additive)
Page -> app: `tts voices` · `tts stop` · `say <n> <rate> <voiceId> <text>`: `n` 1 to 9 digits, no leading zero; `rate` the existing `speed()` form
(`d.dd`, 0.50 to 3.00); `voiceId` `validId`; `text` the rest after the fourth space. `parseBridge` checks `raw.startsWith("say ")` first and sends it
to `parseSay` with `SAY_MAX_LENGTH = 4200`; every other message keeps today's 128-ASCII rule. App -> page: `{"t":"voices","list":[{"id":..,
"lang":..,"def":true|false}]}` (at most 200, empty when the engine failed) and `{"t":"tts","n":<n>,"ev":"start"|"end"|"error"|"stopped"}`.

### Steps
1. `android/shared/.../BridgeProtocol.kt`: `BridgeMessage.TtsVoices`, `TtsStop`, `Say(n: Int, rate: Double, voiceId: String, text: String)`;
   `parseSay`; `encodeVoices(list: List<VoiceInfo>)`, `encodeTts(n: Int, ev: String)` (unknown `ev` -> `error`, `n` coerced 0..999,999,999).
2. New `android/shared/.../SpeechVoices.kt`: `data class VoiceInfo(id, lang, network, notInstalled, isDefault)`; pure `usableVoices(all,
   defaultId): List<VoiceInfo>` (filters invariant 1 and 3, sorts by lang then id, caps 200, flags default).
3. New `android/app/.../Speech.kt`: owns one lazily created `TextToSpeech` (`AudioAttributes` `USAGE_MEDIA`, `CONTENT_TYPE_SPEECH`); `voices(reply)`
   (queued until `onInit`; failure -> empty list); `say(msg, reply)` (unknown voice -> `error`; `setVoice`, `setSpeechRate`, `speak(text,
   QUEUE_FLUSH, null, "u$n")`); `UtteranceProgressListener` posts `encodeTts` on the main thread; `onStop(_, interrupted)` sends nothing for a
   flush, `stopped` for `stop()`; `stop()`, `shutdown()`. Keeps the reply proxy of the last speech message; drops it on `pageStarted`/`detach`.
4. `android/app/.../Bridge.kt` `handle`: dispatch the three messages to `Speech`; `onStop`, `pageStarted`, `detach` call `speech.stop()`.
   `MainActivity.kt` creates `Speech`, passes it to `Bridge`, calls `shutdown()` in `onDestroy`.
5. `android/app/src/main/AndroidManifest.xml`: `<queries><intent><action android:name="android.intent.action.TTS_SERVICE"/></intent></queries>`
   (targetSdk 36 needs it to bind the engine).
6. New `internal/web/static/js/native-speech.js` (Android-only; loaded in `components/layout.templ` beside `native-downloads.js` when
   `p.Shell == "android"`): with `window.tsNative` and `data-shell="android"`, defines `window.tsNativeSpeech` with R1's engine interface plus
   `ask()` (sends `tts voices` once per page). Voice `name` = `Intl.DisplayNames([document.documentElement.lang || navigator.language],
   {type: "language"}).of(lang)` plus " 2", " 3" for further voices of one language. `speak` sends `say` with a counter and the text with
   whitespace collapsed; `cancel` sends `tts stop`; events for the current `n` call `start`/`end`/`fail("voice")`/`stopped`, others are
   ignored. On `voices` it stores the list and dispatches `ts:native:voices`. Listens with `tsNative.addEventListener("message")` (never sets `onmessage`).
7. `internal/web/static/motion_test.go`: `"native-speech.js": {1*1024 + 256, 8 * 1024}`, add it to `jsAndroidFiles`, `jsAndroidTotalGzip` to
   `6 * 1024` (from 4,608; measured 4,592 before). `docs/android.md`: the three verbs, the two events, the free-text rule. Roadmap status note.

### Acceptance tests (R2)
- `BridgeProtocolTest.kt`: accepts `say 1 1.00 en-us-x-sfg-local Hello, world.`, Unicode text (`Ça va? “Oui.” 日本語`), text of exactly 4,000;
  rejects 4,001, `\n`, `\u0085`, `\uD800` alone, empty text (`say 1 1.00 v1` and `say 1 1.00 v1 `), double space in the head, `n` `0` or `01` or 10
  digits, rate `1.03`, voice `-` or `a/b`, `SAY ...`, `tts`, `tts foo`, `tts voices x`; a non-`say` message with non-ASCII is still refused;
  `encodeTts` and `encodeVoices` shapes, unknown `ev`, bad ids and langs dropped. `SpeechVoicesTest.kt`: network, not-installed, cap, default.
- `internal/web/native_speech_browser_test.go` (pattern `native_audio_browser_test.go` and its `tsNative` stub): on a reader page in the android
  shell the stub receives `tts voices`; replying with two voices shows the dock button; press -> `say 1 1.00 <first book-language id> <sentence
  text>`; reply `{"t":"tts","n":1,"ev":"end"}` -> `say 2 ...` with the next sentence; `stopped` -> label "Play"; empty list -> stays hidden; no
  `say` ever names an id that was not listed.
- Gates: `make check`, `make build`, `android/tools/check-android.sh` (`:shared:test`, `lintRelease`), `make apk-debug`.
- Emulator checklist (roadmap note): speaks with an installed voice; pauses a playing audiobook; screen off stops speech, place kept; voice list
  equals Settings > Text-to-speech local voices; no network voice when the phone is offline.

### Out of scope (R2)
Speech with the screen off or the app in the background (decision 0032 "Not handled"), audio focus, notification controls for speech, a native
sentence queue, choosing the TTS engine.
