# 0004 Controls use 12 px corners, not pills

Date: 2026-11-02. Status: accepted by the operator (2026-11-02).

## Context

The design handoff draws buttons, chips, segmented controls and the search field as pills (fully
rounded ends). In the running app the "Recently added" sort select, a plain `.ts-input`, has a
12 px corner (`--radius-md`), so the controls on one screen did not match. The operator's words: the
corners should be standardized like the ones for the sort menu on all buttons across the app, and
not super rounded.

## Decision

1. One token, `--radius-control` (`tokens.css`, equal to `--radius-md`, 12 px at the default text
   size), is the corner of every button, chip, segmented control, text field and select.
2. These moved from the pill radius to `--radius-control`: `.ts-btn`, `.ts-chip`, `.ts-seg` and
   `.ts-mode--block`, the search field (`.ts-search .ts-input`), `.ts-toast--hint`, `.ts-fmt`,
   `.ts-voicepill` and `.ts-readdock__speed`. A segment inside a segmented control gets the container
   corner minus its border and padding (`.ts-seg__item`, `.ts-mode--block .ts-mode__item`), so the
   curves nest.
3. Circles stay round (`--radius-pill` or `50%`): avatars, dots and markers, the switch track and
   knob, the spinner, ring badges, and icon-only buttons whose width equals their height (`.ts-iconbtn`:
   play and pause, skip, the cover play button).
4. The handoff copy under `docs/design/tendshelf-handoff/` is not edited. This decision overrides its
   `tokens.css` and `bundle.css` where they disagree; the app's stylesheets under
   `internal/web/static/css/` carry the change.
