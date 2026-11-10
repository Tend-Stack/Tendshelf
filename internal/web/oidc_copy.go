package web

// Words of sign-in with an OpenID Connect provider (decision 0037, plan docs/plans/oidc.md). Not in
// the copy deck; listed for review in docs/copy-additions.md. %s is the provider's name unless
// said otherwise.
const (
	// The sign-in page and the callback's answers.
	ssoButton      = "Sign in with %s"                                                                                                       // NEW
	ssoOr          = "or use your password"                                                                                                  // NEW
	ssoFailed      = "Tendshelf could not sign you in with %s. Try again, or use your password."                                             // NEW
	ssoNoAccount   = "No account here uses this %s sign-in yet. Ask whoever runs this server for an invite, then link %s from your Account." // NEW (name, name)
	ssoEmailTaken  = "An account here already uses this email. Sign in with your password, then link %s from your Account."                  // NEW
	ssoDown        = "%s is not answering right now. Sign in with your password, or try again in a few minutes."                             // NEW
	ssoCancelled   = "Sign-in with %s was cancelled."                                                                                        // NEW
	ssoBusy        = "Too many sign-ins are under way. Try again in a few minutes."                                                          // NEW
	ssoWrongAddr   = "To sign in with %s in the app, connect it to %s."                                                                      // NEW (name, address)
	ssoMenu        = "Sign in with %s"                                                                                                       // NEW
	ssoMenuCaption = "Link or unlink your account"                                                                                           // NEW

	// The Account page (/me/sso).
	ssoTitle            = "Sign in with %s"                                             // NEW
	ssoLinkedLine       = "Linked to %s since %s."                                      // NEW (email or "your %s account", date)
	ssoLinkedFallback   = "your %s account"                                             // NEW
	ssoNotLinked        = "Not linked yet. Link it to sign in here without a password." // NEW
	ssoLinkButton       = "Link %s"                                                     // NEW
	ssoUnlinkButton     = "Unlink"                                                      // NEW
	ssoUnlinkNoPassword = "Add a password first, so you can still sign in."             // NEW
	ssoUnlinked         = "Unlinked. You can link it again any time."                   // NEW
	ssoLinked           = "Linked. Next time you can sign in with %s."                  // NEW
	ssoConfirmTitle     = "Link %s?"                                                    // NEW
	ssoConfirmBody      = "Sign in to Tendshelf with the %s account %s from now on."    // NEW
	ssoConfirmButton    = "Link it"                                                     // NEW
	ssoConfirmCancel    = "Not now"                                                     // NEW
	ssoTaken            = "That %s account is already linked to someone else here."     // NEW
	ssoAlready          = "You already have a %s account linked here."                  // NEW
	ssoLinkGone         = "This link step ran out. Start again."                        // NEW
	ssoWelcome          = "Use %s instead of a password"                                // NEW
	ssoPersonLinked     = "Signs in with %s"                                            // NEW
	ssoSignOutNote      = "Signing out of Tendshelf does not sign you out of %s."       // NEW

	// The owner's settings page (/admin/sso).
	ssoAdminTitle      = "Sign-in"                                                                                                            // NEW
	ssoAdminIntro      = "Let people sign in with Authelia, Authentik, Keycloak or another OpenID Connect provider. The password form stays." // NEW
	ssoAdminSaved      = "Saved."                                                                                                             // NEW
	ssoStatusOn        = "Sign-in with %s is on. People see its button on the sign-in page."                                                  // NEW
	ssoAdminOff        = "Sign in with a provider is off."                                                                                    // NEW
	ssoAdminOffDone    = "Turned off. Your settings are kept."                                                                                // NEW
	ssoHintRedirect    = "Give your provider this redirect address: %s"                                                                       // NEW
	ssoErrDiscovery    = "Tendshelf could not read the provider's settings at %s. Check the address."                                         // NEW
	ssoErrIssuer       = "The provider calls itself %s. Use that address."                                                                    // NEW
	ssoErrAlg          = "The provider signs with %s, which Tendshelf does not accept. Choose an RSA or EC signing key in the provider."      // NEW
	ssoErrRequired     = "Fill this in."                                                                                                      // NEW
	ssoErrInvalid      = "Check this value."                                                                                                  // NEW
	ssoErrTooLong      = "This is too long."                                                                                                  // NEW
	ssoErrIssuerFormat = "Use the full address, starting with https://."                                                                      // NEW
	ssoErrServerFormat = "Use the address people open this server at, like https://books.example.com."                                        // NEW
	ssoRoleMember      = "Member"                                                                                                             // NEW
	ssoRoleGuest       = "Guest"                                                                                                              // NEW
)
