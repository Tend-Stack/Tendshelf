// Reading settings (M5 R4 P4). A hidden marker on the reader page loads this, because the sheet lives
// in #sheet, outside #main. A change is applied at once; htmx posts the same form to keep it per person.
const LOOK = /^ts-reader--(sans|dyslexic|size-|leading-|margin-|justify|nohyph|tapswap)/;

export function mount(marker) {
  // Every .ts-reader: the page and the sheet's preview.
  const apply = (form, changed) => {
    const data = new FormData(form);
    const get = (name) => data.getAll(name).pop();
    const add = [`ts-reader--size-${get("size")}`, `ts-reader--leading-${get("leading")}`, `ts-reader--margin-${get("margin")}`];
    if (get("font") !== "serif") add.push(`ts-reader--${get("font")}`);
    // The switches: justified text and mirrored taps add a class, hyphens add one when they are off.
    if (get("justify") === "1") add.push("ts-reader--justify");
    if (get("hyphens") !== "1") add.push("ts-reader--nohyph");
    if (get("tap_swap") === "1") add.push("ts-reader--tapswap");
    for (const el of document.querySelectorAll(".ts-reader")) {
      el.classList.remove(...[...el.classList].filter((c) => LOOK.test(c)));
      el.classList.add(...add);
    }
    if (get("theme")) document.documentElement.dataset.theme = get("theme");
    // The page reader lays itself out again for the new look, and takes the new flow.
    const page = document.getElementById("reader");
    if (page) page.dataset.flow = get("flow");
    // Page turn: picking Flip again gives the flip another chance after it was judged too slow on this device.
    if (page) page.dataset.turn = get("turn");
    if (changed === "turn" && get("turn") === "flip") {
      try {
        localStorage.removeItem("ts.flip.slow");
      } catch {
        /* no storage: nothing was kept */
      }
    }
    // Comfort (G5): the three settings are data attributes; the module is fetched only once one is on.
    if (page) {
      page.dataset.keepAwake = String(get("keep_awake") === "1");
      page.dataset.warm = get("warm");
      page.dataset.autoHide = String(get("auto_hide") === "1");
      const comfort = page.dataset.keepAwake === "true" || page.dataset.warm !== "off" || page.dataset.autoHide === "true";
      if (comfort && marker.dataset.comfortSrc) import(marker.dataset.comfortSrc).then((m) => m.mount(marker)).catch(() => {});
    }
    document.dispatchEvent(new Event("ts-reader-look"));
  };

  // The listener stops for good once the marker has left the page.
  const stop = new AbortController();
  document.addEventListener(
    "change",
    (event) => {
      if (!marker.isConnected) {
        stop.abort();
        return;
      }
      const form = event.target.closest("form[data-ts-reader-settings]");
      if (form) apply(form, event.target.name);
    },
    { signal: stop.signal },
  );
}
