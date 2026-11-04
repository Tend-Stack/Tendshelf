package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// Settings are one person's preferences, kept in user_settings as one JSON row per key. The
// playback fields are read by the player (P1c); the Settings page stores them now.
type Settings struct {
	Theme        string // "system", "dark", "light", "sepia" or "contrast"
	Speed        float64
	SkipBackS    int
	SkipForwardS int
	AutoRewind   bool
	NextInSeries bool
	PlayerTint   bool
	BigButtons   bool
	TextSize     string // "standard" or "large"
	Nav          string // "full" or "rail": the desktop sidebar
	TimeZone     string // an IANA name from the person's browser; "" means the server's zone
}

// DefaultSettings applies to every key a person has not set.
var DefaultSettings = Settings{
	Theme: "system", TextSize: "standard", Nav: "full", Speed: 1.0, SkipBackS: 15, SkipForwardS: 30,
	AutoRewind: true, NextInSeries: true, PlayerTint: true,
}

// ErrInvalidSetting means a settings value is outside its allowed set.
var ErrInvalidSetting = errors.New("users: invalid setting")

// The user_settings keys.
const (
	keyTheme        = "ui.theme"
	keySpeed        = "playback.speed"
	keySkipBack     = "playback.skip_back_s"
	keySkipForward  = "playback.skip_forward_s"
	keyAutoRewind   = "playback.auto_rewind"
	keyNextInSeries = "playback.next_in_series"
	keyPlayerTint   = "ui.player_tint"
	keyBigButtons   = "ui.big_buttons"
	keyTextSize     = "ui.text_size"
	keyNav          = "ui.nav"
	keyTimeZone     = "ui.time_zone"
)

func validTheme(t string) bool {
	switch t {
	case "system", "dark", "light", "sepia", "contrast":
		return true
	}
	return false
}

// validTextSize accepts "" as well: a Settings built without a text size (older callers) means standard.
func validTextSize(t string) bool { return t == "" || t == "standard" || t == "large" }

// validNav accepts "" as well: a Settings built without a nav state (older callers) means full.
func validNav(n string) bool { return n == "" || n == "full" || n == "rail" }

// ValidTimeZone reports whether s names a zone Tendshelf can load. "" and "Local" are not names.
func ValidTimeZone(s string) bool {
	if s == "" || s == "Local" || len(s) > 64 {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// validTimeZoneSetting accepts "" (the server's zone) as well as a loadable name.
func validTimeZoneSetting(s string) bool { return s == "" || ValidTimeZone(s) }

func validSpeed(v float64) bool {
	if math.IsNaN(v) || v < 0.5 || v > 3.0 {
		return false
	}
	return math.Abs(v*20-math.Round(v*20)) < 1e-6
}

func validSkip(n int) bool {
	switch n {
	case 10, 15, 30, 60:
		return true
	}
	return false
}

// Validate checks every field against its fixed set.
func (s Settings) Validate() error {
	if !validTheme(s.Theme) || !validTextSize(s.TextSize) || !validNav(s.Nav) || !validTimeZoneSetting(s.TimeZone) || !validSpeed(s.Speed) || !validSkip(s.SkipBackS) || !validSkip(s.SkipForwardS) {
		return ErrInvalidSetting
	}
	return nil
}

type settingEntry struct {
	key string
	val any
}

// entries lists the settings as key and Go value, for storing.
func (s Settings) entries() []settingEntry {
	if s.TextSize == "" {
		s.TextSize = "standard"
	}
	if s.Nav == "" {
		s.Nav = "full"
	}
	return []settingEntry{
		{keyTheme, s.Theme},
		{keySpeed, math.Round(s.Speed*100) / 100},
		{keySkipBack, s.SkipBackS},
		{keySkipForward, s.SkipForwardS},
		{keyAutoRewind, s.AutoRewind},
		{keyNextInSeries, s.NextInSeries},
		{keyPlayerTint, s.PlayerTint},
		{keyBigButtons, s.BigButtons},
		{keyTextSize, s.TextSize},
		{keyNav, s.Nav},
		{keyTimeZone, s.TimeZone},
	}
}

// apply sets the field for key from its JSON value, when the key is known and the value valid.
// Unknown keys and invalid values leave the field alone.
func (s *Settings) apply(key, raw string) {
	switch key {
	case keyTheme:
		var v string
		if json.Unmarshal([]byte(raw), &v) == nil && validTheme(v) {
			s.Theme = v
		}
	case keySpeed:
		var v float64
		if json.Unmarshal([]byte(raw), &v) == nil && validSpeed(v) {
			s.Speed = v
		}
	case keySkipBack:
		var v int
		if json.Unmarshal([]byte(raw), &v) == nil && validSkip(v) {
			s.SkipBackS = v
		}
	case keySkipForward:
		var v int
		if json.Unmarshal([]byte(raw), &v) == nil && validSkip(v) {
			s.SkipForwardS = v
		}
	case keyAutoRewind:
		setBool(&s.AutoRewind, raw)
	case keyNextInSeries:
		setBool(&s.NextInSeries, raw)
	case keyPlayerTint:
		setBool(&s.PlayerTint, raw)
	case keyBigButtons:
		setBool(&s.BigButtons, raw)
	case keyTextSize:
		var v string
		if json.Unmarshal([]byte(raw), &v) == nil && validTextSize(v) {
			s.TextSize = v
		}
	case keyNav:
		var v string
		if json.Unmarshal([]byte(raw), &v) == nil && validNav(v) {
			s.Nav = v
		}
	case keyTimeZone:
		var v string
		if json.Unmarshal([]byte(raw), &v) == nil && validTimeZoneSetting(v) {
			s.TimeZone = v
		}
	}
}

func setBool(dst *bool, raw string) {
	var v bool
	if json.Unmarshal([]byte(raw), &v) == nil {
		*dst = v
	}
}

// storedSettings reads the raw rows of one person.
func (r *Repo) storedSettings(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT key, value_json FROM user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		have[k] = v
	}
	return have, rows.Err()
}

// LoadSettings returns the person's settings, with the default for every absent or invalid row.
func (r *Repo) LoadSettings(ctx context.Context, userID string) (Settings, error) {
	have, err := r.storedSettings(ctx, userID)
	if err != nil {
		return DefaultSettings, err
	}
	s := DefaultSettings
	for k, v := range have {
		s.apply(k, v)
	}
	return s, nil
}

// SaveSettings validates s and writes, in one transaction, only the keys whose stored value
// differs. A key never stored is written only when it differs from the default, except the theme,
// which is always written once chosen so that it beats a stale cookie on another browser.
func (r *Repo) SaveSettings(ctx context.Context, userID string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	have, err := r.storedSettings(ctx, userID)
	if err != nil {
		return err
	}
	defaults := map[string]string{}
	for _, e := range DefaultSettings.entries() {
		raw, _ := json.Marshal(e.val)
		defaults[e.key] = string(raw)
	}
	type change struct{ key, raw string }
	var changes []change
	for _, e := range s.entries() {
		b, err := json.Marshal(e.val)
		if err != nil {
			return err
		}
		raw := string(b)
		old, stored := have[e.key]
		switch {
		case stored && old == raw:
			continue
		case !stored && raw == defaults[e.key] && e.key != keyTheme:
			continue
		}
		changes = append(changes, change{e.key, raw})
	}
	if len(changes) == 0 {
		return nil
	}
	now := r.nowMs()
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, c := range changes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, ?, ?, ?)
				ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
				userID, c.key, c.raw, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Theme reads ui.theme alone: one indexed read for every page render. It returns "" when the
// person has not chosen one.
func (r *Repo) Theme(ctx context.Context, userID string) (string, error) {
	var raw string
	err := r.st.Reader().QueryRowContext(ctx, `SELECT value_json FROM user_settings WHERE user_id = ? AND key = ?`,
		userID, keyTheme).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var v string
	if json.Unmarshal([]byte(raw), &v) != nil || !validTheme(v) {
		return "", nil
	}
	return v, nil
}

// Look is the part of a person's settings every page render needs: the theme and the text size.
// An absent or invalid value is "".
type Look struct{ Theme, TextSize, Nav, TimeZone string }

// Look reads ui.theme, ui.text_size, ui.nav and ui.time_zone in one query.
func (r *Repo) Look(ctx context.Context, userID string) (Look, error) {
	rows, err := r.st.Reader().QueryContext(ctx,
		`SELECT key, value_json FROM user_settings WHERE user_id = ? AND key IN (?, ?, ?, ?)`, userID, keyTheme, keyTextSize, keyNav, keyTimeZone)
	if err != nil {
		return Look{}, err
	}
	defer rows.Close()
	var l Look
	for rows.Next() {
		var k, raw string
		if err := rows.Scan(&k, &raw); err != nil {
			return Look{}, err
		}
		var v string
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		switch {
		case k == keyTheme && validTheme(v):
			l.Theme = v
		case k == keyTextSize && validTextSize(v):
			l.TextSize = v
		case k == keyNav && validNav(v):
			l.Nav = v
		case k == keyTimeZone && ValidTimeZone(v):
			l.TimeZone = v
		}
	}
	return l, rows.Err()
}
