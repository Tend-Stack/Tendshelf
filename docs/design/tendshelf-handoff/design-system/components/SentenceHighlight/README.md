Marks the sentence the narrator is reading, and the word inside it.

- Wrap each sentence in `<span class="ts-s" data-t="start,end">` and each word in `<span class="ts-w" data-t="start,end">` on the server, from the alignment file. Times are seconds into the book.
- The client module sets `is-now` on one sentence and one word from the audio's `timeupdate`. Nothing else changes in the DOM.
- Pick the style on the reader root: `data-hl="wash"` (default), `"underline"` or `"brighten"`. `data-word="off"` hides the word underline.
- Cross-fade is `duration-highlight` (120ms). With reduced motion it snaps.
- Not colour alone: Wash and Brighten always keep the word underline unless the reader turns it off; Underline is the choice for anyone who can't see the wash.
- Screen readers: the highlight is visual only. Don't announce each sentence; the audio is already speaking it.
