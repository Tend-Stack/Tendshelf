// Pages for the book reader (M5 RD2). reader.js loads this only when the reader shows pages.
// One chapter part is laid out in CSS columns; a turn moves the part's scrollLeft by one page.
// A page number here is only an index into this layout, never stored or shown as "page 12": the
// place that is saved is a sentence and an offset (reader.js).

const SLIDE_MS = 180;
const SWIPE_PX = 48;

export function pages(root, kit) {
  const col = root.querySelector(".ts-reader__col");
  const shell = root.closest(".ts-readerpage");
  const section = () => root.querySelector("section[data-part]");
  // A hidden box in the text's own font. Its CSS widths (68ch and 45ch) tell us what a column may measure.
  const probe = document.createElement("div");
  probe.className = "ts-reader__text ts-reader__probe";
  probe.setAttribute("aria-hidden", "true");
  col.append(probe);

  let step = 1; // scrollLeft distance of one page
  let count = 1; // pages in the part
  let page = 0;
  const parts = new Map(); // fetched neighbour parts by address

  // Set the column geometry from the reader's size and read the page count back.
  function lay() {
    const started = performance.now();
    root.scrollTop = 0; // a #sentence address can scroll even a box that hides its overflow
    const css = getComputedStyle(root);
    const probeCss = getComputedStyle(probe);
    const width = root.clientWidth - parseFloat(css.paddingLeft) - parseFloat(css.paddingRight);
    const height = root.clientHeight - parseFloat(css.paddingTop) - parseFloat(css.paddingBottom);
    const gap = Math.round(parseFloat(probeCss.fontSize) * 1.5);
    const measure = parseFloat(probeCss.width);
    const least = parseFloat(probeCss.minWidth);
    // Two columns only from 1024 px, and only when both fit at their least width.
    const cols = innerWidth >= 1024 ? Math.max(1, Math.min(2, Math.floor((width + gap) / (least + gap)))) : 1;
    const column = Math.floor(Math.min(measure, (width - (cols - 1) * gap) / cols));
    root.style.setProperty("--ts-cols", cols);
    root.style.setProperty("--ts-col", column + "px");
    root.style.setProperty("--ts-gap", gap + "px");
    root.style.setProperty("--ts-page-h", Math.floor(height) + "px");
    step = cols * (column + gap);
    shell.style.setProperty("--ts-text-w", step - gap + "px"); // the dock keeps to the width of the text
    count = Math.max(1, Math.ceil(section().scrollWidth / step)); // this read is the layout
    performance.measure("ts-reader-lay", { start: started, end: performance.now() });
  }

  function slide(dir) {
    if (!dir || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    col.animate({ transform: [`translateX(${dir * 24}px)`, "none"], opacity: [0.5, 1] }, { duration: SLIDE_MS, easing: "ease-out" });
  }

  function go(n, dir) {
    page = Math.max(0, Math.min(n, count - 1));
    section().scrollLeft = page * step;
    slide(dir);
    // Two pages or fewer left: fetch the next part now so the turn is instant.
    if (count - page <= 3) fetchPart(section().dataset.nextHref);
  }

  function fetchPart(href) {
    if (!href) return null;
    if (!parts.has(href)) {
      const request = fetch(href)
        .then((res) => (res.ok ? res.text() : Promise.reject(new Error(res.status))))
        .then((html) => {
          const holder = document.createElement("template");
          holder.innerHTML = html;
          return holder.content.querySelector("section[data-part]");
        })
        .catch(() => {
          parts.delete(href);
          return null;
        });
      parts.set(href, request);
    }
    return parts.get(href);
  }

  // Replace the part with a neighbour and land on its first or last page.
  async function cross(href, first, dir) {
    const next = await fetchPart(href);
    if (!next) return false;
    parts.delete(href);
    section().replaceWith(next);
    if (window.htmx) window.htmx.process(next); // the scroll flow's sentinel needs it later
    lay();
    go(first ? 0 : count - 1, dir);
    return true;
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

  root.classList.add("ts-reader--pages");
  lay();

  return {
    lay,
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
    async next() {
      if (page < count - 1) return go(page + 1, 1) || true;
      return cross(section().dataset.nextHref, true, 1);
    },
    async prev() {
      if (page > 0) return go(page - 1, -1) || true;
      return cross(section().dataset.prevHref, false, -1);
    },
    atEnd: () => page >= count - 1 && !section().dataset.nextHref,
    atStart: () => page === 0 && !section().dataset.prevHref,
    stop() {
      done.abort();
      root.classList.remove("ts-reader--pages");
      shell.style.removeProperty("--ts-text-w");
      section().scrollLeft = 0;
      probe.remove();
    },
  };
}
