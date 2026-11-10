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

// The Radio pages and the player's live mode (plan R2).
const (
	radioTitle         = "Radio"
	radioIntro         = "Live stations from around the world. Search for one and keep it here."
	radioFind          = "Find a station"
	radioSearchLabel   = "Search stations"
	radioSearchWord    = "Search"
	radioTabsLabel     = "Podcasts and radio"
	radioSavedTitle    = "Your stations"
	radioRecentTitle   = "Recently played"
	radioNoStationsHd  = "No stations yet"
	radioNoStations    = "Search above for a station and press the star to keep it, or paste its stream address."
	radioNoResults     = "No stations found. You can paste a stream address instead."
	radioResultsLabel  = "Station results"
	radioAdd           = "Add"
	radioAddByAddress  = "Add by stream address"
	radioAddHint       = "Paste the station's stream address."
	radioAddFieldLabel = "Stream address"
	radioAddButton     = "Add station"
	radioBack          = "Back to radio"
	radioPlay          = "Play"
	radioRemove        = "Remove"
	radioSave          = "Save"
	radioLive          = "Live"
	radioOpenStation   = "{title}, live. Show radio"
	radioLiveFailed    = "This station can't be played right now."
)
