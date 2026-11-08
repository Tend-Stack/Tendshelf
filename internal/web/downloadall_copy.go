package web

import "github.com/Tend-Stack/tendshelf/internal/web/pages"

// Words of "Download all" on a series or shelf page (listed in docs/copy-additions.md). The scripts fill {tokens}:
// {count} books, {size} and {free} in decimal units, {done} and {total} books, {n} books that did not download.
var downloadAllMessages = []pages.DownloadMsg{
	{Key: "all-label", Text: "Download all"},
	{Key: "all-confirm-one", Text: "Download 1 book?"},
	{Key: "all-confirm-many", Text: "Download {count} books?"},
	{Key: "all-confirm-body", Text: "About {size}. Books you already have are skipped."},
	{Key: "all-go", Text: "Download"},
	{Key: "all-not-now", Text: "Not now"},
	{Key: "all-close", Text: "Close"},
	{Key: "all-have-title", Text: "Nothing new to download"},
	{Key: "all-have-body", Text: "You already have every book here."},
	{Key: "all-room-title", Text: "Not enough room"},
	{Key: "all-room-body", Text: "These books need about {size}, and this phone has about {free} free. Remove a download, then try again."},
	{Key: "all-error-title", Text: "Couldn't check these books"},
	{Key: "all-error-body", Text: "Check your connection, then try again."},
	{Key: "all-progress", Text: "Downloading {done} of {total}"},
	{Key: "all-stop", Text: "Stop"},
	{Key: "all-stopped", Text: "Stopped. {done} of {total} saved."},
	{Key: "all-finished", Text: "{total} books are ready offline."},
	{Key: "all-finished-one", Text: "1 book is ready offline."},
	{Key: "all-finished-some", Text: "{n} of {total} didn't download. Try again."},
}
