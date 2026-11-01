# 0003 Newest stable versions from day 0

Date: 2026-11-01. Status: accepted by the operator (2026-11-01). Amends [0002](0002-design-handoff.md)
point 2 (htmx 2.x becomes htmx 4).

## Context

Tendshelf starts on a freshly installed machine with no users and no data to migrate. Every version
chosen now is one we would otherwise have to upgrade later, with breaking changes landing on top of
real libraries and real users. The operator prefers to take that pain now.

## Decision

1. **Start on the newest stable release** of every tool, module and vendored file. A beta is acceptable
   when it brings a concrete improvement and its project calls it usable; record why.
2. **Versions at the start (checked 2026-11-01 from the official sources):**

   | Component | Version | Source checked |
   | --- | --- | --- |
   | Go | 1.27.3 (newest stable; no 1.28 pre-release yet) | go.dev/dl |
   | `github.com/a-h/templ` (runtime and CLI) | v0.3.1100 | proxy.golang.org |
   | `modernc.org/sqlite` | v1.61.1 | proxy.golang.org |
   | `golang.org/x/crypto` (P1b) | v0.58.0 | proxy.golang.org |
   | `golang.org/x/net`, `x/text`, `x/image` (R1, if approved) | v0.62.0, v0.43.0, v0.47.0 | proxy.golang.org |
   | htmx | **4.0.0** (released 2026-08-28; npm tag `next`, while `latest` is still 2.0.11) | GitHub release, npm registry |
   | `github.com/wailsapp/wails/v3` (P5) | v3.0.0 (stable) | proxy.golang.org |
   | Fonts | Newest upstream builds (the GitHub *releases* are years old: Fraunces 1.000, mozilla/Fira 4.202); source commit and SHA-256 recorded in `NOTICES.md` | upstream repositories |

3. **htmx 4, not 2.** htmx 4 is the line the project moves forward on. Writing every template against
   2.x now would mean rewriting them later. Where htmx 4 changes behaviour that `AGENTS.md`, the
   architecture or the design handoff describe in 2.x terms (attribute inheritance, configuration,
   swap classes, events), the P0b plan lists the corrections and the docs follow them.
4. **One version source per tool.** The templ CLI is pinned with Go's `tool` directive in `go.mod`, so
   `go tool templ generate` always matches the runtime. Developer tools that are not part of the build
   (gopls, staticcheck, govulncheck, the `wails3` CLI) are installed with `go install` at `@latest`, or
   at a named version.
5. **Keeping current stays cheap**: a dependency bump is its own commit, with the gates run and the
   versions table above updated. `govulncheck ./...` runs before each release.

## Consequences

The P0b plan is written against htmx 4 and templ v0.3.1100; the P0a builder uses `modernc.org/sqlite`
v1.61.1 with newest compatible transitive modules. `docs/architecture.md` dependency rows follow this
table when P0a and P0b land.
