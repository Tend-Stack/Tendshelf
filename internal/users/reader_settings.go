package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ReaderSettings are one person's reading choices for the book reader (M5 R4 P4). They are kept in
// user_settings as one JSON row, reader.v1, so they follow the person across devices. The theme is not here:
// it is the app's ui.theme.
type ReaderSettings struct {
	Font    string `json:"font"`    // "serif", "sans" or "dyslexic"
	Size    int    `json:"size"`    // text size in px: 18, 20, 22 or 26
	Leading string `json:"leading"` // "compact", "normal" or "airy"
	// RD2 (the page reader): the side margins, justified text, hyphenation, pages or scroll, and mirrored tap zones.
	Margin  string `json:"margin"`  // "narrow", "normal" or "wide"
	Justify bool   `json:"justify"` // off by default (the design handoff)
	Hyphens bool   `json:"hyphens"` // on by default
	Flow    string `json:"flow"`    // "auto" (pages below 1280 px, scroll above), "pages" or "scroll"
	TapSwap bool   `json:"tapSwap"` // the left and right tap zones swap, for left-handers
}

// DefaultReader applies to every field a person has not set.
var DefaultReader = ReaderSettings{
	Font: "serif", Size: 20, Leading: "normal",
	Margin: "normal", Hyphens: true, Flow: "auto",
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
		oneOf(r.Margin, "narrow", "normal", "wide") &&
		oneOf(r.Flow, "auto", "pages", "scroll")
	if !ok {
		return ErrInvalidSetting
	}
	return nil
}

// parseReader reads the stored JSON; keys it does not know (older versions kept more) are ignored. A field that is missing, of the wrong type or outside its set keeps its default.
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
	str("font", &out.Font, "serif", "sans", "dyslexic")
	str("leading", &out.Leading, "compact", "normal", "airy")
	str("margin", &out.Margin, "narrow", "normal", "wide")
	str("flow", &out.Flow, "auto", "pages", "scroll")
	for key, dst := range map[string]*bool{"justify": &out.Justify, "hyphens": &out.Hyphens, "tapSwap": &out.TapSwap} {
		var v bool
		if json.Unmarshal(m[key], &v) == nil {
			*dst = v
		}
	}
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
