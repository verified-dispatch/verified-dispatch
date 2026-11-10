package config

import (
	"time"

	"github.com/verified-dispatch/verified-dispatch/internal/gitx"
)

// BuiltinCaps are the caps when no home config lowers them.
var BuiltinCaps = Caps{
	CommitStallMinutes: 20,
	WallClockMinutes:   90,
	GateMinutes:        30,
	GatePrepareMinutes: 30,
	GitTimeoutSeconds:  600,
	OutputBytes:        50 << 20,
	RetentionDays:      30,
}

// Fixed limits that no layer can set.
const (
	// ReceiptLines caps the text receipt.
	ReceiptLines = 40
	// ResultFileBytes caps the builder's result file.
	ResultFileBytes = 64 << 10
	// ContractAdditionBytes caps the contract additions file.
	ContractAdditionBytes = 16 << 10
	// ConfigFileBytes caps each configuration file.
	ConfigFileBytes = 64 << 10
	// MaxPatterns caps each pattern list after merging.
	MaxPatterns = 64
	// MaxPatternBytes caps one pattern.
	MaxPatternBytes = 512
	// MaxPatternInstructions caps the compiled program of one pattern.
	MaxPatternInstructions = 2000
	// MaxRunPatternInstructions caps the compiled programs of every pattern
	// in one run, condense and redaction together.
	MaxRunPatternInstructions = 20000
	// MaxCommandBytes caps the gate and prepare commands.
	MaxCommandBytes = 4096
)

// DefaultPreset is the builder preset when no layer names one.
const DefaultPreset = "claude-code"

// defaults returns the built-in layer.
func defaults() Resolved {
	return Resolved{
		Gate: Gate{
			Minutes:        BuiltinCaps.GateMinutes,
			PrepareMinutes: BuiltinCaps.GatePrepareMinutes,
			Isolated:       true,
		},
		Builder: Builder{Preset: DefaultPreset},
		Limits: Limits{
			CommitStallMinutes: 20,
			WallClockMinutes:   90,
			GitTimeoutSeconds:  int(gitx.DefaultTimeout / time.Second),
			OutputBytes:        50 << 20,
		},
		Contract:      Contract{CommitStyle: "conventional"},
		RetentionDays: 14,
		Caps:          BuiltinCaps,
		Sources:       Sources{Gate: SourceBuiltin, Prepare: SourceBuiltin, Builder: SourceBuiltin},
	}
}
