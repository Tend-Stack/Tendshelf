package web

import "strings"

// Activity (S9). Every string here is NEW: none is in the copy deck yet. They follow the voice rules in
// BRAND.md and are listed in docs/copy-additions.md.
const (
	activityTitle      = "Activity"
	activityLead       = "What people did on this server, newest first. Only owners and admins see this page."
	activityEmpty      = "Nothing has happened yet."
	activityEmptyMatch = "Nothing matches. Try another person or kind."
	activityPersonLbl  = "Person"
	activityKindLbl    = "Kind"
	activityAllPeople  = "Everyone"
	activityAllKinds   = "All kinds"
	activityFilterBtn  = "Show"
	activityClear      = "Clear filters"
	activityNewer      = "Newer"
	activityOlder      = "Older"
	activityPageText   = "Page %d"
	activityListLabel  = "Recent activity"
	activitySomeone    = "Someone"
	activityGonePerson = "Someone who has left"
	activityOpenPrefix = "Open "

	activityKindSignIns = "Sign-ins and devices"
	activityKindPeople  = "People and invitations"
	activityKindSharing = "Sharing"
	activityKindBooks   = "Books"
	activityKindServer  = "Libraries and server"
)

// activityKind is one choice of the Kind filter and the action name prefixes it covers.
type activityKind struct {
	Key, Label string
	Prefixes   []string
}

// activityKinds are the Kind choices, in the order the select shows them. An action that no kind lists still shows
// under "All kinds", with the fallback sentence.
var activityKinds = []activityKind{
	{"signins", activityKindSignIns, []string{"auth.", "setup.", "key."}},
	{"people", activityKindPeople, []string{"user.", "invite."}},
	{"sharing", activityKindSharing, []string{"grant.", "link.", "shelf.", "feed."}},
	{"books", activityKindBooks, []string{"book.", "ebook.", "chapters.", "upload."}},
	{"server", activityKindServer, []string{"library.", "settings.", "smtp.", "backups.", "podcast."}},
}

// activityWho is what a sentence needs to name people. Actor is the person who did it ("Someone" when none is
// stored), Target the person the row is about and Other a person named in the detail (a grant's receiver). An
// empty name reads as "someone".
type activityWho struct{ Actor, Target, Other string }

func (w activityWho) target() string { return nameOr(w.Target, "someone") }
func (w activityWho) other() string  { return nameOr(w.Other, "someone") }

func nameOr(n, fallback string) string {
	if n == "" {
		return fallback
	}
	return n
}

// onOff says "on" or "off" for a boolean detail value.
func onOff(d map[string]any, key string) string {
	if v, _ := d[key].(bool); v {
		return "on"
	}
	return "off"
}

func detailStr(d map[string]any, key string) string {
	s, _ := d[key].(string)
	return s
}

// activitySentence says in plain words what one audit row records. It uses only what the row stores and never
// prints a token, an address or a file path. An action it does not know gets "Something changed: <action>".
func activitySentence(action string, w activityWho, d map[string]any) string {
	a := w.Actor
	switch action {
	case "auth.login":
		switch detailStr(d, "via") {
		case "sign_in_link":
			return a + " signed in with a sign-in link"
		case "pair":
			return a + " signed in by pairing a device"
		case "invite":
			return a + " signed in after accepting an invitation"
		}
		return a + " signed in"
	case "auth.login_failed":
		if w.Actor == activitySomeone {
			return "A sign-in attempt failed"
		}
		return "A sign-in attempt for " + a + " failed"
	case "auth.logout":
		return a + " signed out"
	case "auth.session_signed_out":
		return a + " signed out of one session"
	case "auth.sessions_signed_out":
		return a + " signed out of their other devices"
	case "auth.device_issued", "key.create":
		return a + " added a device"
	case "auth.device_rebound":
		return a + " signed a device back in"
	case "auth.device_renamed":
		return a + " renamed a device"
	case "auth.device_revoked":
		if w.Target != "" && w.Target != w.Actor {
			return a + " turned off a device of " + w.target()
		}
		return a + " turned off a device"
	case "auth.device_code_issued", "auth.pair_started":
		return a + " started adding a device"
	case "auth.device_code_failed", "auth.pair_failed":
		return "A code for adding a device was refused"
	case "auth.pair_approved":
		return a + " approved a new device"
	case "auth.pair_refused":
		return a + " refused a new device"
	case "auth.pair_claimed":
		return "A new device finished pairing"
	case "auth.password_changed":
		return a + " changed their password"
	case "auth.password_change_failed":
		return a + " gave the wrong current password when changing it"
	case "auth.password_reset":
		return a + "'s password was reset"
	case "auth.signin_link_failed", "link.failed":
		return "A link was refused"
	case "setup.owner_created":
		return a + " set up this server"
	case "setup.code_failed":
		return "A wrong setup code was tried"
	case "setup.server_named":
		return a + " named the server"
	case "user.credentials":
		return a + " chose a name and password"
	case "user.role":
		if to := detailStr(d, "to"); to != "" {
			return a + " made " + w.target() + " " + strings.ToLower(roleLabel(to))
		}
		return a + " changed the role of " + w.target()
	case "user.signout":
		return a + " signed " + w.target() + " out everywhere"
	case "user.device_signout":
		return a + " turned off a device of " + w.target()
	case "user.stop_sharing":
		return a + " stopped sharing with " + w.target()
	case "user.books_remove_all":
		return a + " removed every book " + w.target() + " added"
	case "user.signin_link":
		return a + " made a sign-in link for " + w.target()
	case "user.signin_link_revoked":
		return "A sign-in link for " + w.target() + " stopped working"
	case "invite.create":
		if role := detailStr(d, "role"); role != "" {
			return a + " made an invitation for a " + strings.ToLower(roleLabel(role))
		}
		return a + " made an invitation"
	case "invite.accept":
		return a + " joined with an invitation"
	case "invite.rotate":
		return a + " made a new link for an invitation"
	case "invite.revoke":
		return a + " turned off an invitation"
	case "invite.mail":
		return a + " emailed an invitation"
	case "invite.failed":
		return "An invitation link was refused"
	case "grant.put":
		return a + " shared with " + w.other()
	case "grant.revoke":
		return a + " stopped sharing with " + w.other()
	case "link.create":
		return a + " made a share link"
	case "link.revoke":
		return a + " turned off a share link"
	case "shelf.share":
		if v, _ := d["shared"].(bool); v {
			return a + " shared a shelf"
		}
		return a + " stopped sharing a shelf"
	case "feed.create":
		return a + " turned on a podcast feed"
	case "feed.revoke":
		return a + " turned off a podcast feed"
	case "feed.failed":
		return "A podcast feed address was refused"
	case "smtp.update":
		return a + " changed the email settings"
	case "smtp.off":
		return a + " turned email off"
	case "smtp.test":
		return a + " sent a test email"
	case "podcast.settings":
		return a + " changed the podcast settings"
	case "podcast.follow":
		return a + " followed a podcast"
	case "podcast.unfollow":
		return a + " stopped following a podcast"
	case "podcast.import":
		return a + " imported podcasts from a file"
	case "settings.online":
		return a + " turned online lookups " + onOff(d, "on")
	case "backups.folder_set":
		return a + " changed the backup folder"
	case "library.folder_added":
		return a + " added a folder"
	case "library.folder_emptied":
		return a + " said a folder was emptied on purpose"
	case "library.watch_set":
		return a + " turned folder watching " + onOff(d, "watch")
	case "library.uploads":
		return a + " changed where uploads go"
	case "library.auto_lookup":
		return a + " turned automatic lookups " + onOff(d, "on")
	case "book.write.start":
		return a + " started writing details into a book's files"
	case "book.write":
		return "Details were written into a book's files"
	case "book.write.confirm":
		return a + " kept the details written into a book's files"
	case "book.write.undo":
		return a + " undid the details written into a book's files"
	case "book.write.recover":
		return "Tendshelf finished an interrupted write to a book's files"
	case "book.cover.choose":
		return a + " chose a cover"
	case "book.cover.upload":
		return a + " uploaded a cover"
	case "book.text.add":
		return a + " added a text edition"
	case "book.text.clear":
		return a + " removed a text edition"
	case "chapters.edit":
		return a + " edited chapters"
	case "chapters.reset":
		return a + " put the chapters back as they were"
	case "book.details.apply":
		return a + " applied details from online"
	case "book.details.dismiss":
		return a + " kept a book's details as they were"
	case "book.details.reset":
		return a + " put a book's details back"
	case "book.details.auto":
		return "Details for a book were filled in from online"
	case "book.merge":
		return a + " merged two copies of a book"
	case "book.merge_undo":
		return a + " brought a merged copy back"
	case "ebook.merge":
		return a + " merged a text edition into a book"
	case "ebook.pair":
		return a + " paired a text edition with a book"
	case "ebook.unpair":
		return a + " split a text edition from a book"
	case "ebook.not_same":
		return a + " said a text edition is not the same book"
	case "ebook.keep":
		return a + " kept a text edition as its own book"
	case "ebook.default":
		return a + " chose the text edition to read"
	case "upload.place":
		return a + " added a book by upload"
	case "upload.refuse":
		return "An upload from " + a + " was refused"
	case "upload.remove":
		return a + " removed a book they uploaded"
	}
	return "Something changed: " + action
}
