package web

// Copy for the Ebooks to pair inbox (M5 R2 P3). Deck words (boards AddEbook*, NoMatchPhone) are
// used word for word; lines marked "NEW" are not in the deck and are listed in the package report.
const (
	inboxTitle      = "Ebooks to pair"                                                                      // NEW
	inboxLead       = "These ebooks are not paired with an audiobook yet. Only owners and admins see them." // NEW
	inboxEmpty      = "Every ebook is paired with an audiobook."                                            // NEW
	inboxKeptTitle  = "Kept on their own"                                                                   // NEW
	inboxKeptLead   = "It stays here, off your shelf, until you pair it with an audiobook."                 // NEW: the deck's sentence says it goes on the shelf, which R2 does not do
	inboxMore       = "And %d more."                                                                        // NEW
	inboxBack       = "Back to Ebooks to pair"                                                              // NEW: aria label
	inboxNoneChosen = "Choose a book first."                                                                // NEW

	// Board: AddEbook*, NoMatchPhone.
	inboxEyebrow    = "Add an ebook"
	inboxPickTitle  = "Which audiobook is it?"
	inboxNoMatch    = "None of your %d audiobooks has this title and author. It may be listed under a different name."
	inboxClose      = "Close matches"
	inboxChoose     = "Choose"
	inboxChooseBook = "Choose the audiobook"
	inboxKeep       = "Keep it as an ebook on its own"
	inboxSameAuthor = "same author"
	inboxSimilar    = "similar title"

	inboxSearchLabel  = "Search your audiobooks"     // NEW
	inboxSearchGo     = "Search"                     // NEW
	inboxResultsTitle = "Audiobooks matching “%s”"   // NEW
	inboxNoResults    = "No audiobook matches “%s”." // NEW
	inboxSameISBN     = "same ISBN"                  // NEW
	inboxSameTitle    = "same title and author"      // NEW
	inboxToPairOne    = "1 ebook to pair"            // NEW
	inboxToPairMany   = "%d ebooks to pair"          // NEW
	inboxChooseAria   = "Choose %s for %s"           // NEW: {audiobook}, {ebook}
)
