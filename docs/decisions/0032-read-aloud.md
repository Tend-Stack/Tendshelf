# 0032 Read aloud: the reader speaks the book with the device's own voice
Status: accepted (operator pre-approval, 2026-11-08). Plan: `docs/plans/read-aloud.md`. Takes up [0018](0018-book-reader.md) point 2
("a future voice feature uses the device's own text-to-speech"), which left it out of scope; this decision brings it in scope, as that point said.
Parents: the reader of 0018 (`reader.js`, `reader-pages.js`, the sentence spans of `reader_render.go`), offline reading
([0019](0019-offline-reading.md)), and the Android bridge of [0024](0024-android-app.md) and [0026](0026-app-sign-in.md).

## Context
A person reading an ebook sometimes wants it read to them: tired eyes, a walk, a book with no audiobook. 0018 dropped read-along because it needed
a worker and a model the owner installs. Every phone, Mac, Windows PC and most Linux desktops already carry a text-to-speech voice, and the browser
(`speechSynthesis`) or the platform (`android.speech.tts.TextToSpeech`) offers it with no download and no server work.

What exists and is reused: the sentence spans (`<span class="ts-s" data-sid>`), the place (`here`) and its save path (`moved()`, `POST /reading/{id}`),
both layouts (scroll and pages) and the next-part fetch (the `ts-reader-next` sentinel, `cross()` in `reader-pages.js`), the read-along leftovers
`.ts-s.is-now` and the `--read-wash`/`--read-word` tokens, the lazy-module pattern (`data-*-src` plus `import()`), `window.tsPlayer`, and the
Android `tsNative` message bridge with its strict grammar. Nothing here adds a parallel reader, position or player.

## Decision
1. **A "Read aloud" control** opens a small strip in the reader's dock and starts reading at the place (the sentence at the top of the view). On a
   tablet or desktop the control sits in the header beside Aa; on a phone (below 768 px) the header has no room for an eighth button, so it sits at
   the end of the dock row. The offline reader shell has it too: device speech needs no connection.
2. **Only voices on the device.** A browser voice with `localService` false (Chrome's and Edge's online voices) or an Android voice that needs a
   network connection is never offered and never used; every utterance names its voice explicitly, so a remote default is never picked up. The
   control is hidden while the device has no such voice. Book text never leaves the device.
3. **Sentence by sentence.** One utterance per sentence (a sentence longer than 3,000 characters is split at a space), so Chromium's long-utterance
   cut-off never applies. The spoken sentence is marked with `.ts-s.is-now` (wash plus underline, never colour alone) and kept in view by the
   reader's own `show()`, which jumps and never animates. Reading continues into the next part and chapter; in pages it turns the page.
4. **The place is saved the way reading saves it**: when the view follows the voice, the reader's `moved()` runs, so the saved place is the top of
   the view, counted as reading. When the person moves the view away, following stops and "Back to the voice" brings it back.
5. **Voice and speed belong to the device** (`localStorage` keys `ts.aloud.voice`, `ts.aloud.rate`), not to `reader.v1`, because voices differ per
   device. Voices for the book's language come first. Speeds 0.8, 1, 1.2, 1.5, 1.75 and 2.
6. **One sound at a time.** Starting speech pauses a playing audiobook; the audiobook starting stops speech. Leaving the reader stops speech.
7. **The browser engine first (R1), the Android bridge second (R2).** Android System WebView has no usable `speechSynthesis`, so the app speaks
   through `android.speech.tts.TextToSpeech` (platform API, no new dependency). The page reaches it through three new bridge verbs. One of them,
   `say`, is the first page-to-app message that carries free text: the sentence. It has its own length and character rules, never reaches a log,
   and only ever goes to the speech engine.
8. **In the app, speech runs while the app is in front.** When the app goes to the background or the screen turns off, speech stops and the place
   stays where it was.

## Invariants
- No outbound request is added: no online voice, no new `http.Client`, no new server endpoint, table or setting.
- The speech module loads only when the control is first pressed; `reader.js` grows by at most 256 gzip bytes.
- At most one utterance is pending; leaving the reader leaves nothing speaking.
- No inline script or style, no `hx-on`, no CSP change. A fake speech engine exists only in `_test.go` files.
- The `say` text is checked (length, no control characters, no unpaired surrogates) before it is used, and is never logged or echoed.

## Not handled, and why
- Word-by-word marking (`onboundary`): unreliable across engines and voices; the sentence mark carries the place.
- Speech with the app in the background or the screen off: needs a speech player inside `PlaybackService`, audio focus, notification controls and
  a native sentence queue that fetches parts by itself. A package of its own if people ask.
- Starting from a tapped sentence, pitch, a sleep timer for speech, and reading footnote markers differently: later, if asked.
- Online voices, server-side speech and downloading voices: against decision 2 and "never downloads models".
- Desktop app (WebKitGTK): no special code; when WebKitGTK offers no local voice the control stays hidden. A manual check.
- Audio focus against other apps' music on Android.

## Changes to AGENTS.md (applied by the plan, R1 step 14)
In "Security and privacy", after the bullet "No telemetry ...", add: "Read aloud uses only voices on the device (a browser voice with `localService`
true, an Android voice that needs no network), so book text never leaves it (decision 0032)." In "templ and HTMX conventions", in the list of
plain JavaScript, add "the read-aloud strip (`reader-aloud.js`, loaded on its first use)".
