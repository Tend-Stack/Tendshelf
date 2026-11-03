package web

// Words of the "Look up details online" sheets (M4-6). They follow the compare board and the voice
// rules in BRAND.md.
const (
	detailsLookupButton = "Look up details online"
	detailsSheetTitle   = "Look up details online"
	detailsSheetClose   = "Close"

	detailsQueryTitle  = "Title"
	detailsQueryAuthor = "Author"
	detailsQueryASIN   = "ASIN"
	detailsQueryISBN   = "ISBN"
	detailsQueryIntro  = "Tendshelf will ask Audible's book catalogue. Nothing is sent until you press Look up."
	detailsQueryGo     = "Look up"
	detailsMoreTitle   = "Search more places"
	detailsMoreLabel   = "Also search Google Books, Open Library and Apple Books"
	detailsErrEmpty    = "Type a title, an ASIN or an ISBN to look up."

	detailsWaiting    = "Looking up details…"
	detailsCheckAgain = "Check again"
	detailsTryAgain   = "Try again"
	detailsFailed     = "The lookup did not finish."
	detailsNone       = "Nothing close turned up. Check the spelling of the title and author, or try the ASIN or ISBN."
	detailsListIntro  = "Pick the book that matches."
	detailsExact      = "Exact match"
	detailsPickOther  = "Choose a different match"
	detailsFound      = "Found a close match: "
	detailsMaybe      = "This may not be the right book: "
	detailsTick       = " Tick the values you want to use."
	detailsStillRuns  = "The lookup is still running."

	detailsColField  = "Field"
	detailsColNow    = "Now"
	detailsColOnline = "Found online"
	detailsColUse    = "Use online value"
	detailsCaption   = "Compare current details with details found online"
	detailsSame      = "Same"
	detailsShowAll   = "Show all"
	detailsKeep      = "Keep everything as it is"
	detailsNoValue   = "—"

	detailsFromFile = "From file"
	detailsEdited   = "Edited by you"

	detailsResetTitle  = "Go back to the details in the files"
	detailsResetBody   = "This removes the details you chose or took from the internet. The details in your audio files, folders and sidecar files come back."
	detailsResetButton = "Go back to the files' details"

	detailsLabelTitle       = "Title"
	detailsLabelSubtitle    = "Subtitle"
	detailsLabelAuthors     = "Written by"
	detailsLabelNarrators   = "Narrated by"
	detailsLabelSeries      = "Series"
	detailsLabelYear        = "Year"
	detailsLabelPublisher   = "Publisher"
	detailsLabelDescription = "Description"
	detailsLabelLanguage    = "Language"
	detailsLabelGenres      = "Genre"
	detailsLabelISBN        = "ISBN"
	detailsLabelASIN        = "ASIN"
	detailsLabelAbridged    = "Edition"
	detailsLabelCover       = "Cover"
	detailsAbridged         = "Abridged"
	detailsUnabridged       = "Unabridged"
	detailsCoverNow         = "Current cover"
	detailsCoverOnline      = "Cover found online"

	adminOnlineTitle = "Book details from the internet"
	adminOnlineBody  = "When someone who edits books asks, Tendshelf can look a book up on Audible and, if they choose, a few other book sites. Scans never go online."
	adminOnlineOn    = "Look up details when someone asks"
	adminOnlineOff   = "Never go online"
	adminOnlineSave  = "Save"
)

// detailsUsed is the status after "Use {n} changes".
func detailsUsed(n int) string { return countText(n, "Used 1 change.", "Used %d changes.") }

// detailsUseText is the label of the compare sheet's main button.
func detailsUseText(n int) string { return countText(n, "Use 1 change", "Use %d changes") }
