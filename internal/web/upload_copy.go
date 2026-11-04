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
	uploadTitle       = "Add books"
	uploadLead        = "Books go into {path}, in a folder for each author."
	uploadPickHeading = "Choose what to add"
	uploadFiles       = "Choose files"
	uploadFolder      = "Choose a folder"
	uploadHint        = "Audio files, pictures and book details files. Up to 50 GB a book. Copy-protected files are refused. Archives inside archives are left out."
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
	bookRemoveBody    = "This deletes the %d files Tendshelf added for this book. Files changed since then stay."
	bookRemoveButton  = "Remove this book"
	bookRemoveCancel  = "Keep it"
)

func uploadRowWords() components.UploadRowWords {
	return components.UploadRowWords{Pause: uploadPause, Resume: uploadResume, ChooseAgain: uploadChooseAgain,
		Cancel: uploadCancel, TryAgain: uploadTryAgain, OpenBook: uploadOpenBook}
}

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
		{Key: "refused-none", Text: "Not added. There is no audio file in it."},
		{Key: "sec", Text: "{n} s"},
		{Key: "min", Text: "{n} min"},
		{Key: "hour", Text: "{n} h"},
		{Key: "failed", Text: "Tendshelf could not reach the server. Reload this page, then choose the same files again."},
		{Key: "mismatch", Text: "These are not the same files. Choose the files you picked before."},
	}
}
