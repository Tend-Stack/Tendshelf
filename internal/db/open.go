package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the only importer of the driver; registers "sqlite"
)

// FileName is the database file inside the data directory.
const FileName = "tendshelf.db"

// FilePath returns the database path for a data directory.
func FilePath(dataDir string) string { return filepath.Join(dataDir, FileName) }

// openConfig lets tests open a database with other migrations.
type openConfig struct {
	migrations []migration
	supported  int
	now        func() time.Time
}

// Open opens (creating it if needed) the database in dataDir, refusing a file
// that is newer than this binary or not Tendshelf's without changing a byte,
// backing up and migrating an older one.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	ms, err := loadMigrations(embeddedMigrations)
	if err != nil {
		return nil, err
	}
	if len(ms) != SchemaVersion {
		return nil, fmt.Errorf("embedded migrations end at %d but SchemaVersion is %d", len(ms), SchemaVersion)
	}
	return open(ctx, dataDir, openConfig{migrations: ms, supported: SchemaVersion, now: time.Now})
}

func open(ctx context.Context, dataDir string, cfg openConfig) (st *Store, err error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create data folder: %w", err)
	}
	path := FilePath(abs)

	h, exists, err := readHeader(path, cfg.supported)
	if err != nil {
		return nil, err
	}
	if err := CheckHeader(h, cfg.supported); err != nil {
		return nil, err
	}
	if !exists {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create database: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}

	w, err := sql.Open("sqlite", writerDSN(path))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	w.SetConnMaxIdleTime(0)
	var r *sql.DB
	defer func() {
		if err != nil {
			if r != nil {
				r.Close()
			}
			w.Close()
		}
	}()
	if err := w.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	var mode string
	if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return nil, err
	}
	if !strings.EqualFold(mode, "wal") {
		return nil, fmt.Errorf("database did not switch to WAL mode (got %q)", mode)
	}

	// Check again on the writer: it saw the WAL, and the file may have changed.
	var appID int32
	var uv int
	if err := w.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return nil, err
	}
	if err := w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uv); err != nil {
		return nil, err
	}
	if err := CheckHeader(Header{ApplicationID: appID, UserVersion: uv}, cfg.supported); err != nil {
		return nil, err
	}

	var pending []migration
	for _, m := range cfg.migrations {
		if m.Version > uv {
			pending = append(pending, m)
		}
	}
	if uv > 0 && len(pending) > 0 {
		if _, err := backupBefore(ctx, w, abs, uv, cfg.now()); err != nil {
			return nil, fmt.Errorf("back up before migrating: %w", err)
		}
	}
	for _, m := range pending {
		if err := applyMigration(ctx, w, m); err != nil {
			return nil, err
		}
		uv = m.Version
	}

	r, err = sql.Open("sqlite", readerDSN(path))
	if err != nil {
		return nil, err
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)
	if err := r.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("open database reader: %w", err)
	}
	return &Store{w: w, r: r, path: path, version: uv}, nil
}

// fileURI builds the "file:" URI for path. The driver takes the path
// literally, so only the characters that would start a query or fragment are
// escaped.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
	return "file:" + p
}

func writerDSN(path string) string {
	return fileURI(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
}

func readerDSN(path string) string {
	return fileURI(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=query_only(1)"
}

func roDSN(path string) string { return fileURI(path) + "?mode=ro" }

var sqliteMagic = []byte("SQLite format 3\x00")

// readHeader reads application_id and user_version without writing anything.
//
// With no -wal file it reads the 100-byte header directly, because a SQLite
// read-only open of a WAL-mode file creates -wal and -shm. With a -wal file the
// newer header may live only there, so SQLite reads it with mode=ro.
func readHeader(path string, supported int) (h Header, exists bool, err error) {
	notTendshelf := &SchemaError{Found: -1, Supported: supported, Err: ErrNotTendshelf}
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Header{}, false, nil
	}
	if err != nil {
		return Header{}, false, err
	}
	if fi.Size() == 0 {
		return Header{}, true, nil
	}
	if wal, err := os.Stat(path + "-wal"); err == nil && wal.Size() > 0 {
		d, err := sql.Open("sqlite", roDSN(path))
		if err != nil {
			return Header{}, true, err
		}
		defer d.Close()
		var id int32
		var uv int
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
			return Header{}, true, notTendshelf
		}
		if err := d.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uv); err != nil {
			return Header{}, true, notTendshelf
		}
		return Header{ApplicationID: id, UserVersion: uv}, true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return Header{}, true, err
	}
	defer f.Close()
	buf := make([]byte, 100)
	if _, err := io.ReadFull(f, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Header{}, true, notTendshelf
		}
		return Header{}, true, err
	}
	if !bytes.HasPrefix(buf, sqliteMagic) {
		return Header{}, true, notTendshelf
	}
	return Header{
		ApplicationID: int32(binary.BigEndian.Uint32(buf[68:72])),
		UserVersion:   int(int32(binary.BigEndian.Uint32(buf[60:64]))),
	}, true, nil
}
