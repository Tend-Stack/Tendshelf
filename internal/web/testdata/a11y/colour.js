// Colour helpers shared by the contrast scripts (contrast.js, noncontrast.js, focus.js). a11yColour() returns
// {rgba, over, ratio, hex, backdrop}; colours are [r, g, b, a] with r, g, b in 0..255 and a in 0..1. They are read
// through two canvases (one white, one black behind the colour), which gives the exact sRGB value and alpha of any
// CSS colour the browser understands, color-mix() included.
function a11yColour() {
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
  return {rgba, over, ratio, hex, backdrop};
}
