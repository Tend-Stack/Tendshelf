# 0006 Version numbers: SemVer with the year as MAJOR

Date: 2026-11-02. Status: accepted (the operator's decision, 2026-11-02).

## Decision

Tendshelf uses Semantic Versioning, `MAJOR.MINOR.PATCH`, with one rule of its own: MAJOR is the
two-digit year in which that major version is released. The first release is **26.0.0** (2026).

- **MAJOR** changes about once a year, and always takes the year it ships: a major release in 2027
  is 27.0.0, one in 2029 is 29.0.0 (years without a major release are simply skipped).
- **MINOR** counts feature releases within that major, starting at 0.
- **PATCH** counts fix releases within that minor, starting at 0, and keeps advancing until the next
  minor; a new minor resets it to 0, a new major resets both.
- Minor and patch releases keep the major they belong to, whatever the calendar says: a fix to
  26.4 shipped in January 2027 is 26.4.1, not 27.x.
- The usual SemVer promises apply: a major release may break compatibility (data directory, API,
  flags) and says how in its notes; minor and patch releases do not.

## How it is applied

- Git tags are `v26.0.0`. `make build` stamps the binary with `git describe --tags`, so a tagged
  build reports `v26.0.0` and a build after it reports `v26.0.0-3-g1a2b3c4` (`-version` and the
  start-up log line).
- The roadmap's "first tagged release (v0.1)" after M2 becomes **v26.0.0**.
- A second major release in the same year cannot happen under this rule; if one is ever needed, it
  waits for the new year or ships as a minor with a compatibility shim.
