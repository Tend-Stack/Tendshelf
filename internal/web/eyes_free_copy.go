package web

import "github.com/Tend-Stack/tendshelf/internal/web/components"

// Eyes-free mode (M3b, decision 0013): the words of the big-button dialog. Most are the copy
// deck's or the player's own; the one marked NEW is not in the deck (listed in the package report).
const (
	msgEyesFree      = "Eyes-free"
	msgEyesFreeBack  = "Back to the player" // NEW: the board's "Back to reading" belongs to the reader
	msgEyesFreeChap  = "Chapter {n} · {m} min left in chapter"
	msgEyesFreeSkip  = "Back {n} s"
	msgEyesFreeFwd   = "Forward {n} s"
	msgEyesFreePlay  = "Play"
	msgEyesFreePause = "Pause"
	msgEyesFreeNote  = "Big buttons, no text to follow. The screen dims after 10 seconds; tap anywhere to wake it."
)

// eyesFreeMsgs fills the dialog's data-ts-msg-* words. The spoken labels of the skip buttons are the
// player's existing "Back {n} seconds" and "Forward {n} seconds"; the sleep states reuse its choices.
func eyesFreeMsgs() components.EyesFreeMsgs {
	return components.EyesFreeMsgs{
		Title: msgEyesFree, Back: msgEyesFreeBack, ChapterLine: msgEyesFreeChap,
		SkipBack: msgEyesFreeSkip, SkipForward: msgEyesFreeFwd,
		SkipBackLabel: msgBackSeconds, SkipForwardLabel: msgForwardSeconds,
		Play: msgEyesFreePlay, Pause: msgEyesFreePause, Bookmark: msgBookmark, Sleep: msgSleep,
		Note: msgEyesFreeNote, SleepChapter: msgSleepEndOfChapter, SleepMinutes: msgSleepMinutes,
		SleepOff: msgSleepTurnOff,
	}
}

// withEyesFree adds the Eyes-free words and the addresses of its script and stylesheet (loaded on
// first use; the worker's precache holds both) to the player's messages.
func (s *server) withEyesFree(m components.PlayerMsgs) components.PlayerMsgs {
	m.EyesFree = eyesFreeMsgs()
	m.EyesFreeJS = s.assets.URL("js/eyes-free.js")
	m.EyesFreeCSS = s.assets.URL("css/pages-eyes-free.css")
	return m
}
