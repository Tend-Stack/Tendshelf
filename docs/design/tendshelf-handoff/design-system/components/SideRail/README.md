The tablet navigation rail (768–1279px): the mark on top, the same five destinations as the phone tab bar stacked with labels, and Profile pinned to the bottom with the person's avatar.

## Rules

- `size-rail` (88px) wide; items are 72×64 with an icon over a `meta` label.
- The current item gets an `accent-soft` fill, `ink` label at 600 and an `accent` filled icon.
- The mini-player docks to the bottom of the content area, to the right of the rail.
- Admin pages (Server, Users & invites, Libraries, Research) are reached from Profile, not added to the rail.
- Same markup pattern as the tab bar (`<nav aria-label="Main">`, links with `aria-current="page"`), so one templ component can render all three navigations from one list.
