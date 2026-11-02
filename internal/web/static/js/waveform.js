// Tendshelf waveform (W2; docs/architecture.md, Waveform peaks).
//
// A third module next to player.js and player-full.js. It draws a quiet picture of the current
// chapter's loudness behind each scrubber ([data-ts-wave]) from the peaks files of GET /peaks/{id}.
// The picture is decorative (aria-hidden); the scrubber keeps its meaning and its position. Two
// identical SVGs are stacked: the muted one, and the played one, which CSS clips to the scrubber's
// --ts-pos, so nothing is redrawn while the book plays. CSP: no inline style, no markup from data;
// SVG is built with createElementNS and setAttribute only.
//
// The desktop bar's wave ([data-ts-wave="bar"], docs/design/tendshelf-player-bar) is also the
// position control of the bar: this module draws it (four stroked layers), shows the times, the
// hover preview and the book hairline, and seeks by pointer, drag and keys. It runs on the same
// ticks as the rest of the player (ts:player:tick, ts:player:state): there is no second loop.

const SVG_NS = "http://www.w3.org/2000/svg";
const BAR_PX = 4; // 2 px bar and 2 px gap
const MIN_BARS = 8;
const HEADER = 16; // bytes before the bins: "TSPK", format, bin ms, count, floor dB
const FLAT_RANGE = 12; // a chapter whose bars differ by less than this (of 255) is drawn level
const MIN_HEIGHT = 0.12; // the quietest bar still shows as a stub
const BAR_MIN_HEIGHT = 0.2; // the same on the desktop bar: the quietest tenth sits at 20 %
const FLAT_HEIGHT = 0.14; // bars while the values are not there yet
const RETRY_BACKOFF = 1.5; // each wait is this much longer than the last ...
const RETRY_CAP_MS = 120000; // ... up to this ...
const GIVE_UP_MS = 600000; // ... and a file is given up this long after its first try
const HOVER_MIN_MS = 1000; // closer to the position than this, the tip shows no "ahead" or "back"
const VALUE_TEXT_EVERY_MS = 10000; // aria-valuetext changes at most this often while playing
const KEY_STEPS = { ArrowLeft: -5000, ArrowDown: -5000, ArrowRight: 5000, ArrowUp: 5000, PageDown: -30000, PageUp: 30000 };

const slot = document.getElementById("player");
const waves = [...document.querySelectorAll("[data-ts-wave]")];
const drawn = new WeakMap(); // wave element -> the key of what it shows
let files = new Map(); // audio file id -> { status: "loading" | "wait" | "ready" | "none", bins, binMs }
let generation = 0; // bumped on a book change so late answers are dropped
let infoFor = { id: "", info: null };

// The peaks file of one audio file as { binMs, bins }, or null when it is not valid.
function parse(buffer) {
  const bytes = new Uint8Array(buffer);
  if (bytes.length <= HEADER || String.fromCharCode(...bytes.subarray(0, 4)) !== "TSPK" || bytes[4] !== 1) return null;
  const view = new DataView(buffer);
  const binMs = view.getUint16(6, true);
  const count = view.getUint32(8, true);
  if (!binMs || count !== bytes.length - HEADER) return null;
  return { binMs, bins: bytes.subarray(HEADER) };
}

// The loudness of the book time [t0, t1): the mean of the bins whose centre lies inside it, or the
// bin under its middle when none does (a bar narrower than a bin), or 0 past the end of a file.
function barValue(sources, t0, t1) {
  let sum = 0;
  let count = 0;
  for (const s of sources) {
    const first = Math.max(0, Math.ceil((t0 - s.offset) / s.binMs - 0.5));
    const last = Math.min(s.bins.length, Math.ceil((t1 - s.offset) / s.binMs - 0.5));
    for (let i = first; i < last; i++) {
      sum += s.bins[i];
      count++;
    }
  }
  if (count) return sum / count;
  const middle = (t0 + t1) / 2;
  for (const s of sources) {
    const i = Math.floor((middle - s.offset) / s.binMs);
    if (i >= 0 && i < s.bins.length) return s.bins[i];
  }
  return 0;
}

// n bar heights (0..1) for the book time [start, end). sources: [{ offset, binMs, bins }].
// The quietest tenth maps to the stub and the loudest to full height, so a chapter's own shape
// shows; a flat chapter stays level. min is the stub's height.
function heights(sources, start, end, n, min = MIN_HEIGHT) {
  const span = (end - start) / n;
  const values = Array.from({ length: n }, (_, j) => barValue(sources, start + j * span, start + (j + 1) * span));
  const sorted = [...values].sort((a, b) => a - b);
  const lo = sorted[Math.floor(n * 0.1)];
  const hi = sorted[n - 1];
  return values.map((v) => (hi - lo < FLAT_RANGE ? v / 255 : min + (1 - min) * Math.min(1, Math.max(0, (v - lo) / (hi - lo)))));
}

function svgElement(name, attributes) {
  const el = document.createElementNS(SVG_NS, name);
  for (const key in attributes) el.setAttribute(key, attributes[key]);
  return el;
}

// The pictures in wave, or nothing when hs is empty. The full player stacks two filled SVGs (muted,
// played). The desktop bar stacks four, rest, played, ahead and behind, of 2 px round-capped lines
// every 4 px in an SVG whose units are CSS px, so a bar is never stretched.
function paint(wave, hs) {
  // The bar's wave also holds the needle, the hover line, the tip and the input: only the pictures are replaced.
  for (const old of wave.querySelectorAll(":scope > svg")) old.remove();
  if (!hs.length) return;
  const isBar = wave.dataset.tsWave === "bar";
  const height = wave.clientHeight;
  let d = "";
  hs.forEach((h, j) => {
    if (isBar) {
      const half = Math.max(1, (h * (height - 4)) / 2);
      d += "M" + (j * BAR_PX + 1) + " " + (height / 2 - half).toFixed(1) + "V" + (height / 2 + half).toFixed(1);
    } else {
      d += "M" + j * BAR_PX + " " + (50 - 50 * h).toFixed(1) + "h2v" + (100 * h).toFixed(1) + "h-2z";
    }
  });
  const first = svgElement("svg", {
    viewBox: isBar ? "0 0 " + wave.clientWidth + " " + height : "0 0 " + hs.length * BAR_PX + " 100",
    preserveAspectRatio: "none",
    "aria-hidden": "true", // the picture is decoration; the bar's wave also holds the real slider
  });
  first.append(svgElement("path", { d }));
  const names = isBar ? ["rest", "played", "ahead", "behind"] : ["muted", "played"];
  wave.prepend(...names.map((name) => {
    const layer = first.cloneNode(true);
    layer.setAttribute("class", "ts-wave__" + name);
    return layer;
  }));
}

// The loaded book; a different book forgets every file and drops answers still on the way.
function getInfo(bookId) {
  if (infoFor.id !== bookId) {
    infoFor = { id: bookId, info: window.tsPlayer.info() };
    files = new Map();
    generation++;
  }
  return infoFor.info;
}

// The desktop bar's parts, or null where there is no bar.
const barWave = waves.find((w) => w.dataset.tsWave === "bar");
const barBook = document.querySelector("[data-ts-bar-book]");
const bar = barWave && {
  input: barWave.querySelector(".ts-wave__input"),
  label: barWave.querySelector("label"),
  time: barWave.querySelector("[data-ts-tip-time]"),
  rel: barWave.querySelector("[data-ts-tip-rel]"),
  elapsed: document.querySelector('[data-pbar="elapsed"]'),
  title: document.querySelector('[data-pbar="chapter"]'),
  left: document.querySelector('[data-pbar="left"]'),
  meta: document.querySelector("[data-ts-bar-meta]"),
  tip: barBook && barBook.querySelector("[data-ts-bar-booktip]"),
};
let loadingNow = false; // values of the shown chapter are still on their way
let drag = null; // { chapter, ms } while a pointer is down on the bar's wave
let textAt = 0; // when aria-valuetext last changed
let hairlineFor = ""; // the book whose chapter gaps are in the hairline

// Fetches one peaks file. A 404 with a Retry-After means "not built yet" (the server queued it): ask
// again after the longer of that and 1.5 times the last wait (at most 120 s) until it is there, and
// give up 10 minutes after the first try. A 404 without Retry-After, or any other failure, is "no
// picture"; the scrubber still works. tsWave.debug.retryScale shortens every wait for tests.
async function load(file, gen) {
  const before = files.get(file.id) || {};
  const first = before.firstAt || Date.now();
  const last = before.wait || 0;
  files.set(file.id, { status: "loading", firstAt: first, wait: last });
  let after = 0;
  try {
    const res = await fetch("/peaks/" + encodeURIComponent(file.id), { credentials: "same-origin" });
    if (res.ok) {
      const peaks = parse(await res.arrayBuffer());
      if (gen !== generation) return;
      files.set(file.id, peaks ? { status: "ready", ...peaks } : { status: "none" });
      render(false);
      return;
    }
    if (res.status === 404) after = Number(res.headers.get("Retry-After")) || 0;
  } catch {
    // network trouble: no picture
  }
  if (gen !== generation) return;
  const scale = window.tsWave.debug.retryScale;
  if (after && Date.now() - first < GIVE_UP_MS * scale) {
    const wait = Math.min(RETRY_CAP_MS, Math.max(after * 1000, last * RETRY_BACKOFF));
    files.set(file.id, { status: "wait", firstAt: first, wait });
    setTimeout(() => gen === generation && load(file, gen), wait * scale);
  } else {
    files.set(file.id, { status: "none" });
  }
  render(false);
}

// Draws every visible wave for the chapter that holds the position, once all its files are in. The
// bar's wave shows flat bars (pulsing while values are on their way) until then. force: also
// refresh the spoken position.
function render(force) {
  const state = window.tsPlayer.state();
  if (!state.bookId) return;
  const info = getInfo(state.bookId);
  const chapter = info && info.chapters[state.chapterIdx];
  if (!chapter) return;
  const overlapping = info.files.filter((f) => f.playable && f.offset < chapter.end && f.offset + f.dur > chapter.start);
  for (const wave of waves) {
    const width = wave.clientWidth;
    if (!width) continue; // closed dialog, hidden bar: nothing to draw, nothing to fetch
    for (const f of overlapping) {
      if (!files.has(f.id)) load(f, generation);
    }
    const n = Math.max(MIN_BARS, Math.floor(width / BAR_PX));
    const states = overlapping.map((f) => files.get(f.id));
    const ready = states.length > 0 && states.every((s) => s.status === "ready");
    const isBar = wave === barWave;
    const key = ready ? [state.bookId, chapter.start, chapter.end, n, overlapping.map((f) => f.id)].join("|") : isBar ? "flat|" + n : "";
    if (isBar) {
      loadingNow = states.some((s) => s.status === "loading" || s.status === "wait");
      wave.classList.toggle("ts-wave--loading", loadingNow);
    }
    if (drawn.get(wave) !== key) {
      drawn.set(wave, key);
      if (ready) {
        const sources = overlapping.map((f, i) => ({ offset: f.offset, binMs: states[i].binMs, bins: states[i].bins }));
        paint(wave, heights(sources, chapter.start, chapter.end, n, isBar ? BAR_MIN_HEIGHT : MIN_HEIGHT));
      } else {
        paint(wave, isBar ? new Array(n).fill(FLAT_HEIGHT) : []);
      }
    }
  }
  if (barWave) drawBar(state, info, chapter, force);
}

// ---- The desktop bar: times, hover, drag, keys, hairline ----

function msg(name) {
  return slot.dataset["tsMsg" + name[0].toUpperCase() + name.slice(1)] || "";
}

function fill(message, values) {
  return message.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? "");
}

function clamp(value, low, high) {
  return Math.min(high, Math.max(low, value));
}

function setText(el, text) {
  if (el.textContent !== text) el.textContent = text;
}

// "7:05" or "1:02:05"
function clock(ms) {
  const seconds = Math.floor(Math.max(0, ms) / 1000);
  const hours = Math.floor(seconds / 3600);
  const pad = (n) => String(n).padStart(2, "0");
  return (hours ? hours + ":" + pad(Math.floor((seconds % 3600) / 60)) : Math.floor(seconds / 60)) + ":" + pad(seconds % 60);
}

// "6 min 25 s" for the tip
function distance(ms) {
  const seconds = Math.round(Math.abs(ms) / 1000);
  const minutes = Math.floor(seconds / 60);
  const rest = fill(msg("deltaSec"), { s: seconds % 60 });
  if (!minutes) return rest;
  return fill(msg("deltaMin"), { m: minutes }) + (seconds % 60 ? " " + rest : "");
}

// "14 minutes 6 seconds" for a screen reader; hours drop the seconds
function spoken(ms) {
  const seconds = Math.round(ms / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const part = (n, one, many) => fill(msg(n === 1 ? one : many), { n });
  const out = [];
  if (hours) out.push(part(hours, "hour", "hours"));
  if (minutes) out.push(part(minutes, "minute", "minutes"));
  if (!hours) out.push(part(seconds % 60, "second", "seconds"));
  return out.join(" ");
}

// "2 m", "2 h" or "1 h 5 m" left in the book, in the words the cards use (the live count rounds up)
function leftInBook(minutes) {
  const hours = Math.floor(minutes / 60);
  const form = hours === 0 ? "leftInBookMinutes" : minutes % 60 === 0 ? "leftInBookHours" : "leftInBookBoth";
  return fill(msg(form), { h: hours, m: minutes % 60 });
}

// Where the position ms is on the wave: the needle, the input, the elapsed and left times.
function place(chapter, ms) {
  const length = Math.max(1, chapter.end - chapter.start);
  const into = clamp(ms - chapter.start, 0, length);
  barWave.style.setProperty("--ts-pos", (into / length).toFixed(4));
  bar.input.max = Math.floor(length / 1000);
  bar.input.value = Math.floor(into / 1000);
  setText(bar.elapsed, clock(into));
  setText(bar.left, fill(msg("waveLeft"), { t: clock(length - into) }));
}

function drawBar(state, info, chapter, force) {
  const n = state.chapterIdx + 1;
  if (!drag) place(chapter, state.positionMs);
  const name = fill(msg("waveChapter"), { n, title: chapter.title || "" }).replace(/ · $/, "");
  setText(bar.title, loadingNow ? fill(msg("waveDrawing"), { n }) : name);
  bar.input.setAttribute("aria-label", fill(msg("waveLabel"), { n }));
  setText(bar.label, fill(msg("waveLabel"), { n }));
  const left = leftInBook(Math.ceil(Math.max(0, state.durationMs - state.positionMs) / state.speed / 60000));
  setText(bar.meta, info.author ? fill(msg("barMeta"), { author: info.author, left }) : left);
  if (!drag && (force || Date.now() - textAt >= VALUE_TEXT_EVERY_MS)) {
    textAt = Date.now();
    const length = chapter.end - chapter.start;
    bar.input.setAttribute("aria-valuetext", fill(msg("waveValue"), { t: spoken(state.positionMs - chapter.start), total: spoken(length), n, title: chapter.title || "" }).replace(/, $/, ""));
  }
  if (barBook && hairlineFor !== info.id + info.chapters.length) {
    hairlineFor = info.id + info.chapters.length;
    barBook.replaceChildren(bar.tip, ...info.chapters.filter((c) => c.start > 0).map((c) => {
      const gap = document.createElement("i");
      gap.style.setProperty("--at", ((c.start / info.durationMs) * 100).toFixed(2) + "%");
      return gap;
    }));
  }
}

// The chapter the bar shows: the one a drag began in, else the one that holds the position.
function barChapter() {
  const info = window.tsPlayer.info();
  return drag ? drag.chapter : info && info.chapters[window.tsPlayer.state().chapterIdx];
}

function msAt(chapter, clientX) {
  const rect = barWave.getBoundingClientRect();
  return chapter.start + clamp((clientX - rect.left) / rect.width, 0, 1) * (chapter.end - chapter.start);
}

// The tip above the wave and the hover line at ms.
function showTip(chapter, ms) {
  const length = Math.max(1, chapter.end - chapter.start);
  barWave.style.setProperty("--ts-hover", (clamp(ms - chapter.start, 0, length) / length).toFixed(4));
  setText(bar.time, clock(ms - chapter.start));
  const delta = ms - window.tsPlayer.state().positionMs;
  if (drag) setText(bar.rel, fill(msg("waveDragChapter"), { n: window.tsPlayer.state().chapterIdx + 1 }));
  else setText(bar.rel, Math.abs(delta) < HOVER_MIN_MS ? "" : fill(msg(delta > 0 ? "waveAhead" : "waveBack"), { d: distance(delta) }));
}

function endDrag(apply) {
  if (!drag) return;
  const done = drag;
  drag = null;
  barWave.classList.remove("is-dragging");
  if (!barWave.matches(":hover")) barWave.classList.remove("is-hover");
  if (apply) window.tsPlayer.seek(done.ms);
  render(true);
}

function seekBy(chapter, ms) {
  window.tsPlayer.seek(clamp(ms, chapter.start, Math.max(chapter.start, chapter.end - 1000)));
  render(true);
}

function startBar() {
  const player = window.tsPlayer;
  barWave.addEventListener("pointerenter", () => barWave.classList.add("is-hover"));
  barWave.addEventListener("pointerleave", () => !drag && barWave.classList.remove("is-hover"));
  barWave.addEventListener("pointermove", (event) => {
    const chapter = barChapter();
    if (!chapter) return;
    const ms = msAt(chapter, event.clientX);
    if (drag) {
      drag.ms = ms;
      place(chapter, ms);
    }
    showTip(chapter, ms);
  });
  barWave.addEventListener("pointerdown", (event) => {
    const chapter = barChapter();
    if (!chapter || event.button > 0) return;
    event.preventDefault(); // the drag is ours, not the range input's
    bar.input.focus({ preventScroll: true });
    barWave.setPointerCapture(event.pointerId);
    drag = { chapter, ms: msAt(chapter, event.clientX) };
    barWave.classList.add("is-dragging");
    place(chapter, drag.ms);
    showTip(chapter, drag.ms);
  });
  barWave.addEventListener("pointerup", () => endDrag(true));
  barWave.addEventListener("pointercancel", () => endDrag(false));
  bar.input.addEventListener("focus", () => render(true));
  bar.input.addEventListener("keydown", (event) => {
    const chapter = barChapter();
    const step = KEY_STEPS[event.key];
    if (!chapter || event.ctrlKey || event.metaKey || event.altKey) return;
    if (step !== undefined) seekBy(chapter, player.state().positionMs + step);
    else if (event.key === "Home") seekBy(chapter, chapter.start);
    else if (event.key === "End") seekBy(chapter, chapter.end);
    else return;
    event.preventDefault();
  });
  // a screen reader's own adjust gesture changes the value without keys
  bar.input.addEventListener("change", () => {
    const chapter = barChapter();
    if (chapter && !drag) seekBy(chapter, chapter.start + Number(bar.input.value) * 1000);
  });
  if (!barBook) return;
  barBook.addEventListener("pointermove", (event) => {
    const info = player.info();
    if (!info) return;
    const rect = barBook.getBoundingClientRect();
    const share = clamp((event.clientX - rect.left) / rect.width, 0, 1);
    const idx = Math.max(0, info.chapters.findLastIndex((c) => c.start <= share * info.durationMs));
    barBook.style.setProperty("--ts-hover", (share * 100).toFixed(2) + "%");
    setText(bar.tip, fill(msg("bookTip"), { n: idx + 1, total: info.chapters.length, p: Math.round(share * 100) }));
  });
}

// The volume slider sits in a small pop above the bar's volume button. A press outside it, Escape and
// focus leaving it close it; Escape returns the focus to the button.
function startVolumePop() {
  const wrap = document.querySelector("[data-ts-volume-wrap]");
  const button = wrap.querySelector("[data-ts-bar-volume]");
  const pop = wrap.querySelector("#ts-volume-pop");
  const open = (on) => {
    pop.hidden = !on;
    button.setAttribute("aria-expanded", String(on));
  };
  button.addEventListener("click", () => open(pop.hidden));
  document.addEventListener("pointerdown", (event) => !pop.hidden && !wrap.contains(event.target) && open(false));
  wrap.addEventListener("focusout", (event) => !pop.hidden && !wrap.contains(event.relatedTarget) && open(false));
  wrap.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || pop.hidden) return;
    event.preventDefault();
    open(false);
    button.focus();
  });
}

function start() {
  document.addEventListener("ts:player:tick", () => render(false));
  document.addEventListener("ts:player:state", () => render(true));
  const observer = new ResizeObserver(() => render(false));
  for (const wave of waves) observer.observe(wave);
  if (barWave) {
    startBar();
    startVolumePop();
  }
  render(false);
}

// tests may set debug.retryScale before this file runs (an evaluate-on-new-document script)
const preset = (window.tsWave && window.tsWave.debug) || {};
window.tsWave = { debug: { parse, heights, retryScale: preset.retryScale || 1 } };
if (window.tsPlayer) start();
else document.addEventListener("ts:player:ready", start, { once: true });
