// native-audio.js is loaded only inside the Tendshelf Android app's window (data-shell="android").
//
// The app plays the book itself, in a foreground service, so sound survives a frozen page and a locked
// screen. This file lets player.js treat that sound like an audio element: it builds window.tsNativeAudio,
// an object with the parts of the media element API that player.js uses, and the app's pushed state becomes
// the element's events. The page draws what the app says; it never saves a place and never reads a token.
//
// Decision 0026: the app holds the device token and the page never gets one. The one object the app adds,
// window.tsNative, offers postMessage and onmessage only. The words sent to it are a strict grammar the app
// checks again (android/shared BridgeProtocol.kt); nothing here builds a URL, a path or a header.
//
// Without window.tsNative, or on a share-link page, nothing is built and player.js uses its own <audio>.

const native = window.tsNative;

if (native && document.documentElement.dataset.shell === "android" && !document.body.dataset.tsLink) {
  build(native);
}

function build(bridge) {
  const audio = new EventTarget();

  // The last state the app pushed, and when it arrived, so the position can run on between pushes.
  let pushed = null;
  let position = 0; // book ms at the moment `at`
  let at = performance.now();
  let rate = 1;
  let aimMs = null; // where the page wants the next start to begin
  let failure = null;

  const send = (words) => bridge.postMessage(words);
  const fire = (name) => audio.dispatchEvent(new Event(name));
  const pageBookId = () => (window.tsPlayer ? window.tsPlayer.state().bookId : "");
  const holdsPageBook = () => !!pushed && pushed.bookId !== "" && pushed.bookId === pageBookId();

  // The grammar allows 0.50 to 3.00 in steps of 0.05.
  const speedWords = (x) => (Math.min(3, Math.max(0.5, Math.round(x * 20) / 20))).toFixed(2);

  function bookMs() {
    if (!pushed) return position;
    const running = pushed.playing && !pushed.buffering && !pushed.ended;
    const ms = running ? position + (performance.now() - at) * rate : position;
    return pushed.durationMs ? Math.min(ms, pushed.durationMs) : ms;
  }

  function onState(state) {
    const before = pushed;
    pushed = state;
    position = state.positionMs;
    at = performance.now();
    failure = null;
    if (state.speed !== rate) {
      rate = state.speed;
      fire("ratechange");
    }
    const wasPlaying = !!before && before.playing;
    if (state.playing && !wasPlaying) {
      fire("play");
      fire("playing");
    } else if (!state.playing && wasPlaying) {
      fire("pause");
    }
    if (state.buffering && !(before && before.buffering)) fire("waiting");
    if (state.ended && !(before && before.ended)) fire("ended");
    fire("timeupdate");
    // Repeated at every push, which heals a race between a start and the page loading the book.
    if (state.bookId !== "" && state.bookId !== pageBookId()) fire("nativebook");
  }

  bridge.onmessage = (event) => {
    let message;
    try {
      message = JSON.parse(event.data);
    } catch {
      return;
    }
    if (message.t === "state") {
      onState({
        bookId: String(message.bookId),
        positionMs: Number(message.positionMs),
        durationMs: Number(message.durationMs),
        playing: message.playing === true,
        buffering: message.buffering === true,
        ended: message.ended === true,
        speed: Number(message.speed) || 1,
      });
    } else if (message.t === "error") {
      failure = { code: message.code === "book" ? 3 : 2 };
      fire("error");
    } else if (message.t === "back") {
      goBack();
    }
  };

  // Android Back: close what the page has open, innermost first, else say it handled nothing.
  function goBack() {
    const sheet = document.querySelector("#sheet .ts-sheet");
    const dialog = document.querySelector("dialog[open]");
    if (sheet) {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    } else if (dialog) {
      const cancel = new Event("cancel", { cancelable: true });
      dialog.dispatchEvent(cancel);
      if (!cancel.defaultPrevented) dialog.close();
    } else {
      send("back 0");
      return;
    }
    send("back 1");
  }

  Object.defineProperties(audio, {
    paused: { get: () => !pushed || !pushed.playing },
    ended: { get: () => !!pushed && pushed.ended },
    error: { get: () => failure },
    readyState: { value: 4 },
    // NaN keeps player.js from warming the next file: the app crosses files by itself.
    duration: { get: () => NaN },
    currentTime: { get: () => bookMs() / 1000 },
    bookMs: { get: bookMs },
    nativeBookId: { get: () => (pushed ? pushed.bookId : "") },
    // Fixed: player.js probes the setter, sees it is ignored and hides the volume slider.
    volume: { get: () => 1, set: () => {} },
    playbackRate: {
      get: () => rate,
      set: (x) => {
        rate = x;
        send("speed " + speedWords(x));
      },
    },
  });
  audio.defaultPlaybackRate = 1;
  audio.preservesPitch = true;
  audio.src = ""; // ignored: the app streams the book with its own credentials
  audio.canPlayType = () => "probably";
  audio.load = () => {};
  audio.removeAttribute = () => {};

  audio.play = () => {
    send("play");
    return Promise.resolve();
  };
  audio.pause = () => send("pause");

  // Starts the book: a plain play if the app already holds it and the page has no new place in mind.
  audio.start = (bookId) => {
    if (pushed && pushed.bookId === bookId && aimMs === null) {
      send("play");
      return;
    }
    send("start " + bookId + " " + (aimMs === null ? "-" : Math.max(0, Math.round(aimMs))));
    if (aimMs !== null) {
      position = aimMs;
      at = performance.now();
    }
    aimMs = null;
  };
  audio.aim = (ms) => {
    aimMs = ms;
  };
  audio.seekTo = (ms) => {
    position = ms;
    at = performance.now();
    if (holdsPageBook()) send("seek " + Math.max(0, Math.round(ms)));
    else aimMs = ms;
  };

  // The bar and the system bars take the page's colour.
  function sendTheme() {
    const meta = [...document.querySelectorAll('meta[name="theme-color"]')].find((m) => !m.media || matchMedia(m.media).matches);
    if (meta && /^#[0-9a-f]{6}$/.test(meta.content)) send("theme " + meta.content);
  }
  sendTheme();
  document.addEventListener("htmx:after:swap", sendTheme);

  // player.js calls this once its listeners are on, so the full state the app answers with is not missed.
  audio.hello = () => send("hello 1");

  window.tsNativeAudio = audio;
  document.dispatchEvent(new Event("ts:native:ready"));
}
