package config

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/verified-dispatch/verified-dispatch/internal/presets"
)

// knownCostExtractors names the extractors the receipt can run.
var knownCostExtractors = map[string]bool{"claude-code-json": true}

// validate checks a preset definition; name labels errors.
func (p Preset) validate(name string) error {
	if len(p.Command) == 0 {
		return fmt.Errorf("preset %q has no command", name)
	}
	for _, a := range p.Command {
		if a == "" {
			return fmt.Errorf("preset %q has an empty argument", name)
		}
	}
	switch {
	case p.Prompt == PromptStdin:
	case strings.HasPrefix(p.Prompt, promptArgPrefix) && strings.Contains(p.Prompt, PromptFilePlaceholder):
	default:
		return fmt.Errorf("preset %q: prompt must be %q or start with %q and contain %s", name, PromptStdin, promptArgPrefix, PromptFilePlaceholder)
	}
	if p.Cost != "" && !knownCostExtractors[p.Cost] {
		return fmt.Errorf("preset %q names unknown cost extractor %q", name, p.Cost)
	}
	return nil
}

// embeddedPresets decodes and validates the presets built into the binary.
func embeddedPresets() (map[string]Preset, error) {
	var l struct {
		Presets map[string]Preset `toml:"presets"`
	}
	if _, err := toml.Decode(string(presets.Embedded()), &l); err != nil {
		return nil, fmt.Errorf("embedded presets: %w", err)
	}
	for name, p := range l.Presets {
		if err := p.validate(name); err != nil {
			return nil, fmt.Errorf("embedded %w", err)
		}
	}
	return l.Presets, nil
}

// resolvePreset finds the preset named by a layer under the trust rules: a
// home preset wins over an embedded one of the same name; a repository may
// name an embedded preset or a home preset marked repo_selectable, and
// nothing else.
func resolvePreset(name string, src Source, embedded, home map[string]Preset) (Preset, error) {
	if p, ok := home[name]; ok {
		if src == SourceRepo && !p.RepoSelectable {
			return Preset{}, refusef("repository config names preset %q, which the home config does not mark repo_selectable", name)
		}
		return p, nil
	}
	if p, ok := embedded[name]; ok {
		return p, nil
	}
	switch src {
	case SourceRepo:
		return Preset{}, refusef("repository config names preset %q, which is not defined", name)
	case SourceFlag:
		return Preset{}, refusef("--builder names preset %q, which is not defined", name)
	default:
		return Preset{}, fmt.Errorf("%s config names preset %q, which is not defined", src, name)
	}
}
