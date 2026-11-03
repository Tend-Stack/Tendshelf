package metadata

import (
	"context"
	"database/sql"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// SettingOnlineEnabled is the server setting that allows online lookups. A missing key means on:
// every lookup still needs a click from a person who may edit.
const SettingOnlineEnabled = "online.enabled"

// OnlineEnabled reports whether online lookups are allowed (true when the setting was never set).
func OnlineEnabled(ctx context.Context, q db.RowQueryer) (bool, error) {
	var on bool
	found, err := db.GetSetting(ctx, q, SettingOnlineEnabled, &on)
	if err != nil {
		return false, err
	}
	return !found || on, nil
}

// SetOnlineEnabled turns online lookups on or off and records who did it, in one transaction.
func SetOnlineEnabled(ctx context.Context, st *db.Store, on bool, userID string) error {
	return st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := db.PutSetting(ctx, tx, SettingOnlineEnabled, on); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: nowMS(), UserID: userID, Action: "settings.online",
			Target: "server", Detail: map[string]bool{"on": on}})
	})
}
