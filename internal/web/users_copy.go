package web

// The words of the Users page. Board words come from the design handoff (UsersPage, UsersTablet,
// UsersPhone); the ones marked NEW follow the voice rules in BRAND.md and are listed in the report.

const (
	usersTabDevices    = "Devices · %d"
	usersDevicesOwn    = "Manage your own on your Profile."
	usersTitle         = "Users & invites"
	usersEveryone      = "Everything"
	usersListeningNow  = "Listening now"
	usersNow           = "Now"
	usersToday         = "Today"
	usersYesterday     = "Yesterday" // NEW
	usersLastWeek      = "Last week"
	usersOverMonth     = "Over a month ago"   // NEW
	usersNever         = "Never"              // NEW
	usersAccessEnded   = "Access ended"       // NEW
	usersNothingShared = "Nothing shared yet" // NEW

	usersRoleOwner       = "Owner"
	usersRoleAdmin       = "Admin"
	usersRoleMember      = "Member"
	usersRoleContributor = "Contributor" // NEW
	usersRoleGuest       = "Listener"

	usersHintAdmin       = "Can manage books, people and the server."
	usersHintMember      = "Can also make shelves."
	usersHintGuest       = "Can listen, download and bookmark."
	usersHintContributor = "Can also add books, and edit or remove the ones they add." // NEW

	usersErrTarget = "Choose a library, shelf or book." // NEW
	usersErrDay    = "Pick a day after today."          // NEW
	usersErrRole   = "Choose a role."                   // NEW

	usersOffTitle   = "Turn off %s's account?"
	usersOffBody    = "%s is signed out on every device and can't sign in until you turn the account back on. Their place in every book and their bookmarks are kept."
	usersOffConfirm = "Turn off account"
	usersOffKeep    = "Keep it on"

	usersStopTitle   = "Stop sharing with %s?"
	usersStopBody    = "%s won't see any books here until you share something again. Their place in every book and their bookmarks are kept."
	usersStopConfirm = "Stop sharing"
	usersStopKeep    = "Keep sharing"

	usersEverythingKind = "Everything on this server" // NEW
	usersBookKind       = "Book"                      // NEW
	usersNoEnd          = "No end date"               // NEW
)

// Books a person added, and "Remove all their books" (decision 0029). All NEW.
const (
	usersAddedTitle   = "Books they added"
	usersAddedNone    = "No books yet"
	usersAddedFolder  = "Their folder: %s"
	usersAddedNote    = "%s can add books to whole libraries shared with them, where uploads are on."
	usersAddedSee     = "See their books"
	usersAddedRemove  = "Remove all their books"
	usersRemoveTitle  = "Remove all books %s added?"
	usersRemoveBody   = "This removes %d books from the library. Tendshelf deletes only the files it placed for %s that nobody has changed since. Books with changed files stay, and you'll see which."
	usersRemoveBody1  = "This removes 1 book from the library. Tendshelf deletes only the files it placed for %s that nobody has changed since. A book with changed files stays, and you'll see which."
	usersRemoveOK     = "Remove %d books"
	usersRemoveOK1    = "Remove 1 book"
	usersRemoveKeep   = "Keep them"
	usersRemoveBusy   = "Removing books… %d of %d"
	usersRemoveDone   = "Removed %d books."
	usersRemoveDone1  = "Removed 1 book."
	usersRemoveKept   = "Kept %d books"
	usersRemoveKept1  = "Kept 1 book"
	usersRemoveFailed = "Tendshelf could not finish. Some books may be gone already. Try again."
	usersRemoveReload = "Refresh"

	usersKeptChanged      = "Some files changed since they were added."
	usersKeptNotRemovable = "Tendshelf did not add all of its files."
	usersKeptNotTheirs    = "Someone else added part of it."
)
