// native-downloads.js is loaded only inside the Tendshelf Android app's window (data-shell="android").
//
// The book page's Download button, driven by the app instead of by downloads.js (which needs chunks, a cache and
// Web Locks that the app's window does not use). The app keeps the files and tells this page how the book stands:
// {"t":"dl","bookId":"","state":"none|queued|downloading|paused|ready|failed|stale","pct":0}.
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

  function show(state, pct) {
    const { button, link } = nodes();
    if (!button || !link) return;
    const label = button.querySelector(".ts-book__label");
    if (!firstLabel) firstLabel = label.textContent;
    const working = state === "queued" || state === "downloading";
    const listed = state === "ready" || state === "stale" || state === "paused";
    button.hidden = listed;
    button.disabled = working;
    link.hidden = !listed;
    // The link goes to the app's own Downloads page; htmx must not take the click.
    link.setAttribute("hx-boost", "false");
    if (listed) {
      link.querySelector("[data-ts-download-label]").textContent = link.dataset["tsMsg" + (state === "paused" ? "Paused" : "Ready")];
    } else if (state === "downloading") {
      label.textContent = button.dataset.tsMsgDownloading.replace("{pct}", String(pct));
    } else if (state === "queued") {
      label.textContent = button.dataset.tsMsgWaiting;
    } else if (state === "failed") {
      label.textContent = link.dataset.tsMsgFailed;
    } else {
      label.textContent = firstLabel;
    }
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
    if (message.t === "dl" && message.bookId === bookId()) show(String(message.state), Number(message.pct));
  });

  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-ts-download]");
    if (!button || button.disabled) return;
    bridge.postMessage("dl add " + button.dataset.tsDownload);
  });

  // A new page (or a swapped one) asks how its book stands; the app answers with a "dl" push.
  document.addEventListener("htmx:after:swap", ask);
  document.addEventListener("ts:native:ready", ask);
  ask();
}
