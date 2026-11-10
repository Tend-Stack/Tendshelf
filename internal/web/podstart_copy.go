package web

// The words of the first-use page and the suggestion lists (BRAND.md voice: plain, short, no hype).
const (
	podStartTitle    = "Podcasts and radio"
	podStartNetTitle = "What turning it on does"
	podStartFormHead = "Turn on"
	podStartPodcasts = "Podcasts"
	podStartRadio    = "Radio"
	podStartButton   = "Turn on"
	podStartNotOwner = "The owner can turn this on in Settings."
	podStartNone     = "Pick podcasts, radio, or both."

	podStarterTitle = "Popular shows"
	podStarterHint  = "Picked by the Tendshelf project, not a chart. Nothing is followed until you tap Follow."
	podStarterCrime = "True crime"
	podStarterMore  = "More to try"

	radioPopularTitle = "Popular stations"
	radioPopularHint  = "The most listened to on OndaCast right now. Tap Save to keep one here."
)

var podStartLead = []string{
	"Follow the shows you like and see new episodes as they come out.",
	"Keep episodes on your device to listen offline.",
	"Tune in to live radio stations from around the world.",
}

var podStartNetwork = []string{
	"Searching sends only the words you type to ondacast.com, with no account and nothing that names you.",
	"Opening Radio asks ondacast.com for its popular stations. Feeds, audio, artwork and radio streams come straight from their publishers. Nothing else leaves this server.",
}
