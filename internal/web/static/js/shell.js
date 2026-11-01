// Tendshelf app shell. The navigation sits outside #main, so a boosted swap does not
// re-render it: after each swap of #main, mark the current destination and move focus
// to the new content. htmx 4 event names use colons.
function isCurrent(href, path) {
  return href === "/" ? path === "/" : path === href || path.startsWith(href + "/");
}

document.addEventListener("htmx:after:swap", (event) => {
  const target = event.detail && event.detail.ctx && event.detail.ctx.target;
  const main = document.getElementById("main");
  if (!main || (target && target !== main && !main.contains(target))) return;
  const path = location.pathname;
  for (const link of document.querySelectorAll("nav [data-nav]")) {
    if (isCurrent(link.dataset.nav, path)) {
      link.setAttribute("aria-current", "page");
    } else {
      link.removeAttribute("aria-current");
    }
  }
  main.focus({ preventScroll: true });
});

// Progress values: data-ts-value (0 to 100) becomes the --ts-value custom property through the
// CSSOM, which the CSP allows (an inline style attribute would not be). Without JS the fill is 0.
function applyValues(root) {
  for (const el of root.querySelectorAll("[data-ts-value]")) {
    const v = Math.min(100, Math.max(0, Number.parseFloat(el.dataset.tsValue) || 0));
    el.style.setProperty("--ts-value", String(v));
  }
}
applyValues(document);
document.addEventListener("htmx:after:settle", () => applyValues(document));

// Cover tints: data-ts-tint ("#rrggbb", checked by the server for readable ink) becomes the
// --cover-tint custom property through the CSSOM, and .is-tinted switches the spine or blank cover
// from surface and ink to the tint. Without JS, or in the contrast theme, the tint is not used.
const tintPattern = /^#[0-9a-f]{6}$/;
function applyTints(root) {
  for (const el of root.querySelectorAll("[data-ts-tint]")) {
    const tint = el.dataset.tsTint;
    if (!tintPattern.test(tint)) continue;
    el.style.setProperty("--cover-tint", tint);
    el.classList.add("is-tinted");
  }
}
applyTints(document);
document.addEventListener("htmx:after:settle", () => applyTints(document));

// Page enter without View Transitions: outerSync resets the class of #main to the response's, so
// adding ts-entering after each swap of #main restarts the keyframe every time.
document.addEventListener("htmx:after:swap", (event) => {
  if (document.startViewTransition) return;
  const target = event.detail && event.detail.ctx && event.detail.ctx.target;
  const main = document.getElementById("main");
  if (main && target === main) main.classList.add("ts-entering");
});

// Sheets live in #sheet, outside #main. Opening one (a swap into #sheet, or a full page load with
// the sheet in it) focuses it; closing plays the exit motion, then returns focus to the opener.
let opener = null;
function focusSheet(source) {
  const sheet = document.querySelector("#sheet .ts-sheet");
  if (!sheet) return;
  opener = source || null;
  sheet.focus();
}
function closeSheet() {
  const layer = document.querySelector("#sheet .ts-sheet-layer");
  if (!layer) return false;
  for (const el of layer.querySelectorAll(".ts-sheet, .ts-scrim")) el.classList.add("is-leaving");
  return true;
}
focusSheet(null);
document.addEventListener("htmx:after:swap", (event) => {
  const ctx = event.detail && event.detail.ctx;
  if (ctx && ctx.target && ctx.target.id === "sheet") focusSheet(ctx.sourceElement);
});
document.addEventListener("click", (event) => {
  if (event.target.closest && event.target.closest("[data-ts-close]") && closeSheet()) event.preventDefault();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && closeSheet()) event.preventDefault();
});

// Leaving sheets and toasts are removed when their exit animation ends (a sheet takes its layer along).
document.addEventListener("animationend", (event) => {
  const el = event.target;
  if (!(el instanceof Element)) return;
  if (el.classList.contains("is-leaving")) {
    const layer = el.classList.contains("ts-sheet") && el.closest(".ts-sheet-layer");
    (layer || el).remove();
    if (layer && opener && opener.isConnected) opener.focus();
  } else if (el.classList.contains("ts-entering")) el.classList.remove("ts-entering");
});
