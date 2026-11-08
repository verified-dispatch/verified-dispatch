package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// layer is the shape both configuration files decode into. Pointer fields
// tell an unset key from a zero value; list fields are nil when unset.
type layer struct {
	Gate      gateLayer         `toml:"gate" json:"gate"`
	Builder   builderLayer      `toml:"builder" json:"builder"`
	Limits    limitsLayer       `toml:"limits" json:"limits"`
	Caps      capsLayer         `toml:"caps" json:"caps"`
	Condense  patternsLayer     `toml:"condense" json:"condense"`
	Redaction patternsLayer     `toml:"redaction" json:"redaction"`
	Contract  contractLayer     `toml:"contract" json:"contract"`
	Retention retentionLayer    `toml:"retention" json:"retention"`
	Digest    digestLayer       `toml:"digest" json:"digest"`
	Presets   map[string]Preset `toml:"presets" json:"presets"`
}

type gateLayer struct {
	Command        *string  `toml:"command" json:"command"`
	Prepare        *string  `toml:"prepare" json:"prepare"`
	PrepareInputs  []string `toml:"prepare_inputs" json:"prepare_inputs"`
	Minutes        *int     `toml:"minutes" json:"minutes"`
	PrepareMinutes *int     `toml:"prepare_minutes" json:"prepare_minutes"`
	Isolated       *bool    `toml:"isolated" json:"isolated"`
	Protected      []string `toml:"protected" json:"protected"`
}

type builderLayer struct {
	Preset *string `toml:"preset" json:"preset"`
}

type limitsLayer struct {
	CommitStallMinutes *int `toml:"commit_stall_minutes" json:"commit_stall_minutes"`
	WallClockMinutes   *int `toml:"wall_clock_minutes" json:"wall_clock_minutes"`
	GitTimeoutSeconds  *int `toml:"git_timeout_seconds" json:"git_timeout_seconds"`
	OutputMiB          *int `toml:"output_mib" json:"output_mib"`
}

type capsLayer struct {
	CommitStallMinutes *int `toml:"commit_stall_minutes" json:"commit_stall_minutes"`
	WallClockMinutes   *int `toml:"wall_clock_minutes" json:"wall_clock_minutes"`
	GateMinutes        *int `toml:"gate_minutes" json:"gate_minutes"`
	GatePrepareMinutes *int `toml:"gate_prepare_minutes" json:"gate_prepare_minutes"`
	GitTimeoutSeconds  *int `toml:"git_timeout_seconds" json:"git_timeout_seconds"`
	OutputMiB          *int `toml:"output_mib" json:"output_mib"`
	RetentionDays      *int `toml:"retention_days" json:"retention_days"`
}

type patternsLayer struct {
	Patterns []string `toml:"patterns" json:"patterns"`
}

type contractLayer struct {
	CommitStyle *string `toml:"commit_style" json:"commit_style"`
	Additions   *string `toml:"additions" json:"additions"`
}

type retentionLayer struct {
	Days *int `toml:"days" json:"days"`
}

type digestLayer struct {
	KeyFile *string `toml:"key_file" json:"key_file"`
}

// decodeLayer parses one configuration file. name labels errors.
func decodeLayer(data []byte, name string) (*layer, toml.MetaData, error) {
	var l layer
	md, err := toml.Decode(string(data), &l)
	if err != nil {
		return nil, md, fmt.Errorf("%s: %w", name, err)
	}
	return &l, md, nil
}

// unknownKeys lists the keys in md that no layer field accepted, sorted.
func unknownKeys(md toml.MetaData) []string {
	var out []string
	for _, k := range md.Undecoded() {
		out = append(out, k.String())
	}
	sort.Strings(out)
	return out
}

// repoAllowed lists every key a repository config may set. Tables that
// contain an allowed key are allowed as tables.
var repoAllowed = map[string]bool{
	"gate.command":                true,
	"gate.prepare":                true,
	"gate.prepare_inputs":         true,
	"gate.minutes":                true,
	"gate.prepare_minutes":        true,
	"gate.isolated":               true,
	"gate.protected":              true,
	"builder.preset":              true,
	"limits.commit_stall_minutes": true,
	"limits.wall_clock_minutes":   true,
	"limits.git_timeout_seconds":  true,
	"condense.patterns":           true,
	"redaction.patterns":          true,
	"contract.commit_style":       true,
	"contract.additions":          true,
}

func isAllowedTable(k string) bool {
	for a := range repoAllowed {
		if strings.HasPrefix(a, k+".") {
			return true
		}
	}
	return false
}

// checkRepoKeys refuses the first key in document order that a repository
// config may not set. The builder command and preset tables get their own
// wording because they are the one escalation a repository could attempt.
func checkRepoKeys(md toml.MetaData) error {
	for _, key := range md.Keys() {
		k := key.String()
		if repoAllowed[k] || isAllowedTable(k) {
			continue
		}
		switch {
		case k == "builder.command":
			return refusef("repository config may not set builder.command; the builder command is the user's, so name a preset instead")
		case k == "presets" || strings.HasPrefix(k, "presets."):
			return refusef("repository config may not define presets")
		default:
			return refusef("repository config sets %s, which is not an allowed key", k)
		}
	}
	return nil
}
