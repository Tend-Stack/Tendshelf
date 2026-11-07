# 0029 Contributors: people who add books of their own
Status: proposed (operator request, 2026-11-07). Plan: `docs/plans/contributors.md`. Parents: [0015](0015-upload-into-library.md) (adding books by
upload), the M2 Family sharing model (`internal/share`). This decision replaces one point of 0015: point 2 said `share.AddBooks` is "owner and admin,
never by grant". A contributor now passes it, but only through a whole-library or whole-server grant.

## Context
The operator wants a new kind of invite, **Contributor**: a person who listens like a member and can also add books (files and folders) into their own
folder, look up and fix the details of the books they added, and remove those books, never anyone else's. An admin must see every book a person added and
be able to remove them all at once.

What exists and is reused: invites and grants (`internal/share`), the role switch in `share.Can`, the whole upload pipeline (`internal/upload`: staging,
the `upload_paths` ledger, build in a hidden folder, publish by one rename, "Remove this book" that deletes only unchanged ledger files), the Add books page,
the details, lookup and cover sheets (all on `share.EditBook`), the job runner with `/hx/jobs/{id}` views, the person page `/admin/users/{id}` with its
confirm page, and the library filter menu. Nothing here adds a parallel system.

Found while planning:
- **No stored link from a book to the upload that made it.** `uploads.book_path` holds the published folder, but placed uploads are deleted by the sweep
  (`internal/upload/sweep.go`, `endedStates`), so they cannot be the record of who added a book. The book page reaches the upload only by
  `library.Repo.BookUnderPath`.
- **`users.role` cannot gain a value safely.** Its CHECK is in `0001_init.sql`; changing it means rebuilding `users`. Migrations run inside one
  transaction with `foreign_keys(1)` set by the connection, where `PRAGMA foreign_keys` cannot change, and `DROP TABLE users` would then run every
  `ON DELETE CASCADE` on it (sessions, grants, progress, shelves, devices: about 25 tables). A role value is also matched in 26 places in Go and in SQL
  (`role IN ('owner','admin')`). No later migration touches `invites` except `0009` (adds `mail_job_id`).
- **Folder names feed the details.** `metadata.FromPath` reads "Author/Series/Title" from the folders below the library folder, so a book at
  `<upload folder>/Added by Ana/Elias Varn/The Book` would get "Added by Ana" as its author unless the reader starts at the person's folder.

## Decision
1. **A contributor is a member with one flag: `users.adds_books = 1`.** The role stays `member`, so every grant, shelf, share-link and admin rule that
   holds for a member holds unchanged (admins manage contributors, as they manage members). A CHECK on the new column refuses the flag on any other
   role. Pages and forms speak of a **kind** (`users.Kind`: owner, admin, contributor, member, guest), derived from role plus flag. `invites` gets the
   same column with the same CHECK. Reason: no table rebuild, no audit of 26 role switches, and anything that forgets the flag treats a contributor as a
   member, which fails closed.
2. **Rights.** Every check stays in `share.Can`; ownership is read from the database inside it, never from the request or the `Target`.

   | Action | Owner | Admin | Contributor | Member | Listener (guest) |
   | --- | --- | --- | --- | --- | --- |
   | View, listen, read (grant rules) | all | all | grant, **or own book** | grant | grant |
   | Download book or text | all | all | grant with download, **or own book** | same | same |
   | `books.add` on a library | yes | yes | active `all` or `library` grant on it | no | no |
   | `book.edit` (details, look up, cover, chapters, pairing, merge) | all | all | **own books only** | no | no |
   | `book.remove` (new: "Remove this book") | all | all | **own books only**, and only files they placed | no | no |
   | `book.write_files` (new: "Write details into the file") | all | all | no | no | no |
   | `users.manage`, `libraries.manage`, `jobs.manage`, `server.manage` | as today | as today | no | no | no |

   "Own book" means the book's root path is, or lies inside, the root of a book that one of the person's uploads published (`upload_books`, point 4),
   and the person is an enabled contributor right now. A contributor sees their own books even in a library nobody granted them; the library then shows
   in their list with only those books.
3. **Their own folder.** A contributor's upload is built inside `<upload folder>/Added by <name>/`, then laid out as today (`Author/Series/NN - Title`).
   The folder is chosen once per person per upload folder and stored in `contributor_folders`; a later change of display name does not rename it.
   `internal/upload` creates it like every other path: the name goes through `SafeName`, the ledger row (`upload_paths`, `upload_id = 'contrib-' +
   user id`) is written before `Mkdir`, an existing directory is never adopted (" (2)" ... " (99)" until a name is free), and it is never removed by
   "Remove this book". Owner and admin uploads still go to the top of the upload folder. `metadata.FromPath` starts at the person's folder for books
   inside it, so the folder name never becomes an author.
4. **Who added a book: `upload_books`.** One row per published upload: `root_path` (the published folder), `upload_id`, `library_id`, `added_by`,
   `created_at`; written in the same transaction that marks the upload `placed`, for every person (admins too). It has no foreign keys, like
   `upload_paths`, so it survives the sweep, a rescan, a role change and a disabled account. Matching by path, not by book id, survives a library reset
   that gives books new ids. The migration fills it from the placed uploads still in the table; books whose upload was already swept have no "added by".
5. **Removal stays the one delete path.** `upload.Service.Remove` gains `placedBy`: when set, every live audio and library ebook file of the book must lie
   inside an `upload_books` root of that person, otherwise 409 `not_theirs` and nothing is touched. Contributors always remove with `placedBy` = themselves;
   "Remove all their books" uses `placedBy` = that person. When a book's rows go, its `upload_books` rows go too.
6. **Remove all their books** is a job (`upload.remove_all`) started from the person page after a confirm page that says how many books go and that only
   unchanged files Tendshelf placed for that person are deleted. The status polls `/hx/jobs/{id}` and ends with the count removed and the books kept,
   each with its reason. A plain link refreshes it without JavaScript.
7. **The admin sees a person's books** in the library through a new "Added by" filter (`?added=<user id>`). A contributor sees the same filter with one
   choice, "You". An owner or admin sees "Added by Ana" on a book's page; the contributor sees "Added by you"; nobody else sees who added a book.
8. **Role changes.** Contributor to member or listener: the flag goes, `upload_books` stays, their books stay and lose the "own book" rights. Back to
   contributor: the rights return. To admin: the flag goes (admins have everything by role). Turning an account off ends every right at once
   (`Can` refuses a disabled person); their books stay and an admin removes them from the person page.
9. **Where contributors may upload: no separate switch.** A contributor adds books to a library when uploads are on for it and they hold an active
   `all` or `library` grant on it. A shelf or single-book grant never lets anyone add books. A per-library opt-out and a per-person size limit are left
   out (point "Not handled").

## Security invariants
- **No title leak.** Listing, search, facet counts and library lists use `share.BookFilter`/`BookFilterFor`/`LibraryFilter`; a contributor's filter is
  "covered by a grant OR own book", in SQL. The "Added by" filter accepts another person's id only from an owner or admin and is dropped otherwise.
  The Add books page lists only libraries the person may add to.
- **IDOR.** Ownership, a book's library and a grant are all read inside `Can` by id. A contributor asking to edit, look up, remove or rewrite another
  person's book gets 404, as for a missing book. Upload sessions stay per person (`LibraryOf(userID, ...)`). `/admin/users/{id}/books/remove` and its job
  view need `users.manage` on that person, so an admin cannot remove an admin's or the owner's books.
- **Paths.** The folder name is `SafeName` output (no separators, no leading dot, no device names, at most 120 bytes) behind a fixed prefix, created
  through the library `os.Root` (`lroot`) and checked with `within`; a symlink or an existing entry is never reused. A contributor never sees a server path;
  the folder path is shown to owners and admins only.
- **A contributor never deletes another person's file** (point 5), including an admin's ebook paired into their book.
- **Demoted, disabled or ungranted mid-upload.** Every chunk and finish request re-checks `books.add`, so further bytes get 404. The placement job asks
  again (`upload.Config.MayPlace`) before building: a refusal ends the upload as `refused` with `not_allowed`, deletes its staged bytes and places nothing.
- **No file writes by contributors** beyond the upload path: "Write details into the file" needs the new `book.write_files`, owner and admin only, because it
  rewrites library files and would also make the book unremovable (its files no longer match the ledger).

## Not handled, and why
- A per-library switch for contributor uploads and a per-person size limit: grants already decide where, and staging is capped per person (60 GiB);
  add them when someone asks.
- Moving or renaming a contributor's folder, and removing an empty one: never needed for correctness, and every rename widens the write paths.
- Attributing books an admin drops into a contributor's folder by hand: only uploads attribute; such books stay the admin's to manage.
- Books whose upload rows were swept before this migration: no record is left to say who added them.

## Changes to AGENTS.md (applied in package P1)
In "Audio files are the user's", line 97 of AGENTS.md (the sentence wraps after "it writes only" on line 96), replace
"into the one folder the owner chose for uploads in that library (off by default), only creates new"
with
"into the one folder the owner chose for uploads in that library (off by default), and a contributor's uploads only into that person's own folder inside it ([decision 0029](docs/decisions/0029-contributors.md)), only creates new".

Rewrap the paragraph to the file's width. In "Security and privacy", after the bullet starting "All authorization goes through `share.Can(user, action, target)`", (it ends "...counts and search must not leak titles)."), add the bullet
"- A contributor is a member with `users.adds_books = 1` (decision 0029). They edit, look up and remove only books their own uploads placed (`upload_books`,
  read inside `share.Can`, never from the request), never write into a library file, and remove only files they placed."
