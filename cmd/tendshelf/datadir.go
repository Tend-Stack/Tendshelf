package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// defaultDataDir is where the database lives when neither -data nor
// TENDSHELF_DATA is set. It does not depend on the working directory, so the
// server finds the same database wherever it is started.
func defaultDataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		if base, err := os.UserConfigDir(); err == nil {
			return filepath.Join(base, "Tendshelf"), nil
		}
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "Tendshelf"), nil
		}
		if base, err := os.UserConfigDir(); err == nil {
			return filepath.Join(base, "Tendshelf"), nil
		}
	default: // linux and the BSDs
		if base := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(base) { // the XDG spec ignores relative values
			return filepath.Join(base, "tendshelf"), nil
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "tendshelf"), nil
		}
	}
	return "", errors.New("cannot find a data folder; pass -data")
}
