# 0001 Read-along: ebooks as text editions, and an optional alignment runtime

Date: 2026-10-31. Status: accepted for design by operator brief (2026-10-31); the dependency rows in
`docs/architecture.md` still need approval one by one. Design: [read-along.md](../read-along.md).

## Context

The vision listed ebooks as a non-goal, and `AGENTS.md` allows only ffmpeg and ffprobe as external
programs. The operator asked for a read-along reader: ebooks added beside an audiobook, the text
highlighted while listening, using a model for the timing, and one position across both formats.

## Decision

1. **Ebooks are admitted as text editions of a book that has audio**, not as a standalone ebook library.
   Comics stay out. An ebook with no audiobook waits unpaired in the admin inbox.
2. **Ebook files follow the audio-file rules**: library ebooks are read-only; uploads are kept
   byte-identical; copy-protected ebooks are refused with a plain message and never decrypted.
3. **One more external program**: the optional alignment worker (`runtimes/align/worker.py` under an
   owner-installed Python environment), launched only by `internal/align/runtime` with a fixed
   argument list, loopback only, a per-launch token in the environment, bounded lifetime. It is not a
   build step and not part of the binary; the server builds and runs without it (read-along is then
   unavailable, everything else works).
4. **No new outbound connection**: Tendshelf does not download runtimes or models. The admin installs
   them from the official sources shown on `/admin/runtimes`; Tendshelf verifies hashes and records the
   licence. A one-click installer would need a second `http.Client` package and its own decision.
5. **Model output is local data**: the coarse transcript lives only in the reconstructible emissions
   cache, is never shown or exported, and is excluded from backups. (The Research module is dropped, decision 0007.)

## Consequences

`AGENTS.md` "Stack constraints" and "Audio files are the user's" name these exceptions.
`docs/vision.md` "Not goals" is narrowed. New Go modules and vendored fonts are listed under
"Dependencies awaiting approval".
