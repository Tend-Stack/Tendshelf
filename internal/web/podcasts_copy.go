package web

// Words of the podcast routes (decision 0038). P3 owns the page copy; these are the answers the routes
// give on their own: toasts, refusals and the plain-text errors of the audio proxy.
const (
	podcastsTitle        = "Podcasts"
	podcastsSearchTitle  = "Find a podcast"
	podErrBadAddress     = "That is not a web address Tendshelf can follow. Paste the show's RSS address."
	podErrSearchDown     = "Search isn't working right now. You can still paste a show's RSS address."
	podErrNoFeed         = "Tendshelf couldn't find that show's RSS address. You can still paste it."
	podErrBusy           = "Too many podcasts at once. Wait a minute and try again."
	podErrTooMany        = "You follow 200 shows, the most Tendshelf keeps."
	podErrTooSoon        = "This show was just refreshed. Try again in a few minutes."
	podErrBadValue       = "That value is not valid."
	podErrNoFile         = "Choose an OPML file."
	podErrNotOPML        = "That file is not an OPML list of podcasts."
	podErrOPMLBig        = "That file is too large. Tendshelf reads OPML files up to 1 MB."
	podErrVideo          = "This episode is a video, which Tendshelf doesn't play."
	podAudioBusy         = "Too many episodes are playing. Try again in a moment."
	podAudioUnavailable  = "This episode can't be played right now."
	podAudioMissing      = "This episode is not available any more."
	podAdminTitle        = "Podcasts and radio"
	podAdminSaved        = "Saved"
	podAdminBad          = "One of those values is not valid."
	podAdminCleared      = "Saved episodes removed."
	podcastsOPMLFileName = "tendshelf-podcasts.opml"
)
