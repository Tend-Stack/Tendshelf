# 0045 Manage every book, including ones added by hand
Status: accepted (operator, 2026-11-15). Plan: `docs/plans/manage-all-books.md`. Parents: [0015](0015-upload-into-library.md) (point 11, Remove),
[0029](0029-contributors.md), the Duplicates page.

## Context
The operator found the same audiobook twice: one copied into the library folder by hand, one placed by Add books. The hand-copied one had no
Remove option, because point 11 of 0015 allowed deleting only files Tendshelf placed. The operator's words: "The app should be able to manage all
books, even ones that were added to the library folder manually. However, if the file is being deleted, the user needs to be warned of what is
about to happen." This deliberately overrides the rule that only upload-placed files may be deleted.

What exists and is reused: `internal/upload` (the only package that touches library files, `os.Root` access, the job runner, the sweep, the
audit log), `upload_paths` and `upload_books`, `library.DeleteBookAndFiles`, the Duplicates rules, the merge preview and the scanner's merged-folder
keep-out.

## Decision
1. **Same package, same care.** Only `internal/upload` moves library files (`removeany.go`). A hand-added file is touched only when it is a live
   `audio_files` or library `ebook_files` row of the book, or a listed extra, and only after Lstat (regular file, no link), size, mtime and quick
   hash match the scan's record.
2. **All or nothing.** Every file is verified before the first move; the removal and its paths are recorded in the database before anything on
   disk changes; a failed move rolls every earlier move back; a crash mid-move is rolled back at the next start or sweep.
3. **Nothing is destroyed at confirm time.** Files go by one rename to `<folder>/.tendshelf-removed/<removal id>/<path below the folder>`, can be
   put back, and are deleted only after 30 days (`RemovedKeepDays`) or by "Delete now".
4. **The person confirms an exact plan.** The request carries the plan's hash and is refused when the disk no longer matches it.
5. **Owner and admin only** (`share.RemoveLibraryFiles`). Contributors are unchanged.
6. **Check twice for duplicates** (before sending with a browser quick hash, and after staging), same size plus quick hash, with tiers
   `same_files`, `some_files` and `same_details`; a held upload is `failed` with `already_in_library`; "Add anyway" is owner and admin, and a
   contributor only for `same_details`. The check never names a book the uploader cannot list.
7. **Extras.** A cover or sidecar file moves with a hand-added book only when its folder is strictly inside the library folder, holds no other
   book's files, and the file has a listed extension, is at most 50 MiB and matches by size and mtime. Anything else stays, and then the folder stays.
8. **A holding folder** rather than outright delete or the OS trash: one rename, Undo works, no dependency, and the scanner already skips dot names.
9. **"Remove from Tendshelf, keep the files"** is a `removals` row with `mode = 'keep_files'`; the scanner skips its root while the row is held.
10. **Moving runs as the job `upload.remove_files`** (one at a time, one attempt); the request only plans and records.
11. **Confirmation** is a required checkbox plus a button, not typed text; the holding folder makes it recoverable. Upload-placed removals keep
    today's direct path. On the book pages, owner and admin see a notice when another copy exists, linking to the merge preview.

Known limit: re-tagging a file changes its first mebibyte, so its quick hash changes and an exact copy of a re-tagged file is seen as a look-alike ("same details"), which a contributor may still add anyway.

Rejected: deleting outright (no way back); the OS trash (the XDG trash needs `$HOME` on the same filesystem and has no standard-library API);
a second ignore table (the `removals` row does both jobs).

## Addendum 2026-11-15: upload-placed removals
Point 11's "Upload-placed removals keep today's direct path" is replaced: they now go through the same holding folder. Plan
`docs/plans/upload-removals-held.md`. D1: the move runs in the request (the caller needs the book gone when the POST returns), recorded as a
`removals` row first and holding the removal's lock for the whole move. D2: `removals.source` is `library` or `upload`; one mover, one Put back, one
purge. D3: migration 0046 adds `removal_ledger` (which `upload_paths` rows the removal took) and `removal_upload_books` (who-added-it), because
`uploads` rows are pruned and cannot be rebuilt at Put back. D7: the sweep rolls back an `upload` removal left `moving` as soon as it can take its
lock, and fails an `upload` removal left `planned` after an hour. Ebooks stored in the data folder from a book's Text page are still deleted at
once (follow-up L5). Put back and Delete now stay with the owner and admins.
