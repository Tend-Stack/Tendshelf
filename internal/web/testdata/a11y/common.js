// Shared helpers for the audit scripts. The Go side puts this text in front of structure.js and
// contrast.js, and uses a11ySelector alone to name a node found through the accessibility tree.

// a11yVisible is true for an element that is rendered (not display:none, not visibility:hidden).
function a11yVisible(el) {
  return el.checkVisibility({visibilityProperty: true});
}

// a11yPart names one element: its id, or its tag with up to two classes.
function a11yPart(el) {
  if (el.id) {
    return "#" + el.id;
  }
  const classes = [...el.classList].slice(0, 2);
  return el.localName + classes.map((c) => "." + c).join("");
}

// a11ySelector names an element for a finding: "#id", else the tag and up to two classes, with up to
// four ancestors in front, joined by ">". It stops at the first ancestor that has an id.
function a11ySelector(el) {
  if (el.id) {
    return "#" + el.id;
  }
  const parts = [];
  let node = el;
  for (let i = 0; node && node.nodeType === 1 && i < 5; i++) {
    parts.unshift(a11yPart(node));
    if (node.id) {
      break;
    }
    node = node.parentElement;
  }
  return parts.join(">");
}

// a11yCase is the planted case a fixture element belongs to (empty on the real pages).
function a11yCase(el) {
  const holder = el.closest("[data-case]");
  return holder ? holder.dataset.case : "";
}

function a11yText(el) {
  return (el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40);
}
