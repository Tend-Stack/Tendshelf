// Cast sender (decision 0047, docs/plans/casting.md). The page of a person who turned casting on loads this file
// (layout.templ); it is the only code that touches Google's Cast Web Sender SDK.
//
// What it does: on the first open of the full player it loads Google's sender script (the one third-party script
// Tendshelf ever loads, allowed only by the per-response CSP of that person's pages), shows the Cast button once a
// device is around, and on a click asks the server for a private address (POST /casting/address), then tells the
// device to play the book's files from that address through the Default Media Receiver.
//
// It talks to player.js only through events on document, so the player keeps the one save path for progress:
//   ts:cast:begin    {device, positionMs}  cast.js -> player.js  a device took over
//   ts:cast:position {positionMs, paused}  cast.js -> player.js  the device's place in the whole book
//   ts:cast:end      {positionMs}          cast.js -> player.js  the device stopped; the local player takes the place
//   ts:cast:command  {action, positionMs}  player.js -> cast.js  "play", "pause", "seek" or "stop"
// A station is cast like a book with one live item and no place (docs/plans/casting.md, P3).
//
// CSP: no inline script or style, no eval. Every word comes from data-ts-msg-* attributes on #player.

const SDK_URL = "https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1";
const FORM_TYPE = "application/x-www-form-urlencoded";
const ADDRESS_SPARE_MS = 60000; // a minted address is reused until this close to its end

const slot = document.getElementById("player");
const audio = document.getElementById("audio"); // only to ask what this browser can decode
const html = document.documentElement;

let available = false; // the framework reported at least one device
let loadingSdk = false;
let context = null; // cast.framework.CastContext
let remote = null; // cast.framework.RemotePlayer
let controller = null; // cast.framework.RemotePlayerController
let active = null; // while a device plays: {src, base, files, device, lastMs, startedLoad}
let address = null; // {bookId, base, expiresAt}
let hinted = false;
let quiet = false; // set while this file ends a session itself: no ts:cast:end then

// ---- Small helpers ----

function msg(name) {
  const key = "tsMsg" + name.charAt(0).toUpperCase() + name.slice(1);
  return slot.dataset[key] || "";
}

function fire(name, detail) {
  document.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));
}

function toast(kind, text) {
  if (window.tsPlayer) window.tsPlayer.toast(kind, text);
}

function csrfToken() {
  try {
    return JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return "";
  }
}

// Whether this window may cast at all. The server already withholds this file in the apps' windows and for a person
// who has casting off; these checks are the browser's half (the SDK needs a secure context and Chrome's Media Router).
function mayCast() {
  if (html.dataset.shell || document.body.dataset.tsLink) return false;
  if (!window.isSecureContext) return false;
  const brands = navigator.userAgentData ? navigator.userAgentData.brands : [];
  return brands.some((b) => /Chromium|Google Chrome/.test(b.brand));
}

// ---- Loading Google's script, only when the full player is first opened ----

function loadSdk() {
  if (loadingSdk) return;
  loadingSdk = true;
  if (window.cast && window.cast.framework) {
    startFramework(); // the framework is already here (a test double, or a page that loaded it before)
    return;
  }
  window.__onGCastApiAvailable = (isAvailable) => {
    if (isAvailable) startFramework();
  };
  const script = document.createElement("script");
  script.src = SDK_URL;
  script.async = true;
  // Blocked or offline: the button simply never shows and nothing else changes.
  script.addEventListener("error", () => {});
  document.head.append(script);
}

function watchFullPlayer() {
  const dialog = document.getElementById("player-full");
  if (!dialog) return;
  if (dialog.open) return loadSdk();
  const observer = new MutationObserver(() => {
    if (!dialog.open) return;
    observer.disconnect();
    loadSdk();
  });
  observer.observe(dialog, { attributes: true, attributeFilter: ["open"] });
}

function startFramework() {
  context = cast.framework.CastContext.getInstance();
  context.setOptions({
    receiverApplicationId: chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
    autoJoinPolicy: chrome.cast.AutoJoinPolicy.ORIGIN_SCOPED,
  });
  remote = new cast.framework.RemotePlayer();
  controller = new cast.framework.RemotePlayerController(remote);

  const types = cast.framework.CastContextEventType;
  context.addEventListener(types.CAST_STATE_CHANGED, (event) => setAvailable(event.castState));
  context.addEventListener(types.SESSION_STATE_CHANGED, onSession);
  setAvailable(context.getCastState());

  const remoteTypes = cast.framework.RemotePlayerEventType;
  for (const type of [remoteTypes.CURRENT_TIME_CHANGED, remoteTypes.IS_PAUSED_CHANGED, remoteTypes.MEDIA_INFO_CHANGED]) {
    controller.addEventListener(type, report);
  }
  controller.addEventListener(remoteTypes.PLAYER_STATE_CHANGED, onPlayerState);
}

function setAvailable(castState) {
  available = castState !== cast.framework.CastState.NO_DEVICES_AVAILABLE;
  paint();
}

// ---- The button ----

function buttons() {
  return document.querySelectorAll("[data-ts-cast]");
}

// A share link never casts (castSource answers null there); a station casts like a book, with no place to carry over.
function castable() {
  const state = window.tsPlayer ? window.tsPlayer.state() : null;
  return !!state && state.bookId !== "";
}

function fill(text, values) {
  return text.replace(/\{(\w+)\}/g, (all, key) => (key in values ? values[key] : all));
}

function paint() {
  const shown = available && (castable() || !!active);
  const where = active ? fill(msg("castWhere"), { device: active.device }) : "";
  for (const button of buttons()) {
    button.hidden = !shown;
    button.setAttribute("aria-pressed", active ? "true" : "false");
    button.classList.toggle("is-casting", !!active);
    button.setAttribute("aria-label", active ? msg("casting") + ". " + where : msg("castLabel"));
    const label = button.querySelector("[data-ts-cast-label]");
    if (label) label.textContent = active ? msg("casting") : msg("cast");
  }
  for (const line of document.querySelectorAll("[data-ts-cast-where]")) {
    line.hidden = !active;
    line.textContent = where;
  }
}

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-ts-cast]");
  if (!button || !context) return;
  // With a session already running, Chrome's own dialog offers to stop it.
  context.requestSession().catch(() => {});
});

document.addEventListener("ts:player:state", paint);

// ---- Starting a session ----

function onSession(event) {
  const states = cast.framework.SessionState;
  if (event.sessionState === states.SESSION_STARTED) {
    begin();
  } else if (event.sessionState === states.SESSION_ENDED && active) {
    finish(active.lastMs);
  }
}

// POST /casting/address: the private address for this book or episode. Rejects with the HTTP status (0: no answer).
async function mint(src) {
  if (address && address.bookId === src.id && address.expiresAt - Date.now() > ADDRESS_SPARE_MS) return address;
  const body = new URLSearchParams({ kind: src.kind, id: src.id });
  let res;
  try {
    res = await fetch("/casting/address", {
      method: "POST",
      body: body.toString(),
      headers: { "X-CSRF-Token": csrfToken(), "Content-Type": FORM_TYPE },
    });
  } catch {
    throw 0;
  }
  if (!res.ok) throw res.status;
  const answer = await res.json();
  address = { bookId: src.id, base: answer.base, expiresAt: answer.expiresAt };
  return address;
}

function endSession() {
  quiet = true;
  try {
    context.endCurrentSession(true);
  } finally {
    quiet = false;
  }
}

async function begin() {
  const session = context.getCurrentSession();
  const src = window.tsPlayer ? window.tsPlayer.castSource() : null;
  if (!session || !src) {
    endSession();
    return;
  }
  const device = session.getCastDevice().friendlyName;
  let minted;
  try {
    minted = await mint(src);
  } catch (status) {
    endSession();
    toast("err", status === 409 ? msg("castUnreachable") : msg("castFailed"));
    return;
  }
  if (context.getCurrentSession() !== session) return; // the person ended it while the address was being made
  active = { src, base: minted.base, device, lastMs: src.positionMs };
  try {
    if (src.kind === "station") active.liveType = await freeLiveSlot(minted.base);
    if (context.getCurrentSession() !== session) throw new Error("ended");
    await load(src.positionMs, true);
  } catch {
    active = null;
    endSession();
    toast("err", msg("castFailed"));
    paint();
    return;
  }
  fire("ts:cast:begin", { device, positionMs: src.positionMs });
  paint();
  if (!hinted && src.kind !== "station") { // a station keeps no place, so the page need not stay open
    hinted = true;
    toast("hint", msg("castKeepOpen"));
  }
}

// ---- A station: the server lets one stream run per person and station ----

const SLOT_TRIES = 8;

// A station's relay holds a stream slot for the person, so the page's own stream must be closed before the device
// opens the address, or the device would get a 503. Stop the local stream, then ask the address with HEAD (it takes
// and frees the same slot) until it answers; the answer's type is the one the device is told. Throws when it never does.
async function freeLiveSlot(base) {
  window.tsPlayer.pause(); // a station's pause drops the stream
  for (let attempt = 0; attempt < SLOT_TRIES; attempt++) {
    const res = await fetch(base + "/live", { method: "HEAD" });
    if (res.ok) return (res.headers.get("Content-Type") || "audio/mpeg").split(";")[0];
    if (res.status !== 503) throw res.status;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw 503;
}

// ---- Telling the device what to play ----

// A station: one live item with no start place, no artwork (the address has no art route for a station).
function liveMedia(base) {
  const media = new chrome.cast.media.MediaInfo(base + "/live", active.liveType);
  media.streamType = chrome.cast.media.StreamType.LIVE;
  const meta = new chrome.cast.media.GenericMediaMetadata();
  meta.title = active.src.title;
  meta.subtitle = active.src.author;
  media.metadata = meta;
  return media;
}

function mediaFor(file, base) {
  const compat = file.mayCompat && audio && audio.canPlayType(file.mime) === "";
  let url = active.src.kind === "episode" ? base + "/episode" : base + "/audio/" + encodeURIComponent(file.id);
  if (compat) url += "?compat=1";
  const media = new chrome.cast.media.MediaInfo(url, file.mime || "audio/mpeg");
  media.streamType = chrome.cast.media.StreamType.BUFFERED;
  const meta = new chrome.cast.media.GenericMediaMetadata();
  meta.title = active.src.title;
  meta.subtitle = active.src.author;
  meta.images = [new chrome.cast.Image(base + "/art")];
  media.metadata = meta;
  return media;
}

// The playable files of the book, in order, as the device's queue; the queue starts at the file holding positionMs.
function load(positionMs, autoplay) {
  const { src, base } = active;
  if (src.kind === "station") {
    active.files = [{ id: "live", offset: 0, dur: 0 }];
    const request = new chrome.cast.media.QueueLoadRequest([new chrome.cast.media.QueueItem(liveMedia(base))]);
    request.repeatMode = chrome.cast.media.RepeatMode.OFF;
    return new Promise((resolve, reject) => context.getCurrentSession().getSessionObj().queueLoad(request, resolve, reject));
  }
  active.files = src.files.filter((f) => f.playable);
  let start = active.files.findIndex((f) => positionMs < f.offset + f.dur);
  if (start < 0) start = 0;
  const items = active.files.map((file, i) => {
    const item = new chrome.cast.media.QueueItem(mediaFor(file, base));
    item.autoplay = i === start ? autoplay : true;
    item.preloadTime = 20;
    if (i === start) item.startTime = Math.max(0, (positionMs - file.offset) / 1000);
    return item;
  });
  const request = new chrome.cast.media.QueueLoadRequest(items);
  request.startIndex = start;
  request.repeatMode = chrome.cast.media.RepeatMode.OFF;
  const session = context.getCurrentSession().getSessionObj();
  return new Promise((resolve, reject) => session.queueLoad(request, resolve, reject));
}

// ---- Following the device ----

// The file the device is playing now, by the last path segment of its address.
function currentFile() {
  const info = remote.mediaInfo;
  if (!info || !active || !active.files) return null;
  const id = decodeURIComponent(info.contentId.split("?")[0].split("/").pop());
  if (active.src.kind !== "book") return active.files[0]; // an episode or a station: one item
  return active.files.find((f) => f.id === id) || null;
}

function report() {
  if (!active || !remote.isMediaLoaded) return;
  const file = currentFile();
  if (!file) return;
  // A live stream has no place: its clock stays at zero.
  const positionMs = active.src.kind === "station" ? 0 : Math.round(file.offset + remote.currentTime * 1000);
  active.lastMs = positionMs;
  fire("ts:cast:position", { positionMs, paused: remote.isPaused });
}

// The device went idle: a failure is told and ends the session; the end of the last file finishes the book.
function onPlayerState() {
  if (!active || remote.playerState !== chrome.cast.media.PlayerState.IDLE) return;
  const media = context.getCurrentSession() && context.getCurrentSession().getMediaSession();
  const reason = media ? media.idleReason : "";
  if (reason === chrome.cast.media.IdleReason.ERROR) {
    toast("err", msg("castTvError"));
    finish(active.lastMs); // before the session ends, so its end event finds nothing left to report
    endSession();
  } else if (reason === chrome.cast.media.IdleReason.FINISHED && currentFile() === active.files[active.files.length - 1]) {
    const last = active.files[active.files.length - 1];
    finish(last.offset + last.dur);
    endSession();
  }
}

// The session is over: the local player takes the place and stays paused.
function finish(positionMs) {
  if (!active) return;
  active = null;
  paint();
  if (!quiet) fire("ts:cast:end", { positionMs });
}

// The commands the player's buttons, the clock and the sleep timer send while a device plays.
document.addEventListener("ts:cast:command", (event) => {
  if (!active || !controller) return;
  const { action, positionMs } = event.detail;
  if (action === "stop") {
    active = null;
    endSession();
    paint();
  } else if (action === "play" || action === "pause") {
    if (remote.isPaused !== (action === "pause")) controller.playOrPause();
  } else if (action === "seek") {
    seekTo(positionMs);
  }
});

function seekTo(positionMs) {
  const file = active.files.find((f) => positionMs < f.offset + f.dur) || active.files[active.files.length - 1];
  if (file === currentFile()) {
    remote.currentTime = Math.max(0, (positionMs - file.offset) / 1000);
    controller.seek();
    return;
  }
  // Another file: load the queue again from there (the address is reused; it lives for the whole book).
  load(positionMs, !remote.isPaused).catch(() => {
    toast("err", msg("castFailed"));
  });
}

// ---- Start ----

if (mayCast() && slot && audio) watchFullPlayer();
