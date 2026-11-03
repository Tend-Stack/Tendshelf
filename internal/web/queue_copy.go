package web

// Up next (queue) words that Q1 owns: the JSON API's messages. The UI package adds its own.
const (
	queueErrFull       = "Up next holds 100 books. Remove one to add another."
	queueErrUnplayable = "This book has no file Tendshelf can play."
	queueErrWhere      = "where must be next or end."
	queueErrDir        = "dir must be up or down."
	queueErrBookID     = "bookId is required."
	queueErrEnded      = "ended must be a book id of 1 to 64 characters."
)
