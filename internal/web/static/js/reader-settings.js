// Reading settings (M5 R4 P4). A hidden marker on the reader page loads this, because the sheet lives
// in #sheet, outside #main. A change is applied at once; htmx posts the same form to keep it per person.
const LOOK = /^ts-reader--(sans|dyslexic|size-|leading-)/;
const DATA = ["hl", "word", "follow", "followStyle", "view"]; // field names that are also data attributes

export function mount(marker) {
  // Every .ts-reader: the page and the sheet's preview.
  const apply = (form) => {
    const data = new FormData(form);
    // The last value wins: a switch posts a hidden 0, then 1 when it is on.
    const get = (name) => {
      const v = data.getAll(name).pop();
      return v === "1" ? "on" : v === "0" ? "off" : v;
    };
    const add = [`ts-reader--size-${get("size")}`, `ts-reader--leading-${get("leading")}`];
    if (get("font") !== "serif") add.push(`ts-reader--${get("font")}`);
    for (const el of document.querySelectorAll(".ts-reader")) {
      el.classList.remove(...[...el.classList].filter((c) => LOOK.test(c)));
      el.classList.add(...add);
      for (const name of DATA) el.dataset[name] = get(name);
      el.dataset.return = get("returnAuto");
    }
    if (get("theme")) document.documentElement.dataset.theme = get("theme");
  };

  // Without the Custom Highlight API only the sentence is marked: say so.
  const noWord = () => {
    if ("highlights" in CSS) return;
    for (const el of document.querySelectorAll("[data-ts-no-word]")) el.hidden = false;
  };

  // The reader changed its view (focus line): show it in the open sheet, or save it straight away.
  const onView = () => {
    const view = document.getElementById("reader").dataset.view;
    const radio = document.querySelector(`form[data-ts-reader-settings] input[name="view"][value="${view}"]`);
    if (radio) {
      radio.checked = true;
      radio.dispatchEvent(new Event("change", { bubbles: true }));
    } else {
      fetch(marker.dataset.action, {
        method: "POST",
        headers: { "HX-Request": "true", "X-CSRF-Token": marker.dataset.csrf },
        body: new URLSearchParams({ view }),
      }).catch(() => {});
    }
  };

  // Listeners stop for good once the marker has left the page.
  const stop = new AbortController();
  const on = (name, fn) =>
    document.addEventListener(name, (event) => (marker.isConnected ? fn(event) : stop.abort()), { signal: stop.signal });
  on("change", (event) => {
    const form = event.target.closest("form[data-ts-reader-settings]");
    if (form) apply(form);
  });
  on("ts:reader:view", onView);
  on("htmx:after:swap", noWord);
  noWord();
}
