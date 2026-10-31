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
| **The reader-listener** | Reads on the train, listens while walking or cooking; a language learner; a reader with dyslexia or low vision. | Add the ebook beside the audiobook, see the words light up as they are spoken, and switch between reading and listening without hunting for the place. |
| **The researcher** (admin, opt-in) | Studies narration style for TendVox calibration. | Measure pace, pauses, pitch and loudness of owned books, keep the numbers, delete the clips. |

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
8. "When I read along, I want the line being spoken to light up and the page to follow it, so that my
   eyes and ears stay together without effort."
9. "When I stop listening and pick up the text (or the other way round), I want to be at the same
   sentence, so that switching formats never costs me my place."

## What "wow" means here

Measurable, not adjectives. Each is an acceptance target for the phase that delivers it.

- **First sound in under one second** after tapping play on a book on the home network (server on a
  modest mini PC, phone on Wi-Fi). Library page interactive in under 300 ms on a mid-range phone.
- **Never lose your place.** Position saved every 15 s while playing, on pause, on page hide and on
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
- **The words follow the voice** (read-along, [read-along.md](read-along.md)): add an EPUB, PDF, DOCX or
  other DRM-free ebook to a book you own and, after one background job, the sentence being spoken is
  highlighted (and the word, where the browser allows it) and the page scrolls gently with it. Targets:
  at least 95 % of narrated sentences highlighted within ±250 ms of the voice (measured on a
  public-domain recording); the highlight moves within one animation frame of the player crossing a
  sentence start; tap any sentence and it plays from there;
  one **Back to the voice** button when you have scrolled away.
- **Switch formats without losing your place**: read on the train, press play on the walk home and the
  narrator starts at the sentence you were reading; stop listening and the text opens at the sentence
  you last heard. One position, shared by every device.
- **Reading comfort for everyone**: Focus line (one sentence at a time in large type), a
  dyslexia-friendly font option, sizes to 250 %, sepia and night themes, all at WCAG AA contrast.
- **Honest timing**: text that is not in the recording (front matter, an abridged cut, another edition's
  extra paragraph) is shown in grey, never highlighted with an invented time.
: one binary, under 100 MB RAM idle with a 2,000-book library (target; measure it),
  no background network traffic, nothing to keep up to date except the binary.
- **Beautiful on a phone first**, at home on a 27-inch monitor, readable in sunlight and at night.

## Not goals

- A standalone ebook library and comics (Audiobookshelf does them). Ebooks are admitted only as the
  text edition of an audiobook, for read-along ([decision 0001](decisions/0001-read-along.md)).
- Selling, DRM removal, or downloading from stores. Tendshelf refuses copy-protected audio and ebooks.
- A social network. Sharing is private, person to person.
- A cloud service run by us. Self-hosted only.
