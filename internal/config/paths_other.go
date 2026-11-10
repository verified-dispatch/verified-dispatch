//go:build !unix

package config

import "os"

// checkOwner has no ownership information to check on this platform.
func checkOwner(string, os.FileInfo) error { return nil }
