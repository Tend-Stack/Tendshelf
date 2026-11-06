# Copy additions for review

Strings in the interface that are not in the copy deck (`docs/design/tendshelf-handoff/docs/HANDOFF.md`,
"Copy deck") or on the boards. Each follows the voice rules in `design-system/BRAND.md`: sentence case, a
verb on every button, no exclamation marks, no emoji, "book", "chapter", "folder" and "invite" and never
"item", "track" or "token". The operator reviews this list; a change is made in the named constant and the
tests that quote it. 187 strings, generated from the `// NEW` marks and the "new" blocks in
`internal/web/*copy*.go` on 2026-11-02.

Not listed: strings that are the deck's or a board's words, and the placeholders `%s` and `%d` are filled in
by the server. The deck's "Research door" lines are not used (Research was dropped, decision 0007).

Terms: the interface says **Listener** for the guest role (Users page, role picker, Profile chip). The word
"guest" appears only in code.

| String | Constant | File | Why it is new |
| --- | --- | --- | --- |
| This link doesn't work | `linkBrokenTitle` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Ask the person who sent it for a new link. | `linkBrokenBody` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Play | `linkPlayLabel` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| This link works until %s. | `linkEnds` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Your place is kept in this browser only. | `linkFine` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| This book has a password | `linkLockedTitle` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Password | `linkPasswordLabel` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Listen | `linkUnlock` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| That password isn't right. | `linkWrongPassword` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Link made %s | `linkMade` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Ends %s | `linkEndsShort` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Ends %s · Password | `linkEndsPassword` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Stop the link for %s | `linkStopFor` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Your link is ready | `linkReadyTitle` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Anyone with this link can listen to %s until %s. Copy it now: it won't be shown again. | `linkReadyNote` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Link | `linkFieldLabel` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| 1 day | `linkDay1` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| %d days | `linkDaysN` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Choose how long the link works. | `linkErrDays` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Keep the name under 80 characters. | `linkErrLabel` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| Use at least 8 characters for the password. | `linkErrPassword` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
| This book can't be shared with a link. | `linkErrRefused` | `link_copy.go` | Share links have no deck wording; plain words that say what the link does |
|  and 1 other book | `inviteTailOther` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| %s invited you to %s | `inviteLineServer` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| This invite link doesn't work | `inviteBrokenTitle` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| Ask the person who sent it for a new link. | `inviteBrokenBody` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| This invite has ended | `inviteEndedTitle` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| Ask %s for a new link. | `inviteEndedBody` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| This invite was already used | `inviteUsedTitle` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| If it was you, sign in. | `inviteUsedBody` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| Sign in | `inviteSignIn` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| %s hasn't opened the invite yet | `inviteWaiting` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| %s · Until %s | `inviteUntil` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| %s · No end date | `inviteNoEnd` | `invite_copy.go` | The invite page and its broken, ended and used states are not in the deck |
| Which library | `shareWhichLib` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| A shelf | `shareAShelf` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Which shelf | `shareWhichShelf` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Find a book | `shareFindBook` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Add a name so you know whose link it is. | `shareErrName` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Keep the name under 80 characters. | `shareErrNameLen` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Keep the note under 500 characters. | `shareErrNoteLen` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Choose what they can listen to. | `shareErrScope` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Choose what they can do. | `shareErrRole` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Choose how long the link works. | `shareErrExpires` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Invite link for %s | `shareLinkTitle` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Copy it and send it to %s. It works once, for %s. | `shareLinkNoteFor` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Copy it and send it to %s. It works once, until you stop it. | `shareLinkNoteEnd` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| New link for %s | `shareNewLink` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| The old link no longer works. | `shareOldLink` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Link to everything | `shareLinkAll` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Link to "%s" | `shareLinkOne` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Link to "%s" and %d more | `shareLinkMore` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Admin link | `shareLinkAdmin` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
|  · works for %d more days | `shareMetaDays` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
|  · works for 1 more day | `shareMetaDay` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
|  · works until you stop it | `shareMetaNoEnd` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Has this through everything | `shareViaAll` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Has this through the library “%s” | `shareViaLibrary` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Has this through the shelf “%s” | `shareViaShelf` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Stop sharing %s with %s? | `shareStopBookTitle` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| %s won't be able to open this book through this share any more. Their place in it and their bookmarks are kept. | `shareStopBookBody` | `share_copy.go` | Share and invite page: form errors, link notes, access lines; the board shows layout, not these words |
| Enter their email. | `shareErrEmail` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| That email looks incomplete. | `shareErrEmailBad` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Sending from Tendshelf isn't set up yet. Use Send invitation to send it from your own email app. | `shareErrRelayOff` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| That invite has no email address. | `shareErrNoEmail` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| It's still sending. | `shareErrStillSending` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Tendshelf couldn't queue the email. Copy the link and send it yourself. | `shareErrQueue` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| You've sent a lot of invites for now. Try again in an hour, or copy a link instead. | `shareErrMailBusy` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Tendshelf has emailed that address three times today. Copy the link instead. | `shareErrMailAgain` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Invitation for %s is ready | `shareMailReadyTitle` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Open your email app to send it to %s. The link below also works, once, if you'd rather send it another way. | `shareMailReadyNote` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Invite on its way to %s | `shareMailSentTitle` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Tendshelf is emailing it to %s. The link below also works, once, if you'd rather send it yourself. | `shareMailSentNote` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Link created for %s | `shareMailCreated` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Sending to %s… | `shareMailSending` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Sent to %s | `shareMailSent` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Couldn't send to %s. Check the address. | `shareMailBadAddress` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Couldn't send to %s. Send it again. | `shareMailLinkLost` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Tendshelf couldn't confirm the email to %s went out. | `shareMailUnconfirmed` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Couldn't send: check the email settings | `shareMailSettings` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Send it for me | `shareMailSendForMe` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Send again | `shareMailSendAgain` | `share_copy.go` | Emailed invitations (P3-8): no deck wording; says what happened and what to do next |
| Email | `emailTitle` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Tendshelf can send invitations for you. It is optional: without it, you send each invitation from your own email app. | `emailIntro` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Email is not set up. Invitations open in your own email app. | `emailStatusOff` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Email is saved but not tested yet. Send a test email to turn on "Send it for me". | `emailStatusNoTest` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Email is set up and tested. Invitations can be sent from Tendshelf. | `emailStatusOn` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Email settings saved. | `emailSaved` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| The server or username changed, so enter the password again. | `emailDropped` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Send yourself a message to check the settings. Until a test works, invitations are not sent from Tendshelf. | `emailTestHint` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Turn off email | `emailOff` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Tendshelf forgets the server, the username and the password. Invitations still open in your own email app. | `emailOffHint` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Using Amazon SES? Server email-smtp.<region>.amazonaws.com, port 587 with STARTTLS, and the SMTP username and password from the SES console (not your AWS sign-in). The From address must be verified in SES. While your SES account is in the sandbox, it only sends to verified addresses. | `emailSESHelp` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Enter the server name, like email-smtp.eu-west-1.amazonaws.com. | `emailErrHost` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Tendshelf only sends without encryption to a server on this computer. | `emailErrInsecure` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| That email looks incomplete. | `emailErrAddress` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Enter a port from 1 to 65535. | `emailErrPort` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Remove line breaks and keep it shorter. | `emailErrOther` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Save the email settings first. | `emailErrNotReady` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| That's a lot of test emails. Try again in a few minutes. | `emailErrBusy` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Sending a test email to %s… | `emailTestSending` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Sent. Check the inbox of %s. | `emailTestDone` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| The email server didn't accept the username or password. With SES, use the SMTP credentials, not your AWS sign-in. | `emailTestAuth` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| The email server won't send from %s. With Amazon SES, verify that address first. | `emailTestSender` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| The email server refused %s. In the SES sandbox, it only sends to verified addresses. | `emailTestRecipient` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| That server doesn't offer a secure connection, so Tendshelf won't send through it. | `emailTestNoTLS` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Tendshelf couldn't check the server's certificate. Check the server name. | `emailTestTLS` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Tendshelf couldn't reach %s on port %d. Check the server and the port. | `emailTestReach` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| The email server refused the message. Check the email settings. | `emailTestOther` | `email_copy.go` | Email settings tab (optional mail server): no deck wording |
| Yesterday | `usersYesterday` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Over a month ago | `usersOverMonth` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Never | `usersNever` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Access ended | `usersAccessEnded` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Nothing shared yet | `usersNothingShared` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Choose a library, shelf or book. | `usersErrTarget` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Pick a day after today. | `usersErrDay` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Choose a role. | `usersErrRole` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Everything on this server | `usersEverythingKind` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Book | `usersBookKind` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| No end date | `usersNoEnd` | `users_copy.go` | Users page: words the boards leave out (dates, empty and error states) |
| Your shelves | `shelfTitle` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Shelves | `shelfSideNone` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| All shelves | `shelfSideAll` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| New shelf | `shelfNewTitle` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Shelf name | `shelfNameLabel` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Create shelf | `shelfCreate` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| No shelves yet | `shelfEmptyListHead` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Make a shelf for a mood, a person or a trip. | `shelfEmptyListBody` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| This shelf is empty | `shelfEmptyHead` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Open a book and choose Add to shelf. | `shelfEmptyBody` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Rename | `shelfRename` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Save | `shelfSave` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Delete shelf | `shelfDeleteLink` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Delete %s? | `shelfDeleteHead` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| The books stay in your library. | `shelfDeleteBody` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Keep it | `shelfKeep` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Move %s up | `shelfMoveUp` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Move %s down | `shelfMoveDown` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Remove %s from this shelf | `shelfRemove` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Add to shelf | `shelfSheetTitle` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Create and add | `shelfSheetNew` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Close | `shelfSheetClose` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Show more | `shelfMore` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Back to the start | `shelfStart` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Shared by %s | `shelfSharedBy` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Share this shelf | `shelfShare` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Stop sharing | `shelfStopSharing` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Members and admins can see this shelf, but only the books they already have. | `shelfSharedNote` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Only you can see this shelf. | `shelfNotSharedNote` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Shared with you | `shelvesSharedTitle` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Give the shelf a name. | `shelfErrEmpty` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Use 80 characters or fewer. | `shelfErrLong` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| You already have a shelf with that name. | `shelfErrTaken` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| You have reached the limit of 200 shelves. | `shelfErrShelves` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| This shelf is full (5,000 books). | `shelfErrFull` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Use letters, numbers and punctuation only. | `shelfErrControls` | `shelf_copy.go` | Shelves: the boards show only the sidebar group and the Add to shelf action |
| Now playing | `nowPanelTitle` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Now playing: about this book | `nowPanelAriaLabel` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Close | `nowPanelClose` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| More actions | `nowPanelMenu` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Book page | `nowPanelBookPage` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Share | `nowPanelShare` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| About this book | `nowPanelTabsLabel` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| About | `nowPanelAbout` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Bookmarks | `nowPanelBookmarks` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Summary | `nowPanelSummary` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Series | `nowPanelSeries` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Details | `nowPanelDetails` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Read more | `nowPanelReadMore` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Show less | `nowPanelShowLess` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| No description for this book yet. | `nowPanelNoSummary` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Written by | `nowPanelWrittenBy` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Read by | `nowPanelReadBy` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| On the shelf | `nowPanelOnShelf` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Added %s | `nowPanelAddedOn` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Unabridged | `nowPanelUnabridged` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Abridged | `nowPanelAbridged` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| You're here | `nowPanelYoureHere` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| You're here · %d%% | `nowPanelYoureHereN` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Also read by %s | `nowPanelAlsoReadBy` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| More by %s | `nowPanelMoreBy` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| All %d bookmarks and notes | `nowPanelAllMarks` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| No bookmarks yet. | `nowPanelNoMarks` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| Chapter %d of %d | `nowPanelChapterOf` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| read by %s | `nowPanelReadByLine` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
| %d chapters | `nowPanelChapters` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |
|  ·  | `nowPanelStateSep` | `now_panel_copy.go` | Now playing panel: the operator's design shows the layout; wording follows the voice rules |

## M3a offline pocket (added by package D, 2026-11-03)

Strings of the Downloads page, the book page's Download control, the offline banner and the offline sync that
are not in the copy deck or on `DownloadsPhone` / `StateOffline`. Same voice rules as above.

| String | Constant | File | Why it is new |
| --- | --- | --- | --- |
| Resume download | see file | `downloads_copy.go` | Download manager button; the board has Pause but no word for the opposite |
| Paused · {done} of {total} | see file | `downloads_copy.go` | Row meta of a paused download; the board shows only the downloading and waiting rows |
| Waiting for a connection | see file | `downloads_copy.go` | Row meta of a download that stopped because the phone went offline |
| Preparing a version this phone can play | see file | `downloads_copy.go` | Row meta while the server makes a compatible copy (needsCompat file) |
| {done} of {total} | see file | `downloads_copy.go` | Progress line; the board's ETA part is added by the script when it has a rate |
| Keep Tendshelf open until downloads finish. | see file | `downloads_copy.go` | No Background Fetch, so downloads run only while the page is open; the page says so |
| This browser may clear downloads when the phone runs low on space. | see file | `downloads_copy.go` | Shown when persist() was not granted; plain words for storage eviction |
| Safari may clear downloads after a week without a visit. Add Tendshelf to your Home Screen to keep them. | see file | `downloads_copy.go` | iOS storage note (operator asked for it, decision 0012) |
| Downloads need a secure (https) address. Ask the person who runs Tendshelf to set one up. | see file | `downloads_copy.go` | Shown when the page is not a secure context |
| Install Tendshelf | see file | `downloads_copy.go` | Button shown when the browser offers installing |
| Tendshelf uses {used}; {free} free | see file | `downloads_copy.go` | Storage bar label for screen readers |
| There isn't room on this phone for {title}. Remove a download, then try again. | see file | `downloads_copy.go` | Failed row and toast, not enough space |
| {title} is no longer available to download. | see file | `downloads_copy.go` | Failed row and toast, book removed or access changed |
| {title} didn't download. Try again. | see file | `downloads_copy.go` | Failed row and toast, any other failure |
| {title} is ready offline. | see file | `downloads_copy.go` | Toast when a download finishes |
| Downloading · {pct}% | see file | `routes_downloads.go` | The book page's Download control while a download runs |
| Back online. Your place and bookmarks are saved. | see file | `offline_copy.go` | Toast after a sync that followed an offline spell |
| Bookmarked at {time}. It saves when you're back online. | see file | `copy.go (msgBookmarkQueued)` | Toast for a bookmark made offline |
| Downloads of this book are turned off. | see file | `offline_copy.go (downloadOffAnswer)` | Plain answer of /stream?download=1 when a person has no download permission |
| Your listening / Only you see these. | `listenTitle, statsPrivate` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| See your listening over time | `listenMore` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Show · Week · Month · Year · Show {period} | `listenRangeLabel, listenWeek, listenMonth, listenYear, listenShowFmt` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Listened · Days you listened · {n} of {m} · Finished · Usual speed | `listenNum*` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Listening time · {total} in {period}, about {avg} on the days you listened. · {total} in {period}. | `listenTimeTitle, listenSay*` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Time of day · You listen most in the morning. / afternoon. / evening. / at night. · Morning · Afternoon · Evening · Night · 6 am to noon · noon to 6 pm · 6 pm to 10 pm · 10 pm to 6 am · midnight · noon · 6 am · 6 pm · The light band is daytime, 6 am to 6 pm. | `listenDayTitle, listenMost*, listenMorning.. listenBand` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Days of the week · You listen most on {Weekday}s. | `listenWeekdaysTitle, listenMostDayFmt` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Day by day · You listened on {n} days in {year}. · No listening · Under 15 m · 15 to 45 m · 45 m to 1 h 30 m · 1 h 30 m or more | `listenDayByDay, listenDaysInFmt, listenKey*` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Most listened · Books · Authors · Narrators · Finished · Year by year · Listening since {Month YYYY}. | `listenMostTitle .. listenSinceFmt` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Nothing to show yet · Listen to a book and your time will show here, by day and by hour. · Go to your library · No listening in {period}. | `listenEmpty*, listenQuietFmt` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Chart captions, table heads and tooltip titles (Listening time by day, Listening by hour of the day, Day, Month, Hour, Year, Time, "Tue 6 Oct: 1 h 12 m", ...) | `listenCap*, listenHead*` | `listening_copy.go` | /me/listening (decision 0014); the deck has no words for it |
| Add books · Books go into {path}, in a folder for each author. · Choose files · Choose a folder · Choose what to add · Audio files, pictures and book details files. Up to 50 GB a book. Copy-protected files are refused. · This session | `uploadTitle .. uploadQueue` | `upload_copy.go` | /libraries/{id}/add (decision 0015); the deck has no words for it |
| Adding books needs JavaScript in this browser. You can also copy a book's folder into {path}, then press Scan. | `uploadNeedsJS` | `upload_copy.go` | /libraries/{id}/add, in noscript |
| Pause · Resume · Choose files again · Cancel upload · Try again · Open book · Checking the files · Adding it to your library · Putting it in {folder} · Added to {library} | `uploadPause .. uploadAdding` | `upload_copy.go`, `routes_upload_pages.go` | Queue rows (decision 0015) |
| {sent} of {total} · about {time} left · Paused. {sent} of {total} sent. · Stopped at {pct} %. Choose the same files again to carry on. · {n} s / {n} min / {n} h | `uploadMessages`, `uploadRow` | `upload_copy.go`, `routes_upload_pages.go` | Queue rows; the time units are new |
| Not added: {title} · Not added yet: {title} · {file} is copy-protected, so Tendshelf will not open it. Nothing was kept. · Not added. There is no audio file in it. · {n} other files were left out. | `uploadNotAdded*, refused-*` | `upload_copy.go` | Queue rows (decision 0015, board UploadBooks) |
| The server ran out of space. Free some space, then press Try again. · Tendshelf could not finish adding this book. Check that the library folder is available, then press Try again. | `uploadNoSpace, uploadPlaceFailed` | `upload_copy.go` | Failed row; the second is new (no plan sentence for other placement failures) |
| The connection dropped. Trying again in {n} s. · Tendshelf could not reach the server. Reload this page, then choose the same files again. · These are not the same files. Choose the files you picked before. | `uploadMessages` (retry, failed, mismatch) | `upload_copy.go` | New: the uploader's reconnect, failure and wrong-files lines |
| Adding books from this device · Where new books go · Nowhere (off) · Tendshelf makes a folder for each author in the folder you pick. It never changes or deletes anything already there. · Save · Only the owner can change this. · Add books · Tendshelf cannot write in this folder. Check its permissions. | `uploadAdmin*, uploadsCannotWrite` | `upload_copy.go`, `routes_upload.go` | Libraries & scanning (decision 0015) |
| Drop audio files or a folder here · This folder holds several books · {n} books · Archives inside archives are left out. (end of the hint) | `uploadDrop`, `uploadSeveral`, `uploadHint`, message `books` | `upload_copy.go` | /libraries/{id}/add; the drop line is the board's, the other three are new (plan U3); `{n} books` replaces "This session" once the queue holds more than one |
| Remove this book · Remove this book? · This deletes the {n} files Tendshelf added for this book. Files changed since then stay. · Keep it | `bookRemove*` | `upload_copy.go` | Book Details panel and /books/{id}/remove; "Remove this book?" and "Keep it" are new |
| Read {title} | `offlineReadAria` | `offline_copy.go` | Accessible name of a row's Read button on `/offline` (G6b); the visible word is the existing "Read"; no other new words: the Download control, its states and every Downloads sentence are reused for text |
| This speed is for this book. Other books keep your usual speed. · Make it my usual speed · This is now your usual speed. · Jumped too far? · Go back · Rewound {s} s after your break | `msgSpeedForBook, msgSpeedMakeUsual, msgSpeedUsualSet, msgJumpedFar, msgJumpedBack, msgRewound` | `copy.go` | L1 speed sheet and L2 undo toast in the full player; the rewound hint now names the seconds (2, 5, 10 or 20) |
| App keys · A key lets one reading app open your books without your password. It opens one thing only, and you can turn it off here at any time. · No keys yet. · Name · So you know which app it is, for example Kobo or Boox. · For · A book app (OPDS) · KOReader sync · Make a key · Turn off · Turn off {name} (aria) · Book app · KOReader (device kinds) | `akey*` | `appkeys_copy.go`, `auth.KindLabel` | Your devices, App keys section (S5) |
| Your new key · Copy it now. This is the only time it is shown; if you lose it, turn it off and make another. · Server address · User name · Key · Back to your devices · In your reading app, add a catalog (OPDS) with this address, your user name, and the key as the password. · In KOReader, open Progress sync, choose Custom sync server and enter this address, then choose Login (not Register) with your user name and the key as the password. · You have as many apps and keys as one person may have. Turn one off first. · Choose what the key is for. | `akeyShown*`, `akeyAddress`, `akeyUser`, `akeyKey`, `akeyBack`, `akeyOPDSNote`, `akeyKONote`, `akeyLimit`, `akeyBadKind` | `appkeys_copy.go` | The page that shows a new key once (S5) |
| All books · Newest · Every book you can take as an ebook, by title. · The books added most recently. · Search · Search your books · Sign in with your user name and an app key. · Too many wrong keys. Wait a little and try again. · Too many requests from this app. Wait a little and try again. | | `opds_views.go`, `routes_opds.go`, `appauth.go` | OPDS feed titles and plain answers seen by reading apps, not in the web interface (S5) |
| Edit chapters · Change where each chapter starts and what it is called. Each chapter runs until the next one starts. · Back to chapters · Starts at · Name · Listen · Delete · Add below · Save chapters · Chapters saved. · Chapter {n} starts at / name · Listen to chapter {n} · Delete chapter {n} · Add a chapter after chapter {n} | `chedTitle .. chedAddFmt` | `chapters_copy.go` | /books/{id}/chapters/edit (L3a); no board, the deck has no words for it |
| These chapters stay in Tendshelf. They are not written into the audio files of this book. | `chedStays` | `chapters_copy.go` | Edit chapters, books with several files or FLAC |
| Move chapters · Move by (seconds) · Use a minus sign to move earlier, for example -12.5. · From chapter · Move | `chedMove*` | `chapters_copy.go` | Edit chapters disclosure |
| Go back to the chapters in the files · Go back to the chapters in the files? · Your edited chapters will be replaced by the chapters Tendshelf finds in the audio files. · Replace my chapters · Keep my chapters | `chedReset*` | `chapters_copy.go` | /books/{id}/chapters/reset confirm page |
| Edit chapters | `chedEditLink` | `chapters_copy.go` | Link under the chapter list on the book page, only for people who may edit |
| These chapters changed while you were editing. Reload the page to see them. · Some files are still being read. Try again in a minute. | `chedErrStale, chedErrBusy` | `chapters_copy.go` | Edit chapters, inline (words from the plan) |
| A book can have up to 1,000 chapters. · A book needs at least one chapter. · Write the time like 1:02:03 or 12:30, with seconds after a dot if you need them. · The first chapter has to start at 0:00. · A chapter has to start after the one before it. · A chapter has to start before the book ends. · Give this chapter a name. · Use 500 letters or fewer for a chapter name. · There is no room for another chapter here. · Write the move in seconds, like 12 or -12.5. · Choose a chapter to move from, the second one or later. · Moving by that much would put a chapter out of order or past the end. Nothing was moved. | `chedErr*` | `chapters_copy.go` | Edit chapters, inline errors |
| Look up chapters · Audible ASIN · Ten letters and numbers, from the book's Audible page. · Look up · That does not look like an ASIN. · Asking Audnexus for the chapters. · The chapter lookup did not work. Try again later. | `chlkSummary .. chlkFailed` | `chapters_copy.go` | Edit chapters, lookup disclosure and waiting or failed box (L3b); no board |
| Audnexus lists {n} chapters, {length} long. This book is {length} long. · These chapters may not line up with your recording. Check the times before you save, and use Move chapters if they are all early or late. · Use titles and times · Use titles only · Titles only needs the same number of chapters as the list below has. | `chlkFound .. chlkTitlesNote` | `chapters_copy.go` | Edit chapters, finished lookup box |
| The chapters are not ready yet. · The number of chapters is not the same, so only titles and times can be used. · Chapters filled in. Check them, then press Save chapters. · Titles filled in. Check them, then press Save chapters. · One chapter starts after the end of this book and was left out. · {n} chapters start after the end of this book and were left out. | `chlkNotReady .. chlkDropMany` | `chapters_copy.go` | Edit chapters, after a lookup is used |
| Audnexus has no chapters for this book. | `online.Error` `no_chapters` | `internal/metadata/online/client.go` | Job failure text of a chapter lookup |
| The write job refusal: The chapter list runs past the end of this book. Fix it on the Chapters page first. | `writeText[chapter_fit]` | `mediajobs/writes.go` | Write sheet (words from the plan) |
| Duplicate books · These look like the same book more than once. Merging keeps one copy and moves everyone's place, bookmarks, shelves and shares onto it. No file is changed, moved or deleted. · No duplicates found. Tendshelf looks for the same audio files, and for the same title, author and narrator with nearly the same length. | `dupTitle, dupLead, dupEmpty` | `duplicates_copy.go` | /admin/duplicates (S4); the deck has no words for it |
| The same audio files · Every file matches, byte for byte. · The same title, author and narrator · The files differ. It may be another recording or edition, so check the length first. · Suggested to keep · Merge into “{title}” · Not the same · Copies you merged | `dupAudioHead, dupAudioNote, dupDetailsHead, dupDetailsNote, dupSuggested, dupMergeInto, dupNotSame, dupHiddenLink` | `duplicates_copy.go` | /admin/duplicates group heads and row buttons (S4) |
| 1 file / {n} files · 1 person has a place in it / {n} people have a place in it · 1 person has bookmarks / {n} people have bookmarks | `dupFiles*, dupListening*, dupBookmarks*` | `duplicates_copy.go` | /admin/duplicates row lines (S4) |
| Merged. The folder of the other copy stays on disk, and Tendshelf no longer lists it. · Got it. Tendshelf won't suggest that pair again. · That copy will show up again after this scan. | `dupMergedBanner, dupNotSameBanner, dupRestoredBanner` | `duplicates_copy.go` | Banners after a merge, "Not the same" and a restore (S4) |
| Merge these two copies? · Pick the copy to keep. The other copy leaves the library, and what people did with it moves to the one you keep. · Keep · Merge in and hide · What moves to the copy you keep · Merge · Cancel · Keep the other one instead | `dupConfirmTitle .. dupSwap` | `duplicates_copy.go` | /admin/duplicates/merge confirm page (S4) |
| No file is changed, moved or deleted. The folder of the other copy stays where it is, and Tendshelf stops listing it. · These copies do not hold the same files. Places move as they are, so one may land a little off. · You can show that copy again later. What has moved stays with the copy you keep, and that cannot be undone. | `dupFilesStay, dupNotSameAudio, dupOneWay` | `duplicates_copy.go` | Notes on the confirm page (S4) |
| Places in the book: {n} (the newer place wins where a person has both) · Bookmarks: {n} · Highlights: {n} · Shelf entries: {n} · Ebook files: {n} · People who could open the merged-in copy will also be able to open the copy you keep: {n} · Share links that will open the copy you keep: {n} · Nobody has a place, bookmark or share on the merged-in copy. | `dupMove*` | `duplicates_copy.go` | "What moves" list on the confirm page (S4) |
| These two copies are in different libraries, so they can't be merged. · This book is being written to. Try again when that has finished. · Pick two different books. | `dupErrLibrary, dupErrBusy, dupErrSame` | `duplicates_copy.go` | Refusals of a merge (S4) |
| Copies you merged · Tendshelf leaves these folders out of its scans. Show a copy again and the next scan adds it as a new book. Places and bookmarks that moved stay with the copy you kept. · You haven't merged any copies. · Merged into “{title}” · Show that copy again · Show the copy in {path} again · Back to duplicate books | `dupMerged*, dupShowAgain*, dupBack` | `duplicates_copy.go` | /admin/duplicates/merged (S4) |
| Library health · Books that need a look, with a way to fix each one. Tendshelf only reads here; no file is changed. · Nothing needs a look. Every book is in good shape. · Show all {n} · Back to library health · Previous · Next · Page {n} of {m} | `healthTitle, healthLead, healthAllClear, healthShowAll, healthBack, healthPrev, healthNext, healthPageText` | `health_copy.go` | Library health page (S3) |
| Open book · Edit chapters · Look it up · Review this folder · Tendshelf could not read this file. Check it, then scan again. | `healthOpenBook, healthEditChapter, healthLookItUp, healthReviewDir, healthUnreadable` | `health_copy.go` | Library health fix links |
| Missing from disk · Not opened · Files that can't be read · No chapters · No cover · No match found online · Chapters past the end, each with a one-line note | `health*Head, health*Note` | `health_copy.go` | Library health group headings |
| Make a KOReader key in Tendshelf, then choose Login. (the answer to KOReader's Register; JSON, shown by KOReader) · Unauthorized · Invalid fields · Not found (KOReader protocol messages, kept as KOReader expects them) | `kosyncNoCreate`, `kosyncUnauthorized`, `kosyncInvalid` | `routes_kosync.go` | `/kosync` answers (S6); the only new words a person can read, in KOReader's own window; the key note above changed to say Custom sync server and Login |
| Play this clip · Clips · {n} · {Kind} · {n} · Kind · Ends at · Leave empty for a single moment. Counted from the start of the book, like 1:02:30. This clip starts at {time}. · Clip · {time} | `clipPlay, clipChip, clipChipKind, clipKindName, clipEndLabel, clipEndHint, clipLength, clipExportClip` | `copy.go` | Bookmarks tab rows, chips, note sheet and exports (L5); kinds Bookmark, Favourite, Quote, Note, Revisit |
| The end must be after the start. · Write the end as minutes and seconds, like 12:30, or with hours, like 1:02:30. · Nothing is saved as {kind} yet. · No clips yet. Give a bookmark an end to make one. | `clipErrEnd, clipErrEndClock, clipEmptyKind, clipEmptyClips` | `copy.go` | Note sheet errors and empty filters (L5) |
| Listening history · Each time you listened, newest first. Only you see this. · Where was I? · Nothing here yet · When you listen to a book, each stretch you listen to shows up here. · Only {title}. · Show every book · Go back here · Show older · from {a} to {b} · Chapter {n} | `timeline*` | `timeline_copy.go` | Listening timeline (L6) |
| All my highlights · Everything you highlighted in your books, newest first. Only you see this. · Search your highlights · Colour · Any colour · Search · Yellow, Green, Blue, Pink highlight · Open in the book · Older · Newer · {n} highlights · No highlights yet · Select a sentence while you read and pick a colour. It shows up here. · Nothing matches that. · Download as Markdown · Download as JSON | `highlights*` | `highlights_copy.go` | All my highlights (R1) |
| Change mode or server… · Zoom in · Zoom out · Actual size · Keep playing when I close the window · About Tendshelf (the board's More menu; the first four and the last are the board's words) | `desktop_bar.templ` | Desktop app, More menu (X4); Keep playing shows only when a tray exists |
| Skip back · Skip forward · Pause · Show Tendshelf · Quit Tendshelf · (paused) after a book's name in the tray tip | `desktop/tray.go` | Desktop app tray menu (X4) |
| Finished. Tendshelf has stopped. · The sleep timer stopped playback. (the book's title is the notification's title) | `desktop/notify.go` | Desktop notifications at book end and sleep stop (X4) |
| About Tendshelf · Tendshelf {version} · Your audiobooks and ebooks, in a window of their own. | `desktop/main.go` | Desktop About dialog (X4) |
| Drop to add to your library · Audiobooks and ebooks go into your uploads folder. Nothing else on your computer changes. (the board's words) · Tendshelf takes books as files here. Drop the files, or open Add books to add a folder. · Uploads are off, so there is nowhere to put a book. You can turn them on under Libraries. · Only the owner or an admin can add books. | `internal/web/components/desktop_bar.templ` | Desktop app drop zone and its toasts (X5) |
| Connect this phone · This phone will be able to play, download and keep your place. · Connect · This address is not private. Others on this network could see what you do here. · Open Tendshelf on your phone to connect it. · Go back to the Tendshelf app. | `connectTitle, connectBody, connectButton, connectInsecure, connectNotInApp, connectDone` | `app_connect_copy.go` | The Connect page of the Android app (A1) |
| This phone | `msgThisPhone` | `app_connect_copy.go` | The row of the phone's own app in Your devices (A1) |
| This sign-in has expired. Start again from the app. · This sign-in belongs to someone else. Start again from the app. | `msgCodeExpired` and the 409 text | `routes_api.go` | API answers of the code swap (A1) |
| Speed must be from 0.5 to 3, in steps of 0.05. · Skip must be 10, 15, 30 or 60 seconds. · This must be true or false. | `apiSettings*` | `routes_api_settings.go` | API field messages of the app settings (A1) |
| This address is not private · Others on this network could see what you do here. (A1's line) · Anyone along the way could see your password. (non-private addresses) · Continue · Cancel · Connect to your Tendshelf · Type the address you use for your Tendshelf server. · Address · Like tendshelf.home:8095 · Type only the address, like tendshelf.home:8095. · You were signed out. Sign in again to carry on. · Can't reach your server · Check your connection and that the server is running. · Try again · Couldn't save that file. · This server's certificate isn't trusted, so Tendshelf won't open it. (the desktop shell's unreachable, certificate, not-Tendshelf and needs-updating lines are reused word for word) | `android/app/src/main/res/values/strings.xml` | The Android app's start page, plain-http warning and offline page (A2) |
| Sort · Sort: {order} · Sort by · Remove filter · Remove filter: {kind} {value} · Clear all · Active filters · {n} of {total} books · Loading · Show the {kind} list | `libSortLabel`, `libRemoveFilter`, `libClearAll` | `browse_copy.go`, `pages/library.templ` | Library filters rework: the Sort chip, the active filter row (the remove links' accessible names), the one count in the header, the no-JavaScript link under a closed kind |
| Which books to look up · Look up · Books missing details ({n}) · Books not looked up yet ({n}) · All books ({n}) | `lookupScopeLabel`, `lookupStart`, `lookupScope*` | `internal/web/lookups_copy.go` | Libraries page, Online details (the bulk lookup choice, with live counts) |
| Watching · Adding books · Online details · Folders · Scan this library (subsection headings and the per-library scan button) | literals | `internal/web/pages/admin_libraries.templ` | Libraries page, one section per library |
| Saved (the quiet mark beside a setting that saved on change) | `settingSavedWord` | `internal/web/setting_save.go` | Libraries page settings |
| {n} things need a look / 1 thing needs a look · Nothing needs a look. (the short summary linking to Library health) · What needs a look (the summary's label) | `problemsTitle`, `healthNoneSummary`, `healthSummaryLabel` | `internal/web/copy.go`, `health_copy.go` | Libraries page and Library health top |
| Copy-protected, so Tendshelf can't open it. Nothing else is affected. · No audio Tendshelf can play. (the one-line reason on a refused book) | `healthRefusedDRM`, `healthRefusedNoAudio` | `internal/web/health_copy.go` | Library health, Not opened group |
| The backup drive may be unplugged: the backup folder is there but the earlier backup is not. Plug the drive in, or choose the folder again to start a new backup there. The database copy in the data folder was made. · 1 file could not be read, so it was left out. The rest was backed up. · {n} files could not be read, so they were left out. The rest was backed up. | `TextDriveMissing`, `Result.Note` | `internal/backups/backups.go`, `whole.go` | Backup job error and result |
| Downloading… | see file | `routes_downloads.go`, `StartPages.kt`, `strings.xml` (`downloads_working`) | The Download control, the app's Downloads list and the download notification while a book's size is not known yet (otherwise "Downloading · {pct}%", `downloads_percent`) |
| Add a phone · Show a code here, then point your phone's camera at it. You will type a number from the phone, so no password is typed on the phone. · Show a code · Scan this with your phone's camera. It works once, for 3 minutes. · Or open this link on the phone · Waiting for your phone… · {name} wants to sign in as you, from {ip}. Type the number on that phone. Only do this if the phone is in your hand. · Number on the phone · Add phone · Not my phone · That number did not match, so this code has ended. If the phone is yours, show a new code. · Now tap Connect on your phone. · This code has ended. Show a new one. · Show a new code · Check again · This address works only on this computer. Open Tendshelf at an address your phone can reach, then come back here. · Adding a phone needs a sign-in made in Tendshelf itself, not by a proxy in front of it. · Back to your devices · Add a phone… (desktop More menu) · Too many codes. Please wait a little, then show a new one. | `addPhone*` | `internal/web/pair_copy.go` | Add a phone, signed-in device (decision 0028) |
| Add this phone to {server}? · Only go on if you are doing this yourself, with your other device in front of you. · Add this phone · Address: {host} · Type this number on your other device. · Connect as {name} · Almost there · Your other device agreed. Tap the button to finish. · This code has ended · This code has ended. Show a new one on your other device. · Sign in with a password instead · Open in the app · Open this link on the phone you want to add. · The Tendshelf app isn't on this phone yet. | `pair*` | `internal/web/pair_copy.go` | The phone's pages and the hand-over page (decision 0028) |
| Open in the app · The Tendshelf app isn't on this phone yet. You can join here in your browser. · Continue on your phone · This phone is signed in. Sign out first to use this invitation. · QR code for this invitation · QR code for adding a phone (screen-reader name) | `invite*`, `addPhoneQRLabel` | `internal/web/pair_copy.go` | Invite page and link panel (decision 0028) |
