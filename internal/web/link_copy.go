package web

// Share-link copy. Words marked NEW are not in the copy deck; they follow the voice rules in
// BRAND.md and are listed in the P3-5a report. The fixed headings of the Share page section ("Share
// a link anyone can open", "Make a link", "Add a password", "The link works for", "Stop") are
// written in pages/share.templ, like the rest of that page.
const (
	linkBrokenTitle   = "This link doesn't work"                     // NEW
	linkBrokenBody    = "Ask the person who sent it for a new link." // NEW
	linkWaitTitle     = "Please wait a little"
	linkPlayLabel     = "Play"                                     // NEW
	linkEnds          = "This link works until %s."                // NEW
	linkFine          = "Your place is kept in this browser only." // NEW
	linkLockedTitle   = "This book has a password"                 // NEW
	linkPasswordLabel = "Password"                                 // NEW
	linkUnlock        = "Listen"                                   // NEW
	linkWrongPassword = "That password isn't right."               // NEW

	linkMade         = "Link made %s"                                                                           // NEW
	linkEndsShort    = "Ends %s"                                                                                // NEW
	linkEndsPassword = "Ends %s · Password"                                                                     // NEW
	linkStopFor      = "Stop the link for %s"                                                                   // NEW
	linkReadyTitle   = "Your link is ready"                                                                     // NEW
	linkReadyNote    = "Anyone with this link can listen to %s until %s. Copy it now: it won't be shown again." // NEW
	linkFieldLabel   = "Link"                                                                                   // NEW
	linkDay1         = "1 day"                                                                                  // NEW
	linkDaysN        = "%d days"                                                                                // NEW

	linkErrDays         = "Choose how long the link works."             // NEW
	linkErrLabel        = "Keep the name under 80 characters."          // NEW
	linkErrPassword     = "Use at least 8 characters for the password." // NEW
	linkErrPasswordLong = "Use a shorter password."
	linkErrRefused      = "This book can't be shared with a link." // NEW
)
