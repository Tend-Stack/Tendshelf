// Tendshelf Eyes-free mode (M3b, decision 0013).
//
// Five big buttons and no text to follow, for the car or a walk. It is a module of its own, imported
// by player-full.js the first time the person picks "Eyes-free" in the player's menu (the service
// worker precaches it). It never touches #audio: it drives playback through window.tsPlayer and the
// sleep timer and bookmark through window.tsPlayerFull. Every word comes from the data-ts-msg-*
// attributes of #eyes-free; no inline style, no eval.

const DIM_MS = 10000; // the screen dims after this long without a touch or key
const VIBRATE_MS = 40; // a short tick where the device can
const SLEEP_CYCLE = ["15", "30", "60", "chapter", "off"]; // what the Sleep button steps through

const dialog = document.getElementById("eyes-free");
const part = (name) => dialog.querySelector("[data-ts-ef-" + name + "]");
const ui = {};
for (const name of ["close", "title", "chapter", "back", "back-text", "forward", "forward-text", "toggle", "toggle-text",
  "icon-play", "icon-pause", "bookmark", "sleep", "sleep-text", "status"]) {
  ui[name] = part(name);
}

let listeners = []; // removed again when the dialog closes
let dimTimer = 0;
let wakeLock = null;
let opener = null;
let link = null; // the stylesheet <link>, added once

function msg(name) {
  return dialog.dataset["tsMsg" + name[0].toUpperCase() + name.slice(1)] || "";
}

function fill(message, values) {
  return message.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? "");
}

function on(target, type, handler, options) {
  target.addEventListener(type, handler, options);
  listeners.push(() => target.removeEventListener(type, handler, options));
}

function setText(el, text) {
  if (el.textContent !== text) el.textContent = text;
}

// The stylesheet is fetched with the first opening, and the dialog waits for it so it never shows unstyled.
function loadStyles(href) {
  if (link || !href) return Promise.resolve();
  link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = href;
  const loaded = new Promise((resolve) => {
    link.addEventListener("load", resolve, { once: true });
    link.addEventListener("error", resolve, { once: true });
  });
  document.head.append(link);
  return loaded;
}

// ---- What the buttons show ----

function sleepWords() {
  const sleep = window.tsPlayerFull.sleepInfo();
  if (sleep.mode === "off") return msg("sleep");
  if (sleep.choice === "chapter") return msg("sleepChapter");
  const minutes = Number(sleep.choice) || Math.max(1, Math.ceil(sleep.leftMs / 60000));
  return fill(msg("sleepMinutes"), { n: minutes });
}

function draw() {
  const player = window.tsPlayer;
  const book = player.info();
  if (!book) return;
  const state = player.state();
  const chapter = book.chapters[state.chapterIdx] || book.chapters[0];
  const minutes = Math.max(1, Math.ceil((chapter.end - state.positionMs) / 60000));
  setText(ui.title, book.title);
  setText(ui.chapter, fill(msg("chapterLine"), { n: state.chapterIdx + 1, m: minutes }));
  setText(ui["back-text"], fill(msg("skipBack"), { n: book.skipBack }));
  setText(ui["forward-text"], fill(msg("skipForward"), { n: book.skipForward }));
  ui.back.setAttribute("aria-label", fill(msg("skipBackLabel"), { n: book.skipBack }));
  ui.forward.setAttribute("aria-label", fill(msg("skipForwardLabel"), { n: book.skipForward }));
  setText(ui["toggle-text"], state.playing ? msg("pause") : msg("play"));
  ui["icon-play"].hidden = state.playing;
  ui["icon-pause"].hidden = !state.playing;
  setText(ui["sleep-text"], sleepWords());
}

// What a screen reader hears after a press (the visible buttons change as well).
function say(text) {
  ui.status.textContent = "";
  ui.status.textContent = text;
}

// ---- The buttons ----

// Off, 15, 30, 60, end of chapter, off. The timer starts inside the tap, so iOS can ask for motion access.
function cycleSleep() {
  const full = window.tsPlayerFull;
  const info = full.sleepInfo();
  const at = info.mode === "off" ? -1 : SLEEP_CYCLE.indexOf(info.choice);
  const next = at === -1 && info.mode !== "off" ? "off" : SLEEP_CYCLE[at + 1];
  full.sleep(next, Number(next) || 0);
  draw();
  say(next === "off" ? msg("sleepOff") : sleepWords());
}

function press(event) {
  const zone = event.target.closest(".ts-eyesfree__zone");
  if (!zone) return;
  const player = window.tsPlayer;
  if (navigator.vibrate) navigator.vibrate(VIBRATE_MS);
  if (zone === ui.back) player.skip(-1);
  else if (zone === ui.forward) player.skip(1);
  else if (zone === ui.toggle) player.toggle();
  else if (zone === ui.bookmark) window.tsPlayerFull.bookmark({ quiet: true });
  else if (zone === ui.sleep) cycleSleep();
  draw();
}

function onKey(event) {
  restartDim();
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  if (dialog.classList.contains("is-dim")) {
    // a key on a dimmed screen only wakes it, like the first tap
    event.preventDefault();
    wake();
    return;
  }
  if (event.key === "ArrowLeft") window.tsPlayer.skip(-1);
  else if (event.key === "ArrowRight") window.tsPlayer.skip(1);
  else return;
  event.preventDefault();
}

// ---- Dimming ----

function wake() {
  dialog.classList.remove("is-dim");
  restartDim();
}

function restartDim() {
  clearTimeout(dimTimer);
  dimTimer = setTimeout(() => dialog.classList.add("is-dim"), DIM_MS);
}

// The first tap on a dimmed screen only wakes it: it is stopped before any button sees it.
function onClickCapture(event) {
  if (!dialog.classList.contains("is-dim")) return;
  event.preventDefault();
  event.stopPropagation();
  wake();
}

// ---- Keeping the screen on ----

async function requestWakeLock() {
  if (!navigator.wakeLock || document.visibilityState !== "visible" || wakeLock) return;
  try {
    const lock = await navigator.wakeLock.request("screen");
    if (dialog.open && !wakeLock) wakeLock = lock;
    else releaseLock(lock); // closed while the request was on its way
  } catch {
    // refused (battery saver, no permission): the screen may dim by itself, nothing else breaks
  }
}

function releaseLock(lock) {
  if (lock) Promise.resolve(lock.release()).catch(() => {});
}

function onVisibility() {
  if (document.visibilityState === "visible") {
    requestWakeLock();
  } else {
    releaseLock(wakeLock);
    wakeLock = null;
  }
}

// ---- Opening and closing ----

function onClose() {
  clearTimeout(dimTimer);
  dialog.classList.remove("is-dim");
  releaseLock(wakeLock);
  wakeLock = null;
  for (const remove of listeners) remove();
  listeners = [];
  if (opener && opener.isConnected) opener.focus();
  opener = null;
}

export async function openEyesFree(from) {
  if (dialog.open) return;
  const item = document.querySelector("[data-ts-eyes-free-open]");
  await loadStyles(item && item.dataset.tsCss);
  opener = from;
  draw();
  dialog.showModal();
  ui.toggle.focus();
  on(dialog, "click", onClickCapture, true);
  on(dialog, "click", press);
  on(dialog, "pointerdown", restartDim);
  on(dialog, "keydown", onKey);
  on(dialog, "close", onClose, { once: true });
  on(ui.close, "click", () => dialog.close());
  on(document, "ts:player:tick", draw);
  on(document, "ts:player:state", draw);
  on(document, "visibilitychange", onVisibility);
  restartDim();
  requestWakeLock();
}
