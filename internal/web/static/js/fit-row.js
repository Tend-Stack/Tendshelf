// A row of action buttons stays on ONE line. The buttons that do not fit move, from the last one backwards, into the
// row's three-dot menu (decision 0044). Without this file the row simply wraps, as it always did, so nothing is lost.
//
// Markup contract (see components.MoreMenu and docs/decisions/0044-action-rows-fit.md):
//   - the row carries data-ts-module="<this file>" and data-ts-fit;
//   - a button that may collapse (or its cell) carries data-ts-fit-item="<key>"; several elements may share a key
//     (the Download button and its status link), and the key stands for whichever of them is showing;
//   - the three-dot menu holds a hidden twin per key: <button data-ts-fit-for="<key>">;
//   - when the row itself is not a horizontal flex line (the book page's column at phone and tablet widths), the line
//     to measure is the descendant marked data-ts-fit-track.
// Elements without data-ts-fit-item (the play button, the three-dot button) never collapse.

const OFF = "data-ts-fit-off"; // CSS hides it (display none, so it also leaves the Tab order)

// The elements of every key, in page order.
function groupsOf(row) {
  const groups = new Map();
  for (const el of row.querySelectorAll("[data-ts-fit-item]")) {
    const key = el.dataset.tsFitItem;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(el);
  }
  return groups;
}

// A member that takes room in the row (downloads.js hides the one that does not apply).
const occupies = (el) => !el.hidden;
// The one that can be activated now: a Download button still deciding (data-ts-pending) is laid out but invisible.
const usable = (el) => !el.hidden && !el.hasAttribute("data-ts-pending");

// The control inside an item: the item itself when it is a link or button, else the first one inside it.
function controlOf(el) {
  return el.matches("a, button") ? el : el.querySelector("a[href], button");
}

// The flex items of a line, looking through display: contents wrappers and leaving out boxes that are not drawn.
function itemsOf(parent, out = []) {
  for (const child of parent.children) {
    const display = getComputedStyle(child).display;
    if (display === "none" || child.tagName === "P") continue;
    if (display === "contents") itemsOf(child, out);
    else out.push(child);
  }
  return out;
}

function overflows(line) {
  const items = itemsOf(line);
  if (items.length === 0) return false;
  let left = Infinity;
  let right = -Infinity;
  for (const item of items) {
    const box = item.getBoundingClientRect();
    left = Math.min(left, box.left);
    right = Math.max(right, box.right);
  }
  const style = getComputedStyle(line);
  const room = line.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
  return right - left > room + 1;
}

export function mount(row) {
  const menu = row.querySelector(".ts-moremenu");
  if (!menu) return;
  const summary = menu.querySelector("summary");
  const twinOf = (key) => menu.querySelector(`[data-ts-fit-for="${key}"]`);

  // The line to measure: the row, or the marked descendant when the row stacks its children.
  function lineOf() {
    const track = row.querySelector("[data-ts-fit-track]");
    return track && getComputedStyle(row).flexDirection.startsWith("column") ? track : row;
  }

  let fitting = false;
  function fit() {
    if (!row.isConnected) {
      stop();
      return;
    }
    fitting = true;
    const focused = document.activeElement;
    const groups = groupsOf(row);
    const line = lineOf();
    const setOff = (members, off) => members.forEach((el) => el.toggleAttribute(OFF, off));
    for (const members of groups.values()) setOff(members, false);

    // A menu that exists only for the twins is not drawn while everything fits.
    const onlyTwins = menu.hasAttribute("data-ts-fit-only");
    menu.hidden = onlyTwins;
    if (!onlyTwins || overflows(line)) {
      menu.hidden = false;
      const live = [...groups.values()].filter((members) => members.some(occupies));
      for (let i = live.length - 1; i >= 0 && overflows(line); i--) setOff(live[i], true);
    }

    // Twins: shown for what is collapsed, with the words and state of the control they stand for.
    for (const [key, members] of groups) {
      const twin = twinOf(key);
      if (!twin) continue;
      const off = members.some((el) => el.hasAttribute(OFF));
      const control = members.filter(usable).map(controlOf).find(Boolean);
      twin.hidden = !(off && control);
      if (control) {
        const label = control.querySelector(".ts-book__label");
        const words = label ? label.textContent.trim() : "";
        const slot = twin.querySelector("[data-ts-fit-label]");
        if (words && slot) slot.textContent = words;
        twin.disabled = control.disabled === true;
      }
    }

    // Focus that was on a control now folded away moves to the three-dot button.
    if (focused && focused.isConnected && focused.closest(`[${OFF}]`)) summary.focus();
    row.setAttribute("data-ts-fit-on", "");
    observer.takeRecords();
    fitting = false;
  }

  // A twin does what the control it stands for does: htmx, the form, the download script and the sheets run as ever.
  row.addEventListener("click", (event) => {
    const twin = event.target instanceof Element && event.target.closest("[data-ts-fit-for]");
    if (!twin) return;
    event.preventDefault();
    menu.open = false;
    const members = groupsOf(row).get(twin.dataset.tsFitFor) || [];
    const control = members.filter(usable).map(controlOf).find(Boolean);
    if (control) control.click();
  });

  const resize = new ResizeObserver(() => fit());
  // The download script and the player change what is showing; a text-size or language change changes the widths.
  const observer = new MutationObserver((records) => {
    if (fitting) return;
    if (records.some((r) => !r.target.closest("[data-ts-fit-for]") && r.target !== menu)) fit();
  });
  const page = new MutationObserver(() => fit());
  function stop() {
    resize.disconnect();
    observer.disconnect();
    page.disconnect();
  }

  resize.observe(row);
  const track = row.querySelector("[data-ts-fit-track]");
  if (track) resize.observe(track);
  observer.observe(row, { subtree: true, attributes: true, attributeFilter: ["hidden", "data-ts-pending", "disabled"] });
  page.observe(document.documentElement, { attributes: true, attributeFilter: ["data-text-size", "lang", "class"] });
  fit();
  document.fonts?.ready.then(() => row.isConnected && fit());
}
