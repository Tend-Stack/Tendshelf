// Reading settings (M5 R4 P4). A hidden marker on the reader page loads this, because the sheet lives
// in #sheet, outside #main. A change is applied at once; htmx posts the same form to keep it per person.
const LOOK = /^ts-reader--(sans|dyslexic|size-|leading-)/;

export function mount(marker) {
  // Every .ts-reader: the page and the sheet's preview.
  const apply = (form) => {
    const data = new FormData(form);
    const get = (name) => data.getAll(name).pop();
    const add = [`ts-reader--size-${get("size")}`, `ts-reader--leading-${get("leading")}`];
    if (get("font") !== "serif") add.push(`ts-reader--${get("font")}`);
    for (const el of document.querySelectorAll(".ts-reader")) {
      el.classList.remove(...[...el.classList].filter((c) => LOOK.test(c)));
      el.classList.add(...add);
    }
    if (get("theme")) document.documentElement.dataset.theme = get("theme");
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
      if (form) apply(form);
    },
    { signal: stop.signal },
  );
}
