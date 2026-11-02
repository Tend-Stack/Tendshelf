// The Now playing panel (desktop): loads it, fills the top, keeps the tabs.
const WIDE = matchMedia("(min-width: 1280px)");
const $ = (selector) => document.querySelector(selector);
const dialog = $("#player-full");

// Loads the loaded book's panel when the player opens wide.
function load() {
  const id = tsPlayer?.state().bookId;
  if (!WIDE.matches || document.documentElement.dataset.textSize === "large" || !dialog.open || !id || $("#now-panel").dataset.bookId === id) return;
  htmx.ajax("GET", "/hx/player/panel/" + id, { target: "#now-panel", swap: "outerHTML" });
}
new MutationObserver(load).observe(dialog, { attributes: true, attributeFilter: ["open"] });
document.addEventListener("ts:player:state", load);

// The narrator joins the subtitle, the extra links join the ⋯ menu.
document.addEventListener("htmx:after:swap", () => {
  if (!$("#now-panel[data-book-id]")) return;
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
  const other = [...tab.parentElement.children].find((t) => t !== tab);
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
