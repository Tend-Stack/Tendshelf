Short messages that confirm, hint or warn without blocking listening, plus the gentle offline banner.

## Markup

```html
<div id="toasts" aria-live="polite"></div> <!-- HTMX: hx-swap-oob="beforeend:#toasts" -->
<div role="status" class="ts-toast">
  <svg class="ts-icon ts-toast__ok" …></svg>Bookmarked at 14:06
  <button class="ts-btn ts-btn--quiet">Add a note</button>
</div>
```

## Kinds

- **Hint** (`--hint`, pill, 36px): one quiet line that needs no action, such as "Rewound 5 s after your break". Shows for 1.6 s.
- **Confirmation:** a check in `success`, what happened in past tense, one optional action ("Undo", "Add a note"). 4 s, or 8 s when it has Undo.
- **Error:** `role="alert"`, the alert icon in `danger`, plain words and "Try again". Stays until dismissed or retried.
- **Offline banner** (`ts-banner`): sits at the top of the content, not floating; "You're offline. Downloaded books still play." Disappears on reconnect with a hint toast "Back online".

## Rules

- One toast at a time; a new one replaces the old.
- Phone: centered 12px above the mini-player. Desktop: bottom-left above the player bar.
- Enter with `duration-base` / `easing-enter` (fade + 8px rise); leave with `easing-exit`. Reduced motion: fade only.
- Never use a toast for something the person must read to continue.
