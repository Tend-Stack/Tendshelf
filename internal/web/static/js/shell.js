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

// Page enter without View Transitions: outerSync resets the class of #main to the response's, so
// adding ts-entering after each swap of #main restarts the keyframe every time.
document.addEventListener("htmx:after:swap", (event) => {
  if (document.startViewTransition) return;
  const target = event.detail && event.detail.ctx && event.detail.ctx.target;
  const main = document.getElementById("main");
  if (main && target === main) main.classList.add("ts-entering");
});

// Leaving sheets and toasts are removed when their exit animation ends.
document.addEventListener("animationend", (event) => {
  const el = event.target;
  if (!(el instanceof Element)) return;
  if (el.classList.contains("is-leaving")) el.remove();
  else if (el.classList.contains("ts-entering")) el.classList.remove("ts-entering");
});
