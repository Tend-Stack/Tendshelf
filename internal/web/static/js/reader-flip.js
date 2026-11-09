// The page turn of the book reader (decision 0036). reader-pages.js loads this only when the person's Page turn is Flip.
//
// The real page is always at its destination the moment a turn starts (reader-pages.js moves it first). What flies is a
// copy: a stage above the page holding small slices of the text, one per page that is drawn. A slice is built from the
// part's own text by the same column engine, so its text lands where the real text lands. The stage is aria-hidden and
// inert, has no ids and no hx-* attributes, and is gone when the turn ends.
//
// Surfaces. A leaf is one page that turns about its left edge, seen from the front and then from the back.
//   one page, forward:   the leaf shows the old page and turns 0 to -90 degrees, away over the new page
//   one page, back:      the old page stays; the leaf shows the new page and turns -90 to 0
//   a spread, forward:   the old left page stays; the leaf on the right shows the old right page, and the new left page on its back; 0 to -180
//   a spread, back:      the old right page stays; the leaf shows the new right page, and the old left page on its back; -180 to 0
// Under the leaf the real page (already the new one) shows through the stage wherever the stage is empty.

const SPREAD_MS = 420;
const PAGE_MS = 340;
const EASE = "cubic-bezier(.4, 0, .2, 1)"; // a slow lift, a quick middle, a soft landing
const STEPS = 12; // samples of a shadow's motion in a flight
const MIN_MS = 120; // the least a drag takes to finish
const CLOSE_MS = 120; // a turn this soon after another has no motion
const START_PX = 10; // a drag starts after this much sideways travel
const SWIPE_PX = 48; // a swipe without a leaf turns the page when it goes this far
const REST_MS = 80; // a finger still this long before it lifts is not flicking
const FLICK = 0.35; // px per ms: a quick flick decides a drag
const BUILD_SLOW_MS = 50;
const GAP_SLOW_MS = 34;
const TRIALS = 3;
const SLOW_KEY = "ts.flip.slow";
const DEPTH = 5; // the eye is this many page widths from the page: nearer is more dramatic, farther is flatter

const px = (n) => n + "px";
const clamp = (n, low, high) => Math.min(high, Math.max(low, n));
const make = (tag, name) => {
  const el = document.createElement(tag);
  el.className = name;
  return el;
};

// ---- One page's face ----

// Where a page's paper is on the screen: the frame, or a half of it on a spread.
export function paper(geo, slot) {
  const frame = geo.frame;
  if (geo.cols !== 2) return { left: frame.left, top: frame.top, width: frame.width, height: frame.height };
  const half = frame.width / 2;
  return { left: frame.left + slot * half, top: frame.top, width: half, height: frame.height };
}

// The section's children in reading order, with the text box's children in its place.
function itemsOf(section) {
  const out = [];
  for (const el of section.children) {
    if (el.classList.contains("ts-reader__text")) for (const child of el.children) out.push({ el: child, box: el });
    else out.push({ el, box: null });
  }
  return out;
}

// Take out everything that must not be twice in the document, and everything that could act.
function strip(el) {
  for (const node of [el, ...el.querySelectorAll("*")]) {
    node.removeAttribute("id");
    node.removeAttribute("style");
    for (const name of node.getAttributeNames()) if (name.startsWith("hx-")) node.removeAttribute(name);
    if (node.localName === "img") {
      node.loading = "eager";
      node.decoding = "sync";
    }
  }
  return el;
}

// A hidden stand-in of an item that is before the page: rules like `p + p` and `:first-of-type` still see it.
function standIn(el) {
  const out = document.createElement(el.localName);
  out.className = el.className;
  out.classList.add("ts-flip__off");
  return out;
}

// Which column of the part a box fragment is in.
const columnOf = (rect, origin, step) => Math.floor((rect.left - origin + 1) / step);

// Which items of the section a column shows. This is all the reading of the real page's layout a face needs, so a flight
// reads for every face first and only then writes: a write between two reads would lay the page out again each time.
function plan(section, column, geo, list) {
  const step = geo.column + geo.gap;
  const origin = geo.left - section.scrollLeft;
  const at = { section, list, first: -1, last: list.length, startColumn: 0, offset: 0 };
  for (let i = 0; i < list.length; i++) {
    const rects = list[i].el.getClientRects();
    if (!rects.length) continue; // hidden: it has no place
    const start = columnOf(rects[0], origin, step);
    if (at.first < 0) {
      if (start > column) break; // nothing is in this column: a blank page
      if (columnOf(rects[rects.length - 1], origin, step) < column) continue;
      at.first = i;
      at.startColumn = start;
      at.offset = rects[0].top - geo.top;
    } else if (start > column) {
      at.last = i;
      break;
    }
  }
  return at;
}

// The slice as its own one-column section: the items before the first are hidden stand-ins, then come copies.
function slice(at) {
  const { section, list, first, last, offset } = at;
  const box = strip(section.cloneNode(false));
  box.classList.remove("ts-reader__short"); // the spacer below already holds the page's top
  if (first < 0) return box;
  if (offset > 0) {
    const spacer = make("div", "ts-flip__spacer");
    spacer.style.setProperty("height", px(offset));
    box.append(spacer);
  }
  let holder = null;
  let into = box;
  for (let i = 0; i < last; i++) { // `last` is the first item that starts past the column: not on this page
    const { el, box: parent } = list[i];
    if (parent) {
      if (parent !== holder) {
        holder = parent;
        into = strip(parent.cloneNode(false));
        box.append(into);
      }
    } else {
      into = box;
      holder = null;
    }
    if (i < first) {
      into.append(standIn(el));
      continue;
    }
    const copy = strip(el.cloneNode(true));
    if (i === first) copy.style.setProperty("margin-top", "0");
    into.append(copy);
  }
  return box;
}

// Read what one face needs. `slot` is the page's place in the turn on show, `labels` the two words of its foot.
function prepare(section, list, column, geo, slot, labels = ["", ""]) {
  const live = section.closest(".ts-reader");
  const classes = [...live.classList].filter((name) => name !== "ts-reader--spread" && name !== "ts-reader--scroll").join(" ");
  return { at: plan(section, column, geo, list), sheet: paper(geo, slot), geo, slot, column, labels, classes, lang: live.lang };
}

// Draw a face: paper with the text where the real page's text sits. It goes into `parent` first, and only then is its text
// scrolled into place, since a box that is not in the document has no scroll; `settle` does that.
function draw(job, parent) {
  const { geo, sheet, slot } = job;
  const step = geo.column + geo.gap;
  const el = make("div", "ts-flip__face");
  const wrap = make("div", job.classes);
  if (job.lang) wrap.lang = job.lang;
  wrap.style.setProperty("--ts-cols", "1");
  wrap.style.setProperty("--ts-col", px(geo.column));
  wrap.style.setProperty("--ts-gap", px(geo.gap));
  wrap.style.setProperty("--ts-page-h", px(geo.pageH));
  const page = make("div", "ts-reader__col");
  page.style.setProperty("--flip-x", px(geo.left + slot * step - sheet.left));
  page.style.setProperty("--flip-y", px(geo.top - sheet.top));
  const box = slice(job.at);
  const foot = make("div", "ts-pagefoot");
  for (const word of job.labels) foot.append(Object.assign(document.createElement("span"), { textContent: word }));
  page.append(box, foot);
  wrap.append(page);
  el.append(wrap);
  parent.prepend(el); // under the shading
  return { el, settle: () => (box.scrollLeft = job.at.first < 0 ? 0 : (job.column - job.at.startColumn) * step) };
}

// One page drawn as a face, settled (the form the tests use).
export function face(section, column, geo, slot, parent, labels) {
  const drawn = draw(prepare(section, itemsOf(section), column, geo, slot, labels), parent);
  drawn.settle();
  return drawn.el;
}

// ---- What each part of a flight looks like at a given moment ----

// s runs 0 to 1: 0 is the leaf flat on its page, 1 is turned all the way (90 degrees on one page, 180 on a spread).
// Where the free edge of the leaf is seen takes the perspective into account, so the shadow can reach past it.
function look(s, max) {
  const angle = s * max;
  const rad = (angle * Math.PI) / 180;
  const lift = Math.sin(rad);
  const edge = Math.abs((Math.cos(rad) * DEPTH) / (DEPTH - lift));
  // The shadow the leaf throws on the page beneath: it grows as the leaf rises, then narrows as it stands up.
  const cast = (on) => ({ opacity: on ? Math.pow(lift, 0.5) : 0, transform: `scaleX(${clamp(edge + 0.2 * lift, 0.02, 1)})` });
  return {
    leaf: `rotateY(${-angle}deg)`,
    front: clamp(angle / 90, 0, 1), // the face turning away darkens
    back: clamp((180 - angle) / 90, 0, 1), // the face coming down lightens
    castRight: cast(angle <= 90),
    castLeft: cast(angle > 90),
  };
}

// ---- The flipper ----

export function flipper(root, book) {
  const shell = root.closest(".ts-readerpage");
  const done = new AbortController();
  // The stage is made and removed with every flight, inside a host that stays: a node added straight to the reader page makes
  // the browser re-test the page's :has() rules, which costs more than the whole build.
  const host = make("div", "ts-flip-host");
  shell.append(host);
  let stage = null;
  let flight = null;
  let lastBegin = -1e9;
  const trials = []; // the first flights: how long the stage took to build, and the rAF gaps
  const still = { land() {}, follow() {}, release() {}, quiet: true };

  const slow = () => {
    try {
      return localStorage.getItem(SLOW_KEY) === "1";
    } catch {
      return false;
    }
  };

  // Finish whatever is in the air at once. A drag that was not let go is put back.
  function drop() {
    const was = flight;
    flight = null;
    stage?.remove();
    stage = null;
    was?.end();
  }

  function begin(dir) {
    drop();
    const now = performance.now();
    const quick = now - lastBegin < CLOSE_MS;
    lastBegin = now;
    if (quick || slow()) return still;
    const started = performance.now();
    const geo = book.geometry();
    const spread = geo.cols === 2;
    const first = book.column();
    const max = spread ? 180 : 90;
    const full = spread ? SPREAD_MS : PAGE_MS;
    // The stage fills the reader page, which is not always at the window's corner (the desktop shell's bar is above it).
    const corner = shell.getBoundingClientRect();
    const at = (sheet) => ({ left: sheet.left - corner.left, top: sheet.top - corner.top, width: sheet.width, height: sheet.height });
    const leafSheet = at(paper(geo, spread ? 1 : 0));
    const hinge = leafSheet.left;

    // Read the real page for every face first (a face of the old page or pages); writing comes after.
    const section = book.section();
    const right = spread ? 1 : 0;
    const list = itemsOf(section);
    const job = (column, slot) => prepare(section, list, column, geo, slot, book.labels(column));
    const jobs =
      dir > 0
        ? [spread && [job(first, 0), "page"], [job(first + right, right), "front"]]
        : [[job(first + right, right), "page"], spread && [job(first, 0), "back"]];
    // Inside the part the new page is known now, so it is read with the old ones. Across a part edge the new part is not here
    // yet, and its face is built when it has arrived. On a spread the new face is the side that turns into view only past the
    // middle of the flight, so it is drawn just after the leaf has started and not before: that keeps the start quick.
    const to = book.dest(dir);
    const arriving = dir > 0 ? spread : true; // a turn forward on one page shows no new face; every other turn does
    let late = null; // the new face of a spread, drawn once the leaf is moving
    if (to >= 0 && arriving) {
      const made = dir > 0 ? [job(to, 0), "back"] : [job(to + right, right), "front"];
      if (spread) late = made;
      else jobs.push(made);
    }
    const later = to < 0 && arriving;

    stage = make("div", "ts-flip ts-flip--wait");
    const mine = stage;
    stage.setAttribute("aria-hidden", "true");
    stage.inert = true;
    stage.style.setProperty("--flip-depth", px(DEPTH * leafSheet.width));
    stage.style.setProperty("perspective-origin", `${hinge}px ${leafSheet.top + leafSheet.height / 2}px`);
    const place = (el, sheet) => {
      el.style.setProperty("left", px(sheet.left));
      el.style.setProperty("top", px(sheet.top));
      el.style.setProperty("width", px(sheet.width));
      el.style.setProperty("height", px(sheet.height));
    };
    // The still page (if any), the shadows, then the leaf with its sides.
    const still0 = dir > 0 ? (spread ? 0 : -1) : spread ? 1 : 0;
    const parts = {};
    if (still0 >= 0) {
      parts.page = make("div", "ts-flip__page");
      place(parts.page, at(paper(geo, still0)));
      stage.append(parts.page);
    }
    parts.castLeft = spread ? make("div", "ts-flip__cast ts-flip__cast--left") : null;
    parts.castRight = make("div", "ts-flip__cast");
    if (parts.castLeft) {
      place(parts.castLeft, at(paper(geo, 0)));
      stage.append(parts.castLeft);
    }
    place(parts.castRight, leafSheet);
    stage.append(parts.castRight);
    parts.leaf = make("div", "ts-flip__leaf");
    place(parts.leaf, leafSheet);
    parts.front = make("div", "ts-flip__side");
    parts.leaf.append(parts.front);
    if (spread) {
      parts.back = make("div", "ts-flip__side ts-flip__side--back");
      parts.leaf.append(parts.back);
    }
    parts.frontShade = parts.front.appendChild(make("div", "ts-flip__shade ts-flip__shade--front"));
    if (parts.back) parts.backShade = parts.back.appendChild(make("div", "ts-flip__shade ts-flip__shade--back"));
    stage.append(parts.leaf);
    host.append(stage);

    // The faces of the page or pages on show now. Forward: the old right (or only) page is the leaf's front. Back: the old
    // right page stays and the old left page is the leaf's back; one page back keeps the old page still.
    const drawn = jobs.filter(Boolean).map(([made, name]) => draw(made, parts[name]));
    for (const one of drawn) one.settle();
    let built = performance.now() - started;
    let current = dir > 0 ? 0 : 1; // how far the leaf is turned now, 0 to 1

    const pose = (s) => {
      current = s;
      const at = look(s, max);
      parts.leaf.style.setProperty("transform", at.leaf);
      parts.frontShade.style.setProperty("opacity", at.front);
      parts.backShade?.style.setProperty("opacity", at.back);
      parts.castRight.style.setProperty("opacity", at.castRight.opacity);
      parts.castRight.style.setProperty("transform", at.castRight.transform);
      parts.castLeft?.style.setProperty("opacity", at.castLeft.opacity);
      parts.castLeft?.style.setProperty("transform", at.castLeft.transform);
    };

    let anims = [];
    let gaps = [];
    let frame = 0;
    const settle = () => {
      cancelAnimationFrame(frame);
      for (const anim of anims) anim.cancel();
      anims = [];
    };

    // Play the flight from one state to another, with the shadows sampled along the way.
    function play(from, to, ms, easing, then) {
      settle();
      const at = [];
      for (let i = 0; i <= STEPS; i++) at.push(look(from + ((to - from) * i) / STEPS, max));
      const options = { duration: ms, easing, fill: "both" };
      const run = (el, frames) => el && anims.push(el.animate(frames, options));
      run(parts.leaf, [{ transform: at[0].leaf }, { transform: at[STEPS].leaf }]);
      run(parts.frontShade, at.map((t) => ({ opacity: t.front })));
      run(parts.backShade, at.map((t) => ({ opacity: t.back })));
      run(parts.castRight, at.map((t) => ({ opacity: t.castRight.opacity, transform: t.castRight.transform })));
      run(parts.castLeft, at.map((t) => ({ opacity: t.castLeft.opacity, transform: t.castLeft.transform })));
      let before = performance.now();
      const tick = () => {
        const now = performance.now();
        gaps.push(now - before);
        before = now;
        frame = requestAnimationFrame(tick);
      };
      frame = requestAnimationFrame(tick);
      anims[0].onfinish = () => {
        cancelAnimationFrame(frame);
        if (mine === stage && flight === me) {
          if (!me.shifted) judge(built, gaps);
          drop();
        }
        then?.();
      };
    }

    // The first flights decide whether this device can flip: a slow build or ragged frames make the turn a slide from then on.
    function judge(build, spaced) {
      if (trials.length >= TRIALS || !spaced.length) return;
      const sorted = [...spaced].sort((a, b) => a - b);
      trials.push({ build, p90: sorted[Math.floor(0.9 * (sorted.length - 1))] });
      const slowBuild = build > BUILD_SLOW_MS;
      const slowFrames = trials.filter((t) => t.p90 > GAP_SLOW_MS).length >= 2;
      if (!slowBuild && !slowFrames) return;
      try {
        localStorage.setItem(SLOW_KEY, "1");
      } catch {
        /* no storage: judged again next time */
      }
    }

    // The face that arrives with the move across a part edge.
    function arrive() {
      if (!later) return measured();
      const began = performance.now();
      const now = book.section();
      const geoNow = book.geometry();
      const at = book.column();
      const made =
        dir > 0
          ? spread && [prepare(now, itemsOf(now), at, geoNow, 0, book.labels(at)), "back"]
          : [prepare(now, itemsOf(now), at + right, geoNow, right, book.labels(at + right)), "front"];
      if (made) {
        const one = draw(made[0], parts[made[1]]);
        one.settle();
      }
      built += performance.now() - began;
      measured();
    }

    function measured() {
      const end = performance.now();
      performance.measure("ts-flip-build", { start: end - built, end });
      if (!late) return;
      // Drawn in the frame after the leaf has left: its side is not in view before the middle of the flight.
      const [made, name] = late;
      late = null;
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          if (mine !== stage) return;
          const began = performance.now();
          draw(made, parts[name]).settle();
          performance.measure("ts-flip-build-late", { start: began, end: performance.now() });
        }),
      );
    }

    const me = {
      dir,
      max,
      // The page has moved: add the faces that arrive and let the leaf go.
      land() {
        if (flight || mine !== stage) return;
        flight = me;
        arrive();
        pose(dir > 0 ? 0 : 1);
        mine.classList.remove("ts-flip--wait");
        play(dir > 0 ? 0 : 1, dir > 0 ? 1 : 0, full, EASE);
      },
      // The drag: the page has moved, show the leaf at the finger.
      grab() {
        if (flight || mine !== stage) return;
        flight = me;
        arrive();
        pose(dir > 0 ? 0 : 1);
        mine.classList.remove("ts-flip--wait");
      },
      follow(angle) {
        if (flight === me) pose(clamp(angle / max, 0, 1));
      },
      // Let go: finish the turn (commit) or put the leaf back, in the time the rest of the way takes.
      release(commit, velocity = 0) {
        if (flight !== me) return;
        const target = commit ? (dir > 0 ? 1 : 0) : dir > 0 ? 0 : 1;
        const ms = Math.max(MIN_MS, Math.abs(target - current) * full * (Math.abs(velocity) > FLICK ? 0.7 : 1));
        if (commit) {
          me.shifted?.commit();
          me.rollback = null;
        }
        play(current, target, ms, "ease-out");
      },
      shifted: null,
      rollback: null,
      end() {
        settle();
        me.rollback?.();
        me.rollback = null;
      },
      stage: mine,
    };
    return me;
  }

  // ---- Dragging the leaf ----

  let pointer = null;
  let swallow = null;
  const interactive = "a, button, input, textarea, select, .ts-hl";

  shell.addEventListener(
    "pointerdown",
    (event) => {
      if (pointer || !event.isPrimary || (event.pointerType === "mouse" && event.button !== 0)) return;
      if (book.mover() !== "flip" || slow()) return;
      const edge = event.target.closest(".ts-edge");
      if (!edge && (event.pointerType === "mouse" || !root.contains(event.target) || event.target.closest(interactive))) return;
      pointer = { id: event.pointerId, x: event.clientX, y: event.clientY, edge, selecting: !getSelection().isCollapsed, started: false, dir: 0, last: [], target: event.target };
    },
    { signal: done.signal },
  );

  shell.addEventListener(
    "pointermove",
    (event) => {
      if (!pointer || event.pointerId !== pointer.id) return;
      const dx = event.clientX - pointer.x;
      const dy = event.clientY - pointer.y;
      pointer.last.push([event.timeStamp, event.clientX]);
      if (pointer.last.length > 6) pointer.last.shift();
      if (!pointer.started) {
        if (Math.abs(dy) > START_PX && Math.abs(dy) >= Math.abs(dx)) pointer = null; // a scroll or a selection gesture
        else if (Math.abs(dx) >= START_PX && Math.abs(dx) > 1.5 * Math.abs(dy) && !pointer.selecting) start(event, dx < 0 ? 1 : -1);
        return;
      }
      if (pointer.flight) pointer.flight.follow(angleAt(event.clientX));
    },
    { signal: done.signal },
  );

  const finish = (event, cancelled) => {
    if (!pointer || event.pointerId !== pointer.id) return;
    const was = pointer;
    pointer = null;
    if (!was.started) return;
    swallowClick();
    const dx = event.clientX - was.x;
    const velocity = event.timeStamp - was.last[was.last.length - 1]?.[0] > REST_MS ? 0 : speed(was.last); // a finger that rested has no flick
    if (was.flight) {
      if (cancelled) {
        was.flight.release(false);
        return;
      }
      const s = was.progress;
      const forward = was.dir > 0;
      const past = forward ? s >= 0.5 : s <= 0.5;
      const flick = Math.abs(velocity) > FLICK;
      const withIt = forward ? velocity < 0 : velocity > 0;
      const commit = flick ? withIt : past;
      was.flight.release(commit, velocity);
      if (commit) book.moved();
    } else if (!cancelled && Math.abs(dx) >= SWIPE_PX) {
      book.turn(was.dir);
    }
  };
  shell.addEventListener("pointerup", (event) => finish(event, false), { signal: done.signal });
  shell.addEventListener("pointercancel", (event) => finish(event, true), { signal: done.signal });

  const speed = (list) => {
    if (list.length < 2) return 0;
    const [t0, x0] = list[0];
    const [t1, x1] = list[list.length - 1];
    return t1 > t0 ? (x1 - x0) / (t1 - t0) : 0;
  };

  // The angle the finger has turned the leaf to: the leaf's free edge follows the finger.
  let track = null;
  function angleAt(x) {
    const tip = x + track.offset;
    const low = track.max === 180 ? -1 : 0;
    const angle = (Math.acos(clamp((tip - track.hinge) / track.width, low, 1)) * 180) / Math.PI;
    pointer.progress = clamp(angle / track.max, 0, 1);
    return angle;
  }

  function start(event, dir) {
    if (dir > 0 ? book.atEnd() : book.atStart()) {
      pointer = null;
      return;
    }
    pointer.started = true;
    pointer.dir = dir;
    book.claimSwipe();
    try {
      (pointer.edge || root).setPointerCapture(event.pointerId);
    } catch {
      /* a pointer that is gone */
    }
    const flightNow = begin(dir);
    const shifted = flightNow.quiet ? null : book.shift(dir);
    if (!shifted) {
      if (!flightNow.quiet) drop();
      return; // no leaf: a swipe past 48 px turns on release
    }
    const geo = book.geometry();
    const spread = geo.cols === 2;
    const sheet = paper(geo, spread ? 1 : 0);
    flightNow.shifted = shifted;
    flightNow.rollback = () => shifted.undo();
    track = { max: flightNow.max, hinge: sheet.left, width: sheet.width, offset: 0 };
    // The tip of the leaf now: the right edge going forward, the left edge (or the spine) coming back.
    const tip = dir > 0 ? sheet.left + sheet.width : spread ? sheet.left - sheet.width : sheet.left;
    track.offset = tip - event.clientX;
    pointer.flight = flightNow;
    pointer.progress = dir > 0 ? 0 : 1;
    flightNow.grab();
    flightNow.follow(angleAt(event.clientX));
  }

  // The click that ends a drag must not also be a tap.
  function swallowClick() {
    swallow?.abort();
    swallow = new AbortController();
    const stop = (event) => {
      event.stopPropagation();
      event.preventDefault();
    };
    window.addEventListener("click", stop, { capture: true, signal: swallow.signal });
    setTimeout(() => swallow?.abort(), 120);
  }

  document.addEventListener("visibilitychange", () => document.hidden && drop(), { signal: done.signal });

  return {
    begin,
    drop,
    stop() {
      drop();
      host.remove();
      swallow?.abort();
      done.abort();
    },
  };
}
