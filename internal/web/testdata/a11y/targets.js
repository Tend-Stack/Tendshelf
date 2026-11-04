// Touch-target size of one page. Called as (this function)(options) with options {min}, the smallest
// width and height in CSS px for this viewport (44 on the touch layouts, 24 at 1440: a11yTargetMin).
// Returns {findings, measured}. A finding is a target smaller than min on either side, with detail
// "WxH" and wcag258 telling whether it still meets the WCAG 2.5.8 floor of 24 px.
//
// A hit area grown by a pseudo-element counts: when a box is short, the points min/2 away from its centre
// on the short axis are probed with elementFromPoint, and the target passes when each one lands on the
// element or on something inside it.
(options) => {
  const min = options.min;
  const findings = [];
  let measured = 0;

  const targetSelector = [
    "a[href]", "button", "input:not([type=hidden])", "select", "textarea", "summary",
    "[role=button]", "[role=link]", "[role=tab]", "[role=switch]", "[role=checkbox]", "[role=radio]",
    "[role=menuitem]", "[role=option]", "[role=slider]", "[tabindex]:not([tabindex=\"-1\"])",
  ].join(",");

  // hiddenControl is true for a checkbox, radio or file input drawn by something else: transparent, clipped
  // to a point, or a point wide. Its label is the target then.
  const hiddenControl = (el, rect) => {
    if (el.localName !== "input") {
      return false;
    }
    const style = getComputedStyle(el);
    return Number(style.opacity) === 0 || rect.width <= 2 || rect.height <= 2 || style.clip !== "auto" && style.position === "absolute";
  };

  // inlineLink is true for a link inside a sentence: an inline box whose block has other text around it.
  // WCAG 2.5.8 exempts those (and so does this audit).
  const inlineLink = (el) => {
    if (el.localName !== "a" || getComputedStyle(el).display !== "inline") {
      return false;
    }
    let block = el.parentElement;
    while (block && getComputedStyle(block).display === "inline") {
      block = block.parentElement;
    }
    if (!block) {
      return false;
    }
    const outside = (block.textContent || "").trim().length - (el.textContent || "").trim().length;
    return outside > 1;
  };

  const seen = new Set();
  for (const el of document.querySelectorAll(targetSelector)) {
    if (el.closest("[inert],[aria-hidden=true]") || el.matches(":disabled,[aria-disabled=true]")) {
      continue;
    }
    if (!a11yVisible(el) || el.closest("details:not([open])") && !el.matches("details > summary:first-of-type")) {
      continue;
    }
    let box = el;
    let rect = el.getBoundingClientRect();
    if (hiddenControl(el, rect)) {
      const label = (el.labels && el.labels[0]) || el.closest("label");
      if (!label) {
        continue;
      }
      box = label;
      rect = label.getBoundingClientRect();
    }
    if (rect.width === 0 || rect.height === 0 || inlineLink(el)) {
      continue;
    }
    measured++;
    let width = rect.width;
    let height = rect.height;
    let ok = width >= min - 0.5 && height >= min - 0.5;
    if (!ok) {
      // Grown by a pseudo-element? Probe the short axis from the centre.
      box.scrollIntoView({block: "center", inline: "center"});
      const r = box.getBoundingClientRect();
      const cx = r.left + r.width / 2;
      const cy = r.top + r.height / 2;
      const reach = min / 2 - 0.5;
      const points = [];
      if (r.width < min - 0.5) {
        points.push([cx - reach, cy], [cx + reach, cy]);
      }
      if (r.height < min - 0.5) {
        points.push([cx, cy - reach], [cx, cy + reach]);
      }
      ok = points.every(([x, y]) => {
        const hit = document.elementFromPoint(x, y);
        return hit && (hit === el || box.contains(hit));
      });
    }
    if (ok) {
      continue;
    }
    const selector = a11ySelector(el);
    const detail = Math.round(width) + "x" + Math.round(height);
    const key = selector + "|" + detail;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    findings.push({
      check: "target-size", selector, text: a11yText(el) || el.getAttribute("aria-label") || "", detail,
      measured: Math.round(Math.min(width, height) * 10) / 10, required: min,
      wcag258: width >= 24 && height >= 24, mark: a11yCase(el),
    });
  }
  window.scrollTo(0, 0);
  return {findings, measured};
}
