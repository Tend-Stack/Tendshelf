package desktopshell

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "desktop.json")
	first, err := Load(path)
	if err != nil || first.Mode != "" || first.Schema != 1 {
		t.Fatalf("missing file: %+v, %v (want a first run)", first, err)
	}
	want := Config{Mode: ModeConnect, Server: "https://books.example.com", Port: 0}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Mode != want.Mode || got.Server != want.Server || got.Schema != 1 {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	if err := Save(path, Config{Mode: ModePersonal, Port: 41234}); err != nil {
		t.Fatal(err)
	}
	if got, _ = Load(path); got.Mode != ModePersonal || got.Port != 41234 {
		t.Errorf("after second save: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600", info.Mode().Perm())
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v, want 0700", di.Mode().Perm())
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".desktop-*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if err := Save(path, Config{Mode: "cloud"}); err == nil {
		t.Error("an unknown mode must not be saved")
	}
}

func TestDesktopConfigCorrupt(t *testing.T) {
	cases := map[string]string{
		"not json":       "{nope",
		"unknown mode":   `{"schema":1,"mode":"cloud","server":"","port":0}`,
		"future schema":  `{"schema":2,"mode":"","server":"","port":0}`,
		"bad server":     `{"schema":1,"mode":"connect","server":"ftp://x","port":0}`,
		"port too large": `{"schema":1,"mode":"personal","server":"","port":70000}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "desktop.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path)
			if err != nil || c.Mode != "" {
				t.Fatalf("Load = %+v, %v; want a first run", c, err)
			}
			if _, err := os.Stat(path + ".bad"); err != nil {
				t.Errorf("the damaged file was not set aside: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the damaged file is still in place")
			}
		})
	}
}

func TestDesktopConfigPaths(t *testing.T) {
	t.Setenv(HomeEnv, "/h")
	d, err := Paths()
	if err != nil || d.Config != "/h/config" || d.Library != "/h/library" || d.Shell != "/h/shell" {
		t.Fatalf("Paths = %+v, %v", d, err)
	}
	if d.File() != "/h/config/desktop.json" || d.Cookies() != "/h/shell/cookies.sqlite" {
		t.Errorf("File %q, Cookies %q", d.File(), d.Cookies())
	}
	t.Setenv(HomeEnv, "")
	t.Setenv("XDG_DATA_HOME", "/data")
	t.Setenv("XDG_CONFIG_HOME", "/conf")
	d, err = Paths()
	if err != nil || d.Config != "/conf/tendshelf-desktop" || d.Library != "/data/tendshelf-desktop/library" || d.Shell != "/data/tendshelf-desktop/shell" {
		t.Fatalf("Paths = %+v, %v", d, err)
	}
}
