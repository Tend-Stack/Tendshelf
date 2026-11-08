# Contributing to Tendshelf

Thank you for caring about this. Tendshelf is a small, quiet program for people who want their own books on
their own shelf, and it stays small on purpose. Good reports and kind questions help as much as code.

## Report a problem

Open an issue and pick "Bug report". It asks for the version (`tendshelf -version`), the device or app you
were using, what happened, what you expected, and what the server log says.

- Say what you saw in plain words. A screenshot helps.
- **Before you paste a log, remove anything private**: sign-in or app tokens, invitation and share links,
  and the address of your server. Never post a token. If one slips out, sign that device out in Tendshelf
  at once.

## Propose a feature

Open an issue and pick "Feature request". **Describe the problem first**: what you were trying to do, what
you do today instead, and who else it would help. A solution is welcome, but a clear problem lets us find
the smallest good answer, which is sometimes no new feature at all.

Read [What Tendshelf will not do](docs/vision.md#what-tendshelf-will-not-do) and
[A new option has to earn its place](docs/vision.md#a-new-option-has-to-earn-its-place) first. In short, a
feature that costs scan time, page weight or binary size has a high bar.

## Build and test

You need Go (the version in [decision 0003](docs/decisions/0003-newest-versions.md)) and `ffmpeg` and
`ffprobe`. Nothing else, and no Node.js.

```
make generate   # regenerates the templ files; commit the result
make check      # gofmt, go vet, go test -race ./...
make build      # writes bin/tendshelf
```

Run `make check` and `make build` before you open a pull request, and add tests with every change: table
tests for parsers, `httptest` tests for handlers, a migration test for each migration. Tests use fake audio;
never commit real books, covers or personal data. Commits use the conventional style (`fix(scan): ...`).
Please keep a pull request to one thing, and say what you reused, extended or replaced.

## The hard rules

[AGENTS.md](AGENTS.md) holds the rules every change must follow: the stack, security and privacy, how
migrations work, accessibility, and the design. The words you write in the interface follow
[BRAND.md](docs/design/tendshelf-handoff/design-system/BRAND.md): plain, warm, and understandable by
someone who has never used an audiobook app.

**A new Go module needs approval first.** Ask in the issue before you add one; a pull request that changes
`go.mod` without that is not merged.

## What we will decline, and why

We say no kindly, and we say why.

- **Telemetry, analytics, update pings or anything that phones home.** Tendshelf sends nothing about you
  anywhere.
- **Removing copy protection.** Protected files are refused, never opened.
- **Node.js, npm, a bundler or any JavaScript build step.** The program is one Go binary, and it should stay
  buildable with Go alone.
- **A new dependency or a second program to install.** `ffmpeg` and `ffprobe` are the only outside programs.
- **Models, workers or downloaded runtimes, read-along and voice sync.** Read aloud uses the voices a device
  already has.
- **Options that make every page or scan slower** for a few people's benefit.
- **Changes that write to library files** outside the two jobs the owner starts.

If your idea is close to one of these, open an issue and tell us the problem. There is often a way to solve
it that fits.
