package web

// Up next (queue) words that Q1 owns: the JSON API's messages. The UI package adds its own.
const (
	queueErrFull       = "Up next holds 100 items. Remove one to add another."
	queueErrUnplayable = "This book has no file Tendshelf can play."
	queueErrWhere      = "where must be next or end."
	queueErrDir        = "dir must be up or down."
	queueErrBookID     = "bookId is required."
	queueErrEnded      = "ended must be a book or episode id of 1 to 64 characters."
	queueErrKinds      = "kinds must name book, episode or both, separated by a comma."
)

// The Up next screens' words (Q2): the split button, the list and the toasts.
const (
	queueTitle       = "Up next"
	queueToggle      = "Queue options"
	queuePlayNext    = "Play next"
	queueAddEnd      = "Add to queue"
	queueAddedEnd    = "Added to Up next"
	queueAlready     = "Already in Up next"
	queueAddedFront  = "Added to the top of Up next"
	queueEmpty       = "Nothing is waiting. Choose Add to queue on a book and it plays after this one."
	queueMoveUpFmt   = "Move %s up"
	queueMoveDownFmt = "Move %s down"
	queueRemoveFmt   = "Remove %s from Up next"
)

const queueErrGone = "We couldn't find that."
