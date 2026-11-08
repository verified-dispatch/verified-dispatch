package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Flags is the command-line layer. Empty means unset.
type Flags struct {
	// Gate replaces the gate command.
	Gate string
	// Builder names the preset.
	Builder string
}

// Inputs are the raw layers. A nil Home or Repo means that layer is absent.
type Inputs struct {
	Home  []byte
	Repo  []byte
	Flags Flags
}

// Load resolves the layers into one value. A *Refusal means REFUSED; any
// other error means the tool could not read a file it needs.
func Load(in Inputs) (*Resolved, error) {
	r := defaults()
	embedded, err := embeddedPresets()
	if err != nil {
		return nil, err
	}
	home := map[string]Preset{}

	if in.Home != nil {
		if len(in.Home) > ConfigFileBytes {
			return nil, fmt.Errorf("home config is larger than %d bytes", ConfigFileBytes)
		}
		l, md, err := decodeLayer(in.Home, "home config")
		if err != nil {
			return nil, err
		}
		if u := unknownKeys(md); len(u) > 0 {
			return nil, fmt.Errorf("home config: unknown key %s", u[0])
		}
		if err := lowerCaps(&r, l.Caps); err != nil {
			return nil, err
		}
		if err := apply(&r, l, SourceHome); err != nil {
			return nil, err
		}
		if l.Retention.Days != nil {
			if err := checkRange("home config", "retention.days", int64(*l.Retention.Days), int64(r.Caps.RetentionDays)); err != nil {
				return nil, err
			}
			r.RetentionDays = *l.Retention.Days
		}
		if l.Digest.KeyFile != nil {
			if !filepath.IsAbs(*l.Digest.KeyFile) {
				return nil, fmt.Errorf("home config: digest.key_file must be an absolute path, got %q", *l.Digest.KeyFile)
			}
			r.DigestKeyFile = *l.Digest.KeyFile
		}
		for name, p := range l.Presets {
			if err := p.validate(name); err != nil {
				return nil, fmt.Errorf("home config: %w", err)
			}
			home[name] = p
		}
	}

	if in.Repo != nil {
		if len(in.Repo) > ConfigFileBytes {
			return nil, refusef("repository config is larger than %d bytes", ConfigFileBytes)
		}
		l, md, err := decodeLayer(in.Repo, "repository config")
		if err != nil {
			return nil, &Refusal{Reason: err.Error()}
		}
		if err := checkRepoKeys(md); err != nil {
			return nil, err
		}
		if err := apply(&r, l, SourceRepo); err != nil {
			return nil, err
		}
		r.RepoLayerHash = hashJSON(l)
	}

	if in.Flags.Gate != "" {
		if err := checkCommand("--gate", "gate.command", in.Flags.Gate); err != nil {
			return nil, err
		}
		r.Gate.Command = in.Flags.Gate
		r.Sources.Gate = SourceFlag
	}
	if in.Flags.Builder != "" {
		r.Builder.Preset = in.Flags.Builder
		r.Sources.Builder = SourceFlag
	}

	p, err := resolvePreset(r.Builder.Preset, r.Sources.Builder, embedded, home)
	if err != nil {
		return nil, err
	}
	r.Builder.Command = append([]string(nil), p.Command...)
	r.Builder.Prompt = p.Prompt
	r.Builder.Cost = p.Cost

	if err := validate(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func layerName(src Source) string {
	switch src {
	case SourceHome:
		return "home config"
	case SourceRepo:
		return "repository config"
	default:
		return string(src)
	}
}

// apply copies one decoded layer onto r, checking caps as values arrive.
func apply(r *Resolved, l *layer, src Source) error {
	who := layerName(src)
	if l.Gate.Command != nil {
		if err := checkCommand(who, "gate.command", *l.Gate.Command); err != nil {
			return err
		}
		r.Gate.Command = *l.Gate.Command
		r.Sources.Gate = src
	}
	if l.Gate.Prepare != nil {
		if err := checkCommand(who, "gate.prepare", *l.Gate.Prepare); err != nil {
			return err
		}
		r.Gate.Prepare = *l.Gate.Prepare
		r.Sources.Prepare = src
	}
	r.Gate.PrepareInputs = mergeList(r.Gate.PrepareInputs, l.Gate.PrepareInputs)
	if l.Gate.Minutes != nil {
		if err := checkRange(who, "gate.minutes", int64(*l.Gate.Minutes), int64(r.Caps.GateMinutes)); err != nil {
			return err
		}
		r.Gate.Minutes = *l.Gate.Minutes
	}
	if l.Gate.PrepareMinutes != nil {
		if err := checkRange(who, "gate.prepare_minutes", int64(*l.Gate.PrepareMinutes), int64(r.Caps.GatePrepareMinutes)); err != nil {
			return err
		}
		r.Gate.PrepareMinutes = *l.Gate.PrepareMinutes
	}
	if l.Gate.Isolated != nil {
		r.Gate.Isolated = *l.Gate.Isolated
	}
	r.Gate.Protected = mergeList(r.Gate.Protected, l.Gate.Protected)

	if l.Builder.Preset != nil {
		r.Builder.Preset = *l.Builder.Preset
		r.Sources.Builder = src
	}

	if v := l.Limits.CommitStallMinutes; v != nil {
		if err := checkRange(who, "limits.commit_stall_minutes", int64(*v), int64(r.Caps.CommitStallMinutes)); err != nil {
			return err
		}
		r.Limits.CommitStallMinutes = *v
	}
	if v := l.Limits.WallClockMinutes; v != nil {
		if err := checkRange(who, "limits.wall_clock_minutes", int64(*v), int64(r.Caps.WallClockMinutes)); err != nil {
			return err
		}
		r.Limits.WallClockMinutes = *v
	}
	if v := l.Limits.GitTimeoutSeconds; v != nil {
		if err := checkRange(who, "limits.git_timeout_seconds", int64(*v), int64(r.Caps.GitTimeoutSeconds)); err != nil {
			return err
		}
		r.Limits.GitTimeoutSeconds = *v
	}
	if v := l.Limits.OutputMiB; v != nil {
		if err := checkRange(who, "limits.output_mib", int64(*v), r.Caps.OutputBytes>>20); err != nil {
			return err
		}
		r.Limits.OutputBytes = int64(*v) << 20
	}

	// Bound each layer's own list before merging, so duplicates that the
	// merge drops still count against the limit.
	if n := len(l.Condense.Patterns); n > MaxPatterns {
		return refusef("%s sets condense.patterns with %d entries, above the limit %d", who, n, MaxPatterns)
	}
	if n := len(l.Redaction.Patterns); n > MaxPatterns {
		return refusef("%s sets redaction.patterns with %d entries, above the limit %d", who, n, MaxPatterns)
	}
	r.Condense = mergeList(r.Condense, l.Condense.Patterns)
	r.Redaction = mergeList(r.Redaction, l.Redaction.Patterns)

	if l.Contract.CommitStyle != nil {
		r.Contract.CommitStyle = *l.Contract.CommitStyle
	}
	if l.Contract.Additions != nil {
		// An empty value is not local, so a layer that sets it empty is
		// refused here; validate cannot tell empty from unset.
		if !filepath.IsLocal(*l.Contract.Additions) {
			return refusef("%s sets contract.additions %q, which is not a local relative path", who, *l.Contract.Additions)
		}
		r.Contract.Additions = *l.Contract.Additions
	}
	return nil
}

// lowerCaps applies the home [caps] table. A cap may only go down, and the
// defaults that sat at the old cap follow it down.
func lowerCaps(r *Resolved, c capsLayer) error {
	lower := func(key string, v *int, cur *int, builtin int) error {
		if v == nil {
			return nil
		}
		if *v < 1 {
			return fmt.Errorf("home config sets caps.%s = %d; a cap must be at least 1", key, *v)
		}
		if *v > builtin {
			return fmt.Errorf("home config sets caps.%s = %d, above the built-in cap %d", key, *v, builtin)
		}
		*cur = *v
		return nil
	}
	if err := lower("commit_stall_minutes", c.CommitStallMinutes, &r.Caps.CommitStallMinutes, BuiltinCaps.CommitStallMinutes); err != nil {
		return err
	}
	if err := lower("wall_clock_minutes", c.WallClockMinutes, &r.Caps.WallClockMinutes, BuiltinCaps.WallClockMinutes); err != nil {
		return err
	}
	if err := lower("gate_minutes", c.GateMinutes, &r.Caps.GateMinutes, BuiltinCaps.GateMinutes); err != nil {
		return err
	}
	if err := lower("gate_prepare_minutes", c.GatePrepareMinutes, &r.Caps.GatePrepareMinutes, BuiltinCaps.GatePrepareMinutes); err != nil {
		return err
	}
	if err := lower("git_timeout_seconds", c.GitTimeoutSeconds, &r.Caps.GitTimeoutSeconds, BuiltinCaps.GitTimeoutSeconds); err != nil {
		return err
	}
	if err := lower("retention_days", c.RetentionDays, &r.Caps.RetentionDays, BuiltinCaps.RetentionDays); err != nil {
		return err
	}
	if c.OutputMiB != nil {
		mib := int(BuiltinCaps.OutputBytes >> 20)
		var cur int
		if err := lower("output_mib", c.OutputMiB, &cur, mib); err != nil {
			return err
		}
		r.Caps.OutputBytes = int64(cur) << 20
	}
	// Pull every default down to the new caps.
	r.Limits.CommitStallMinutes = min(r.Limits.CommitStallMinutes, r.Caps.CommitStallMinutes)
	r.Limits.WallClockMinutes = min(r.Limits.WallClockMinutes, r.Caps.WallClockMinutes)
	r.Limits.GitTimeoutSeconds = min(r.Limits.GitTimeoutSeconds, r.Caps.GitTimeoutSeconds)
	r.Limits.OutputBytes = min(r.Limits.OutputBytes, r.Caps.OutputBytes)
	r.Gate.Minutes = min(r.Gate.Minutes, r.Caps.GateMinutes)
	r.Gate.PrepareMinutes = min(r.Gate.PrepareMinutes, r.Caps.GatePrepareMinutes)
	r.RetentionDays = min(r.RetentionDays, r.Caps.RetentionDays)
	return nil
}

// checkRange refuses a value below 1 or above its cap, naming all three.
func checkRange(who, key string, value, cap int64) error {
	if value < 1 {
		return refusef("%s sets %s = %d; the value must be at least 1", who, key, value)
	}
	if value > cap {
		return refusef("%s sets %s = %d, above the cap %d", who, key, value, cap)
	}
	return nil
}

// checkCommand refuses an empty or oversized shell command.
func checkCommand(who, key, v string) error {
	if strings.TrimSpace(v) == "" {
		return refusef("%s sets %s to an empty command", who, key)
	}
	if len(v) > MaxCommandBytes {
		return refusef("%s sets %s to %d bytes, above the limit %d", who, key, len(v), MaxCommandBytes)
	}
	return nil
}

// mergeList appends the entries of add that base does not already hold,
// keeping order. Lists are never replaced by a later layer.
func mergeList(base, add []string) []string {
	seen := make(map[string]bool, len(base)+len(add))
	out := make([]string, 0, len(base)+len(add))
	for _, s := range base {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range add {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// validate checks the merged value: a gate exists, paths are local, the
// commit style is known, and the pattern lists are bounded and compile.
func validate(r *Resolved) error {
	if r.Gate.Command == "" {
		return refusef("no gate command is configured; set gate.command in %s or pass --gate", RepoFile)
	}
	if r.Contract.CommitStyle != "conventional" && r.Contract.CommitStyle != "any" {
		return refusef("contract.commit_style must be \"conventional\" or \"any\", got %q", r.Contract.CommitStyle)
	}
	for _, p := range r.Gate.PrepareInputs {
		if !filepath.IsLocal(p) {
			return refusef("gate.prepare_inputs entry %q is not a local relative path", p)
		}
	}
	for _, p := range r.Gate.Protected {
		if !filepath.IsLocal(p) {
			return refusef("gate.protected entry %q is not a local relative path", p)
		}
	}
	if r.Contract.Additions != "" && !filepath.IsLocal(r.Contract.Additions) {
		return refusef("contract.additions %q is not a local relative path", r.Contract.Additions)
	}
	if err := checkPatterns("condense.patterns", r.Condense); err != nil {
		return err
	}
	return checkPatterns("redaction.patterns", r.Redaction)
}

func checkPatterns(key string, patterns []string) error {
	if len(patterns) > MaxPatterns {
		return refusef("%s has %d entries, above the limit %d", key, len(patterns), MaxPatterns)
	}
	for _, p := range patterns {
		if len(p) > MaxPatternBytes {
			return refusef("%s entry is %d bytes, above the limit %d", key, len(p), MaxPatternBytes)
		}
		if _, err := regexp.Compile(p); err != nil {
			return refusef("%s entry %q does not compile: %v", key, p, err)
		}
	}
	return nil
}

// RepoFile is the repository configuration path, relative to the top level.
const RepoFile = ".vdispatch.toml"
