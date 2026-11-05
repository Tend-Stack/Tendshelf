package web

import "github.com/Tend-Stack/tendshelf/internal/web/pages"

// Words of the Downloads page and the book page's Download control (docs/plans/m3-offline.md "Copy").
// Board and deck words are exact; the lines marked new follow the BRAND voice and are listed in
// docs/copy-additions.md by the proof package. {tokens} are filled by js/downloads.js.
const (
	downloadsTitle     = "Downloads"
	downloadsLead      = "Books saved here play without a connection."
	downloadsShellNote = "Downloads are for your phone or browser. This app plays straight from your library or server."
	downloadsEmpty     = "No downloads yet"
	downloadsEmptyHow  = "Download a book to listen without a connection."

	downloadsStorage     = "Storage on this phone"
	downloadsRemoveTitle = "Remove finished books"
	downloadsRemoveHint  = "After 7 days. Frees space; your progress stays."
	downloadsActive      = "Downloading"
	downloadsReady       = "Ready offline"
	downloadsSettings    = "Download settings"

	// New.
	downloadsKeepOpen = "Keep Tendshelf open until downloads finish."
	downloadsPersist  = "This browser may clear downloads when the phone runs low on space."
	downloadsSafari   = "Safari may clear downloads after a week without a visit. Add Tendshelf to your Home Screen to keep them."
	downloadsInsecure = "Downloads need a secure (https) address. Ask the person who runs Tendshelf to set one up."
	downloadsInstall  = "Install Tendshelf"

	bookDownload = "Download"
)

// downloadsMessages is every data-ts-msg-* string js/downloads.js composes from. The key is the
// attribute's suffix; strings with {tokens} are filled in by the script.
var downloadsMessages = []pages.DownloadMsg{
	{Key: "free", Text: "{size} free"},
	{Key: "used", Text: "Tendshelf · {size}"},
	{Key: "bar", Text: "Tendshelf uses {used}; {free} free"},
	{Key: "progress", Text: "{done} of {total}"},
	{Key: "eta", Text: "about {n} min"},
	{Key: "waiting", Text: "Waiting"},
	{Key: "waiting-size", Text: "Waiting · {size}"},
	{Key: "waiting-net", Text: "Waiting for a connection"},
	{Key: "paused", Text: "Paused"},
	{Key: "paused-progress", Text: "Paused · {done} of {total}"},
	{Key: "preparing", Text: "Preparing a version this phone can play"},
	{Key: "ready", Text: "Ready offline · {size}"},
	{Key: "finished", Text: "finished {n} days ago"},
	{Key: "pause", Text: "Pause download"},
	{Key: "resume", Text: "Resume download"},
	{Key: "cancel", Text: "Cancel download"},
	{Key: "retry", Text: "Try again"},
	{Key: "remove", Text: "Remove download of {title}"},
	{Key: "downloading", Text: "Downloading {title}"},
	{Key: "error-space", Text: "There isn't room on this phone for {title}. Remove a download, then try again."},
	{Key: "error-gone", Text: "{title} is no longer available to download."},
	{Key: "error-failed", Text: "{title} didn't download. Try again."},
	{Key: "toast-ready", Text: "{title} is ready offline."},
}

func downloadsView() pages.DownloadsView {
	return pages.DownloadsView{
		Title: downloadsTitle, Lead: downloadsLead,
		Empty: downloadsEmpty, EmptyHow: downloadsEmptyHow,
		Storage: downloadsStorage, RemoveTitle: downloadsRemoveTitle, RemoveHint: downloadsRemoveHint,
		Active: downloadsActive, Ready: downloadsReady, Settings: downloadsSettings,
		KeepOpen: downloadsKeepOpen, Persist: downloadsPersist, Safari: downloadsSafari,
		Insecure: downloadsInsecure, Install: downloadsInstall,
		Messages:  downloadsMessages,
		ShellNote: downloadsShellNote,
	}
}
