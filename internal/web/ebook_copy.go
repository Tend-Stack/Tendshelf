package web

// Copy for the Text tab (M5 R1 E4). Strings from the reader handoff's copy deck (boards AddEbook*,
// ProtectedPhone, Contents*) are used word for word. Lines marked "NEW" are not in the deck, follow
// the voice rules in BRAND.md and are listed in the package report.
const (
	ebookTabLabel = "Text" // NEW

	// Board: AddEbookDesktop, AddEbookPhone.
	ebookAddTitle    = "Add an ebook"
	ebookAddLead     = "With the text and the narration together, anyone you share this book with can read along, or just read."
	ebookFoundLabel  = "Found in your folders"
	ebookOrAddLabel  = "Or add a file"
	ebookChooseLabel = "Choose an ebook file" // NEW: the deck says "Choose an EPUB file"
	// NEW: the deck says "next to the audio", which is untrue for an upload.
	ebookHint      = "EPUB, PDF, Word, FB2, MOBI, AZW3, HTML, Markdown or plain text, up to 200 MB. No copy protection. Kept on your server."
	ebookAddGo     = "Add this ebook" // NEW
	ebookFoundIn   = "Found in %s"
	ebookFromThis  = "Added from this device" // NEW
	ebookSummary   = "%s · %s · %s"           // NEW: {format} · {n} chapters · {n} words
	ebookFront     = "Front matter"           // NEW
	ebookShowAll   = "Show all %d chapters"   // NEW
	ebookReading   = "Reading %s…"            // NEW
	ebookReadCheck = "Check the result"       // same words as the backup status link

	// Board: ProtectedPhone.
	ebookDRMTitle    = "This ebook is copy-protected"
	ebookDRMBody     = "Tendshelf can only open ebooks without copy protection, so it can't pair %s with the audiobook. Nothing was changed."
	ebookDRMAdvice   = "If the shop you bought it from offers a copy without protection, add that one instead."
	ebookDRMAnother  = "Choose another file"
	ebookDRMWhy      = "Why can't Tendshelf open it?"
	ebookDRMWhyBody  = "Copy protection locks a file to one shop's apps. Tendshelf never removes or works around it." // NEW
	ebookStillPlays  = "%s still plays as an audiobook."
	ebookClear       = "Clear" // NEW
	ebookClearLabel  = "Clear %s"
	ebookRefusedHead = "Tendshelf did not open %s" // NEW

	// NEW: the other refusal reasons and failures, in plain words.
	ebookUnsupported = "Tendshelf cannot read this kind of file."
	ebookTooLarge    = "This ebook is larger than Tendshelf reads."
	ebookDamaged     = "This file looks damaged, so Tendshelf did not open it."
	ebookPDFImages   = "This PDF is pictures of pages, so Tendshelf cannot read its words."
	ebookNoText      = "Tendshelf found no text in this file."
	ebookReadFailed  = "Tendshelf could not read this file. Try again later."
	ebookGone        = "This file is no longer in its folder."
	ebookPDFLayout   = "This PDF keeps its page layout. Read along will show its words plainly."

	// NEW: upload errors (the 422, 413, 409 and 507 answers).
	ebookNoFile = "Choose a file first."
)

// Copy for pairing (M5 R2 P2). Deck words (boards AddEbook*, NoMatchPhone) are used word for word;
// lines marked "NEW" are not in the deck and are listed in the package report.
const (
	ebookNotSame      = "Not the same book"
	ebookFoundInLead  = "Found in"
	ebookPairGo       = "Pair with this audiobook" // NEW: the label while no checked word timing exists for the book's language
	ebookSameTitle    = "Same title and author: %s"
	ebookSameTitleNA  = "Same title"    // NEW: the deck line needs an author
	ebookSameISBN     = "Same ISBN: %s" // NEW
	ebookChaptersSame = "%d chapters in both, with the same names"
	ebookChaptersNum  = "%d chapters in both"                                             // NEW
	ebookChaptersDiff = "%d chapters in the ebook, %d in the audiobook"                   // NEW
	ebookDiffTitle    = "Different title or author: %s by %s"                             // NEW
	ebookDiffTitleNA  = "Different title or author: %s"                                   // NEW: an ebook that names no author
	ebookBookEdition  = "The book's edition"                                              // NEW
	ebookYourEdition  = "Your edition"                                                    // NEW
	ebookReadThis     = "Read this edition"                                               // NEW
	ebookMakeDefault  = "Make it the book's edition"                                      // NEW
	ebookNotReadyYet  = "This ebook is still being read, or Tendshelf could not open it." // NEW
	ebookAlreadyHere  = "This ebook is already on this book."
	ebookPairMeta     = "%s · %s · %s" // NEW: {authors} · {FORMAT} · {n} pages
	ebookPairMetaNoA  = "%s · %s"      // NEW: {FORMAT} · {n} pages, for an ebook that names no author
	ebookPagesOf      = "%d pages"     // NEW
)

// Copy for preparing read-along on the Text tab (M5 R3 P5). Deck words (boards PreparingPhone, AddEbookPhone and
// the AlignmentCard README) are used word for word; lines marked "NEW" are not in the deck and are listed in the
// package report. The failure lines follow docs/read-along.md section 2.
const (
	ebookPairAndPrepare = "Pair and prepare read-along"
	alignPrepare        = "Prepare read-along" // NEW: for an ebook that is already paired
	alignPrepareAgain   = "Prepare read-along again"
	alignTryAgain       = "Try again"
	alignTitle          = "Preparing read-along"
	alignChapterOf      = "%d of %d"
	alignMatching       = "Matching chapter %d \u00b7 %s"
	alignMatchingNoName = "Matching chapter %d"        // NEW: a chapter with no title
	alignListening      = "Listening to the audiobook" // NEW
	alignWaiting        = "Waiting for its turn"       // NEW
	alignLeft           = "about %d min left"
	alignLeftShort      = "less than a minute left" // NEW
	alignRunsHere       = "This runs on your server, so you can close the app."
	alignCancel         = "Cancel"                        // NEW
	alignResume         = "Resume"                        // NEW
	alignDiscard        = "Discard"                       // NEW
	alignHeldTitle      = "Read-along is waiting for you" // NEW
	alignDone           = "Read-along is ready for %s"
	alignPartial        = "Read-along works for %d %% of the book."
	alignAbridged       = "The recording seems shorter than the ebook (abridged?). Parts not read are shown in grey."
	alignDifferent      = "This looks like a different edition. It still works; small differences are not highlighted."
	alignStale          = "The audio changed since read-along was prepared." // NEW
	alignNoMatch        = "This ebook does not seem to match the recording."
	alignNoMatchHint    = "If it is the wrong ebook, choose Not the same book." // NEW
	alignNoRuntime      = "Read-along for %s needs word timing for %s. An admin can set it up."
	alignNoRuntimeAny   = "Read-along is not available for this book's language yet."                                          // NEW
	alignNoAudio        = "The audio files are missing or cannot be read, so Tendshelf cannot prepare read-along."             // NEW
	alignFinished       = "This has already finished."                                                                         // NEW
	alignFailedOther    = "Tendshelf could not prepare read-along. Try again later."                                           // NEW
	alignHeldWorker     = "The word timing stopped on part of this book. Resume to try again, or discard this."                // NEW
	alignHeldRuntime    = "The word timing model changed. Resume to use the new one, or discard this."                         // NEW
	alignHeldAudio      = "The audio files changed while Tendshelf was working. Resume to use the new files, or discard this." // NEW
)
