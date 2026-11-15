// Tendshelf full player (P1c-5a; docs/architecture.md, Web player).
//
// A second module next to player.js. It owns the full player dialog (#player-full) and the compact
// scrubber of the full player; it never touches #audio and drives playback only through
// window.tsPlayer and its ts:player:* events. CSP: no inline style, no markup from data, no eval;
// custom properties only through style.setProperty. Every word comes from the data-ts-msg-*
// attributes of #player. P1c-5b adds the speed and sleep timer sheets, the sleep timer with its
// fade, the volume slider of the desktop bar, the bookmark button and the B key.

const slot = document.getElementById("player");
const dialog = document.getElementById("player-full");
const root = document.documentElement;
const LINK = document.body.dataset.tsLink; // link mode: no account, so no settings or bookmark requests
const SIDE_PANEL = matchMedia("(width >= 1280px)"); // from here the player is a side panel
// Larger text never uses the side panel: the desktop shell does not fit at the bigger size.
function sidePanel() { return SIDE_PANEL.matches && document.documentElement.dataset.textSize !== "large"; }
const REDUCED = matchMedia("(prefers-reduced-motion: reduce)");

const HEX = /^#[0-9a-f]{6}$/i;
const VALUE_TEXT_EVERY_MS = 10000; // aria-valuetext changes at most this often while playing
const SWIPE_MIN_PX = 8; // movement before a press becomes a swipe
const SWIPE_OPEN_PX = 40; // swipe up on the bar opens the player
const SWIPE_CLOSE_FRACTION = 0.25; // swipe down past this part of the screen closes it
const SWIPE_FLICK_PX = 48; // a quick flick closes it from this far ...
const SWIPE_FLICK_SPEED = 0.8; // ... at this many px per ms
const SWIPE_FADE = 0.5; // how much of the player fades out by the time it is dragged off the screen
const SETTLE_MS = 250;
const FORM_TYPE = "application/x-www-form-urlencoded";
const SPEED_SAVE_MS = 1000; // a speed change is saved this long after the last one
const LONG_PRESS_MS = 500; // a held press on the bar opens the speed sheet
const SLEEP_STEP_MS = 250;
const FADE_MS = 10000; // the last seconds of a sleep timer fade out
const PLUS_MS = 600000; // "+10 min"
const CHAPTER_MIN_LEFT_MS = 2000; // under this, the end-of-chapter choice means the next chapter's end
const SKIPPED_AHEAD_MS = 1500; // past a chapter timer's end by this much, the person skipped ahead
const SHAKE_PEAK = 12; // m/s², above gravity's noise
const SHAKE_GAP_MS = 150; // peaks closer than this are one jolt
const SHAKE_WINDOW_MS = 1000; // two jolts within this are a shake
const BOOKMARK_TOAST_MS = 4000;

let player = null; // window.tsPlayer
let book = null; // player.info() of the loaded book
let opener = null; // what had focus when the player opened
let sheetOpener = null; // what had focus when a sheet opened
const SHEET_LEAVE_MS = 400; // fallback for the sheet's 200 ms leave
let openName = ""; // "chapters", "speed" or "sleep" while a sheet is open

// ---- Elements and words ----

const $ = (name, scope = dialog) => scope.querySelector("[data-ts-" + name + "]");
const ui = {};
for (const name of ["close-player", "full-chapter", "full-cover", "full-cover-tile", "full-title", "full-chapter-title", "full-sep", "full-author", "full-clock", "full-left",
  "full-back", "full-forward", "full-back-num", "full-forward-num", "full-toggle", "full-elapsed", "full-fine",
  "full-book-left", "full-speed", "full-book", "full-finish", "full-menu", "full-body", "chapters-sheet", "chapter-list",
  "chapter-row", "open-chapters", "speed-sheet", "sleep-sheet", "speed-range", "speed-out", "sleep-meta", "sleep-custom",
  "sleep-minutes", "full-sleep-left", "full-toasts", "bookmark-toast", "open-speed", "open-sleep", "full-read", "speed-usual",
  "full-live", "full-show", "full-radio", "speed-for", "open-notes", "full-fav", "notes-sheet", "notes-body", "full-jump", "full-jump-time", "full-jump-left", "full-chapter-left", "full-bookbar-left"]) {
  ui[name] = $(name);
}
const miniOpen = $("open", slot);
const bar = { // the desktop bar's extras
  speed: $("bar-speed", slot),
  speedValue: $("bar-speed-value", slot),
  sleep: $("bar-sleep", slot),
  sleepLeft: $("bar-sleep-left", slot),
  chapters: $("bar-chapters", slot),
  read: $("bar-read", slot), // the book icon, beside the chapters icon
  expand: $("bar-expand", slot),
  volumeWrap: $("volume-wrap", slot),
  volume: $("volume", slot),
};

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

function csrf() {
  try {
    return JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return "";
  }
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
  // --ts-mark colours the phone's played marks: the cover accent when the book has one, else the theme's
  // played-wave token (the stylesheet's default), which always reaches 3:1.
  for (const [property, value] of [["--cover-accent", accent], ["--ts-mark", accent], ["--cover-tint", tint]]) {
    dialog.style.setProperty(property, HEX.test(value || "") ? value : ""); // "" removes the property
  }
}

// ---- Drawing: times, scrubbers ----

// Everything the full player shows for position ms; also follows a drag.
function drawTimes(ms, speed) {
  const idx = chapterAt(ms);
  const chapter = book.chapters[idx];
  const leftMs = Math.max(0, book.durationMs - ms) / speed;
  // Rounded to the nearest minute, like the length on the book's page, so the player never says more is left than
  // the book is long (17 m 20 s read "18 m left" beside "17 m"); it still never says 0 m while sound remains.
  const minutes = leftMs > 0 ? Math.max(1, Math.round(leftMs / 60000)) : 0;
  if (book.kind === "radio") {
    // a live station has no clock: the Live badge stands where the chapter number is
    setText("full-chapter", msg("live"));
    for (const name of ["full-clock", "full-left", "full-elapsed", "full-book-left", "full-chapter-title", "full-sep", "full-chapter-left", "full-bookbar-left", "full-jump-time", "full-jump-left"]) setText(name, "");
    return;
  }
  const episode = book.kind === "episode";
  const whole = episode && book.chapters.length <= 1; // an episode of one piece: its own words, one "left" line
  setText("full-chapter", whole ? msg("episodeWord") : player.chapterWords(book, idx).header);
  setText("full-clock", clock(ms - chapter.start));
  setText("full-left", whole ? fill(msg("leftInEpisode"), { t: clock(Math.max(0, book.durationMs - ms)) }) : fill(msg("leftInChapter"), { t: clock(chapter.end - ms) }));
  setText("full-elapsed", clock(ms - chapter.start));
  setText("full-book-left", whole ? "" : episode ? fill(msg("leftInEpisode"), { t: clock(Math.max(0, book.durationMs - ms)) }) : leftInBookText(minutes));
  // The phone player's twins, written from the same strings so the two layouts never disagree: its time row
  // shows the chapter's time left, the label over its book bar the book's, and its scrub card the target time.
  setText("full-chapter-left", ui["full-left"].textContent);
  setText("full-bookbar-left", ui["full-book-left"].textContent);
  setText("full-jump-time", ui["full-clock"].textContent);
  setText("full-jump-left", ui["full-left"].textContent);
  // The board: the chapter's name in serif italic, then the author. A chapter without a title, or a
  // book of one chapter, shows the author alone.
  const named = book.chapters.length > 1 && chapter.title;
  setText("full-chapter-title", named ? chapter.title : "");
  setText("full-sep", named ? msg("subtitleSep") : "");
}

// "2 m", "2 h" or "1 h 5 m" left in book, in the words the cards use (the server owns them). The live
// count rounds to the nearest minute but never says "0 m" while sound remains; 0 m shows only when nothing is left.
function leftInBookText(minutes) {
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  const form = hours === 0 ? "leftInBookMinutes" : rest === 0 ? "leftInBookHours" : "leftInBookBoth";
  return fill(msg(form), { h: hours, m: rest });
}

// The scrubber is one chapter wide: its left end is the chapter's start, its right end the chapter's
// end (decision 0005). chapter is fixed while a drag lasts, so the line does not change under the finger.
function drawScrub(s, ms, force, chapter = book.chapters[chapterAt(ms)]) {
  const into = clamp(ms - chapter.start, 0, chapter.end - chapter.start);
  setVars(s.root, { "--ts-pos": (into / Math.max(1, chapter.end - chapter.start)).toFixed(4) });
  s.input.max = Math.floor((chapter.end - chapter.start) / 1000);
  s.input.value = Math.floor(into / 1000);
  const now = performance.now();
  if (force || !s.input.hasAttribute("aria-valuetext") || now - s.textAt >= VALUE_TEXT_EVERY_MS) {
    s.textAt = now;
    const seconds = Math.floor(into / 1000);
    s.input.setAttribute("aria-valuetext", fill(msg("scrubValue"), { chapter: player.chapterWords(book, book.chapters.indexOf(chapter)).lead, m: Math.floor(seconds / 60), s: seconds % 60 }));
  }
}

// The slim ruler across the whole book: the fill follows the position, not interactive.
function drawRuler(ms) {
  setVars(ruler, { "--ts-pos": clamp(ms / (book.durationMs || 1), 0, 1).toFixed(4) });
  drawSegments(ms);
}

// The phone's book bar draws one segment per chapter, as wide as the chapter is long, so a tap on a segment
// is the chapter chapter-bar.js picks for that x. The segments are the ruler's children (--from/--to in
// percent, like the ticks); done and now change only when the chapter does.
function buildSegments() {
  ruler.segmentIndex = -1;
  const duration = book.durationMs || 1;
  const spans = book.chapters.map((c, i) => {
    const seg = document.createElement("span");
    seg.className = "ts-player__seg";
    if (i === book.chapters.length - 1) seg.classList.add("is-last");
    setVars(seg, { "--from": (c.start / duration * 100).toFixed(3) + "%", "--to": (c.end / duration * 100).toFixed(3) + "%" });
    return seg;
  });
  ruler.segs.querySelectorAll(".ts-player__seg").forEach((old) => old.remove());
  ruler.segs.prepend(...spans);
  fitSegments();
  if (player.state) drawSegments(player.state().positionMs);
}

// A segment under 4 px (a 2 px bar and a 2 px gap) would vanish: it loses its gap and radius so the bar reads
// as one run, and the ruler's mark stands in for it when it is the current one.
function fitSegments() {
  const width = ruler.segs.clientWidth;
  if (!width || !book) return;
  const segs = ruler.segs.querySelectorAll(".ts-player__seg");
  book.chapters.forEach((c, i) => {
    if (segs[i]) segs[i].classList.toggle("is-thin", (c.end - c.start) / (book.durationMs || 1) * width < 4);
  });
  ruler.classList.toggle("is-thin-now", !!segs[ruler.segmentIndex] && segs[ruler.segmentIndex].classList.contains("is-thin"));
}

function drawSegments(ms) {
  const idx = chapterAt(ms);
  if (idx === ruler.segmentIndex) return;
  const segs = ruler.segs.querySelectorAll(".ts-player__seg");
  ruler.segmentIndex = idx;
  segs.forEach((seg, i) => {
    seg.classList.toggle("is-done", i < idx);
    seg.classList.toggle("is-now", i === idx);
  });
  ruler.classList.toggle("is-thin-now", !!segs[idx] && segs[idx].classList.contains("is-thin"));
}

// "1.0×", "1.5×", "2.35×": one decimal, or two when the speed has them.
function speedNumber(speed) {
  return speed % 1 ? String(+speed.toFixed(2)) : speed.toFixed(1);
}

function speedText(speed) {
  return fill(msg("speedValue"), { v: speedNumber(speed) });
}

function update(kind) {
  if (!book) return;
  const state = player.state();
  const ms = state.positionMs;
  if (!scrub.drag) {
    drawScrub(scrub, ms, kind !== "tick");
    drawRuler(ms);
  }
  const speedLabel = fill(msg("speedLabel"), { v: speedNumber(state.speed) });
  bar.speedValue.textContent = speedText(state.speed);
  bar.speed.setAttribute("aria-label", speedLabel);
  if (document.activeElement !== bar.volume) bar.volume.value = Math.round(state.volume * 100);
  syncAddForms(ms);
  syncExpand();
  if (!dialog.open) return;
  if (!scrub.drag) drawTimes(ms, state.speed);
  dialog.classList.toggle("is-playing", state.playing);
  ui["full-toggle"].setAttribute("aria-label", (state.playing ? msg("pausePrefix") : msg("playPrefix")) + " " + book.title);
  setText("full-speed", speedText(state.speed));
  ui["open-speed"].setAttribute("aria-label", speedLabel);
  // the value is drawn in the accent when it is not the normal speed (the text itself already says which)
  ui["open-speed"].classList.toggle("is-active", state.speed !== 1);
  if (!ui["speed-sheet"].hidden) drawSpeedSheet(state.speed);
}

// The bar's "open the player" button: open or closed, and what pressing it would do.
function syncExpand() {
  if (!book) return;
  bar.expand.setAttribute("aria-expanded", String(dialog.open));
  const at = player.state().positionMs;
  const idx = chapterAt(at);
  const left = Math.max(1, Math.ceil((book.chapters[idx].end - at) / 60000));
  const open = book.kind === "radio" ? fill(msg("openStation"), { title: book.title }) : fill(msg("openPlayer"), { title: book.title, chapter: player.chapterWords(book, idx).inline, m: left });
  bar.expand.setAttribute("aria-label", dialog.open ? msg("closePlayer") : open);
}

// What differs between a book and a podcast episode: the page it opens, the finished mark, the end-of-timer
// choice and the controls that only a book has (bookmarks and the book's reader).
// Sets an element's words for an episode or back to the book's (kept the first time).
function wordsFor(el, episode, episodeWords) {
  if (!el.dataset.tsBookWords) el.dataset.tsBookWords = el.textContent;
  el.textContent = episode ? episodeWords : el.dataset.tsBookWords;
}

function drawKind() {
  const episode = book.kind === "episode";
  const live = book.kind === "radio";
  dialog.classList.toggle("is-live", live); // the stylesheet hides what a live stream cannot do
  // The two facts the stylesheet keys on (the only place they are set): the kind, and whether there is more than one chapter.
  dialog.dataset.tsKind = live ? "radio" : episode ? "episode" : "book";
  dialog.dataset.tsChapters = book.chapters.length > 1 ? "many" : "one";
  const id = encodeURIComponent(book.id);
  ui["full-book"].href = episode ? "/podcasts/episodes/" + id : "/books/" + id;
  wordsFor(ui["full-book"], episode, msg("goToEpisode"));
  ui["full-finish"].action = episode ? "/podcasts/episodes/" + id + "/played" : "/hx/books/" + id + "/finished";
  wordsFor(ui["full-finish"].querySelector("button"), episode, msg("markPlayed"));
  ui["full-show"].href = book.showHref || "/";
  ui["full-show"].hidden = !episode || !book.showHref || !!LINK;
  ui["full-radio"].hidden = !live;
  // A station carries no address of a book or an episode, even while its entries are hidden.
  if (live) {
    ui["full-book"].href = "/radio";
    ui["full-finish"].action = "/radio";
  }
  for (const item of [ui["full-book"], ui["full-finish"], dialog.querySelector(".ts-player__menuqueue"), dialog.querySelector("[data-ts-eyes-free-open]")]) {
    item.hidden = live || !!LINK;
  }
  wordsFor(ui["speed-for"], episode, msg("speedForShow"));
  ui["speed-usual"].hidden = episode || live;
  // found by either name: after an episode the field is called "played", and a lookup by "finished" alone would throw
  const finishField = ui["full-finish"].querySelector('[name="finished"], [name="played"]');
  finishField.name = episode ? "played" : "finished";
  const choice = ui["sleep-sheet"].querySelector('[data-ts-sleep-choice="chapter"], [data-ts-sleep-choice="episode"]');
  choice.dataset.tsSleepChoice = episode ? "episode" : "chapter";
  wordsFor(choice.querySelector("span"), episode, msg("sleepEndOfEpisode"));
  // an episode without chapters of its own has nothing to list
  const noChapters = (episode || live) && book.chapters.length <= 1;
  ui["open-chapters"].hidden = noChapters;
  ui["open-notes"].hidden = !(episode && book.hasNotes);
  if (book.id !== ui["notes-body"].dataset.tsFor) ui["notes-body"].replaceChildren(); // another item: its notes are fetched on open
  bar.chapters.hidden = noChapters;
  for (const mark of [...slot.querySelectorAll("[data-ts-bookmark], [data-ts-bar-bookmark]"), ...dialog.querySelectorAll("[data-ts-bookmark]")]) {
    mark.hidden = episode || live || !!LINK;
  }
  choice.closest("li").hidden = live; // no end of a chapter on the radio
  drawFavourite();
}

// The save / star button replaces the bookmark for an episode or a station; a book keeps its bookmark.
function drawFavourite() {
  const button = ui["full-fav"];
  const episode = book.kind === "episode";
  const live = book.kind === "radio";
  button.hidden = !(episode || live) || !!LINK;
  const on = Boolean(book.favourite);
  button.setAttribute("aria-pressed", on ? "true" : "false");
  // A star is a toggle with a fixed name; a station says what pressing does, as the Radio page does.
  const label = live ? fill(msg(on ? "removeStation" : "saveStation"), { name: book.title }) : msg("starEpisode");
  button.setAttribute("aria-label", label);
  button.title = label;
  const filled = button.querySelector("svg");
  if (filled) filled.classList.toggle("ts-icon--fill", on); // the filled star is the second cue beside aria-pressed
}

let favouriting = false; // one request at a time

async function toggleFavourite() {
  if (!book || favouriting || LINK || (book.kind !== "episode" && book.kind !== "radio")) return;
  favouriting = true;
  const live = book.kind === "radio";
  const on = !book.favourite;
  const id = encodeURIComponent(book.id);
  const url = live ? "/radio/stations/" + id + (on ? "/save" : "/remove") : "/podcasts/episodes/" + id + "/favourite";
  const sent = book; // a press for one item must not change the next item if the player moved on meanwhile
  try {
    const res = await fetch(url, {
      method: "POST",
      headers: { Accept: "application/json", "X-CSRF-Token": csrf(), "Content-Type": FORM_TYPE },
      body: live ? "" : "on=" + (on ? "1" : "0"),
    });
    if (res.status === 204) {
      sent.favourite = on;
      player.setFavourite(sent.id, on);
      if (book === sent) drawFavourite();
      player.toast("hint", msg(live ? (on ? "favSaved" : "favRemoved") : (on ? "favStarred" : "favUnstarred")));
    } else if (res.status === 429) {
      player.toast("err", msg("favBusy"));
    } else if (res.status === 422) {
      player.toast("err", msg("favFull"));
    } else {
      player.toast("err", msg("favFailed"));
    }
  } catch {
    player.toast("err", msg("favFailed"));
  } finally {
    favouriting = false;
  }
}

// What changes only when another book loads.
function drawBook() {
  // The book icon asks where to read from; it shows only for a book that has an ebook the person may read.
  for (const button of [bar.read, ui["full-read"]]) button.hidden = !book.readChoose;
  setText("full-title", book.title);
  setText("full-author", book.author);
  if (book.cover && ui["full-cover"].getAttribute("src") !== book.cover) ui["full-cover"].src = book.cover;
  ui["full-cover"].hidden = !book.cover;
  // A station with no picture shows its initials tile (the same one the bar draws).
  const tile = ui["full-cover-tile"];
  tile.hidden = book.cover || book.kind !== "radio" || !book.logoInitials;
  if (!tile.hidden) {
    tile.querySelector(".ts-stnlogo__text").textContent = book.logoInitials;
    tile.className = "ts-avatar ts-stnlogo ts-stnlogo--fill ts-avatar--c" + book.logoColour;
  }
  for (const direction of ["back", "forward"]) {
    const seconds = direction === "back" ? book.skipBack : book.skipForward;
    setText("full-" + direction + "-num", String(seconds));
    ui["full-" + direction].setAttribute("aria-label", fill(msg(direction + "Seconds"), { n: seconds }));
  }
  drawKind();
  scrub.input.removeAttribute("aria-valuetext");
  // a ruler tick at every chapter start but the first
  ruler.ticks.replaceChildren(...book.chapters.filter((c) => c.start > 0).map((c) => {
    const tick = document.createElement("span");
    tick.className = "ts-ruler__tick";
    tick.style.setProperty("--at", (c.start / book.durationMs * 100).toFixed(2) + "%");
    return tick;
  }));
  buildSegments();
  applyAccent();
}

// ---- Scrubbers: a chapter wide; click or drag anywhere in it, fine control, keys ----

const scrub = { // the full player's chapter slider; the desktop bar's wave is waveform.js
  root: $("scrub"),
  input: $("range"),
  bubble: $("bubble"),
  textAt: 0,
  drag: null, // { rect, x, y, raw, ms } while a pointer is down
};


const ruler = document.querySelector("[data-ts-ruler]");
ruler.ticks = $("ruler-ticks", ruler);
ruler.segs = $("ruler-segs", ruler);
ruler.segmentIndex = -1;
// The ruler's width changes with the window and the text size; the thin segments follow it.
new ResizeObserver(fitSegments).observe(ruler.segs);

// The finger moves the position inside the chapter the drag began in (the line is that chapter's
// length); the further it is from the bar vertically, the finer the scrub.
function dragTo(s, x, y) {
  const d = s.drag;
  const chapter = d.chapter;
  const away = Math.abs(y - (d.rect.top + d.rect.height / 2));
  const rate = away > 80 ? 0.25 : away > 40 ? 0.5 : 1;
  d.ms = clamp(d.ms + (x - d.x) / d.rect.width * (chapter.end - chapter.start) * rate, chapter.start, chapter.end);
  d.x = x;
  drawScrub(s, d.ms, false, chapter);
  // A title that is only "Chapter 6" would repeat the number the bubble already shows.
  const title = /^\D*\d+$/.test(chapter.title) ? "" : chapter.title;
  const words = player.chapterWords(book, book.chapters.indexOf(chapter));
  // A chapter shown by its short name ("Prologue") would repeat it in front of its longer title.
  const shownTitle = words.numbered || title !== words.lead ? title : "";
  s.bubble.textContent = fill(msg("scrubBubble"), { chapter: words.lead, title: shownTitle, t: clock(d.ms - chapter.start) }).replace(" ·  ·", " ·");
  drawTimes(d.ms, player.state().speed);
  // The phone's time row shows where the book was before the finger went down, in place of the chapter's time left.
  setText("full-chapter-left", fill(msg("scrubWas"), { t: clock(d.from - chapter.start) }));
  ui["full-chapter-left"].classList.add("is-was");
  drawRuler(d.ms);
}

function endDrag(s, apply) {
  const d = s.drag;
  if (!d) return;
  s.drag = null;
  s.root.classList.remove("is-dragging");
  setVars(s.root, { "--ts-from": "" }); // an empty value removes it: the stylesheet's default follows the position again
  ui["full-chapter-left"].classList.remove("is-was");
  ui["full-fine"].textContent = "";
  if (apply) {
    scrubFrom = d.from;
    player.seek(d.ms);
    scrubFrom = -1;
  } else update("seek");
}

// The chapter the scrubber shows: the one that holds the position.
function chapterNow() {
  return book.chapters[chapterAt(player.state().positionMs)];
}

const scrubKeys = {
  ArrowLeft: () => player.skip(-1),
  ArrowDown: () => player.skip(-1),
  ArrowRight: () => player.skip(1),
  ArrowUp: () => player.skip(1),
  PageUp: () => player.chapter(-1),
  PageDown: () => player.chapter(1),
  Home: () => player.seek(chapterNow().start),
  End: () => player.seek(chapterNow().end),
};

{
  const s = scrub;
  const input = s.input;
  input.addEventListener("pointerdown", (event) => {
    if (!book || event.button > 0) return;
    event.preventDefault(); // the drag is ours, not the range input's
    input.focus({ preventScroll: true });
    input.setPointerCapture(event.pointerId);
    const rect = input.getBoundingClientRect();
    const chapter = chapterNow();
    const ms = chapter.start + clamp((event.clientX - rect.left) / rect.width, 0, 1) * (chapter.end - chapter.start);
    const from = player.state().positionMs;
    s.drag = { rect, chapter, x: event.clientX, ms, from };
    s.root.classList.add("is-dragging");
    // Where the position was, as a share of the chapter: the wave marks what the drag will skip over from there.
    setVars(s.root, { "--ts-from": clamp((from - chapter.start) / Math.max(1, chapter.end - chapter.start), 0, 1).toFixed(4) });
    ui["full-fine"].textContent = msg("fineScrub");
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
    if (!s.drag && book) player.seek(chapterNow().start + Number(input.value) * 1000);
  });
}

// The chapter bar (chapter-bar.js) asks for a jump to a chapter start; it goes through the scrubber's
// path, so a jump over two seconds away offers the undo.
document.addEventListener("ts:chapterbar:seek", (event) => {
  if (!book || event.detail.bookId !== book.id) return;
  event.preventDefault();
  scrubFrom = player.state().positionMs;
  player.seek(event.detail.ms);
  scrubFrom = -1;
});

// ---- Opening, closing, swiping ----

// True while the full screen player slides (up when it opens, down when it closes or springs back).
// A press must not start a swipe then.
let sliding = false;
// The timer that ends the slide now under way. A close can take over a rise or a spring-back.
let slideTimer = 0;
// True once a close has started, until the dialog is shut.
let leaving = false;

// after runs once the player is open. On a phone or tablet the real dialog rises from below the
// screen, the reverse of how a swipe closes it; the side panel just appears.
function openPlayer(after) {
  if (!book || dialog.open || leaving) return;
  opener = document.activeElement;
  const rise = !sidePanel() && !REDUCED.matches;
  // Start fully below the screen and fully opaque, before the dialog is first painted.
  if (rise) {
    dialog.style.setProperty("--ts-drag", window.innerHeight + "px");
    dialog.style.setProperty("--ts-fade", "1");
  }
  if (sidePanel()) dialog.show();
  else dialog.showModal();
  slot.classList.add("is-covered");
  // All content is filled in this same task, so nothing pops in while the player slides.
  update("open");
  drawSleep();
  markScrolls();
  if (rise) {
    sliding = true;
    dialog.getBoundingClientRect(); // commit the start position, so the transition has somewhere to start from
    dialog.classList.add("is-rising");
    dialog.style.setProperty("--ts-drag", "0px");
    slideTimer = setTimeout(() => {
      dialog.classList.remove("is-rising");
      sliding = false;
    }, SETTLE_MS);
  }
  if (after) after();
}

function closeNow() {
  closeSheet(false, true);
  for (const wrap of Object.values(sheetWraps)) hideSheetNow(wrap);
  slot.classList.remove("is-covered");
  dialog.close();
  leaving = false;
  syncExpand();
  const back = opener && opener.isConnected && opener !== document.body ? opener : miniOpen;
  back.focus({ preventScroll: true });
}

// The button, Escape and Android back slide the player down like a finished swipe. A close during
// the rise is honoured: it turns back from wherever the player has got to. A second close is ignored.
function closePlayer() {
  if (!dialog.open || leaving) return;
  if (sidePanel()) closeNow();
  else settle(dialog.offsetHeight, true);
}

// A press that moves vertically becomes a swipe; onMove follows the finger, onEnd decides (with
// the distance and the speed in px per ms). The document listens while the press lasts, so a mouse
// that leaves el is still followed, and a button inside el still gets its click when the press never
// became a swipe. allow(event) can refuse a press; a mostly sideways press is never a swipe.
function swipe(el, enabled, onMove, onEnd, allow) {
  el.addEventListener("pointerdown", (event) => {
    if (!enabled() || !event.isPrimary || (event.pointerType === "mouse" && event.button) || (allow && !allow(event))) return;
    const start = { x: event.clientX, y: event.clientY, at: event.timeStamp, live: false, dead: false };
    const move = (e) => {
      const dy = e.clientY - start.y;
      const dx = Math.abs(e.clientX - start.x);
      if (start.dead) return;
      if (!start.live && dx >= SWIPE_MIN_PX && dx > Math.abs(dy)) start.dead = true;
      else if (start.live || (Math.abs(dy) >= SWIPE_MIN_PX && Math.abs(dy) >= dx)) {
        start.live = true;
        onMove(dy);
      }
    };
    const stop = (e) => {
      for (const type of ["pointermove", "pointerup", "pointercancel"]) document.removeEventListener(type, type === "pointermove" ? move : stop);
      const dy = e.type === "pointercancel" ? 0 : e.clientY - start.y;
      if (start.live) onEnd(dy, dy / Math.max(1, e.timeStamp - start.at));
    };
    for (const type of ["pointermove", "pointerup", "pointercancel"]) document.addEventListener(type, type === "pointermove" ? move : stop);
  });
}

// Moves the open player to px below its place (fading with it), then closes it or springs back.
// With reduced motion nothing is animated: it just closes.
function settle(px, closing) {
  if (REDUCED.matches) {
    moveTo(0);
    if (closing) closeNow();
    return;
  }
  clearTimeout(slideTimer);
  if (closing) leaving = true;
  sliding = true;
  dialog.classList.remove("is-rising");
  dialog.classList.add("is-settling");
  moveTo(px);
  slideTimer = setTimeout(() => {
    sliding = false;
    dialog.classList.remove("is-settling");
    moveTo(0);
    if (closing) closeNow();
  }, SETTLE_MS);
}

// --ts-drag moves the player, --ts-fade lets what is behind show through; player.css reads both.
function moveTo(px) {
  dialog.style.setProperty("--ts-drag", px + "px");
  dialog.style.setProperty("--ts-fade", String(1 - SWIPE_FADE * Math.min(1, px / Math.max(1, dialog.offsetHeight))));
}

// The body scrolls on a very short or large-text screen; there a drag must stay free to scroll it (player-fit.css).
function markScrolls() {
  ui["full-body"].classList.toggle("is-scrolling", ui["full-body"].scrollHeight > ui["full-body"].clientHeight);
}
window.addEventListener("resize", markScrolls);

// A touch swipe may start anywhere that is not a control, the scrubbers, an open sheet or a list
// that is scrolled down. The header takes any pointer (it has no scrolling).
const SWIPE_REFUSED = "button, a, input, select, textarea, summary, [role=slider], [data-ts-chbar], [data-ts-scrub], .ts-player__scrub, .ts-player__menubody";
function swipeAllowed(event) {
  const target = event.target;
  if (target.closest(".ts-player__head")) return true;
  if (event.pointerType !== "touch" || openName || target.closest(SWIPE_REFUSED)) return false;
  for (let node = target; node && node !== dialog; node = node.parentElement) {
    if (node.scrollTop > 0) return false;
  }
  return true;
}

swipe(dialog, () => !sidePanel() && !sliding && !leaving,
  (dy) => REDUCED.matches || moveTo(Math.max(0, dy)),
  (dy, speed) => (dy > dialog.offsetHeight * SWIPE_CLOSE_FRACTION || (dy > SWIPE_FLICK_PX && speed > SWIPE_FLICK_SPEED) ? settle(dialog.offsetHeight, true) : settle(0, false)),
  swipeAllowed);
swipe(slot.querySelector(".ts-mini"), () => !sidePanel() && !dialog.open, () => {}, (dy) => {
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
  if (openName) closeSheet(true);
  else closePlayer();
});
dialog.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && openName && !ui["full-menu"].open) {
    event.preventDefault();
    closeSheet(true);
  }
});
dialog.addEventListener("close", () => {
  slot.classList.remove("is-covered");
  drawSleep(); // the card moves from the player to the bar
});

// ---- Sheets: chapters, speed, sleep timer ----

const sheetWraps = { chapters: ui["chapters-sheet"], speed: ui["speed-sheet"], sleep: ui["sleep-sheet"], notes: ui["notes-sheet"] };

function fillChapters() {
  const position = player.state();
  const rows = book.chapters.map((chapter, idx) => {
    const row = ui["chapter-row"].content.firstElementChild.cloneNode(true);
    const button = row.firstElementChild;
    const words = player.chapterWords(book, idx);
    const title = chapter.title || words.header;
    const played = idx < position.chapterIdx;
    button.children[0].textContent = words.column; // empty for a chapter without a number
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
}

// Scrolls only the chapter list so the current chapter sits in the middle of it.
function centreCurrentChapter(row) {
  const list = ui["chapter-list"];
  const above = row.getBoundingClientRect().top - list.getBoundingClientRect().top;
  list.scrollTop += above - (list.clientHeight - row.offsetHeight) / 2;
}

// Fills the notes sheet from the episode panel's fragment (the same gate as the page) and keeps only the
// .ts-podnotes node, so no id of the panel lands twice in the document. A failure shows one plain line;
// the next open asks again.
function fillNotes() {
  const body = ui["notes-body"];
  const ep = book;
  if (body.dataset.tsFor === ep.id) return;
  const failed = () => {
    if (book !== ep) return;
    const line = document.createElement("p");
    line.className = "meta";
    line.textContent = msg("notesFailed");
    body.replaceChildren(line);
  };
  fetch("/hx/player/episode-panel/" + encodeURIComponent(ep.id), { headers: { "HX-Request": "true" } })
    .then((res) => (res.ok ? res.text() : Promise.reject(new Error("notes"))))
    .then((html) => {
      const node = new DOMParser().parseFromString(html, "text/html").querySelector(".ts-podnotes");
      if (!node) return failed();
      if (book !== ep) return;
      body.replaceChildren(document.importNode(node, true));
      body.dataset.tsFor = ep.id;
    })
    .catch(failed);
}

// Opens a sheet over the player, opening the player first when it is closed. from is the control
// that asked, so closing the sheet gives focus back to it.
function openSheet(name, from = document.activeElement) {
  if (!book || openName) return;
  if (!dialog.open) {
    openPlayer(() => openSheet(name, from));
    return;
  }
  if (name === "chapters") fillChapters();
  if (name === "speed") drawSpeedSheet(player.state().speed);
  if (name === "sleep") drawSleep();
  if (name === "notes") fillNotes();
  openName = name;
  sheetOpener = from;
  const wrap = sheetWraps[name];
  hideSheetNow(wrap); // cancels a leave still running, so the sheet is never hidden while open
  wrap.hidden = false;
  ui["full-body"].inert = true;
  // Focus the current choice, else the first. preventScroll: a focus that scrolls would also scroll
  // the player body behind the sheet (the whole panel jumped); the chapter list scrolls itself.
  const choice = wrap.querySelector("[aria-current], [aria-checked=true]") || wrap.querySelector("ol button, [role=radio]:not([hidden])") || wrap.querySelector("button[data-ts-sheet-close]");
  choice.focus({ preventScroll: true });
  if (name === "chapters") centreCurrentChapter(choice);
}

// Ends a sheet's leave (or hides it at once): back to its resting state, hidden, nothing left over.
const sheetLeaveTimers = new WeakMap();
function hideSheetNow(wrap) {
  clearTimeout(sheetLeaveTimers.get(wrap));
  for (const el of wrap.querySelectorAll(".is-leaving")) el.classList.remove("is-leaving");
  wrap.inert = false;
  wrap.hidden = true;
}

// The sheet sinks (the same is-leaving rules as the #sheet sheets, motion.css) and is hidden when the
// animation ends; the timeout is the fallback when no animation runs. Focus and the player body do not wait.
function leaveSheet(wrap) {
  const sheet = wrap.querySelector(".ts-sheet");
  wrap.inert = true;
  for (const el of wrap.querySelectorAll(".ts-sheet, .ts-scrim")) el.classList.add("is-leaving");
  const done = () => {
    sheet.removeEventListener("animationend", onEnd);
    hideSheetNow(wrap);
  };
  const onEnd = (event) => {
    if (event.target === sheet) done();
  };
  sheet.addEventListener("animationend", onEnd);
  sheetLeaveTimers.set(wrap, setTimeout(done, SHEET_LEAVE_MS));
}

function closeSheet(restoreFocus, instant = false) {
  if (!openName) return;
  const wrap = sheetWraps[openName];
  if (instant) hideSheetNow(wrap);
  else leaveSheet(wrap);
  ui["full-body"].inert = false;
  if (restoreFocus && sheetOpener && sheetOpener.isConnected) sheetOpener.focus({ preventScroll: true });
  openName = "";
  sheetOpener = null;
}

// A bar tool opens its sheet, and pressed again closes it (the sheet's own X still does too). With another
// sheet open it switches to its own.
function toggleSheet(name, from) {
  const wasOpen = openName;
  if (wasOpen) closeSheet(wasOpen === name);
  if (wasOpen !== name) openSheet(name, from);
}

// Up and Down move between the choices of a sheet; Enter and Space choose (they are buttons).
for (const group of dialog.querySelectorAll(".ts-options")) {
  group.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const choices = [...group.querySelectorAll("[role=radio]:not([hidden])")];
    const at = choices.indexOf(document.activeElement);
    if (at < 0) return;
    event.preventDefault();
    choices[(at + (event.key === "ArrowDown" ? 1 : -1) + choices.length) % choices.length].focus();
  });
}

// ---- Speed: presets and a fine slider; saved to the person's settings ----

let speedTimer = 0;
let speedUnsaved = false;

function drawSpeedSheet(speed) {
  for (const choice of ui["speed-sheet"].querySelectorAll("[data-ts-speed-choice]")) {
    choice.setAttribute("aria-checked", String(Math.abs(Number(choice.dataset.tsSpeedChoice) - speed) < 0.001));
  }
  setText("speed-out", speedText(speed));
  if (document.activeElement !== ui["speed-range"]) ui["speed-range"].value = speed;
}

// Speed is remembered for this book and follows the person to other devices: POST /hx/books/{id}/speed,
// quietly (the book's own speed; a book never changed plays at the usual speed). A failure changes
// nothing this session. A change waits a second for the next one; leaving the page sends it at once.
// The sheet's usual-speed button (makeUsual) sets the usual speed instead and the book follows it again.
function saveSpeed(leaving, makeUsual) {
  clearTimeout(speedTimer);
  if ((!speedUnsaved && !makeUsual) || LINK) return;
  speedUnsaved = false;
  const state = player.state();
  if (state.itemKind === "radio") return;
  if (state.itemKind === "episode") { // an episode's speed is its show's: the settings handler keeps the fields not sent
    if (!book || !book.showHref || book.id !== state.bookId) return;
    fetch(book.showHref + "/settings", {
      method: "POST",
      keepalive: leaving === true,
      headers: { "X-CSRF-Token": csrf(), "HX-Request": "true", "Content-Type": FORM_TYPE },
      body: "speed=" + state.speed.toFixed(2),
    }).catch(() => {});
    return;
  }
  fetch("/hx/books/" + encodeURIComponent(state.bookId) + "/speed", {
    method: "POST",
    keepalive: leaving === true,
    headers: { "X-CSRF-Token": csrf(), "HX-Request": "true", "Content-Type": FORM_TYPE },
    body: "speed=" + state.speed.toFixed(2) + (makeUsual ? "&make_default=1" : ""),
  }).catch(() => {});
}

function changeSpeed(value) {
  player.setSpeed(value);
  speedUnsaved = true;
  clearTimeout(speedTimer);
  speedTimer = setTimeout(saveSpeed, SPEED_SAVE_MS);
}

ui["speed-sheet"].addEventListener("click", (event) => {
  const choice = event.target.closest("[data-ts-speed-choice]");
  if (!choice) return;
  changeSpeed(Number(choice.dataset.tsSpeedChoice));
  closeSheet(true);
});
ui["speed-range"].addEventListener("input", () => changeSpeed(Number(ui["speed-range"].value)));
addEventListener("pagehide", () => saveSpeed(true));
ui["speed-usual"].addEventListener("click", () => {
  saveSpeed(false, true);
  player.toast("hint", msg("speedUsualSet"));
});

// ---- Sleep timer ----
//
// Counts only while sound plays, from performance.now() deltas (a throttled timer stays accurate),
// and lives in memory: a reload clears it. The end-of-chapter choice is the chapter's end position when it is
// set; "minutes" is real minutes whatever the speed. The last ten seconds fade the volume out
// (tsPlayer.setFade multiplies the person's level; it never replaces it).

const sleepState = { choice: "", mode: "off", leftMs: 0, endMs: 0, lastAt: 0, fading: false, said: false, motion: false, peakAt: 0, peaks: 0 };
let sleepTimer = 0;
let motionAllowed = false;
const cards = [...document.querySelectorAll("[data-ts-sleep-card]")].map((el) => ({
  el,
  count: $("sleep-count", el),
  hint: $("sleep-hint", el),
  said: $("sleep-said", el),
}));
const coarsePointer = matchMedia("(pointer: coarse)");

// In the Android app the timer runs in the service (it must work with the screen off and the page gone). The
// page only sends the choice and draws what the app pushes (the app bridge file, "nativesleep"); nothing here decides.
const nativeSleep = () => window.tsNativeAudio;

// Where the end-of-chapter choice ends: this chapter's end, or the next one's when almost there.
function chapterEndNow() {
  if (book.kind === "episode") return book.durationMs; // the end of the episode is where the sleep timer rests
  const state = player.state();
  let idx = state.chapterIdx;
  if (book.chapters[idx].end - state.positionMs < CHAPTER_MIN_LEFT_MS && book.chapters[idx + 1]) idx++;
  return book.chapters[idx].end;
}

// Milliseconds of listening left; for a chapter, what is left of it at the current speed.
function sleepLeft() {
  if (nativeSleep()) {
    const running = player.state().playing && sleepState.mode !== "off";
    return Math.max(0, sleepState.leftMs - (running ? performance.now() - sleepState.lastAt : 0));
  }
  if (sleepState.mode === "chapter") {
    const state = player.state();
    return Math.max(0, sleepState.endMs - state.positionMs) / state.speed;
  }
  return sleepState.leftMs;
}

// iOS asks the person before it shares motion; the question must come from a tap, so it is asked here.
function askMotion() {
  motionAllowed = false;
  if (typeof DeviceMotionEvent === "undefined") return;
  if (typeof DeviceMotionEvent.requestPermission === "function") {
    DeviceMotionEvent.requestPermission().then((answer) => (motionAllowed = answer === "granted")).catch(() => {});
  } else {
    motionAllowed = true;
  }
}

// What the app pushed: the page just shows it. "custom" is any minutes that are not a row of the sheet.
function onNativeSleep() {
  const pushed = nativeSleep().sleepState;
  const row = ui["sleep-sheet"].querySelector(`[data-ts-sleep-choice="${CSS.escape(pushed.choice)}"]`);
  sleepState.mode = pushed.mode;
  sleepState.choice = pushed.choice && !row ? "custom" : pushed.choice;
  sleepState.leftMs = pushed.leftMs;
  sleepState.lastAt = performance.now();
  sleepState.fading = pushed.fading;
  if (!pushed.fading) sleepState.said = false;
  motionAllowed = true; // the app listens to the sensor during the fade
  drawSleep();
}

// ---- Night sleep: playback that starts inside the person's night hours starts the timer ----

const NIGHT_OFF_KEY = "ts-night-sleep-off"; // sessionStorage: when the window the person switched the timer off in ends
let autoStarted = false; // the running timer was started by night sleep, not chosen

// Whether a time of day (minutes after midnight) is inside the window; it wraps midnight when from is
// later than to. The start is in, the end is out, and from == to is an empty window.
function inNightWindow(nowMinutes, from, to) {
  if (from === to) return false;
  if (from < to) return nowMinutes >= from && nowMinutes < to;
  return nowMinutes >= from || nowMinutes < to;
}

// The device's clock decides, not the stored time zone: the person is in bed where the device is.
function nightNowMinutes(now) {
  return now.getHours() * 60 + now.getMinutes();
}

// The moment the window closes (only meaningful inside it).
function nightWindowEndMs(now, to) {
  const minutesLeft = (to - nightNowMinutes(now) + 1440) % 1440;
  return now.getTime() + minutesLeft * 60000 - now.getSeconds() * 1000 - now.getMilliseconds();
}

function nightMarkRead() {
  try {
    return Number(sessionStorage.getItem(NIGHT_OFF_KEY)) || 0;
  } catch {
    return 0;
  }
}

function nightMarkWrite(untilMs) {
  try {
    sessionStorage.setItem(NIGHT_OFF_KEY, String(untilMs));
  } catch {
    // storage is blocked: the timer may start again on the next play, which is harmless
  }
}

// The person turned an automatic timer off: leave it off until the window ends.
function nightSwitchedOff() {
  if (!autoStarted || !book || !book.night) return;
  nightMarkWrite(nightWindowEndMs(new Date(), book.night.to));
}

// A choice made by the person (the sheet, Eyes-free, the toast): it may be the "off" that night sleep respects.
function chooseSleep(choice, minutes) {
  if (choice === "off") nightSwitchedOff();
  setSleep(choice, minutes);
}

// Called when playback goes from paused to playing.
function maybeStartNightSleep() {
  const night = book && book.night;
  if (!night || sleepState.mode !== "off") return;
  const now = new Date();
  if (!inNightWindow(nightNowMinutes(now), night.from, night.to)) return;
  if (nightMarkRead() > now.getTime()) return;
  setSleep("minutes", night.minutes);
  autoStarted = true; // after setSleep, which clears it
  player.toast("hint", fill(msg("nightSleepOn"), { n: night.minutes }), [{
    label: msg("nightSleepOff"),
    run: () => chooseSleep("off"),
  }]);
}

function setSleep(choice, minutes) {
  autoStarted = false;
  if (nativeSleep()) {
    nativeSleep().sleep(choice === "off" || choice === "chapter" || choice === "episode" ? choice : String(Math.min(999, Math.max(1, Math.round(minutes)))));
    // Shown at once; the app's answer, a moment later, replaces it (Eyes-free reads this right after the tap).
    sleepState.mode = choice === "off" ? "off" : choice === "chapter" || choice === "episode" ? "chapter" : "minutes";
    sleepState.choice = choice;
    return;
  }
  clearInterval(sleepTimer);
  sleepState.choice = choice;
  stopFade();
  if (choice === "off") {
    sleepState.mode = "off";
  } else {
    const toEnd = choice === "chapter" || choice === "episode"; // the end of the chapter, or of the episode
    sleepState.mode = toEnd ? "chapter" : "minutes";
    sleepState.endMs = toEnd ? chapterEndNow() : 0;
    sleepState.leftMs = minutes * 60000;
    sleepState.lastAt = performance.now();
    askMotion();
    sleepTimer = setInterval(sleepTick, SLEEP_STEP_MS);
  }
  drawSleep();
}

function stopFade() {
  sleepState.fading = false;
  sleepState.said = false;
  removeEventListener("devicemotion", onMotion);
  sleepState.motion = false;
  player.setFade(1);
}

function sleepTick() {
  stepSleep(performance.now() - sleepState.lastAt);
  drawSleep();
}

// Moves the timer on by deltaMs of wall time.
function stepSleep(deltaMs) {
  sleepState.lastAt = performance.now();
  if (sleepState.mode === "off" || !player.state().playing) return;
  const state = player.state();
  if (sleepState.mode === "chapter" && state.positionMs > sleepState.endMs + SKIPPED_AHEAD_MS) {
    sleepState.endMs = chapterEndNow(); // the person skipped ahead: the timer follows to that chapter's end
  }
  if (sleepState.mode === "minutes") sleepState.leftMs -= deltaMs;
  const left = sleepLeft();
  if (left <= 0) {
    finishSleep();
    return;
  }
  if (left <= FADE_MS) startFade(left);
  else if (sleepState.fading) stopFade();
}

function startFade(left) {
  if (!sleepState.fading) {
    sleepState.fading = true;
    if (motionAllowed && !sleepState.motion) {
      addEventListener("devicemotion", onMotion);
      sleepState.motion = true;
    }
  }
  player.setFade(left / FADE_MS);
}

// The timer ran out: pause. For a chapter, rest at its end so Resume starts the next one.
function finishSleep() {
  const end = sleepState.mode === "chapter" ? sleepState.endMs : -1;
  player.pause();
  if (end >= 0) player.seek(end);
  setSleep("off");
}

// "+10 min": minutes mode adds ten; a chapter timer turns into a ten-minute extension of what is left.
function sleepPlus() {
  if (sleepState.mode === "off") return;
  if (nativeSleep()) {
    nativeSleep().sleep("plus");
    return;
  }
  sleepState.leftMs = sleepLeft() + PLUS_MS;
  sleepState.mode = "minutes";
  sleepState.choice = "";
  stopFade();
  drawSleep();
}

// Two jolts within a second are a shake. Gravity is taken out when the device reports it separately.
function onMotion(event) {
  const a = event.acceleration;
  const g = event.accelerationIncludingGravity;
  let size = 0;
  if (a && a.x !== null && a.x !== undefined) size = Math.hypot(a.x, a.y || 0, a.z || 0);
  else if (g) size = Math.abs(Math.hypot(g.x || 0, g.y || 0, g.z || 0) - 9.81);
  const now = performance.now();
  if (size <= SHAKE_PEAK || now - sleepState.peakAt < SHAKE_GAP_MS) return;
  sleepState.peaks = now - sleepState.peakAt < SHAKE_WINDOW_MS ? sleepState.peaks + 1 : 1;
  sleepState.peakAt = now;
  if (sleepState.peaks >= 2) {
    sleepState.peaks = 0;
    sleepPlus();
  }
}

// What the sleep timer shows: the two bar items, the choices, the card.
function drawSleep() {
  if (!book) return;
  const active = sleepState.mode !== "off";
  const left = active ? sleepLeft() : 0;
  const text = clock(Math.ceil(left / 1000) * 1000);
  // the spoken label changes once a minute, not every second
  const label = active ? fill(msg("sleepOnLabel"), { t: clock(Math.ceil(left / 60000) * 60000) }) : msg("sleepOffLabel");
  for (const [item, value] of [[ui["open-sleep"], ui["full-sleep-left"]], [bar.sleep, bar.sleepLeft]]) {
    item.classList.toggle("is-active", active);
    if (item.getAttribute("aria-label") !== label) item.setAttribute("aria-label", label);
    value.hidden = !active;
    if (active && value.textContent !== text) value.textContent = text;
  }
  for (const choice of ui["sleep-sheet"].querySelectorAll("[data-ts-sleep-choice]")) {
    const name = choice.dataset.tsSleepChoice;
    if (name === "off") choice.hidden = !active;
    choice.setAttribute("aria-checked", String(active && name === sleepState.choice));
  }
  if (!ui["sleep-sheet"].hidden) {
    const state = player.state();
    const minutes = Math.ceil(Math.max(0, chapterEndNow() - state.positionMs) / state.speed / 60000);
    setText("sleep-meta", fill(msg("sleepIn"), { m: minutes }));
  }
  const showing = active && sleepState.fading && player.state().playing;
  for (const card of cards) {
    card.el.hidden = !(showing && card.el.dataset.tsSleepCard === (dialog.open ? "full" : "bar"));
  }
  if (!showing) return;
  const seconds = Math.ceil(left / 1000);
  const shake = motionAllowed && coarsePointer.matches;
  for (const card of cards) {
    card.count.textContent = fill(msg("sleeping"), { n: seconds });
    card.hint.textContent = msg(shake ? "sleepFadingShake" : "sleepFading");
    if (!sleepState.said) card.said.textContent = fill(msg("sleeping"), { n: seconds }); // said once, at the start
  }
  sleepState.said = true;
}

ui["sleep-sheet"].addEventListener("click", (event) => {
  const choice = event.target.closest("[data-ts-sleep-choice]");
  if (choice) {
    const name = choice.dataset.tsSleepChoice;
    if (name === "custom") {
      ui["sleep-custom"].hidden = false;
      ui["sleep-minutes"].focus();
      return;
    }
    chooseSleep(name, Number(name) || 0);
    closeSheet(true);
  } else if (event.target.closest("[data-ts-sleep-start]")) {
    startCustomSleep();
  }
});
ui["sleep-minutes"].addEventListener("keydown", (event) => {
  if (event.key !== "Enter") return;
  event.preventDefault();
  startCustomSleep();
});

function startCustomSleep() {
  const input = ui["sleep-minutes"];
  if (!input.value || !input.checkValidity()) {
    input.reportValidity();
    return;
  }
  chooseSleep("custom", Number(input.value));
  closeSheet(true);
}

// ---- Volume: the desktop bar's slider (per device); not offered where the browser ignores it ----

bar.volume.addEventListener("input", () => player.setVolume(Number(bar.volume.value) / 100));

// ---- Bookmarks: the button, the B key, the Bookmarks tab's form ----

let bookmarking = false; // one request at a time

// Always the live position at the moment of the press, never the position saved earlier.
async function addBookmark(options) {
  if (!book || bookmarking || LINK || book.kind !== "") return; // a book only: an episode or a station has no bookmarks
  bookmarking = true;
  const positionMs = player.state().positionMs;
  try {
    let res;
    try {
      res = await fetch("/books/" + encodeURIComponent(book.id) + "/bookmarks", {
        method: "POST",
        headers: { Accept: "application/json", "X-CSRF-Token": csrf(), "Content-Type": FORM_TYPE },
        body: "position_ms=" + positionMs,
      });
    } catch {
      return queueBookmark(positionMs); // the network is gone: keep it for offline.js to send
    }
    if (res.status === 201) showBookmarkToast(await res.json(), Boolean(options && options.quiet));
    else if (res.status === 409) player.toast("err", (await res.text()).trim()); // the limit, in plain words
    else player.toast("err", msg("bookmarkFailed"));
  } catch {
    player.toast("err", msg("bookmarkFailed"));
  } finally {
    bookmarking = false;
  }
}

// A bookmark made without a connection waits in ts.offline.marks (at most 200, the oldest dropped).
function queueBookmark(positionMs) {
  const clientId = crypto.randomUUID ? crypto.randomUUID() : Date.now().toString(36) + Math.random().toString(36).slice(2);
  try {
    const marks = JSON.parse(localStorage.getItem("ts.offline.marks")) || [];
    marks.push({ bookId: book.id, positionMs, clientId, at: Date.now() });
    localStorage.setItem("ts.offline.marks", JSON.stringify(marks.slice(-200)));
  } catch {
    return player.toast("err", msg("bookmarkFailed"));
  }
  const time = clock(positionMs - book.chapters[chapterAt(positionMs)].start);
  player.toast("hint", fill(msg("bookmarkQueued"), { time }));
}

// A bookmark toast (time and a note link) with a link that opens the note sheet like the
// Bookmarks tab does (the same htmx attributes), after closing a full screen player.
function showBookmarkToast(saved, quiet) {
  const toast = ui["bookmark-toast"].content.firstElementChild.cloneNode(true);
  const link = $("toast-link", toast);
  if (quiet) link.remove(); // Eyes-free has no note link: the text is all it shows
  $("toast-text", toast).textContent = fill(msg("bookmarked"), { time: saved.label });
  if (!quiet) {
    link.href = saved.noteHref;
    link.setAttribute("hx-get", saved.noteHref); // the other htmx attributes are in the template
  }
  const dismiss = () => {
    toast.classList.add("is-leaving");
    setTimeout(() => toast.remove(), 800); // shell.js usually removes it first
  };
  link.addEventListener("click", () => {
    if (dialog.open && !sidePanel()) closeNow();
    dismiss();
  });
  // Eyes-free sits on top of the player: its own region first (a selector list would find the player's)
  const host = document.querySelector("#eyes-free:modal [data-ts-full-toasts]") || document.querySelector("#player-full:modal [data-ts-full-toasts]");
  (host || document.getElementById("toasts")).append(toast);
  if (window.htmx && !quiet) window.htmx.process(link);
  // leaves after a few seconds, but not while the pointer or focus is on it
  const wait = () => (toast.matches(":hover, :focus-within") ? setTimeout(wait, 1000) : dismiss());
  setTimeout(wait, BOOKMARK_TOAST_MS);
}

// The B key, unless a field, an open sheet or a dialog (keyboard list, note sheet) wants the key.
document.addEventListener("keydown", (event) => {
  if (event.ctrlKey || event.metaKey || event.altKey || event.repeat || event.key.toLowerCase() !== "b" || !book) return;
  const target = event.target;
  if (target.closest && target.closest("input, textarea, select, [contenteditable]")) return;
  if (openName || document.querySelector("dialog[open]:not(.ts-player), #sheet .ts-sheet")) return;
  event.preventDefault();
  addBookmark();
});

// The Bookmarks tab of the loaded book: its add button shows the live place
// and saves it (the server's own number is the last saved place, which can be behind).
function syncAddForms(ms) {
  for (const form of document.querySelectorAll("form[data-ts-bookmark-add]")) {
    if (form.dataset.tsBookmarkAdd !== book.id) continue;
    form.hidden = false;
    const label = $("bookmark-add-label", form);
    const text = fill(msg("bookmarkMomentAt"), { time: clock(ms - book.chapters[chapterAt(ms)].start) });
    if (label.textContent !== text) label.textContent = text;
  }
}

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (book && form.matches && form.matches("form[data-ts-bookmark-add]") && form.dataset.tsBookmarkAdd === book.id) {
    form.elements.position_ms.value = player.state().positionMs;
  }
}, true); // before htmx serialises the form
document.addEventListener("htmx:after:settle", () => update("settle"));

// ---- Long press on the bar: the speed sheet ----

let pressTimer = 0;
let pressAt = -Infinity; // when a long press fired
let pressFrom = null;

function endPress() {
  clearTimeout(pressTimer);
  pressFrom = null;
}

miniOpen.addEventListener("pointerdown", (event) => {
  if (event.button !== 0 || !book) return;
  endPress();
  pressFrom = { x: event.clientX, y: event.clientY };
  pressTimer = setTimeout(() => {
    pressAt = performance.now();
    openSheet("speed", miniOpen);
  }, LONG_PRESS_MS);
});
miniOpen.addEventListener("pointermove", (event) => {
  if (pressFrom && Math.hypot(event.clientX - pressFrom.x, event.clientY - pressFrom.y) > SWIPE_MIN_PX) endPress();
});
for (const type of ["pointerup", "pointercancel", "pointerleave"]) miniOpen.addEventListener(type, endPress);
// The click and the context menu that follow a long press must not also navigate or open a menu.
for (const type of ["click", "contextmenu"]) {
  miniOpen.addEventListener(type, (event) => {
    if (performance.now() - pressAt > 1000) return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);
}

// ---- Clicks, menu ----

const actions = {
  "close-player": closePlayer,
  "full-prev": () => player.chapter(-1),
  "full-next": () => player.chapter(1),
  "full-back": () => player.skip(-1),
  "full-forward": () => player.skip(1),
  "full-toggle": () => player.toggle(),
  "open-chapters": () => openSheet("chapters", ui["open-chapters"]),
  "open-speed": () => openSheet("speed", ui["open-speed"]),
  "open-sleep": () => openSheet("sleep", ui["open-sleep"]),
  "open-notes": () => openSheet("notes", ui["open-notes"]),
  "sheet-close": () => closeSheet(true),
  "bookmark": addBookmark,
  "full-fav": toggleFavourite,
  "sleep-plus": sleepPlus,
};

// The desktop bar sits outside the dialog: its tools open the side panel's sheets (speed, sleep,
// chapters), add a bookmark, toggle the panel and open the volume slider.
slot.addEventListener("click", (event) => {
  const hit = (name) => event.target.closest("[data-ts-bar-" + name + "]");
  if (hit("speed")) toggleSheet("speed", bar.speed);
  else if (hit("sleep")) toggleSheet("sleep", bar.sleep);
  else if (hit("chapters")) toggleSheet("chapters", bar.chapters);
  else if (hit("bookmark")) addBookmark();
  else if (hit("expand")) (dialog.open ? closePlayer : openPlayer)();
});

// The bar's sleep card sits beside the slot (so the hero hiding the bar does not hide it): it answers itself.
cards.find((card) => card.el.dataset.tsSleepCard === "bar").el.addEventListener("click", (event) => {
  if (event.target.closest("[data-ts-sleep-plus]")) sleepPlus();
});

dialog.addEventListener("click", (event) => {
  for (const name in actions) {
    if (event.target.closest("[data-ts-" + name + "]")) return actions[name]();
  }
});

// Following any link in the ⋯ menu (Go to book, Up next) closes the menu.
ui["full-menu"].addEventListener("click", (event) => {
  if (event.target.closest("a")) ui["full-menu"].open = false;
  const item = event.target.closest("[data-ts-eyes-free-open]");
  if (!item) return;
  ui["full-menu"].open = false;
  // Eyes-free is a module of its own, fetched the first time it is asked for (the worker precaches it)
  import(item.dataset.tsSrc).then((module) => module.openEyesFree(ui["full-menu"].querySelector("summary"))).catch(() => {});
});

// The book icon of the bar and of the full screen player: the read-from sheet (the server goes straight to the
// reader when only one place is on offer). Playback is never touched; going to read is a boosted swap of #main.
function openReadChoice(event) {
  if (!book.readChoose) return;
  if (!navigator.onLine) {
    location.assign(book.readHref); // the offline reader shell, as before
    return;
  }
  if (dialog.open && !sidePanel()) closeNow(); // the sheet belongs to the page behind the full screen player
  const ms = Math.max(0, Math.round(player.state().positionMs));
  window.htmx.ajax("GET", book.readChoose + "?ms=" + ms, { target: "#sheet", source: event.currentTarget });
}
for (const button of [bar.read, ui["full-read"]]) button.addEventListener("click", openReadChoice);

ui["full-book"].addEventListener("click", () => {
  if (!sidePanel()) closeNow(); // the page behind the full screen player is the destination
});

// A plain form to the finished endpoint; htmx sends it without leaving the page.
ui["full-finish"].addEventListener("submit", () => {
  const fields = ui["full-finish"].elements;
  fields.next.value = location.pathname + location.search;
  fields._csrf.value = csrf();
  ui["full-menu"].open = false;
});

// ---- Undo after a big move: a short offer to go back ----

const FAR_JUMP_MS = 60000; // a seek over a minute away offers the undo, as does any chapter jump or scrub
const SCRUB_MIN_MS = 2000; // a scrub that moved less than this is a tap, not a jump
let lastMs = -1; // the place before the latest move: the last tick or state seen
let scrubFrom = -1; // where a scrub began, set while its seek runs
let chapterFrom = -1; // a small seek that a chapter jump may announce a moment later
let undoing = false;
let undoToast = null;

function offerUndo(fromMs, bookId) {
  dismissUndo();
  undoToast = player.toast("hint", msg("jumpedFar"), [{
    label: msg("jumpedBack"),
    run: () => {
      dismissUndo();
      if (player.state().bookId !== bookId) return;
      undoing = true; // going back is not a jump to undo again
      player.seek(fromMs);
      undoing = false;
    },
  }]);
}

function dismissUndo() {
  if (undoToast) undoToast.remove();
  undoToast = null;
}

// Called for every player event with its kind and the position it reports. A chapter jump seeks first
// (emit "seek") and then says "chapter" in the same call, so a small jump is held back until it does.
function trackJump(kind, ms, bookId) {
  const from = scrubFrom >= 0 ? scrubFrom : lastMs;
  if (kind === "seek" && !undoing && from >= 0) {
    const far = Math.abs(ms - from) > FAR_JUMP_MS || (scrubFrom >= 0 && Math.abs(ms - from) > SCRUB_MIN_MS);
    chapterFrom = from;
    if (far) {
      chapterFrom = -1;
      offerUndo(from, bookId);
    }
  } else if (kind === "chapter" && chapterFrom >= 0 && !undoing) {
    offerUndo(chapterFrom, bookId);
    chapterFrom = -1;
  } else if (kind === "load") {
    dismissUndo();
  }
  if (kind !== "seek" && kind !== "chapter") chapterFrom = -1;
  lastMs = kind === "load" ? -1 : ms;
}

// ---- Start-up ----

function start() {
  player = window.tsPlayer;
  if (nativeSleep()) nativeSleep().addEventListener("nativesleep", onNativeSleep);
  document.addEventListener("ts:player:tick", (event) => {
    lastMs = event.detail.positionMs;
    update("tick");
  });
  document.addEventListener("ts:player:state", (event) => {
    trackJump(event.detail.kind, event.detail.positionMs, event.detail.bookId);
    const refresh = ["load", "chapters", "duration", "nowplaying"].includes(event.detail.kind); // an episode's chapters or length arrived
    if (refresh || !book || event.detail.bookId !== book.id) {
      if (book && event.detail.bookId !== book.id && !nativeSleep()) setSleep("off"); // another book: the timer was for this one
      book = player.info();
      if (book) drawBook();
    }
    if (event.detail.kind === "play") maybeStartNightSleep(); // after the book is current
    update(event.detail.kind);
  });
  new MutationObserver(applyAccent).observe(root, { attributes: true, attributeFilter: ["data-theme"] });
  for (const query of ["(prefers-color-scheme: light)", "(prefers-contrast: more)"]) matchMedia(query).addEventListener("change", applyAccent);
  bar.volumeWrap.hidden = !player.canSetVolume;
  book = player.info();
  if (book) {
    drawBook();
    update("load");
    // This module loads after the bar, so the first play may already have happened: it counts too.
    if (player.state().playing) maybeStartNightSleep();
  }
}

// The surface Eyes-free (js/eyes-free.js) uses; debug is for tests only (like tsPlayer.debug).
window.tsPlayerFull = {
  sleep: (choice, minutes) => chooseSleep(choice, minutes), // sync: iOS asks for motion access inside the tap
  sleepInfo: () => ({ mode: sleepState.mode, choice: sleepState.choice, leftMs: sleepState.mode === "off" ? 0 : sleepLeft() }),
  bookmark: (options) => addBookmark(options),
  debug: {
    advanceSleep: (ms) => {
      stepSleep(ms);
      drawSleep();
    },
    sleep: () => ({ ...sleepState }),
    inNightWindow,
    leftInBook: leftInBookText,
  },
};

// player.js is an async module too: it may not have run yet.
if (window.tsPlayer) start();
else document.addEventListener("ts:player:ready", start, { once: true });
