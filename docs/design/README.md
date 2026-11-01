# Design

Visual design for Tendshelf is produced with Claude Design and implemented by hand as templ components
and plain CSS. The accepted design is **Walnut & Ember** ([decision 0002](../decisions/0002-design-handoff.md)).

## Files

| Path | What | Owner |
| --- | --- | --- |
| `tendshelf-handoff/` | The accepted handoff (exported 2026-11-01): design system (`tokens.css`, `tokens.json`, `components/bundle.css`, 36 components with `README.md` and `preview.html`, logos), `screens/` (114 boards as PNG + reference HTML, listed in `screens/INDEX.md`), `docs/HANDOFF.md` (grid, persistent elements, motion, HTMX regions, accessibility, copy deck, cover accent, reader) and the original brief. Start with its `README.md`. | Replaced only by a new accepted round. |
| `claude-design-prompt.md`, `claude-design-prompt-reader.md` | The briefs given to Claude Design. | The lead writes them; do not edit without the lead. |

Live copies (owner access only): the design system at
https://claude.ai/artifact/64fwa6bjmdTQn8KJtgbh4P and the screens canvas at
https://claude.ai/artifact/KXfHbFsyKpv3K7xfVqnZSw. The folder here is the reference the code follows;
a new design round replaces the folder in one commit and records what changed.

## Rules for turning a design into code

- Precedence inside the handoff: `docs/HANDOFF.md` > component `README.md` > `BRAND.md` > board PNG >
  board HTML. `AGENTS.md` and `docs/architecture.md` win over the handoff on stack, routes, security
  and privacy (see decision 0002 for the resolved differences).
- `tokens.css` and `bundle.css` become `internal/web/static/css/`; extend with new `ts-` classes. Board
  HTML is reference markup (inline layout styles, placeholder covers, frozen `is-focus`/`is-hover`
  states): rebuild it as templ components in `internal/web/components/`, never paste it.
- No external fonts, icon CDNs, Tailwind classes or framework markup. Fraunces, Fira Sans and Fira Mono
  are self-hosted with their licence in `NOTICES.md`; icons are inline SVG sprites.
- Every theme (`dark`, `light`, `sepia`, `contrast`) is checked; text 4.5:1, contrast theme 7:1.
- Phone (390 px) is the primary frame; tablet (1024) and desktop (1440) extend it.
- Keep the persistent player and the `#main` region from `docs/architecture.md`; a design that needs a
  full-page reload during playback is sent back.
- Nothing from `screens/directions/` (early moodboards) and no TV layouts.
