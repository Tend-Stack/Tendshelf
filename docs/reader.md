# The reader

Tendshelf reads DRM-free ebooks itself ([decision 0018](decisions/0018-book-reader.md), which retired read-along and
its alignment worker; plan in [plans/m5-book-reader.md](plans/m5-book-reader.md)). The only external programs are
`ffmpeg` and `ffprobe`. Phase history is in [roadmap.md](roadmap.md).

## Rules

1. Ebook files are the user's: library ebooks are never written, renamed or deleted; an uploaded ebook is kept
   byte-identical as Tendshelf's own copy. Everything derived (the canonical document) can be rebuilt from the original.
2. Copy-protected ebooks are refused with a plain message and never decrypted, converted or parsed past the check.
3. Reading needs its own permission (`read_text`), separate from listening. Highlights, notes and bookmarks are private
   to the person who made them.
4. The reader is server-rendered (templ and HTMX). The publisher's HTML, CSS, fonts and scripts never reach the browser.

## Formats (`internal/ebook`)

One parser per format, all to the same canonical document: EPUB 2 and 3, PDF (text layer only, reflowed), plain text and
Markdown, DOCX, HTML, FB2 (and `.fb2.zip`), MOBI and AZW3 (DRM-free only). Files come from the scanner (an ebook inside
a book's folder), an upload on the book's Text tab (`<data>/ebooks/<sha256>.<ext>`, 200 MB cap) or the Add books page.

## Refusals

Checked before any content is parsed, with the message "This ebook is copy-protected, so Tendshelf will not open it."
(`refused_reason='drm'`): EPUB with `META-INF/rights.xml` (Adobe ADEPT), `sinf.xml` (Apple), `license.lcpl` (Readium
LCP) or an `encryption.xml` that encrypts anything but font obfuscation; PDF with an `/Encrypt` dictionary; encrypted
MOBI; any KFX; password-protected Office files.

Hostile-file bounds: 200 MB file, 1 GiB uncompressed, 10,000 zip entries, compression ratio over 100, no extraction to
disk, no DTD entity expansion, a 60 s parse deadline and `recover()` around each parser, 2 M words per edition.

## The canonical document

`tendshelf-doc-v1`, gzip JSON at `<data>/text/<doc_sha256>.json.gz`, immutable. Chapters hold blocks (paragraph,
heading, quote, list item, image, note, preformatted, rule); a block holds its text, style runs, sentences and words.
Ids: block `c{chapter}.p{block}`, sentence `c{chapter}.p{block}.s{n}`. Offsets are UTF-16 code units of the block text,
what the browser counts. The same file and recipe give the same bytes and ids. A new parser recipe never rewrites a
document; "Read the ebook again" makes a new one and re-anchors highlights by their quote.

## The reader page (`internal/web`)

`GET /books/{id}/read` takes `ch`, `at` (a sentence id), `off` and `sheet` (`contents`, `marks`, `reading`); an old
`mode=` is ignored. A chapter loads at a time (`/hx/books/{id}/read/chapters/{idx}`); the next one is requested as the
end comes into view. Highlights, notes and bookmarks are stored as a sentence id and an offset plus the quote. Reading
settings (font, size, line spacing, theme) are `reader.v1` in `user_settings`.
