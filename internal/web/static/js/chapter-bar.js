// The chapter bar: every book ruler with chapter ticks (the full player and the side panel, Home's
// hero, the book page's play region) names the chapter under the pointer or the focus, and a click, a
// tap or Enter goes to that chapter's start. It is a chapter picker, not a free scrubber: the chapter
// scrubber (player-full.js) and the desktop bar's waveform stay the places to drag.
//
// A bar is any element with [data-ts-chbar]. data-ts-book names its book; without it the bar belongs to
// the loaded book (the full player's). A bar of the loaded book seeks through the player (the full player
// turns the seek into a scrubber move, so the Undo offer, the save and the sleep timer behave); a bar of
// another book starts that book at the chapter through the player's own load. The other book's chapters
// are asked for once, when someone first points at or focuses its bar, and are kept for the page.
// Everything is delegated on the document, so bars htmx swaps in need nothing.

const MARGIN = 8; // px kept between the tip and the edge of the window or the panel
const CANCEL_AWAY_PX = 60; // a press that ends this far above or below the bar is not a jump
const PEEKED = new Map(); // book id -> Promise of { id, durationMs, chapters } for books that are not loaded

const player = () => window.tsPlayer;
const slot = () => document.getElementById("player");

function msg(name) {
  const node = slot();
  return (node && node.dataset["tsMsg" + name[0].toUpperCase() + name.slice(1)]) || "";
}

function fill(message, values) {
  return message.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? "");
}

// 1:05:09 or 5:09, the way the player shows a clock.
function clock(ms) {
  const total = Math.floor(Math.max(0, ms) / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

// ---- Which chapters a bar stands for ----

// { id, durationMs, chapters, loaded } or null. loaded: the player has this book, so a jump is a seek.
function chaptersFor(bar) {
  const p = player();
  if (!p) return Promise.resolve(null);
  const info = p.info();
  const id = bar.dataset.tsBook || (info && info.id);
  if (!id) return Promise.resolve(null);
  if (info && info.id === id) return Promise.resolve({ id, durationMs: info.durationMs, chapters: info.chapters, loaded: true });
  if (!PEEKED.has(id)) PEEKED.set(id, p.peek(id));
  return PEEKED.get(id).then((book) => {
    if (!book) PEEKED.delete(id); // try again next time
    return book && { ...book, loaded: false };
  });
}

function chapterAtMs(data, ms) {
  return Math.max(0, data.chapters.findLastIndex((c) => c.start <= ms));
}

function titleOf(chapter, index) {
  // a chapter with no name in the file is named by its number
  return chapter.title || fill(msg("chbarUntitled"), { n: index + 1 });
}

// ---- The full player's own bar follows the loaded book ----

// Fewer than two chapters: the bar stays as before, a picture hidden from screen readers.
function syncOwnBars() {
  const p = player();
  const info = p && p.info();
  for (const bar of document.querySelectorAll("[data-ts-chbar]:not([data-ts-book])")) {
    const usable = !!info && info.chapters.length > 1;
    if (!usable) {
      bar.setAttribute("aria-hidden", "true");
      for (const name of ["role", "tabindex", "aria-label", "aria-valuemin", "aria-valuemax", "aria-valuenow", "aria-valuetext"]) bar.removeAttribute(name);
      hide(bar);
      continue;
    }
    bar.removeAttribute("aria-hidden");
    bar.setAttribute("role", "slider");
    bar.setAttribute("tabindex", "0");
    bar.setAttribute("aria-label", bar.dataset.tsLabel || "");
    bar.setAttribute("aria-valuemin", "1");
    bar.setAttribute("aria-valuemax", String(info.chapters.length));
    describe(bar, { chapters: info.chapters }, chapterAtMs(info, p.state().positionMs));
  }
}

// aria-valuenow and aria-valuetext: "Chapter 3 of 12: The Lamp".
function describe(bar, data, index) {
  bar.setAttribute("aria-valuenow", String(index + 1));
  bar.setAttribute("aria-valuetext", fill(msg("chbarValue"), { n: index + 1, m: data.chapters.length, title: titleOf(data.chapters[index], index) }));
}

// ---- The tip ----

function tipOf(bar) {
  let tip = bar.querySelector(".ts-chtip");
  if (tip) return tip;
  tip = document.createElement("span");
  tip.className = "ts-chtip";
  tip.setAttribute("aria-hidden", "true"); // the slider's value text is what a screen reader hears
  const mark = document.createElement("span");
  mark.className = "ts-chmark";
  mark.setAttribute("aria-hidden", "true");
  bar.append(mark, tip);
  return tip;
}

// The title and the start time, in the order the message gives them. The title shortens with an
// ellipsis; the time never does.
function fillTip(tip, title, time) {
  const parts = msg("chbarTip").split(/(\{title\}|\{t\})/);
  tip.replaceChildren(...parts.filter(Boolean).map((part) => {
    const node = document.createElement("span");
    if (part === "{title}") {
      node.className = "ts-chtip__title";
      node.textContent = title;
    } else if (part === "{t}") {
      node.className = "ts-chtip__time";
      node.textContent = time;
    } else {
      node.className = "ts-chtip__sep";
      node.textContent = part;
    }
    return node;
  }));
}

// Where the tip may sit: inside the window, and inside the player's panel when the bar is in one.
function limits(bar) {
  const panel = bar.closest(".ts-player__body");
  const box = panel ? panel.getBoundingClientRect() : { left: 0, right: document.documentElement.clientWidth };
  return { left: Math.max(0, box.left), right: Math.min(document.documentElement.clientWidth, box.right) };
}

// Shows chapter index of data above the bar, centred on anchorX (a viewport x), kept inside the limits.
function show(bar, data, index, anchorX) {
  const chapter = data.chapters[index];
  const tip = tipOf(bar);
  const mark = bar.querySelector(".ts-chmark");
  const rect = bar.getBoundingClientRect();
  fillTip(tip, titleOf(chapter, index), clock(chapter.start));
  bar.classList.add("is-tipped");
  const edge = limits(bar);
  const width = tip.offsetWidth;
  const wanted = anchorX - rect.left - width / 2;
  const least = edge.left + MARGIN - rect.left;
  const most = edge.right - MARGIN - rect.left - width;
  tip.style.setProperty("--tip-left", Math.max(least, Math.min(wanted, Math.max(least, most))).toFixed(1) + "px");
  const duration = data.durationMs || 1;
  mark.style.setProperty("--from", (chapter.start / duration * 100).toFixed(3) + "%");
  mark.style.setProperty("--to", (Math.min(chapter.end, duration) / duration * 100).toFixed(3) + "%");
}

function hide(bar) {
  bar.classList.remove("is-tipped");
  bar.classList.remove("is-pressed");
  delete bar.dataset.tsPreview;
}

// ---- Going to a chapter ----

function go(bar, data, index) {
  const chapter = data.chapters[index];
  const p = player();
  if (!chapter || !p) return;
  if (data.loaded) {
    const asked = new CustomEvent("ts:chapterbar:seek", { cancelable: true, detail: { bookId: data.id, ms: chapter.start } });
    if (document.dispatchEvent(asked)) p.seek(chapter.start);
    // A page's bar (Home's hero) is a way to start listening; the player's own bar only moves the place.
    if (bar.dataset.tsBook && !p.state().playing) p.play();
  } else {
    p.load(data.id, { positionMs: chapter.start, play: true });
  }
  bar.dispatchEvent(new CustomEvent("ts:chapterbar:go", { bubbles: true, detail: { bookId: data.id, index, ms: chapter.start } }));
}

// ---- Pointer: hover for a mouse, press and drag for a finger ----

let lastPressAt = -Infinity; // when a pointer last went down on a bar
let pressed = null; // { bar, id, x, y } while a pointer is down on a bar

function barOf(event) {
  return event.target instanceof Element ? event.target.closest("[data-ts-chbar]") : null;
}

function fraction(bar, x) {
  const rect = bar.getBoundingClientRect();
  return Math.min(1, Math.max(0, (x - rect.left) / (rect.width || 1)));
}

// force: the caller is acting on the press, so the bar need not be hovered when the chapters arrive.
async function point(bar, x, force = false) {
  const data = await chaptersFor(bar);
  if (!data) return null;
  if (!force && !pressed && !bar.matches(":hover")) return null; // the pointer left while the chapters came
  const index = chapterAtMs(data, fraction(bar, x) * data.durationMs);
  bar.dataset.tsPreview = String(index);
  show(bar, data, index, x);
  return { data, index };
}

document.addEventListener("pointermove", (event) => {
  const bar = barOf(event);
  if (pressed && pressed.bar !== bar && event.pointerId === pressed.id) {
    // the pointer is captured by the bar, so it is always the target; this is only for safety
    return;
  }
  if (!bar) return;
  if (event.pointerType === "touch" && !pressed) return;
  if (pressed && event.pointerId === pressed.id) pressed.y = event.clientY;
  point(bar, event.clientX);
});

// pointerout also fires between the bar's own children, so only a pointer that really left counts.
document.addEventListener("pointerout", (event) => {
  const bar = barOf(event);
  if (bar && !pressed && !(event.relatedTarget instanceof Node && bar.contains(event.relatedTarget))) hide(bar);
});

document.addEventListener("pointerdown", (event) => {
  const bar = barOf(event);
  if (!bar || event.button > 0) return;
  pressed = { bar, id: event.pointerId, y: event.clientY };
  lastPressAt = performance.now();
  bar.classList.add("is-pressed");
  try {
    bar.setPointerCapture(event.pointerId);
  } catch {
    // a pointer that is already gone
  }
  point(bar, event.clientX);
});

async function release(event, cancelled) {
  if (!pressed || event.pointerId !== pressed.id) return;
  const { bar, y } = pressed;
  pressed = null;
  const rect = bar.getBoundingClientRect();
  const away = Math.abs(y - (rect.top + rect.height / 2));
  const touch = event.pointerType === "touch";
  const result = cancelled || away > CANCEL_AWAY_PX ? null : await point(bar, event.clientX, true);
  if (result) go(bar, result.data, result.index);
  // a mouse keeps its tip while it is over the bar; a finger's goes when it lifts
  if (touch || !result || !bar.matches(":hover")) hide(bar);
  else bar.classList.remove("is-pressed");
}

document.addEventListener("pointerup", (event) => release(event, false));
document.addEventListener("pointercancel", (event) => release(event, true));

// A long press on a phone would open the browser's menu over the bar.
document.addEventListener("contextmenu", (event) => {
  if (barOf(event)) event.preventDefault();
});

// ---- Keyboard: arrows choose a chapter, Enter or Space goes there ----

async function currentIndex(bar, data) {
  if (bar.dataset.tsPreview !== undefined) return Number(bar.dataset.tsPreview);
  if (data.loaded) return chapterAtMs(data, player().state().positionMs);
  return Math.min(data.chapters.length - 1, Math.max(0, Number(bar.getAttribute("aria-valuenow")) - 1));
}

function anchorFor(bar, data, index) {
  const rect = bar.getBoundingClientRect();
  return rect.left + rect.width * (data.chapters[index].start / (data.durationMs || 1));
}

async function preview(bar, index) {
  const data = await chaptersFor(bar);
  if (!data) return null;
  const at = Math.min(data.chapters.length - 1, Math.max(0, index ?? (await currentIndex(bar, data))));
  bar.dataset.tsPreview = String(at);
  describe(bar, data, at);
  show(bar, data, at, anchorFor(bar, data, at));
  return { data, index: at };
}

document.addEventListener("focusin", (event) => {
  const bar = barOf(event);
  // A press focuses the bar too, a moment after the pointer went down (a finger's compatibility mouse
  // events come after it lifts); the press shows the tip where the pointer is, so focus stays quiet.
  if (bar && !pressed && performance.now() - lastPressAt > 1000) preview(bar);
});

document.addEventListener("focusout", (event) => {
  const bar = barOf(event);
  if (bar && !pressed) hide(bar);
});

const KEY_STEP = { ArrowLeft: -1, ArrowDown: -1, ArrowRight: 1, ArrowUp: 1 };

document.addEventListener("keydown", async (event) => {
  const bar = barOf(event);
  if (!bar || event.target !== bar || event.ctrlKey || event.metaKey || event.altKey) return;
  const key = event.key;
  const step = KEY_STEP[key];
  const confirm = key === "Enter" || key === " ";
  if (step === undefined && !confirm && key !== "Home" && key !== "End") return;
  event.preventDefault();
  event.stopPropagation(); // the arrows and Space mean skip and play outside the bar
  const data = await chaptersFor(bar);
  if (!data) return;
  const now = await currentIndex(bar, data);
  if (confirm) {
    await preview(bar, now);
    go(bar, data, now);
  } else if (key === "Home") {
    preview(bar, 0);
  } else if (key === "End") {
    preview(bar, data.chapters.length - 1);
  } else {
    preview(bar, now + step);
  }
}, true); // before the arrows and Space mean skip and play anywhere else

// ---- Keeping the full player's bar in step with the book ----

document.addEventListener("ts:player:state", (event) => {
  if (event.detail.kind !== "tick") syncOwnBars();
});
document.addEventListener("ts:player:ready", syncOwnBars);
if (player()) syncOwnBars();
