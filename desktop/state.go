package main

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The page says what is playing with "tendshelf:state <json>" (decision 0023, point 10). The words are
// data from a page, possibly from a server the person does not run, so they are limited, cleaned
// and escaped before they reach the tray or a notification, which may read markup.
const (
	msgState = "tendshelf:state "

	maxStateBytes = 1024
	maxStateRunes = 200
)

// nowState is what the tray and the notifications know about the page. Stop is "" while a book plays
// or is paused by hand, "end" when the book finished and "sleep" when the sleep timer stopped it.
type nowState struct {
	Title   string `json:"title"`
	Chapter string `json:"chapter"`
	Left    string `json:"left"`
	Playing bool   `json:"playing"`
	Theme   string `json:"theme"`
	CanAdd  bool   `json:"canAdd"`
	Stop    string `json:"stop"`
}

// themes are the four a window can be on; the saved ground is one of them.
var themes = map[string]bool{"dark": true, "light": true, "sepia": true, "contrast": true}

// parseState reads one message body. It reports false for anything that is not a state of the
// allowed size and shape.
func parseState(body string) (nowState, bool) {
	var s nowState
	if len(body) > maxStateBytes {
		return s, false
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		return nowState{}, false
	}
	s.Title = cleanWords(s.Title)
	s.Chapter = cleanWords(s.Chapter)
	s.Left = cleanWords(s.Left)
	if !themes[s.Theme] {
		s.Theme = ""
	}
	if s.Stop != "end" && s.Stop != "sleep" {
		s.Stop = ""
	}
	return s, true
}

// cleanWords drops control characters, cuts to 200 runes and escapes <, > and & (notification
// bodies may carry markup).
func cleanWords(in string) string {
	in = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, in)
	if utf8.RuneCountInString(in) > maxStateRunes {
		in = string([]rune(in)[:maxStateRunes])
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(in)
}

// plainWords undoes cleanWords' escaping for a place that shows text, not markup (a menu line).
func plainWords(in string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(in)
}
