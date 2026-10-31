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
- **Small and calm**: one binary, under 100 MB RAM idle with a 2,000-book library (target; measure it),
  no background network traffic, nothing to keep up to date except the binary.
- **Beautiful on a phone first**, at home on a 27-inch monitor, readable in sunlight and at night.

## Not goals

- Ebooks and comics (Audiobookshelf does them; Tendshelf stays focused on listening). Revisit later.
- Selling, DRM removal, or downloading from stores. Tendshelf refuses copy-protected files.
- A social network. Sharing is private, person to person.
- A cloud service run by us. Self-hosted only.
