# 0007 Drop the Research module

Date: 2026-11-02. Status: accepted (the operator's decision, 2026-11-02: "Yes, drop M7").

## Decision

The Research module (milestone M7, the P6 port of the TendVox Extractor) is dropped from Tendshelf
entirely. Nothing of it is built, and it is not planned.

- **Stays:** the Extractor's book-info lookup and parsing, which populate the library accurately. It
  is planned in M4 Details, unchanged.
- **Goes:** clip extraction, categories, measurements, coverage, the keep-numbers/delete-clips rule,
  the `tendvox-reference-styles` export, the admin Research door and lab, the `research` package,
  the `research.enabled` setting, the `research.use` action and the `research_*` tables. The `lab-*`
  design tokens are not used.
- The handoff's `screens/admin/Research*` boards stay in `docs/design/tendshelf-handoff/` as history.
  They are not built.

## Reason

The operator, 2026-11-02: "We do not need the extractor functionality about the clips and such; those
are not needed for Tendshelf, which is mostly an audiobook player and reader. The only thing we need
from the extractor is the logic to pull books info to populate the library accurately. Nothing else."

## Consequences

- The fair-practice section of `AGENTS.md` is removed. The rules it held (owned files only, clips
  deleted, transcripts never stored) no longer apply because no clip or transcript is ever created.
- Copy-protection refusal and the no-DRM rule still apply everywhere, as before.
- The alignment worker (read-along, decision 0001) is unaffected. Its transcript rule in decision
  0001 stands for read-along.
