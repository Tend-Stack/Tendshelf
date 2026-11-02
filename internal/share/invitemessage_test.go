package share

import (
	"net/url"
	"strings"
	"testing"
)

func parseMailto(t *testing.T, raw string) (to, subject, body string) {
	t.Helper()
	if !strings.HasPrefix(raw, "mailto:") {
		t.Fatalf("not a mailto: %q", raw)
	}
	rest := strings.TrimPrefix(raw, "mailto:")
	addr, query, ok := strings.Cut(rest, "?")
	if !ok {
		t.Fatalf("no query: %q", raw)
	}
	to, err := url.PathUnescape(addr)
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 2 {
		t.Fatalf("query has %d fields, want 2: %v", len(q), q)
	}
	return to, q.Get("subject"), q.Get("body")
}

func TestMailtoEncoding(t *testing.T) {
	link := "https://tend.example/invite/AbC123-xyz_"
	for _, c := range []struct {
		name string
		m    InviteMessage
		to   string
	}{
		{"plain", InviteMessage{Inviter: "Ana", Invitee: "Rosa", ServerName: "Home", Note: "See you", URL: link, Days: 7}, "rosa@example.org"},
		{"specials in every field", InviteMessage{Inviter: "A&B?", Invitee: "R#=osa", ServerName: "Café & Co", Note: "a&b=c?d#e %41 +plus\nsecond line\r\nthird", URL: link}, "rosa+tag@example.org"},
		{"accents and emoji", InviteMessage{Inviter: "José", Invitee: "Zoë", ServerName: "Niño ✓", Note: "¡Hola! 🎧", URL: link, Days: 1}, "zoe@example.org"},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := c.m.Mailto(c.to)
			if strings.ContainsAny(raw, " \n\r#") || strings.Count(raw, "?") != 1 || strings.Count(raw, "&") != 1 {
				t.Fatalf("not safely encoded: %q", raw)
			}
			to, subject, body := parseMailto(t, raw)
			if to != c.to || subject != c.m.Subject() {
				t.Errorf("to %q subject %q", to, subject)
			}
			want := strings.ReplaceAll(strings.ReplaceAll(c.m.Body(), "\r\n", "\n"), "\n", "\r\n")
			if body != want {
				t.Errorf("body round trip:\n got %q\nwant %q", body, want)
			}
			if !strings.Contains(body, link) {
				t.Error("link missing")
			}
		})
	}
}

func TestMailtoTruncatesTheNoteNeverTheLink(t *testing.T) {
	link := "https://tend.example/invite/" + strings.Repeat("Z", 43)
	m := InviteMessage{Inviter: "Ana", Invitee: "Rosa", ServerName: "Home", URL: link, Days: 7,
		Note: strings.Repeat("Ünï & ? # ✓ ", 80)} // 500-ish characters of awkward text
	raw := m.Mailto("rosa@example.org")
	if len(raw) > mailtoLimit {
		t.Fatalf("length %d over the limit", len(raw))
	}
	_, _, body := parseMailto(t, raw)
	for _, want := range []string{link + "\r\n", "The link works once, for 7 more days."} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	if !strings.Contains(body, "Their note: ") || !strings.Contains(body, "…") {
		t.Errorf("note not shortened with an ellipsis: %q", body)
	}
	short := InviteMessage{Inviter: "Ana", Invitee: "Rosa", ServerName: "Home", URL: link, Note: "ok"}
	if _, _, b := parseMailto(t, short.Mailto("rosa@example.org")); !strings.Contains(b, "Their note: ok\r\n") {
		t.Errorf("a short note changed: %q", b)
	}
	// With no note at all there is no note line.
	none := InviteMessage{Inviter: "Ana", Invitee: "Rosa", ServerName: "Home", URL: link}
	if _, _, b := parseMailto(t, none.Mailto("rosa@example.org")); strings.Contains(b, "Their note") {
		t.Errorf("empty note printed: %q", b)
	}
}

func TestInviteMessageWords(t *testing.T) {
	m := InviteMessage{Inviter: "Ana", Invitee: "Rosa", ServerName: "Home", URL: "https://x/invite/t", Note: "Hi!", Days: 3}
	if m.Subject() != "Ana invited you to Home" {
		t.Error(m.Subject())
	}
	want := "Hi Rosa,\n\nAna invited you to listen to audiobooks on Home.\n\nTheir note: Hi!\n\nOpen this link to make your account:\nhttps://x/invite/t\n\n" +
		"The link works once, for 3 more days.\n\nIf you weren't expecting this, you can ignore this email.\n"
	if m.Body() != want {
		t.Errorf("body:\n%q\nwant\n%q", m.Body(), want)
	}
}
