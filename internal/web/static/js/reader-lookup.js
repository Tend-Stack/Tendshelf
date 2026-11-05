// Look-up (M5 G1), loaded by reader-marks.js on the first "Look up" in the selection toolbar. The server answers
// with a dialog holding the definitions, the web search link and a Save word button; every word in it is server
// text. This file only picks the word, opens the dialog the way reader-notes.js does (Escape, the backdrop and
// Close close it) and sends the reader's hidden word form when Save word is pressed.

// Letters and digits only at both ends: "“Serene,”" is looked up as "Serene".
const trim = (text) => text.trim().replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, "");

// The word to look up: the selection, or its first word when it is long or runs over a sentence.
function pick(marked) {
  let word = trim(marked.text);
  if ([...word].length > 64 || marked.start !== marked.end) word = trim(marked.text.trim().split(/\s+/)[0]);
  return [...word].slice(0, 64).join("");
}

export async function open(reader, marked) {
  const copy = JSON.parse(reader.dataset.copy || "{}");
  const word = pick(marked);
  if (!word) return;
  const query = new URLSearchParams({ q: word, edition: reader.dataset.edition });
  let res;
  try {
    res = await fetch(`/hx/books/${reader.dataset.book}/read/lookup?${query}`, { credentials: "same-origin" });
  } catch {
    return window.tsPlayer?.toast("hint", copy.needsNet); // offline: look-up needs the server
  }
  if (!res.ok) return window.tsPlayer?.toast("hint", copy.failed);
  const holder = document.createElement("template");
  holder.innerHTML = await res.text();
  const box = holder.content.querySelector("dialog");
  box.addEventListener("click", (event) => {
    if (event.target === box || event.target.closest("[data-close]")) box.close();
    if (!event.target.closest("[data-save]")) return;
    const form = document.getElementById("reader-word-form");
    form.elements.word.value = word;
    form.elements.quote.value = marked.start.textContent;
    form.elements.sid.value = marked.start.dataset.sid;
    form.elements.off.value = marked.startOff + Math.max(0, marked.text.indexOf(word));
    form.requestSubmit();
    box.close();
  });
  box.addEventListener("close", () => {
    box.remove();
    marked.start.tabIndex = -1; // the toolbar is gone: focus goes back to where the word was
    marked.start.focus({ preventScroll: true });
  });
  document.body.append(box);
  box.showModal();
}
