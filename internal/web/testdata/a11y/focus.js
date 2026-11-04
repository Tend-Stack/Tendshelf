// Keyboard focus, read by the Go walk (a11y_focus_test.go) one Tab at a time. Called as (this function)(options):
//   {op: "tabbables"}            -> [{id, selector}] the visible elements a Tab walk should reach
//   {op: "stop", themes: [...]}  -> what the focused element looks like, with the ring read in each theme
// Ids come from a WeakMap kept on window, so the same element has the same id in both calls.
(options) => {
  const colours = a11yColour();
  window.__a11yFocusIds = window.__a11yFocusIds || {map: new WeakMap(), next: 1};
  // A date field has several Tab stops inside it (day, month, year, the picker button).
  const dateLike = (el) => el.matches("input[type=date],input[type=time],input[type=datetime-local],input[type=month],input[type=week]");
  const idOf = (el) => {
    const ids = window.__a11yFocusIds;
    if (!ids.map.has(el)) {
      ids.map.set(el, ids.next++);
    }
    return ids.map.get(el);
  };

  if (options.op === "tabbables") {
    const list = [];
    const radios = new Map();
    const candidates = "a[href],button,input:not([type=hidden]),select,textarea,summary,[tabindex],[contenteditable=true]";
    for (const el of document.querySelectorAll(candidates)) {
      if (el.tabIndex < 0 || el.matches(":disabled") || el.closest("[inert]") || !a11yVisible(el)) {
        continue;
      }
      if (el.closest("details:not([open])") && !el.matches("details > summary:first-of-type")) {
        continue;
      }
      const r = el.getBoundingClientRect();
      if (r.width === 0 && r.height === 0 && getComputedStyle(el).position !== "fixed") {
        continue;
      }
      if (el.matches("input[type=radio][name]")) {
        // One stop per radio group: the checked one, or the first.
        const key = (el.form ? "f" : "d") + "|" + el.name;
        const group = [...document.querySelectorAll("input[type=radio]")].filter((x) => x.name === el.name && x.form === el.form);
        const stop = group.find((x) => x.checked) || group[0];
        if (radios.has(key) || stop !== el) {
          continue;
        }
        radios.set(key, true);
      }
      list.push({id: idOf(el), selector: a11ySelector(el), extra: dateLike(el) ? 5 : 0});
    }
    return list;
  }

  const el = document.activeElement;
  if (!el || el === document.body || el === document.documentElement) {
    return {none: true};
  }

  const rect = el.getBoundingClientRect();
  // An input drawn by something else (a visually hidden checkbox, a range under its track) shows focus on
  // the label or on a wrapper: the ring is looked for on the element, then on its labels and two ancestors.
  const indicators = [el];
  if (el.matches("input,select,textarea,summary")) {
    indicators.push(...(el.labels || []));
    if (el.parentElement) {
      indicators.push(el.parentElement);
      if (el.parentElement.parentElement) {
        indicators.push(el.parentElement.parentElement);
      }
    }
  }
  // The box that has to be seen: the element, or for an input drawn by something else the first label or
  // wrapper that has a real size.
  const hidden = el.localName === "input" && (Number(getComputedStyle(el).opacity) === 0 || rect.width <= 2 || rect.height <= 2);
  const visual = (hidden && indicators.slice(1).find((node) => {
    const r = node.getBoundingClientRect();
    return r.width > 4 && r.height > 4;
  })) || el;

  // rings lists the box-shadow rings and the outline of an element that are drawn outside it.
  const rings = (node) => {
    const style = getComputedStyle(node);
    const out = [];
    if (style.outlineStyle !== "none" && style.outlineStyle !== "hidden" && (style.outlineStyle === "auto" || parseFloat(style.outlineWidth) >= 2)) {
      // An "auto" outline is the browser's own two-tone ring, which is built to be seen on any background.
      out.push({kind: style.outlineStyle === "auto" ? "auto" : "outline", colour: style.outlineColor, width: parseFloat(style.outlineWidth)});
    }
    if (style.boxShadow && style.boxShadow !== "none") {
      // Computed box-shadow: "rgb(..) 0px 0px 0px 2px, rgb(..) 0px 0px 0px 4px" (colour first in Chromium).
      let depth = 0;
      let part = "";
      const parts = [];
      for (const ch of style.boxShadow) {
        if (ch === "(") {
          depth++;
        } else if (ch === ")") {
          depth--;
        }
        if (ch === "," && depth === 0) {
          parts.push(part);
          part = "";
        } else {
          part += ch;
        }
      }
      parts.push(part);
      for (const shadow of parts) {
        // An inset ring is drawn inside the element (a side menu item cannot grow a ring outwards); it is
        // read against the element's own background instead of what is behind the element.
        const inset = /\binset\b/.test(shadow);
        const lengths = (shadow.match(/-?[\d.]+px/g) || []).map(parseFloat);
        const colour = shadow.replace(/-?[\d.]+px/g, "").replace(/\binset\b/, "").trim();
        const spread = lengths[3] || 0;
        const blur = lengths[2] || 0;
        if (spread >= 2 && blur === 0) {
          out.push({kind: inset ? "inset" : "ring", colour, width: spread});
        }
      }
    }
    return out;
  };
  let found = [];
  for (const node of indicators) {
    found = rings(node);
    if (found.length) {
      break;
    }
  }

  // Five points (the centre and the four corners, inset 2 px) tell whether anything covers the element.
  const probe = visual.getBoundingClientRect();
  const hits = [];
  if (probe.width > 0 && probe.height > 0) {
    const inset = Math.min(2, probe.width / 2, probe.height / 2);
    for (const [x, y] of [
      [probe.left + probe.width / 2, probe.top + probe.height / 2],
      [probe.left + inset, probe.top + inset], [probe.right - inset, probe.top + inset],
      [probe.left + inset, probe.bottom - inset], [probe.right - inset, probe.bottom - inset],
    ]) {
      const hit = document.elementFromPoint(x, y);
      // A transparent label stretched over a card (a link-style row) is how the card is clicked, not a cover.
      hits.push(Boolean(hit) && ((hit.closest("label") && [...(el.labels || [])].includes(hit.closest("label"))) || visual.contains(hit) || el.contains(hit) || hit.contains(visual) && hit !== document.documentElement && hit !== document.body));
    }
  }
  const obscured = hits.length > 0 && !hits.some(Boolean);
  const offscreen = hits.length > 0 && hits.every((h) => !h) && (probe.bottom < 0 || probe.top > innerHeight || probe.right < 0 || probe.left > innerWidth);

  // The ring's contrast in each theme: the best of its colours against what is behind the element.
  const perTheme = {};
  const keep = document.documentElement.getAttribute("data-theme");
  for (const theme of options.themes) {
    document.documentElement.setAttribute("data-theme", theme);
    void document.body.offsetHeight;
    for (const animation of document.getAnimations()) {
      try { animation.finish(); } catch (error) { /* an infinite animation has no end */ }
    }
    void document.body.offsetHeight;
    const now = [];
    for (const node of indicators) {
      now.push(...rings(node));
      if (now.length) {
        break;
      }
    }
    if (!now.length) {
      perTheme[theme] = {ratio: 0, detail: "no ring"};
      continue;
    }
    if (now.some((r) => r.kind === "auto")) {
      perTheme[theme] = {ratio: 21, detail: "browser focus ring"};
      continue;
    }
    const outside = colours.backdrop(visual.parentElement || visual);
    const inside = colours.backdrop(visual);
    let best = 0;
    let bestColour = "";
    for (const r of now) {
      const c = colours.rgba(r.colour);
      const behind = r.kind === "inset" ? inside : outside;
      const flat = colours.over(c, behind.colour);
      const got = colours.ratio(flat, behind.colour);
      if (got > best || !bestColour) {
        best = got;
        bestColour = colours.hex(flat) + " on " + colours.hex(behind.colour) + (r.kind === "inset" ? " (inside)" : "") + (behind.picture ? " (image behind)" : "");
      }
    }
    perTheme[theme] = {ratio: Math.round(best * 100) / 100, detail: bestColour};
  }
  document.documentElement.setAttribute("data-theme", keep);
  void document.body.offsetHeight;
  for (const animation of document.getAnimations()) {
    try { animation.finish(); } catch (error) { /* an infinite animation has no end */ }
  }

  return {
    id: idOf(el), selector: a11ySelector(el), text: a11yText(el) || el.getAttribute("aria-label") || "",
    focusVisible: el.matches(":focus-visible"), ring: found.length > 0, obscured: obscured && !offscreen,
    offscreen, perTheme, mark: a11yCase(el),
    extra: dateLike(el) ? 6 : 0,
  };
}
