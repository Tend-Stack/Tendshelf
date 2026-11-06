package web

// Words of adding a phone by QR code and of invitations opened in the app (docs/copy-additions.md,
// decision 0028). Not in the copy deck; listed for review.
const (
	// The signed-in device (owner).
	addPhoneTitle     = "Add a phone"
	addPhoneIntro     = "Show a code here, then point your phone's camera at it. You will type a number from the phone, so no password is typed on the phone."
	addPhoneShow      = "Show a code"
	addPhoneScan      = "Scan this with your phone's camera. It works once, for 3 minutes."
	addPhoneLinkLabel = "Or open this link on the phone"
	addPhoneCopy      = "Copy link"
	addPhoneQRLabel   = "QR code for adding a phone"
	addPhoneWaiting   = "Waiting for your phone…"
	addPhoneClaimed   = "%s wants to sign in as you, from %s. Type the number on that phone. Only do this if the phone is in your hand."
	addPhoneNumber    = "Number on the phone"
	addPhoneApprove   = "Add phone"
	addPhoneNotMine   = "Not my phone"
	addPhoneWrong     = "That number did not match, so this code has ended. If the phone is yours, show a new code."
	addPhoneApproved  = "Now tap Connect on your phone."
	addPhoneEnded     = "This code has ended. Show a new one."
	addPhoneNewCode   = "Show a new code"
	addPhoneCheck     = "Check again"
	addPhoneLoopback  = "This address works only on this computer. Open Tendshelf at an address your phone can reach, then come back here."
	addPhoneNoSession = "Adding a phone needs a sign-in made in Tendshelf itself, not by a proxy in front of it."
	addPhoneBack      = "Back to your devices"
	addPhoneLink      = "Add a phone"
	addPhoneMenu      = "Add a phone…"
	addPhoneBusy      = "Too many codes. Please wait a little, then show a new one."

	// The phone.
	pairAsk        = "Add this phone to %s?"
	pairAskBody    = "Only go on if you are doing this yourself, with your other device in front of you."
	pairAskButton  = "Add this phone"
	pairNumberLead = "Type this number on your other device."
	pairConnectAs  = "Connect as %s"
	pairWaitTitle  = "Almost there"
	pairApprovedIs = "Your other device agreed. Tap the button to finish."
	pairEndedTitle = "This code has ended"
	pairEnded      = "This code has ended. Show a new one on your other device."
	pairPassword   = "Sign in with a password instead"
	pairAddress    = "Address: %s"
	pairInsecure   = "This address is not private. Others on this network could see what you do here."

	// The server's page that hands a link to the app.
	pairHandoverTitle  = "Add this phone"
	pairHandoverOpen   = "Open in the app"
	pairHandoverOthers = "Open this link on the phone you want to add."
	pairNoApp          = "The Tendshelf app isn't on this phone yet."

	// The invite page.
	inviteOpenInApp   = "Open in the app"
	inviteNoApp       = "The Tendshelf app isn't on this phone yet. You can join here in your browser."
	inviteOnPhone     = "Continue on your phone"
	inviteBoundNotice = "This phone is signed in. Sign out first to use this invitation."
	inviteQRLabel     = "QR code for this invitation"
)
