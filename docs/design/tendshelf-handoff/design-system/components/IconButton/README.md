Round icon-only buttons for transport controls, sheet closers and toolbars, always 44px or larger.

## Markup

```html
<button type="button" class="ts-iconbtn ts-iconbtn--cover ts-iconbtn--xl" aria-label="Pause">
  <svg class="ts-icon ts-icon--fill" viewBox="0 0 24 24" aria-hidden="true">…</svg>
</button>
```

Variants: plain (transparent), `--tonal` (`raised` fill), `--filled` (ember, `on-accent` icon), `--cover` (`cover-accent`, `on-cover-accent` icon; only inside the player). Sizes: default 44, `--md` 56 (skip ±30 s), `--lg` 64 (mini-player and read-along play), `--xl` 80 (the player's play/pause).

Skip buttons: add `ts-skip` and a `<span class="ts-skip__num">30</span>` inside; the number follows the user's skip setting (10, 15, 30, 45, 60).

## Rules

- Every icon button has an `aria-label` that names the action, not the icon: "Back 30 seconds", "Close player", "Add bookmark".
- Toggles (bookmark, sleep timer on) use `aria-pressed`; the pressed icon fills and turns `accent`.
- The play button is the only `--xl`. In the player it is `--cover`; everywhere else it is `--filled`.
- Long-press on the player's play button opens the speed sheet; it still works as a plain tap.

## States

Default, hover (`raised-hover`), focus-visible (`focus-ring`), pressed (94% scale), disabled (`ink-subtle`), loading (`aria-busy` + spinner).
