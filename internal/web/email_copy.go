package web

// Email settings copy. Words marked NEW are not in the copy deck; they follow the voice rules in BRAND.md.
const (
	emailTitle = "Email" // NEW

	emailIntro        = "Tendshelf can send invitations for you. It is optional: without it, you send each invitation from your own email app." // NEW
	emailStatusOff    = "Email is not set up. Invitations open in your own email app."                                                          // NEW
	emailStatusNoTest = "Email is saved but not tested yet. Send a test email to turn on \"Send it for me\"."                                   // NEW
	emailStatusOn     = "Email is set up and tested. Invitations can be sent from Tendshelf."                                                   // NEW
	emailSaved        = "Email settings saved."                                                                                                 // NEW
	emailDropped      = "The server or username changed, so enter the password again."
	emailTestHint     = "Send yourself a message to check the settings. Until a test works, invitations are not sent from Tendshelf." // NEW
	emailOff          = "Turn off email"                                                                                              // NEW
	emailOffHint      = "Tendshelf forgets the server, the username and the password. Invitations still open in your own email app."  // NEW

	emailSESHelp = "Using Amazon SES? Server email-smtp.<region>.amazonaws.com, port 587 with STARTTLS, and the SMTP username and password from the SES console (not your AWS sign-in). The From address must be verified in SES. While your SES account is in the sandbox, it only sends to verified addresses."

	emailErrHost     = "Enter the server name, like email-smtp.eu-west-1.amazonaws.com."
	emailErrInsecure = "Tendshelf only sends without encryption to a server on this computer."
	emailErrAddress  = "That email looks incomplete."
	emailErrPort     = "Enter a port from 1 to 65535."
	emailErrOther    = "Remove line breaks and keep it shorter."
	emailErrNotReady = "Save the email settings first."
	emailErrBusy     = "That's a lot of test emails. Try again in a few minutes."

	emailTestSending   = "Sending a test email to %s…"
	emailTestDone      = "Sent. Check the inbox of %s."
	emailTestAuth      = "The email server didn't accept the username or password. With SES, use the SMTP credentials, not your AWS sign-in."
	emailTestSender    = "The email server won't send from %s. With Amazon SES, verify that address first."
	emailTestRecipient = "The email server refused %s. In the SES sandbox, it only sends to verified addresses."
	emailTestNoTLS     = "That server doesn't offer a secure connection, so Tendshelf won't send through it."
	emailTestTLS       = "Tendshelf couldn't check the server's certificate. Check the server name."
	emailTestReach     = "Tendshelf couldn't reach %s on port %d. Check the server and the port."
	emailTestOther     = "The email server refused the message. Check the email settings."
)
