// Tendshelf waveform (W2; docs/architecture.md, Waveform peaks).
//
// A third module next to player.js and player-full.js. It draws a quiet picture of the current
// chapter's loudness behind each scrubber ([data-ts-wave]) from the peaks files of GET /peaks/{id}.
// The picture is decorative (aria-hidden); the scrubber keeps its meaning and its position. Two
// identical SVGs are stacked: the muted one, and the played one, which CSS clips to the scrubber's
// --ts-pos, so nothing is redrawn while the book plays. CSP: no inline style, no markup from data;
// SVG is built with createElementNS and setAttribute only.

const SVG_NS = "http://www.w3.org/2000/svg";
const BAR_PX = 4; // 2 px bar and 2 px gap
const MIN_BARS = 8;
const HEADER = 16; // bytes before the bins: "TSPK", format, bin ms, count, floor dB
const FLAT_RANGE = 12; // a chapter whose bars differ by less than this (of 255) is drawn level
const MIN_HEIGHT = 0.12; // the quietest bar still shows as a stub
const RETRY_S = 30; // used when the server sends no Retry-After

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
// shows; a flat chapter stays level.
function heights(sources, start, end, n) {
  const span = (end - start) / n;
  const values = Array.from({ length: n }, (_, j) => barValue(sources, start + j * span, start + (j + 1) * span));
  const sorted = [...values].sort((a, b) => a - b);
  const lo = sorted[Math.floor(n * 0.1)];
  const hi = sorted[n - 1];
  return values.map((v) => (hi - lo < FLAT_RANGE ? v / 255 : MIN_HEIGHT + (1 - MIN_HEIGHT) * Math.min(1, Math.max(0, (v - lo) / (hi - lo)))));
}

// The two stacked pictures in wave, or nothing when hs is empty.
function paint(wave, hs) {
  if (!hs.length) {
    wave.replaceChildren();
    return;
  }
  let d = "";
  hs.forEach((h, j) => {
    d += "M" + j * BAR_PX + " " + (50 - 50 * h).toFixed(1) + "h2v" + (100 * h).toFixed(1) + "h-2z";
  });
  const muted = document.createElementNS(SVG_NS, "svg");
  muted.setAttribute("viewBox", "0 0 " + hs.length * BAR_PX + " 100");
  muted.setAttribute("preserveAspectRatio", "none");
  muted.setAttribute("class", "ts-wave__muted");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute("d", d);
  muted.append(path);
  const played = muted.cloneNode(true);
  played.setAttribute("class", "ts-wave__played");
  wave.replaceChildren(muted, played);
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

// Fetches one peaks file. A 404 means "not built yet" (the server queued it and says when to look
// again): wait that long and ask once more, then give up for this file until the page reloads.
async function load(file, gen, retried) {
  let wait = 0;
  try {
    const res = await fetch("/peaks/" + encodeURIComponent(file.id), { credentials: "same-origin" });
    if (res.ok) {
      const peaks = parse(await res.arrayBuffer());
      if (gen !== generation) return;
      files.set(file.id, peaks ? { status: "ready", ...peaks } : { status: "none" });
      render();
      return;
    }
    if (res.status === 404 && !retried) wait = (Number(res.headers.get("Retry-After")) || RETRY_S) * 1000;
  } catch {
    // network trouble: no picture
  }
  if (gen !== generation) return;
  if (wait) {
    files.set(file.id, { status: "wait" });
    setTimeout(() => gen === generation && load(file, gen, true), wait);
  } else {
    files.set(file.id, { status: "none" });
  }
  render();
}

// Draws every visible wave for the chapter that holds the position, once all its files are in.
function render() {
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
      if (!files.has(f.id)) {
        files.set(f.id, { status: "loading" });
        load(f, generation, false);
      }
    }
    const n = Math.max(MIN_BARS, Math.floor(width / BAR_PX));
    const states = overlapping.map((f) => files.get(f.id));
    const ready = states.length > 0 && states.every((s) => s.status === "ready");
    const key = ready ? [state.bookId, chapter.start, chapter.end, n, overlapping.map((f) => f.id)].join("|") : "";
    if (drawn.get(wave) === key) continue;
    drawn.set(wave, key);
    if (!ready) {
      paint(wave, []);
      continue;
    }
    const sources = overlapping.map((f, i) => ({ offset: f.offset, binMs: states[i].binMs, bins: states[i].bins }));
    paint(wave, heights(sources, chapter.start, chapter.end, n));
  }
}

function start() {
  for (const type of ["ts:player:tick", "ts:player:state"]) document.addEventListener(type, render);
  const observer = new ResizeObserver(render);
  for (const wave of waves) observer.observe(wave);
  render();
}

window.tsWave = { debug: { parse, heights } };
if (window.tsPlayer) start();
else document.addEventListener("ts:player:ready", start, { once: true });
