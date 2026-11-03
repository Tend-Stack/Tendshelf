package web

import "github.com/Tend-Stack/tendshelf/internal/web/components"

// The words of the offline banner and the offline shell (screens/states/StateOffline, the copy deck).
// Words the deck lacks follow BRAND.md and are listed in docs/copy-additions.md.
const (
	offlineBannerOffline     = "You're offline. Downloaded books still play, and your place is saved."
	offlineBannerUnreachable = "Couldn't reach your server. Downloaded books still play."
	offlineBannerBack        = "Back online. Your place and bookmarks are saved."
	offlineRetry             = "Try again"

	offlineTitle   = "Offline"
	offlineCaption = "Showing what's on this phone. Everything else comes back when you're online."
	offlineSection = "On this phone"
	offlineNote    = "Bookmarks and progress you make now sync the moment you're back."
	offlineEmpty   = "No downloads yet"
	offlineEmptyBy = "Download a book to listen without a connection."

	offlineReady      = "Ready offline"
	offlineReadyChap  = "Ready offline · Chapter %d"
	offlineReadyPct   = "Ready offline · %d%%"
	offlineNeedsNet   = "Needs a connection"
	offlinePlayAria   = "Play %s"
	downloadOffAnswer = "Downloads of this book are turned off."
)

// offlineMsgs is what the banner carries to offline.js.
func offlineMsgs() components.OfflineMsgs {
	return components.OfflineMsgs{
		Offline: offlineBannerOffline, Unreachable: offlineBannerUnreachable,
		Back: offlineBannerBack, Retry: offlineRetry,
	}
}
