// native-speech.js is loaded only inside the Tendshelf Android app's window (data-shell="android").
//
// Read aloud (decision 0032) in the app: Android System WebView has no usable speechSynthesis, so the app speaks through
// the phone's own text-to-speech engine and this file is the engine reader-aloud.js expects (window.tsNativeSpeech):
//   voices()                            -> [{ id, name, lang, isDefault }]   what the app listed, voices on the phone only
//   speak(text, { voiceId, rate }, ev)  ev = { start(), end(), fail(code), stopped() }
//   cancel(), ask()                     ask() sends "tts voices" once per page
// The words sent: "tts voices", "tts stop" and "say <n> <rate> <voiceId> <text>". The text is one sentence, checked by the
// app (1 to 4,000 characters, no control characters). The app answers with
//   {"t":"voices","list":[{"id","lang","def"}]} and {"t":"tts","n":1,"ev":"start|end|error|stopped"}.
// Like native-downloads.js it listens with addEventListener and never takes over onmessage from native-audio.js.

const native = window.tsNative;

if (native && document.documentElement.dataset.shell === "android" && !document.body.dataset.tsLink) {
  start(native);
}

function start(bridge) {
  let list = [];
  let asked = false;
  let counter = 0;
  let current = null; // { n, ev } of the sentence being spoken; events of any other number are ignored

  // "English (United States)", then " 2", " 3" for the further voices of one language.
  function named(voices) {
    const display = new Intl.DisplayNames([document.documentElement.lang || navigator.language], { type: "language" });
    const seen = {};
    return voices.map((voice) => {
      let label = voice.lang;
      try {
        label = display.of(voice.lang) || voice.lang;
      } catch {
        // An odd tag: the tag itself is the name.
      }
      seen[voice.lang] = (seen[voice.lang] || 0) + 1;
      return { id: voice.id, name: seen[voice.lang] > 1 ? label + " " + seen[voice.lang] : label, lang: voice.lang, isDefault: voice.def === true };
    });
  }

  // The app takes steps of 0.05 between 0.50 and 3.00, written d.dd.
  const rateWord = (rate) => (Math.min(3, Math.max(0.5, Math.round(rate * 20) / 20))).toFixed(2);

  // One line of text with no control characters and no lone surrogates, which the app would refuse.
  function plain(text) {
    const cleaned = text.replace(/[\u0000-\u001f\u007f-\u009f]+/g, " ").replace(/\s+/g, " ").trim();
    return cleaned.toWellFormed ? cleaned.toWellFormed().replace(/�/g, "") : cleaned;
  }

  window.tsNativeSpeech = {
    voices: () => list,
    ask() {
      if (asked) return;
      asked = true;
      bridge.postMessage("tts voices");
    },
    speak(text, { voiceId, rate }, ev) {
      const words = plain(text);
      if (!words || !voiceId || !list.some((voice) => voice.id === voiceId)) {
        // Nothing to say, or a voice the app did not list: never sent.
        current = null;
        queueMicrotask(() => ev.fail("voice"));
        return;
      }
      counter += 1;
      current = { n: counter, ev };
      bridge.postMessage("say " + counter + " " + rateWord(rate) + " " + voiceId + " " + words);
    },
    cancel() {
      if (!current) return;
      current = null;
      bridge.postMessage("tts stop");
    },
  };

  bridge.addEventListener("message", (event) => {
    let message;
    try {
      message = JSON.parse(event.data);
    } catch {
      return;
    }
    if (message.t === "voices" && Array.isArray(message.list)) {
      list = named(message.list.filter((voice) => voice && typeof voice.id === "string" && typeof voice.lang === "string"));
      document.dispatchEvent(new Event("ts:native:voices"));
    } else if (message.t === "tts" && current && message.n === current.n) {
      const { ev } = current;
      if (message.ev === "start") ev.start();
      else if (message.ev === "end") {
        current = null;
        ev.end();
      } else if (message.ev === "error") {
        current = null;
        ev.fail("voice");
      } else if (message.ev === "stopped") {
        current = null;
        ev.stopped();
      }
    }
  });
}
