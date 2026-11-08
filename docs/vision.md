# Vision

Tendshelf is the audiobook app people choose because it feels better than the one they paid for, and
it happens to run on their own machine.

## Who it is for

| User | Situation | What they need |
| --- | --- | --- |
| **The owner** | Has a folder of audiobooks (bought DRM-free, ripped from CDs they own, from libraries that sell DRM-free files, public-domain recordings) on a NAS, a mini PC or a laptop. Somewhat technical, not a sysadmin. | Install in minutes, point at a folder, see a beautiful library with correct details and covers, and never worry that the files get damaged. |
| **The family listener** | Partner, parent, teenager. Not technical at all. Uses a phone. | Open a link, tap a cover, it plays, it remembers. Nothing else to learn. |
| **The invited guest** | A friend the owner lends one book to, for a month. | A link that works on their phone without creating anything complicated, and stops working when the loan ends. |
| **The commuter / runner / sleeper** | Any of the above, listening in a car, on a run, in bed. | Big controls, lock-screen controls, offline downloads, a sleep timer that fades out, never losing their place. |
| **The reader-listener** | Reads on the train, listens while walking or cooking; a language learner; a reader with dyslexia or low vision. | Add the ebook beside the audiobook, read it set in the type you chose, and switch between reading and listening without hunting for the place. |

## Jobs to be done

1. "When I drop new files in my folder, I want them to appear correctly named, grouped and covered,
   so that my library looks like a shop, not a file browser."
2. "When I press play on any device, I want to continue from exactly where I stopped on another device."
3. "When I fall asleep listening, I want it to stop and remember where I really stopped hearing it."
4. "When I want to share a book with my sister, I want to send a link, not explain a server."
5. "When I am offline on a plane, I want the books I downloaded to play like nothing happened."
6. "When the details are wrong, I want to fix them once, from a good online match, field by field,
   and optionally save them into the file itself, safely."
7. "When I come back after a pause, I want to hear the last few seconds again so I know where I am."
8. "When I read an ebook, I want it to open where I stopped, set in the type I chose, so that I read
   instead of setting things up."
9. "When a book comes in both formats, I want one book with one button for whichever I used last, so that
   two formats never feel like two libraries."

## What "wow" means here

Measurable, not adjectives. Each is an acceptance target for the phase that delivers it.

- **First sound in under one second** after tapping play on a book on the home network (server on a
  modest mini PC, phone on Wi-Fi). Library page interactive in under 300 ms on a mid-range phone.
- **Never lose your place.** Position saved every 10 s while playing, on pause, on page hide and on
  lock; offline listening syncs on reconnect; the newest position wins across devices, and "you were at
  4:12:30 on your laptop, continue there?" when two devices disagree by more than a minute.
- **Lock screen like a native app**: title, chapter, cover, play/pause, skip by the user's interval,
  chapter next/previous (Media Session API).
- **Comfort details Audible users expect**: speed 0.5x to 3.0x in 0.05 steps with pitch kept, sleep
  timer by minutes or end of chapter with a gentle fade and shake-to-extend where the device allows it,
  automatic short rewind after a pause, chapter-relative progress ("12 min left in chapter"), "time left
  at this speed".
- **Correct library on the first scan** for the common folder layouts (Author/Series/Book, single m4b,
  folder of numbered MP3s, multi-disc folders) with no manual fixing.
- **Sharing in two taps**: "Share this book" gives a link with an expiry; the guest listens on their phone
  without installing anything.
- **A book reader in the same place** ([decision 0018](decisions/0018-book-reader.md)): an ebook with no
  audiobook is a book in the library, with a cover, a **Read** button and its own place. An ebook that
  matches an audiobook joins it; the book page then offers one primary button for the format you used
  last and a plain button for the other, and the player shows a book icon that opens the reader.
- **Two formats, two places**: the listening place and the reading place never write each other. Reading
  on the train does not move the narrator, and pressing play on the walk home does not move the page.
- **Reading comfort for everyone**: pages or scrolling, a dyslexia-friendly font option, sizes to 250 %,
  sepia and night themes, highlights in four colours, all at WCAG AA contrast.
: one binary, under 100 MB RAM idle with a 2,000-book library (target; measure it),
  no background network traffic, nothing to keep up to date except the binary.
- **Beautiful on a phone first**, at home on a 27-inch monitor, readable in sunlight and at night.

## Not goals

- Comics and fixed-layout books, and a store. Ebooks are books here ([decision 0018](decisions/0018-book-reader.md)),
  but only reflowable, DRM-free ones; there is no read-along (the voice and the words do not share a place).
  The reader can read a book aloud with the device's own voice (decision 0032).
- Selling, DRM removal, or downloading from stores. Tendshelf refuses copy-protected audio and ebooks.
- A social network. Sharing is private, person to person.
- A cloud service run by us. Self-hosted only.

## What Tendshelf will not do

These are promises already made in `AGENTS.md`, in this file and in the decisions. They are the reason a
request is sometimes answered with a kind no.

- **No telemetry.** No analytics, crash reports, update pings or third-party assets (fonts, CDNs, map
  tiles). The only outbound requests are ones a person starts (a book lookup, a cover download) and the
  mail server the owner sets up for invitations (`AGENTS.md`, "Security and privacy").
- **No removing copy protection.** Copy-protected audio and ebooks are refused and never decoded,
  streamed, remuxed or decrypted (`AGENTS.md`, "Audio files are the user's"; "Not goals" above).
- **No Node.js or JavaScript build step.** The shipped app, the build, CI and the development scripts need
  none: templ templates, one vendored HTMX file and a little plain JavaScript (`AGENTS.md`, "Stack
  constraints").
- **One binary.** One pure-Go server program with its pages and migrations inside. `ffmpeg` and `ffprobe`
  are the only outside programs, and a new Go module needs the owner's approval (`AGENTS.md`).
- **No models or downloaded runtimes.** Tendshelf never asks a person to install a worker, a model or a
  language runtime ([decision 0018](decisions/0018-book-reader.md)).
- **No read-along and no voice sync.** The voice and the words do not share a place. The reader can read a
  book aloud, but only with a voice the device already has, so the book's text never leaves it
  ([decision 0018](decisions/0018-book-reader.md), [decision 0032](decisions/0032-read-aloud.md)).
- **No library file changed behind your back.** Library files are read-only to Tendshelf except for the
  two jobs the owner starts: writing details into a file (with a backup and Undo) and adding a book by
  upload (`AGENTS.md`).

## A new option has to earn its place

Every option is one more thing to explain, test and keep working on a phone. A feature that costs scan
time, page weight or binary size has a high bar: say what it costs and what it gives back. Advanced options
sit behind a disclosure, in plain words (`AGENTS.md`, "Accessibility and layout"). The test is the one in
"What wow means here": one binary, nothing to keep up to date, and a page that is ready on a mid-range
phone. Contributors should read [CONTRIBUTING.md](../CONTRIBUTING.md) before proposing a feature.
