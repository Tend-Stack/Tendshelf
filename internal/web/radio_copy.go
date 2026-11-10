package web

// Radio copy (decision 0038, plan R1). Plain words; no address or upstream text ever appears here.
const (
	radioErrBadAddress   = "That is not a web address Tendshelf can open. Paste the station's stream address."
	radioErrNoStation    = "Tendshelf couldn't find that station. You can still paste its stream address."
	radioErrSearchDown   = "Station search isn't working right now. You can still paste a stream address."
	radioErrBusy         = "Too many stations added at once. Wait a while and try again."
	radioErrTooMany      = "You keep 100 stations, the most Tendshelf stores."
	radioLiveBusy        = "Too many streams are playing. Try again in a moment."
	radioLivePersonBusy  = "You are already listening to a station. Stop it first."
	radioLiveUnavailable = "This station can't be played right now."
)
