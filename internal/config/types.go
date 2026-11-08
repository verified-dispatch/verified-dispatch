// Package config resolves the four configuration layers (built-in, home,
// repository, flags) into one value held in memory for a run. It enforces
// the repository key allow list and the caps, resolves the builder preset
// under the trust rules, records where the gate and builder came from, and
// hashes the result so a later change can be detected.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Source names the layer a value came from.
type Source string

// The four layers, in override order.
const (
	SourceBuiltin Source = "builtin"
	SourceHome    Source = "home"
	SourceRepo    Source = "repo"
	SourceFlag    Source = "flag"
)

// Gate is the gate configuration.
type Gate struct {
	Command        string   `json:"command"`
	Prepare        string   `json:"prepare"`
	PrepareInputs  []string `json:"prepare_inputs"`
	Minutes        int      `json:"minutes"`
	PrepareMinutes int      `json:"prepare_minutes"`
	Isolated       bool     `json:"isolated"`
	Protected      []string `json:"protected"`
}

// PromptStdin is the prompt mode that feeds the prompt on standard input.
const PromptStdin = "stdin"

// promptArgPrefix starts the prompt mode that passes the prompt file path
// as one extra argument, with PromptFilePlaceholder replaced.
const promptArgPrefix = "arg:"

// PromptFilePlaceholder is replaced by the prompt file path in an
// "arg:" prompt template.
const PromptFilePlaceholder = "{prompt_file}"

// Preset is a named builder command template.
type Preset struct {
	Command        []string `toml:"command" json:"command"`
	Prompt         string   `toml:"prompt" json:"prompt"`
	Cost           string   `toml:"cost" json:"cost"`
	RepoSelectable bool     `toml:"repo_selectable" json:"repo_selectable"`
}

// Builder is the resolved builder: the preset name and the preset's fields.
type Builder struct {
	Preset  string   `json:"preset"`
	Command []string `json:"command"`
	Prompt  string   `json:"prompt"`
	Cost    string   `json:"cost"`
}

// Argv returns the full command line for a run whose prompt is stored at
// promptFile. For a stdin preset it is the command alone.
func (b Builder) Argv(promptFile string) []string {
	argv := append([]string(nil), b.Command...)
	if strings.HasPrefix(b.Prompt, promptArgPrefix) {
		tmpl := strings.TrimPrefix(b.Prompt, promptArgPrefix)
		argv = append(argv, strings.ReplaceAll(tmpl, PromptFilePlaceholder, promptFile))
	}
	return argv
}

// Limits bound one run.
type Limits struct {
	CommitStallMinutes int   `json:"commit_stall_minutes"`
	WallClockMinutes   int   `json:"wall_clock_minutes"`
	GitTimeoutSeconds  int   `json:"git_timeout_seconds"`
	OutputBytes        int64 `json:"output_bytes"`
}

// Contract configures the builder contract.
type Contract struct {
	CommitStyle string `json:"commit_style"`
	Additions   string `json:"additions"`
}

// Caps are the upper bounds values may take. The home layer may lower
// them; nothing may raise them.
type Caps struct {
	CommitStallMinutes int   `json:"commit_stall_minutes"`
	WallClockMinutes   int   `json:"wall_clock_minutes"`
	GateMinutes        int   `json:"gate_minutes"`
	GatePrepareMinutes int   `json:"gate_prepare_minutes"`
	GitTimeoutSeconds  int   `json:"git_timeout_seconds"`
	OutputBytes        int64 `json:"output_bytes"`
	RetentionDays      int   `json:"retention_days"`
}

// Sources records the layer the gate, prepare, and builder came from, for
// the receipt.
type Sources struct {
	Gate    Source `json:"gate"`
	Prepare Source `json:"prepare"`
	Builder Source `json:"builder"`
}

// Resolved is the configuration of one run.
type Resolved struct {
	Gate          Gate     `json:"gate"`
	Builder       Builder  `json:"builder"`
	Limits        Limits   `json:"limits"`
	Condense      []string `json:"condense"`
	Redaction     []string `json:"redaction"`
	Contract      Contract `json:"contract"`
	RetentionDays int      `json:"retention_days"`
	// DigestKeyFile is the home config's digest.key_file. Empty means the
	// state directory default.
	DigestKeyFile string  `json:"digest_key_file"`
	Caps          Caps    `json:"caps"`
	Sources       Sources `json:"sources"`
	// RepoLayerHash hashes the decoded repository layer. Empty when the
	// repository has no config.
	RepoLayerHash string `json:"repo_layer_hash"`
}

// Hash returns a hex SHA-256 over the JSON form of r. The receipt and the
// run directory record compare this value.
func (r *Resolved) Hash() string { return hashJSON(r) }

func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Every type passed here is a plain struct of strings, numbers,
		// bools, slices, and maps; marshaling cannot fail.
		panic("config: hashJSON: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
