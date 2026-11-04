package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ReaderSettings are one person's reading choices for the read-along reader (M5 R4 P4). They are kept in
// user_settings as one JSON row, reader.v1, so they follow the person across devices. The theme is not here:
// it is the app's ui.theme.
type ReaderSettings struct {
	Font        string `json:"font"`        // "serif", "sans" or "dyslexic"
	Size        int    `json:"size"`        // text size in px: 18, 20, 22 or 26
	Leading     string `json:"leading"`     // "compact", "normal" or "airy"
	Highlight   string `json:"hl"`          // "wash", "underline" or "brighten"
	Word        bool   `json:"word"`        // underline each word as it is spoken
	Follow      bool   `json:"follow"`      // scroll with the voice
	FollowStyle string `json:"followStyle"` // "steady" or "gentle"
	ReturnAuto  bool   `json:"returnAuto"`  // scroll back to the voice by itself after the person scrolled away
	View        string `json:"view"`        // "page" or "focus"
}

// DefaultReader applies to every field a person has not set.
var DefaultReader = ReaderSettings{
	Font: "serif", Size: 20, Leading: "normal", Highlight: "wash",
	Word: true, Follow: true, FollowStyle: "steady", ReturnAuto: false, View: "page",
}

const keyReader = "reader.v1"

func oneOf(s string, allowed ...string) bool {
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}

func validReaderSize(n int) bool { return n == 18 || n == 20 || n == 22 || n == 26 }

// Validate checks every field against its fixed set.
func (r ReaderSettings) Validate() error {
	ok := oneOf(r.Font, "serif", "sans", "dyslexic") &&
		validReaderSize(r.Size) &&
		oneOf(r.Leading, "compact", "normal", "airy") &&
		oneOf(r.Highlight, "wash", "underline", "brighten") &&
		oneOf(r.FollowStyle, "steady", "gentle") &&
		oneOf(r.View, "page", "focus")
	if !ok {
		return ErrInvalidSetting
	}
	return nil
}

// parseReader reads the stored JSON. A field that is missing, of the wrong type or outside its set keeps its default.
func parseReader(raw string) ReaderSettings {
	out := DefaultReader
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return out
	}
	str := func(key string, dst *string, allowed ...string) {
		var v string
		if json.Unmarshal(m[key], &v) == nil && oneOf(v, allowed...) {
			*dst = v
		}
	}
	flag := func(key string, dst *bool) {
		var v bool
		if json.Unmarshal(m[key], &v) == nil {
			*dst = v
		}
	}
	str("font", &out.Font, "serif", "sans", "dyslexic")
	str("leading", &out.Leading, "compact", "normal", "airy")
	str("hl", &out.Highlight, "wash", "underline", "brighten")
	str("followStyle", &out.FollowStyle, "steady", "gentle")
	str("view", &out.View, "page", "focus")
	flag("word", &out.Word)
	flag("follow", &out.Follow)
	flag("returnAuto", &out.ReturnAuto)
	var n int
	if json.Unmarshal(m["size"], &n) == nil && validReaderSize(n) {
		out.Size = n
	}
	return out
}

// LoadReader returns the person's reading settings, with the default for every missing or invalid field.
func (r *Repo) LoadReader(ctx context.Context, userID string) (ReaderSettings, error) {
	var raw string
	err := r.st.Reader().QueryRowContext(ctx, `SELECT value_json FROM user_settings WHERE user_id = ? AND key = ?`,
		userID, keyReader).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultReader, nil
	}
	if err != nil {
		return DefaultReader, err
	}
	return parseReader(raw), nil
}

// SaveReader validates s and stores it as the person's reader.v1 row.
func (r *Repo) SaveReader(ctx context.Context, userID string, s ReaderSettings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
			userID, keyReader, string(raw), r.nowMs())
		return err
	})
}
