package web

// The words of the Users page. Board words come from the design handoff (UsersPage, UsersTablet,
// UsersPhone); the ones marked NEW follow the voice rules in BRAND.md and are listed in the report.

const (
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

	usersRoleOwner  = "Owner"
	usersRoleAdmin  = "Admin"
	usersRoleMember = "Member"
	usersRoleGuest  = "Listener"

	usersHintAdmin  = "Can manage books, people and the server."
	usersHintMember = "Can also make shelves."
	usersHintGuest  = "Can listen, download and bookmark."

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
