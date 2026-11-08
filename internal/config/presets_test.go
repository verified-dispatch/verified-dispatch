package config

import (
	"errors"
	"strings"
	"testing"
)

func TestEmbeddedPresetsLoadAndValidate(t *testing.T) {
	p, err := embeddedPresets()
	if err != nil {
		t.Fatal(err)
	}
	cc, ok := p["claude-code"]
	if !ok || cc.Command[0] != "claude" || cc.Prompt != PromptStdin || cc.Cost != "claude-code-json" || !cc.RepoSelectable {
		t.Fatalf("claude-code = %+v", cc)
	}
	cx, ok := p["codex"]
	if !ok || cx.Command[0] != "codex" || cx.Prompt != PromptStdin || cx.Cost != "" || !cx.RepoSelectable {
		t.Fatalf("codex = %+v", cx)
	}
}

func TestPresetValidation(t *testing.T) {
	cases := []struct {
		name string
		p    Preset
		want string
	}{
		{"nocmd", Preset{Prompt: PromptStdin}, "no command"},
		{"emptyarg", Preset{Command: []string{"x", ""}, Prompt: PromptStdin}, "empty argument"},
		{"badprompt", Preset{Command: []string{"x"}, Prompt: "file"}, "prompt must be"},
		{"argnoplaceholder", Preset{Command: []string{"x"}, Prompt: "arg:--p"}, "prompt must be"},
		{"badcost", Preset{Command: []string{"x"}, Prompt: PromptStdin, Cost: "magic"}, "unknown cost extractor"},
	}
	for _, c := range cases {
		err := c.p.validate(c.name)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
	good := Preset{Command: []string{"x"}, Prompt: "arg:{prompt_file}", Cost: "claude-code-json"}
	if err := good.validate("good"); err != nil {
		t.Fatalf("valid preset rejected: %v", err)
	}
}

func TestResolvePresetTrustRules(t *testing.T) {
	embedded := map[string]Preset{"claude-code": {Command: []string{"claude"}, Prompt: PromptStdin, RepoSelectable: true}}
	home := map[string]Preset{
		"open":   {Command: []string{"open"}, Prompt: PromptStdin, RepoSelectable: true},
		"closed": {Command: []string{"closed"}, Prompt: PromptStdin},
		// A home preset that replaces the embedded one by name wins.
		"claude-code": {Command: []string{"claude", "--mine"}, Prompt: PromptStdin, RepoSelectable: true},
	}
	isRefusal := func(err error) bool { var r *Refusal; return errors.As(err, &r) }

	if p, err := resolvePreset("claude-code", SourceRepo, embedded, home); err != nil || p.Command[1] != "--mine" {
		t.Fatalf("home replacement not used: %+v, %v", p, err)
	}
	if p, err := resolvePreset("claude-code", SourceBuiltin, embedded, map[string]Preset{}); err != nil || p.Command[0] != "claude" {
		t.Fatalf("embedded default: %+v, %v", p, err)
	}
	if _, err := resolvePreset("open", SourceRepo, embedded, home); err != nil {
		t.Fatalf("repo_selectable home preset refused: %v", err)
	}
	if _, err := resolvePreset("closed", SourceRepo, embedded, home); !isRefusal(err) || !strings.Contains(err.Error(), "repo_selectable") {
		t.Fatalf("closed home preset from repo: err = %v, want a refusal naming repo_selectable", err)
	}
	if _, err := resolvePreset("closed", SourceHome, embedded, home); err != nil {
		t.Fatalf("home may name its own closed preset: %v", err)
	}
	if _, err := resolvePreset("closed", SourceFlag, embedded, home); err != nil {
		t.Fatalf("a flag may name a closed preset: %v", err)
	}
	if _, err := resolvePreset("nope", SourceRepo, embedded, home); !isRefusal(err) {
		t.Fatalf("unknown preset from repo: err = %v, want *Refusal", err)
	}
	if _, err := resolvePreset("nope", SourceFlag, embedded, home); !isRefusal(err) || !strings.Contains(err.Error(), "--builder") {
		t.Fatalf("unknown preset from flag: err = %v, want a refusal naming --builder", err)
	}
	if _, err := resolvePreset("nope", SourceHome, embedded, home); err == nil || isRefusal(err) {
		t.Fatalf("unknown preset from home: err = %v, want a plain error", err)
	}
}
