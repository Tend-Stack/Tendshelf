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
