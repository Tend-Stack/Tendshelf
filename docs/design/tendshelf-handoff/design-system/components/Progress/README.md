How Tendshelf shows progress: rings on covers, thin bars for downloads and scans, the chapter ruler for a book, and skeletons while a region loads.

## Markup

```html
<!-- ring: r=11, circumference 69.1; dasharray = 69.1 × percent -->
<span class="ts-ring-badge">
  <svg class="ts-ring" viewBox="0 0 28 28" role="img" aria-label="35% listened">
    <circle class="ts-ring__track" cx="14" cy="14" r="11"/>
    <circle class="ts-ring__fill" cx="14" cy="14" r="11" stroke-dasharray="24.2 69.1"/>
  </svg>
</span>

<div class="ts-bar" role="progressbar" aria-label="Downloading Salt and Ember"
     aria-valuemin="0" aria-valuemax="100" aria-valuenow="46">
  <div class="ts-bar__fill" style="width:46%"></div>
</div>
```

## Rules

- **Rings** sit bottom-right on started books only. Not started shows nothing; finished shows a `success` check disc. No "New" badges.
- **Bars** are 4px, `line` track and `accent-fill` fill, always with a sentence beside them ("Downloading · 214 of 468 MB", "Found 412 books… reading covers").
- **The ruler** is the book: a hairline with a tick per chapter and the listened part filled. It is the progress shape everywhere a single book's position matters (hero card, Book detail, read-along).
- **Skeletons** replace spinners for any region loading through HTMX. Match the final layout (cover squares, two text lines). The shimmer stops under reduced motion.
- Rings and bars ease to their new value over `duration-ring` with `easing-standard`; they never jump backwards visibly unless the user scrubbed back.
- Every progress element has an `aria-label` naming the thing and, where useful, `aria-valuetext` in words ("6 hours 40 minutes left").
