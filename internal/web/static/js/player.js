// Tendshelf web player (P1c-4): the one <audio> outside #main; see docs/architecture.md, Web player.
// CSP: no inline style, no markup from data, no eval; custom properties only through style.setProperty.
const doc = document;
const audio = doc.getElementById("audio");
const slot = doc.getElementById("player");
const ui = {};
for (const k of ["title", "meta", "open", "cover", "back", "forward", "toggle", "back-num", "forward-num", "mini-progress"]) ui[k] = slot.querySelector("[data-ts-" + k + "]");
const BREAK = 60000; // a pause this long is a break
const enc = encodeURIComponent;
const now = () => performance.now();
const FORM = "application/x-www-form-urlencoded";
const on = (el, ev, fn) => el.addEventListener(ev, fn);
const fire = (el, name, detail) => el.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));

let book = null;
let cur = { fileIdx: -1, startMs: 0, loaded: false, compat: false };
let idleAt = 0; // Date.now() of the last pause or restored row; 0 = playing
let heard = 0, since = 0, lastSaved = -1, lastRender = 0, hiddenAt = 0, firstSound = false;
let stopped = false, prompting = false, beaconed = false, compatSeq = 0, seekTimer = 0, memId = "";

const msg = (k) => slot.dataset["tsMsg" + k[0].toUpperCase() + k.slice(1)] || "";
const fill = (m, v) => m.replace(/\{(\w+)\}/g, (_, k) => (v[k] ?? ""));
const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
const pad = (n) => String(n).padStart(2, "0");
function clock(ms) {
  const s = Math.floor(ms / 1000), h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
  return (h ? h + ":" + pad(m) : m) + ":" + pad(s % 60);
}
function token() {
  try { return JSON.parse(doc.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || ""; } catch { return ""; }
}
const rand = () => btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(16)))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
function deviceId() {
  try {
    let id = localStorage.getItem("ts.deviceId");
    if (!id) localStorage.setItem("ts.deviceId", (id = rand()));
    return id;
  } catch { return memId || (memId = rand()); }
}

// Toasts: the #toasts markup, text through text nodes only.
function toast(kind, text, actions) {
  const el = doc.createElement("div");
  el.className = kind === "hint" ? "ts-toast ts-toast--hint" : "ts-toast";
  el.role = kind === "err" ? "alert" : "status";
  el.append(text);
  if (actions) {
    const row = el.appendChild(doc.createElement("span"));
    row.className = "ts-toast__actions";
    for (const a of actions) {
      const b = row.appendChild(doc.createElement("button"));
      b.className = "ts-btn ts-btn--quiet";
      b.textContent = a.label;
      b.type = "button";
      on(b, "click", a.run);
    }
  }
  doc.getElementById("toasts").append(el);
  return el;
}
function leave(el) {
  if (!el) return;
  el.classList.add("is-leaving");
  setTimeout(() => el.remove(), 800); // shell.js usually removes it first
}
function hint(text) { const el = toast("hint", text); setTimeout(() => leave(el), 1600); }

function parseData(el) {
  const d = el.dataset, n = (v) => Number(v) || 0, u = n(d.updatedAt);
  const list = (sel, f) => [...el.querySelectorAll(sel + " > li")].map((l) => f(l.dataset, l));
  const b = {
    id: d.bookId, title: d.title, author: d.author, cover: d.coverUrl, durationMs: n(d.durationMs), autoRewind: d.autoRewind === "1",
    skipBack: n(d.skipBackS) || 15, skipForward: n(d.skipForwardS) || 30, speed: clamp(n(d.speed) || 1, 0.5, 3),
    stored: u > 0 ? { positionMs: n(d.positionMs), updatedAt: u, deviceId: d.deviceId, deviceLabel: d.deviceLabel, finished: d.finished === "1" } : null,
    files: list("[data-ts-files]", (x) => ({ id: x.id, offset: n(x.offsetMs), dur: n(x.durationMs), mime: x.mime, mayCompat: x.mayNeedCompat === "1", playable: x.playable === "1" })),
    chapters: list("[data-ts-chapters]", (x, l) => ({ start: n(x.startMs), end: n(x.endMs), title: l.textContent })),
  };
  if (!b.chapters.length) b.chapters.push({ start: 0, end: b.durationMs, title: "" }); // no chapters: the book is one
  return b;
}
async function hydrate(id) {
  try {
    const res = await fetch(id ? "/hx/player/" + enc(id) : "/hx/player");
    if (res.status !== 200 || res.redirected) return null;
    const el = new DOMParser().parseFromString(await res.text(), "text/html").getElementById("player-data");
    return el ? parseData(el) : null;
  } catch { return null; }
}

const streamUrl = (f) => "/stream/" + enc(f.id);
// The first playable file ending after ms: its holder, or the next one at 0 across a gap.
function locate(ms) {
  const i = book ? book.files.findIndex((f) => f.playable && ms < f.offset + f.dur) : -1;
  return i < 0 ? null : { fileIdx: i, offsetMs: Math.max(0, ms - book.files[i].offset) };
}
const localMs = () => (cur.loaded && audio.readyState >= 1 ? audio.currentTime * 1000 : cur.startMs);
const gpos = () => (book && cur.fileIdx >= 0 ? book.files[cur.fileIdx].offset + localMs() : 0);
const nextPlayable = (i) => book.files.findIndex((f, j) => j > i && f.playable);
const chapterIdx = (ms) => Math.max(0, book.chapters.findLastIndex((c) => c.start <= ms));

function load(url, offsetMs, play) {
  cur.loaded = true;
  cur.startMs = offsetMs;
  audio.defaultPlaybackRate = book.speed; // loading resets playbackRate to this
  audio.src = url + "#t=" + (offsetMs / 1000).toFixed(3);
  if (play) audio.play().catch(() => {});
}
function setSource(idx, offsetMs, play) {
  const f = book.files[idx];
  compatSeq++;
  cur = { fileIdx: idx, startMs: offsetMs, loaded: false, compat: false };
  if (f.mayCompat && f.mime && audio.canPlayType(f.mime) === "") useCompat(idx, offsetMs, play);
  else load(streamUrl(f), offsetMs, play);
}
// A file the browser can't decode: poll for the converted copy while the server makes it.
async function useCompat(idx, offsetMs, play) {
  const url = streamUrl(book.files[idx]) + "?compat=1", mine = ++compatSeq, deadline = Date.now() + 600000;
  let hinted = null, res;
  for (;;) {
    try { res = await fetch(url, { method: "HEAD" }); } catch { res = null; }
    if (mine !== compatSeq) return leave(hinted);
    if (res && res.ok) {
      leave(hinted);
      cur.compat = true;
      return load(url, offsetMs, play);
    }
    if (!res || res.status !== 503 || Date.now() > deadline) break;
    hinted = hinted || toast("hint", msg("compatWait"));
    await new Promise((r) => setTimeout(r, (Number(res.headers.get("Retry-After")) || 3) * 1000));
  }
  leave(hinted);
  toast("err", msg("compatFailed"));
  emit("error");
}
function seek(ms, play) {
  if (!book) return;
  const t = locate(clamp(ms, 0, book.durationMs ? book.durationMs - 1 : Infinity));
  if (!t) return;
  if (!cur.loaded) { cur.fileIdx = t.fileIdx; cur.startMs = t.offsetMs; }
  else if (t.fileIdx !== cur.fileIdx) setSource(t.fileIdx, t.offsetMs, play === undefined ? !audio.paused : play);
  else { audio.currentTime = t.offsetMs / 1000; cur.startMs = t.offsetMs; }
  if (audio.paused) { clearTimeout(seekTimer); seekTimer = setTimeout(() => save("seek"), 2000); }
  emit("seek");
}
function chapter(delta) {
  if (!book) return;
  const pos = gpos(), i = chapterIdx(pos), c = book.chapters;
  if (delta < 0) seek(pos - c[i].start > 3000 || !i ? c[i].start : c[i - 1].start);
  else if (c[i + 1]) seek(c[i + 1].start);
  emit("chapter");
}

// After a break: rewind 5 s (setting) and look for a newer place on another device.
function afterBreak() {
  const away = idleAt ? Date.now() - idleAt : 0;
  idleAt = 0;
  if (away < BREAK) return;
  if (book.autoRewind && gpos() > 0) { seek(Math.max(0, gpos() - 5000), false); hint(msg("rewound")); }
  checkRemote();
}
function play() {
  if (!book || cur.fileIdx < 0) return;
  afterBreak();
  if (!cur.loaded) setSource(cur.fileIdx, cur.startMs, true);
  else audio.play().catch(() => {});
}
function pause() { compatSeq++; audio.pause(); }
const toggle = () => (audio.paused ? play() : pause());
function skip(dir) { if (book) seek(gpos() + dir * (dir < 0 ? book.skipBack : book.skipForward) * 1000); }
function setSpeed(v) {
  if (!book) return;
  book.speed = clamp(Math.round(v * 20) / 20, 0.5, 3);
  audio.defaultPlaybackRate = audio.playbackRate = book.speed;
  emit("speed");
}

// Saves go to localStorage first, cleared when answered; a lost one is re-sent at the next start.
const pendingGet = () => { try { return JSON.parse(localStorage.getItem("ts.pending")) || {}; } catch { return {}; } };
function pendingSet(id, body) {
  const all = pendingGet();
  if (body) all[id] = body; else delete all[id];
  try { localStorage.setItem("ts.pending", JSON.stringify(all)); } catch { /* storage is optional */ }
}
function accrue(stop) { if (since) { heard += now() - since; since = stop ? 0 : now(); } }
const payload = (extra) => new URLSearchParams({ position_ms: Math.round(gpos()), client_updated_at: Date.now(), device_id: deviceId(), speed: audio.playbackRate.toFixed(2), ...extra });
const progressUrl = (id) => "/progress/" + enc(id);
// api answers null when signed out: a redirect, or anything but JSON.
async function api(url, init) {
  const res = await fetch(url, init);
  return res.redirected || !(res.headers.get("Content-Type") || "").includes("json") ? null : res;
}
const post = (id, body) => api(progressUrl(id), { method: "POST", keepalive: true, body, headers: { "X-CSRF-Token": token(), "Content-Type": FORM } });
const near = () => Math.abs(gpos() - lastSaved) < 1000;
async function save(kind) {
  if (!book || stopped || cur.fileIdx < 0 || (kind === "tick" && (prompting || near())) || (kind === "beacon" && (!cur.loaded || (audio.paused && near())))) return;
  const id = book.id, pos = Math.round(gpos());
  const p = payload(kind === "end" ? { finished: 1 } : kind === "force" ? { force: 1 } : {});
  pendingSet(id, p.toString());
  accrue();
  const sent = Math.round(heard);
  heard = 0;
  lastSaved = pos;
  p.set("listened_ms", sent);
  if (kind === "beacon") {
    p.set("_csrf", token());
    if (!navigator.sendBeacon(progressUrl(id), p)) heard += sent;
    return;
  }
  let res;
  try { res = await post(id, p.toString()); } catch { heard += sent; return; }
  if (!res) return signedOut();
  if (!res.ok) { heard += sent; return; }
  pendingSet(id, null);
  const j = await res.json();
  fire(doc.body, "progress-saved", { bookId: id, positionMs: pos, applied: j.applied });
  if (!j.applied && book && book.id === id) checkPrompt(j.progress);
}
async function flushPending() {
  for (const [id, body] of Object.entries(pendingGet())) {
    try { if (!(await post(id, body))) return; } catch { return; }
    pendingSet(id, null);
  }
}
function signedOut() {
  if (stopped) return;
  stopped = true;
  toast("err", msg("signedOut"), [{ label: msg("signIn"), run: () => location.assign("/login") }]);
}

async function checkRemote() {
  if (!book || stopped) return;
  try {
    const res = await api(progressUrl(book.id));
    if (res) checkPrompt((await res.json()).progress);
  } catch { /* the next check will do */ }
}
function checkPrompt(p) {
  if (!p || prompting || p.finished || p.deviceId === deviceId() || p.deviceId === "server" || Math.abs(p.positionMs - gpos()) <= BREAK) return;
  prompting = true;
  const close = () => { prompting = false; leave(el); };
  const el = toast("status", fill(msg("continueAsk"), { time: clock(p.positionMs), device: p.deviceLabel || msg("otherDevice") }), [
    { label: msg("continueThere"), run: () => { close(); seek(p.positionMs); } },
    { label: msg("stayHere"), run: () => { close(); save("force"); } },
  ]);
}

function render(force) {
  if (!book) return;
  lastRender = now();
  const pos = gpos(), ci = chapterIdx(pos), ch = book.chapters[ci], t = book.title, left = Math.max(1, Math.ceil((ch.end - pos) / 60000));
  const label = (k, v) => ui[k].setAttribute("aria-label", v);
  ui.title.textContent = t;
  ui.meta.textContent = fill(msg("chapterLeft"), { n: ci + 1, m: left });
  ui.open.href = "/books/" + enc(book.id);
  label("open", fill(msg("open"), { title: t, n: ci + 1 }));
  if (book.cover && ui.cover.getAttribute("src") !== book.cover) ui.cover.src = book.cover;
  ui.cover.hidden = !book.cover;
  ui["back-num"].textContent = book.skipBack;
  ui["forward-num"].textContent = book.skipForward;
  label("back", fill(msg("backSeconds"), { n: book.skipBack }));
  label("forward", fill(msg("forwardSeconds"), { n: book.skipForward }));
  label("toggle", (audio.paused ? msg("playPrefix") : msg("pausePrefix")) + " " + t);
  slot.classList.toggle("is-playing", !audio.paused);
  ui["mini-progress"].style.setProperty("--ts-progress", book.durationMs ? clamp(pos / book.durationMs, 0, 1).toFixed(4) : "0");
  if (force) session();
}
function state() {
  return book ? { bookId: book.id, title: book.title, positionMs: Math.round(gpos()), durationMs: book.durationMs, playing: !audio.paused,
    speed: book.speed, fileIdx: cur.fileIdx, chapterIdx: chapterIdx(gpos()) } : { bookId: "", playing: false };
}
function emit(kind) {
  render(true);
  fire(doc, "ts:player:state", { kind, ...state() });
}

// One resume per screen: a visible [data-ts-play] for the loaded book hides the bar.
let watcher = null;
const suppress = (yes) => { slot.classList.toggle("is-suppressed", yes); slot.inert = yes; };
function scanResume() {
  if (watcher) watcher.disconnect();
  suppress(false);
  if (!book) return;
  const seen = new Set();
  watcher = new IntersectionObserver((entries) => {
    for (const e of entries) if (e.isIntersecting) seen.add(e.target); else seen.delete(e.target);
    suppress(seen.size > 0);
  });
  for (const el of doc.querySelectorAll("[data-ts-play]")) if (el.dataset.tsPlay === book.id) watcher.observe(el);
}

const actions = [], hasSession = "mediaSession" in navigator;
function session() {
  if (!hasSession || !book) return;
  const ch = book.chapters[chapterIdx(gpos())], sec = book.durationMs / 1000;
  try {
    navigator.mediaSession.metadata = new MediaMetadata({ title: book.title, artist: book.author, album: ch.title, artwork: book.cover ? [{ src: book.cover, sizes: "512x512" }] : [] });
    if (sec) navigator.mediaSession.setPositionState({ duration: sec, position: clamp(gpos() / 1000, 0, sec), playbackRate: audio.playbackRate || 1 });
  } catch { /* an unsupported field is skipped */ }
}
if (hasSession) {
  const h = { play, pause, stop: pause, seekbackward: () => skip(-1), seekforward: () => skip(1), seekto: (d) => seek(d.seekTime * 1000), previoustrack: () => chapter(-1), nexttrack: () => chapter(1) };
  for (const k in h) {
    try { navigator.mediaSession.setActionHandler(k, h[k]); actions.push(k); } catch { /* unsupported here */ }
  }
}

function adopt(b, posMs) {
  if (book && book.id !== b.id) { save("pause"); since = 0; cur.loaded = false; audio.removeAttribute("src"); audio.load(); }
  book = b;
  prompting = false;
  if (cur.fileId) cur.fileIdx = b.files.findIndex((f) => f.id === cur.fileId);
  else {
    const t = locate(posMs) || locate(0);
    if (!t) return false;
    if (!cur.loaded || cur.fileIdx !== t.fileIdx) cur = { fileIdx: t.fileIdx, startMs: t.offsetMs, loaded: false, compat: false };
  }
  lastSaved = -1;
  slot.hidden = false;
  doc.body.classList.add("has-player");
  emit("load");
  scanResume();
  return true;
}
async function loadBook(id, o) {
  const b = await hydrate(id), at = o && o.positionMs;
  if (!b || !adopt(b, at !== undefined ? at : b.stored && !b.stored.finished ? b.stored.positionMs : 0)) return false;
  idleAt = at === undefined && b.stored ? b.stored.updatedAt : 0;
  if (cur.fileId) { // a click started sound: settings and the break rule catch up
    delete cur.fileId;
    audio.defaultPlaybackRate = b.speed;
    if (!audio.paused) audio.playbackRate = b.speed;
    if (!b.stored || Math.abs(gpos() - b.stored.positionMs) > 2000) idleAt = 0;
    afterBreak();
  } else if (o && o.play) play();
  return true;
}
// The link names the file: sound starts inside the click, before the fragment arrives.
function startFromLink(d) {
  if (book) { save("pause"); book = null; }
  const start = Number(d.tsStartMs) || 0, startMs = Math.max(0, start - (Number(d.tsFileStartMs) || 0));
  if (d.tsMayNeedCompat === "1" && d.tsMime && audio.canPlayType(d.tsMime) === "") return loadBook(d.tsPlay, { positionMs: start, play: true });
  cur = { fileIdx: -1, fileId: d.tsFile, startMs, loaded: true, compat: false };
  audio.defaultPlaybackRate = 1;
  audio.src = "/stream/" + enc(d.tsFile) + "#t=" + (startMs / 1000).toFixed(3);
  audio.play().catch(() => {});
  return loadBook(d.tsPlay);
}
on(doc, "click", (event) => {
  const el = event.target.closest("[data-ts-play], [data-ts-play-at]");
  if (!el) return;
  const d = el.dataset;
  event.preventDefault();
  performance.mark("ts-play-click");
  if (d.tsPlayAt !== undefined) {
    const ms = Number(d.tsPlayAt) || 0;
    if (book && book.id === d.tsBook) { seek(ms, true); play(); } else loadBook(d.tsBook, { positionMs: ms, play: true });
    return;
  }
  firstSound = true;
  if (book && book.id === d.tsPlay) play();
  else if (d.tsFile) startFromLink(d);
  else loadBook(d.tsPlay, { play: true });
});
const revealPlayAt = () => { for (const el of doc.querySelectorAll("[data-ts-play-at][hidden]")) el.hidden = false; };

on(audio, "play", () => emit("play"));
on(audio, "playing", () => { since = now(); idleAt = 0; render(true); });
on(audio, "waiting", () => accrue(true));
on(audio, "pause", () => {
  if (audio.ended || !book) return;
  accrue(true);
  idleAt = Date.now();
  save("pause");
  emit("pause");
});
on(audio, "timeupdate", () => {
  if (firstSound && !audio.paused && audio.currentTime * 1000 >= cur.startMs + 50) { firstSound = false; performance.mark("ts-first-sound"); }
  if (now() - lastRender >= 1000) { render(); session(); if (!audio.paused) fire(doc, "ts:player:tick", state()); }
  if (book && !cur.prefetched && audio.duration - audio.currentTime < 20) { // warm the next file's first bytes
    cur.prefetched = true;
    const n = nextPlayable(cur.fileIdx);
    if (n >= 0 && !book.files[n].mayCompat) fetch(streamUrl(book.files[n]), { headers: { Range: "bytes=0-65535" } }).catch(() => {});
  }
});
on(audio, "ended", () => {
  if (!book) return;
  accrue(true);
  const n = nextPlayable(cur.fileIdx);
  if (n >= 0) { setSource(n, 0, true); emit("chapter"); return; }
  save("end");
  idleAt = Date.now();
  emit("pause");
});
on(audio, "error", () => {
  if (!book || !cur.loaded) return;
  const f = book.files[cur.fileIdx];
  if (audio.error && audio.error.code === 4 && f && f.mayCompat && !cur.compat) { useCompat(cur.fileIdx, cur.startMs, true); return; }
  toast("err", msg("compatFailed"));
  emit("error");
});

on(slot, "click", (e) => {
  const b = e.target.closest("button");
  if (b === ui.toggle) toggle(); else if (b === ui.back) skip(-1); else if (b === ui.forward) skip(1);
});
// Keys: Space and arrows (shell.js has the navigation keys).
on(doc, "keydown", (e) => {
  if (!book || e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target instanceof Element ? e.target : doc.body;
  if (t.closest("input, textarea, select, [contenteditable]:not([contenteditable=false])") || doc.querySelector("dialog[open], #sheet .ts-sheet")) return;
  if (e.key === " " && !t.closest("button, a, summary, [role=button]")) toggle();
  else if (e.key === "ArrowLeft") skip(-1);
  else if (e.key === "ArrowRight") skip(1);
  else return;
  e.preventDefault();
});

// Leaving the page: one beacon per hide.
const hide = () => { if (!beaconed) { beaconed = true; save("beacon"); } };
on(doc, "visibilitychange", () => {
  if (doc.visibilityState === "hidden") { hiddenAt = Date.now(); hide(); return; }
  beaconed = false;
  if (hiddenAt && Date.now() - hiddenAt >= BREAK) checkRemote();
  hiddenAt = 0;
});
on(window, "pagehide", hide);
setInterval(() => { if (!audio.paused) save("tick"); }, 10000);
on(doc, "htmx:after:settle", () => { scanResume(); revealPlayAt(); });

window.tsPlayer = {
  state, load: loadBook, play, pause, toggle, seek: (ms) => seek(ms), skip, chapter, setSpeed,
  debug: { setPausedAt: (ms) => { idleAt = ms; }, actions, locate, chapterIdx },
};
revealPlayAt();
flushPending();
hydrate("").then((b) => {
  if (!b || book) return;
  idleAt = b.stored ? b.stored.updatedAt : 0;
  adopt(b, b.stored && !b.stored.finished ? b.stored.positionMs : 0);
});
performance.mark("ts-player-ready");
