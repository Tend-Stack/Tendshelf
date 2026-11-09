// Pages for the book reader (M5 RD2). reader.js loads this for the pages flow, which is the default at every width.
// One chapter part is laid out in CSS columns; a turn moves the part's scrollLeft by one page.
// A page number here is for showing only: it fits this device and look and is kept on this device. The place
// that is saved is a sentence and an offset (reader.js, decision 0036).

const SLIDE_MS = 180;
const SWIPE_PX = 48;
const RETRY_MS = 1500; // the quiet second try of a part that did not load
const WIDE_PX = 1024; // from here a page is a bounded sheet on a desk, and two can face each other
const TALL = 1.6; // on the desk a page is at most this many times as tall as it is wide
const LOOK_CLASS = /^ts-reader--(sans|dyslexic|size-|leading-|margin-|justify|nohyph)/; // what changes how many pages a part takes

const px = (value) => parseFloat(value) || 0;
const fill = (text, ...numbers) => numbers.reduce((out, n) => out.replace("%d", n), text);

export function pages(root, kit) {
  const col = root.querySelector(".ts-reader__col");
  const shell = root.closest(".ts-readerpage");
  const section = () => root.querySelector("section[data-part]");
  const copy = JSON.parse(root.dataset.copy || "{}");
  // A hidden box in the text's own font. Its CSS widths (30em and 24em) tell us what a column may measure.
  const probe = document.createElement("div");
  probe.className = "ts-reader__text ts-reader__probe";
  probe.setAttribute("aria-hidden", "true");
  col.append(probe);

  let step = 1; // scrollLeft distance of one turn
  let colStep = 1; // scrollLeft distance of one column
  let cols = 1; // columns in a turn: 1, or 2 for a spread
  let phys = 1; // columns in the part
  let count = 1; // turns in the part
  let page = 0; // the turn now shown
  let frame = null; // the page's box, for the tap zones
  let guess = 1; // characters a page holds, from the look alone
  const parts = new Map(); // fetched neighbour parts by address
  const ready = new Map(); // the neighbour parts that have arrived, by address (a drag over a part edge swaps one in at once)
  let colW = 0; // the laid-out sizes, for the flip's faces
  let gapW = 0;
  let pageHt = 0;
  let marginW = 0;
  let layMs = 0; // how long the last lay() took
  let flip = null; // reader-flip.js, loaded when the turn is Flip

  // ---- Page numbers: display only, measured as parts are laid out and kept on this device per look (decision 0036) ----

  const storeKey = "ts.offline.pagecount." + root.dataset.edition;
  let store = { look: "", parts: {} }; // parts: { "<chapter>.<part>": { b: characters before, c: characters in it, n: pages } }
  const stored = () => {
    try {
      return JSON.parse(localStorage.getItem(storeKey));
    } catch {
      return null;
    }
  };

  // The part just laid out: where it starts, how long it is and how many columns it took.
  function record(look) {
    if (store.look !== look) {
      const kept = stored();
      store = kept?.look === look ? kept : { look, parts: {} };
    }
    const box = section();
    store.parts[box.dataset.ch + "." + box.dataset.part] = { b: Number(box.dataset.before), c: Number(box.dataset.partChars), n: phys };
    try {
      localStorage.setItem(storeKey, JSON.stringify(store));
    } catch {
      /* no storage: the numbers are estimated again each time */
    }
  }

  // What is on show: the page line, the foot, and the pages left in the chapter.
  let shown = 0; // the total on display, kept steady while the estimate settles
  function pageInfo(at = page) {
    const box = section();
    const list = Object.values(store.parts);
    const sum = (rows, key) => rows.reduce((total, row) => total + row[key], 0);
    const long = list.filter((row) => row.n >= 3);
    const perPage = long.length ? sum(long, "c") / sum(long, "n") : guess;
    const before = Number(box.dataset.before);
    const mine = Number(box.dataset.partChars);
    const earlier = list.filter((row) => row.b + row.c <= before + 1);
    const base = sum(earlier, "n") + Math.round(Math.max(0, before - sum(earlier, "c")) / perPage);
    const whole = Number(root.dataset.totalChars);
    const exact = sum(list, "c") >= 0.99 * whole; // every part has been laid out on this device
    let total = exact ? sum(list, "n") : base + phys + Math.round(Math.max(0, whole - before - mine) / perPage);
    if (!exact && total >= 100) total = Math.round(total / 10) * 10;
    const first = base + at * cols + 1;
    if (exact || !shown || Math.abs(total - shown) > 0.02 * shown || first > shown) shown = Math.max(total, first);
    const second = cols === 2 && at * cols + 1 < phys ? first + 1 : 0; // a blank right page has no number
    const seen = at * cols + (second ? 2 : 1);
    const afterPart = Number(box.dataset.chars) - (Number(box.querySelector("[data-c]")?.dataset.c) || 0) - mine;
    const left = phys - seen + Math.round(Math.max(0, afterPart) / perPage);
    const words = exact ? [copy.pg, copy.pgs, copy.foot] : [copy.pgAbout, copy.pgsAbout, copy.footAbout];
    return {
      line: second ? fill(words[1], first, second, shown) : fill(words[0], first, shown),
      a: cols === 2 ? String(first) : fill(words[2], first, shown),
      b: cols === 2 ? (second ? String(second) : "") : left <= 0 ? copy.lastPage : left === 1 ? copy.onePageLeft : fill(copy.pagesLeft, left),
    };
  }

  // ---- Laying out ----

  // The last drawn box of the part: its last fragment tells which column the text ends in.
  function lastFragment(box) {
    const items = box.querySelectorAll(":scope > *, :scope > .ts-reader__text > *");
    for (let i = items.length - 1; i >= 0; i--) {
      const rects = items[i].getClientRects();
      if (rects.length) return rects[rects.length - 1];
    }
    return null;
  }

  // Set the column geometry from the reader's size and read the page count back.
  function layOut() {
    const started = performance.now();
    root.scrollTop = 0; // a #sentence address can scroll even a box that hides its overflow
    const box = section();
    box.classList.remove("ts-reader__short");
    box.scrollLeft = 0;
    const css = getComputedStyle(root);
    const colCss = getComputedStyle(col);
    const probeCss = getComputedStyle(probe);
    const wide = innerWidth >= WIDE_PX;
    // A page's side margin is the column's own padding (the sheet on the desk), else the root's (full bleed).
    const margin = px(colCss.paddingLeft) || px(css.paddingLeft);
    const gap = Math.round(2 * margin);
    const foot = col.querySelector(".ts-pagefoot")?.offsetHeight || 0;
    const colUp = px(colCss.paddingTop) + px(colCss.paddingBottom);
    const availW = root.clientWidth - px(css.paddingLeft) - px(css.paddingRight) - px(colCss.paddingLeft) - px(colCss.paddingRight);
    const availH = root.clientHeight - px(css.paddingTop) - px(css.paddingBottom) - colUp - foot;
    const measure = px(probeCss.width);
    const least = px(probeCss.minWidth);
    cols = wide && availW >= 2 * least + gap ? 2 : 1; // two pages only when both fit at their least width
    const column = Math.floor(Math.min(measure, (availW - (cols - 1) * gap) / cols));
    const line = px(probeCss.lineHeight);
    let pageH = wide ? Math.min(availH, Math.ceil(TALL * (column + 2 * margin))) : availH;
    if (line > 0) pageH = Math.floor(pageH / line) * line; // a whole number of lines
    pageH = Math.floor(pageH * 100) / 100;
    guess = (column / (0.46 * px(probeCss.fontSize))) * (pageH / (line || 1.5 * px(probeCss.fontSize)));
    colW = column;
    gapW = gap;
    pageHt = pageH;
    marginW = margin;
    root.style.setProperty("--ts-cols", cols);
    root.style.setProperty("--ts-col", column + "px");
    root.style.setProperty("--ts-gap", gap + "px");
    root.style.setProperty("--ts-page-h", pageH + "px");
    root.classList.toggle("ts-reader--spread", cols === 2);
    colStep = column + gap;
    step = cols * colStep;
    shell.style.setProperty("--ts-text-w", step - gap + "px"); // the dock keeps to the width of the text
    shell.style.setProperty("--ts-frame-w", cols * column + (cols - 1) * gap + 2 * margin + "px");
    shell.style.setProperty("--ts-frame-h", pageH + colUp + foot + "px");
    // Columns in the part: a part that overflows says so by its scroll width; one that fits is read off its last box.
    const own = box.getBoundingClientRect();
    if (box.scrollWidth > box.clientWidth + 1) {
      phys = Math.max(1, Math.round((box.scrollWidth + gap) / colStep));
    } else {
      const last = lastFragment(box);
      phys = last ? Math.max(1, Math.floor((last.right - own.left - 1) / colStep) + 1) : 1;
      // A short part sits a little lower on its page, so it does not hug the top edge.
      if (phys === 1 && last && last.bottom - own.top < pageH / 2) box.classList.add("ts-reader__short");
    }
    count = Math.max(1, Math.ceil(phys / cols));
    page = Math.min(page, count - 1);
    frame = wide ? col.getBoundingClientRect() : root.getBoundingClientRect();
    shell.style.setProperty("--ts-frame-pad", margin + "px"); // the edges reach this far into the page
    shell.style.setProperty("--ts-frame-top", Math.round(frame.top - shell.getBoundingClientRect().top) + "px"); // where the page edges start
    record([column, pageH, cols, [...root.classList].filter((name) => LOOK_CLASS.test(name)).join(" ")].join("|"));
    layMs = performance.now() - started;
    performance.measure("ts-reader-lay", { start: started, end: started + layMs });
  }

  // A new look or size lays out again, and a flight in the air is over.
  function lay() {
    flip?.drop();
    layOut();
  }

  // ---- How a page turns: a flip when its module is here, else a slide, else nothing (decision 0036) ----

  const slowFlag = () => {
    try {
      return localStorage.getItem("ts.flip.slow") === "1";
    } catch {
      return false;
    }
  };
  const reduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;
  const mover = () => (reduced() || root.dataset.turn === "none" ? "none" : root.dataset.turn === "flip" && flip && !slowFlag() ? "flip" : "slide");
  let loadingFlip = false;
  function wantFlip() {
    if (flip || loadingFlip || root.dataset.turn !== "flip" || slowFlag() || reduced() || !root.dataset.flipSrc) return;
    loadingFlip = true;
    import(root.dataset.flipSrc)
      .then((module) => (flip = module.flipper(root, book)))
      .catch(() => {}) // the slide stays
      .finally(() => (loadingFlip = false));
  }

  function slide(dir) {
    if (!dir || mover() !== "slide") return;
    col.animate({ transform: [`translateX(${dir * 24}px)`, "none"], opacity: [0.5, 1] }, { duration: SLIDE_MS, easing: "ease-out" });
  }

  function go(n, dir) {
    page = Math.max(0, Math.min(n, count - 1));
    section().scrollLeft = page * step;
    slide(dir);
    // Two pages or fewer left: fetch the next part now so the turn is instant.
    if (count - page <= 3) fetchPart(section().dataset.nextHref);
    if (page <= 1) fetchPart(section().dataset.prevHref);
  }

  // A part that failed is never kept in `parts`, so Try again asks the server again. One quiet retry comes first, since a
  // restart of the server or a brief drop of the network is the likely cause; what went wrong is warned to the console
  // (the address without its query, never a token) so the next occurrence can be told apart.
  let signedOut = false; // the last answer was a redirect: the session ended
  async function attempt(href) {
    const where = new URL(href, location.href).pathname;
    try {
      const res = await fetch(href);
      if (res.redirected) {
        signedOut = true;
        console.warn("reader part did not load", where, "redirected: signed out");
        return null;
      }
      if (!res.ok) throw new Error("status " + res.status);
      const holder = document.createElement("template");
      holder.innerHTML = await res.text();
      const part = holder.content.querySelector("section[data-part]");
      if (!part) throw new Error("status " + res.status + " but no part in the answer");
      return part;
    } catch (error) {
      console.warn("reader part did not load", where, String(error.message || error));
      return null;
    }
  }

  function fetchPart(href) {
    if (!href) return null;
    if (!parts.has(href)) {
      signedOut = false;
      const request = attempt(href)
        .then((part) => part || (signedOut ? null : new Promise((resolve) => setTimeout(resolve, RETRY_MS)).then(() => attempt(href))))
        .then((part) => {
          if (part) {
            clearToast();
            ready.set(href, part);
          } else parts.delete(href);
          return part;
        });
      parts.set(href, request);
    }
    return parts.get(href);
  }

  // The one "did not load" toast: a further failed turn reuses it. It goes when the person leaves the reader and when a
  // part loads after all.
  let toast = null;
  let retry = null;
  function clearToast() {
    toast?.remove();
    toast = null;
  }
  function failed(again) {
    retry = again;
    if (toast?.isConnected) return;
    const words = JSON.parse(root.dataset.copy);
    toast = document.createElement("div");
    toast.className = "ts-toast";
    toast.setAttribute("role", "status");
    toast.append(signedOut ? words.signedOut : words.failed);
    const button = toast.appendChild(document.createElement("button"));
    button.className = "ts-btn ts-btn--quiet";
    button.textContent = signedOut ? words.signIn : words.retry;
    button.addEventListener("click", () => {
      clearToast();
      if (signedOut) location.assign("/login");
      else retry();
    });
    document.getElementById("toasts").append(toast);
  }

  // Replace the part with a neighbour and land on its first or last page.
  async function cross(href, first, dir) {
    const next = await fetchPart(href);
    if (!next) {
      failed(() => cross(href, first, dir));
      return false;
    }
    parts.delete(href);
    ready.delete(href);
    const flight = mover() === "flip" ? flip.begin(dir) : null; // the old page's face, before the part is replaced
    section().replaceWith(next);
    if (window.htmx) window.htmx.process(next); // the scroll flow's sentinel needs it later
    layOut();
    go(first ? 0 : count - 1, dir);
    flight?.land();
    return true;
  }

  // One page on, in the part or across its edge. The motion never waits: the real page is at its place when this returns.
  async function turnBy(dir) {
    flip?.drop();
    const to = page + dir;
    if (to < 0 || to >= count) return cross(dir > 0 ? section().dataset.nextHref : section().dataset.prevHref, dir > 0, dir);
    const flight = mover() === "flip" ? flip.begin(dir) : null;
    go(to, dir);
    flight?.land();
    return true;
  }

  // The drag of reader-flip.js moves the real page first and can put it back. Over a part edge that is only possible when
  // the neighbour has arrived and laying it out is quick; else there is nothing to drag and a swipe past 48 px turns.
  function shift(dir) {
    const to = page + dir;
    if (to >= 0 && to < count) {
      const was = page;
      go(to, 0);
      return { undo: () => go(was, 0), commit() {} };
    }
    const href = dir > 0 ? section().dataset.nextHref : section().dataset.prevHref;
    const next = ready.get(href);
    if (!next || layMs > 50) return null;
    const old = section();
    const was = page;
    old.replaceWith(next);
    layOut();
    go(dir > 0 ? 0 : count - 1, 0);
    return {
      undo() {
        next.replaceWith(old);
        layOut();
        go(was, 0);
      },
      commit() {
        parts.delete(href);
        ready.delete(href);
        if (window.htmx) window.htmx.process(next);
      },
    };
  }

  // A swipe of touch or pen: 48 px across and mostly sideways turns the page. A mouse drags to select text.
  const done = new AbortController();
  let from = null;
  root.addEventListener("pointerdown", (event) => (from = event.pointerType === "mouse" ? null : [event.clientX, event.clientY]), { signal: done.signal });
  root.addEventListener(
    "pointerup",
    (event) => {
      if (!from) return;
      const across = event.clientX - from[0];
      const down = event.clientY - from[1];
      from = null;
      if (Math.abs(across) >= SWIPE_PX && Math.abs(across) > 1.5 * Math.abs(down)) kit.turn(across < 0 ? 1 : -1);
    },
    { signal: done.signal },
  );

  // ---- Marking a page: a ribbon on each page shown, the page line for a screen reader, and the first-use hint ----

  const ribbons = [...shell.querySelectorAll(".ts-ribbon")];
  const edges = [...shell.querySelectorAll(".ts-edge")];
  const live = document.getElementById("reader-page-live");

  // Where column k of the page now shown starts, in the same coordinates as a rect: text before it is "before".
  const columnLeft = (k) => section().getBoundingClientRect().left + k * colStep - 1;

  // Each ribbon belongs to a column of the page shown. It is pressed when a bookmark flag lies in that column; a right
  // page that does not exist (the end of a part) or a single page has no second ribbon.
  function decorate() {
    const flags = [...section().querySelectorAll(".ts-reader__bm")];
    for (const button of ribbons) {
      const k = Number(button.dataset.page);
      button.hidden = k >= cols || page * cols + k >= phys;
      if (button.hidden) continue;
      const flag = flags.find((el) => {
        const left = el.getBoundingClientRect().left;
        return left >= columnLeft(k) && left < columnLeft(k + 1);
      });
      button.dataset.bm = flag?.dataset.bm || "";
      button.setAttribute("aria-pressed", String(Boolean(flag)));
      button.setAttribute("aria-label", flag ? copy.bmRemove : copy.bmPage);
    }
    // At the very start or end the edge has nowhere to go: it stays in the tree (focus must not be lost) but rests.
    for (const edge of edges) edge.setAttribute("aria-disabled", String(Number(edge.dataset.edge) > 0 ? pastEnd() : pastStart()));
    if (turned) {
      clearTimeout(speak);
      speak = setTimeout(() => live && (live.textContent = pageInfo().line), 500);
    }
  }
  const pastEnd = () => page >= count - 1 && !section().dataset.nextHref;
  const pastStart = () => page === 0 && !section().dataset.prevHref;
  let turned = false; // the page line is spoken after a turn, not on arrival
  let speak = 0;

  // A press marks the first sentence of that page, or takes away the bookmark that its flag stands for. The server does the
  // toggle by sentence; the answer redraws the block, and reader.js lays out again and calls decorate().
  shell.addEventListener(
    "click",
    (event) => {
      const button = event.target.closest(".ts-ribbon");
      if (!button) return;
      const k = Number(button.dataset.page);
      const sid = button.dataset.bm || kit.firstOn((rect) => rect.left < columnLeft(k))?.dataset.sid;
      const form = document.getElementById("reader-bm-form");
      if (!sid || !form) return;
      form.elements.sid.value = sid;
      form.elements.client_id.value = crypto.randomUUID();
      form.elements.remove?.remove();
      if (button.dataset.bm) form.append(Object.assign(document.createElement("input"), { type: "hidden", name: "remove", value: "1" }));
      form.requestSubmit();
    },
    { signal: done.signal },
  );

  // The hint, once per device: a touch screen is shown the tap zones; a mouse sees the edges pulse once.
  let hint = null;
  let hintTimer = 0;
  function unhint() {
    clearTimeout(hintTimer);
    hint?.remove();
    hint = null;
  }
  function hinted() {
    try {
      return localStorage.getItem("ts.reader.hinted") === "1";
    } catch {
      return true; // no storage, no way to say "once": stay quiet
    }
  }
  function showHint() {
    if (hinted() || !copy.hint) return;
    try {
      localStorage.setItem("ts.reader.hinted", "1");
    } catch {
      return;
    }
    if (matchMedia("(pointer: coarse)").matches) {
      const swapped = root.classList.contains("ts-reader--tapswap");
      hint = shell.appendChild(document.createElement("div"));
      hint.className = "ts-pagehint";
      hint.setAttribute("role", "status");
      // The left zone points left unless the person swapped the sides.
      for (const side of ["l", "r"]) hint.append(Object.assign(document.createElement("span"), { className: `ts-pagehint__chev ts-pagehint__chev--${side === "l" !== swapped ? "l" : "r"} ts-pagehint__zone--${side}` }));
      hint.append(Object.assign(document.createElement("p"), { className: "ts-pagehint__text", textContent: copy.hint }));
      hintTimer = setTimeout(unhint, 5000);
      document.addEventListener("pointerdown", unhint, { once: true, capture: true, signal: done.signal });
    } else if (!matchMedia("(prefers-reduced-motion: reduce)").matches) {
      for (const edge of edges) if (getComputedStyle(edge).pointerEvents !== "none") edge.animate({ opacity: [0.6, 1, 0.6] }, { duration: 1400, easing: "ease-in-out" });
    }
  }

  // Leaving the reader (a boosted link swaps #main, a plain one unloads) takes its toast along.
  document.addEventListener("htmx:after:swap", () => root.isConnected || clearToast(), { signal: done.signal });
  window.addEventListener("pagehide", clearToast, { signal: done.signal });

  // What reader-flip.js needs of the pages: the real section, where its columns sit on the screen, and the page numbers.
  const book = {
    section,
    mover,
    shift,
    moved: () => kit.moved?.(),
    turn: (dir) => kit.turn(dir),
    atEnd: () => page >= count - 1 && !section().dataset.nextHref,
    atStart: () => page === 0 && !section().dataset.prevHref,
    column: () => page * cols, // the first column of the turn on show
    dest: (dir) => (page + dir >= 0 && page + dir < count ? (page + dir) * cols : -1), // the first column of the turn a step away, in this part
    claimSwipe: () => (from = null), // a drag has the touch; the plain swipe stands down
    geometry() {
      const own = section().getBoundingClientRect();
      const wide = innerWidth >= WIDE_PX;
      return { cols, column: colW, gap: gapW, pageH: pageHt, margin: marginW, left: own.left, top: own.top, frame: wide ? col.getBoundingClientRect() : root.getBoundingClientRect() };
    },
    // The foot of a column: the number at the left, then the pages left (on a spread the left page has the one, the right page the other).
    labels(column) {
      const info = pageInfo(Math.floor(column / cols));
      return cols === 2 ? (column % cols ? ["", info.b] : [info.a, ""]) : [info.a, info.b];
    },
  };
  document.addEventListener("ts-reader-look", wantFlip, { signal: done.signal });

  root.classList.add("ts-reader--pages");
  layOut();
  showHint();
  wantFlip();

  return {
    lay,
    // The page's box: a tap outside it turns the page.
    zone: () => frame,
    pageInfo,
    decorate,
    // A rect is before the page when it starts left of the section's left edge.
    before() {
      const left = section().getBoundingClientRect().left;
      return (rect) => rect.left < left - 1;
    },
    show(sid, off) {
      const rect = kit.locate(sid, off);
      if (!rect) return false;
      const box = section();
      go(Math.floor((rect.left - box.getBoundingClientRect().left + box.scrollLeft + 2) / step), 0);
      return true;
    },
    next() {
      turned = true;
      unhint();
      return turnBy(1);
    },
    prev() {
      turned = true;
      unhint();
      return turnBy(-1);
    },
    atEnd: () => page >= count - 1 && !section().dataset.nextHref,
    atStart: () => page === 0 && !section().dataset.prevHref,
    stop() {
      flip?.stop();
      done.abort();
      clearToast();
      unhint();
      clearTimeout(speak);
      for (const button of ribbons) button.hidden = true;
      root.classList.remove("ts-reader--pages", "ts-reader--spread");
      for (const name of ["--ts-text-w", "--ts-frame-w", "--ts-frame-h", "--ts-frame-top", "--ts-frame-pad"]) shell.style.removeProperty(name);
      section().classList.remove("ts-reader__short");
      section().scrollLeft = 0;
      probe.remove();
    },
  };
}
