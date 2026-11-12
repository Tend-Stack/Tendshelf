package web

// Words for passkeys (decision 0041, plan docs/plans/passkeys.md section 5). Strings marked NEW are
// not in the copy deck and follow the voice rules in BRAND.md.
const (
	passkeyLoginButton    = "Sign in with a passkey"
	passkeyWaiting        = "Waiting for your passkey…"
	passkeyLoginFailed    = "That passkey did not work here. Try again, or sign in with your password."
	passkeyLoginCancelled = "Sign-in with a passkey was cancelled."
	passkeyBusy           = "Too many people are signing in right now. Try again in a moment." // NEW
	passkeyUnavailable    = "Passkeys are not available at this address."                      // NEW
	passkeyBadRequest     = "That request was not understood."                                 // NEW

	passkeyTitle        = "Passkeys"
	passkeyNone         = "None yet"
	passkeyOne          = "1 passkey"
	passkeyMany         = "%d passkeys"
	passkeyLead         = "A passkey signs you in with your phone or computer's fingerprint, face or screen lock. Nothing to remember. Your password still works."
	passkeyAddHead      = "Add a passkey"
	passkeyName         = "Name"
	passkeyNameHint     = "So you can tell them apart, like Phone or Work laptop."
	passkeyPass         = "Your password"
	passkeyPassHint     = "To make sure it is you."
	passkeyAddBtn       = "Add a passkey"
	passkeyAdded        = "Passkey added."
	passkeyStale        = "Sign out and back in, then add your passkey within 15 minutes."
	passkeyLimit        = "You have 20 passkeys, the most one account can hold. Remove one first."
	passkeyTaken        = "This passkey is already on your account."
	passkeyBadPass      = "That password is not right."
	passkeyAddFail      = "That passkey could not be added. Try again."
	passkeyAddCancelled = "Adding a passkey was cancelled."
	passkeyNameLong     = "Use 64 characters or fewer."

	passkeyAddedOn    = "Added %s · Last used %s"
	passkeyNotUsed    = "Added %s · Not used yet"
	passkeySynced     = "Synced across your devices"
	passkeyOneDevice  = "On one device"
	passkeyCopied     = "This passkey may have been copied, so Tendshelf stopped accepting it. Remove it and add a new one."
	passkeyRemove     = "Remove"
	passkeyRemoveAria = "Remove passkey %s"
	passkeyRemoved    = "Passkey removed. You can also delete it from your phone or password manager."
	passkeyLast       = "This passkey is your only way to sign in. Add a password first."

	passkeyNeedHTTPS = "Passkeys need a secure address (https). Ask whoever runs this server to set one up."
	passkeyNeedName  = "Passkeys need a server name, not a number address. Open Tendshelf by its name to add one."
	passkeyInShell   = "Add passkeys in a web browser. This app's window cannot use them yet."
	passkeyNoJS      = "Passkeys need JavaScript in this browser."
	passkeyNoSupport = "This browser cannot use passkeys."

	passkeyAdminCount  = "%d passkeys"
	passkeyAdminOne    = "1 passkey"
	passkeyAdminRemove = "Remove their passkeys"
	passkeyAdminDone   = "Their passkeys are removed. Their password and sign-in links still work."

	passkeyPasswordNote = "Changing your password does not remove your passkeys. Remove any you do not recognise on Passkeys."
)
