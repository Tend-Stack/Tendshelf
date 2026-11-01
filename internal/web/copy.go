package web

import (
	"strconv"
	"time"
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
