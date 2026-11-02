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

	shareViaAll     = "Has this through everything"     // NEW
	shareViaLibrary = "Has this through the library %s" // NEW
	shareViaShelf   = "Has this through the shelf %s"   // NEW

	shareStopBookTitle = "Stop sharing %s with %s?"                                                                                        // NEW
	shareStopBookBody  = "%s won't be able to open this book through this share any more. Their place in it and their bookmarks are kept." // NEW
)
