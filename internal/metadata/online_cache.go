package metadata

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// sqlCache keeps the online client's answers in the online_cache table.
type sqlCache struct{ st *db.Store }

// NewOnlineCache returns the online client's cache, backed by the database. The table accepts
// the providers audnexus, audible, google, openlibrary and itunes.
func NewOnlineCache(st *db.Store) online.Cache { return sqlCache{st} }

func (c sqlCache) Get(ctx context.Context, provider, key string, maxAge time.Duration) ([]byte, bool, error) {
	var body string
	var at int64
	err := c.st.Reader().QueryRowContext(ctx, `SELECT response_json, fetched_at FROM online_cache WHERE provider = ? AND query_key = ?`,
		provider, key).Scan(&body, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if time.Since(time.UnixMilli(at)) >= maxAge {
		return nil, false, nil
	}
	return []byte(body), true, nil
}

func (c sqlCache) Put(ctx context.Context, provider, key string, body []byte, at time.Time) error {
	_, err := c.st.Writer().ExecContext(ctx, `INSERT INTO online_cache(provider, query_key, response_json, fetched_at) VALUES (?,?,?,?)
  ON CONFLICT(provider, query_key) DO UPDATE SET response_json = excluded.response_json, fetched_at = excluded.fetched_at`,
		provider, key, string(body), at.UnixMilli())
	return err
}

// PruneOnlineCache deletes cache rows fetched before the given time.
func PruneOnlineCache(ctx context.Context, st *db.Store, before time.Time) error {
	_, err := st.Writer().ExecContext(ctx, `DELETE FROM online_cache WHERE fetched_at < ?`, before.UnixMilli())
	return err
}

func nowMS() int64 { return time.Now().UnixMilli() }
