# Plan: Contributors (decision 0029)
Two builder packages, run in order: **P1 server** then **P2 pages**. Read `docs/decisions/0029-contributors.md` first; it is binding. A gap is reported as
`PLAN GAP: <one line>` after committing what is green.

## Invariants (both packages)
1. Contributor = `role='member' AND adds_books=1 AND disabled=0`; `users.User.Contributor()` is the only test of it in Go.
2. Every right goes through `share.Can`; "own book" is read from `upload_books` by book id inside `Can`, never from the request or `Target`.
3. Listings, search, facets and counts filter in SQL (`BookFilter`, `BookFilterFor`, `LibraryFilter`, `AddedBySQL`); no Go-side filtering.
4. Only `internal/upload` creates folders or deletes files; the person folder is never adopted, renamed or deleted.
5. A remove with `placedBy` set never deletes a file outside that person's `upload_books` roots.
6. A refusal is 404 like a missing book (existing `loadBook`/`allow` behaviour); owner and admin rights are unchanged.
7. Contributors never see a server path or a library they may not view.

## Shared names
- Migration `internal/db/migrations/0034_contributors.sql`:
  `ALTER TABLE users ADD COLUMN adds_books INTEGER NOT NULL DEFAULT 0 CHECK (adds_books IN (0,1) AND (adds_books = 0 OR role = 'member'));`
  the same line for `invites`; `CREATE TABLE contributor_folders (folder_id TEXT NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE, user_id
  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, path TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL, PRIMARY KEY (folder_id, user_id))
  STRICT, WITHOUT ROWID;` `CREATE TABLE upload_books (root_path TEXT PRIMARY KEY, upload_id TEXT NOT NULL, library_id TEXT NOT NULL, added_by TEXT NOT
  NULL, created_at INTEGER NOT NULL) STRICT, WITHOUT ROWID;` (no foreign keys, comment why) `CREATE INDEX upload_books_by ON upload_books(added_by,
  root_path);` backfill `INSERT OR IGNORE INTO upload_books SELECT book_path, id, library_id, created_by, updated_at FROM uploads WHERE state =
  'placed' AND book_path IS NOT NULL ORDER BY updated_at DESC;` (verified: SQLite accepts the cross-column CHECK on ADD COLUMN and refuses `owner`+1).
- Actions: `RemoveBook Action = "book.remove"`, `WriteFiles Action = "book.write_files"` (add both to `knownActions`).
- Ownership SQL: `share.AddedBySQL(alias string) string` (alias checked like `SharerSQL`; one `?` = user id):
  `EXISTS (SELECT 1 FROM upload_books share_ub WHERE share_ub.added_by = ? AND (<a>.root_path = share_ub.root_path OR (<a>.root_path > share_ub.root_path || '/' AND <a>.root_path < share_ub.root_path || '0')))`.
- `users.Kind` (`KindOwner "owner"`, `KindAdmin "admin"`, `KindContributor "contributor"`, `KindMember "member"`, `KindGuest "guest"`).
- Job kind `upload.KindRemoveAll = "upload.remove_all"`; payload `upload.RemoveAllPayload{UserID string \`json:"user_id"\`; By string \`json:"by"\`}`.

## P1 server (Go, no templ changes except where a signature forces one)
1. `0034_contributors.sql` as above, header comment naming decision 0029.
2. `internal/users/users.go`: `User.AddsBooks bool`; append `adds_books` to `userColumns` and scan it in `scanUser`; `func (u User) Contributor() bool`;
   `func (u User) Kind() Kind`; `func ParseKind(s string) (Role, bool, error)` (admin/contributor/member/guest; else `ErrRole`); `SetRole` UPDATE becomes
   `SET role = ?, adds_books = CASE WHEN ? = 'member' THEN adds_books ELSE 0 END`; new `SetKind(ctx, tx, byID, id string, to Kind) (Kind, error)`: same
   guards as `SetRole` (`ErrOwnerFixed`, `ErrSelf`, `ErrRole`), one UPDATE of role and flag, unchanged writes nothing, audit `user.role` with
   `{"from": kind, "to": kind}`. `ErrRole` text: "a role must be admin, contributor, member or guest". `NewUser.AddsBooks`; `CreateInvited` inserts it.
3. `internal/share/share.go`: in `Can`, before `canByGrant` for member/guest, `if u.Contributor() { if ok, decided := c.canContributor(ctx, u, a, t); decided { return ok } }`.
   `canContributor`: `AddBooks`+`TargetServer` → true; `AddBooks`+`TargetLibrary` → active grant `scope='all' OR (scope='library' AND library_id=t.ID)`
   (use `activeSQL`); `TargetBook` and a in {ViewBook, ListenBook, DownloadBook, ReadText, DownloadText, EditBook, RemoveBook}: own (`AddedBySQL("b")`
   on `books b WHERE b.id = ?`) → true; not own and a is EditBook/RemoveBook → false, decided; `ViewLibrary`+`TargetLibrary`: own book in it → true;
   otherwise undecided. `filter` and `BookFilterFor`: for a contributor wrap as `(<grant SQL> OR <AddedBySQL>)`, args appended in order.
   `LibraryFilter`: OR `EXISTS (SELECT 1 FROM books share_lb WHERE share_lb.library_id = <a>.id AND <AddedBySQL("share_lb")>)`. Update the `AddBooks` comment.
4. `internal/share/invites.go`: `InviteSpec.AddsBooks`, `Invite.AddsBooks` (column in `inviteColumns`, scanned, inserted in `Create`); `Create` refuses
   `AddsBooks` with a role other than member (`ErrInviteRefused`); `Accept` passes it to `CreateInvited`. `ManageableInviteKinds(u) []users.Kind`:
   owner → admin, contributor, member, guest; admin → contributor, member, guest. Keep `ManageableInviteRoles`.
5. `internal/web/routes_share.go` (~:316): role `contributor` → `spec.Role = member, spec.AddsBooks = true`. `internal/web/routes_users.go`
   `postPersonRole` (~:372): `users.ParseKind`, call `SetKind`; grants revoke on promotion to admin unchanged; `ManageUsers` target role stays `member`.
6. `internal/upload`: `Config.MayPlace func(ctx context.Context, userID, libraryID string) Placement` with `type Placement int` (`PlaceRefused`,
   `PlaceTop`, `PlaceOwnFolder`); nil means `PlaceTop`. In `place` after the compare-and-set and before `recoverPlacement`'s build branch:
   `PlaceRefused` → state `refused`, `error_code='not_allowed'`, staging removed, nothing placed, job result `PlaceResult{State: refused}` (reuse
   `s.refuse` if its refusal type fits, else `s.refuseNotAllowed`). New file `internal/upload/person.go`: `personFolder(ctx, g, lr, folderID, userID,
   name string) (string, error)`: read `contributor_folders`; an existing real directory (Lstat, not a symlink, `within`) is used; a stored path that is
   gone is recreated with plain `Mkdir` (fail on anything existing); no row: name `"Added by " + SafeName(name, "a contributor")` trimmed to the
   `SafeName` byte limit, then `suffixed` " (2)".." (99)"; a candidate must not exist and must pass `usableAncestor`; write the `upload_paths` row
   (`upload_id = "contrib-"+userID`, kind dir, planned) and the `contributor_folders` row in one `s.write`, then `lr.Mkdir`; on `fs.ErrExist` drop both
   rows and try the next name; then mark present. `name` = display name, else username. `layout` gains a `base string` first-candidate directory
   (root for `PlaceTop`, the person folder otherwise); `build` passes it. `commitPublish`: in its tx `INSERT INTO upload_books ... ON CONFLICT(root_path)
   DO UPDATE SET upload_id=excluded.upload_id, library_id=excluded.library_id, added_by=excluded.added_by, created_at=excluded.created_at`.
7. `internal/upload/remove.go`: `Remove(ctx, userID, bookID, placedBy string)`; after `Removable`, when `placedBy != ""` every live audio and library
   ebook path must satisfy `AddedBySQL` for that person (one query) else `fail(409, "not_theirs", "Someone else added part of this book, so only an
   admin can remove it.")`. When the book's rows are deleted, in the same tx delete the `upload_books` rows whose `root_path` equals the book's
   root or lies inside it. `func (s *Service) RemovableBy(ctx, bookID, userID string) bool`.
8. New `internal/upload/remove_all.go`: register `KindRemoveAll` in `Register` (Concurrency 1, MaxAttempts 1). Handler loads
   `library.Repo.BooksAddedBy(ctx, userID)`, calls `Remove(ctx, by, id, userID)` per book, progress `{"done":n,"total":m}`, result
   `RemoveAllResult{Removed int; Kept []KeptBook{BookID, Title, Reason string}}`, reasons `changed` (files kept), `not_removable`, `not_theirs`.
9. `internal/library`: `BooksAddedBy(ctx, userID) ([]Book, error)` (order root_path), `CountAddedBy(ctx, userID) (int, error)`,
   `AddedBy(ctx, bookID) (userID string, err error)` ("" when none, newest row wins), `ContributorFolderOf(ctx, folderID, bookRoot) (string, error)`,
   `ContributorFolders(ctx, userID) ([]string, error)`; `ListQuery.AddedBy string` → `w.add(share.AddedBySQL("b"), q.AddedBy)`;
   `AddedByFacet(ctx, viewer filter) ([]NamedFacet, error)` (people with at least one visible book, display name, count, through `BookFilter`).
10. `internal/metadata/local.go` both `FromPath` sites: base = `ContributorFolderOf(...)` when non-empty, else `folder.Path`.
11. `internal/web`: server wiring sets `upload.Config.MayPlace` (load user; disabled/missing → refused; owner/admin → top; contributor and
    `Can(AddBooks, library)` → own folder; else refused). Switch `routes_upload.go:244`, `routes_upload_pages.go:252`, `routes_book.go:172` to
    `RemoveBook`; call `Remove`/`Removable` with `placedBy = ""` for owner/admin, else the actor id (`RemovableBy`). Switch `routes_write.go:39` and
    `routes_book.go:191` to `WriteFiles`. `getAddBooks`: the library chips keep only libraries with an upload folder where `Can(AddBooks)` holds.
12. `AGENTS.md`: apply the two edits in decision 0029; `docs/decisions/0015`: one line under point 2, "Superseded in part by 0029 (contributors)".
### P1 tests (each new, table-driven where it fits)
- `internal/db`: `TestMigration0034` from the 0033 fixture and from empty: users and invites keep every row, flag 0; owner+flag and admin+flag refused;
  placed uploads backfilled, refused ones not.
- `share_test.go`: extend `TestCanMatrix` with contributor rows for every action in the decision 0029 table (own book in an ungranted library, other
  person's book in a granted library, disabled contributor, member with a stale `upload_books` row: all edit/remove refused); extend
  `TestCanAgreesWithFilter` with a contributor; `TestContributorLibraryFilter` (ungranted library with an own book listed, others not).
- `invites_test.go`: contributor invite created by admin and by owner, accepted gives `AddsBooks`; `AddsBooks` with role guest refused.
- `users`: `TestSetKind` (contributor↔member↔admin, owner fixed, self refused, audit row).
- `internal/upload`: `TestContributorPlacesInOwnFolder` (folder created once, reused, " (2)" when an "Added by Ana" dir already exists, symlink never
  used, name with `../` and a leading dot sanitised); `TestPlaceRefusedNotAllowed` (MayPlace refused: nothing in the library, staging gone);
  `TestUploadBooksRow`; `TestRemovePlacedByRefusesOthersFile` (admin ebook in a contributor's book: 409, nothing deleted); `TestRemoveAll` (two books
  removed, one with a changed file kept with `changed`).
- `internal/metadata`: book at `<lib>/Added by Ana/Elias Varn/The Book` reads author Elias Varn, no series.
- `internal/web` httptest, per role (owner, admin, contributor, member, guest) and for another contributor: upload create in granted and ungranted
  library, details/lookup/cover/chapters on own and others' books, `POST /books/{id}/remove`, write sheet (contributor 404), `?added=<other id>`
  ignored for a contributor, Add books chips. Extend `TestUploadRoutesRefuse` and `TestBookRemoveRefusals`.
### P1 gates
`make generate`, `make check`, `make build`. Out of P1: every page or copy change below.

## P2 pages (templ, CSS, copy, captures)
Copy (use exactly; listed as new in the report):
- Kind words: Owner, Admin, **Contributor**, Member, Listener. Picker hints: Listener "Can listen, download and bookmark."; Member "Can also make
  shelves."; Contributor "Can also add books, and edit or remove the ones they add."; Admin "Can manage books, people and the server."
- Person page section `Books they added`: count "12 books" / "1 book" / "No books yet"; folder line "Their folder: {path}" (one per folder);
  note for a contributor "{Name} can add books to whole libraries shared with them, where uploads are on."; link "See their books"; danger button
  "Remove all their books".
- Confirm: title "Remove all books {Name} added?"; body "This removes {n} books from the library. Tendshelf deletes only the files it placed for
  {Name} that nobody has changed since. Books with changed files stay, and you'll see which."; confirm "Remove {n} books"; keep "Keep them".
- Status: running "Removing books… {done} of {total}"; done "Removed {n} books."; kept heading "Kept {k} books"; reasons `changed` "Some files changed
  since they were added.", `not_removable` "Tendshelf did not add all of its files.", `not_theirs` "Someone else added part of it."; failed
  "Tendshelf could not finish. Some books may be gone already. Try again."; fallback link "Refresh".
- Library filter: label "Added by", any "Anyone", own choice "You". Book page: "Added by {Name}" (owner/admin), "Added by you" (the contributor).
- Add books lead for a contributor: "Books you add go into your own folder, {folder name}." (no path); before the first upload "Books you add go into a
  folder of your own."
Steps:
1. `internal/web/share_views.go` `shareRoleChoices` → kinds via `ManageableInviteKinds`, order Listener, Member, Contributor, Admin; contributor is
   treated like member wherever the invite form branches on role (grep `RoleAdmin` in `share_views.go`, `pages/invite.templ`, `pages/share*.templ`).
   Copy constants in `share_copy.go` (`shareContributorHint`) and `users_copy.go` (`usersRoleContributor`, `usersHintContributor`).
2. `users_views.go`: `kindWord(users.Kind)`; every pill and invite line that shows a role uses it; `routes_users.go` role choices add contributor.
3. `pages/admin_user.templ`: `userAddedBooks(v)` section, class `ts-users__added`, between the share form and Account, shown when the count > 0 or the
   person is a contributor; `AdminUserView.Added AddedBooksView{Count int; CountWords string; Folders []string; Note, SeeHref, RemoveHref string}`.
4. Routes in new `internal/web/routes_users_books.go`: `GET /admin/users/{id}/books/remove` (confirm via `AdminUserConfirm`; with `?job=` the status
   page `pages.AdminUserRemoveAll`), `POST /admin/users/{id}/books/remove` (CSRF, `ManageUsers` on the person, count > 0 else back to the page; enqueue
   `KindRemoveAll`; 303 to `?job={id}`); `jobViews[upload.KindRemoveAll] = (*server).getRemoveAllJob` re-checking `ManageUsers` on the payload's person
   (else 404). Fragment `components.RemoveAllStatus` (file `components/remove_all_status.templ`, root `ts-removeall`) polls `hx-get="/hx/jobs/{id}"
   hx-trigger="every 2s" hx-swap="outerHTML"` while queued/running; done and failed render without the poll attributes.
5. Library filter: `browseQuery.Added`, parsed from `added`, kept in `href`, `filtered()`, `clearFilters()`, an active chip; `libraryKinds` adds kind
   `added` (not lazy) after `format`: owner/admin options from `AddedByFacet`, a contributor only "You" when they have a visible own book; anyone else no
   kind. `listQuery` sets `AddedBy` only when the viewer is owner/admin or the id is their own.
6. Book page (`routes_book.go`, its view, `pages/book*.templ`): `AddedBy, AddedByHref` from `library.AddedBy`; shown in the book's facts list beside the
   date added, or if there is none a caption `ts-book__addedby` under the byline; the name links to `/admin/users/{id}` only when `Can(ManageUsers)`.
7. Add books page (`routes_upload_pages.go`, `pages/add_books*.templ`): for a contributor the lead and the no-JS line use the contributor copy, never
   `folder.Path`.
8. `internal/web/static/css/bundle.css`: `ts-users__added`, `ts-removeall`, `ts-book__addedby` with tokens only, `--radius-control`, 44 px targets.
### P2 tests and checks
- httptest: confirm and POST for owner (any person), admin (contributor yes, admin target 404), contributor and member (404), wrong CSRF (403 as the
  house pattern), zero books (back to the page); job view for another admin 404; picker shows Contributor for owner and admin, not for a member.
- Captures: extend `TestVisualUsersPages` (person page with the section, confirm, status done with one kept book), `TestVisualInvitePages` (picker),
  `TestVisualLibraryFilters` (Added by menu open), `TestUploadVisual` (contributor lead), at 390, 768, 1024 and 1440, in dark and light; seeded ids from
  `visual_ids_dev_test.go`, no temp paths on screen (use a fixed folder string in the view).
- Gates: `make generate`, `make check`, `make build`, `TENDSHELF_TEST_BROWSER=... VISUAL_RUN='TestVisualUsersPages|TestVisualInvitePages|TestVisualLibraryFilters|TestUploadVisual' make visual`, and the CSS size budget test.

## Out of scope (both)
Per-library contributor switch, per-person size limit, renaming/moving/removing person folders, attributing hand-placed books, "Added by" in
the API, OPDS or Android app, email copy changes beyond the kind word, translations (separate scribe package after P2).
