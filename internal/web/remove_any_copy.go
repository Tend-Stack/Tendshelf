package web

// The words of removing any book, including one put in the library folder by hand (decision 0045). The first
// block is the plan's section 6, exactly; the second block is added words, listed in docs/copy-additions.md.

const (
	removeAnyTitle     = "Remove this book and its files?"
	removeAnyBody      = "Tendshelf found this book in the library folder. These files move to a holding folder inside it, and are deleted for good after 30 days:"
	removeAnyFileLine  = "%s · %s"
	removeAnyTotal     = "%d files · %s"
	removeAnyStaying   = "%d other files in the folder stay, and so does the folder."
	removeAnyLoss      = "Everyone's listening places, bookmarks and shelf entries for this book go too. Putting the files back brings the book back without them."
	removeAnyCheck     = "I understand these files leave the library."
	removeAnyButton    = "Remove this book and its files"
	removeAnyKeep      = "Remove from Tendshelf, keep the files"
	removeAnyCancel    = "Keep it"
	removeAnyKeepHelp  = "The files stay where they are. Tendshelf stops showing the book until you show it again."
	removeAnyMoving    = "Moving the files…"
	removeAnyHeld      = "Removed. The files are kept until %s."
	removeAnyPutBack   = "Put back"
	removeAnyDeleteNow = "Delete now"
	removeAnyShowAgain = "Show again"

	removeAnyPlanChanged      = "The files changed since you opened this page. Look at the list again."
	removeAnyChangedSinceScan = "Some files changed since the last scan, so nothing was moved. Scan the library, then try again."
	removeAnyReadOnly         = "Tendshelf cannot change files in this folder, so nothing was moved."
	removeAnyCannotMove       = "Tendshelf could not move every file, so it put them all back. Nothing was removed."
	removeAnyNotConfirmed     = "Tick the box to confirm."
	removeAnyBusy             = "This book is already being removed."
)

// Added words (docs/copy-additions.md).
const (
	removeAnyTotalOne        = "1 file · %s"
	removeAnyStayingOne      = "1 other file in the folder stays, and so does the folder."
	removeAnyKeptOut         = "Removed from Tendshelf. The files stay where they are."
	removeAnyRestored        = "Put back. The book comes back in a moment."
	removeAnyRestoredKept    = "Put back. %d files stayed in the holding folder because something else has their name."
	removeAnyRestoredKeptOne = "Put back. 1 file stayed in the holding folder because something else has its name."
	removeAnyPurged          = "Deleted for good."
	removeAnyGone            = "The files were deleted for good."
	removeAnyFailedTitle     = "The files were not moved."
	removeAnyInterrupted     = "The move was interrupted, so Tendshelf put every file back. Nothing was removed."
	removeAnyFailedOther     = "Tendshelf could not remove this book. Nothing was moved."
	removeAnyNotHeld         = "These files are not waiting in the holding folder."
	removeAnyDeleteCheck     = "I understand these files are deleted for good."
	removeAnyBack            = "Back to the library"
	removeAnyListTitle       = "Removed books"
	removeAnyListLead        = "Books removed from %s. Put back brings a book and its files back. Files in the holding folder are deleted for good after 30 days."
	removeAnyListEmpty       = "No removed books."
	removeAnyListBack        = "Back to Libraries & scanning"
	removeAnyKeptMeta        = "Files kept where they were"
	removeAnyDaysOne         = "1 day left"
	removeAnyDaysMany        = "%d days left"
	removeAnyDaysLess        = "Less than a day left"
	removeAnyPutBackAria     = "Put back %s"
	removeAnyDeleteAria      = "Delete now: %s"
	removeAnyShowAgainAria   = "Show again: %s"
	removeAnyPageTitle       = "Removing a book"
	removeAnyFolderLabel     = "Folder"
)

// removeAnyMessage is the plain-words text of a stored failure code of a removal.
func removeAnyMessage(code string) string {
	switch code {
	case "changed_since_scan":
		return removeAnyChangedSinceScan
	case "read_only":
		return removeAnyReadOnly
	case "cannot_move":
		return removeAnyCannotMove
	case "interrupted":
		return removeAnyInterrupted
	case "plan_changed":
		return removeAnyPlanChanged
	}
	return removeAnyFailedOther
}
