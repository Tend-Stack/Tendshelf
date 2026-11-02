package web

// Share & invite copy. Words marked NEW are not in the copy deck; they follow the voice rules in
// BRAND.md and are listed in the P3-4 report.
const (
	shareTitle = "Share & invite"

	shareEverything  = "Everything"
	shareBooks       = "%d books"
	shareOneBook     = "1 book"
	shareOneLibrary  = "One library"
	shareWhichLib    = "Which library" // NEW
	shareAShelf      = "A shelf"       // NEW (the board's "A shelf: {name}" without a name)
	shareAShelfNamed = "A shelf: %s"
	shareWhichShelf  = "Which shelf" // NEW
	shareShelfBy     = "%s, chosen by %s"
	shareShelfByYou  = "%s, chosen by you"
	shareJustOneBook = "Just one book"
	shareFindBook    = "Find a book" // NEW

	shareMemberHint = "Can also make shelves." // decision 1, replaces the board's longer line
	shareListener   = "Can listen, download and bookmark."
	shareAdminHint  = "Can manage books, people and the server."

	shareDay1  = "1 day"
	shareDay7  = "7 days"
	shareDay30 = "30 days"
	shareNever = "Until I stop it"

	shareErrName    = "Add a name so you know whose link it is." // NEW
	shareErrNameLen = "Keep the name under 80 characters."       // NEW
	shareErrNoteLen = "Keep the note under 500 characters."      // NEW
	shareErrScope   = "Choose what they can listen to."          // NEW
	shareErrRole    = "Choose what they can do."                 // NEW
	shareErrExpires = "Choose how long the link works."          // NEW

	shareLinkTitle   = "Invite link for %s"                                           // NEW
	shareLinkNoteFor = "Copy it and send it to %s. It works once, for %s."            // NEW
	shareLinkNoteEnd = "Copy it and send it to %s. It works once, until you stop it." // NEW
	shareNewLink     = "New link for %s"                                              // NEW
	shareOldLink     = "The old link no longer works."                                // NEW

	sharePendingLine = "%s · hasn't opened it yet"
	shareLinkAll     = "Link to everything"         // NEW
	shareLinkOne     = "Link to \"%s\""             // NEW
	shareLinkMore    = "Link to \"%s\" and %d more" // NEW
	shareLinkAdmin   = "Admin link"                 // NEW
	shareMetaDays    = " · works for %d more days"  // NEW
	shareMetaDay     = " · works for 1 more day"    // NEW
	shareMetaNoEnd   = " · works until you stop it" // NEW

	shareViaAll     = "Has this through everything"       // NEW
	shareViaLibrary = "Has this through the library “%s”" // NEW; quoted, so a name ending in "library" still reads
	shareViaShelf   = "Has this through the shelf “%s”"   // NEW

	shareStopBookTitle = "Stop sharing %s with %s?"                                                                                        // NEW
	shareStopBookBody  = "%s won't be able to open this book through this share any more. Their place in it and their bookmarks are kept." // NEW

	// Emailed invitations (P3-8). NEW unless noted.
	shareErrEmail        = "Enter their email."
	shareErrEmailBad     = "That email looks incomplete."
	shareErrRelayOff     = "Sending from Tendshelf isn't set up yet. Use Send invitation to send it from your own email app."
	shareErrNoEmail      = "That invite has no email address."
	shareErrStillSending = "It's still sending."
	shareErrQueue        = "Tendshelf couldn't queue the email. Copy the link and send it yourself."
	shareErrMailBusy     = "You've sent a lot of invites for now. Try again in an hour, or copy a link instead."
	shareErrMailAgain    = "Tendshelf has emailed that address three times today. Copy the link instead."

	shareMailReadyTitle = "Invitation for %s is ready"
	shareMailReadyNote  = "Open your email app to send it to %s. The link below also works, once, if you'd rather send it another way."
	shareMailSentTitle  = "Invite on its way to %s"
	shareMailSentNote   = "Tendshelf is emailing it to %s. The link below also works, once, if you'd rather send it yourself."

	shareMailCreated     = "Link created for %s"
	shareMailSending     = "Sending to %s…"
	shareMailSent        = "Sent to %s"
	shareMailBadAddress  = "Couldn't send to %s. Check the address."
	shareMailLinkLost    = "Couldn't send to %s. Send it again."
	shareMailUnconfirmed = "Tendshelf couldn't confirm the email to %s went out."
	shareMailSettings    = "Couldn't send: check the email settings"
	shareMailSendForMe   = "Send it for me"
	shareMailSendAgain   = "Send again"
)
