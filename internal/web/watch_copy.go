package web

// Watch folders and the scheduled scan (S1). Every string here is NEW: none is in the copy deck yet.
// They follow the voice rules in BRAND.md.
const (
	watchTitle       = "Finding new books"
	watchLead        = "Tendshelf can notice books you add to a folder, so you don't have to press Scan. It waits until a copy has finished before it looks. A network drive can't tell Tendshelf what changed, so it is checked on a schedule instead."
	watchLabel       = "Watch for new books"
	watchEveryLabel  = "Also check every"
	watchSave        = "Save"
	watchErrValue    = "Pick one of the times in the list."
	watchNever       = "Never"
	watchHour        = "Hour"
	watchSixHours    = "6 hours"
	watchTwelveHours = "12 hours"
	watchDay         = "Day"
	watchStatusOff   = "Not watching. Press Scan to look for new books."
	watchStatusOn    = "Watching for new books."
	watchStatusSched = "Not watched live: this folder is on a network drive or is very large, so it is checked on the schedule."
	watchStatusGone  = "A folder isn't available right now. Tendshelf waits and checks again when it is back."
)
