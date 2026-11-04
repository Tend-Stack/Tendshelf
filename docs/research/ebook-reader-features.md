# What makes an ebook reader great (research, 2026-11-04)

Input to the reader plan. Web evidence is thin (feature guides, forum threads, the foliate-js README); items marked
(judgement) come from product knowledge, and self-hosting complaints are anecdotal. Features are described in our own
words; nothing here copies a product's name or look. Sentence-level read and listen is out of scope
(read-along was dropped, 2026-11-04).

## Must have

| Feature | Why readers care | Size |
| --- | --- | --- |
| Resume exactly, per person, across devices (anchor plus percent, never a page number) | The defining feature of the big readers | M |
| Reading settings that stick per person: size, font, spacing, margins, justify and hyphenation, themes | The first thing every reader judges | M |
| Comfortable measure: about 50-75 characters, 45 on a phone; line height 1.4-1.6 | Research supports the range | S |
| Pages and scroll, switchable without losing place | People want both | M |
| Contents, progress bar, time left in chapter and book from the person's own measured speed | The most-cited small feature; fixed speeds were about double the real time | M |
| Tap zones, keys, swipe, swappable for left-handed use | One-handed reading | S |
| Footnotes as a popup with a way back (judgement) | Jumping loses your place | M |
| Highlights, notes and bookmarks with colours and export | Partly built | M |
| Search in the book with a jump to the hit | Expected | M |
| Plain-word errors for damaged or locked books | Refused, never decoded | S |

## What makes it great

| Feature | Notes | Size |
| --- | --- | --- |
| Offline look-up: a word's definition from a dictionary file the owner adds, otherwise a web search the reader chooses | Saved to a personal word list | M |
| Reading stats the person owns: time, speed, streak, yearly goal, finish dates | Private; an optional family view | M |
| Print page numbers when the book has them, otherwise stable locations | Page counts change with settings | M |
| Skim without losing your place: scrubber with chapter ticks and a "back to where I was" chip | | M |
| Characters and places list, spoiler-free | Hold back until the quality is good | L |
| Comfort: warm tint on a schedule, hidden chrome, brightness gesture, keep screen on | | S-M |
| Offline reading through the service worker, per person | Same model as audio | M |
| Send a book by share link or email | Upload exists | M |
| Collections, series, what to read next | | M |

## Later

OPDS feed (M). KOReader progress sync with an app key, never the login password (M). Fixed-layout EPUB and comics, a
separate renderer (L). Right-to-left and vertical text (L). Dictionary packs the owner imports (M).

## Pages and scroll

Offer both; pages by default on phone and tablet, scroll on desktop. Paginate with CSS columns, one chapter at a time:
`column-width` set to the page, pages turned through `scrollLeft`, the visible range found by bisecting text nodes.
Settings go through CSSOM and custom properties, which the CSP allows. Study foliate-js and Readium CSS for design;
write our own small module and check licences before porting anything.

Pitfalls: long chapters lay out slowly (split, debounce resize); publisher `vw`/`vh` and fixed heights break columns;
cap images and tables to the page box; never promise page numbers; scripted EPUB content must not run; test WebKit
(Wails); embedded fonts may be obfuscated; right-to-left and vertical text change the axis.

## Mistakes to avoid

1. Progress sync behind a paywall or a separate service.
2. No way to add books from the web.
3. Requiring a strict folder layout: read metadata from the file first.
4. Splitting audio and ebooks into two apps; one book, two formats, is Tendshelf's advantage.
5. A web reader whose position other clients cannot read.
6. Features that only work for some file types.
7. Time estimates that are far off: start modest and learn per person.
8. Shipping a half-working character index.
9. Settings locked to one device.
10. Silent failure on odd EPUBs: say so in plain words and keep the book downloadable.

## Sources

- https://www.aboutamazon.com/md/news/devices/the-6-best-kindle-features-of-all-time-according-to-a-kindle-superfan
- https://github.com/pazos/koreader and https://koreader.rocks/user_guide/
- https://baymard.com/research-articles/line-length-readability and https://journals.uc.edu/index.php/vl/article/view/5765
- https://ebookfriendly.com/6-most-useful-kindle-features-of-all-time-according-to-the-kindle-chronicles-host/
- https://goodereader.com/blog/?p=263641
- https://blog.the-ebook-reader.com/2012/12/23/new-kobo-firmware-2-3-adds-in-book-reading-stats-to-kobo-ereaders/
- Self-hosting threads: https://lemmy.klein.ruhr/post/228440/930425, https://lemmy.nekusoul.de/post/1674859/4712072, https://lemm.ee/post/90828
- https://github.com/jberlyn/kosync-dotnet
- https://github.com/johnfactotum/foliate-js
- https://github.com/readium/readium-css
