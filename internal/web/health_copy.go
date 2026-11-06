package web

// Library health (S3). Every string here is NEW: none is in the copy deck yet. They follow the voice rules in
// BRAND.md and are listed in docs/copy-additions.md.
const (
	healthTitle       = "Library health"
	healthLead        = "Books that need a look, with a way to fix each one. Tendshelf only reads here; no file is changed."
	healthAllClear    = "Nothing needs a look. Every book is in good shape."
	healthShowAll     = "Show all %d"
	healthBack        = "Back to library health"
	healthPrev        = "Previous"
	healthNext        = "Next"
	healthPageText    = "Page %d of %d"
	healthOpenBook    = "Open book"
	healthEditChapter = "Edit chapters"
	healthLookItUp    = "Look it up"
	healthReviewDir   = "Review this folder"
	healthUnreadable  = "Tendshelf could not read this file. Check it, then scan again."

	healthRefusedDRM     = "Copy-protected, so Tendshelf can't open it. Nothing else is affected."
	healthRefusedNoAudio = "No audio Tendshelf can play."
	healthNoneSummary    = "Nothing needs a look."
	healthSummaryLabel   = "What needs a look"

	healthMissingHead = "Missing from disk"
	healthMissingNote = "Tendshelf can't find these books' files. If a whole folder was emptied on purpose, you can say so."
	healthRefusedHead = "Not opened"
	healthRefusedNote = "Copy-protected, damaged or unsupported. Tendshelf lists them and never opens them."
	healthUnreadHead  = "Files that can't be read"
	healthUnreadNote  = "A file in each of these books failed to open."
	healthNoChapHead  = "No chapters"
	healthNoChapNote  = "These books play as one piece. Add chapters to move around them."
	healthNoCoverHead = "No cover"
	healthNoCoverNote = "Open the book to choose a cover."
	healthNoMatchHead = "No match found online"
	healthNoMatchNote = "A lookup found nothing sure, or failed. Look again, or pick one yourself."
	healthPastEndHead = "Chapters past the end"
	healthPastEndNote = "An edited chapter ends after the audio does. Fix the times."
)
