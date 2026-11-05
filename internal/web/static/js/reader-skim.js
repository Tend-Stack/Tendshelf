// The skim bar of the book reader (M5 G4): a range input laid over the dock's ruler. Dragging it names the
// chapter under the thumb; letting go opens the book there. A jump saves no place, it only leaves the way back
// ("ts.back.<book>", read by reader-marks.js), so reading on is what moves the saved place.
// Loaded by reader.js on the first touch of the bar. The words come from the page: data-skim on #reader is
// [[characters before the chapter, title], ...] and the bar's label is the page's.
export function skim(root, input, { go, place, from, released }) {
  const list = JSON.parse(root.dataset.skim || "[]");
  const total = Number(root.dataset.totalChars);
  const tip = document.getElementById("reader-skim-tip");
  const backKey = "ts.back." + root.dataset.book;
  let start = from; // where the thumb was before this touch, which Escape and a drag back to it keep
  if (!list.length) return;

  // The index of the chapter that holds the thumb.
  const chapter = () => {
    const chars = (input.value * total) / 10000;
    let found = 0;
    while (found + 1 < list.length && list[found + 1][0] <= chars) found++;
    return found;
  };

  function show() {
    const text = `${list[chapter()][1]} · ${Math.round(input.value / 100)} %`;
    tip.textContent = text;
    tip.hidden = false;
    tip.style.setProperty("--ts-value", input.value / 100); // CSSOM: the CSP allows no style attribute
    input.setAttribute("aria-valuetext", text);
  }

  function hide() {
    tip.hidden = true;
  }

  function commit() {
    hide();
    if (input.value === start) return;
    const here = place();
    if (here && !sessionStorage.getItem(backKey)) sessionStorage.setItem(backKey, JSON.stringify([here.sid, here.off]));
    // ch is only the bar's guess (the offline reader opens a chapter at its start); bp is the place.
    go(`/books/${root.dataset.book}/read?ch=${chapter() + 1}&bp=${input.value}`);
  }

  // A pointer drag ends wherever the finger is lifted, so the end is watched on the window.
  function press() {
    start = input.value;
    addEventListener("pointerup", commit, { once: true });
  }

  input.addEventListener("input", show);
  input.addEventListener("pointerdown", press);
  input.addEventListener("focus", () => {
    input.step = 100; // an arrow key moves one percent
  });
  input.addEventListener("blur", () => {
    input.step = 1;
    input.value = start;
    hide();
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") commit();
    else if (event.key === "Escape") {
      input.value = start;
      hide();
    }
  });

  // The touch that loaded this module has already begun: catch up with it, and commit when it ends.
  if (document.activeElement === input) input.step = 100;
  if (input.value !== start) show();
  released?.then(commit);
}
