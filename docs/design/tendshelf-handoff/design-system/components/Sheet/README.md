Bottom sheets for choices on phone (sleep timer, speed, sort, filters, chapters) and centered dialogs for confirmations on tablet and desktop.

## Markup

```html
<div class="ts-scrim"></div>
<div class="ts-sheet" role="dialog" aria-modal="true" aria-labelledby="sleep-title">
  <div class="ts-sheet__grab" aria-hidden="true"></div>
  <div class="ts-sheet__head">
    <h2 class="ts-sheet__title" id="sleep-title">Sleep timer</h2>
    <button class="ts-iconbtn" aria-label="Close">…</button>
  </div>
  <ul class="ts-options" role="radiogroup" aria-labelledby="sleep-title">…</ul>
</div>
```

On desktop the same content renders in a `ts-dialog` (or a popover anchored to the button that opened it). Use the native `<dialog>` element where possible.

## Rules

- A sheet holds one decision. Choosing an option applies it and closes the sheet; no OK button.
- Confirmation dialogs name the action in the title as a question and in the destructive button ("Stop sharing"), with the safe choice first and secondary.
- Say what is kept: "Her progress and notes stay saved."
- Sheets rise over `duration-base` with `easing-enter`; swipe down or tap the scrim to dismiss. Reduced motion: fade.
- Focus moves into the sheet on open and back to the trigger on close; Escape closes.
- The speed sheet adds a fine-tune slider (0.5× to 3.0×); the sleep sheet shows "in 18 min" beside End of chapter.
