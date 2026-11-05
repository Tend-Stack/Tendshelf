// The footnote popup (M5 RD3), loaded on the first footnote link. The server sends the dialog with its words;
// this file opens it, closes it on Escape, the backdrop or Close, and gives focus back to the link.
// A search result opens with its words marked: a text range in the page's highlight registry (::highlight(ts-hit)).
export function hit(reader) {
  const query = new URLSearchParams(location.search);
  const home = reader.querySelector(`.ts-s[data-sid="${CSS.escape(query.get("at") || "")}"]`);
  if (!home) return;
  const words = document.createTreeWalker(home.closest("[data-c]"), NodeFilter.SHOW_TEXT);
  const texts = [];
  for (let node = words.nextNode(); node; node = words.nextNode()) texts.push(node);
  // The text node and the offset in it for a character offset in the block.
  const spot = (offset) => {
    for (const node of texts) {
      if (offset <= node.length) return [node, offset];
      offset -= node.length;
    }
  };
  const from = Number(query.get("off"));
  const found = new Range();
  found.setStart(...spot(from));
  found.setEnd(...spot(from + Number(query.get("hit"))));
  CSS.highlights.set("ts-hit", new Highlight(found));
}

export async function open(link, remember) {
  const res = await fetch(link.dataset.noteHref, { credentials: "same-origin" });
  if (!res.ok) throw new Error(res.status);
  const holder = document.createElement("template");
  holder.innerHTML = await res.text();
  const box = holder.content.querySelector("dialog");
  box.addEventListener("click", (event) => (event.target === box || event.target.closest("[data-close]")) && box.close());
  box.querySelector("a").addEventListener("click", remember); // "Go to the note" keeps the way back
  box.addEventListener("close", () => {
    box.remove();
    link.focus();
  });
  document.body.append(box);
  box.showModal();
}
