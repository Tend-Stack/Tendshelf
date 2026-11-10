package podcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// The settings keys (table settings, JSON values). Only the owner changes them, through /admin/podcasts.
const (
	KeyEnabled    = "podcasts.enabled"
	KeyRefresh    = "podcasts.refresh"
	KeyCacheBytes = "podcasts.cache_bytes"
	KeyServerAuto = "podcasts.server_auto"
	KeyRadio      = "radio.enabled"

	// MaxCacheBytes is the largest limit the owner can set: 1 TiB.
	MaxCacheBytes = 1 << 40
	// DefaultCacheBytes is 10 GiB.
	DefaultCacheBytes = 10 << 30
)

// Refresh intervals the owner can pick.
const (
	RefreshOff = "off"
	Refresh1h  = "1h"
	Refresh6h  = "6h"
	Refresh24h = "24h"
)

// ErrBadSetting means a settings value is out of range.
var ErrBadSetting = errors.New("podcast: bad setting")

// Settings are the owner's choices. The zero value is not the default: use Defaults.
type Settings struct {
	Enabled      bool
	Refresh      string
	CacheBytes   int64
	ServerAuto   bool
	RadioEnabled bool
}

// Defaults is everything off, no scheduled refresh, 10 GiB of cache.
func Defaults() Settings {
	return Settings{Refresh: RefreshOff, CacheBytes: DefaultCacheBytes}
}

// Interval is the scheduled-refresh period, or 0 when refresh is off.
func (s Settings) Interval() time.Duration {
	switch s.Refresh {
	case Refresh1h:
		return time.Hour
	case Refresh6h:
		return 6 * time.Hour
	case Refresh24h:
		return 24 * time.Hour
	}
	return 0
}

// Valid reports whether every value is in range.
func (s Settings) Valid() bool {
	switch s.Refresh {
	case RefreshOff, Refresh1h, Refresh6h, Refresh24h:
	default:
		return false
	}
	return s.CacheBytes >= 0 && s.CacheBytes <= MaxCacheBytes
}

// LoadSettings reads the settings. A missing or damaged value takes its default, so a database error
// reads as "off".
func LoadSettings(ctx context.Context, st *db.Store) Settings {
	s := Defaults()
	rows, err := st.Reader().QueryContext(ctx, `SELECT key, value_json FROM settings WHERE key IN (?,?,?,?,?)`,
		KeyEnabled, KeyRefresh, KeyCacheBytes, KeyServerAuto, KeyRadio)
	if err != nil {
		return Defaults()
	}
	defer rows.Close()
	for rows.Next() {
		var k, raw string
		if rows.Scan(&k, &raw) != nil {
			return Defaults()
		}
		switch k {
		case KeyEnabled:
			_ = json.Unmarshal([]byte(raw), &s.Enabled)
		case KeyRefresh:
			var v string
			if json.Unmarshal([]byte(raw), &v) == nil {
				s.Refresh = v
			}
		case KeyCacheBytes:
			var v int64
			if json.Unmarshal([]byte(raw), &v) == nil {
				s.CacheBytes = v
			}
		case KeyServerAuto:
			_ = json.Unmarshal([]byte(raw), &s.ServerAuto)
		case KeyRadio:
			_ = json.Unmarshal([]byte(raw), &s.RadioEnabled)
		}
	}
	if rows.Err() != nil || !s.Valid() {
		return Defaults()
	}
	return s
}

// SaveSettings validates and stores the settings in one transaction and writes an audit entry naming
// what changed (never a value that is a secret: there are none here).
func SaveSettings(ctx context.Context, st *db.Store, by string, s Settings, now time.Time) error {
	if !s.Valid() {
		return ErrBadSetting
	}
	before := LoadSettings(ctx, st)
	return st.WriteTx(ctx, func(tx *sql.Tx) error {
		for k, v := range map[string]any{KeyEnabled: s.Enabled, KeyRefresh: s.Refresh, KeyCacheBytes: s.CacheBytes,
			KeyServerAuto: s.ServerAuto, KeyRadio: s.RadioEnabled} {
			if err := db.PutSetting(ctx, tx, k, v); err != nil {
				return err
			}
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now.UnixMilli(), UserID: by, Action: "podcast.settings", Target: "server",
			Detail: map[string]any{"enabled": s.Enabled, "was_enabled": before.Enabled, "refresh": s.Refresh,
				"cache_bytes": s.CacheBytes, "server_auto": s.ServerAuto, "radio": s.RadioEnabled}})
	})
}
