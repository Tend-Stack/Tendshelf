// Package desktopshell holds the parts of the desktop app that need no cgo: its saved choice, the
// server check and the first-run start page (decision 0020). The window itself lives in desktop/.
package desktopshell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv, when set, moves every folder under one root: <h>/config, <h>/library and <h>/shell.
// Tests and isolated runs use it.
const HomeEnv = "TENDSHELF_DESKTOP_HOME"

// Modes the app can be in. "" means the person has not chosen yet.
const (
	ModePersonal = "personal"
	ModeConnect  = "connect"
)

// Dirs are the folders the desktop app owns. Personal data never shares the server's default data
// folder (plan invariant 9).
type Dirs struct {
	Config  string // holds desktop.json
	Library string // personal mode's data folder
	Shell   string // the window's cookie store (cookies.sqlite), mode 0700
}

// File is the path of the saved choice.
func (d Dirs) File() string { return filepath.Join(d.Config, "desktop.json") }

// Cookies is the path of the window's cookie database.
func (d Dirs) Cookies() string { return filepath.Join(d.Shell, "cookies.sqlite") }

// Paths resolves the folders: under TENDSHELF_DESKTOP_HOME when set, else the person's config and
// data folders (XDG_DATA_HOME, or ~/.local/share).
func Paths() (Dirs, error) {
	if h := os.Getenv(HomeEnv); h != "" {
		return Dirs{Config: filepath.Join(h, "config"), Library: filepath.Join(h, "library"), Shell: filepath.Join(h, "shell")}, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("find the config folder: %w", err)
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, fmt.Errorf("find the home folder: %w", err)
		}
		data = filepath.Join(home, ".local", "share")
	}
	root := filepath.Join(data, "tendshelf-desktop")
	return Dirs{
		Config:  filepath.Join(cfg, "tendshelf-desktop"),
		Library: filepath.Join(root, "library"),
		Shell:   filepath.Join(root, "shell"),
	}, nil
}

// Config is the saved choice. Port is personal mode's loopback port (0 until one is picked); it is
// kept because localStorage is keyed by origin.
type Config struct {
	Schema int    `json:"schema"`
	Mode   string `json:"mode"`
	Server string `json:"server"`
	Port   int    `json:"port"`
}

const schemaVersion = 1

// Load reads the saved choice. A missing file is a first run. A file that cannot be read, parsed
// or understood is renamed to <path>.bad and also treated as a first run, so a person is never
// locked out by a damaged file.
func Load(path string) (Config, error) {
	first := Config{Schema: schemaVersion}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return first, nil
	}
	if err == nil {
		var c Config
		if json.Unmarshal(b, &c) == nil && valid(c) {
			return c, nil
		}
	}
	if rerr := os.Rename(path, path+".bad"); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return first, fmt.Errorf("set aside the damaged settings file: %w", rerr)
	}
	return first, nil
}

func valid(c Config) bool {
	if c.Schema != schemaVersion || c.Port < 0 || c.Port > 65535 {
		return false
	}
	switch c.Mode {
	case "", ModePersonal:
		return true
	case ModeConnect:
		origin, err := NormalizeServer(c.Server)
		return err == nil && origin == c.Server
	}
	return false
}

// Save writes the choice atomically (temporary file, sync, rename) with mode 0600.
func Save(path string, c Config) error {
	c.Schema = schemaVersion
	if !valid(c) {
		return errors.New("the settings are not valid")
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".desktop-*.tmp") // CreateTemp makes it 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
