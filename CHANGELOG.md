# Changelog

Versions are `YEAR.RELEASE.BUGFIX` ([decision 0006](docs/decisions/0006-version-numbers.md)). Dates are UTC.

## Unreleased

This will be **2026.2.0**; the date is added on release day. It brings offline use, uploads, a book reader,
ebooks as books, and a desktop app.

### What you can do

- **Offline.** Install Tendshelf as an app and download a book, audio and text, to read or listen with no
  connection. Your place and bookmarks sync when you are back online. Downloading and installing need HTTPS
  ([docs/deploy.md](docs/deploy.md)).
- **Add books by upload.** The owner can let people add books from the browser into one folder of a library
  (off by default). Nothing is overwritten, and removing a book deletes only what the upload created.
- **Ebooks are books.** An ebook has a cover, a Read button and its own place. An ebook that matches an
  audiobook (same folder, ISBN or ASIN, or title and author) joins it automatically into one book; you can
  also pair them by hand. Copy-protected ebooks are refused.
- **The book reader.** Pages or scrolling; fonts, sizes, themes and a dyslexia-friendly option; highlights
  in four colours with notes; search; contents; footnotes; a skim bar over the chapters; print page numbers
  where the book has them; comfort options (keep the screen on, warm light, a bar that hides).
- **Look-up and a word list.** Select a word to see definitions from dictionary files you add to the data
  folder, search the web by tapping a link, and save words to a list. Nothing is bundled or fetched.
- **Your listening and your reading.** A dashboard of the time you listened, your days, hours and finished
  books, and the same for reading, with a yearly goal. It shows only your own data.
- **Eyes-free.** A big-button screen for the car.
- **Scheduled backups.** The owner can back up the database every day or every week, or on demand.
- **A better Profile and a devices page.** Profile puts your books first, Home shows what else is in
  progress, and "Your devices" lists the devices and browsers you are signed in on.
- **Desktop app.** A Tendshelf window for Linux, with a tray icon and media keys, as a `.deb`
  ([docs/desktop.md](docs/desktop.md)). It runs its own library or opens a server you already run.
- **Accessibility audit.** Every page was checked at four widths and four themes for contrast, targets,
  focus and zoom; the fixes are in this release.
- **Book details.** Look up a book online and compare the results field by field before you apply them. Change or upload
  a cover. Author photos and bios. Read sidecar files that sit next to a book. Write details into the audio file with
  "Looks good", and Undo puts the original back.
- **Sidebar.** On a computer the sidebar collapses into a rail of icons and opens again.
- **Library header.** Tidier: one Filter menu, Category, and sorting under Show.
- **Up next.** Play next and Add to queue from the play button. Books play one after another, and the Up next tab in
  the Now playing panel and the `/queue` page show what is waiting.
- **Automatic book details.** Turn on look-ups per library for new books, or look up a whole library at a gentle pace,
  filling only what is missing. A list shows the books that need a look.
- **Fewer interruptions.** Actions no longer stop the book that is playing.
- **Wide screens.** The app stops growing at 2560 px.

### Known limits

- The checks on real devices are not all run: iOS Safari for offline use, a screen reader (Orca, TalkBack,
  VoiceOver), and the desktop app on the owner's own computer. See [docs/manual-checks.md](docs/manual-checks.md).
- The desktop app is verified on Linux only; it has no offline downloads or auto-update.
- Passkeys and OPDS are not built.

### Upgrading

This release uses database schema 23 (it was 9 in 2026.1.0). Before Tendshelf upgrades an existing database it
writes a verified copy to `backups` in the data folder. Going back to an older version needs a backup: stop
the server, delete `tendshelf.db-wal` and `tendshelf.db-shm`, and put the backup back as `tendshelf.db`; an
older program refuses a database made by this one. Scheduled backups hold the database only, so to keep uploaded
ebooks and covers copy the whole data folder.

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

- The one-handed phone check ([docs/manual-checks.md](docs/manual-checks.md)) passed on Android Chrome;
  shake to extend the sleep timer needs HTTPS and was skipped. iOS Safari has not been checked yet.
- Safari-engine coverage is a WebKitGTK run (passed on 2026-11-02) of the design-system kitchen sink and the browse pages (Home, Library, a book, Search, Settings, Libraries) at 447 and 1440 px, not a run on an iPhone or a Mac. The player bar, the Now playing panel, Users, invites, share links and Shared with you are checked in Chromium only.
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
