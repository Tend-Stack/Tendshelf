// Structure checks of one page. Called as (this function)(options) with options {shell: bool}.
// Returns [{check, selector, text, detail, mark}]; mark is the fixture case (empty on real pages).
(options) => {
  const findings = [];
  const add = (check, el, detail) => {
    findings.push({
      check,
      selector: el ? a11ySelector(el) : "document",
      text: el ? a11yText(el) : "",
      detail: detail || "",
      mark: el ? a11yCase(el) : "",
    });
  };
  const root = document.documentElement;

  if (!/^[a-z]{2}/i.test(root.getAttribute("lang") || "")) {
    add("lang", null, "html lang is \"" + (root.getAttribute("lang") || "") + "\"");
  }
  if (document.title.trim() === "") {
    add("title", null, "the page has no title");
  }

  // heading-skip: a heading more than one level deeper than the one before it
  let previous = 0;
  for (const h of document.querySelectorAll("h1, h2, h3, h4, h5, h6")) {
    if (!a11yVisible(h)) {
      continue;
    }
    const level = Number(h.localName[1]);
    if (previous > 0 && level - previous > 1) {
      add("heading-skip", h, "h" + previous + " is followed by h" + level);
    }
    previous = level;
  }

  if (options.shell) {
    const navs = [...document.querySelectorAll("nav, [role=navigation]")].filter(a11yVisible);
    if (navs.length === 0) {
      add("nav-missing", null, "no visible navigation landmark");
    }
    const toasts = document.getElementById("toasts");
    const live = toasts && (toasts.getAttribute("aria-live") || /^(status|alert)$/.test(toasts.getAttribute("role") || ""));
    if (!live) {
      add("live-region-missing", toasts, "the toast container is not a live region");
    }
  }

  for (const img of document.querySelectorAll("img")) {
    if (!img.hasAttribute("alt")) {
      add("img-alt", img, "img without an alt attribute");
    }
  }

  const skipTypes = new Set(["hidden", "submit", "button", "reset", "image"]);
  for (const field of document.querySelectorAll("input, select, textarea")) {
    if (field.localName === "input" && skipTypes.has(field.type)) {
      continue;
    }
    if (!a11yVisible(field)) {
      continue;
    }
    const named = field.labels.length > 0 || field.hasAttribute("aria-label") || field.hasAttribute("aria-labelledby");
    if (!named) {
      add("label-missing", field, field.localName + " has no label");
    }
  }

  const seen = new Map();
  for (const el of document.querySelectorAll("[id]")) {
    if (seen.has(el.id)) {
      add("duplicate-id", el, "id \"" + el.id + "\" is used more than once");
    } else {
      seen.set(el.id, el);
    }
  }

  const references = [["label[for]", "for"], ["[aria-labelledby]", "aria-labelledby"], ["[aria-describedby]", "aria-describedby"], ["[aria-controls]", "aria-controls"]];
  for (const [selector, attribute] of references) {
    for (const el of document.querySelectorAll(selector)) {
      for (const id of el.getAttribute(attribute).split(/\s+/).filter(Boolean)) {
        if (!document.getElementById(id)) {
          add("aria-ref-broken", el, attribute + " names a missing id \"" + id + "\"");
        }
      }
    }
  }

  const focusable = "a[href], button, input:not([type=hidden]), select, textarea, summary, [tabindex], [contenteditable=true]";
  const hiddenScope = "[aria-hidden=true] :is(" + focusable + "), [aria-hidden=true]:is(" + focusable + ")";
  for (const el of document.querySelectorAll(hiddenScope)) {
    if (el.getAttribute("tabindex") === "-1" || el.disabled || el.closest("[inert]") || !a11yVisible(el)) {
      continue;
    }
    add("aria-hidden-focusable", el, "focusable inside aria-hidden");
  }

  const viewport = document.querySelector("meta[name=viewport]");
  if (viewport) {
    const content = viewport.getAttribute("content") || "";
    const max = /maximum-scale\s*=\s*([\d.]+)/.exec(content);
    if (/user-scalable\s*=\s*(no|0)/.test(content) || (max && Number(max[1]) < 2)) {
      add("viewport-zoom", viewport, content);
    }
  }

  const tokens = (el) => (el.getAttribute("autocomplete") || "").split(/\s+/);
  for (const field of document.querySelectorAll("input")) {
    if (field.type === "password" && !tokens(field).some((t) => t === "current-password" || t === "new-password")) {
      add("autocomplete-missing", field, "password field without current-password or new-password");
    }
    const identity = field.type === "email" || /^(username|email|user)$/i.test(field.name) || /^(username|email|user)$/i.test(field.id);
    if (identity && field.type !== "password" && !tokens(field).some((t) => t === "username" || t === "email")) {
      add("autocomplete-missing", field, "username or email field without autocomplete");
    }
  }
  return findings;
}
