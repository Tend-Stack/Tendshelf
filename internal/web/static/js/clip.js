// Stops a clip at its end. A clip is a bookmark with an end; its "Play this clip" button carries
// data-ts-clip-end (the end in ms) next to the data-ts-play-at and data-ts-book that player.js
// already handles. The Bookmarks tab loads this file only when it shows a clip. Without it a clip
// simply plays on past its end.

const audio = document.getElementById("audio");
const SLACK_MS = 1000; // how far past an edge still counts as playing through it, not a seek

let armed = null; // { bookId, start, end, engaged }

// Runs after player.js's own click listener (registered first), which has already started the book.
document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-ts-clip-end]");
  if (!button) return;
  armed = {
    bookId: button.dataset.tsBook,
    start: Number(button.dataset.tsPlayAt) || 0,
    end: Number(button.dataset.tsClipEnd) || 0,
    engaged: false,
  };
});

audio?.addEventListener("timeupdate", () => {
  if (!armed || !window.tsPlayer) return;
  const state = window.tsPlayer.state();
  if (state.bookId !== armed.bookId) {
    // Loading the clip's book may still be under way; once it was playing, another book ends the clip.
    if (armed.engaged) armed = null;
    return;
  }
  const at = state.positionMs;
  if (!armed.engaged) {
    // Wait until the seek to the start has landed; a place far from the clip is the old position.
    armed.engaged = at >= armed.start - SLACK_MS && at < armed.end;
    return;
  }
  if (at < armed.start - SLACK_MS || at > armed.end + SLACK_MS) {
    armed = null; // a seek before the start or past the end
  } else if (at >= armed.end) {
    window.tsPlayer.pause();
    armed = null;
  }
});
