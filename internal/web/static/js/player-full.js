// Tendshelf full player (P1c-5a; docs/architecture.md, Web player).
//
// A second module next to player.js. It owns the full player dialog (#player-full) and the compact
// scrubber of the desktop bar; it never touches #audio and drives playback only through
// window.tsPlayer and its ts:player:* events. CSP: no inline style, no markup from data, no eval;
// custom properties only through style.setProperty. Every word comes from the data-ts-msg-*
// attributes of #player.

const slot = document.getElementById("player");
const dialog = document.getElementById("player-full");
const root = document.documentElement;
const SIDE_PANEL = matchMedia("(min-width: 1280px)"); // from here the player is a side panel
const REDUCED = matchMedia("(prefers-reduced-motion: reduce)");

const HEX = /^#[0-9a-f]{6}$/i;
const VALUE_TEXT_EVERY_MS = 10000; // aria-valuetext changes at most this often while playing
const SNAP_FRACTION = 0.015; // a drag within 1.5 % of a chapter start settles on it
const SWIPE_MIN_PX = 8; // movement before a press becomes a swipe
const SWIPE_OPEN_PX = 40; // swipe up on the bar opens the player
const SWIPE_CLOSE_FRACTION = 0.3; // swipe down past this part of the screen closes it
const SETTLE_MS = 250;

let player = null; // window.tsPlayer
let book = null; // player.info() of the loaded book
let opener = null; // what had focus when the player opened
let sheetOpener = null;

// ---- Elements and words ----

const $ = (name, scope = dialog) => scope.querySelector("[data-ts-" + name + "]");
const ui = {};
for (const name of ["close-player", "full-chapter", "full-cover", "full-title", "full-author", "full-clock", "full-left",
  "full-back", "full-forward", "full-back-num", "full-forward-num", "full-toggle", "full-elapsed", "full-fine",
  "full-book-left", "full-speed", "full-book", "full-finish", "full-menu", "full-body", "chapters-sheet", "chapter-list",
  "chapter-row", "open-chapters"]) {
  ui[name] = $(name);
}
const miniOpen = $("open", slot);

function msg(name) {
  return slot.dataset["tsMsg" + name[0].toUpperCase() + name.slice(1)] || "";
}

function fill(message, values) {
  return message.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? "");
}

function clamp(value, low, high) {
  return Math.min(high, Math.max(low, value));
}

function clock(ms) {
  const seconds = Math.floor(Math.max(0, ms) / 1000);
  const hours = Math.floor(seconds / 3600);
  const pad = (n) => String(n).padStart(2, "0");
  return (hours ? hours + ":" + pad(Math.floor((seconds % 3600) / 60)) : Math.floor(seconds / 60)) + ":" + pad(seconds % 60);
}

function setText(name, text) {
  if (ui[name].textContent !== text) ui[name].textContent = text;
}

function setVars(el, vars) {
  for (const key in vars) el.style.setProperty(key, vars[key]);
}

function chapterAt(ms) {
  return Math.max(0, book.chapters.findLastIndex((c) => c.start <= ms));
}

// ---- Cover accent: only the full player takes the cover's colour ----

function theme() {
  if (root.dataset.theme) return root.dataset.theme;
  if (matchMedia("(prefers-contrast: more)").matches) return "contrast";
  return matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

// A missing or invalid value, or the contrast theme, removes the property: the tokens (ember) decide.
function applyAccent() {
  if (!book) return;
  const name = theme();
  const accent = name === "contrast" ? "" : book.accents[name];
  const tint = name === "contrast" ? "" : book.tint;
  dialog.dataset.tint = name === "dark" ? "full" : name === "contrast" ? "none" : "pale"; // how strong the fade is
  for (const [property, value] of [["--cover-accent", accent], ["--cover-tint", tint]]) {
    dialog.style.setProperty(property, HEX.test(value || "") ? value : ""); // "" removes the property
  }
}

// ---- Drawing: times, scrubbers ----

// Everything the full player shows for position ms; also follows a drag.
function drawTimes(ms, speed) {
  const idx = chapterAt(ms);
  const chapter = book.chapters[idx];
  const leftMs = Math.max(0, book.durationMs - ms) / speed;
  const minutes = Math.ceil(leftMs / 60000);
  setText("full-chapter", fill(msg("chapterOf"), { n: idx + 1, total: book.chapters.length }));
  setText("full-clock", clock(ms - chapter.start));
  setText("full-left", fill(msg("leftInChapter"), { t: clock(chapter.end - ms) }));
  setText("full-elapsed", clock(ms));
  setText("full-book-left", fill(msg("leftInBook"), { h: Math.floor(minutes / 60), m: minutes % 60 }));
}

function drawScrub(s, ms, force) {
  const idx = chapterAt(ms);
  const chapter = book.chapters[idx];
  const fraction = (value) => clamp(value / (book.durationMs || 1), 0, 1).toFixed(4);
  setVars(s.root, { "--ts-pos": fraction(ms), "--ts-ch-at": fraction(chapter.start), "--ts-ch-w": fraction(chapter.end - chapter.start) });
  s.input.value = Math.floor(ms / 1000);
  const now = performance.now();
  if (force || !s.input.hasAttribute("aria-valuetext") || now - s.textAt >= VALUE_TEXT_EVERY_MS) {
    s.textAt = now;
    const hours = Math.floor(ms / 3600000);
    s.input.setAttribute("aria-valuetext", fill(msg("scrubValue"), { h: hours, m: Math.floor(ms / 60000) % 60, n: idx + 1 }));
  }
}

function update(kind) {
  if (!book) return;
  const state = player.state();
  const ms = state.positionMs;
  for (const s of scrubs) if (!s.drag) drawScrub(s, ms, kind !== "tick");
  if (!dialog.open) return;
  if (!fullScrub.drag) drawTimes(ms, state.speed);
  dialog.classList.toggle("is-playing", state.playing);
  ui["full-toggle"].setAttribute("aria-label", (state.playing ? msg("pausePrefix") : msg("playPrefix")) + " " + book.title);
  setText("full-speed", (state.speed % 1 ? +state.speed.toFixed(2) : state.speed.toFixed(1)) + "×");
}

// What changes only when another book loads.
function drawBook() {
  setText("full-title", book.title);
  setText("full-author", book.author);
  if (book.cover && ui["full-cover"].getAttribute("src") !== book.cover) ui["full-cover"].src = book.cover;
  ui["full-cover"].hidden = !book.cover;
  for (const direction of ["back", "forward"]) {
    const seconds = direction === "back" ? book.skipBack : book.skipForward;
    setText("full-" + direction + "-num", String(seconds));
    ui["full-" + direction].setAttribute("aria-label", fill(msg(direction + "Seconds"), { n: seconds }));
  }
  ui["full-book"].href = "/books/" + encodeURIComponent(book.id);
  ui["full-finish"].action = "/hx/books/" + encodeURIComponent(book.id) + "/finished";
  for (const s of scrubs) {
    s.input.max = Math.floor(book.durationMs / 1000);
    s.input.removeAttribute("aria-valuetext");
    // a tick at every chapter start but the first
    s.ticks.replaceChildren(...book.chapters.filter((c) => c.start > 0).map((c) => {
      const tick = document.createElement("span");
      tick.className = "ts-scrub__tick";
      tick.style.setProperty("--at", (c.start / book.durationMs * 100).toFixed(2) + "%");
      return tick;
    }));
  }
  applyAccent();
}

// ---- Scrubbers: drag with fine control, snap to chapter starts, keys ----

const scrubs = [...document.querySelectorAll("[data-ts-scrub]")].map((el) => ({
  root: el,
  input: $("range", el),
  bubble: $("bubble", el),
  ticks: $("ticks", el),
  textAt: 0,
  drag: null, // { rect, x, y, raw, ms } while a pointer is down
}));

const fullScrub = scrubs.find((s) => s.root.dataset.tsScrub === "full"); // the other is the desktop bar's

function snap(ms) {
  const near = book.chapters.find((c) => c.start > 0 && Math.abs(c.start - ms) <= book.durationMs * SNAP_FRACTION);
  return near ? near.start : ms;
}

// The finger moves the position; the further it is from the bar vertically, the finer the scrub.
function dragTo(s, x, y) {
  const d = s.drag;
  const away = Math.abs(y - (d.rect.top + d.rect.height / 2));
  const rate = away > 80 ? 0.25 : away > 40 ? 0.5 : 1;
  d.raw = clamp(d.raw + (x - d.x) / d.rect.width * book.durationMs * rate, 0, book.durationMs);
  d.x = x;
  d.ms = snap(d.raw);
  const idx = chapterAt(d.ms);
  const chapter = book.chapters[idx];
  drawScrub(s, d.ms, false);
  s.bubble.textContent = fill(msg("scrubBubble"), { n: idx + 1, title: chapter.title, t: clock(d.ms - chapter.start) }).replace(" ·  ·", " ·");
  if (s === fullScrub) drawTimes(d.ms, player.state().speed);
}

function endDrag(s, apply) {
  const d = s.drag;
  if (!d) return;
  s.drag = null;
  s.root.classList.remove("is-dragging");
  ui["full-fine"].textContent = "";
  if (apply) player.seek(d.ms);
  else update("seek");
}

const scrubKeys = {
  ArrowLeft: () => player.skip(-1),
  ArrowDown: () => player.skip(-1),
  ArrowRight: () => player.skip(1),
  ArrowUp: () => player.skip(1),
  PageUp: () => player.chapter(-1),
  PageDown: () => player.chapter(1),
  Home: () => player.seek(0),
  End: () => player.seek(book.durationMs),
};

for (const s of scrubs) {
  const input = s.input;
  input.addEventListener("pointerdown", (event) => {
    if (!book || event.button > 0) return;
    event.preventDefault(); // the drag is ours, not the range input's
    input.focus({ preventScroll: true });
    input.setPointerCapture(event.pointerId);
    const rect = input.getBoundingClientRect();
    const raw = clamp((event.clientX - rect.left) / rect.width, 0, 1) * book.durationMs;
    s.drag = { rect, x: event.clientX, raw, ms: raw };
    s.root.classList.add("is-dragging");
    if (s === fullScrub) ui["full-fine"].textContent = msg("fineScrub");
    dragTo(s, event.clientX, event.clientY);
  });
  input.addEventListener("pointermove", (event) => {
    if (s.drag) dragTo(s, event.clientX, event.clientY);
  });
  input.addEventListener("pointerup", () => endDrag(s, true));
  input.addEventListener("pointercancel", () => endDrag(s, false));
  input.addEventListener("keydown", (event) => {
    const act = scrubKeys[event.key];
    if (!act || !book || event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    act();
  });
  // a screen reader's own adjust gesture changes the value without keys
  input.addEventListener("change", () => {
    if (!s.drag && book) player.seek(Number(input.value) * 1000);
  });
}

// ---- Opening, closing, swiping ----

// Runs change inside a view transition where the browser has them, so the cover glides between
// the bar and the player. from and to name who owns the shared "ts-now-cover" before and after.
function morph(from, to, change) {
  if (!document.startViewTransition || REDUCED.matches) {
    change();
    return;
  }
  root.classList.add("ts-vt-" + from);
  document.startViewTransition(() => {
    root.classList.replace("ts-vt-" + from, "ts-vt-" + to);
    change();
  }).finished.finally(() => root.classList.remove("ts-vt-" + to, "ts-vt-" + from));
}

function openPlayer() {
  if (!book || dialog.open) return;
  opener = document.activeElement;
  const show = () => {
    if (SIDE_PANEL.matches) dialog.show();
    else dialog.showModal();
    slot.classList.add("is-covered");
    update("open");
  };
  if (SIDE_PANEL.matches) show();
  else morph("mini", "full", show);
}

function closeNow() {
  closeSheet(false);
  slot.classList.remove("is-covered");
  dialog.close();
  const back = opener && opener.isConnected && opener !== document.body ? opener : miniOpen;
  back.focus({ preventScroll: true });
}

function closePlayer() {
  if (!dialog.open) return;
  if (SIDE_PANEL.matches) closeNow();
  else morph("full", "mini", closeNow);
}

// A press that moves vertically becomes a swipe; onMove follows the finger, onEnd decides. The
// document listens while the press lasts, so a mouse that leaves el is still followed, and a
// button inside el still gets its click when the press never became a swipe.
function swipe(el, enabled, onMove, onEnd) {
  el.addEventListener("pointerdown", (event) => {
    if (!enabled() || (event.pointerType === "mouse" && event.button)) return;
    const start = { x: event.clientX, y: event.clientY, live: false };
    const move = (e) => {
      const dy = e.clientY - start.y;
      if (!start.live && (Math.abs(dy) < SWIPE_MIN_PX || Math.abs(dy) < Math.abs(e.clientX - start.x))) return;
      start.live = true;
      onMove(dy);
    };
    const stop = (e) => {
      for (const type of ["pointermove", "pointerup", "pointercancel"]) document.removeEventListener(type, type === "pointermove" ? move : stop);
      if (start.live) onEnd(e.type === "pointercancel" ? 0 : e.clientY - start.y);
    };
    for (const type of ["pointermove", "pointerup", "pointercancel"]) document.addEventListener(type, type === "pointermove" ? move : stop);
  });
}

// Moves the open player to px below its place, then closes it or springs back.
function settle(px, closing) {
  dialog.classList.add("is-settling");
  dialog.style.setProperty("--ts-drag", px + "px");
  setTimeout(() => {
    dialog.classList.remove("is-settling");
    dialog.style.setProperty("--ts-drag", "0px");
    if (closing) closeNow();
  }, REDUCED.matches ? 0 : SETTLE_MS);
}

swipe(dialog.querySelector(".ts-player__head"), () => !SIDE_PANEL.matches,
  (dy) => dialog.style.setProperty("--ts-drag", Math.max(0, dy) + "px"),
  (dy) => (dy > dialog.offsetHeight * SWIPE_CLOSE_FRACTION ? settle(dialog.offsetHeight, true) : settle(0, false)));
swipe(slot.querySelector(".ts-mini"), () => !SIDE_PANEL.matches && !dialog.open, () => {}, (dy) => {
  if (dy < -SWIPE_OPEN_PX) openPlayer();
});

miniOpen.addEventListener("click", (event) => {
  if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || !book) return;
  event.preventDefault();
  if (dialog.open) closePlayer();
  else openPlayer();
});

dialog.addEventListener("cancel", (event) => {
  event.preventDefault(); // Escape: close the sheet, else the player, with the same motion as the button
  if (sheetOpener) closeSheet(true);
  else closePlayer();
});
dialog.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && sheetOpener && !ui["full-menu"].open) {
    event.preventDefault();
    closeSheet(true);
  }
});
dialog.addEventListener("close", () => slot.classList.remove("is-covered"));

// ---- Chapters sheet ----

function openSheet() {
  if (!book || sheetOpener) return;
  const position = player.state();
  const rows = book.chapters.map((chapter, idx) => {
    const row = ui["chapter-row"].content.firstElementChild.cloneNode(true);
    const button = row.firstElementChild;
    const title = chapter.title || fill(msg("chapterOf"), { n: idx + 1, total: book.chapters.length });
    const played = idx < position.chapterIdx;
    button.children[0].textContent = String(idx + 1);
    button.children[1].textContent = title;
    button.children[2].textContent = clock(chapter.end - chapter.start);
    button.children[3].hidden = !played;
    button.setAttribute("aria-label", played ? fill(msg("chapterPlayed"), { title }) : title);
    if (idx === position.chapterIdx) button.setAttribute("aria-current", "true");
    button.addEventListener("click", () => {
      player.seek(chapter.start);
      player.play();
      closeSheet(true);
    });
    return row;
  });
  ui["chapter-list"].replaceChildren(...rows);
  sheetOpener = document.activeElement;
  ui["chapters-sheet"].hidden = false;
  ui["full-body"].inert = true;
  (ui["chapter-list"].querySelector("[aria-current]") || ui["chapter-list"].firstElementChild.firstElementChild).focus();
}

function closeSheet(restoreFocus) {
  if (!sheetOpener) return;
  ui["chapters-sheet"].hidden = true;
  ui["full-body"].inert = false;
  if (restoreFocus) sheetOpener.focus();
  sheetOpener = null;
}

// ---- Clicks, menu ----

const actions = {
  "close-player": closePlayer,
  "full-prev": () => player.chapter(-1),
  "full-next": () => player.chapter(1),
  "full-back": () => player.skip(-1),
  "full-forward": () => player.skip(1),
  "full-toggle": () => player.toggle(),
  "open-chapters": openSheet,
  "sheet-close": () => closeSheet(true),
};

dialog.addEventListener("click", (event) => {
  for (const name in actions) {
    if (event.target.closest("[data-ts-" + name + "]")) return actions[name]();
  }
});

ui["full-book"].addEventListener("click", () => {
  ui["full-menu"].open = false;
  if (!SIDE_PANEL.matches) closeNow(); // the page behind the full screen player is the destination
});

// A plain form to the finished endpoint; htmx sends it without leaving the page.
ui["full-finish"].addEventListener("submit", () => {
  const fields = ui["full-finish"].elements;
  fields.next.value = location.pathname + location.search;
  try {
    fields._csrf.value = JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    fields._csrf.value = "";
  }
  ui["full-menu"].open = false;
});

// ---- Start-up ----

function start() {
  player = window.tsPlayer;
  document.addEventListener("ts:player:tick", () => update("tick"));
  document.addEventListener("ts:player:state", (event) => {
    if (event.detail.kind === "load" || !book || event.detail.bookId !== book.id) {
      book = player.info();
      if (book) drawBook();
    }
    update(event.detail.kind);
  });
  new MutationObserver(applyAccent).observe(root, { attributes: true, attributeFilter: ["data-theme"] });
  for (const query of ["(prefers-color-scheme: light)", "(prefers-contrast: more)"]) matchMedia(query).addEventListener("change", applyAccent);
  book = player.info();
  if (book) {
    drawBook();
    update("load");
  }
}

// player.js is an async module too: it may not have run yet.
if (window.tsPlayer) start();
else document.addEventListener("ts:player:ready", start, { once: true });
