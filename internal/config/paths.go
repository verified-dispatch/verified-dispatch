package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// HomePath returns the home config path for goos, reading the environment
// through getenv: $XDG_CONFIG_HOME/vdispatch/config.toml on Linux, falling
// back to ~/.config when the variable is unset, and ~/Library/Application
// Support/vdispatch/config.toml on macOS. HOME and XDG_CONFIG_HOME must be
// absolute; a relative value could place the home config inside the
// working tree.
func HomePath(goos string, getenv func(string) string) (string, error) {
	home := getenv("HOME")
	if home != "" && !filepath.IsAbs(home) {
		return "", fmt.Errorf("config: HOME %q is not an absolute path", home)
	}
	if goos == "darwin" {
		if home == "" {
			return "", errors.New("config: HOME is not set")
		}
		return filepath.Join(home, "Library", "Application Support", "vdispatch", "config.toml"), nil
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		if !filepath.IsAbs(x) {
			return "", fmt.Errorf("config: XDG_CONFIG_HOME %q is not an absolute path", x)
		}
		return filepath.Join(x, "vdispatch", "config.toml"), nil
	}
	if home == "" {
		return "", errors.New("config: neither XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(home, ".config", "vdispatch", "config.toml"), nil
}

// DefaultHomePath returns HomePath for this process.
func DefaultHomePath() (string, error) {
	return HomePath(runtime.GOOS, os.Getenv)
}

// ReadHome reads the home config. A missing file is not an error and
// returns nil. A file over ConfigFileBytes, or a path that is not a regular
// file, is an error. A file that other users can write, or that another
// user owns, is an error, because this file sets the builder command.
func ReadHome(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("home config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("home config %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("home config %s is writable by group or others; it sets the builder command, so only its owner may write it", path)
	}
	if err := checkOwner(path, info); err != nil {
		return nil, err
	}
	if info.Size() > ConfigFileBytes {
		return nil, fmt.Errorf("home config %s is larger than %d bytes", path, ConfigFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("home config: %w", err)
	}
	return data, nil
}
