package web

// The words of look-up and the word list (M5 G1). All of it is NEW copy (not in the deck), in the voice of BRAND.md.

const (
	lookupTitle        = "Look up"
	lookupNone         = "No dictionary has this word."
	lookupNoDictionary = "No dictionary on this server yet."
	lookupSearch       = "Search the web for “%s”"
	lookupOutside      = "This opens outside Tendshelf."
	lookupSave         = "Save word"
	lookupSaved        = "Word saved."
	lookupFull         = "Your word list is full."
	lookupBadWord      = "That is not a word Tendshelf can look up."
	lookupAdminHint    = "Add a StarDict or TSV file to the dictionaries folder in the data folder."
	lookupClose        = "Close"

	wordsTitle      = "Your word list"
	wordsLead       = "Words you saved while reading. Only you can see them."
	wordsEmptyTitle = "No words yet"
	wordsEmptyBody  = "Select a word in a book and choose Look up, then Save word."
	wordsBack       = "Profile"
	wordsDelete     = "Remove"
	wordsGoTo       = "Go to the page"
	wordsMore       = "Show more"
	wordsExportMD   = "Download as Markdown"
	wordsExportJSON = "Download as JSON"
	wordsRemoved    = "Word removed."
	wordsOneBook    = "Only words from “%s”."
	wordsAllBooks   = "Show every book"
	wordsFrom       = "From %s"
	wordsRemoveFmt  = "Remove “%s”"
)

// lookupSearchURL is the web search a reader may choose to open. Only the selected word is added to it, encoded;
// nothing is sent until the person taps the link (operator, 2026-11-04: DuckDuckGo).
const lookupSearchURL = "https://duckduckgo.com/?q="
