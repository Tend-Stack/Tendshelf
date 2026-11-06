// Tendshelf web player (P1c-4; docs/architecture.md, Web player).
//
// The one media element lives in the layout outside #main, so htmx navigation never replaces it and
// playback carries on across pages. A book's files play one after the other through it; there is
// deliberately no second element. CSP: no inline style, no markup from data, no eval; custom
// properties only through style.setProperty. Every word shown comes from server-rendered
// data-ts-msg-* attributes.

// In the Android app the app plays the book and this page is its remote control (native-audio.js).
const NATIVE = document.documentElement.dataset.shell === "android" && !!window.tsNative && !document.body.dataset.tsLink;
const audio = NATIVE ? await nativeAudio() : document.getElementById("audio");
const slot = document.getElementById("player");

const BREAK_MS = 60000; // a pause this long counts as a break
const COMPAT_DEADLINE_MS = 600000; // how long to wait for a converted copy
const SAVE_EVERY_MS = 10000;
const FORM_TYPE = "application/x-www-form-urlencoded";

const CONTROL_NAMES = ["title", "meta", "open", "cover", "back", "forward", "toggle", "back-num", "forward-num", "mini-progress"];
const controls = {};
for (const name of CONTROL_NAMES) {
  controls[name] = slot.querySelector("[data-ts-" + name + "]");
}

// ---- State ----

let book = null; // the loaded book (see parseBook), or null

// The file being played: an index into book.files, the local start offset, and whether audio.src is
// set. Also used: fileId (sound started from a link before the book arrived), compat (playing the
// converted copy) and prefetched (the next file is warmed).
let current = { fileIdx: -1, startMs: 0, loaded: false, compat: false };

let idleAt = 0; // Date.now() of the last pause or restored position; 0 while playing
let heardMs = 0; // listening time not yet reported
let heardSince = 0; // performance.now() when this stretch of sound began; 0 when silent
let lastSavedMs = -1; // book position of the last save
let lastRenderAt = 0;
let hiddenAt = 0;
let wantsFirstSound = false; // a play click is waiting for its first audible sound
let stopped = false; // signed out: stop saving
// A share link page (/s/{token}) puts the player in link mode: its media come from the link's own
// address, the book from the inline #player-data, and the place stays in this browser only.
const LINK = document.body.dataset.tsLink || "";
let prompting = false; // the Continue there? question is on screen
let beaconSent = false;
let compatSeq = 0; // bumped to cancel a wait for a converted copy
let seekSaveTimer = 0;
let userVolume = storedVolume(); // 0 to 1, per device
let fade = 1; // 0 to 1, the sleep timer's ramp
let memoryDeviceId = ""; // used when localStorage is unavailable

// ---- Small helpers ----

const encode = encodeURIComponent;
const perfNow = () => performance.now();

function listen(target, type, handler) {
  target.addEventListener(type, handler);
}

function fire(target, name, detail) {
  target.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));
}

// A server-rendered message: msg("playPrefix") reads data-ts-msg-play-prefix.
function msg(name) {
  const attribute = "tsMsg" + name[0].toUpperCase() + name.slice(1);
  return slot.dataset[attribute] || "";
}

function fill(message, values) {
  return message.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? "");
}

function clamp(value, low, high) {
  return Math.min(high, Math.max(low, value));
}

function twoDigits(n) {
  return String(n).padStart(2, "0");
}

function clock(ms) {
  const totalSeconds = Math.floor(ms / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const first = hours ? hours + ":" + twoDigits(minutes) : minutes;
  return first + ":" + twoDigits(totalSeconds % 60);
}

// The CSRF token, from hx-headers on <body>.
function csrfToken() {
  try {
    return JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return "";
  }
}

function randomId() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// This browser's id, so the server can tell its saves from another device's.
function deviceId() {
  try {
    let id = localStorage.getItem("ts.deviceId");
    if (!id) {
      id = randomId();
      localStorage.setItem("ts.deviceId", id);
    }
    return id;
  } catch {
    memoryDeviceId = memoryDeviceId || randomId();
    return memoryDeviceId;
  }
}

// ---- Toasts ----

// A toast in #toasts. Text goes in as text nodes only. actions: optional { label, run } buttons.
function toast(kind, text, actions) {
  const el = document.createElement("div");
  el.className = kind === "hint" ? "ts-toast ts-toast--hint" : "ts-toast";
  el.role = kind === "err" ? "alert" : "status";
  el.append(text);
  if (actions) {
    const row = el.appendChild(document.createElement("span"));
    row.className = "ts-toast__actions";
    for (const action of actions) {
      const button = row.appendChild(document.createElement("button"));
      button.className = "ts-btn ts-btn--quiet";
      button.textContent = action.label;
      button.type = "button";
      listen(button, "click", action.run);
    }
  }
  // While the modal full player is open the page behind it is inert, so toasts go into its own region.
  const host = document.querySelector("#eyes-free:modal [data-ts-full-toasts]") || document.querySelector("#player-full:modal [data-ts-full-toasts]") || document.getElementById("toasts");
  host.append(el);
  return el;
}

function dismissToast(el) {
  if (!el) return;
  el.classList.add("is-leaving");
  setTimeout(() => el.remove(), 800); // shell.js usually removes it first
}

// Toasts the server sends out of band (components.ToastOOB, ErrorToast) carry data-ts-ms, their lifetime.
// Nothing else dismissed them, so a saved-note toast stayed. They leave on time, but not while the pointer or
// focus is on one (it stays reachable by Tab while shown).
function dismissLater(el, ms) {
  setTimeout(() => (el.matches(":hover, :focus-within") ? dismissLater(el, 1000) : dismissToast(el)), ms);
}
const serverToasts = document.getElementById("toasts");
if (serverToasts) {
  new MutationObserver((records) => {
    for (const record of records) {
      for (const node of record.addedNodes) {
        if (node.dataset && node.dataset.tsMs) dismissLater(node, Number(node.dataset.tsMs));
      }
    }
  }).observe(serverToasts, { childList: true });
}

// A toast for the other modules (offline, downloads, the full player). It leaves by itself, 4 s, or 8 s
// for an error, and a tap dismisses it. It must not rely on the #toasts observer: in the full player
// or Eyes-free the toast goes into their own region, which nothing else watches (operator, 2026-11-04).
function timedToast(kind, text, actions) {
  const el = toast(kind, text, actions);
  dismissLater(el, kind === "err" ? 8000 : 4000);
  listen(el, "click", () => dismissToast(el));
  return el;
}

function hint(text) {
  const el = toast("hint", text);
  setTimeout(() => dismissToast(el), 1600);
}

// ---- Data: the book description the server renders ----

function parseBook(el) {
  const d = el.dataset;
  const num = (value) => Number(value) || 0;
  const updatedAt = num(d.updatedAt);
  // One object per <li> of a nested list, made from its data attributes.
  const rows = (selector, make) => [...el.querySelectorAll(selector + " > li")].map((li) => make(li.dataset, li));

  const parsed = {
    id: d.bookId,
    title: d.title,
    author: d.author,
    cover: d.coverUrl,
    durationMs: num(d.durationMs),
    autoRewind: d.autoRewind === "1",
    rewind: (d.rewind || "").split(",").map((step) => step.split(":").map(Number)), // [pause ms, seconds], longest last
    skipBack: num(d.skipBackS) || 15,
    skipForward: num(d.skipForwardS) || 30,
    speed: clamp(num(d.speed) || 1, 0.5, 3),
    accents: { dark: d.accentDark, light: d.accentLight, sepia: d.accentSepia }, // cover colours for the full player
    tint: d.tint || "",
    readHref: d.readHref || "", // the book's reader, empty without an ebook the person may read
    // null when this book was never started
    stored: updatedAt > 0
      ? { positionMs: num(d.positionMs), updatedAt, deviceId: d.deviceId, deviceLabel: d.deviceLabel, finished: d.finished === "1" }
      : null,
    files: rows("[data-ts-files]", (x) => ({
      id: x.id,
      offset: num(x.offsetMs), // where this file starts in the whole book
      dur: num(x.durationMs),
      mime: x.mime,
      mayCompat: x.mayNeedCompat === "1", // a converted copy can stand in
      playable: x.playable === "1",
    })),
    chapters: rows("[data-ts-chapters]", (x, li) => ({ start: num(x.startMs), end: num(x.endMs), title: li.textContent })),
  };
  if (!parsed.chapters.length) {
    parsed.chapters.push({ start: 0, end: parsed.durationMs, title: "" }); // no chapters: the book is one
  }
  return parsed;
}

// Fetches a book's fragment (the last played book when id is empty). Null when there is none, or
// when redirected, which means signed out.
async function fetchBook(id, init) {
  if (LINK) return linkBook(id);
  try {
    const res = await fetch(id ? "/hx/player/" + encode(id) : "/hx/player", init);
    if (res.status !== 200 || res.redirected) return null;
    const html = await res.text();
    const el = new DOMParser().parseFromString(html, "text/html").getElementById("player-data");
    const parsed = el ? parseBook(el) : null;
    if (parsed) useNewerPendingSave(parsed);
    return parsed;
  } catch {
    return null;
  }
}

// A save that never reached the server (offline) can be newer than the stored place the fragment
// carries, possibly a copy from before the save: the newer one wins until offline.js has sent it.
function useNewerPendingSave(parsed) {
  const saved = new URLSearchParams(pendingSaves()[parsed.id] || "");
  const savedAt = Number(saved.get("client_updated_at")) || 0;
  if (savedAt <= (parsed.stored ? parsed.stored.updatedAt : 0)) return;
  parsed.stored = {
    positionMs: Number(saved.get("position_ms")) || 0,
    updatedAt: savedAt,
    deviceId: saved.get("device_id") || "",
    deviceLabel: parsed.stored ? parsed.stored.deviceLabel : "",
    finished: saved.get("finished") === "1",
  };
}

// ---- Link mode: the book is on the page and the place is kept in this browser ----

function linkPlaceKey(bookId) {
  return "ts.link.place." + bookId;
}

function readLinkPlace(bookId) {
  try {
    return JSON.parse(localStorage.getItem(linkPlaceKey(bookId)));
  } catch {
    return null;
  }
}

function saveLinkPlace(bookId, positionMs, finished) {
  lastSavedMs = positionMs;
  try {
    localStorage.setItem(linkPlaceKey(bookId), JSON.stringify({ positionMs, updatedAt: Date.now(), finished }));
  } catch {
    // storage is optional
  }
}

// The book of a link page, with the place this browser kept. Null for any other book.
function linkBook(id) {
  const el = document.getElementById("player-data");
  const parsed = el ? parseBook(el) : null;
  if (!parsed || (id && id !== parsed.id)) return null;
  const place = readLinkPlace(parsed.id);
  parsed.stored = place && place.updatedAt > 0 ? { positionMs: Number(place.positionMs) || 0, updatedAt: place.updatedAt, deviceId: "", deviceLabel: "", finished: !!place.finished } : null;
  return parsed;
}

// The page's Play button becomes Resume when a place is kept, and starts from the loaded book
// (not from the server's first file) so a click always goes to that place.
function showLinkResume(bookId) {
  const place = readLinkPlace(bookId);
  if (!place || place.finished || !(place.positionMs > 0)) return;
  for (const el of document.querySelectorAll("[data-ts-play]")) {
    const label = el.querySelector("[data-ts-play-label]");
    if (label && msg("resume")) label.textContent = msg("resume");
    delete el.dataset.tsFile;
    el.dataset.tsStartMs = Math.round(place.positionMs);
  }
}

// ---- Timeline: one position across all the files of a book ----

function streamUrl(file) {
  return LINK + "/stream/" + encode(file.id);
}

// The first playable file ending after ms: its index and the offset in it, or the next file at 0
// when ms falls in a gap. Null when nothing is left.
function locate(ms) {
  const idx = book ? book.files.findIndex((f) => f.playable && ms < f.offset + f.dur) : -1;
  if (idx < 0) return null;
  return { fileIdx: idx, offsetMs: Math.max(0, ms - book.files[idx].offset) };
}

// Position in the current file; before the media loads, the start offset we asked for.
function nativeAudio() {
  if (window.tsNativeAudio) return window.tsNativeAudio;
  return new Promise((resolve) => document.addEventListener("ts:native:ready", () => resolve(window.tsNativeAudio), { once: true }));
}

function localMs() {
  const known = current.loaded && audio.readyState >= 1;
  return known ? audio.currentTime * 1000 : current.startMs;
}

// Position in the whole book.
function bookPosition() {
  if (NATIVE) return book ? audio.bookMs : 0;
  if (!book || current.fileIdx < 0) return 0;
  return book.files[current.fileIdx].offset + localMs();
}

function nextPlayableIndex(after) {
  return book.files.findIndex((f, i) => i > after && f.playable);
}

function chapterIndexAt(ms) {
  return Math.max(0, book.chapters.findLastIndex((c) => c.start <= ms));
}

// ---- Controls: loading files, seeking, playing ----

// Points the audio element at a file. The #t= fragment starts at the offset without a separate
// seek. Loading resets playbackRate to defaultPlaybackRate, so that is set first.
function loadSource(url, offsetMs, shouldPlay) {
  current.loaded = true;
  current.startMs = offsetMs;
  audio.defaultPlaybackRate = book.speed;
  audio.src = url + "#t=" + (offsetMs / 1000).toFixed(3);
  if (shouldPlay) audio.play().catch(() => {});
}

// Switches to another file, or to its converted copy if the browser cannot decode it.
function setSource(idx, offsetMs, shouldPlay) {
  const file = book.files[idx];
  compatSeq++;
  current = { fileIdx: idx, startMs: offsetMs, loaded: false, compat: false };
  if (file.mayCompat && file.mime && audio.canPlayType(file.mime) === "") {
    useCompatCopy(idx, offsetMs, shouldPlay);
  } else {
    loadSource(streamUrl(file), offsetMs, shouldPlay);
  }
}

// Polls for the converted copy while the server is still making it (503), until it is ready,
// fails or the deadline passes.
async function useCompatCopy(idx, offsetMs, shouldPlay) {
  const url = streamUrl(book.files[idx]) + "?compat=1";
  const mine = ++compatSeq;
  const deadline = Date.now() + COMPAT_DEADLINE_MS;
  let waitingToast = null;
  let res;
  for (;;) {
    try {
      res = await fetch(url, { method: "HEAD" });
    } catch {
      res = null;
    }
    if (mine !== compatSeq) {
      // the listener moved on: give up quietly
      dismissToast(waitingToast);
      return;
    }
    if (res && res.ok) {
      dismissToast(waitingToast);
      current.compat = true;
      loadSource(url, offsetMs, shouldPlay);
      return;
    }
    if (!res || res.status !== 503 || Date.now() > deadline) break;
    waitingToast = waitingToast || toast("hint", msg("compatWait"));
    const retrySeconds = Number(res.headers.get("Retry-After")) || 3;
    await new Promise((resolve) => setTimeout(resolve, retrySeconds * 1000));
  }
  dismissToast(waitingToast);
  toast("err", msg("compatFailed"));
  emit("error");
}

// Moves to a book position. shouldPlay says whether a file change keeps playing (default: as now).
function seek(ms, shouldPlay) {
  if (!book) return;
  const lastMs = book.durationMs ? book.durationMs - 1 : Infinity;
  const target = locate(clamp(ms, 0, lastMs));
  if (!target) return;
  if (NATIVE) {
    audio.seekTo(clamp(ms, 0, lastMs));
    emit("seek");
    return;
  }

  if (!current.loaded) {
    // nothing loaded yet: only remember where to start
    current.fileIdx = target.fileIdx;
    current.startMs = target.offsetMs;
  } else if (target.fileIdx !== current.fileIdx) {
    setSource(target.fileIdx, target.offsetMs, shouldPlay === undefined ? !audio.paused : shouldPlay);
  } else {
    audio.currentTime = target.offsetMs / 1000;
    current.startMs = target.offsetMs;
  }

  if (audio.paused) {
    // no tick saves while paused, so save shortly after the last seek
    clearTimeout(seekSaveTimer);
    seekSaveTimer = setTimeout(() => save("seek"), 2000);
  }
  emit("seek");
}

// Previous chapter (delta < 0) or next. Previous restarts this chapter when more than 3 s in.
function jumpChapter(delta) {
  if (!book) return;
  const position = bookPosition();
  const idx = chapterIndexAt(position);
  const chapters = book.chapters;
  if (delta < 0) {
    const restartThis = position - chapters[idx].start > 3000 || idx === 0;
    seek(restartThis ? chapters[idx].start : chapters[idx - 1].start);
  } else if (chapters[idx + 1]) {
    seek(chapters[idx + 1].start);
  }
  emit("chapter");
}

// After a pause: rewind more the longer it was (a setting; the table comes from the server) and, after a
// break, look for a newer place on another device.
function afterBreak() {
  const away = idleAt ? Date.now() - idleAt : 0;
  idleAt = 0;
  let back = 0;
  for (const [from, seconds] of book.rewind) if (away >= from) back = seconds;
  if (back && book.autoRewind && bookPosition() > 0) {
    seek(Math.max(0, bookPosition() - back * 1000), false);
    hint(fill(msg("rewound"), { s: back }));
  }
  if (away >= BREAK_MS) checkRemote();
}

// audio.play() must run synchronously inside the click or key press, or browsers refuse it, so
// nothing here awaits before reaching it.
function play() {
  if (NATIVE) {
    if (book) audio.start(book.id);
    return;
  }
  if (!book || current.fileIdx < 0) return;
  afterBreak();
  if (!current.loaded) {
    setSource(current.fileIdx, current.startMs, true);
  } else {
    audio.play().catch(() => {});
  }
}

function pause() {
  compatSeq++; // also cancels a wait for a converted copy
  audio.pause();
}

function toggle() {
  if (audio.paused) play();
  else pause();
}

function skip(direction) {
  if (!book) return;
  const seconds = direction < 0 ? book.skipBack : book.skipForward;
  seek(bookPosition() + direction * seconds * 1000);
}

// ---- Volume: the person's level and the sleep timer's fade ----

// The level the person chose, kept on this device. The fade multiplies it and never replaces it.
function storedVolume() {
  try {
    const text = localStorage.getItem("ts.volume");
    const value = Number(text);
    return text !== null && value >= 0 && value <= 1 ? value : 1;
  } catch {
    return 1;
  }
}

function applyVolume() {
  audio.volume = clamp(userVolume * fade, 0, 1);
}

function setVolume(value) {
  userVolume = clamp(value, 0, 1);
  try {
    localStorage.setItem("ts.volume", String(userVolume));
  } catch {
    // no storage: the level lasts until the page reloads
  }
  applyVolume();
}

// fraction 1 is the person's level, 0 is silence.
function setFade(fraction) {
  fade = clamp(fraction, 0, 1);
  applyVolume();
}

// iOS ignores the volume property (it reads back 1): there the fade and the slider are not offered.
function probeVolume() {
  const before = audio.volume;
  audio.volume = 0.5;
  const works = audio.volume === 0.5;
  audio.volume = before;
  return works;
}

function setSpeed(value) {
  if (!book) return;
  book.speed = clamp(Math.round(value * 20) / 20, 0.5, 3); // steps of 0.05
  audio.defaultPlaybackRate = book.speed;
  audio.playbackRate = book.speed;
  emit("speed");
}

// ---- Saves: position goes to the server, safely ----

// A save is written to localStorage first and cleared when the server answers, so one lost to a
// closed tab or dead network is sent again by offline.js.
function pendingSaves() {
  try {
    return JSON.parse(localStorage.getItem("ts.pending")) || {};
  } catch {
    return {};
  }
}

function setPendingSave(bookId, body) {
  const all = pendingSaves();
  if (body) all[bookId] = body;
  else delete all[bookId];
  try {
    localStorage.setItem("ts.pending", JSON.stringify(all));
  } catch {
    // storage is optional
  }
}

function accrueHeard(stopping) {
  if (!heardSince) return;
  heardMs += perfNow() - heardSince;
  heardSince = stopping ? 0 : perfNow();
}

function progressUrl(bookId) {
  return "/progress/" + encode(bookId);
}

function savePayload(extra) {
  return new URLSearchParams({
    position_ms: Math.round(bookPosition()),
    client_updated_at: Date.now(),
    device_id: deviceId(),
    speed: audio.playbackRate.toFixed(2),
    ...extra,
  });
}

// fetch that answers null when signed out: a redirect, or anything but JSON.
async function api(url, init) {
  const res = await fetch(url, init);
  const isJson = (res.headers.get("Content-Type") || "").includes("json");
  return res.redirected || !isJson ? null : res;
}

function postProgress(bookId, body) {
  return api(progressUrl(bookId), {
    method: "POST",
    keepalive: true,
    body,
    headers: { "X-CSRF-Token": csrfToken(), "Content-Type": FORM_TYPE },
  });
}

function nearLastSave() {
  return Math.abs(bookPosition() - lastSavedMs) < 1000;
}

// Saves the position. kind: tick (every 10 s while playing), pause, seek, end (finished), force
// (keep this place after the question), beacon (the page is being hidden).
async function save(kind) {
  if (NATIVE) return;
  if (!book || stopped || current.fileIdx < 0) return;
  if (audio.ended && kind !== "end") return; // a book that ended keeps its finished mark
  if (kind === "tick" && (prompting || nearLastSave())) return;
  if (kind === "beacon" && (!current.loaded || (audio.paused && nearLastSave()))) return;

  const id = book.id;
  const position = Math.round(bookPosition());
  if (LINK) return saveLinkPlace(id, position, kind === "end");
  const extra = kind === "end" ? { finished: 1 } : kind === "force" ? { force: 1 } : {};
  const body = savePayload(extra);
  setPendingSave(id, body.toString());

  accrueHeard();
  const reportedMs = Math.round(heardMs);
  heardMs = 0;
  lastSavedMs = position;
  body.set("listened_ms", reportedMs);

  if (kind === "beacon") {
    // The page is going away, so fetch may not finish; the browser queues sendBeacon. It cannot
    // set headers, so the CSRF token goes in the body.
    body.set("_csrf", csrfToken());
    if (!navigator.sendBeacon(progressUrl(id), body)) heardMs += reportedMs;
    return;
  }

  let res;
  try {
    res = await postProgress(id, body.toString());
  } catch {
    heardMs += reportedMs;
    return;
  }
  if (!res) return signedOut();
  if (!res.ok) {
    heardMs += reportedMs;
    return;
  }
  setPendingSave(id, null);
  const answer = await res.json();
  fire(document.body, "progress-saved", { bookId: id, positionMs: position, applied: answer.applied });
  // the server kept a newer place from another device
  if (!answer.applied && book && book.id === id) maybeAskContinue(answer.progress);
}

// A redirect to sign-in means the session ended: say so once.
function signedOut() {
  if (stopped) return;
  stopped = true;
  toast("err", msg("signedOut"), [{ label: msg("signIn"), run: () => location.assign("/login") }]);
}

// ---- Prompt: continue where another device left off ----

async function checkRemote() {
  if (NATIVE) return;
  if (!book || stopped || LINK) return;
  try {
    const res = await api(progressUrl(book.id));
    if (res) maybeAskContinue((await res.json()).progress);
  } catch {
    // the next check will do
  }
}

// Asks Continue there? when another device is over a break away. Nothing moves until a button is pressed.
function maybeAskContinue(remote) {
  if (!remote || prompting || remote.finished) return;
  if (remote.deviceId === deviceId() || remote.deviceId === "server") return;
  if (Math.abs(remote.positionMs - bookPosition()) <= BREAK_MS) return;

  prompting = true;
  let question = null;
  const close = () => {
    prompting = false;
    dismissToast(question);
  };
  const text = fill(msg("continueAsk"), { time: clock(remote.positionMs), device: remote.deviceLabel || msg("otherDevice") });
  question = toast("status", text, [
    { label: msg("continueThere"), run: () => { close(); seek(remote.positionMs); } },
    { label: msg("stayHere"), run: () => { close(); save("force"); } },
  ]);
}

// ---- Screen: the bar and its state event ----

// Writes name, chapter, labels and progress into the bar. force also updates the Media Session.
function render(force) {
  if (!book) return;
  lastRenderAt = perfNow();
  const position = bookPosition();
  const chapterIdx = chapterIndexAt(position);
  const chapter = book.chapters[chapterIdx];
  const minutesLeft = Math.max(1, Math.ceil((chapter.end - position) / 60000));
  const setLabel = (name, text) => controls[name].setAttribute("aria-label", text);

  controls.title.textContent = book.title;
  controls.open.dataset.letter = book.title.charAt(0).toUpperCase();
  controls.meta.textContent = fill(msg("chapterLeft"), { n: chapterIdx + 1, m: minutesLeft });
  controls.open.href = LINK || "/books/" + encode(book.id);
  setLabel("open", fill(msg("openPlayer") || msg("open"), { title: book.title, n: chapterIdx + 1, m: minutesLeft }));
  if (book.cover && controls.cover.getAttribute("src") !== book.cover) controls.cover.src = book.cover;
  controls.cover.hidden = !book.cover;
  controls["back-num"].textContent = book.skipBack;
  controls["forward-num"].textContent = book.skipForward;
  setLabel("back", fill(msg("backSeconds"), { n: book.skipBack }));
  setLabel("forward", fill(msg("forwardSeconds"), { n: book.skipForward }));
  setLabel("toggle", (audio.paused ? msg("playPrefix") : msg("pausePrefix")) + " " + book.title);
  slot.classList.toggle("is-playing", !audio.paused);
  const fraction = book.durationMs ? clamp(position / book.durationMs, 0, 1).toFixed(4) : "0";
  controls["mini-progress"].style.setProperty("--ts-progress", fraction);
  if (force) updateMediaSession();
}

function state() {
  if (!book) return { bookId: "", playing: false };
  return {
    bookId: book.id,
    title: book.title,
    positionMs: Math.round(bookPosition()),
    durationMs: book.durationMs,
    playing: !audio.paused,
    speed: book.speed,
    volume: userVolume,
    fileIdx: NATIVE ? book.files.findLastIndex((f) => f.offset <= bookPosition()) : current.fileIdx,
    chapterIdx: chapterIndexAt(bookPosition()),
  };
}

// Redraws and tells the page (ts:player:state).
function emit(kind) {
  render(true);
  syncPageButtons();
  updateSuppression();
  fire(document, "ts:player:state", { kind, ...state() });
}

// ---- One resume per screen ----

// A page showing its own resume button for the loaded book hides the bar, so a screen never has
// two. An observer watches those buttons come into and leave view. While the book plays the bar
// stays on every page instead, and those buttons become Pause controls (syncPageButtons).
let resumeWatcher = null;
const visibleResumeButtons = new Set();

function setSuppressed(suppressed) {
  slot.classList.toggle("is-suppressed", suppressed);
  slot.inert = suppressed; // a hidden bar must not be reachable by keyboard either
}

// Hides the bar while a resume button for the paused book is in view; never while it plays.
function updateSuppression() {
  setSuppressed(!!book && audio.paused && visibleResumeButtons.size > 0);
}

// The loaded book's buttons on this page: Pause while it plays, their own words otherwise. The
// server's label and aria-label are kept in data attributes so pausing restores them. The play
// icon is hidden with CSSOM (an SVG ignores the hidden attribute); the pause icon sits in a span
// that is hidden without JavaScript, shown here as display: contents so layout is unchanged.
function setDisplay(el, value) {
  el.style.setProperty("display", value); // an empty value removes the declaration
}

function syncPageButtons() {
  const playing = !!book && !audio.paused;
  for (const el of document.querySelectorAll("[data-ts-play]")) {
    const pauses = playing && el.dataset.tsPlay === book.id;
    if ((el.dataset.tsPausing === "1") === pauses) continue;
    const label = el.querySelector("[data-ts-play-label]");
    const pauseIcon = el.querySelector("[data-ts-pause-icon]");
    const playIcon = el.querySelector("svg");
    // The hero's button of the playing book shows the player instead of pausing (one Pause per screen).
    const shows = pauses && el.hasAttribute("data-ts-show-player");
    const eyebrow = el.closest(".ts-hero")?.querySelector("[data-ts-hero-eyebrow]");
    if (pauses) {
      const verb = msg(shows ? "showPlayer" : "pausePrefix");
      if (label) {
        el.dataset.tsOwnLabel = label.textContent;
        label.textContent = verb;
      }
      if (el.hasAttribute("aria-label")) {
        el.dataset.tsOwnAria = el.getAttribute("aria-label");
        el.setAttribute("aria-label", shows ? verb : verb + " " + book.title);
      }
      if (shows && eyebrow) {
        eyebrow.dataset.tsOwnEyebrow = eyebrow.textContent;
        eyebrow.textContent = msg("nowPlaying");
      }
    } else {
      if (label) label.textContent = el.dataset.tsOwnLabel;
      if (el.dataset.tsOwnAria !== undefined) el.setAttribute("aria-label", el.dataset.tsOwnAria);
      if (eyebrow && eyebrow.dataset.tsOwnEyebrow) eyebrow.textContent = eyebrow.dataset.tsOwnEyebrow;
    }
    if (playIcon) setDisplay(playIcon, pauses ? "none" : "");
    if (pauseIcon) {
      pauseIcon.hidden = !pauses || shows;
      setDisplay(pauseIcon, pauses && !shows ? "contents" : "");
    }
    el.dataset.tsPausing = pauses ? "1" : "0";
  }
}

function watchPageResumeButtons() {
  if (resumeWatcher) resumeWatcher.disconnect();
  visibleResumeButtons.clear();
  syncPageButtons();
  updateSuppression();
  if (!book) return;
  resumeWatcher = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting) visibleResumeButtons.add(entry.target);
      else visibleResumeButtons.delete(entry.target);
    }
    updateSuppression();
  });
  for (const el of document.querySelectorAll("[data-ts-play]")) {
    if (el.dataset.tsPlay === book.id) resumeWatcher.observe(el);
  }
}

// Buttons that play from a chosen time (bookmarks) stay hidden until this script runs.
function revealPlayAtButtons() {
  for (const el of document.querySelectorAll("[data-ts-play-at][hidden]")) {
    el.hidden = false;
  }
}

// ---- Media Session: lock screen and headset controls ----

const hasMediaSession = !NATIVE && "mediaSession" in navigator;
const mediaSessionActions = []; // the actions the browser accepted; read by tests

function updateMediaSession() {
  if (!hasMediaSession || !book) return;
  const chapter = book.chapters[chapterIndexAt(bookPosition())];
  const durationSeconds = book.durationMs / 1000;
  try {
    navigator.mediaSession.metadata = new MediaMetadata({
      title: book.title,
      artist: book.author,
      album: chapter.title,
      artwork: book.cover ? [{ src: book.cover, sizes: "512x512" }] : [],
    });
    if (durationSeconds) {
      navigator.mediaSession.setPositionState({
        duration: durationSeconds,
        position: clamp(bookPosition() / 1000, 0, durationSeconds),
        playbackRate: audio.playbackRate || 1,
      });
    }
  } catch {
    // an unsupported field is skipped
  }
}

if (hasMediaSession) {
  const handlers = {
    play,
    pause,
    stop: pause,
    seekbackward: () => skip(-1),
    seekforward: () => skip(1),
    seekto: (details) => seek(details.seekTime * 1000),
    previoustrack: () => jumpChapter(-1),
    nexttrack: () => jumpChapter(1),
  };
  for (const action in handlers) {
    try {
      navigator.mediaSession.setActionHandler(action, handlers[action]);
      mediaSessionActions.push(action);
    } catch {
      // not supported in this browser
    }
  }
}

// ---- Loading a book ----

// Makes newBook the loaded one and shows the bar. A switch saves the old book first. False when
// no file is playable.
function adopt(newBook, posMs) {
  if (book && book.id !== newBook.id) {
    save("pause");
    heardSince = 0;
    current.loaded = false;
    audio.removeAttribute("src");
    audio.load();
  }
  book = newBook;
  prompting = false;

  if (current.fileId) {
    // sound already started from a link, which knew only the file's id
    current.fileIdx = newBook.files.findIndex((f) => f.id === current.fileId);
  } else {
    const target = locate(posMs) || locate(0);
    if (!target) return false;
    if (!current.loaded || current.fileIdx !== target.fileIdx) {
      current = { fileIdx: target.fileIdx, startMs: target.offsetMs, loaded: false, compat: false };
    }
  }

  lastSavedMs = -1;
  slot.hidden = false;
  document.body.classList.add("has-player");
  emit("load");
  watchPageResumeButtons();
  return true;
}

// Fetches and adopts a book. options.positionMs overrides the saved place; options.play plays.
async function loadBook(id, options) {
  const fetched = await fetchBook(id, options && options.init);
  const given = options && options.positionMs;
  const savedStart = fetched && fetched.stored && !fetched.stored.finished ? fetched.stored.positionMs : 0;
  if (!fetched || !adopt(fetched, given !== undefined ? given : savedStart)) return false;

  idleAt = given === undefined && fetched.stored ? fetched.stored.updatedAt : 0;
  if (current.fileId) {
    // a click started sound before this data arrived: settings and the break rule catch up
    delete current.fileId;
    audio.defaultPlaybackRate = fetched.speed;
    if (!audio.paused) audio.playbackRate = fetched.speed;
    if (!fetched.stored || Math.abs(bookPosition() - fetched.stored.positionMs) > 2000) idleAt = 0;
    afterBreak();
  } else if (options && options.play) {
    if (NATIVE && given !== undefined) audio.aim(given);
    play();
  }
  return true;
}

// The resume link names its file, so sound starts inside the click, before the fragment arrives.
// The book data follows and takes over (loadBook).
function startFromLink(data) {
  if (NATIVE) return loadBook(data.tsPlay, { play: true });
  if (book) {
    save("pause");
    book = null;
  }
  const startMs = Number(data.tsStartMs) || 0;
  const localStartMs = Math.max(0, startMs - (Number(data.tsFileStartMs) || 0));
  if (data.tsMayNeedCompat === "1" && data.tsMime && audio.canPlayType(data.tsMime) === "") {
    // needs the converted copy, which setSource handles once the book is known
    return loadBook(data.tsPlay, { positionMs: startMs, play: true });
  }
  current = { fileIdx: -1, fileId: data.tsFile, startMs: localStartMs, loaded: true, compat: false };
  audio.defaultPlaybackRate = 1;
  audio.src = LINK + "/stream/" + encode(data.tsFile) + "#t=" + (localStartMs / 1000).toFixed(3);
  audio.play().catch(() => {});
  return loadBook(data.tsPlay);
}

// ---- Clicks and keys ----

// One listener for every play button, so buttons that htmx swaps in just work.
listen(document, "click", (event) => {
  const el = event.target.closest("[data-ts-play], [data-ts-play-at]");
  if (!el) return;
  const data = el.dataset;
  event.preventDefault();
  performance.mark("ts-play-click"); // make perf and make player measure from here

  if (data.tsPlayAt !== undefined) {
    // a bookmark or note: play from a chosen time
    const ms = Number(data.tsPlayAt) || 0;
    if (book && book.id === data.tsBook) {
      seek(ms, true);
      play();
    } else {
      loadBook(data.tsBook, { positionMs: ms, play: true });
    }
    return;
  }

  if (book && book.id === data.tsPlay && !audio.paused) {
    // the hero's button of the playing book shows the player (the panel on a desktop)
    if (el.hasAttribute("data-ts-show-player")) {
      if (!document.getElementById("player-full").open) slot.querySelector("[data-ts-open]").click();
      return;
    }
    // the page button of the playing book is a Pause control
    pause();
    return;
  }
  wantsFirstSound = true;
  if (book && book.id === data.tsPlay) {
    // a finished book that is still loaded (the Listen again link) starts at the beginning of the book
    if (data.tsFromStart === "1") seek(0, true);
    play();
  } else if (data.tsFile) startFromLink(data);
  else loadBook(data.tsPlay, { play: true });
});

listen(slot, "click", (event) => {
  const button = event.target.closest("button");
  if (button === controls.toggle) toggle();
  else if (button === controls.back) skip(-1);
  else if (button === controls.forward) skip(1);
});

// Space and arrows (shell.js owns the navigation keys). Not in fields, dialogs or the sheet, and
// Space not on a button or link, where it already means press.
listen(document, "keydown", (event) => {
  if (!book || event.ctrlKey || event.metaKey || event.altKey) return;
  const target = event.target instanceof Element ? event.target : document.body;
  const inField = target.closest("input, textarea, select, [contenteditable]:not([contenteditable=false])");
  if (inField || document.querySelector("dialog[open]:not(.ts-player), #sheet .ts-sheet")) return; // the full player is not a blocker
  if (event.key === " " && !target.closest("button, a, summary, [role=button]")) toggle();
  else if (event.key === "ArrowLeft") skip(-1);
  else if (event.key === "ArrowRight") skip(1);
  else return;
  event.preventDefault();
});

// ---- Audio events ----

listen(audio, "play", () => emit("play"));

listen(audio, "playing", () => {
  heardSince = perfNow();
  idleAt = 0;
  render(true);
});

listen(audio, "waiting", () => accrueHeard(true)); // buffering is not listening

listen(audio, "pause", () => {
  if (audio.ended || !book) return; // the end has its own handler
  accrueHeard(true);
  idleAt = Date.now();
  save("pause");
  emit("pause");
});

listen(audio, "timeupdate", () => {
  // first sound: the clock is 50 ms past the start, so something was audible
  if (wantsFirstSound && !audio.paused && audio.currentTime * 1000 >= current.startMs + 50) {
    wantsFirstSound = false;
    performance.mark("ts-first-sound");
  }
  if (perfNow() - lastRenderAt >= 1000) {
    render();
    updateMediaSession();
    if (!audio.paused) fire(document, "ts:player:tick", state());
  }
  warmNextFile();
});

// Near the end of a file, fetch the next one's first bytes so the switch has no gap.
function warmNextFile() {
  if (!book || current.prefetched) return;
  const secondsLeft = audio.duration - audio.currentTime; // NaN until the duration is known
  if (!(secondsLeft < 20)) return;
  current.prefetched = true;
  const next = nextPlayableIndex(current.fileIdx);
  if (next >= 0 && !book.files[next].mayCompat) {
    fetch(streamUrl(book.files[next]), { headers: { Range: "bytes=0-65535" } }).catch(() => {});
  }
}

listen(audio, "ended", () => {
  if (NATIVE) {
    emit("pause");
    return;
  }
  if (!book) return;
  accrueHeard(true);
  const next = nextPlayableIndex(current.fileIdx);
  if (next >= 0) {
    setSource(next, 0, true);
    emit("chapter");
    return;
  }
  // the last file ended: the book is finished, then Up next plays its first book
  save("end").then(playNext);
  idleAt = Date.now();
  emit("pause");
});

// The server pops the first book of Up next and drops the ended one from it (204 when empty).
// Nothing happens in link mode, or when the person already started something else while the
// place was being saved (then the audio is no longer ended).
function playNext() {
  if (LINK || !audio.ended || !book) return;
  loadBook("next", {
    play: true,
    init: {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken(), "Content-Type": FORM_TYPE },
      body: "ended=" + encode(book.id),
    },
  });
}

listen(audio, "error", () => {
  if (NATIVE) {
    toast("err", msg("nativeFailed"));
    emit("error");
    return;
  }
  if (!book || !current.loaded) return;
  const file = book.files[current.fileIdx];
  const unsupported = audio.error && audio.error.code === 4; // MEDIA_ERR_SRC_NOT_SUPPORTED
  if (unsupported && file && file.mayCompat && !current.compat) {
    useCompatCopy(current.fileIdx, current.startMs, true);
    return;
  }
  toast("err", msg("compatFailed"));
  emit("error");
});

// ---- Leaving the page ----

// One beacon per hide: pagehide and visibilitychange can both fire for one departure.
function sendBeaconOnce() {
  if (beaconSent) return;
  beaconSent = true;
  save("beacon");
}

listen(document, "visibilitychange", () => {
  if (document.visibilityState === "hidden") {
    hiddenAt = Date.now();
    sendBeaconOnce();
    return;
  }
  beaconSent = false;
  if (hiddenAt && Date.now() - hiddenAt >= BREAK_MS) checkRemote();
  hiddenAt = 0;
});
listen(window, "pagehide", sendBeaconOnce);

// ---- Start-up ----

setInterval(() => {
  if (!audio.paused) save("tick");
}, SAVE_EVERY_MS);

// htmx swapped in content: watch its resume buttons, show its bookmark buttons.
listen(document, "htmx:after:settle", () => {
  watchPageResumeButtons();
  revealPlayAtButtons();
});

// Speed changes keep the voice's pitch; the stored level applies from the start.
audio.preservesPitch = true;
if (NATIVE) {
  // The app is the truth: a book it plays that the page has not loaded is loaded now, and its speed is the page's.
  const takeSpeed = () => {
    if (book) {
      book.speed = audio.playbackRate;
      emit("speed");
    }
  };
  // The speed the app pushed before the book was loaded is taken once it is.
  listen(audio, "nativebook", () => loadBook(audio.nativeBookId).then(takeSpeed));
  listen(audio, "ratechange", takeSpeed);
  audio.hello();
}
const canSetVolume = probeVolume();
applyVolume();

// The loaded book as player-full.js needs it (copies), or null.
function info() {
  if (!book) return null;
  const { id, title, author, cover, durationMs, skipBack, skipForward, tint, readHref } = book;
  return { id, title, author, cover, durationMs, skipBack, skipForward, tint, readHref, accents: { ...book.accents }, chapters: book.chapters.map((c) => ({ ...c })),
    files: book.files.map(({ id, offset, dur, playable }) => ({ id, offset, dur, playable })), // for the waveform
  };
}

// The public surface; debug is for tests only.
window.tsPlayer = {
  state,
  info,
  load: loadBook,
  play,
  pause,
  toggle,
  seek: (ms) => seek(ms),
  skip,
  chapter: jumpChapter,
  setSpeed,
  setVolume,
  setFade,
  canSetVolume,
  toast: timedToast,
  debug: {
    setPausedAt: (ms) => {
      idleAt = ms;
    },
    actions: mediaSessionActions,
    locate,
    chapterIdx: chapterIndexAt,
  },
};

fire(document, "ts:player:ready"); // player-full.js is an async module and may have run first
revealPlayAtButtons();

// Show the last played book in the bar, paused.
fetchBook("").then((fetched) => {
  if (!fetched || book) return;
  idleAt = fetched.stored ? fetched.stored.updatedAt : 0;
  adopt(fetched, fetched.stored && !fetched.stored.finished ? fetched.stored.positionMs : 0);
  if (LINK) showLinkResume(fetched.id);
});

performance.mark("ts-player-ready");
