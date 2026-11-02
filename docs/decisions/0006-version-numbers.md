# 0006 Version numbers: YEAR.RELEASE.BUGFIX

Date: 2026-11-02. Status: accepted (the operator's decision, 2026-11-02; revised the same day from a
two-digit-year SemVer scheme, before any release, so that versions never wrap at 2100).

## Decision

Tendshelf versions follow the JetBrains pattern `YEAR.RELEASE.BUGFIX`:

- **YEAR** is the full four-digit year the release ships: 2026, 2027, …
- **RELEASE** counts the feature releases of that year, starting at 1: 2026.1, 2026.2, … The first
  feature release of a new year is YEAR.1 again (2027.1).
- **BUGFIX** counts fix releases of that feature release, starting at 0: 2026.1.0, then 2026.1.1,
  2026.1.2, … A bug-fix release keeps its feature release's year even when it ships in January of
  the next year (a fix to 2026.3 shipped in 2027 is 2026.3.1).
- The first release is **2026.1.0** (spoken and shown as "2026.1").
- This is not SemVer: a new year does not by itself mean a breaking change. Any release that changes
  the data directory, the API or the flags in a way older setups notice says so in its notes, and a
  database migration stays forward-only with the older-binary refusal (`AGENTS.md`).

## How it is applied

- Git tags are `v2026.1.0`, always three parts so they sort. `make build` stamps the binary with
  `git describe --tags`, so a tagged build reports `v2026.1.0` and a later one
  `v2026.1.0-3-g1a2b3c4` (`-version` and the start-up log line). The interface may show "2026.1"
  for a .0 release.
- The roadmap's first tagged release after M2 is **v2026.1.0**.
