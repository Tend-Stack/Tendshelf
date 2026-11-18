package web

import "fmt"

// Own lookup sites copy (decision 0046). Every word here is NEW: none is in the copy deck, so each follows
// the voice rules in BRAND.md.
const (
	providersTitle = "Your own lookup sites"                                                                                                       // NEW
	providersIntro = "Add a lookup site you run yourself, such as one made for Audiobookshelf. Tendshelf sends it only a book's title and author." // NEW
	providersOff   = "Online lookups are off, so these sites are not asked. Turn them on in Libraries & scanning."                                 // NEW
	providersEmpty = "No sites yet."                                                                                                               // NEW

	providersNameLabel    = "Name"                                                                                    // NEW
	providersNameHint     = "Shown under Also search."                                                                // NEW
	providersAddressLabel = "Address"                                                                                 // NEW
	providersAddressHint  = "The address before /search, like https://books.example.org or http://192.168.1.20:3001." // NEW
	providersAuthLabel    = "Authorization header (optional)"                                                         // NEW
	providersAuthHint     = "Only if the site asks for one. Tendshelf keeps it and never shows it again."             // NEW
	providersAdd          = "Add site"                                                                                // NEW
	providersAdded        = "Site added. Choose it under Also search when you look up a book."                        // NEW
	providersRemove       = "Remove"                                                                                  // NEW
	providersRemoved      = "Site removed."                                                                           // NEW
	providersTest         = "Test"                                                                                    // NEW
	providersAsking       = "Asking %s…"                                                                              // NEW
	providersFound        = "%s answered with %d books for a test search."                                            // NEW
	providersNothing      = "%s answered, but found nothing for a test search."                                       // NEW
	providersAuthSaved    = "Authorization header saved"                                                              // NEW
	providersCheck        = "Check the result"                                                                        // NEW
	providersAddHeading   = "Add a site"                                                                              // NEW
	providersListLabel    = "Lookup sites"                                                                            // NEW
	providersTestOther    = "The test did not finish. Try again."                                                     // NEW

	providersLink = "Your own lookup sites" // NEW

	providersErrName      = "Give the site a name, up to 40 characters."             // NEW
	providersErrNameTaken = "You already have a site with that name."                // NEW
	providersErrAddress   = "Enter an address that starts with https:// or http://." // NEW
	providersErrParts     = "Leave out the user name, the ? part and the # part."    // NEW
	providersErrPort      = "Use port 80, 443 or a port from 1024 to 65535."         // NEW
	providersErrAuth      = "Remove line breaks and keep it shorter."                // NEW
	providersErrTooMany   = "You can add up to 10 sites. Remove one first."          // NEW
	providersErrOther     = "Tendshelf could not save that site. Check the fields."  // NEW
)

// providersCount is the count that sits beside the link on the Libraries page.
func providersCount(n int) string {
	switch n {
	case 0:
		return "None yet" // NEW
	case 1:
		return "1 site" // NEW
	}
	return fmt.Sprintf("%d sites", n) // NEW
}
