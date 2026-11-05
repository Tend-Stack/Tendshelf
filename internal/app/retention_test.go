package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

func TestSweepRetentionPass(t *testing.T) {
	ctx := context.Background()
	st, err := db.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	old := now.Add(-db.JobsKeep - time.Hour).UnixMilli()
	if _, err := st.Writer().Exec(`INSERT INTO jobs(id, kind, state, created_at, updated_at) VALUES ('j', 'scan', 'done', ?, ?)`, old, old); err != nil {
		t.Fatal(err)
	}
	coverDir := t.TempDir()
	stale := filepath.Join(coverDir, "a.img")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := now.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	covers := online.New(online.Options{CoverDir: coverDir})
	sweepRetention(ctx, st, covers, slog.New(slog.NewTextHandler(io.Discard, nil)), now)
	var n int
	_ = st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE id = 'j'`).Scan(&n)
	if n != 0 {
		t.Error("old finished job survived the pass")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("expired cover survived the pass")
	}
}
