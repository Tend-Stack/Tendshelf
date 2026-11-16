package web

import (
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Words of the Add books page, its queue rows, the owner's setting and "Remove this book"
// (decision 0015, "Copy"; board UploadBooks). Lines marked new are not in the copy deck and are
// listed in docs/copy-additions.md. {tokens} are filled by js/upload.js.
const (
	uploadTitle            = "Add books"
	uploadLeadContrib      = "Books you add go into your own folder, %s."     // NEW
	uploadLeadContribFirst = "Books you add go into a folder of your own."    // NEW
	uploadNeedsJSContrib   = "Adding books needs JavaScript in this browser." // NEW
	uploadPlacingOwn       = "Putting it in your folder"                      // NEW
	uploadLead             = "Books go into {path}, in a folder for each author."
	uploadPickHeading      = "Choose what to add"
	uploadFiles            = "Choose files"
	uploadFolder           = "Choose a folder"
	uploadHint             = "Audio files, ebooks, pictures and book details files. Up to 50 GB a book. Copy-protected files are refused. Archives inside archives are left out."
	// Plan U3: the drop line is the board's; the tick box, the "{n} books" header and the sentence about nested archives are new.
	uploadDrop      = "Drop audio files or a folder here"
	uploadSeveral   = "This folder holds several books"
	uploadQueue     = "This session"
	uploadLibraries = "Libraries"
	uploadNeedsJS   = "Adding books needs JavaScript in this browser. You can also copy a book's folder into {path}, then press Scan."

	uploadPause       = "Pause"
	uploadResume      = "Resume"
	uploadChooseAgain = "Choose files again"
	uploadCancel      = "Cancel upload"
	uploadTryAgain    = "Try again"
	uploadOpenBook    = "Open book"
	uploadChecking    = "Checking the files"
	uploadAdding      = "Adding it to your library"
	uploadNoSpace     = "The server ran out of space. Free some space, then press Try again."
	// New: the plan has no sentence for a placement that failed for another reason.
	uploadPlaceFailed = "Tendshelf could not finish adding this book. Check that the library folder is available, then press Try again."
	uploadNotAdded    = "Not added: %s"
	uploadNotAddedYet = "Not added yet: %s"

	uploadAdminTitle = "Adding books from this device"
	uploadAdminLead  = "Tendshelf makes a folder for each author in the folder you pick. It never changes or deletes anything already there."
	uploadAdminWhere = "Where new books go"
	uploadAdminOff   = "Nowhere (off)"
	uploadAdminSave  = "Save"
	uploadAdminOwner = "Only the owner can change this."
	uploadAdminOpen  = "Add books"

	bookRemoveLabel   = "Remove this book"
	bookRemoveConfirm = "Remove this book?"
	bookRemoveBody    = "This moves the %d files Tendshelf added for this book into a hidden holding folder. They are kept for 30 days, and the owner or an admin can put them back. Files changed since then stay."
	bookRemoveBodyOne = "This moves the file Tendshelf added for this book into a hidden holding folder. It is kept for 30 days, and the owner or an admin can put it back. Files changed since then stay."
	bookRemoveButton  = "Remove this book"
	bookRemoveCancel  = "Keep it"

	// Removing one part of a book that has both audio and text (decision 0015, "Remove one part").
	bookRemoveCombinedConfirm  = "What would you like to remove?"
	bookRemoveCombinedBody     = "This book has audio and a text book. Pick what to remove. Files Tendshelf added are kept for 30 days, and the owner or an admin can put them back. Files changed since then stay."
	bookRemoveTextButton       = "Remove the text book only"
	bookRemoveTextHelp         = "The audiobook, your listening place and your bookmarks stay."
	bookRemoveTextFileButton   = "Remove the text book %s only"
	bookRemoveAudioButton      = "Remove the audiobook only"
	bookRemoveAudioHelp        = "The text book stays, with your reading place, highlights and bookmarks. Your listening place goes with the audio and does not come back."
	bookRemoveWholeButton      = "Remove the whole book"
	bookRemoveWholeHelp        = "Moves the audio and every text book Tendshelf added into the holding folder. Your listening progress, bookmarks and reading places on this book go, and putting the files back does not bring them back."
	bookRemoveTextBody         = "This removes the book. Ebook files Tendshelf put in the library are kept for 30 days, and the owner or an admin can put them back. Your reading places, bookmarks and shelves for it go and do not come back."
	bookRemoveStayingOne       = "1 file stays on disk because Tendshelf did not place it."
	bookRemoveStaying          = "%d files stay on disk because Tendshelf did not place them."
	bookRemoveStayingOthersOne = "1 text book stays because someone else added it."
	bookRemoveStayingOthers    = "%d text books stay because someone else added them."
	bookRemoveEditionLabel     = "Remove this edition"
	bookRemoveEditionAria      = "Remove this edition: %s"
	bookRemoveEditionConfirm   = "Remove this edition?"
	bookRemoveEditionBody      = "This removes the ebook %s from this book. The other editions, the audiobook and your places stay."
	bookRemoveEditionHeld      = "This moves the ebook %s into the holding folder for 30 days; the owner or an admin can put it back. The other editions, the audiobook and your places stay."
	bookRemoveStoredGone       = "Ebooks uploaded on the Text page are deleted now and cannot be put back."
	bookRemoveEditionNo        = "Tendshelf did not add this ebook, or it is the only text of the book, so it will not remove it here."
	bookRemoveEditionDone      = "The edition was removed."
	bookRemoveTextKept         = "That file changed since Tendshelf added it, so it stays."
	bookRemoveTextDone         = "The text book was removed. The audiobook is still here."
	bookRemoveAudioDone        = "The audiobook was removed. The text book is still here."
)

func uploadRowWords() components.UploadRowWords {
	return components.UploadRowWords{Pause: uploadPause, Resume: uploadResume, ChooseAgain: uploadChooseAgain,
		Cancel: uploadCancel, TryAgain: uploadTryAgain, OpenBook: uploadOpenBook,
		OpenIt: uploadOpenIt, EditDetails: moreEditDetails, AddAnyway: uploadAddAnyway, DoNotAdd: uploadDoNotAdd}
}

// bookRemoveText is the sentence on the remove confirmation: "the file" for one, "the N files" for more.
func bookRemoveText(n int) string { return countText(n, bookRemoveBodyOne, bookRemoveBody) }

// uploadFilesText is "1 file" or "14 files".
func uploadFilesText(n int) string { return countText(n, "1 file", "%d files") }

// uploadLeftOut is "{n} other files were left out."
func uploadLeftOut(n int) string {
	return countText(n, "1 other file was left out.", "%d other files were left out.")
}

// uploadMessages are the sentences js/upload.js composes from, keyed by the data-ts-msg- suffix.
// New: the time units, the reconnect line and the refusal lines that name a file.
func uploadMessages() []pages.UploadMsg {
	return []pages.UploadMsg{
		{Key: "books", Text: "{n} books"},
		{Key: "files-one", Text: "1 file"},
		{Key: "files-many", Text: "{n} files"},
		{Key: "progress", Text: "{sent} of {total} · about {time} left"},
		{Key: "progress-plain", Text: "{sent} of {total}"},
		{Key: "paused", Text: "Paused. {sent} of {total} sent."},
		{Key: "checking", Text: uploadChecking},
		{Key: "retry", Text: "The connection dropped. Trying again in {n} s."},
		{Key: "refused-title", Text: fmt.Sprintf(uploadNotAdded, "{title}")},
		{Key: "refused-drm", Text: "{file} is copy-protected, so Tendshelf will not open it. Nothing was kept."},
		{Key: "refused-none", Text: "Not added. There is no audio or ebook file in it."},
		{Key: "sec", Text: "{n} s"},
		{Key: "min", Text: "{n} min"},
		{Key: "hour", Text: "{n} h"},
		{Key: "failed", Text: "Tendshelf could not reach the server. Reload this page, then choose the same files again."},
		{Key: "already-same", Text: uploadAlreadySame},
		{Key: "already-some", Text: uploadAlreadySome},
		{Key: "already-like", Text: uploadAlreadyLike},
		{Key: "already-ask", Text: uploadAlreadyAsk},
		{Key: "already-recent", Text: uploadAlreadyRecent},
		{Key: "mismatch", Text: "These are not the same files. Choose the files you picked before."},
	}
}
