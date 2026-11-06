package web

// The words of /me/highlights (R1). The deck has none of them yet (docs/copy-additions.md); they follow the
// voice rules in BRAND.md.
const (
	highlightsTitle       = "All my highlights"
	highlightsBack        = "Profile"
	highlightsLead        = "Everything you highlighted in your books, newest first. Only you see this."
	highlightsLink        = "All my highlights"
	highlightsSearchLabel = "Search your highlights"
	highlightsColourLabel = "Colour"
	highlightsAnyColour   = "Any colour"
	highlightsApply       = "Search"
	highlightsOneBookFmt  = "Only %s."
	highlightsAllBooks    = "Show every book"
	highlightsOpen        = "Open in the book"
	highlightsOpenLabel   = "Open this highlight in %s"
	highlightsOlder       = "Older"
	highlightsNewer       = "Newer"
	highlightsCountFmt    = "%d highlights"
	highlightsCountOne    = "1 highlight"
	highlightsColourFmt   = "%s highlight"
	highlightsEmptyTitle  = "No highlights yet"
	highlightsEmptyBody   = "Select a sentence while you read and pick a colour. It shows up here."
	highlightsNoMatch     = "Nothing matches that."
	highlightsExportMD    = "Download as Markdown"
	highlightsExportJSON  = "Download as JSON"
	highlightsFileTitle   = "Highlights"
	highlightsDayFmt      = "2 Jan 2006"
)

// highlightsColours are the names shown for the four colours, in the order of the reader's toolbar.
var highlightsColours = []struct{ Value, Label string }{
	{"yellow", "Yellow"}, {"green", "Green"}, {"blue", "Blue"}, {"pink", "Pink"},
}
