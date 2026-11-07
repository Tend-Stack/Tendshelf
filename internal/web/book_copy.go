package web

// Copy for the book, people and search pages (P1b-6). Strings from the copy deck and the design
// boards are used word for word; the lines marked "New" follow the voice rules in BRAND.md and are
// listed in the package report.
const (
	// Board: BookPhone, BookTablet (tabs and rails).
	bookTabAbout    = "About"
	bookTabChapters = "Chapters"
	bookTabDetails  = "Details"
	bookMore        = "More"
	bookLess        = "Show less"
	bookBy          = "by"
	bookNarratedBy  = "Narrated by"
	// Board: SeriesPhone.
	bookReadBy         = "read by"
	bookSeriesOrder    = "Books in order"
	bookSeriesOverline = "Series · "
	bookInAll          = " in all"
	bookSeriesPrefix   = "More in "
	bookAlsoNarrated   = "Also narrated by "
	bookThisBook       = "This book"
	// Board: BookPhone, BookTablet, BookDesktop (series tiles, overline, short progress line).
	bookStartSeries    = "Start series"
	bookSeriesOf       = "Book %s of %d"
	bookFinishedIn     = "finished in %s"
	bookTilePercent    = "%d%%"
	cardThrough        = "%d %% through" // NEW: hidden words of the thin line on a started book's cover
	bookTileUpNext     = "Next in series"
	bookTileNotStarted = "Not started"
	bookWhereShort     = "Chapter %d · %s left"
	bookBylineSep      = " · "
	// Board: SearchPhone and the copy deck (empty states, search).
	searchFieldLabel    = "Search"
	searchPlaceholder   = "Search books, authors, narrators and series"
	searchClearLabel    = "Clear search"
	searchEmptyTitle    = "Nothing for “%s”"
	searchEmptyBody     = "Check the spelling, or try an author or narrator."
	searchDidYouMean    = "Did you mean"
	searchEverything    = "Everything"
	searchBooks         = "Books"
	searchPeople        = "People"
	searchSeries        = "Series"
	searchShowOnlyLabel = "Show only"
	// Board: AuthorTablet, NarratorPhone.
	bookRoleAuthor   = "Author"
	bookRoleNarrator = "Narrator"

	// New.
	bookFinishedInYear  = "finished in %s %d"
	bookTileFinished    = "Finished"
	bookTileStarted     = "Started"
	bookWhereShortBook  = "%s left"
	bookBack            = "Back to library"
	bookBanner          = "This book's files are missing. It comes back when they do."
	bookRefusedFallback = "Tendshelf can't play this book."
	bookShowAllChapters = "Show all %d chapters"
	bookNoChapters      = "This book has no chapters."
	bookChapterN        = "Chapter %d"
	bookSeriesBook      = "Book "
	bookDetailsHeading  = "Details"
	bookBooksHeading    = "Books"
	bookShowMore        = "Show more"
	bookBackToStart     = "Back to the start"
	bookBadCursor       = "This list changed. Reload the page to see it."
	searchBooksFound    = "%d books found"
	searchPeopleFound   = "%d people found"
	searchSeriesFound   = "%d series found"
	searchShowAll       = "Show all %d books"
	searchPageTitle     = "Search"
)

// Labels of the Details tab (New).
const (
	bookLabelPublisher = "Publisher"
	bookLabelPublished = "Published"
	bookLabelLanguage  = "Language"
	bookLabelISBN      = "ISBN"
	bookLabelASIN      = "ASIN"
	bookLabelEdition   = "Edition"
	bookLabelGenres    = "Genres"
	bookLabelTags      = "Tags"
	bookLabelLength    = "Length"
	bookLabelChapters  = "Chapters"
	bookLabelFiles     = "Files"
	bookLabelAdded     = "Added"
	bookLabelAddedBy   = "Added by" // NEW
	bookAddedByYou     = "you"      // NEW: reads "Added by you"
	bookLabelFolder    = "Folder"
)
