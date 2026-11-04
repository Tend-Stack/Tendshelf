package web

// Copy for /admin/ebooks (M5 R2 P3, reshaped by B3 when ebooks became books). Deck words (boards
// AddEbook*, NoMatchPhone) are used word for word; lines marked "NEW" are not in the deck and are listed in
// the package report.
const (
	inboxTitle      = "Ebooks"                                                                                                // NEW: the nav label
	inboxLead       = "Ebooks are books you can read. When one may be the same book as an audiobook, you can join them here." // NEW
	inboxEmpty      = "Nothing here needs a look."                                                                            // NEW
	inboxMaybeTitle = "Maybe the same book"                                                                                   // NEW
	inboxMaybeLead  = "Joining makes one book you can listen to and read. Nothing is changed in your folders."                // NEW
	inboxCannotHead = "Could not be opened"                                                                                   // NEW
	inboxMore       = "And %d more."                                                                                          // NEW
	inboxBack       = "Back to Ebooks"                                                                                        // NEW: aria label
	inboxNoneChosen = "Choose a book first."                                                                                  // NEW
	inboxMake       = "Make one book"                                                                                         // NEW
	inboxMakeAria   = "Make one book of %s and %s"                                                                            // NEW: {ebook}, {audiobook}
	inboxGuard      = "People who can see this ebook today lose it, or may gain the audiobook. Check who has access first."   // NEW
	inboxDownload   = "Download the file"                                                                                     // NEW
	inboxDownloadAr = "Download %s"                                                                                           // NEW: {file}

	// Board: AddEbook*, NoMatchPhone.
	inboxEyebrow    = "Add an ebook"
	inboxPickTitle  = "Which audiobook is it?"
	inboxNoMatch    = "None of your %d audiobooks has this title and author. It may be listed under a different name."
	inboxClose      = "Close matches"
	inboxChooseBook = "Choose the audiobook"
	inboxKeep       = "Keep it as an ebook on its own"
	inboxKeepNote   = "It goes on the shelf as a book you can read"
	inboxSameAuthor = "same author"
	inboxSimilar    = "similar title"

	inboxSearchLabel  = "Search your audiobooks"     // NEW
	inboxSearchGo     = "Search"                     // NEW
	inboxResultsTitle = "Audiobooks matching “%s”"   // NEW
	inboxNoResults    = "No audiobook matches “%s”." // NEW
	inboxSameISBN     = "same ISBN"                  // NEW
	inboxSameTitle    = "same title and author"      // NEW
	inboxSameFolder   = "in the same folder"         // NEW
)
