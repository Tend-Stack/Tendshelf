package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
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

// P1c-6 play buttons, Home rows and the Show filter. "Start listening", "Resume", "Listen again",
// "Continue listening", "Chapter {n} · {m} min left in chapter · {h} h {m} m left in book",
// "Continue series", "Start series" and the filter words are in the copy deck and the boards; the
// rest follows the voice rules in BRAND.md.
const (
	playStart    = "Start listening"
	playResume   = "Resume"
	playAgain    = "Listen again"
	homeContinue = "Continue listening"
	// NEW (B2): the hero of a book last read; "Continue reading" is in the copy deck, the line follows homeWhere.
	homeContinueReading = "Continue reading"
	homeWhereReading    = "Chapter %d · about %s left in chapter · about %s left in book"
	homeReadBy          = "read by %s"
	homeWhere           = "Chapter %d · %d min left in chapter · %s left in book"
	homeWhereBook       = "%s left in book"
	homeChapters        = "Chapters"
	homeBookProgress    = "Book progress"
	homeRulerChapter    = "Book progress, chapter %d of %d"
	homeSeriesTitle     = "Your series in progress"
	homeSeriesLine      = "Book %d of %d · %s · %d%%"
	homeSeriesNext      = "Book %d of %d · up next: %s"
	homeSeriesCont      = "Continue series %s"
	homeSeriesStart     = "Start series %s"
	homeBecause         = "Because you listened to"
	homeShared          = "Shared with you"
	homeSharedFrom      = "from %s"
	homeSharedTwo       = "%s and %s"
	homeSharedOne       = "%s, %s and 1 other"
	homeSharedMany      = "%s, %s and %d others"
	playMarkFinished    = "Mark as finished"
	playMarkNot         = "Mark as not started"
	chapterPlayed       = "%s, played"
	showLabel           = "Show"
	showAny             = "Any"
	showUnfinished      = "Unfinished"
	showNotStarted      = "Not started"
	showFinished        = "Finished"
	showShared          = "Shared with you"
)

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
	bookmarkNoteSaved    = "Note saved."
	bookmarkDeleted      = "Bookmark deleted."
	bookmarkExportTitle  = "Export bookmarks"
	bookmarkExportText   = "Plain text"
	bookmarkExportMD     = "Markdown"
	bookmarkExportGroup  = "Book"
	bookmarkExportHeader = "Bookmarks and notes"
)

// L5 clips (New; listed in docs/copy-additions.md). A clip is a bookmark with an end.
const (
	clipPlay        = "Play this clip"
	clipChip        = "Clips · %d"
	clipChipKind    = "%s · %d"
	clipKindName    = "Kind"
	clipEndLabel    = "Ends at"
	clipEndHint     = "Leave empty for a single moment. Counted from the start of the book, like 1:02:30. This clip starts at %s."
	clipLength      = "Clip · %s"
	clipExportClip  = "Clip %s"
	clipErrEnd      = "The end must be after the start."
	clipErrEndClock = "Write the end as minutes and seconds, like 12:30, or with hours, like 1:02:30."
	clipEmptyKind   = "Nothing is saved as %s yet."
	clipEmptyClips  = "No clips yet. Give a bookmark an end to make one."
)

// clipKinds are the kinds in the order the sheet and the chips list them.
var clipKinds = []struct{ Value, Label string }{
	{"bookmark", "Bookmark"}, {"favourite", "Favourite"}, {"quote", "Quote"}, {"note", "Note"}, {"revisit", "Revisit"},
}

// clipKindWord is the word for a kind, "" for the plain bookmark.
func clipKindWord(kind string) string {
	if kind == "bookmark" {
		return ""
	}
	for _, k := range clipKinds {
		if k.Value == kind {
			return k.Label
		}
	}
	return ""
}

// P1c-4 player. "Rewound 5 s after your break" and the hint style are in BRAND.md; "Chapter {n} · {m}
// min left" follows the MiniPlayer board; the rest follows the voice rules. {name} marks a value the
// player fills in.
const (
	msgPlayerOpen     = "Open {title}, chapter {n}"
	msgOpenBook       = "Open {title}"
	msgMinLeft        = "{m} min left"
	msgPlayPrefix     = "Play"
	msgPausePrefix    = "Pause"
	msgBackSeconds    = "Back {n} seconds"
	msgForwardSeconds = "Forward {n} seconds"
	msgChapterLeft    = "Chapter {n} · {m} min left"
	msgRewound        = "Rewound {s} s after your break"
	msgCompatWait     = compatWaitText
	msgCompatFailed   = "This book can't play in this browser."
	// Android app only (A4): the app could not stream the book. Not in the copy deck; listed for review.
	msgNativeFailed = "This book can't play right now. Check your connection."
	// msgNativeBackground: Android refused to start playback while the app was out of sight.
	msgNativeBackground = "Tendshelf can't start by itself while it is closed. Tap Play to listen."
	msgSignedOut        = "You're signed out. Sign in to keep your place."
	msgSignIn           = "Sign in"
	msgContinueAsk      = "You were at {time} on {device}. Continue there?"
	msgOtherDevice      = "another device"
	msgContinueThere    = "Continue there"
	msgStayHere         = "Stay here"

	// P1c-5a full player: the board and the HANDOFF words; the rest follows the voice rules.
	msgChapterOf     = "Chapter {n} of {total}"
	msgLeftInChapter = "{t} left in chapter"
	// A length of time in plain words, one source for the cards (durationText) and the full player's
	// "left in book" line, so the two cannot drift. {h} is hours, {m} is minutes.
	msgDurationMinutes      = "{m} m"
	msgDurationHours        = "{h} h"
	msgDurationHoursMinutes = "{h} h {m} m"
	msgLeftInBookSuffix     = " left in book"
	msgLeftInBookMinutes    = msgDurationMinutes + msgLeftInBookSuffix
	msgLeftInBookHours      = msgDurationHours + msgLeftInBookSuffix
	msgLeftInBookBoth       = msgDurationHoursMinutes + msgLeftInBookSuffix
	msgSubtitleSep          = " · " // between the chapter title and the author under the book's title
	msgScrubLabel           = "Position in chapter"
	msgScrubValue           = "Chapter {n}, {m} minutes {s} seconds elapsed"
	msgFineScrub            = "Drag up for fine control"
	msgScrubBubble          = "Chapter {n} · {title} · {t}"
	// The desktop bar's chapter waveform (PB1); {t} is a clock, {d} a spoken-short distance. U+2212 is a true minus.
	msgWaveLabel       = "Position in chapter {n}"
	msgWaveValue       = "{t} of {total}, chapter {n}, {title}"
	msgHour            = "{n} hour"
	msgHours           = "{n} hours"
	msgMinute          = "{n} minute"
	msgMinutes         = "{n} minutes"
	msgSecond          = "{n} second"
	msgSeconds         = "{n} seconds"
	msgWaveDrawing     = "Drawing the waveform for chapter {n}…"
	msgWaveChapter     = "Chapter {n} · {title}"
	msgWaveLeft        = "−{t}"
	msgWaveAhead       = "{d} ahead"
	msgWaveBack        = "{d} back"
	msgWaveDragChapter = "chapter {n}"
	msgDeltaMin        = "{m} min"
	msgDeltaSec        = "{s} s"
	msgBookTip         = "Chapter {n} of {total} · {p}% of the book"
	msgBarMeta         = "{author} · {left}"
	msgShowPlayer      = "Show player"
	msgClosePlayer     = "Close player"
	msgPrevChapter     = "Previous chapter"
	msgNextChapter     = "Next chapter"
	msgSpeed           = "Speed"
	msgSleep           = "Sleep"
	msgChapters        = "Chapters"
	msgBookmark        = "Bookmark"
	msgMoreActions     = "More"
	msgGoToBook        = "Go to book"
	msgMarkFinished    = "Mark as finished"
	msgChapterPlayed   = "{title}, played"
	msgOpenPlayer      = "Open player: {title}, chapter {n}, {m} min left"

	msgKeysTitle   = "Keyboard shortcuts"
	msgKeysPlay    = "Play or pause"
	msgKeysSkip    = "Back or forward"
	msgKeysSearch  = "Search"
	msgKeysLibrary = "Library"
	msgKeysHome    = "Home"
	msgKeysList    = "This list"
	msgKeysClose   = "Close"

	// P1c-5b speed, sleep timer, volume, bookmark key. The board and the deck give the words marked
	// there; the rest follows the voice rules.
	msgSpeedValue        = "{v}×"
	msgSpeedLabel        = "Playback speed, {v} times"
	msgSpeedFine         = "Fine-tune"
	msgSpeedForBook      = "This speed is for this book. Other books keep your usual speed."
	msgSpeedMakeUsual    = "Make it my usual speed"
	msgJumpedFar         = "Jumped too far?"
	msgJumpedBack        = "Go back"
	msgSpeedUsualSet     = "This is now your usual speed."
	msgSleepTitle        = "Sleep timer"
	msgSleepEndOfChapter = "End of chapter"
	msgSleepMinutes      = "{n} min"
	msgSleepIn           = "in {m} min"
	msgSleepCustom       = "Custom"
	msgSleepCustomLabel  = "Minutes"
	msgSleepStart        = "Start timer"
	msgSleepTurnOff      = "Turn off"
	msgSleepOffLabel     = "Sleep timer, off"
	msgSleepOnLabel      = "Sleep timer, {t} left"
	msgSleeping          = "Sleeping in {n} seconds"
	msgSleepFading       = "Fading out."
	msgSleepFadingShake  = "Fading out. Shake your phone to add 10 minutes."
	msgPlusTen           = "+10 min"
	msgVolume            = "Volume"
	msgBookmarkMoment    = "Bookmark this moment"
	msgOpenReader        = "Read this book" // NEW (B2): the player's book icon
	msgBookmarkFailed    = "That bookmark didn't save. Try again."
	msgBookmarkQueued    = "Bookmarked at {time}. It saves when you're back online."
	msgKeysBookmark      = "Bookmark"
)

// playerMsgs fills the strings the layout hands to the player (data-ts-msg-* on #player).
func playerMsgs() components.PlayerMsgs {
	return components.PlayerMsgs{
		Open: msgPlayerOpen, OpenBook: msgOpenBook, MinLeft: msgMinLeft, PlayPrefix: msgPlayPrefix, PausePrefix: msgPausePrefix,
		BackSeconds: msgBackSeconds, ForwardSeconds: msgForwardSeconds, ChapterLeft: msgChapterLeft,
		Rewound: msgRewound, CompatWait: msgCompatWait, CompatFailed: msgCompatFailed, NativeFailed: msgNativeFailed, NativeBackground: msgNativeBackground,
		SignedOut: msgSignedOut, SignIn: msgSignIn, ContinueAsk: msgContinueAsk, OtherDevice: msgOtherDevice,
		ContinueThere: msgContinueThere, StayHere: msgStayHere,
		KeysTitle: msgKeysTitle, KeysPlay: msgKeysPlay, KeysSkip: msgKeysSkip, KeysSearch: msgKeysSearch,
		KeysLibrary: msgKeysLibrary, KeysHome: msgKeysHome, KeysList: msgKeysList, KeysClose: msgKeysClose,
		ChapterOf: msgChapterOf, LeftInChapter: msgLeftInChapter, LeftInBookMinutes: msgLeftInBookMinutes,
		LeftInBookHours: msgLeftInBookHours, LeftInBookBoth: msgLeftInBookBoth, SubtitleSep: msgSubtitleSep, ScrubLabel: msgScrubLabel,
		ScrubValue: msgScrubValue, FineScrub: msgFineScrub, ClosePlayer: msgClosePlayer, PrevChapter: msgPrevChapter,
		NextChapter: msgNextChapter, Speed: msgSpeed, Sleep: msgSleep, Chapters: msgChapters, Bookmark: msgBookmark,
		MoreActions: msgMoreActions, GoToBook: msgGoToBook, MarkFinished: msgMarkFinished, ChapterPlayed: msgChapterPlayed,
		OpenPlayer: msgOpenPlayer, Resume: playResume, ScrubBubble: msgScrubBubble,
		SpeedValue: msgSpeedValue, SpeedLabel: msgSpeedLabel, SpeedFine: msgSpeedFine, SpeedForBook: msgSpeedForBook, SpeedMakeUsual: msgSpeedMakeUsual, JumpedFar: msgJumpedFar, JumpedBack: msgJumpedBack, SpeedUsualSet: msgSpeedUsualSet, SleepTitle: msgSleepTitle,
		SleepEndOfChapter: msgSleepEndOfChapter, SleepMinutes: msgSleepMinutes, SleepIn: msgSleepIn,
		SleepCustom: msgSleepCustom, SleepCustomLabel: msgSleepCustomLabel, SleepStart: msgSleepStart,
		SleepTurnOff: msgSleepTurnOff, SleepOffLabel: msgSleepOffLabel, SleepOnLabel: msgSleepOnLabel,
		Sleeping: msgSleeping, SleepFading: msgSleepFading, SleepFadingShake: msgSleepFadingShake,
		PlusTen: msgPlusTen, Volume: msgVolume, BookmarkMoment: msgBookmarkMoment, OpenReader: msgOpenReader, BookmarkFailed: msgBookmarkFailed,
		KeysBookmark: msgKeysBookmark,
		// one source with the Bookmarks tab (P1c-7a): its %s becomes the player's {time}
		Bookmarked:       strings.Replace(bookmarkToast, "%s", "{time}", 1),
		BookmarkMomentAt: strings.Replace(bookmarkAddButton, "%s", "{time}", 1), BookmarkQueued: msgBookmarkQueued,
		AddNote:   bookmarkAddNote,
		WaveLabel: msgWaveLabel, WaveValue: msgWaveValue, Hour: msgHour, Hours: msgHours, Minute: msgMinute, Minutes: msgMinutes,
		Second: msgSecond, Seconds: msgSeconds, WaveDrawing: msgWaveDrawing, WaveChapter: msgWaveChapter, WaveLeft: msgWaveLeft,
		WaveAhead: msgWaveAhead, WaveBack: msgWaveBack, WaveDragChapter: msgWaveDragChapter, DeltaMin: msgDeltaMin,
		DeltaSec: msgDeltaSec, BookTip: msgBookTip, BarMeta: msgBarMeta,
		ShowPlayer: msgShowPlayer, NowPlaying: nowPanelTitle, UpNext: queueTitle,
	}
}
