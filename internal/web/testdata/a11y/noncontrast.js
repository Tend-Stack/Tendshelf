// Non-text contrast (WCAG 1.4.11) of one page in the theme that is set now: 3:1 for the boundary of a form
// control and for an icon that is the only content of a button or a link. Called as (this function)(options)
// with options {theme}. Returns {findings, unmeasured, measured}.
//
// contrast-control: the border colour of a field (the strongest side with a border of 1 px or more), or
// its background when it has none, against what is behind it.
// contrast-icon: a button or link with no visible text whose svg is drawn in currentColor, against
// what is behind it.
(options) => {
  const findings = [];
  const unmeasured = [];
  let measured = 0;
  const required = 3;
  const {rgba, over, ratio, hex, backdrop} = a11yColour();

  // clippedAway is true for an element that is 1 px or less and clips (the screen-reader-only pattern).
  const clippedAway = (el) => {
    for (let node = el; node && node !== document.body; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.overflow === "visible" && style.clip === "auto" && style.clipPath === "none") {
        continue;
      }
      const r = node.getBoundingClientRect();
      if (r.width <= 1 || r.height <= 1) {
        return true;
      }
    }
    return false;
  };

  const skipped = (el) => !a11yVisible(el) || el.closest("[inert],[aria-hidden=true]") || el.matches(":disabled,[aria-disabled=true]");

  const seen = new Set();
  const report = (el, got, detail, check) => {
    const selector = a11ySelector(el);
    const key = check + "|" + selector + "|" + detail;
    if (seen.has(key)) {
      return;
    }
    seen.add(key);
    findings.push({
      check, selector, text: a11yText(el) || el.getAttribute("aria-label") || "", detail,
      measured: Math.round(got * 100) / 100, required, mark: a11yCase(el),
    });
  };
  const leave = (el, why, check) => {
    const selector = a11ySelector(el);
    const key = "u|" + check + "|" + selector;
    if (!seen.has(key)) {
      seen.add(key);
      unmeasured.push({check, selector, text: a11yText(el), detail: why, mark: a11yCase(el)});
    }
  };

  // ---- form controls
  const fieldSelector = "input:not([type=hidden]):not([type=submit]):not([type=button]):not([type=reset]):not([type=image]),select,textarea,[role=switch]";
  for (const el of document.querySelectorAll(fieldSelector)) {
    if (skipped(el)) {
      continue;
    }
    const rect = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    if (rect.width <= 2 || rect.height <= 2 || Number(style.opacity) === 0 || clippedAway(el)) {
      leave(el, "drawn by another element", "contrast-control");
      continue;
    }
    const back = backdrop(el.parentElement || el);
    if (back.picture) {
      leave(el, "over an image", "contrast-control");
      continue;
    }
    const sides = ["Top", "Right", "Bottom", "Left"].filter((s) => parseFloat(style["border" + s + "Width"]) >= 1 && style["border" + s + "Style"] !== "none" && style["border" + s + "Style"] !== "hidden");
    let got;
    let detail;
    if (sides.length) {
      got = 0;
      for (const s of sides) {
        const flat = over(rgba(style["border" + s + "Color"]), back.colour);
        const r = ratio(flat, back.colour);
        if (r > got) {
          got = r;
          detail = "border " + hex(flat) + " on " + hex(back.colour);
        }
      }
    } else if (el.matches("input[type=checkbox],input[type=radio],input[type=range],input[type=file],input[type=color]") && style.appearance !== "none") {
      // A native control is drawn by the browser with its own boundary.
      leave(el, "native control", "contrast-control");
      continue;
    } else {
      const field = backdrop(el);
      const flat = field.colour;
      got = ratio(flat, back.colour);
      detail = "background " + hex(flat) + " on " + hex(back.colour) + " (no border)";
    }
    measured++;
    if (got < required) {
      report(el, got, detail, "contrast-control");
    }
  }

  // ---- icon-only buttons and links
  const visibleText = (el) => {
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (node.nodeValue.trim() !== "" && node.parentElement && !clippedAway(node.parentElement) && a11yVisible(node.parentElement)) {
        return true;
      }
    }
    return false;
  };
  for (const el of document.querySelectorAll("button,a[href],[role=button],[role=link]")) {
    if (skipped(el) || visibleText(el)) {
      continue;
    }
    const svg = [...el.querySelectorAll("svg")].find((s) => a11yVisible(s));
    if (!svg) {
      continue;
    }
    const colour = getComputedStyle(svg).color;
    const current = [svg, ...svg.querySelectorAll("*")].some((node) => {
      const style = getComputedStyle(node);
      return (style.fill !== "none" && style.fill === colour) || (style.stroke !== "none" && style.stroke === colour);
    });
    if (!current) {
      continue;
    }
    const back = backdrop(el);
    if (back.picture) {
      leave(el, "over an image", "contrast-icon");
      continue;
    }
    const flat = over(rgba(colour), back.colour);
    const got = ratio(flat, back.colour);
    measured++;
    if (got < required) {
      report(el, got, "icon " + hex(flat) + " on " + hex(back.colour), "contrast-icon");
    }
  }
  return {findings, unmeasured, measured};
}
