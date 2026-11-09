// The book reader (M5 RD2): pages or scroll, the place you are at, saving it, input and progress.
// shell.js mounts it from data-ts-module on #reader. reader-pages.js (the columns) loads only for pages.
// A place is a sentence id and an offset into its block, with a quote and a character count. It is never
// a screen page, so it survives a change of size, font, margin or flow.

const SAVE_DELAY_MS = 3000;
const MAX_ACTIVE_MS = 120000; // one turn counts at most two minutes of reading time

const textNodes = (el) => {
  const found = [];
  for (const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT); walker.nextNode(); ) found.push(walker.currentNode);
  return found;
};

// The text node and offset of character k of an element's text.
function textPoint(el, k) {
  let last = null;
  for (const node of textNodes(el)) {
    if (k < node.length) return [node, k];
    k -= node.length;
    last = node;
  }
  return last ? [last, last.length] : [el, 0];
}

// The rect of character k (or the sentence's first line when the character has no box).
function charRect(el, k) {
  const range = document.createRange();
  range.setStart(...textPoint(el, k));
  range.setEnd(...textPoint(el, k + 1));
  return range.getClientRects()[0];
}

const lastRect = (el) => {
  const rects = el.getClientRects();
  return rects[rects.length - 1];
};

// This browser's id, the same as the player's, so the server can tell its saves from another device's.
function deviceId() {
  try {
    let id = localStorage.getItem("ts.deviceId");
    if (!id) {
      id = Math.random().toString(36).slice(2) + Date.now().toString(36);
      localStorage.setItem("ts.deviceId", id);
    }
    return id;
  } catch {
    return "reader";
  }
}

function csrfToken() {
  try {
    return JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return "";
  }
}

// A toast with buttons, in #toasts. It stays until a button is pressed.
function offer(text, actions) {
  const toast = document.createElement("div");
  toast.className = "ts-toast";
  toast.setAttribute("role", "status");
  toast.append(text);
  const row = toast.appendChild(document.createElement("span"));
  row.className = "ts-toast__actions";
  for (const [label, run] of actions) {
    const button = row.appendChild(document.createElement("button"));
    button.type = "button";
    button.className = "ts-btn ts-btn--quiet";
    button.textContent = label;
    button.addEventListener("click", () => {
      toast.remove();
      run();
    });
  }
  document.getElementById("toasts").append(toast);
}

export function mount(root) {
  const copy = JSON.parse(root.dataset.copy || "{}");
  const shell = root.closest(".ts-readerpage");
  const bookId = root.dataset.book;
  const stop = new AbortController();
  // Every listener stops once the reader has left the page.
  const on = (target, type, run, options) =>
    target.addEventListener(type, (event) => (root.isConnected ? run(event) : stop.abort()), { signal: stop.signal, ...options });

  let view = null; // the adapter of the flow now shown
  let flow = "";
  let here = null; // the place we are at
  let busy = false;
  let dirty = false;
  let timer = 0;
  let chars = 0; // characters read since the last save, in forward turns only
  let active = 0; // milliseconds of visible reading since the last save
  let lastTurn = Date.now();
  let known = 0; // the newest place the server has told us of
  let turns = 0; // forward turns since this page opened, which retire the chip that leads back after a skim or a link

  // ---- Finding the place ----

  const sentence = (sid) => root.querySelector(`.ts-s[data-sid="${CSS.escape(sid)}"]`);

  // The rect of a place on screen, or null when its sentence is not in the page.
  function locate(sid, off) {
    const el = sentence(sid);
    if (!el) return null;
    const k = Math.max(0, Math.min(off - Number(el.dataset.o), el.textContent.length));
    return charRect(el, k) || lastRect(el) || null;
  }

  // A place from a sentence element and the character in it.
  function describe(el, k) {
    const block = el.closest("[data-c]");
    const part = el.closest("section");
    const off = Number(el.dataset.o) + k;
    const first = Number(part.querySelector("[data-c]").dataset.c);
    return {
      sid: el.dataset.sid,
      off,
      quote: block.textContent.slice(off, off + 64),
      chapter: Number(part.dataset.ch),
      inChapter: Number(block.dataset.c) + off, // characters of the chapter before the place
      charPos: Number(part.dataset.before) + Number(block.dataset.c) - first + off, // and of the book
      page: block.dataset.pg || "", // the book's own label when it has a page list, so "xii" works
      chars: Number(part.dataset.chars),
    };
  }

  // The first sentence not before the page, by a binary search over where the sentences end. `before` is a test on a rect.
  function firstOn(before) {
    const list = root.querySelectorAll(".ts-s");
    let low = 0;
    let high = list.length - 1;
    while (low < high) {
      const mid = (low + high) >> 1;
      const rect = lastRect(list[mid]);
      if (rect && !before(rect)) high = mid;
      else low = mid + 1;
    }
    return list[low] || null;
  }

  // The first text on screen: that sentence, then a binary search over its characters. About twenty layout reads.
  function firstShown(before = view.before()) {
    const el = firstOn(before);
    if (!el) return null;
    let low = 0;
    let high = el.textContent.length - 1;
    while (low < high) {
      const mid = (low + high) >> 1;
      const rect = charRect(el, mid);
      if (rect && !before(rect)) high = mid;
      else low = mid + 1;
    }
    return describe(el, low);
  }

  // ---- The two flows ----

  const scrollTop = () => root.getBoundingClientRect().top;
  const screen = () => root.clientHeight - 2 * (parseFloat(getComputedStyle(root.querySelector("section .ts-reader__text")).lineHeight) || 24);
  const scrollFlow = {
    before: () => {
      const top = scrollTop();
      return (rect) => rect.top < top - 1;
    },
    show(sid, off) {
      const rect = locate(sid, off);
      if (sentence(sid) === root.querySelector(".ts-s")) root.scrollTop = 0; // the start of the book or chapter keeps its title
      else if (rect) root.scrollTop += rect.top - scrollTop() - 8;
      return Boolean(rect);
    },
    // A screen minus two lines.
    async next() {
      return !this.atEnd() && !root.scrollBy(0, screen());
    },
    async prev() {
      if (this.atStart()) root.querySelector(".ts-reader__prev")?.click();
      return !this.atStart() && !root.scrollBy(0, -screen());
    },
    atEnd: () => root.scrollTop >= root.scrollHeight - root.clientHeight - 2 && !root.querySelector(".ts-reader__next"),
    atStart: () => root.scrollTop <= 0,
    lay() {},
    stop() {},
  };

  // Pages at every width; only a person who chose Scroll gets the scroll (decision 0036).
  const wanted = () => (root.dataset.flow === "scroll" ? "scroll" : "pages");

  // Show the flow that is wanted, at the place we are at.
  async function setFlow() {
    const next = wanted();
    if (next === flow) return false;
    view?.stop();
    if (next === "pages") {
      // Pages show one part at a time: keep the part we are in.
      const keep = here && sentence(here.sid)?.closest("section");
      for (const part of root.querySelectorAll("section[data-part]")) if (keep && part !== keep) part.remove();
      const module = await import(root.dataset.pagesSrc);
      view = module.pages(root, { locate, turn, moved: afterTurn });
    } else {
      view = scrollFlow;
    }
    flow = next;
    root.classList.toggle("ts-reader--scroll", next === "scroll");
    return true;
  }

  // Lay out again and bring the place back on screen (a resize, a font, a setting, a swap).
  function relay() {
    view.lay();
    if (here) view.show(here.sid, here.off);
    paint();
  }

  // ---- Progress ----

  const minutes = (n, unit) => {
    try {
      return new Intl.NumberFormat(document.documentElement.lang || undefined, { style: "unit", unit, unitDisplay: "short" }).format(n);
    } catch {
      return n + (unit === "hour" ? " h" : " min");
    }
  };
  // Time for a number of characters at this person's speed, in steps of five minutes.
  function span(left) {
    const cpm = Number(root.dataset.cpm);
    if (left <= 0 || !cpm) return "";
    const total = Math.ceil(left / cpm / 5) * 5;
    const parts = [];
    if (total >= 60) parts.push(minutes(Math.floor(total / 60), "hour"));
    if (total % 60 || total < 60) parts.push(minutes(total % 60, "minute"));
    return parts.join(" ");
  }

  const skimBar = document.getElementById("reader-skim");
  function paint() {
    if (!here) return;
    const total = Number(root.dataset.totalChars);
    const pages = Number(root.dataset.pages);
    const about = root.dataset.learned === "true" ? "" : "About";
    const printed = here.page && pages ? copy.pageOf.replace("%d", here.page).replace("%d", pages) : "";
    root.dataset.hereSid = here.sid;
    const info = view.pageInfo?.();
    if (info) {
      // Pages: the number that fits this device first, the book's own label second.
      document.getElementById("reader-page").textContent = info.line;
      document.getElementById("reader-print").textContent = printed;
      document.getElementById("reader-foot-a").textContent = info.a;
      document.getElementById("reader-foot-b").textContent = info.b;
    } else {
      document.getElementById("reader-page").textContent = printed;
      document.getElementById("reader-print").textContent = "";
    }
    const left = [];
    const chapterTime = span(here.chars - here.inChapter);
    const bookTime = span(total - here.charPos);
    if (chapterTime) left.push(copy[about ? "chAbout" : "ch"].replace("%s", chapterTime));
    if (bookTime) left.push(copy[about ? "bkAbout" : "bk"].replace("%s", bookTime));
    document.getElementById("reader-left").textContent = left.join(" · ");
    const ruler = shell.querySelector(".ts-ruler");
    const percent = total ? Math.min(100, (here.charPos * 100) / total) : 0;
    ruler.querySelector(".ts-ruler__fill").style.setProperty("--ts-value", percent);
    shell.style.setProperty("--ts-value", percent); // the thin line along the bottom edge while the bar is hidden
    const stack = Math.round((8 * percent) / 100); // the two page-edge stacks, 8 px in all, thicken as the book is read
    shell.style.setProperty("--ts-stack-l", stack + "px");
    shell.style.setProperty("--ts-stack-r", 8 - stack + "px");
    ruler.setAttribute("aria-valuenow", Math.round(percent));
    if (skimBar && document.activeElement !== skimBar) skimBar.value = Math.round(percent * 100); // the skim bar follows the place unless it is in use
  }

  // ---- Saving ----

  function body() {
    return new URLSearchParams({
      edition_id: root.dataset.edition,
      chapter_idx: here.chapter,
      sid: here.sid,
      off: here.off,
      quote: here.quote,
      char_pos: here.charPos,
      client_updated_at: Date.now(),
      device_id: deviceId(),
      chars_read: chars,
      active_ms: active,
      _csrf: csrfToken(),
    });
  }

  // Tell reader-offline.js what happened to a save (it keeps the place queue, decision 0019): "place" before
  // anything is sent, then "saved" when the server answered or "unsent" when it did not.
  function tell(name, form) {
    root.dispatchEvent(new CustomEvent("ts:reader:" + name, { detail: String(form) }));
  }

  // Send the place. The place reaches the queue first. When the page is going away (beacon) the browser
  // queues the request and gives no answer, so the queue keeps the place until a later save is answered.
  async function save(beacon) {
    clearTimeout(timer);
    if (!dirty || !here) return;
    dirty = false;
    const form = body();
    chars = active = 0;
    tell("place", form);
    if (beacon) {
      if (!navigator.onLine || !navigator.sendBeacon(root.dataset.save, form)) tell("unsent", form);
      return;
    }
    try {
      const res = await fetch(root.dataset.save, { method: "POST", body: form, headers: { "X-CSRF-Token": csrfToken() } });
      if (!res.ok) throw new Error(res.status);
      tell("saved", form);
      const answer = await res.json();
      known = Math.max(known, answer.reading.clientUpdatedAt);
      if (!answer.applied) other(answer.reading);
    } catch {
      tell("unsent", form);
    }
  }

  // After a turn or a scroll step: learn the place, count what was read and save soon.
  function moved() {
    const was = here;
    const now = firstShown();
    if (!now || (was && was.sid === now.sid && was.off === now.off)) return;
    here = now;
    const gone = now.charPos - (was ? was.charPos : now.charPos);
    const standard = Number(root.dataset.totalChars) / (Number(root.dataset.pages) || 1);
    if (gone > 0 && gone <= 3 * Math.max(standard, 1500)) chars += gone; // forward turns of up to three pages
    active += Math.min(Date.now() - lastTurn, MAX_ACTIVE_MS);
    lastTurn = Date.now();
    dirty = true;
    if (gone > 0 && ++turns >= 3) shell.querySelector(".ts-reader__back")?.remove(); // three turns on, the way back is no longer wanted
    paint();
    clearTimeout(timer);
    timer = setTimeout(save, SAVE_DELAY_MS);
  }

  // ---- The skim bar: shown at once, its script loaded on the first touch ----

  if (skimBar) {
    skimBar.hidden = false;
    let loading = null;
    const wake = (event) => {
      const from = skimBar.value; // before the touch that woke it has moved the thumb
      // The touch that woke the script may end before the script arrives: its end is kept as a promise.
      const released = event.type === "pointerdown" ? new Promise((done) => addEventListener("pointerup", done, { once: true })) : null;
      loading ??= import(skimBar.dataset.src).then((module) => module.skim(root, skimBar, { go, place: () => here, from, released }));
    };
    for (const type of ["pointerdown", "keydown", "focus"]) skimBar.addEventListener(type, wake, { signal: stop.signal });
  }

  // ---- Read aloud (decision 0032): the controls show only while the device has a voice of its own; the script loads on the first press ----

  const aloud = shell.querySelectorAll("[data-aloud-src]");
  const voiced = () => {
    const n = window.tsNativeSpeech ? tsNativeSpeech.voices().length : window.speechSynthesis?.getVoices().filter((v) => v.localService).length;
    for (const button of aloud) button.hidden = !n;
  };
  voiced();
  window.speechSynthesis?.addEventListener("voiceschanged", voiced, { signal: stop.signal });
  window.tsNativeSpeech?.ask();
  on(document, "ts:native:voices", voiced);
  let loading = null;
  const kit = { place: () => here, flow: () => flow, turn, firstOn, follow: (sid) => view.show(sid, Number(sentence(sid)?.dataset.o)) && moved() };
  for (const button of aloud) {
    button.addEventListener("click", () => {
      loading ??= import(button.dataset.aloudSrc).then((module) => module.aloud(root, shell, kit));
      loading.then((api) => api.toggle(button));
    });
  }

  // ---- Another device ----

  const go = (href) => {
    const link = root.appendChild(document.createElement("a"));
    link.href = href;
    link.click();
    link.remove();
  };

  function other(reading) {
    if (!reading || reading.deviceId === deviceId() || reading.clientUpdatedAt <= known) return;
    known = reading.clientUpdatedAt;
    const label = reading.deviceLabel || "another device";
    offer(copy.further.replace("%s", label), [
      [copy.go, () => go(`/books/${bookId}/read?at=${encodeURIComponent(reading.sid)}&off=${reading.off}`)],
      [copy.stay, () => {}],
    ]);
  }

  async function ask(first) {
    try {
      const res = await fetch(root.dataset.save);
      const { reading } = await res.json();
      if (first) known = reading ? reading.clientUpdatedAt : 0;
      else other(reading);
    } catch {
      /* offline: nothing to compare */
    }
  }

  // ---- Input ----

  // The place moved by a turn (also a dragged leaf that was let go past the middle).
  function afterTurn() {
    moved();
    // On a phone the bar and dock lie over the page, so a turn puts them away (unless the person is using them).
    if (flow === "pages" && innerWidth < 1024 && !document.activeElement?.closest(".ts-readerbar, .ts-readdock")) shell.dataset.chrome = "off";
  }

  async function turn(direction) {
    if (busy || !view) return;
    busy = true;
    try {
      if (await (direction > 0 ? view.next() : view.prev())) afterTurn();
    } finally {
      busy = false;
    }
  }

  // A tap clears a selection before its click, so the selection is noted when the press starts.
  let selected = false;
  let pointerKind = "touch";
  on(root, "pointerdown", (event) => {
    selected = !getSelection().isCollapsed;
    pointerKind = event.pointerType;
  });
  function tap(event) {
    if (event.target.closest("a, button, input, textarea, select, .ts-hl") || selected) return;
    // The zones are measured on the page itself: a press on the desk beside it is a turn.
    const rect = view.zone?.() || root.getBoundingClientRect();
    const across = (event.clientX - rect.left) / rect.width;
    if (across >= 0.25 && across <= 0.75) {
      // A mouse click in the text never hides the bar (the way back is the bar's handle, or the mouse at the top);
      // a finger or pen in the middle still toggles it.
      if (pointerKind !== "mouse") shell.dataset.chrome = shell.dataset.chrome === "off" ? "on" : "off";
    } else if (flow === "pages") {
      const swapped = root.classList.contains("ts-reader--tapswap");
      turn(across > 0.5 !== swapped ? 1 : -1);
    }
  }

  // The handle shown while the bar is hidden brings it back.
  on(shell, "click", (event) => {
    if (!event.target.closest("[data-show-bar]")) return;
    shell.dataset.chrome = "on";
    shell.querySelector(".ts-readerbar a")?.focus({ preventScroll: true });
  });

  // In the scroll flow a hidden bar takes no room (pages-reader.css), so the text's box moves up or down by the bar's
  // height. The scroll position follows by the same amount, so the words stay where they are and nothing is recorded
  // as a move. The pages flow keeps the room, since its columns are laid out for it.
  let rootTop = root.getBoundingClientRect().top;
  new MutationObserver(() => {
    const top = root.getBoundingClientRect().top;
    if (flow === "scroll" && top !== rootTop) root.scrollTop += top - rootTop;
    rootTop = top;
  }).observe(shell, { attributes: true, attributeFilter: ["data-chrome"] });
  on(window, "resize", () => (rootTop = root.getBoundingClientRect().top));

  // A click is a tap or a mouse press; the browser keeps swipes and drags out of it. Swipes are in reader-pages.js.
  on(root, "click", tap);

  // Keys, caught before player.js sees them. A field, a dialog or an open sheet keeps its own keys.
  on(
    window,
    "keydown",
    (event) => {
      const key = event.key;
      if (event.ctrlKey || event.metaKey || event.altKey || event.target.closest?.("input, textarea, select, [contenteditable], dialog, [role=dialog]")) return;
      if (document.querySelector("#sheet [role=dialog]")) return;
      const space = key === " ";
      let run = null;
      if (key === "ArrowRight" || key === "PageDown" || (space && !event.shiftKey)) run = () => turn(1);
      else if (key === "ArrowLeft" || key === "PageUp" || (space && event.shiftKey)) run = () => turn(-1);
      else if ((key === "[" || key === "]") && here) {
        const chapter = here.chapter + (key === "]" ? 1 : -1);
        if (chapter >= 1 && chapter <= Number(root.dataset.chapterCount)) run = () => go(`/books/${bookId}/read?ch=${chapter}`);
      } else if (key === "t" || key === "/") {
        run = () => shell.querySelector(`.ts-readerbar a[href*="sheet=${key === "t" ? "contents" : "search"}"]`)?.click();
      }
      if (!run || (space && event.target.closest?.("button, a, summary"))) return;
      event.preventDefault();
      event.stopPropagation();
      run();
    },
    { capture: true },
  );

  // ---- Scroll flow: scrolling is a move, and the end of a part asks for the next ----

  let scrolling = 0;
  on(root, "scroll", () => {
    if (flow !== "scroll") return;
    clearTimeout(scrolling);
    scrolling = setTimeout(moved, 150);
  });
  const sentinels = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        sentinels.unobserve(entry.target);
        entry.target.dispatchEvent(new Event("ts-reader-next", { bubbles: true }));
      }
    },
    { root, rootMargin: "0px 0px 100% 0px" },
  );
  const watch = () => root.querySelectorAll(".ts-reader__next").forEach((el) => flow === "scroll" && sentinels.observe(el));

  // ---- Start and the things that change the layout ----

  let resizing = 0;
  on(window, "resize", () => {
    clearTimeout(resizing);
    resizing = setTimeout(async () => {
      await setFlow();
      relay();
      watch();
    }, 150);
  });
  on(document, "ts-reader-look", async () => {
    await setFlow();
    relay();
    watch();
  });
  on(document, "htmx:after:swap", (event) => {
    if (!root.contains(event.target)) return;
    watch();
    if (flow === "pages") relay();
  });
  on(document, "htmx:before:request", (event) => {
    if (!root.contains(event.target)) save(true); // leaving the book
  });
  on(document, "visibilitychange", () => {
    if (document.hidden) {
      active += Math.min(Date.now() - lastTurn, MAX_ACTIVE_MS);
      save(true);
    } else {
      lastTurn = Date.now();
      ask(false);
    }
  });
  on(window, "pagehide", () => save(true));
  if (document.fonts) on(document.fonts, "loadingdone", () => view && relay());

  setFlow().then(() => {
    // A phone opens on the page alone: the bar would cover text. A tap in the middle brings it back.
    if (flow === "pages" && innerWidth < 1024) shell.dataset.chrome = "off";
    const el = sentence(root.dataset.at);
    if (el) {
      view.lay();
      view.show(root.dataset.at, Number(root.dataset.off) || 0);
      here = describe(el, Math.max(0, (Number(root.dataset.off) || 0) - Number(el.dataset.o)));
      paint();
    } else {
      view.lay();
      here = firstShown();
      paint();
    }
    watch();
    ask(true);
    document.fonts?.ready.then(() => root.isConnected && relay());
  });
}
