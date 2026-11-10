package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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
// file, is an error. A file that others can write, or that another user
// owns, is an error, because this file sets the builder command.
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
	if err := checkPrivate(path, info); err != nil {
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

// checkPrivate refuses a home config that another user owns or that anyone
// outside its owner's group can write. Group write is accepted: systems
// that give each user a private group often use umask 002, so 0664 is the
// normal mode there, and a shared group that can write this file can also
// write the user's shell profile.
func checkPrivate(path string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("home config %s is writable by others; it sets the builder command, so only its owner may write it", path)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("home config %s is owned by another user; it sets the builder command, so it must be yours", path)
	}
	return nil
}
