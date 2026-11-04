# 0002 Adopt the Walnut & Ember design handoff, and the P0 dependencies

Date: 2026-11-01. Status: accepted by the operator (2026-11-01).

## Context

Claude Design delivered the full interface as `docs/design/tendshelf-handoff/`: tokens and `ts-`
component CSS, 36 components, 114 boards (phone, tablet, desktop; core, browse, people, admin, states,
reader) and a developer handoff (`docs/HANDOFF.md`). It assumes Go + templ + HTMX + plain CSS, so it
fits the stack, but it disagrees with the project rules in a few places. Nothing could be built
because no dependency was approved.

## Decision

1. **The handoff is the visual source of truth.** Precedence inside it: `docs/HANDOFF.md` > component
   `README.md` > `BRAND.md` > board PNG > board HTML. Project rules (`AGENTS.md`, `architecture.md`)
   still win over the handoff on stack, security, routes and privacy.
2. **P0 dependencies approved**: `github.com/a-h/templ` (v0.3.1070), `modernc.org/sqlite` (v1.60.1),
   htmx 2.x vendored as one file, `golang.org/x/crypto` (argon2). Each gets its `NOTICES.md` row in the
   commit that adds it. Read-along rows stay "awaiting approval".
3. **Fonts self-hosted, never Google Fonts**: Fraunces, Fira Sans and Fira Mono (lab) as woff2 under
   `internal/web/static/fonts/` (P0b), OpenDyslexic as the one extra reader option (R4). Literata and
   Atkinson Hyperlegible are dropped; the reader offers the design's serif (Fraunces) and sans
   (Fira Sans) plus OpenDyslexic.
4. **Progress is saved every 10 s** while playing (the design's value; was 15 s), plus on pause, page
   hide and lock.
5. **Routes follow `architecture.md`**: the handoff's suggested routes are replaced by canonical pages
   (selected with `hx-select`) and `/hx/...` fragments; the handoff's targets and triggers are kept.
   The mapping is in `architecture.md`, "Design regions".
6. **Breakpoints follow the design**: phone 360-767 (designed at 390), tablet 768-1279 (designed at
   1024), desktop 1280+ (designed at 1440). Checks run at 390, 768, 1024 and 1440.
7. **First run creates the owner account first**, then names the server and adds the first folder. The
   design shows folder and scan before the account; an unauthenticated folder picker is not acceptable
   on a server that may be exposed. The boards' look is kept, only the order changes.
8. **Scope added from the design**: the cover tint and per-theme cover accent are computed at scan time
   (answering the handoff's open question) with the standard library from ffmpeg JPEG thumbnails; a
   rule-based "Because you listened to…" Home row (P1); bookshelf spines on Home use the cover tint;
   Eyes-free mode is the P4 car mode. A TV app is "Later" and undesigned; nothing is improvised for it.
9. **Visual checks without Node**: an opt-in Go test helper drives the system `chromium --headless
   --screenshot` (no Go module) at the four widths and writes captures under
   `.local/tests/visual/<run-id>/` for side-by-side review against the board PNGs. Boards use demo
   data, so the comparison is a reviewed check, not a pixel diff.
10. **Delivery order**: M0 foundation, M1 single-owner listening, M2 sharing (P3), M3 offline PWA (P4),
    M4 online details (P2), M5 read-along (R), so read-along lands with its sharing and offline parts.

## Consequences

`AGENTS.md` (layout, design and checks), `docs/architecture.md` (dependencies, design regions, cover
colour, setup), `docs/roadmap.md` (milestones), `docs/vision.md` (10 s), `docs/reader.md` (fonts)
and `docs/design/README.md` are updated in the same commit. The duplicate handoff zip and canvas
overview screenshots were not kept.
