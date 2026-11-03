# 0010 Settings as a preferences window, and the wide layout
Status: proposed (2026-11-03). Plan: `docs/plans/settings-desktop.md`.

## Operator's words
- On the pages to cover: "profile settings, /me and the different settings pages, download page, search, and any
  other page that haven't, needs to be accommodated to the new limits layout."
- On the Settings area: "Also the settings area could be more intuitive and more native looking, rather than just a
  long list of options. When the user opens the app, they should feel like they are on a high end, beautiful desktop
  app. This way when we pack on Wails v3, it will look great."
- On the screenshot of `/me/settings`: "this that can be side by side, buttons, tabs, etc."

## Context
- Earlier today the app frame was capped at 1920 px pinned left (`--size-app-max: 120rem`).
- `.ts-main` lost its max-width and got 20 px side padding from 1280 px.
- Pages now make room for the open Now playing side panel.
- Several pages still carried their own caps, so they sat as narrow columns in a wide window:
  - Profile and Settings: 40rem.
  - Shelves: 45rem.
  - Share: 75rem.
  - Queue: 40rem.
- Settings was one long form, and `/me/downloads` was a dead link.

## Decision
1. **The 1920 px frame cap replaces the handoff's "desktop content max 1200"** (`HANDOFF.md`, layout grid). Pages
   fill the frame. Running text still stops at `--size-measure` (68ch), and panels keep a comfortable measure.
2. **Settings becomes a preferences window.**
   - A category list beside a content pane:
     - the person's own card (Profile);
     - Playback, Appearance, Easier to use and Privacy;
     - a "Your server" group of Users & invites, Libraries and Email. It shows only what that person may manage, and
       Email is owner-only.
   - Each category has its own URL that returns a full page. Moving between categories never reloads the player.
   - Each pane is made of rounded panels of rows (in the manner of macOS System Settings and GNOME preference groups).
     The panels sit side by side when there is room.
3. **The phone splits Settings into pages.** On phone, and wherever there is not room for two columns, the list is its
   own page (`/me/settings`), and each category opens as a page with a back link. This replaces the single long
   scroll of the handoff's `SettingsPhone` board. The controls, copy and order inside each category stay the board's.
4. **Two columns depend on the space the content actually gets**, not on the screen size: a CSS container query at
   44rem.
   - Tablet portrait (768 px, about 632 px of content) therefore uses the phone pattern, as does any width in large
     text where the list would squeeze the pane.
   - Tablet landscape, desktop, and desktop with the side panel open use two columns.
   - Reason: a list beside a pane narrower than a phone helps nobody.
5. **Each other page fills, uses columns, or stays narrow, as the plan's section 5 sets out.**
   - Unordered collections fill: the shelves list, and Search's result groups side by side.
   - Ordered lists stay one measured column (48rem): a single shelf, and the Queue.
   - Confirmations and short forms stay narrow cards: deleting a shelf, the admin confirms, the password form and
     Email.
6. **`/me/downloads` becomes an honest page.** It says that listening without a connection is on its way, and it shows
   no controls until M3 (Pocket) builds downloads. The navigation link stays, because it is part of the handoff's
   navigation.

## Consequences
- Board captures for Settings and Profile change on phone (pages instead of one scroll) and on desktop.
- The Users and Libraries pages render inside the pane.
  - Their wide-screen rules move from viewport queries to pane-width queries where only those pages use them.
  - The rules home shares stay as they are.
- The plan adds no settings keys, no migration and no JavaScript.
- Icons in the category list are left for later.
