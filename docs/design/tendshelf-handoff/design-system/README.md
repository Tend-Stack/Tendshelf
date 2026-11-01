# Tendshelf design system

- `BRAND.md`: the brand book (voice, colour, type, layout, shape, motion, icons, accessibility, Research lab, reader).
- `tokens.json`: every token with values per theme and a usage note. `tokens.css` is generated from it: one block per theme (`[data-theme="dark"]` is also `:root`), then shared tokens and type classes.
- `components/bundle.css`: every `ts-` class. Load it after `tokens.css`.
- `components/<Name>/README.md` and `preview.html`: rules and markup for each component, every state shown. Open `preview.html` in a browser (it links the two stylesheets; fonts load from Google Fonts).
- `assets/logos/`: the mark (ember for dark grounds, walnut for paper, cream for photography) and the app icon.

Fonts: Fraunces (serif titles and reading), Fira Sans (everything else), Fira Mono (Research lab figures and file paths). Self-host them for an offline server.
