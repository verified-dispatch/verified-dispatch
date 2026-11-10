package config

import (
	"fmt"
	"path/filepath"
	"slices"
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
			return nil, fmt.Errorf("home config: unknown key %s", shortKey(u[0]))
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
			return nil, fmt.Errorf("repository config is larger than %d bytes", ConfigFileBytes)
		}
		l, md, err := decodeLayer(in.Repo, "repository config")
		if err != nil {
			return nil, err
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

// apply copies one decoded layer onto r. Every rule about a single value is
// checked here, against the layer that set it, so a refusal names the
// layer. Rules that span layers live in validate.
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
	if err := checkPathList(who, "gate.prepare_inputs", l.Gate.PrepareInputs, pathInput); err != nil {
		return err
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
	if err := checkPathList(who, "gate.protected", l.Gate.Protected, pathProtected); err != nil {
		return err
	}
	r.Gate.Protected = mergeList(r.Gate.Protected, l.Gate.Protected)

	if l.Builder.Preset != nil && !(src == SourceRepo && r.Sources.Builder == SourceHome) {
		// The builder command is the user's. A home choice stands against
		// a repository; a repository chooses only when the home config
		// names no preset. An ignored repository choice is still part of
		// the decoded layer and so of RepoLayerHash. The flag layer is
		// applied by Load and wins over both.
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

	if err := checkPatternList(who, "condense.patterns", l.Condense.Patterns); err != nil {
		return err
	}
	if err := checkPatternList(who, "redaction.patterns", l.Redaction.Patterns); err != nil {
		return err
	}
	r.Condense = mergeList(r.Condense, l.Condense.Patterns)
	r.Redaction = mergeList(r.Redaction, l.Redaction.Patterns)

	if l.Contract.CommitStyle != nil {
		if s := *l.Contract.CommitStyle; s != "conventional" && s != "any" {
			return refusef("%s sets contract.commit_style = %q; it must be \"conventional\" or \"any\"", who, s)
		}
		r.Contract.CommitStyle = *l.Contract.CommitStyle
	}
	if l.Contract.Additions != nil {
		if err := checkPath(who, "contract.additions", *l.Contract.Additions, pathInput); err != nil {
			return err
		}
		r.Contract.Additions = *l.Contract.Additions
	}
	return nil
}

// checkPatternList bounds one layer's own list before merging, so entries
// the merge drops as duplicates still count, and checks every entry.
func checkPatternList(who, key string, patterns []string) error {
	if n := len(patterns); n > MaxPatterns {
		return refusef("%s sets %s with %d entries, above the limit %d", who, key, n, MaxPatterns)
	}
	for _, p := range patterns {
		if err := checkPattern(who, key, p); err != nil {
			return err
		}
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
			return refusef("home config sets caps.%s = %d; a cap must be at least 1", key, *v)
		}
		if *v > builtin {
			return refusef("home config sets caps.%s = %d, above the built-in cap %d", key, *v, builtin)
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

// checkCommand refuses an empty or oversized shell command, and one that
// holds a control character other than tab. A control character in a
// command can rewrite the line that shows the user what will run.
func checkCommand(who, key, v string) error {
	if strings.TrimSpace(v) == "" {
		return refusef("%s sets %s to an empty command", who, key)
	}
	if len(v) > MaxCommandBytes {
		return refusef("%s sets %s to %d bytes, above the limit %d", who, key, len(v), MaxCommandBytes)
	}
	if hasControl(v, '\t') {
		return refusef("%s sets %s with a control character; only printable characters and tab are allowed", who, key)
	}
	return nil
}

// hasControl reports whether s holds a C0 or C1 control character or
// DEL, other than the allowed runes.
func hasControl(s string, allowed ...rune) bool {
	for _, r := range s {
		if r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
			continue
		}
		if slices.Contains(allowed, r) {
			continue
		}
		return true
	}
	return false
}

// pathKind says which extra rules a path list follows.
type pathKind int

const (
	// pathProtected is a path the gate must not change. It may name the
	// repository config.
	pathProtected pathKind = iota
	// pathInput is a file the tool reads or hands to the builder. It may
	// not be the top level or any .vdispatch file.
	pathInput
)

// checkPathList bounds one layer's own list before merging and checks
// every entry.
func checkPathList(who, key string, paths []string, kind pathKind) error {
	if n := len(paths); n > MaxPathEntries {
		return refusef("%s sets %s with %d entries, above the limit %d", who, key, n, MaxPathEntries)
	}
	for _, p := range paths {
		if err := checkPath(who, key, p, kind); err != nil {
			return err
		}
	}
	return nil
}

// checkPath refuses a path that is empty, oversized, holds a control
// character, leaves the working tree, is not in clean form, or names a
// .git component. Clean form means filepath.Clean leaves it unchanged, so
// a string match against a path git reports cannot miss. The .git check
// ignores case because some file systems do.
func checkPath(who, key, p string, kind pathKind) error {
	if p == "" {
		return refusef("%s sets %s with an empty entry", who, key)
	}
	if len(p) > MaxPathBytes {
		return refusef("%s sets %s with an entry of %d bytes, above the limit %d", who, key, len(p), MaxPathBytes)
	}
	if hasControl(p) {
		return refusef("%s sets %s with an entry that holds a control character", who, key)
	}
	if !filepath.IsLocal(p) {
		return refusef("%s sets %s entry %q, which is not a local relative path", who, key, p)
	}
	if filepath.Clean(p) != p {
		return refusef("%s sets %s entry %q, which is not in clean form (write it as %q)", who, key, p, filepath.Clean(p))
	}
	for _, part := range strings.Split(p, "/") {
		if strings.EqualFold(part, ".git") {
			return refusef("%s sets %s entry %q, which names a .git component", who, key, p)
		}
		if kind == pathInput && strings.HasPrefix(part, ".vdispatch") {
			return refusef("%s sets %s entry %q, which may not name a .vdispatch file", who, key, p)
		}
	}
	if kind == pathInput && p == "." {
		return refusef("%s sets %s to %q, which is the top level, not a file", who, key, p)
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

// validate checks what only the merged value can tell: a gate exists, the
// merged lists stay within bounds, and the patterns fit the run budget.
func validate(r *Resolved) error {
	if r.Gate.Command == "" {
		return refusef("no gate command is configured; set gate.command in %s or pass --gate", RepoFile)
	}
	for key, l := range map[string][]string{"gate.prepare_inputs": r.Gate.PrepareInputs, "gate.protected": r.Gate.Protected} {
		if len(l) > MaxPathEntries {
			return refusef("%s has %d entries after merging the layers, above the limit %d", key, len(l), MaxPathEntries)
		}
	}
	for key, l := range map[string][]string{"condense.patterns": r.Condense, "redaction.patterns": r.Redaction} {
		if len(l) > MaxPatterns {
			return refusef("%s has %d entries after merging the layers, above the limit %d", key, len(l), MaxPatterns)
		}
	}
	return checkPatternBudget(r.Condense, r.Redaction)
}
