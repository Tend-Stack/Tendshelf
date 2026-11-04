// Text contrast of one page in the theme that is set now. Called as (this function)(options) with
// options {theme}. Returns {findings, unmeasured, measured}; a finding is one text colour pair that is
// too weak, "unmeasured" lists text over an image, a video or a gradient.
//
// Colours are read through two canvases (one white, one black behind the colour): that gives the
// exact sRGB value and alpha of any CSS colour the browser understands, color-mix() included.
(options) => {
  const findings = [];
  const unmeasured = [];
  let measured = 0;
  const contrastTheme = options.theme === "contrast";

  const {rgba, over, ratio, hex, backdrop} = a11yColour();


  // clippedAway is true for text inside a box of 1 px or less that clips (the screen-reader-only pattern).
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


  const mediaTags = new Set(["IMG", "VIDEO", "CANVAS", "PICTURE", "SVG"]);
  // pictureUnder is true when an image, a video or a gradient is painted above the first opaque
  // background at the centre of the text.
  const pictureUnder = (el, rect, opaque) => {
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    if (x < 0 || y < 0 || x >= innerWidth || y >= innerHeight) {
      return false;
    }
    for (const hit of document.elementsFromPoint(x, y)) {
      if (hit === opaque) {
        return false;
      }
      // The text's own element and its ancestors were already read by backdrop().
      if (hit.contains(el)) {
        continue;
      }
      if (mediaTags.has(hit.tagName.toUpperCase())) {
        return true;
      }
      if (getComputedStyle(hit).backgroundImage !== "none") {
        return true;
      }
    }
    return false;
  };

  const seen = new Set();
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (node.nodeValue.trim() === "") {
      continue;
    }
    const el = node.parentElement;
    if (!el || ["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "OPTION"].includes(el.tagName)) {
      continue;
    }
    if (!el.checkVisibility({opacityProperty: true, visibilityProperty: true})) {
      continue;
    }
    range.selectNodeContents(node);
    const rect = range.getBoundingClientRect();
    if (rect.width <= 1 || rect.height <= 1) {
      continue;
    }
    if (el.closest(":disabled, [aria-disabled=true], [inert]") || clippedAway(el)) {
      continue;
    }

    const style = getComputedStyle(el);
    let opacity = 1;
    for (let n = el; n; n = n.parentElement) {
      opacity *= Number(getComputedStyle(n).opacity);
    }
    const text = [...rgba(style.color)]; // a copy: the cache is shared
    text[3] *= opacity;
    const back = backdrop(el);
    const selector = a11ySelector(el);
    if (back.picture || pictureUnder(el, rect, back.opaque)) {
      const key = "u|" + selector;
      if (!seen.has(key)) {
        seen.add(key);
        unmeasured.push({check: "contrast-text", selector, text: a11yText(el), detail: "over an image", mark: a11yCase(el)});
      }
      continue;
    }
    const fg = over(text, back.colour);
    const size = parseFloat(style.fontSize);
    const weight = Number(style.fontWeight);
    const large = size >= 24 || (size >= 18.66 && weight >= 700);
    const required = contrastTheme ? (large ? 4.5 : 7) : (large ? 3 : 4.5);
    const got = ratio(fg, back.colour);
    measured++;
    if (got >= required) {
      continue;
    }
    const detail = hex(fg) + " on " + hex(back.colour) + " " + Math.round(size * 100) / 100 + "px " + weight;
    const key = selector + "|" + detail;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    findings.push({
      check: "contrast-text", selector, text: a11yText(el), detail,
      measured: Math.round(got * 100) / 100, required, mark: a11yCase(el),
    });
  }
  return {findings, unmeasured, measured};
}
