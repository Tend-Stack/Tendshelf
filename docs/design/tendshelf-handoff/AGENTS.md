# Instructions for coding agents

You are building the Tendshelf web interface (Go + templ + HTMX, plain CSS) from this design handoff.

1. Read `README.md` in this folder first, then `docs/HANDOFF.md` and `design-system/BRAND.md`.
2. Use `design-system/tokens.css` and `design-system/components/bundle.css` as the app's stylesheets. Extend `bundle.css` with new `ts-` classes rather than writing inline styles or hard-coded colours.
3. For each screen, open its PNG in `screens/` (see `screens/INDEX.md`) and copy structure, roles and copy from the matching `.html` snapshot and from `design-system/components/<Name>/preview.html`. Snapshots are reference markup, not production code.
4. Precedence when sources disagree: `docs/HANDOFF.md` > component `README.md` > `design-system/BRAND.md` > screen PNG > screen HTML snapshot.
5. Each HTMX region listed in HANDOFF.md is its own templ component and swaps alone. Keep `<audio>`, the mini-player or player bar, and navigation outside swaps with `hx-preserve`.
6. Use the copy deck's words exactly. Don't invent labels; if a string is missing, follow the voice rules in BRAND.md and list it in your summary.
7. Every screen must pass in all four themes (`dark`, `light`, `sepia`, `contrast`), at 44px targets, with visible focus and `prefers-reduced-motion` respected.
8. Don't build TV layouts or anything from `screens/directions/` (early moodboards).
9. When you finish a screen, compare it side by side with its PNG at the design size (390, 1024 or 1440 wide) and fix differences before moving on.
