// Small progressive enhancements for invitations (P3-12). Without this script the invite link and
// the message are still selectable, read-only text, and "Open in my email app" is a plain link.
//
//   [data-copy="#id"]    a Copy button; it starts hidden and is shown here, because it does nothing
//                        without script. It copies the value of the element it names.
//   [data-mailto-auto]   the "Open in my email app" link: opened once, when the answer to "Send
//                        invitation" lands. If the browser refuses, the link stays on the page.

function revealCopyButtons(root) {
  for (const button of root.querySelectorAll("button[data-copy][hidden]")) {
    button.hidden = false;
  }
}

async function copyText(text, field) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (error) {
      // Refused (permissions policy, no focus): fall back to selecting the text.
    }
  }
  field.focus();
  field.select();
  try {
    return document.execCommand("copy");
  } catch (error) {
    return false;
  }
}

document.addEventListener("click", async (event) => {
  const button = event.target.closest("button[data-copy]");
  if (!button) {
    return;
  }
  const field = document.querySelector(button.dataset.copy);
  if (!field) {
    return;
  }
  const done = await copyText(field.value, field);
  // The live region sits in the same panel; its text changes, so a screen reader says it.
  const live = button.closest("[role=status]")?.querySelector("[data-copy-status]");
  if (live) {
    live.textContent = done ? "Copied" : "Select the text and copy it.";
  }
  if (!done) {
    field.focus();
    field.select();
  }
});

function openEmailApp(root) {
  const link = root.querySelector("a[data-mailto-auto]");
  if (!link || link.dataset.opened) {
    return;
  }
  link.dataset.opened = "1";
  location.assign(link.href);
}

function enhance(root) {
  revealCopyButtons(root);
  openEmailApp(root);
}

enhance(document);
document.addEventListener("htmx:after:settle", () => enhance(document));
