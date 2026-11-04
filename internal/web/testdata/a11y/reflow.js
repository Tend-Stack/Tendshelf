// Reflow of one page: does it scroll sideways, and does any box cut its text off? Called as
// (this function)(options) with options {width}, the width of the window the tab was set up with, and returns {scrollX, clips, truncated}. The Go side names the findings
// (scroll-x, reflow-320, text-200-scroll-x, text-200-clip) from the viewport it set up.
//   scrollX    null, or {selector, detail}: the page is wider than the window (the widest box that sticks out
//              and is not inside a scroller of its own is named)
//   clips      boxes with overflow hidden or clip whose text does not fit
//   truncated  the same, when the box truncates on purpose (text-overflow: ellipsis, line-clamp)
(options) => {
  // innerWidth is not asked: a phone-style window grows to the width of its content, so a page that
  // is too wide would measure itself as fitting.
  const limit = options.width;
  const wide = document.scrollingElement.scrollWidth;
  const out = {scrollX: null, clips: [], truncated: []};
  if (wide > limit + 1) {
    let culprit = null;
    let farthest = limit + 1;
    for (const el of document.body.querySelectorAll("*")) {
      if (!a11yVisible(el)) {
        continue;
      }
      const r = el.getBoundingClientRect();
      if (r.right <= farthest || r.width === 0) {
        continue;
      }
      let scrolls = false;
      for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) {
        const style = getComputedStyle(node);
        if (style.overflowX !== "visible") {
          scrolls = true;
          break;
        }
      }
      if (!scrolls && getComputedStyle(el).position !== "fixed") {
        culprit = el;
        farthest = r.right;
      }
    }
    out.scrollX = {
      selector: culprit ? a11ySelector(culprit) : "document",
      detail: "scrollWidth " + wide + " > " + limit + (culprit ? ", " + Math.round(farthest) + "px right edge" : ""),
      text: culprit ? a11yText(culprit) : "",
      mark: culprit ? a11yCase(culprit) : "",
    };
  }

  const seen = new Set();
  for (const el of document.body.querySelectorAll("*")) {
    const style = getComputedStyle(el);
    if (!/hidden|clip/.test(style.overflowX + style.overflowY) || style.display === "inline" || !a11yVisible(el)) {
      continue;
    }
    if (!(el.textContent || "").trim()) {
      continue;
    }
    const r = el.getBoundingClientRect();
    if (r.width <= 1 || r.height <= 1) {
      continue; // the screen-reader-only pattern
    }
    const sideways = el.scrollWidth - el.clientWidth > 1 && /hidden|clip/.test(style.overflowX);
    const down = el.scrollHeight - el.clientHeight > 1 && /hidden|clip/.test(style.overflowY);
    if (!sideways && !down) {
      continue;
    }
    const selector = a11ySelector(el);
    const detail = "content " + el.scrollWidth + "x" + el.scrollHeight + " in a box of " + el.clientWidth + "x" + el.clientHeight;
    if (seen.has(selector + detail)) {
      continue;
    }
    seen.add(selector + detail);
    const finding = {selector, text: a11yText(el), detail, mark: a11yCase(el)};
    const clamp = style.webkitLineClamp && style.webkitLineClamp !== "none";
    if (style.textOverflow === "ellipsis" || clamp) {
      finding.detail += " (truncated on purpose)";
      out.truncated.push(finding);
    } else {
      out.clips.push(finding);
    }
  }
  return out;
}
