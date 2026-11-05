// Tendshelf offline support on every page (decision 0012): keeps one person's offline data, sends
// saves that never reached the server, shows the offline banner, registers the service worker and
// loads the download manager when a page needs it.
const root = document.documentElement;
const banner = document.getElementById("ts-offline");
const OFFLINE_PREFIX = "ts.offline";
const params = new URLSearchParams(location.search);

function readJSON(key, fallback) {
  try {
    return JSON.parse(localStorage.getItem(key)) || fallback;
  } catch {
    return fallback;
  }
}

function writeJSON(key, value) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // storage is optional
  }
}

// The CSRF token, from hx-headers on <body>, as player.js reads it.
function csrfToken() {
  try {
    return JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return "";
  }
}

// ---- One person's data per browser ----

// Removes the downloads and every unsent save in this browser.
async function wipe() {
  if ("caches" in window) {
    for (const name of await caches.keys()) {
      if (name.startsWith("ts-person-")) await caches.delete(name);
    }
  }
  for (const key of Object.keys(localStorage)) {
    if (key.startsWith(OFFLINE_PREFIX) || key === "ts.pending") localStorage.removeItem(key);
  }
}

// Runs before anything is shown or sent: a sign-out, or a different person, clears the previous
// person's downloads and unsent saves.
async function settlePerson() {
  const person = root.dataset.tsPerson || "";
  const known = localStorage.getItem("ts.offline.person");
  try {
    if (params.has("signed_out") || (person && known && known !== person)) await wipe();
    if (person && "caches" in window) {
      for (const name of await caches.keys()) {
        if (name.startsWith("ts-person-") && name !== "ts-person-" + person) await caches.delete(name);
      }
    }
    if (person) localStorage.setItem("ts.offline.person", person);
  } catch {
    // storage is optional
  }
}

// ---- Sending what was saved offline ----

let wasOffline = !navigator.onLine;
let failures = 0;
let flushing = false;
let flushTimer = 0;
const RETRY_MS = [5000, 15000, 60000, 300000];

async function postJSON(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
    body: JSON.stringify(body),
  });
  return res.redirected ? null : res;
}

// All unsent place saves as one batch. A save is dropped only when the server answered it and it has
// not changed since it was sent. Returns false when sending should stop and be tried again.
async function flushPending() {
  const pending = readJSON("ts.pending", {});
  const sent = Object.entries(pending).slice(0, 100);
  if (!sent.length) return true;
  const updates = sent.map(([bookId, form]) => {
    const f = new URLSearchParams(form);
    return {
      bookId,
      positionMs: Number(f.get("position_ms")),
      clientUpdatedAt: Number(f.get("client_updated_at")),
      deviceId: f.get("device_id") || "",
      speed: Number(f.get("speed")) || 1,
      finished: f.get("finished") === "1",
      force: f.get("force") === "1",
    };
  });
  const res = await postJSON("/api/v1/progress/batch", { updates });
  if (!res || !res.ok) return false;
  const answered = new Set((await res.json()).results.map((r) => r.bookId));
  const now = readJSON("ts.pending", {});
  for (const [bookId, form] of sent) {
    if (answered.has(bookId) && now[bookId] === form) delete now[bookId];
  }
  writeJSON("ts.pending", now);
  return true;
}

// Bookmarks made offline, one by one. 201, 200, 404 and 409 are answers; anything else stops.
async function flushMarks() {
  let marks = readJSON("ts.offline.marks", []);
  while (marks.length) {
    const mark = marks[0];
    const res = await postJSON("/api/v1/books/" + encodeURIComponent(mark.bookId) + "/bookmarks", {
      positionMs: mark.positionMs,
      clientId: mark.clientId,
    });
    if (!res || ![200, 201, 404, 409].includes(res.status)) return false;
    // Take the first mark off the list as it is now, in case another page added one meanwhile.
    marks = readJSON("ts.offline.marks", []).filter((m) => m.clientId !== mark.clientId);
    writeJSON("ts.offline.marks", marks);
  }
  return true;
}

// Reading places waiting to be sent (reader-offline.js keeps the queue; a sent record is only a local place).
function unsentReading() {
  return Object.values(readJSON("ts.offline.reading", {})).filter((record) => !record.sent);
}

// Sends them by the reader module, loaded only when there is something to send.
async function flushReading() {
  const src = banner && banner.dataset.tsReaderOfflineSrc;
  if (!unsentReading().length || !src) return true;
  return (await import(src)).flushReading(csrfToken());
}

function queued() {
  return Object.keys(readJSON("ts.pending", {})).length + readJSON("ts.offline.marks", []).length + unsentReading().length;
}

async function flush() {
  if (flushing) return;
  flushing = true;
  clearTimeout(flushTimer);
  let ok = false;
  const had = queued() > 0;
  try {
    ok = (await flushPending()) && (await flushMarks()) && (await flushReading());
  } catch {
    ok = false;
  }
  flushing = false;
  if (ok) {
    failures = 0;
    if (had && wasOffline) say("back");
    wasOffline = false;
    return;
  }
  wasOffline = true;
  // 5 s, 15 s, 60 s, then every 5 minutes, and only while someone is looking.
  flushTimer = setTimeout(() => {
    if (document.visibilityState === "visible") flush();
  }, RETRY_MS[Math.min(failures, RETRY_MS.length - 1)]);
  failures += 1;
}

// A notice leaves by itself and a tap dismisses it: player.js times every toast it makes for us.
function say(name) {
  const text = banner ? banner.dataset["tsMsg" + name[0].toUpperCase() + name.slice(1)] : "";
  if (text && window.tsPlayer) window.tsPlayer.toast("hint", text);
}

// ---- The banner ----

function shellShowing() {
  return !!document.querySelector("[data-ts-offline-shell]");
}

// Online but showing the shell means the server was too slow or failed once; it may answer now. The
// same address is asked for again after a pause, until the real page arrives (each attempt that
// ends in the shell again renders the banner, which arms the next one).
const RETRY_AFTER_MS = 10000;
let retryTimer = 0;

function armRetry(retry) {
  if (retryTimer) return;
  retryTimer = setTimeout(() => {
    retryTimer = 0;
    if (navigator.onLine && shellShowing() && !retry.hidden) retry.click();
  }, RETRY_AFTER_MS);
}

function renderBanner() {
  if (!banner) return;
  const offline = !navigator.onLine;
  const unreachable = !offline && shellShowing();
  const text = banner.querySelector("[data-ts-offline-text]");
  const retry = banner.querySelector("[data-ts-offline-retry]");
  banner.hidden = !(offline || unreachable);
  text.textContent = offline ? banner.dataset.tsMsgOffline : banner.dataset.tsMsgUnreachable;
  retry.hidden = !unreachable;
  retry.href = location.href;
  if (unreachable) armRetry(retry);
}

window.addEventListener("offline", () => {
  wasOffline = true;
  renderBanner();
});

window.addEventListener("online", () => {
  renderBanner();
  flush();
  // With the shell on screen, the same address is asked for again (boosted, no reload).
  const retry = banner && banner.querySelector("[data-ts-offline-retry]");
  if (retry && !retry.hidden) retry.click();
});

document.addEventListener("htmx:after:swap", renderBanner);

// ---- The service worker ----

const audio = document.getElementById("audio");
let registration = null;
let updatedAt = Date.now();

// A waiting worker takes over only while nothing is playing, so a listener is never interrupted.
function activateWaiting() {
  if (!registration || !registration.waiting) return;
  if (audio && !audio.paused) return;
  registration.waiting.postMessage({ type: "activate" });
}

async function registerWorker() {
  const guarded = root.hasAttribute("data-shell") || document.body.dataset.tsLink !== undefined;
  if (guarded || !isSecureContext || !navigator.serviceWorker) return;
  try {
    registration = await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  } catch {
    return;
  }
  activateWaiting();
  registration.addEventListener("updatefound", () => {
    const worker = registration.installing;
    if (worker) worker.addEventListener("statechange", activateWaiting);
  });
  // Media events do not bubble, so they are caught on the way down.
  document.addEventListener("pause", activateWaiting, true);
  document.addEventListener("ended", activateWaiting, true);
  document.addEventListener("htmx:after:swap", () => {
    activateWaiting();
    if (Date.now() - updatedAt > 3600000) {
      updatedAt = Date.now();
      registration.update().catch(() => {});
    }
  });
}

// ---- The download manager, loaded when a page needs it ----

let downloads = null;

function needsDownloads() {
  if (document.documentElement.hasAttribute("data-shell")) return false;
  if (document.querySelector("[data-ts-downloads], [data-ts-download]")) return true;
  const books = Object.values(readJSON("ts.offline", { books: {} }).books || {});
  return books.some((book) => book.state !== "ready");
}

function loadDownloads() {
  const src = banner && banner.dataset.tsDownloadsSrc;
  if (!src || downloads || !needsDownloads()) return;
  downloads = import(src).catch(() => {
    downloads = null;
  });
}

document.addEventListener("htmx:after:swap", loadDownloads);

// ---- Start ----

await settlePerson();
renderBanner();
flush();
registerWorker();
loadDownloads();
