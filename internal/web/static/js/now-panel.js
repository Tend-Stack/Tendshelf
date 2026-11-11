// The Now playing panel (desktop): loads it, fills the top, keeps the tabs.
const WIDE = matchMedia("(width >= 1280px)");
const $ = (selector) => document.querySelector(selector);
const dialog = $("#player-full");

// Loads the loaded book's or episode's panel when the player opens wide.
function load() {
  const kind = tsPlayer?.state().itemKind;
  const id = kind === "radio" ? "" : tsPlayer?.state().bookId; // a station has no panel
  if (!WIDE.matches || document.documentElement.dataset.textSize === "large" || !dialog.open || !id || $("#now-panel").dataset.bookId === id) return;
  const path = kind === "episode" ? "/hx/player/episode-panel/" : "/hx/player/panel/";
  htmx.ajax("GET", path + id, { target: "#now-panel", swap: "outerHTML" });
}
new MutationObserver(load).observe(dialog, { attributes: true, attributeFilter: ["open"] });
document.addEventListener("ts:player:state", load);

// The narrator joins the subtitle, the extra links join the ⋯ menu.
document.addEventListener("htmx:after:swap", () => {
  if (!$("#now-panel[data-book-id]:not(.ts-np--episode)")) return; // an episode has no narrator or extra links
  const by = $("[data-ts-now-by]");
  $("[data-ts-full-by]").textContent = by ? " · " + by.textContent : "";
  $("[data-ts-full-extra]").replaceChildren(...($("[data-ts-now-menu]")?.children ?? []));
});

// Tabs: one in the tab order; arrows move to the other.
document.addEventListener("click", (event) => {
  const tab = event.target.closest(".ts-np__tabbtn");
  if (!tab) return;
  for (const t of tab.parentElement.children) {
    t.setAttribute("aria-selected", t === tab);
    t.tabIndex = t === tab ? 0 : -1;
  }
  $("#now-panel-tab").setAttribute("aria-labelledby", tab.id);
});
document.addEventListener("keydown", (event) => {
  const tab = event.target.closest(".ts-np__tabbtn");
  if (!tab || !/^Arrow[LR]/.test(event.key)) return;
  // arrows move to the next or previous one, wrapping round
  const list = tab.parentElement;
  const other =
    event.key === "ArrowRight"
      ? tab.nextElementSibling || list.firstElementChild
      : tab.previousElementSibling || list.lastElementChild;
  other.focus();
  other.click();
  event.preventDefault();
});

// A bar bookmark refreshes the tab or the count.
const marked = new MutationObserver(() => {
  const id = $("#now-panel").dataset.bookId;
  if (!id || !$("[data-ts-toast-link]")) return;
  if ($("#now-tab-bookmarks[aria-selected=true]")) $("#now-tab-bookmarks").click();
  else htmx.ajax("GET", "/hx/player/panel/" + id + "/count", { target: "#now-panel-count", swap: "outerHTML" });
});
for (const box of document.querySelectorAll("#toasts, [data-ts-full-toasts]")) marked.observe(box, { childList: true });

// Up next: when a row moves or one is removed, the rows glide to their new places instead of jumping (FLIP).
// Before the swap each row's place is noted by its id; after it, a row that sits elsewhere starts at its old
// place and animates to its new one with the Web Animations API (no inline style). Reduced motion: no movement.
const REDUCED = matchMedia("(prefers-reduced-motion: reduce)");
const rowPlaces = new Map(); // the page lists rows in a grid on wide screens, so a row can move sideways too
const rows = () => document.querySelectorAll(".ts-upnext__item[id]");
document.addEventListener("htmx:before:swap", (event) => {
  rowPlaces.clear();
  if (!event.detail.ctx?.target?.classList.contains("ts-upnext") || REDUCED.matches) return;
  for (const row of rows()) {
    const box = row.getBoundingClientRect();
    rowPlaces.set(row.id, [box.left, box.top]);
  }
});
document.addEventListener("htmx:after:swap", () => {
  if (rowPlaces.size === 0) return;
  const style = getComputedStyle(document.documentElement);
  for (const row of rows()) {
    const was = rowPlaces.get(row.id);
    if (!was) continue;
    const box = row.getBoundingClientRect();
    const dx = was[0] - box.left;
    const dy = was[1] - box.top;
    if (!dx && !dy) continue;
    row.animate({ translate: [dx + "px " + dy + "px", "0 0"] }, { duration: 200, easing: style.getPropertyValue("--easing-standard") || "ease" });
  }
  rowPlaces.clear();
});
