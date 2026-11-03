# Changelog

Versions are `YEAR.RELEASE.BUGFIX` ([decision 0006](docs/decisions/0006-version-numbers.md)). Dates are UTC.

## 2026.1.0

The first release. Tendshelf is a self-hosted audiobook server and player for a family: one program, your
own files, no account anywhere else.

### What you can do

- **Listen.** Play a book on your phone or computer and pick it up on another device where you left off.
  Speed, a sleep timer that fades out, chapters, "Resume", and a "Continue there?" question when another
  device went further.
- **Library.** Add folders of audiobooks; Tendshelf scans them, reads chapters and covers, lists the books
  and tells you in plain words about files it cannot play. Copy-protected files are refused, never opened.
  Browse by library, filters and sorting, as a grid or a list, with search that finds titles, authors,
  narrators and series and suggests a fix for a typo.
- **Book pages.** Cover, details, chapters, bookmarks and notes (export them as Markdown), people and
  series pages.
- **Shelves.** Make your own shelves, order the books, and share a shelf with the other members.
- **The player bar.** A full player on a phone and a bar on a computer with a waveform for the current
  chapter: click it to jump. The "Now playing" panel on a wide screen shows the book's story, details
  and bookmarks beside the player.
- **Larger text.** Settings, Easier to use: a bigger text size that also changes the layout so nothing is cut.
- **Users.** The owner and admins invite family and friends as members or listeners, choose which
  libraries, shelves or books each person can open, set an end date, and stop sharing at any time.
- **Invites.** Send an invitation from your own email app with one tap, or let Tendshelf send it through a
  mail server you set up (optional). Or copy a link.
- **Share links.** A link that lets anyone listen to one book, with an end date and an optional password,
  without an account. Stop it whenever you like.
- **Shared with you.** What others share appears on Home and in the Library, labelled with who shared it.
- **Devices.** See and sign out the browsers and devices signed in to your account.
- **JSON API.** Device tokens and a documented JSON API for apps and scripts (library, search, streaming,
  progress and bookmark sync): see [docs/api.md](docs/api.md).
- **Private.** No telemetry, no third-party fonts or scripts. The server calls out only for the optional
  mail server you configure.

### Known limits

- The one-handed check on real phones (Android Chrome and iOS Safari) is written
  ([docs/manual-checks.md](docs/manual-checks.md)) but has not been run yet.
- Safari-engine coverage is a WebKitGTK run of the main pages, not a run on an iPhone or a Mac.
- Larger text set only inside the browser (the browser's own text-size setting, not the Tendshelf setting)
  keeps the desktop player bar on windows 1280 to 1599 px wide.
- Passkeys are not built yet; sign in with a password.
- No downloads or offline listening yet (milestone M3, the installable app).
- Looking up book details online, editing details and writing them into the file come later (milestone M4).
  Read-along is designed and not built.
- Sleep-timer shake needs HTTPS on a phone.

### Upgrading

This release uses database schema 9. A new install starts at 9. Before Tendshelf upgrades an existing
database it writes a verified copy to the `backups` folder inside the data folder and keeps the newest
three. A database made by a newer version is refused and left untouched. Your audio files are never
changed. The flags and their environment variables are in the [README](README.md).
