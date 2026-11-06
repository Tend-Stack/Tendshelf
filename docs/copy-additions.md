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
| Your new key · Copy it now. This is the only time it is shown; if you lose it, turn it off and make another. · Server address · User name · Key · Back to your devices · In your reading app, add a catalog (OPDS) with this address, your user name, and the key as the password. · In KOReader, open the progress sync settings, choose a custom sync server with this address, and log in with your user name and the key as the password. · You have as many apps and keys as one person may have. Turn one off first. · Choose what the key is for. | `akeyShown*`, `akeyAddress`, `akeyUser`, `akeyKey`, `akeyBack`, `akeyOPDSNote`, `akeyKONote`, `akeyLimit`, `akeyBadKind` | `appkeys_copy.go` | The page that shows a new key once (S5) |
| All books · Newest · Every book you can take as an ebook, by title. · The books added most recently. · Search · Search your books · Sign in with your user name and an app key. · Too many wrong keys. Wait a little and try again. · Too many requests from this app. Wait a little and try again. | | `opds_views.go`, `routes_opds.go`, `appauth.go` | OPDS feed titles and plain answers seen by reading apps, not in the web interface (S5) |
