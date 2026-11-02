package web

// Invite copy. Words marked NEW are not in the copy deck; they follow the voice rules in BRAND.md
// and are listed in the P3-3 report.
const (
	inviteServerFall  = "Tendshelf"
	inviteLead        = "%s invited you to listen to " // the title follows, in italics
	inviteTailOther   = " and 1 other book"            // NEW
	inviteTailOthers  = " and %d other books"
	inviteLineServer  = "%s invited you to %s"                       // NEW
	inviteBrokenTitle = "This invite link doesn't work"              // NEW
	inviteBrokenBody  = "Ask the person who sent it for a new link." // NEW
	inviteEndedTitle  = "This invite has ended"                      // NEW
	inviteEndedBody   = "Ask %s for a new link."                     // NEW
	inviteUsedTitle   = "This invite was already used"               // NEW
	inviteUsedBody    = "If it was you, sign in."                    // NEW
	inviteSignIn      = "Sign in"                                    // NEW
	inviteWaiting     = "%s hasn't opened the invite yet"            // NEW
	inviteUntil       = "%s · Until %s"                              // NEW
	inviteNoEnd       = "%s · No end date"                           // NEW
	inviteTabLabel    = "Invites · %d"
)
