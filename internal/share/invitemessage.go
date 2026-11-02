package share

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// InviteMessage is the words of an invitation email. The same text is sent by the relay, opened in
// the person's own email app (a mailto: link) and offered as "Copy message".
type InviteMessage struct {
	Inviter, Invitee, ServerName, Note, URL string
	// Days is how many more days the link works (at least 1); 0 means until the inviter stops it.
	Days int
}

// Subject is "Rosa invited you to Home".
func (m InviteMessage) Subject() string {
	return fmt.Sprintf("%s invited you to %s", m.Inviter, m.ServerName)
}

// Body is plain text with LF line ends. note replaces the message's note, so a caller can shorten it.
func (m InviteMessage) body(note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\n", m.Invitee)
	fmt.Fprintf(&b, "%s invited you to listen to audiobooks on %s.\n", m.Inviter, m.ServerName)
	if note != "" {
		fmt.Fprintf(&b, "\nTheir note: %s\n", note)
	}
	b.WriteString("\nOpen this link to make your account:\n")
	b.WriteString(m.URL + "\n\n")
	switch {
	case m.Days == 1:
		b.WriteString("The link works once, for 1 more day.\n")
	case m.Days > 1:
		fmt.Fprintf(&b, "The link works once, for %d more days.\n", m.Days)
	default:
		fmt.Fprintf(&b, "The link works once, until %s stops it.\n", m.Inviter)
	}
	b.WriteString("\nIf you weren't expecting this, you can ignore this email.\n")
	return b.String()
}

// Body is the whole message text.
func (m InviteMessage) Body() string { return m.body(m.Note) }

// HeaderParts are the names that end up in a subject or greeting; a line break in any is refused.
func (m InviteMessage) HeaderParts() []string { return []string{m.Inviter, m.Invitee, m.ServerName} }

// TestMessage is the test email's subject and body.
func TestMessage(serverName string) (subject, body string) {
	return "Test email from " + serverName, "Email from " + serverName + " works. Invites will be sent from this address.\n"
}

// mailtoLimit is how long the whole mailto: address may be. Email apps and browsers cut long
// addresses somewhere between 2,000 and 8,000 characters; staying under 1,900 works nearly everywhere.
const mailtoLimit = 1900

// percentEncode encodes every byte except the unreserved set, so a name or note with &, ?, #, = or
// accents can never end a field early. Line breaks become %0D%0A.
func percentEncode(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// Mailto builds the address that opens the person's email app with the message ready. When it would
// be too long, the note is shortened (with an ellipsis) or dropped; the link is never cut.
func (m InviteMessage) Mailto(to string) string {
	build := func(note string) string {
		return "mailto:" + strings.ReplaceAll(url.PathEscape(to), "%40", "@") + "?subject=" + percentEncode(m.Subject()) + "&body=" + percentEncode(m.body(note))
	}
	note := m.Note
	out := build(note)
	for len(out) > mailtoLimit && note != "" {
		keep := utf8.RuneCountInString(note) - max(10, utf8.RuneCountInString(note)/4)
		if keep <= 0 {
			note = ""
		} else {
			note = strings.TrimSpace(string([]rune(note)[:keep])) + "…"
		}
		out = build(note)
	}
	return out
}
