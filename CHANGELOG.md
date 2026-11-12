# Changelog

Versions are `YEAR.RELEASE.BUGFIX` ([decision 0006](docs/decisions/0006-version-numbers.md)). Dates are UTC.

## Unreleased

### Added

- **Open, browse and play a podcast before following it.** Tap a search result or a suggested show to look at its episodes and play one; Follow is a separate button and only then does the show join your list.

### Changed

- **The Now playing side panel slides in and out on wide screens** instead of appearing at once.
- **The Chapters, Speed and Sleep sheets in the full player slide down when you close them** instead of vanishing.
- **Up next rows glide to their new places** when you move one up or down or remove one, instead of jumping.
- A show you tap in podcast search or the suggestions says "Opening…" while its feed is fetched, so the tap is seen to have worked.
- A station whose logo arrives a moment after it starts playing now shows that logo on the lock screen and in the system's media controls too, not only in the player.
- **Shelf view: every row is filled edge to edge.** A row holds as many books as fit and they share the room left over, so there is no gap at the end and nothing scrolls sideways. The last row is left alone: its books keep their own thickness.
- **A podcast's page on a phone no longer leaves an empty space under the picture.** The description and the buttons now run the full width below it.

## 2026.9.0

Released 2026-11-11. Radio plays HLS stations and stations you have not saved, Podcasts and Radio get a first-use page and starter lists, chapters inside an episode's file show in the player, and pages stop shifting as they open. The Android app is the same as in 2026.8.1 apart from its version.

### Added

- **Radio plays HLS stations.** Many public broadcasters publish only HLS. Add one by its address or pick it in search: the server reads the stream and plays it as ordinary audio, so the web player and the Android app need nothing new. Locked streams, video-only streams, recordings and sound formats Tendshelf can't play are refused with a plain reason when you add them.
- **Play works on stations you have not saved.** Search results and the popular stations now have Play next to Save. A played station appears under Recent, where you can save it later.

- **Podcasts and Radio have a first-use page and starter lists.** The Podcasts item is always there; until the owner turns podcasts or radio on it explains what that does and the owner turns them on with one button. Empty Shows and the podcast search offer a project-picked list of popular shows (mostly true crime), and Radio offers OndaCast's popular stations. Nothing is followed or saved until you tap.
- **Chapters inside an episode's file now show in the player.** Many podcasts put their chapters in the MP3 or M4A itself instead of a separate chapters file. When Tendshelf keeps an episode on the server, it now reads those chapters and shows them in the web player, in the offline copy and in the Android app, as long as the publisher did not already provide a chapters file.

### Podcast and radio pages: first-use fixes (2026-11-10)

- The Podcasts tabs have a search field at the top, like Radio, in place of the Find a podcast button.
- Popular stations and Popular shows (true crime first) always show under what you saved or follow, minus what you already have.
- A station without a logo, or with one too small to draw sharp, shows its initials on a colour in the card, search rows, player bar and full player. Logos OndaCast gives as a relative address are now fetched.
- Song titles from iHeart streams read "Artist - Song" instead of a row of codes; adverts show nothing.
- Radio keeps Podcasts marked in the navigation after you open it from the tabs.
- After you follow a show, its page fills in by itself when the episodes arrive. It no longer shows the feed address as a title.
- Podcast and station search results show their pictures. The server fetches them, so your browser never talks to the publishers.
- The show page follows the app's width and gutters, with a header that counts the episodes, shows the site name, clamps a long description under "More", and has one Play latest or Resume button.
- Radio is its own designed page: heading, a short intro, a search field, and stations as cards with a logo, Play and a star to keep.
- On a live station the bar's round button now changes to Play when you stop the stream and back to Stop when you play it again; the lock screen follows too, and a station's logo shows in the bar even if it was still being fetched when you pressed Play.
- The Radio tab no longer drops onto a second row on a phone, and the show header keeps its buttons on one row on a tablet.
- The Now playing panel on a wide screen now fills out for podcast episodes: details, chapters, show notes and the usual actions.

### Fixed

- **Chapters, the bookmark and the chapter ticks come back after a podcast episode.** After you had played an episode, the full player kept the episode's layout for the next book until a reload, so Chapters and the bookmark were missing.
- On a wide screen the bar's Chapters, Speed and Sleep buttons close their list when pressed again, besides the X on the list.
- **The rail and sidebar stay still on short pages.** Opening Search, Downloads or Shelves made the navigation move a few pixels up and back when the page had little on it.
- A popular show you already follow by its feed address is no longer suggested again (matched by its title).
- **The Downloads page no longer drops its content when it opens.** The storage and settings blocks used to appear after the page and push the rest down by about 160 pixels; they now keep their place from the start.
- **The book page no longer shifts when it opens.** The Download button used to appear late, sliding the other buttons sideways or pushing the tabs and text down; it now keeps its place.
- **Pages reached from the menu start exactly at the top.** A long page opened a few pixels scrolled, so its heading sat slightly too high.
- **The sidebar footer glides when the player bar slides away or back,** instead of jumping.
- **The Downloads page keeps still when your browser protects your storage.** The note about storage being cleared used to vanish a moment after the page appeared, pulling everything below it up by about 30 pixels; the page now remembers the browser's last answer and starts at its final height (the very first visit still has to ask).
- **The reader's footer no longer slides sideways when a book opens.** The page line and the progress ruler first drew at one width and then widened to the text's width (by 640 pixels on a wide screen); they now appear once the text is laid out.
- **Pages no longer blink and jump a moment after they appear.** Moving to another page drew it once, then about a quarter of a second later made it vanish and rise in again from a few pixels lower; now the rise-in starts with the first frame of the new page.

## 2026.8.1

Released 2026-11-10. The same app as 2026.8.0, now with the Android APK attached: the 2026.8.0 release build stopped before attaching it, because a release check mistook the new provider sign-in for the removed password sign-in.

## 2026.8.0

Released 2026-11-10. Podcasts and radio, on the web and in the Android app: follow shows, download episodes or let new ones arrive on their own, one Up next for books and episodes, and live stations with the song on air. Also sign-in with Authelia, Authentik or Keycloak, a Docker setup and an Activity page for owners.

### Added

- **Podcasts.** Follow a show by searching OndaCast or pasting its RSS address, or bring your shows from another app with an OPML file (and take them with you the same way). Shows, episodes, places, stars and history live in Tendshelf's own database, so an OndaCast outage only pauses search. Each show has its own speed and intro and outro skips, episodes have chapters and show notes, filters (All, New, Unplayed, In progress, Played) and a row menu, and Home gets a New episodes shelf. Podcasts are off until the owner turns them on under Settings, Podcasts and radio; guests never see them.
- **Podcast episodes play in the web player.** An episode plays in the same player as a book, through your server with seeking, keeps your place on every device, and the sleep timer can stop at the end of the episode. Episodes can be downloaded to the browser for offline listening, and the owner can let the server keep a size-limited cache and fetch new episodes on its own.
- **One Up next for books and episodes.** Play next and Add to queue work on episodes too, and books and episodes share one queue on every device.
- **Radio.** Find stations through OndaCast or paste a stream address, save favourites with their logos, and see the song on air. Stations play live through your server, and saved stations and your recent ones keep working when OndaCast is down.
- **Podcast episodes play in the Android app.** An episode plays in the same background player as a book: lock screen and notification show its title, the show and the show's picture, speed and skip intro/outro follow the show's settings from the first sound, chapters show under the title, the sleep timer can end at the end of the episode, and your place is saved offline-safe and shared with the web. Needs podcasts turned on by the owner; downloads and the shared queue come later.
- **Podcast episodes can be downloaded in the Android app, and new ones can arrive on their own.** Press Download on an episode page; it plays with no connection and keeps your place. Under Downloads, "Podcasts on this phone" turns on, per show, downloading the newest episodes on Wi-Fi only; played ones are removed, downloads you made yourself never are, and a notification tells you when new ones arrive. Play next works on episodes, and the next item in Up next starts after an episode, book or episode.
- **Podcasts in the car, on the widget and on the headset button (Android).** In Android Auto, Shelves lists Podcasts first, with New, Downloaded and Shows; a show opens its newest episodes, each with the show's picture, length and a played mark, and a row can be marked played or unplayed and starred. The home-screen widget shows the playing episode, its show and picture, and Play resumes it after a restart, as does a headset button. Downloaded stays empty until episode downloads arrive.
- **Radio plays natively in the Android app.** A saved station plays in the background player like a book: the lock screen and notification show the station name, the song on air (the server is asked every 15 seconds) and the station picture, with no seek bar or skip. Pause lets go of the stream and Play rejoins the live edge, a busy server is retried and then explained in plain words, the sleep timer works, and nothing is saved as progress or touches your queue. Android Auto gets a Radio row (Your stations, Recently played), the widget shows the station with Live, and the media button resumes the last station.
- **Sign in with Authelia, Authentik or Keycloak.** The owner sets up one OpenID Connect provider under Settings, Your server, Sign-in: its name, address, client ID and secret. People then see a "Sign in with ..." button above the password form, and can link or unlink their account from their Account page. The password form always stays, so a provider that is down never locks anyone out. Owners and admins link from their Account; the owner can also let verified emails link members and guests, or make accounts for new people.
- **Sign in with a provider in the Android app.** The provider's pages open inside the app's sign-in window (the server names them at `/login/oidc/origins`), the sign-in returns to the server's page, and every other link still opens in the phone's browser.
- **Run Tendshelf with Docker.** The repository now has a Dockerfile and a compose file, so `docker compose up` builds and starts the server with ffmpeg included, your data in a folder you choose and your books mounted read-only. The deploy guide explains HTTPS, uploads, file ownership and upgrades.
- **An Activity page for owners and admins.** Under Settings, Your server, it shows what people did on the server in plain words (who signed in, who invited whom, who changed a role), newest first, filtered by person or by kind. It shows no addresses or links that could sign anyone in.

## 2026.7.0

Released 2026-11-09. Books read in pages by default, with a page turn that flips like a book, a ribbon to mark a page and real page numbers; a More options menu on the book page; and, on Android, the next book downloads on its own and a home-screen widget.

### Added

- **A page turn that feels like a book.** Turning a page now lifts the page and turns it over, with the text on both sides, a shadow that falls on the page underneath and light that changes as the page turns. On a wide screen the right page turns over onto the left; on a phone the page folds away to the side. You can also drag the page with a finger or a pen, and let go to finish the turn or put it back. In Reading settings, "Page turn" offers Flip, Slide and None; if the screen is too slow for the flip, the reader quietly slides instead, and choosing Flip again gives it another chance. If your device asks for reduced motion, pages turn without any movement.

### Fixes

- **The sides of the page now say they turn it.** On a computer, a small arrow sits at each side of the page, grows clearer when you point at it, and the page's corner lifts a little. Click it, or reach it with Tab and press Enter, and the page turns. On a phone or tablet you are shown once, with two arrows and one sentence, that tapping the sides or swiping turns the page and tapping the middle brings the menu.
- **You can mark a page.** Every page has a bookmark ribbon at its outer top corner (on a two-page spread, one on each page). Press it to bookmark the page and the ribbon fills in; press it again to take the bookmark away. The bookmark button in the bar does the same for the first page. Your bookmarks stay in the marks list, as before, and a screen reader is told "Page 12 of about 340" after you turn.
- **Pages are how you read now, on every screen.** On a wide window the book used to scroll like a web page; now it is a page with a number, and a person who prefers scrolling can still choose Scroll in Reading settings ("Automatic" is gone; it counts as Pages). The page is a comfortable width, so lines no longer run across the whole window. On a large screen it is a sheet of paper on a softly tinted desk, with two facing pages when the window is wide enough.
- **The page number.** The bar's footer says "Page 12 of about 340" (the number is for this screen and text size; "about" goes away once you have been through the whole book on this device), and the book's own page number comes second when it has one. Under the text, a small line shows the page and how many pages are left in the chapter.
- **On a phone the bar gets out of the way.** The book opens with the bar and the controls at the bottom hidden, so the text has all the room, and turning a page hides them again. Tap the middle to show them. The scroll layout also gives the bottom controls' room back to the text when they are hidden.
- **A sheet's dim backdrop covers the whole window** on a wide screen; a pale strip no longer stays beside it.

- **Reader sheets keep their title and close button in view.** Contents, Reading settings and the other sheets scroll only underneath their handle and heading, and the scrollbar stays inside the rounded box. Rows in Contents line up even when a chapter has no title.
- **The big first letter only opens real paragraphs.** It no longer lands on a dedication, an epigraph, opening pages or a list of links, and short lines of verse no longer start with an indent.
- **"This part did not load" shows once and goes away.** A part that fails to load is tried again quietly first. If it still fails you see one message, not a pile of them, and it is gone when you leave the book or when the part loads. "Try again" now really asks again, and if you were signed out the message says so.
- **The reader bar no longer vanishes by itself.** Clicking in the text with a mouse leaves it alone; a finger tap in the middle still hides and shows it. A hidden bar leaves a small "Show the bar" button at the top, and with "Hide the bar after a moment" on, moving the mouse to the top or bottom, or pressing a key, brings it back. In the scroll layout a hidden bar gives its room to the text.

- **Pages no longer look squeezed while they load.** Moving between pages, the Library in particular, used to draw the new page narrow for a moment before it settled; now it appears at its real size.
- **The desktop app no longer closes when you change page** on some Linux graphics setups.
- **The book page holds together at 200 % text.** Long names are cut with "…" instead of pushing the page sideways, and the buttons stay on screen.
- **The book tabs stay on one row on a phone,** and slide sideways when there are more than fit.
- **On Android, another book's button no longer says Pause** after the app moves on to a different book.

### What you can do

- **A "More options" button on the book page.** The three dots beside Add to shelf and Mark as finished hold the rarer things: Go to series,
  Share, Podcast feed, Change cover and Edit details, each only if you may use it. The series page keeps its Podcast feed there too. On a phone the
  series now sits above the title as one line (the name is cut with "…" when long, "Book 5 of 5" always stays), and the length and year share one row.

- **The next book is ready when you need it (Android app).** Turn on "Download the next book on its own" in Settings, under Playback. When the book you
  are listening to has two hours left or less and you are on Wi-Fi, the next one in Up next (or the next in its series) downloads, so it is on the phone
  when this one ends. It never uses mobile data, keeps at most 5 GB this way, leaves 2 GB free, and never removes a book you have finished. It starts while a book
  is playing or the app is open, and a started download finishes with the screen off. It is off until you turn it on. When a book ends and the phone
  cannot reach the server, the next book plays from the phone, and it plays even with no connection.

- **A widget for your home screen (Android).** Add the Tendshelf widget to see your book's cover, its title and the chapter, with Play or
  Pause, Back and Forward. Tap the cover or the words to open the book. With nothing playing it shows the last book and Play carries on from
  your place. Make it narrower and it drops the chapter and the skip buttons first. Signing out clears it.

## 2026.6.0

Released 2026-11-09. It brings podcast feeds, read aloud, the Shelf view, whole-series downloads and pages that use the whole window.

### What you can do

- **Pages use the whole window.** The book page, Search, Downloads, the Libraries page and other lists, your listening pages and Up next no longer sit in a narrow strip with empty space around them. On a wide screen they fill the window and show more columns; reading text stays a comfortable width, and forms stay narrow. Phones look the same.
- **Download a whole series or shelf in one tap.** The series page and each shelf now have a "Download all" button, in the browser and in the
  app. It asks first ("Download 12 books? About 34 GB."), skips books you already have, says if they will not fit, shows "Downloading 3 of 12"
  with a Stop button, and tells you at the end how many did not download. It saves the audio; a book's text stays with its own Download.
- **See your library as a shelf.** The Library has a third view, Shelf, where each book stands on a plank as thick as it is long, with your place marked on it. Home's shelf now runs to the edge of the window and scrolls sideways. Home shows the same spines. Your choice of Grid, List or Shelf is remembered.

- **Find your account and settings at once.** Profile now opens with two buttons, Account and Settings, each with a short menu of
  everything inside. On a computer your picture and name stay at the bottom of the side menu.
- **Listen in a podcast app.** A book or a series with audio now has a "Podcast feed" button, for people who may download it. It makes a
  private address you paste into AntennaPod, Pocket Casts, Apple Podcasts or Overcast; the app then plays the original audio, chapters
  included. The address is shown once, with a QR code, and each address is listed on Your devices with the book or series it carries
  and when an app last used it, where you can turn it off. Removing someone's right to download, disabling them or a password reset
  stops their feeds too.
- **Have a book read aloud.** In the reader, "Read aloud" speaks the book with a voice already on your computer or phone,
  sentence by sentence, and marks the sentence being read. Play, Pause, Back one sentence and Forward one sentence sit in the
  reader's bottom bar; "Voice and speed" picks the voice and a speed from 0.8× to 2×. If you scroll away, "Back to the voice"
  takes you back. It keeps going into the next chapter and stops at the end of the book. The text never leaves your device, and
  only voices that run on the device are offered. In the Android app it uses the phone's own voices.
- **Skip silence in the Android app.** Settings, Playback has "Skip silence", which shortens long pauses while you listen. Your place
  and chapters stay where they were; the time it counts as listened is the time you actually spent. It works in the app only.
- **Turn pages with the volume buttons in the Android app.** In the reader's settings, under More, "Turn pages with the volume buttons"
  makes volume down the next page and volume up the previous one, also while a book plays. Everywhere else the buttons change the volume.
- **The screen stays on while you read in the Android app**, when the reader's keep-awake setting is on. As in the browser, it lets the screen sleep again after
  10 minutes without a touch, and when you leave the reader.

- **Name your devices.** On Your devices, each browser sign-in and each phone or desktop app has a "Name" button, so you can tell
  "Kitchen laptop" from "Work PC". An empty name on a browser goes back to the usual label; an app always keeps a name. People who
  manage accounts see the same names on the Devices tab. "Add a phone" now sits inside the devices card instead of running into it.
- **Listen up to 3.5 times as fast.** The speed sheet's fine slider and the usual speed in Settings now go up to 3.5×.

### Upgrading

This version uses database schema 38 (it was 35 in 2026.5.0).

## 2026.5.0

Released 2026-11-08. It brings Contributors, sign-in links for people without a password, and waveforms for very long books.

### What you can do

- **Invite someone as a Contributor.** Next to Listener, Member and Admin there is now Contributor: a person who listens like a
  member and can also add books. Their books go into a folder of their own inside the upload folder, and they can fix the details,
  look up the book online, change the cover and remove the books they added, never anyone else's. They add books only to a whole
  library (or the whole server) that was shared with them and where uploads are on. You can make a member a Contributor, or the
  other way round, on the person's page.
- **See who added a book.** Owners and admins get an "Added by" choice in the Library's Filter menu and a line on the book's page;
  a Contributor sees "You" and "Added by you" for their own books. Nobody else is told who added a book.
- **Remove all the books a person added.** On a person's page, "Books they added" shows how many there are, a link to see them in
  the Library and "Remove all their books". Tendshelf asks first, deletes only the files it placed for that person that nobody has
  changed, and tells you which books stayed and why.
- **Get someone back in with a sign-in link.** On a person's page, an admin can make a sign-in link that works once, for
  24 hours. It is for someone who never chose a password or forgot it: they open the link, confirm, and pick a new password
  within the hour. Making a new link ends the old one; the owner's account has no links. The sign-in page now says
  "No password yet, or forgot it? Ask whoever runs this server for a sign-in link." A person's page shows their username.

### Fixed

- **Books longer than 48 hours get a waveform.** The player bar showed none for them; it now works for books up to 500 hours.

### For people who package Tendshelf

- **The Android app has an F-Droid recipe** (`android/fdroid/app.tendshelf.android.yml`). It builds from the tagged source and
  checks the result against the signed APK on the release page. How to submit it is in `docs/android.md`.

### Upgrading

This release uses database schema 35 (it was 33 in 2026.4.0). Before Tendshelf upgrades an existing database it writes a
verified copy to `backups` in the data folder, and an older program refuses a database made by this one.

## 2026.4.0

Released 2026-11-07. It brings the Android app, adding a phone with a QR code, a chapter bar you can point at, and a
full player that slides up and swipes away.

### What you can do

- **The full player slides up from the bottom.** On a phone or tablet it now rises over a quarter of a second, the way it
  slides down when you close it, instead of fading in. The arrow, Escape and Back slide it down too.
- **Pick a chapter on the chapter bar.** Point at the thin chapter line (in the player, the side player, on Home and on a book's
  page) and a small label names the chapter and where it starts; hold a finger on it to see the same. Click or tap to go to
  that chapter (Go back is offered); on Home it starts the book there. Keyboard: arrows choose, Enter goes.
- **Swipe down to minimise the full player.** On a phone or tablet, drag down from anywhere on the player that is not a
  button, a slider or a list and it follows your finger and fades; let go past a quarter of the screen, or flick, and it
  closes, otherwise it springs back. The arrow is still there. Playback is not touched.
- **Add a phone with a QR code.** Under Your devices, "Add a phone" shows a code that works once for 3 minutes. Point the phone's
  camera at it, open the app, and type the number the phone shows into the page. No password is typed on the phone.
  Invitation links show a QR as well, so someone beside you can join from their phone.
  On an address that only works on this computer (localhost) the page says so instead of drawing a code.
- **A phone already signed in to the app can no longer join an invitation by mistake**, which used to leave the page and
  the app signed in as two different people.
- **Book details search Audible first, and other places only when you choose them.** A normal search asks only Audible
  (through Audnexus). "Search more places" now has a box for Open Library, one for Apple Books and one for All, and
  Tendshelf remembers your choice. The "Look up" button on Libraries & scanning uses the same choice. Google Books is
  gone: it refused Tendshelf's requests, and Tendshelf will not ask you for an API key. Each place now says what happened in
  plain words, such as "Open Library is busy right now, try again later." or "Could not reach Apple Books.", and one
  place failing never hides what another found.

- **Tendshelf for Android has a release build.** `make apk` makes an APK whose version comes from the release tag, with
  a proper icon. It is signed with your own key when you provide one and left clearly unsigned when you do not. Two builds
  of the same source give the same APK. The steps, including how to make your own key and how to upgrade, are in
  `docs/android.md`. A phone with a test build needs one uninstall before the first release APK.

### Upgrading

This release uses database schema 33 (it was 32 in 2026.3.0). Before Tendshelf upgrades an existing database it writes a
verified copy to `backups` in the data folder, and an older program refuses a database made by this one.

## 2026.3.0

Released 2026-11-06. It brings library care, chapter editing, reading apps, a clearer Library, and a desktop app with its own title bar.

### What you can do

- **Library filters are easier to use.** One Filter menu now lists every kind (Author, Narrator, Series, Category,
  Length, Format) and its choices together, so choosing a value is one step. While you filter, a row under the
  controls shows one chip per filter, each with a cross that removes only that filter, and Clear all next to them
  (it keeps your sort and view). Sort is its own control ("Sort: Recently added"), Show keeps only the listening
  states, and the header reads "15 of 60 books" instead of a separate count. Saved links keep working.
- **Look up the books that need it.** On Libraries & scanning, "Look up" now asks which books: those missing details (the
  default), those not looked up yet, or all, each with how many it covers. A book you reviewed and accepted is not asked
  about again. The page is organised by library, with settings that save as you change them. The books that need a look
  moved to Library health, which opens with a count and a link to each group.

- **The desktop window stops at 1,920 px.** The pages already stop there, so a wider window on an ultrawide monitor
  only showed a blank strip. Dragging an edge or corner stops at 1,920 px, and Maximise fits the window to the screen:
  on a wider screen it is 1,920 px wide and as high as the screen, and pressing it again goes back.

- **New books appear by themselves.** Tendshelf watches your library folders and scans after nothing has changed
  for 45 seconds, so a book that is still being copied is not read half written. It also scans on a schedule (every
  6 hours unless you choose another time). A network folder is only checked by the schedule.
- **Whole backups.** The owner can choose a folder outside the data folder and the libraries. Tendshelf copies the
  database and the cover and ebook files there, checks each copy, and keeps deleted files for 30 days. Restoring is by
  copying; the Backups page lists the steps.
- **Chapter editor.** Change the start and title of each chapter, add or delete chapters, move them all by some
  time, or reset to the file's own. Edited chapters survive a rescan.
- **Chapter lookup.** Ask an online source for a book's chapter titles and times by ASIN, compare them, and use
  the titles and times or the titles only. Nothing is stored until you save.
- **Duplicates and merge.** The owner and admins see books that look like the same audiobook, merge them (places,
  bookmarks, highlights, shelves and shares move to the kept book), or mark them "Not the same". A merged copy can
  be shown again.
- **Library health.** One page lists books with missing files, files that cannot be opened, no chapters, no cover,
  no online match or chapters past the end, each with a link to fix it.
- **App keys and OPDS.** Make a key on Your devices and add your library to an ebook reader app that speaks OPDS
  (a catalogue of your books, search and downloads).
- **KOReader sync.** A second key lets KOReader push and pull your reading place for EPUB and PDF books. The place
  lands near where you were, not on it. It has not yet been tried with a real KOReader.
- **Clips.** Give a bookmark an end time to keep a stretch of a book, then press "Play this clip" and it stops at the
  end. Bookmarks can be filtered by kind and are exported with their kind and length.
- **Listening history.** "Where was I?" shows your listening sessions by day, with a button to go back to that
  moment.
- **All my highlights.** Every highlight and note from every book in one place, with search, colour and book
  filters, and downloads as Markdown or JSON.
- **Desktop app, new look.** The window has its own title bar (back, forward, where you are, now playing, minimise,
  maximise, close) on a real window with the system's shadow, corners and resize edge, new icons, and a More menu
  with change mode or server, zoom, Keep playing and About. The tray shows the book that is playing with Pause,
  Skip back and Skip forward, and a notification tells you when a book ends or the sleep timer stops it while the
  window is not in front.
- **Drop a book.** Drag audio or ebook files onto the desktop window to open Add books with them already chosen
  (owner or admin, in a library with uploads on). A folder is not accepted; copy-protected files are refused as before.

### Known limits

- Not tried on a real computer yet: sound from the desktop app in a hidden window, media keys, tray and
  notification clicks, a drag from the file manager, the icons on a panel, X11, and a long playback on the build
  without C. See [docs/manual-checks.md](docs/manual-checks.md).
- KOReader sync has not met a real KOReader.
- Whole backups are not encrypted, and there is no restore button.
- Library health only reads; it does not fix.
- Merged copies leave their folders on disk; there is no merge across libraries.
- OPDS has no OPDS 2.0, no audio and no per-shelf feeds.

### Upgrading

This release uses database schema 32 (it was 24 in 2026.2.0). Before Tendshelf upgrades an existing database it
writes a verified copy to `backups` in the data folder, and an older program refuses a database made by this one.
Watching is on for every library after the upgrade, and a library with no schedule gets a scan every six hours; both
are read-only on your files and can be changed under Libraries.

## 2026.2.0

Released 2026-11-06. It brings offline use, uploads, a book reader, ebooks as books, and a desktop app.

### What you can do

- **Speed for each book, and a rewind that fits the pause.** The Speed sheet now remembers the speed for the book you are in, on every device, and can make it your usual speed. When you press play again the player steps back 2, 5, 10 or 20 seconds, the longer you were away. After a chapter jump, a long seek or a scrub, "Jumped too far?" offers a way back.
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

This release uses database schema 24 (it was 9 in 2026.1.0). Before Tendshelf upgrades an existing database it
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
