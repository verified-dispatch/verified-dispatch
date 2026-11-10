package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomePath(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		goos string
		env  map[string]string
		want string
		err  bool
	}{
		{"linux", map[string]string{"HOME": "/home/u"}, "/home/u/.config/vdispatch/config.toml", false},
		{"linux", map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/xdg"}, "/xdg/vdispatch/config.toml", false},
		{"darwin", map[string]string{"HOME": "/Users/u", "XDG_CONFIG_HOME": "/xdg"}, "/Users/u/Library/Application Support/vdispatch/config.toml", false},
		{"linux", map[string]string{}, "", true},
		{"darwin", map[string]string{}, "", true},
	}
	for _, c := range cases {
		got, err := HomePath(c.goos, env(c.env))
		if (err != nil) != c.err {
			t.Errorf("%s %v: err = %v, want error %v", c.goos, c.env, err, c.err)
			continue
		}
		if got != c.want {
			t.Errorf("%s %v: path = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
	// HOME and XDG_CONFIG_HOME must be absolute, or the home config could
	// land inside the working tree.
	if _, err := HomePath("linux", env(map[string]string{"HOME": "relative"})); err == nil {
		t.Fatal("relative HOME accepted")
	}
	if _, err := HomePath("linux", env(map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "relative"})); err == nil {
		t.Fatal("relative XDG_CONFIG_HOME accepted")
	}
	if _, err := HomePath("darwin", env(map[string]string{"HOME": "relative"})); err == nil {
		t.Fatal("relative HOME accepted on darwin")
	}
}

func TestReadHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if b, err := ReadHome(path); err != nil || b != nil {
		t.Fatalf("absent file: %v, %v; want nil, nil", b, err)
	}
	if err := os.WriteFile(path, []byte("[gate]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadHome(path); err != nil || string(b) != "[gate]\n" {
		t.Fatalf("present file: %q, %v", b, err)
	}
	for _, mode := range []os.FileMode{0o602, 0o606, 0o666} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadHome(path); err == nil || !strings.Contains(err.Error(), "writable by others") {
			t.Fatalf("mode %o: err = %v, want an error about other writers", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o640, 0o644, 0o660, 0o664} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadHome(path); err != nil {
			t.Fatalf("mode %o: err = %v, want nil", mode, err)
		}
	}
	if err := os.WriteFile(path, make([]byte, ConfigFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHome(path); err == nil {
		t.Fatal("oversized home config read without error")
	}
	if _, err := ReadHome(dir); err == nil {
		t.Fatal("a directory was read as a config file")
	}
}
