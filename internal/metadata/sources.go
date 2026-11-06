package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// keyLookupSources is the user_settings row that remembers which extra places a person searches.
// It is per person, like the other saved choices in user_settings; the same choice feeds the book
// details sheet and the Libraries "Look up" button.
const keyLookupSources = "lookup.v1"

type lookupSources struct {
	Places []string `json:"places"`
}

// CleanSources keeps the optional places in list order, once each. Unknown names (the retired
// "google" included) and the reliable source are dropped.
func CleanSources(in []string) []string {
	var out []string
	for _, p := range online.ExtraProviders {
		for _, x := range in {
			if x == p {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// LoadSources returns the optional places the person last chose; none when nothing was saved or
// the saved row cannot be read.
func (s *Service) LoadSources(ctx context.Context, userID string) ([]string, error) {
	var raw string
	err := s.repo.Store().Reader().QueryRowContext(ctx, `SELECT value_json FROM user_settings WHERE user_id = ? AND key = ?`,
		userID, keyLookupSources).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v lookupSources
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, nil
	}
	return CleanSources(v.Places), nil
}

// SaveSources remembers the person's choice of optional places.
func (s *Service) SaveSources(ctx context.Context, userID string, places []string) error {
	raw, err := json.Marshal(lookupSources{Places: CleanSources(places)})
	if err != nil {
		return err
	}
	return s.repo.Store().WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
			userID, keyLookupSources, string(raw), s.now().UnixMilli())
		return err
	})
}
