# 0015 Adding books by upload, straight into a library folder

Status: proposed (2026-11-04). Plan: `docs/plans/upload-books.md`. Board: `docs/design/boards/UploadBooks.html`.
Amended by [0018](0018-book-reader.md) (2026-11-04): uploads may also hold ebooks.
Amends AGENTS.md "Audio files are the user's" and architecture invariant 1.

## Context
- Today a library is only folders the scanner reads; library files are read-only except for the write-details job.
- The operator decided on 2026-11-04 that uploads go straight into an existing library folder (Tendshelf makes
  the book's folder there) and that one book or several can be uploaded at once.
- What exists and is reused (nothing is duplicated):
  - `internal/scan`: `walk`, `GroupFiles`, the `scan.library` job (concurrency 1), refusal of `.aax/.aaxc/.aa/.m4p`
    by extension (`media.Classify`) and of encrypted streams by probe (`media.Probe.Encrypted`), `RefusalText`.
  - `internal/metadata`: `FromTags`, `ParseBookName`, `FromPath` (names for the folder; `FromPath` reads our
    `Author/Series/NN - Title` layout back) and the `metadata.local` job and automatic lookups after a book settles.
  - `internal/jobs`: persisted jobs, leases, fencing, `/hx/jobs/{id}` views by kind (`routes_jobs.go jobViews`).
  - `internal/media`: `QuickHash`, `FileSHA256`, `FreeBytes`, `Tools.Probe` (fixed args, protocol whitelist).
  - The M4 staging pattern `<data>/covers/incoming/` with a 24 h sweep (`mediajobs.sweepStaged`); the
    write-details rules (same-directory temporary name, free-space check, a ledger table that outlives jobs).
  - The lazy module pattern (`offline.js` imports `downloads.js`, `player-full.js` imports `eyes-free.js`).
  - `share.Can`, CSRF header `X-CSRF-Token`, `api.Write`/`api.Error` JSON helpers, `db.Audit`.
  - The R1 ebook upload (designed, not built): uploads kept byte-identical under `<data>/ebooks/`, owner and admin
    only. Audio uploads differ on purpose: they land in the library folder, because the operator wants the files
    where their other tools see them.
- There is no upload or import board in the handoff; this decision adds `docs/design/boards/UploadBooks.html`.

## Invariants
1. Tendshelf writes into a library folder only through `internal/upload`, only into the one folder the owner
   picked for that library (`libraries.upload_folder_id`; NULL = off, the default).
2. It only creates. It never overwrites, renames or deletes a path it did not create. Every created path is
   recorded in `upload_paths` before it is created; a path it cannot prove it created is never deleted.
3. A book appears in the library folder whole or not at all: it is built in a hidden `.tendshelf-upload-<id>`
   folder (the scanner skips dot names) and published by one rename to a name that does not exist.
4. Copy-protected uploads never reach the library folder; their staged bytes are deleted.
5. Every request is authorized by `share.Can` and carries CSRF; members, guests and share links can never upload.
6. Bytes on disk are never claimed by the database before they are synced; resume is driven by the server's offset.
7. Disk space is checked before bytes are accepted and before placement; a full disk fails cleanly and leaves
   nothing half-written in the library folder.
8. No new module, no new outbound connection, no request blocks on long work.

## Decision
1. **Setting, per library, off by default.** `libraries.upload_folder_id` (migration 0014) names the folder of that
   library that receives uploads. Only the **owner** changes it (`share.ManageServer`, like `online.enabled`), because
   it lets the server write to the owner's disk. Turning it on checks the folder is writable by creating and
   removing `.tendshelf-write-check-<random>`. Admins see the setting read-only.
2. **Who uploads.** New action `share.AddBooks` (`books.add`) on the library target: owner and admin (the role switch
   in `Can`), never by grant. Refusal is 404, like every other route.
3. **Where the screen lives: one page, two doors.** `GET /libraries/{id}/add` ("Add books"). Doors: an "Add books"
   button in the Library page header (owner and admin, when that library, or for `/libraries` at least one library,
   takes uploads; with several such libraries the page shows a library chooser) and an "Add books" link in the new
   Libraries admin section "Adding books from this device", which also holds the setting.
4. **Inputs.** U2: "Choose files" (all chosen files make one book) and "Choose a folder" (`webkitdirectory`; the folder
   is one book, its subfolders such as CD1 come along). U3: `.zip` (each one book), "This folder holds several books"
   (each top-level subfolder is one book) and drag and drop on desktop.
5. **What is placed.** Audio (`media.KindAudio`), pictures (`.jpg .jpeg .png .webp`) and sidecars
   (`.opf .nfo .txt .json .cue`). Other files are left out and counted ("2 other files were left out"). Names starting
   with `.` and the scanner's `skipName` list are left out. A book with any `.aax .aaxc .aa .m4p` file is refused
   before any byte is sent (the client checks; the server refuses the manifest with `copy_protected`). After upload,
   every staged audio file is probed; one encrypted stream refuses the whole book and deletes its staged bytes.
6. **Transfer.** Chunked, resumable, sequential per file: `PUT` of at most 8 MiB at the server's current offset,
   optional `X-Chunk-SHA256` (the client sends it when `crypto.subtle` exists, that is in a secure context; on plain
   http over the LAN, size verification only). A chunk is written at its offset, synced, then the offset is advanced
   in one conditional update. Staging: `<data>/uploads/incoming/<uploadID>/<fileID>.part`, mode 0600. An upload with
   no chunk for 24 h expires and its staging is removed (hourly sweep). After a reload the person picks the same files
   again; the client matches them by path, size and modified time and carries on from the server's offsets.
7. **Limits.** File 20 GiB; book 50 GiB and 1,000 files; path 1,024 bytes and 8 segments; 3 receiving uploads per
   person. Staging needs free space for the remaining bytes of every receiving upload plus 1 GiB; placement needs the
   book's size plus 5 % plus 1 GiB in the library folder's filesystem (once when staging and library share a
   filesystem, because the file is then moved, not copied).
8. **Layout.** `Author/Title/`, or `Author/Series/NN - Title/` when a series is known (NN: integer sequences padded to
   two digits, `1.5` becomes `01.5`). Names come from the first audio file's tags (`metadata.FromTags`), then the
   picked folder or file name (`metadata.ParseBookName`), then "Unknown author" and "Untitled book". Each segment is
   made safe on every filesystem (rules in the plan, `upload.SafeName`). A taken book folder gets " (2)" to " (99)".
   An existing ancestor is reused only when it is a real directory (not a link) that is not inside a book and holds no
   audio directly; otherwise the layout falls back to `Author/Title/`, then to `Author - Title/` at the folder's top,
   so a new folder never turns an existing book into a container (`GroupFiles` rule) and never splits it.
9. **Publish.** The hidden folder is created inside the final parent; files are moved there (same filesystem) or copied
   with `O_EXCL`, synced and checked (size, and SHA-256 recomputed from the written copy when copied). Then `Lstat` of
   the final name must say "does not exist" and `os.Rename` publishes. Residual risk, accepted and named: on Linux and
   macOS, an **empty** directory created by someone else between the check and the rename would be replaced; a file or
   a non-empty folder never can be (rename fails). Go's standard library has no portable no-replace rename.
10. **Import.** Placement enqueues the existing `scan.library` job with a scope (`folder_id`, `subdir`): the scanner
   walks only the new book folder, so nothing else can be marked missing, and scoped and full scans never run at once
   (kind concurrency 1). The usual probe, `metadata.local` and automatic lookup follow.
11. **Remove.** "Remove this book" (book page, owner and admin) is offered only when every live audio file of the book
   is a `present` row in `upload_paths`. It deletes each recorded file whose size, mtime and quick hash are unchanged,
   keeps and names any other, removes the recorded folders that are now empty (deepest first) and deletes the book
   rows when no audio is left. This is the only delete path.
12. **JavaScript.** One lazy module `js/upload.js`, about 5 KB gzip, imported by a generic 8-line loader in `shell.js`
   (`[data-ts-module]`) only on the Add books page. No-JavaScript fallback: **not built**; the page says
   "Adding books needs JavaScript in this browser. You can also copy a book's folder into {path}, then press Scan."
   Reason: the CSRF middleware parses multipart bodies in memory up to their limit, so a GB form upload would need a
   CSRF exception and a second, non-resumable path.
13. **Audit.** `library.uploads` (setting), `upload.place` (published path), `upload.refuse`, `upload.remove`.

## Copy (new strings; add to docs/copy-additions.md in U2)
- Page: "Add books" · "Books go into {path}, in a folder for each author." · "Choose files" · "Choose a folder" ·
  "Drop audio files or a folder here" (U3) · "Audio files, pictures and book details files. Up to 50 GB a book."
- Rows: "{sent} of {total} · about {time} left" · "Pause" · "Resume" · "Paused. {sent} of {total} sent." ·
  "Stopped at {pct} %. Choose the same files again to carry on." · "Choose files again" · "Cancel upload" ·
  "Checking the files" · "Putting it in {folder}" · "Adding it to your library" · "Added to {library}" ·
  "Open book" · "Not added. {file} is copy-protected, so Tendshelf will not open it. Nothing was kept." ·
  "Not added. There is no audio file in it." · "The server ran out of space. Free some space, then press Try again." ·
  "Try again" · "{n} other files were left out."
- Admin: "Adding books from this device" · "Where new books go" · "Nowhere (off)" · "Tendshelf makes a folder for each
  author in the folder you pick. It never changes or deletes anything already there." · "Save" ·
  "Tendshelf cannot write in this folder. Check its permissions." · "Only the owner can change this."
- Book: "Remove this book" · "This deletes the {n} files Tendshelf added for this book. Files changed since then stay."

## Not handled, and why
- Uploads by members or guests, and through the API or bearer tokens: the operator scoped it to owner and admin.
- Replacing or adding files to an existing book: needs overwrite or merge rules; invariant 2 forbids it.
- Converting, remuxing or merging formats: unchanged bytes go in; the scanner's rules decide the book.
- Ebook uploads: since M5 B3 Add books takes ebooks too (EPUB, MOBI, AZW3, AZW, FB2, PDF; `.kfx` and `.kfx-zip` are refused
  by extension). Staged ebooks are opened with the library's reader during placement: a copy-protected one refuses the whole
  upload and an unreadable one too, with the staged bytes deleted and nothing placed. TXT, MD, HTML and DOCX stay Text-tab
  uploads (`/books/{id}/text`, R1). "Remove this book" counts live ebook files with a ledger path as well as audio.
- Files changed by "Write details into the file" are kept by Remove (their quick hash differs).
- Nested `.zip` files, encrypted zip entries and links inside archives are never extracted (U3).
- Windows servers: the residual rename risk in point 9 does not exist there, and no Windows-only code is added.

## Operator decisions
1. JS budget: `upload.js` 5 KB gzip and `shell.js` +0.25 KB. Either count lazy page-only modules outside the 56 KB
   all-app total (recommended: they never load on other pages) or raise the total to 61.5 KB.
2. The residual empty-folder rename race in point 9 (accept, or build a Linux-only `renameat2` through raw syscalls).
3. Remove (point 11) deletes the book row, so everyone's progress, bookmarks and queue rows for it go with it (the
   existing cascades). Recommended, since the person asked to remove it; the alternative keeps the book as missing.

Answered by the operator on 2026-11-04 ("use your picks"):
1. Raise the all-app total to 61.5 KB, counting `upload.js` like every other module (as `eyes-free.js` is);
   `upload.js` 5 KB, `shell.js` 5.625 KB.
2. Accept the residual empty-folder race; no `renameat2`.
3. Remove deletes the files Tendshelf created and the book row (with its cascades), after a confirmation.
