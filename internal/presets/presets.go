// Package presets holds the builder presets embedded in the binary.
package presets

import _ "embed"

//go:embed embedded.toml
var embedded []byte

// Embedded returns the TOML text of the built-in presets.
func Embedded() []byte { return embedded }
