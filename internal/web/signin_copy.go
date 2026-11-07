package web

// Words of sign-in links (decision 0030, plan docs/plans/sign-in-links.md). Not in the copy deck;
// listed for review.
const (
	// The person's page (admin).
	personUsername           = "Username: %s"                                                                                                          // NEW
	personUsernameNoPassword = "Username: %s · No password yet"                                                                                        // NEW
	signInMake               = "Make a sign-in link"                                                                                                   // NEW
	signInMakeHelp           = "For when %s can't sign in. It works once, for 24 hours, then they choose a new password. A new link ends the old one." // NEW
	signInOpenLine           = "A sign-in link is open until %s."                                                                                      // NEW
	signInPanelTitle         = "Sign-in link for %s"                                                                                                   // NEW
	signInPanelNote          = "Works once, until %s. Send it only to %s."                                                                             // NEW
	signInFieldLabel         = "Sign-in link"                                                                                                          // NEW
	signInQRLabel            = "QR code for this sign-in link"                                                                                         // NEW

	// The public page.
	signInConfirmTitle  = "Sign in to %s"                                                                  // NEW
	signInConfirmBody   = "You'll be signed in as %s on this device."                                      // NEW
	signInConfirmButton = "Sign in"                                                                        // NEW
	signInBrokenTitle   = "This sign-in link doesn't work"                                                 // NEW
	signInBrokenBody    = "It may be used already or too old. Ask whoever runs this server for a new one." // NEW
	signInBoundNotice   = "This phone is signed in. Sign out first to use this sign-in link."              // NEW

	// Choosing the new password.
	signInNewTitle  = "Choose a new password"                             // NEW
	signInNewLead   = "Your username is %s. You'll need both to sign in." // NEW
	signInNewLabel  = "New password"                                      // NEW
	signInNewButton = "Save password"                                     // NEW
	signInNewSkip   = "Not now"                                           // NEW

	// The sign-in page.
	loginNoPassword = "No password yet, or forgot it? Ask whoever runs this server for a sign-in link." // NEW
)
