// native-feel.js is loaded only inside the Tendshelf Android app's window (data-shell="android").
//
// Android feel (decision 0033). The words sent to the app, all fixed, the second part exactly 0 or 1:
//   "wake 1" / "wake 0"         the page holds / lets go of the screen (the app sets FLAG_KEEP_SCREEN_ON)
//   "pagekeys 1" / "pagekeys 0" the reader is in front with "Turn pages with the volume buttons" on / not
//   "quiet 1" / "quiet 0"       the Skip silence switch in Settings changed
// The app answers {"t":"pagekey","dir":"next|prev"} when a volume button is pressed while pagekeys is 1; the page turns
// through the reader's own PageDown / PageUp key path, so reader.js is untouched.
// window.tsNativeWake stands in for navigator.wakeLock, which Android System WebView does not grant, so reader-comfort.js
// keeps deciding when to hold the screen. Like native-speech.js it listens with addEventListener, never onmessage.

const native = window.tsNative;

if (native && document.documentElement.dataset.shell === "android" && !document.body.dataset.tsLink) {
  start(native);
}

function start(bridge) {
  let held = null; // the sentinel handed out last, until it is released
  let sentKeys = null; // the last pagekeys word sent ("1" or "0"); null before any

  const reader = () => document.getElementById("reader");
  const keysWanted = () => reader()?.dataset.volumeKeys === "true";

  function sentinel() {
    const stand = new EventTarget();
    let done = false;
    stand.release = () => {
      if (done) return Promise.resolve();
      done = true;
      if (held === stand) held = null;
      bridge.postMessage("wake 0");
      stand.dispatchEvent(new Event("release"));
      return Promise.resolve();
    };
    return stand;
  }

  window.tsNativeWake = {
    request(type) {
      if (type !== "screen") return Promise.reject(new TypeError("screen only"));
      held?.release();
      held = sentinel();
      bridge.postMessage("wake 1");
      return Promise.resolve(held);
    },
  };

  // Tell the app whether the page wants the volume buttons, only when that changed.
  function syncKeys() {
    const word = keysWanted() ? "1" : "0";
    if (sentKeys === null && word === "0") return;
    if (word === sentKeys) return;
    sentKeys = word;
    bridge.postMessage("pagekeys " + word);
  }

  syncKeys();
  document.addEventListener("htmx:after:swap", syncKeys);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") syncKeys();
  });
  window.addEventListener("pagehide", () => {
    if (sentKeys === "1") {
      sentKeys = "0";
      bridge.postMessage("pagekeys 0");
    }
  });

  document.addEventListener("change", (event) => {
    const box = event.target;
    if (!(box instanceof HTMLInputElement) || box.type !== "checkbox") return;
    if (box.name === "volume_keys") {
      const root = reader();
      if (root) root.dataset.volumeKeys = String(box.checked);
      syncKeys();
    } else if (box.name === "skip_quiet") {
      bridge.postMessage("quiet " + (box.checked ? "1" : "0"));
    }
  });

  bridge.addEventListener("message", (event) => {
    let message;
    try {
      message = JSON.parse(event.data);
    } catch {
      return;
    }
    if (!message || message.t !== "pagekey" || !keysWanted()) return;
    if (message.dir !== "next" && message.dir !== "prev") return;
    document.body.dispatchEvent(
      new KeyboardEvent("keydown", { key: message.dir === "next" ? "PageDown" : "PageUp", bubbles: true, cancelable: true }),
    );
  });
}
