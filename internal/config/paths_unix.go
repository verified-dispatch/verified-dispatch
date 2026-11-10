//go:build unix

package config

import (
	"fmt"
	"os"
	"syscall"
)

// checkOwner refuses a home config owned by another user.
func checkOwner(path string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("home config %s is owned by another user; it sets the builder command, so it must be yours", path)
	}
	return nil
}
