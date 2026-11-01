package web

// Copy for personal shelves (P1b-8). "Your shelves" and "Add to shelf" are word for word from the
// design boards (LibraryPage sidebar, BookPhone); every line marked New follows the voice rules in
// BRAND.md and is listed in the package report.
const (
	// Board: LibraryPage (sidebar group), BookPhone (action).
	shelfSideGroup = "Your shelves"
	shelfAddLabel  = "Add to shelf"

	// New.
	shelfTitle         = "Your shelves"
	shelfSideNone      = "Shelves"
	shelfSideAll       = "All shelves"
	shelfNewTitle      = "New shelf"
	shelfNameLabel     = "Shelf name"
	shelfCreate        = "Create shelf"
	shelfEmptyListHead = "No shelves yet"
	shelfEmptyListBody = "Make a shelf for a mood, a person or a trip."
	shelfEmptyHead     = "This shelf is empty"
	shelfEmptyBody     = "Open a book and choose Add to shelf."
	shelfRename        = "Rename"
	shelfSave          = "Save"
	shelfDeleteLink    = "Delete shelf"
	shelfDeleteHead    = "Delete %s?"
	shelfDeleteBody    = "The books stay in your library."
	shelfKeep          = "Keep it"
	shelfMoveUp        = "Move %s up"
	shelfMoveDown      = "Move %s down"
	shelfRemove        = "Remove %s from this shelf"
	shelfSheetTitle    = "Add to shelf"
	shelfSheetNew      = "Create and add"
	shelfSheetClose    = "Close"
	shelfMore          = "Show more"
	shelfStart         = "Back to the start"

	shelfErrEmpty    = "Give the shelf a name."
	shelfErrLong     = "Use 80 characters or fewer."
	shelfErrTaken    = "You already have a shelf with that name."
	shelfErrShelves  = "You have reached the limit of 200 shelves."
	shelfErrFull     = "This shelf is full (5,000 books)."
	shelfErrControls = "Use letters, numbers and punctuation only."
)
