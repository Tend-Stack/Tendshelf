A bookmark in the timeline: where it is (chapter and time), when it was made, the sentence it belongs to when the ebook is paired, the person's note, and "Play from here".

## Markup

```html
<article class="ts-mark">
  <div class="ts-mark__rail" aria-hidden="true"><span class="ts-mark__dot"></span><span class="ts-mark__stem"></span></div>
  <div class="ts-mark__body">
    <div class="ts-mark__when"><span>Chapter 12 · 14:06</span><span>·</span><span>Tonight</span>
      <button class="ts-mark__jump">Play from here</button></div>
    <p class="ts-mark__quote">“She carried it up the hundred and nine steps…”</p>
    <p class="ts-mark__note">The second lamp is the father's.</p>
  </div>
  <button class="ts-iconbtn" aria-label="Edit note">…</button>
</article>
```

## Rules

- Newest first. The timeline rail (dot and stem) is decoration; the order is the list order.
- The quote line appears only when an ebook is paired; it is the sentence being spoken at that moment, in italic serif and `ink-muted`.
- Notes are private to the person unless they share them.
- Bookmarks without a note show "No note" in `ink-muted` and an Add button instead of Edit.
- "Play from here" is a quiet text button; editing opens a sheet with a text field and Save / Delete bookmark.
- Export (from the page header): plain text or Markdown, grouped by chapter.
- Pressing B (desktop) or the bookmark button adds one at the current second and shows a toast: "Bookmarked at 14:06 · Add a note".
