// native-downloads.js is loaded only inside the Tendshelf Android app's window (data-shell="android").
//
// The book page's Download button, driven by the app instead of by downloads.js (which needs chunks, a cache and
// Web Locks that the app's window does not use). The app keeps the files and tells this page how the book stands:
// {"t":"dl","bookId":"","state":"none|queued|downloading|paused|ready|failed|stale","pct":0}, plus "known":false
// while the app cannot yet tell how big the book is (an older app never sends it, and then the percent is known).
// The words sent are "dl add <id>" and "dl ask <id>"; like native-audio.js this file builds no URL and reads no token.

const native = window.tsNative;

if (native && document.documentElement.dataset.shell === "android" && !document.body.dataset.tsLink) {
  start(native);
}

function start(bridge) {
  const nodes = () => ({
    button: document.querySelector("[data-ts-download]"),
    link: document.querySelector("[data-ts-download-status]"),
  });

  // The page owns the first label ("Download"); the app owns everything after it.
  let firstLabel = "";

  function show(state, pct, known) {
    const { button, link } = nodes();
    if (!button || !link) return;
    const label = button.querySelector(".ts-book__label");
    if (!firstLabel) firstLabel = label.textContent;
    const listed = state === "ready" || state === "stale" || state === "paused";
    button.hidden = listed;
    // A waiting book keeps its button: an automatic download that waits for Wi-Fi turns into the person's own, and
    // starts at once, when they press Download (decision 0035, invariant 7). For any other waiting book the press
    // changes nothing. Only a running download is disabled.
    button.disabled = state === "downloading";
    link.hidden = !listed;
    // The link goes to the app's own Downloads page; htmx must not take the click.
    link.setAttribute("hx-boost", "false");
    if (state !== "downloading" && state !== "queued") showBar(button, null);
    if (state !== "downloading") {
      showPercent(button, null);
      button.removeAttribute("aria-label");
    }
    if (listed) {
      link.querySelector("[data-ts-download-label]").textContent = link.dataset["tsMsg" + (state === "paused" ? "Paused" : "Ready")];
    } else if (state === "downloading") {
      // Without a known total the words say so and the bar keeps moving, instead of a stuck 0 %.
      // The row of buttons is tight, so the number sits on top of the button and the label keeps its width.
      // A screen reader gets the whole sentence from the button's name; the overlay is hidden from it.
      const words = known ? button.dataset.tsMsgDownloading.replace("{pct}", String(pct)) : (button.dataset.tsMsgWorking || "Downloading…");
      label.textContent = known ? firstLabel : words;
      button.setAttribute("aria-label", words);
      showPercent(button, known ? pct : null);
      showBar(button, known ? pct : -1);
    } else if (state === "queued") {
      label.textContent = button.dataset.tsMsgWaiting;
      showBar(button, null);
    } else if (state === "failed") {
      label.textContent = link.dataset.tsMsgFailed;
    } else {
      label.textContent = firstLabel;
    }
  }

  // The percent laid over the button; null hides it.
  function showPercent(button, pct) {
    const over = button.querySelector("[data-ts-download-pct]");
    if (!over) return;
    over.hidden = pct === null;
    over.textContent = pct === null ? "" : pct + "%";
  }

  // The bar inside the button: a number is a share, -1 is "working, size not known yet", null hides it.
  function showBar(button, pct) {
    const bar = button.querySelector("[data-ts-download-bar]");
    if (!bar) return;
    bar.hidden = pct === null;
    bar.classList.toggle("ts-bar--busy", pct === -1);
    if (pct === null) return;
    if (pct === -1) {
      bar.removeAttribute("aria-valuenow");
      return;
    }
    bar.setAttribute("aria-valuenow", String(pct));
    bar.querySelector(".ts-bar__fill").style.setProperty("--ts-value", String(Math.max(0, Math.min(100, pct))));
  }

  const bookId = () => (nodes().button ? nodes().button.dataset.tsDownload : "");

  function ask() {
    const id = bookId();
    if (id) bridge.postMessage("dl ask " + id);
  }

  bridge.addEventListener("message", (event) => {
    let message;
    try {
      message = JSON.parse(event.data);
    } catch {
      return;
    }
    if (message.t === "dl") {
      standing.set(message.bookId, String(message.state));
      for (const callback of listeners) callback();
    }
    if (message.t === "room" && roomAnswer) roomAnswer(Number(message.bytes) || 0);
    if (message.t === "dl" && message.bookId === bookId()) show(String(message.state), Number(message.pct), message.known !== false);
  });

  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-ts-download]");
    if (!button || button.disabled) return;
    bridge.postMessage("dl add " + button.dataset.tsDownload);
  });

  // ---- Download all (downloads-all.js): the app's own queue, driven with the same words ----

  const standing = new Map(); // book id -> the last state the app pushed
  const listeners = [];
  let roomAnswer = null;
  let allLoad = null;
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  // Resolves with the app's answer to "dl room" (the bytes it can still give downloads), or null when it never comes.
  function room() {
    return new Promise((resolve) => {
      roomAnswer = (bytes) => resolve(bytes);
      bridge.postMessage("dl room");
      setTimeout(() => resolve(null), 2000);
    }).finally(() => {
      roomAnswer = null;
    });
  }

  const driver = {
    usable: () => true,
    // Asks how each book stands and waits a moment for the answers; a book that does not answer is not held.
    async have(ids) {
      for (const id of ids) {
        standing.delete(id);
        bridge.postMessage("dl ask " + id);
      }
      for (let i = 0; i < 15 && ids.some((id) => !standing.has(id)); i++) await wait(100);
      return new Set(ids.filter((id) => !["none", "failed", undefined].includes(standing.get(id))));
    },
    async room(bytes) {
      const free = await room();
      return { ok: free === null || free >= bytes, free: free === null ? 0 : free };
    },
    // The app answers "dl add" with the state at that moment (none) before it has fetched the book; wait for the
    // next state, which comes once the book is queued or refused (a refusal never comes: give up after a while).
    async add(id) {
      standing.delete(id);
      bridge.postMessage("dl add " + id);
      for (let i = 0; i < 60 && (!standing.has(id) || standing.get(id) === "none"); i++) await wait(100);
    },
    stop: (ids) => ids.forEach((id) => bridge.postMessage("dl remove " + id)),
    states: (ids) => new Map(ids.map((id) => [id, stateOf(standing.get(id))])),
    onChange: (callback) => listeners.push(callback),
    quiet: () => {},
  };

  function stateOf(state) {
    if (state === "ready" || state === "stale") return "ready";
    if (state === "failed") return "failed";
    return state === undefined || state === "none" ? "none" : "pending";
  }

  function loadAll() {
    const banner = document.getElementById("ts-offline");
    const src = banner && banner.dataset.tsDownloadsAllSrc;
    if (allLoad || !src || !document.querySelector("[data-ts-download-all]")) return;
    allLoad = import(src).then((m) => m.mount(driver));
  }

  // A new page (or a swapped one) asks how its book stands; the app answers with a "dl" push.
  document.addEventListener("htmx:after:swap", ask);
  document.addEventListener("htmx:after:swap", loadAll);
  document.addEventListener("ts:native:ready", ask);
  ask();
  loadAll();
}
