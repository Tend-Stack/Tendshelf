package web

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Interface copy for the auth pages. The users and auth packages return codes; the words live here.
const (
	msgBadCode        = "That setup code doesn't match. Check the server's log."
	msgBadCredentials = "That username and password don't match. Try again."
)

// fieldMessage turns a validation code from internal/users into a sentence. An unknown code gets a
// plain fallback, so a new code is never shown as raw text.
func fieldMessage(field, code string) string {
	switch field + "/" + code {
	case "display_name/required":
		return "Tell us your name."
	case "display_name/too_long":
		return "Use 80 characters or fewer."
	case "display_name/invalid":
		return "Use letters, numbers and punctuation only."
	case "username/taken":
		return "That username is taken."
	case "password/too_short":
		return "Use at least 10 characters."
	case "password/too_long":
		return "Use 256 characters or fewer."
	case "password/same_as_username":
		return "Choose a password that is different from your username."
	}
	switch field {
	case "username":
		return "Use 3 to 32 letters, numbers, dots, dashes or underscores."
	case "email":
		return "Check the email address."
	}
	return "Check this field."
}

// limitedMessage is the answer to too many failed tries.
func limitedMessage(retry time.Duration) string {
	n := int((retry + time.Minute - 1) / time.Minute) // round up
	if n < 1 {
		n = 1
	}
	unit := " minutes"
	if n == 1 {
		unit = " minute"
	}
	return "Too many tries. Wait " + strconv.Itoa(n) + unit + ", then try again."
}

// Copy for the Libraries admin page and the setup steps (P1b-4). Strings from the design board are
// verbatim; the others follow the voice rules in BRAND.md.
const (
	msgServerNameRequired = "Give your server a name."
	msgServerNameTooLong  = "Use 80 characters or fewer."
)

// pathMessages are the answers to a folder path that can not be used, by code.
var pathMessages = map[string]string{
	"path_relative":   "Type the full path to the folder, like /srv/media/audiobooks.",
	"path_missing":    "Tendshelf can't find that folder.",
	"path_unreadable": "Tendshelf isn't allowed to read that folder. Check its permissions.",
	"path_file":       "That's a file, not a folder.",
	"path_data":       "That folder holds Tendshelf's own data. Pick the folder with your audiobooks.",
	"path_overlap":    "That folder is already part of your library, or holds a folder that is.",
	"name_required":   "Give the library a name.",
}

func pathMessage(code string) string {
	if m, ok := pathMessages[code]; ok {
		return m
	}
	return "Tendshelf can't use that folder."
}

// quoted wraps a book title in the typographic quotes the board uses.
func quoted(s string) string { return "“" + s + "”" }

// titleList turns the capped list of a problem group into display lines, with a last line for the
// books left out.
func titleList(list []library.ProblemBook, total int) []string {
	out := make([]string, 0, len(list)+1)
	for _, b := range list {
		out = append(out, b.Title)
	}
	if total > len(list) {
		out = append(out, fmt.Sprintf("and %s more", thousands(total-len(list))))
	}
	return out
}

// problemCards writes the "things need a look" groups, in the order the board shows them.
func problemCards(p library.Problems) []pages.ProblemCard {
	var cards []pages.ProblemCard
	if list := p.Refused[library.RefusedDRM]; len(list) > 0 {
		n := p.RefusedCount[library.RefusedDRM]
		c := pages.ProblemCard{Kind: "drm", Title: countText(n, "1 protected file was skipped", "%d protected files were skipped")}
		if n == 1 {
			c.Body = quoted(list[0].Title) + " is copy-protected, so Tendshelf can’t open it. Nothing else is affected. " +
				"A copy without protection will be picked up automatically."
		} else {
			c.Body = "They're copy-protected, so Tendshelf can't open them. Nothing else is affected."
			c.Titles = titleList(list, n)
		}
		cards = append(cards, c)
	}
	if list := p.Refused[library.RefusedDamaged]; len(list) > 0 {
		n := p.RefusedCount[library.RefusedDamaged]
		cards = append(cards, pages.ProblemCard{Kind: "damaged",
			Title:  countText(n, "1 book couldn't be read", "%d books couldn't be read"),
			Body:   scan.RefusalText(library.RefusedDamaged),
			Titles: titleList(list, n)})
	}
	var none []library.ProblemBook
	noneN := 0
	for _, r := range []string{library.RefusedUnsupported, library.RefusedNoAudio} {
		none = append(none, p.Refused[r]...)
		noneN += p.RefusedCount[r]
	}
	if noneN > 0 {
		cards = append(cards, pages.ProblemCard{Kind: "unsupported",
			Title:  countText(noneN, "1 book has no audio Tendshelf can play", "%d books have no audio Tendshelf can play"),
			Titles: titleList(none, noneN)})
	}
	if p.MissingCount > 0 {
		c := pages.ProblemCard{Kind: "missing", Titles: titleList(p.Missing, p.MissingCount)}
		if p.MissingCount == 1 {
			c.Title = "1 book is missing its files"
			c.Body = "Its files weren't found in the last scan. It comes back when the files do."
		} else {
			c.Title = fmt.Sprintf("%d books are missing their files", p.MissingCount)
			c.Body = "Their files weren't found in the last scan. They come back when the files do."
		}
		cards = append(cards, c)
	}
	if n := p.NoCoverCount; n > 0 {
		c := pages.ProblemCard{Kind: "nocover", Title: countText(n, "1 book has no cover", "%d books have no cover")}
		switch {
		case n == 1:
			c.Body = quoted(p.NoCover[0].Title) + " had no picture in its files."
		case n == 2:
			c.Body = quoted(p.NoCover[0].Title) + " and " + quoted(p.NoCover[1].Title) + " had no picture in their files."
		default:
			c.Body = quoted(p.NoCover[0].Title) + ", " + quoted(p.NoCover[1].Title) + " and " +
				thousands(n-2) + " more had no picture in their files."
		}
		cards = append(cards, c)
	}
	return cards
}

func problemsTitle(n int) string {
	return countText(n, "1 thing needs a look", "%d things need a look")
}

// Folder browser. "Browse" is on the OnboardFolder board; the rest follows the voice rules in BRAND.md.
const (
	msgBrowseOpen    = "Tendshelf can't open that folder."
	msgBrowseTooMany = "This folder has too many entries to show. Type the path instead."
	msgBrowseSlow    = "This folder took too long to open. Type the path instead."
	msgBrowseEmpty   = "No folders inside."
)

// audioHereText is the count line under a listing: empty at zero.
func audioHereText(n int) string {
	if n <= 0 {
		return ""
	}
	return countText(n, "1 audio file here", "%d audio files here")
}

// P1c-7a bookmarks. "Bookmarks and notes", "Export", "Play from here", "No note", "Add a note", "Bookmark
// this moment", the empty state, "Chapter {n}", "Tonight" and "Yesterday" are in the copy deck; the
// rest follows the voice rules in BRAND.md.
const (
	bookTabBookmarks     = "Bookmarks"
	bookmarkHeading      = "Bookmarks and notes"
	bookmarkExport       = "Export"
	bookmarkChipAll      = "All · %d"
	bookmarkChipNotes    = "With notes · %d"
	bookmarkChipsLabel   = "Show"
	bookmarkPlayFrom     = "Play from here"
	bookmarkNoNote       = "No note"
	bookmarkAddNote      = "Add a note"
	bookmarkEditNote     = "Edit note"
	bookmarkAddButton    = "Bookmark this moment · %s"
	bookmarkToast        = "Bookmarked at %s · "
	bookmarkEmptyTitle   = "No bookmarks yet"
	bookmarkEmptyBody    = "Tap the bookmark button while listening to save a moment."
	bookmarkEmptyNotes   = "No bookmarks have a note yet."
	bookmarkChapterN     = "Chapter %d"
	bookmarkToday        = "Today"
	bookmarkTonight      = "Tonight"
	bookmarkYesterday    = "Yesterday"
	bookmarkSheetTitle   = "Note"
	bookmarkNoteLabel    = "Note"
	bookmarkSave         = "Save"
	bookmarkDelete       = "Delete bookmark"
	bookmarkClose        = "Close"
	bookmarkErrTooLong   = "Notes can be up to 2,000 characters."
	bookmarkErrLimit     = "This book has 1,000 bookmarks, the most it can hold."
	bookmarkErrPosition  = "That position is not a number of milliseconds."
	bookmarkExportTitle  = "Export bookmarks"
	bookmarkExportText   = "Plain text"
	bookmarkExportMD     = "Markdown"
	bookmarkExportGroup  = "Book"
	bookmarkExportHeader = "Bookmarks and notes"
)
