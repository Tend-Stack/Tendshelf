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

  const makeCanvas = (fill) => {
    const canvas = document.createElement("canvas");
    canvas.width = 1;
    canvas.height = 1;
    const ctx = canvas.getContext("2d", {willReadFrequently: true});
    return {ctx, fill};
  };
  const onWhite = makeCanvas("#ffffff");
  const onBlack = makeCanvas("#000000");
  const paint = (surface, css) => {
    surface.ctx.fillStyle = surface.fill;
    surface.ctx.fillRect(0, 0, 1, 1);
    surface.ctx.fillStyle = css;
    surface.ctx.fillRect(0, 0, 1, 1);
    return surface.ctx.getImageData(0, 0, 1, 1).data;
  };

  const colourCache = new Map();
  // rgba returns [r, g, b, a] (0..255, a 0..1) of a computed colour string.
  const rgba = (css) => {
    if (colourCache.has(css)) {
      return colourCache.get(css);
    }
    const w = paint(onWhite, css);
    const b = paint(onBlack, css);
    // over white: c*a + 255*(1-a); over black: c*a. The difference of a channel is 255*(1-a).
    const alpha = 1 - (w[1] - b[1]) / 255;
    const value = alpha <= 0.001 ? [0, 0, 0, 0] : [b[0] / alpha, b[1] / alpha, b[2] / alpha, alpha];
    colourCache.set(css, value);
    return value;
  };
  const over = (top, bottom) => {
    const a = top[3];
    return [top[0] * a + bottom[0] * (1 - a), top[1] * a + bottom[1] * (1 - a), top[2] * a + bottom[2] * (1 - a), 1];
  };
  const luminance = (c) => {
    const lin = (v) => {
      const s = v / 255;
      return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2]);
  };
  const ratio = (a, b) => {
    const la = luminance(a);
    const lb = luminance(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  };
  const hex = (c) => "#" + c.slice(0, 3).map((v) => Math.round(v).toString(16).padStart(2, "0")).join("");

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

  // backdrop walks from the element up to the first opaque background. It returns the colour that
  // is under the text and whether an image or a gradient may be part of it.
  const backdrop = (el) => {
    const layers = [];
    let picture = false;
    let opaque = null;
    for (let node = el; node; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.backgroundImage !== "none") {
        picture = true;
      }
      const colour = rgba(style.backgroundColor);
      if (colour[3] > 0) {
        layers.push(colour);
      }
      if (colour[3] >= 0.999) {
        opaque = node;
        break;
      }
    }
    let base = [255, 255, 255, 1];
    for (let i = layers.length - 1; i >= 0; i--) {
      base = over(layers[i], base);
    }
    return {colour: base, picture, opaque};
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
