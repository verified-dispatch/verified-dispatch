package config

import (
	"errors"
	"testing"
)

func TestArgvAppendsPromptFileForArgPresets(t *testing.T) {
	b := Builder{Command: []string{"tool", "--flag"}, Prompt: "arg:--prompt-file={prompt_file}"}
	got := b.Argv("/tmp/p.md")
	want := []string{"tool", "--flag", "--prompt-file=/tmp/p.md"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
	// The receiver's command must not be modified.
	if len(b.Command) != 2 {
		t.Fatalf("Command was modified: %q", b.Command)
	}
}

func TestArgvLeavesStdinPresetsAlone(t *testing.T) {
	b := Builder{Command: []string{"tool"}, Prompt: PromptStdin}
	if got := b.Argv("/tmp/p.md"); len(got) != 1 || got[0] != "tool" {
		t.Fatalf("argv = %q, want [tool]", got)
	}
}

func TestDefaultsMatchTheSpec(t *testing.T) {
	d := defaults()
	if d.Gate.Minutes != 30 || d.Gate.PrepareMinutes != 30 || !d.Gate.Isolated {
		t.Fatalf("gate defaults = %+v", d.Gate)
	}
	if d.Builder.Preset != "claude-code" {
		t.Fatalf("default preset = %q", d.Builder.Preset)
	}
	if d.Limits.CommitStallMinutes != 20 || d.Limits.WallClockMinutes != 90 || d.Limits.GitTimeoutSeconds != 120 || d.Limits.OutputBytes != 50<<20 {
		t.Fatalf("limits defaults = %+v", d.Limits)
	}
	if d.RetentionDays != 14 || d.Contract.CommitStyle != "conventional" {
		t.Fatalf("retention = %d, commit style = %q", d.RetentionDays, d.Contract.CommitStyle)
	}
	if d.Caps != BuiltinCaps {
		t.Fatalf("caps = %+v, want the built-in caps", d.Caps)
	}
	if d.Sources != (Sources{Gate: SourceBuiltin, Prepare: SourceBuiltin, Builder: SourceBuiltin}) {
		t.Fatalf("sources = %+v", d.Sources)
	}
}

func TestHashIsStableAndSensitive(t *testing.T) {
	a, b := defaults(), defaults()
	if a.Hash() != b.Hash() {
		t.Fatal("two equal values hashed differently")
	}
	b.Gate.Protected = []string{"Makefile"}
	if a.Hash() == b.Hash() {
		t.Fatal("a protected path change did not change the hash")
	}
}

func TestRefusalIsTypedAndNamesTheReason(t *testing.T) {
	err := refusef("repository config sets %s = %d, above the cap %d", "limits.wall_clock_minutes", 120, 90)
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %T, want *Refusal", err)
	}
	if r.Reason != "repository config sets limits.wall_clock_minutes = 120, above the cap 90" {
		t.Fatalf("reason = %q", r.Reason)
	}
}
