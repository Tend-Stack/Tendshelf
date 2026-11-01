Pill buttons for actions: one ember primary per screen, secondary for the rest, quiet for links that act.

## Markup

```html
<button type="button" class="ts-btn ts-btn--primary ts-btn--lg">
  <svg class="ts-icon ts-icon--fill" viewBox="0 0 24 24" aria-hidden="true">…</svg>
  Resume
</button>
```

Classes: `ts-btn` plus one variant (`--primary`, `--secondary`, `--quiet`, `--danger`) and optional `--lg` (56px, `button-lg` type) and `--block` (full width). Use an `<a class="ts-btn …">` when it navigates.

## Rules

- **One primary per screen.** It is the obvious next step: "Resume", "Start listening", "Invite someone", "Scan now".
- Primary is `accent-fill` with `on-accent` text. Never white text on ember.
- Secondary is `raised` with a `line` border: Download, Add to shelf, Share.
- Quiet is `accent` text, no fill: "See all", "More", "Edit".
- Danger is an outline in `danger` and always says what it does ("Remove access", never "OK").
- Labels are verbs in sentence case, two words or fewer where possible. No "Submit", no "Click here".
- Minimum height is `size-tap` (44px); `--lg` is 56px for the hero and onboarding.

## States

Default, hover (`accent-fill-hover` / `raised-hover`), focus-visible (`focus-ring`), pressed (pressed fill + 98% scale for `duration-instant`), disabled (`raised` fill, `ink-subtle` text, `not-allowed`), loading (`aria-busy="true"`, a `ts-spinner` and an -ing label such as "Saving…"; keep the width steady). The `.is-hover`, `.is-focus` and `.is-pressed` classes exist only to show states in docs.

## Accessibility

Real `<button>` or `<a>`. Icons inside are `aria-hidden`; the label carries the meaning. Disabled buttons that block a task get a nearby sentence explaining why. Spinners stop under reduced motion.
