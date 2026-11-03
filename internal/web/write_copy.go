package web

// Words of the write-details screens (M4-9). They follow the voice rules in BRAND.md; the copy deck
// has none of them yet. The reasons a write is refused come from internal/mediajobs (writeText).
const (
	writeOpenLink     = "Write details into the files"
	writeSheetTitle   = "Write details into the files"
	writeSheetClose   = "Close"
	writeIntro        = "Tendshelf will put these details into the audio files themselves, so other apps show them too."
	writeFilesLabel   = "Files"
	writeDetailsLabel = "Details"
	writeNote         = "Tendshelf keeps a copy of each file until you choose Looks good or Undo."
	writeRefusedLabel = "Tendshelf can't write these files yet"
	writeButton       = "Write details into the files"
	writeCheckAgain   = "Check again"

	writeLabelTitle     = "Title"
	writeLabelAuthors   = "Authors"
	writeLabelNarrators = "Narrators"
	writeLabelSeries    = "Series"
	writeLabelYear      = "Year"
	writeLabelGenres    = "Genres"
	writeLabelChapters  = "Chapters"

	writeWaiting     = "Writing the details into the files…"
	writeProgress    = "Writing file %d of %d…"
	writeFailed      = "Tendshelf could not write the details, so your files were left as they were."
	writeSettled     = "This write is finished."
	writeJobDone     = "The details are in the files."
	writePendingHead = "Waiting for you"
	writePendingBody = "Listen to a little of the book, then choose Looks good or Undo."
	writeConfirmBtn  = "Looks good"
	writeUndoBtn     = "Undo"

	writeKept         = "Kept the new details."
	writeUndone       = "Put the files back as they were."
	writeAttention    = "Tendshelf stopped before changing this file. Both copies are still in its folder."
	writeAttentionDir = "Folder: %s"
)
