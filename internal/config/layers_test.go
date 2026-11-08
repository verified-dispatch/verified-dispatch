package config

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeLayerReadsEveryTable(t *testing.T) {
	src := `
[gate]
command = "make test"
prepare = "make deps"
prepare_inputs = ["go.sum"]
minutes = 15
prepare_minutes = 5
isolated = false
protected = ["Makefile"]

[builder]
preset = "codex"

[limits]
commit_stall_minutes = 10
wall_clock_minutes = 60
git_timeout_seconds = 300
output_mib = 10

[caps]
wall_clock_minutes = 60

[condense]
patterns = ["^ok"]

[redaction]
patterns = ["ghp_[A-Za-z0-9]+"]

[contract]
commit_style = "any"
additions = "docs/rules.md"

[retention]
days = 7

[digest]
key_file = "/home/u/.key"

[presets.mine]
command = ["mine", "--yes"]
prompt = "stdin"
repo_selectable = true
`
	l, md, err := decodeLayer([]byte(src), "test")
	if err != nil {
		t.Fatal(err)
	}
	if u := unknownKeys(md); len(u) != 0 {
		t.Fatalf("unknown keys: %v", u)
	}
	if *l.Gate.Command != "make test" || *l.Gate.Minutes != 15 || *l.Gate.Isolated || l.Gate.Protected[0] != "Makefile" {
		t.Fatalf("gate = %+v", l.Gate)
	}
	if *l.Builder.Preset != "codex" || *l.Limits.OutputMiB != 10 || *l.Caps.WallClockMinutes != 60 {
		t.Fatalf("builder/limits/caps decoded wrong")
	}
	if l.Condense.Patterns[0] != "^ok" || l.Redaction.Patterns[0] != "ghp_[A-Za-z0-9]+" || *l.Contract.CommitStyle != "any" || *l.Retention.Days != 7 || *l.Digest.KeyFile != "/home/u/.key" {
		t.Fatalf("lists/contract/retention/digest decoded wrong")
	}
	if p, ok := l.Presets["mine"]; !ok || p.Command[1] != "--yes" || !p.RepoSelectable {
		t.Fatalf("presets = %+v", l.Presets)
	}
}

func TestDecodeLayerLeavesUnsetKeysNil(t *testing.T) {
	l, _, err := decodeLayer([]byte("[gate]\ncommand = \"x\"\n"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if l.Gate.Minutes != nil || l.Gate.Isolated != nil || l.Builder.Preset != nil || l.Retention.Days != nil {
		t.Fatal("an unset key decoded as a non-nil pointer")
	}
}

func TestDecodeLayerReportsUnknownKeys(t *testing.T) {
	_, md, err := decodeLayer([]byte("[gate]\ncommand = \"x\"\ncolour = \"red\"\n[limits]\nbogus = 1\n"), "test")
	if err != nil {
		t.Fatal(err)
	}
	u := unknownKeys(md)
	if len(u) != 2 || u[0] != "gate.colour" || u[1] != "limits.bogus" {
		t.Fatalf("unknown = %v, want [gate.colour limits.bogus]", u)
	}
	// An unknown table is reported too, by its own name or its keys.
	_, md, err = decodeLayer([]byte("[extra]\nk = 1\n"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if u := unknownKeys(md); len(u) == 0 || !strings.HasPrefix(u[0], "extra") {
		t.Fatalf("unknown = %v, want an entry starting with extra", u)
	}
}

func TestDecodeLayerRejectsWrongTypes(t *testing.T) {
	if _, _, err := decodeLayer([]byte("[gate]\nminutes = \"ten\"\n"), "test"); err == nil {
		t.Fatal("a string in an int key decoded without error")
	}
}

func TestCheckRepoKeysAllowsEveryListedKey(t *testing.T) {
	src := `
[gate]
command = "x"
prepare = "y"
prepare_inputs = ["a"]
minutes = 1
prepare_minutes = 1
isolated = true
protected = ["b"]
[builder]
preset = "claude-code"
[limits]
commit_stall_minutes = 1
wall_clock_minutes = 1
git_timeout_seconds = 1
[condense]
patterns = ["c"]
[redaction]
patterns = ["d"]
[contract]
commit_style = "any"
additions = "e"
`
	_, md, err := decodeLayer([]byte(src), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRepoKeys(md); err != nil {
		t.Fatalf("allowed keys were refused: %v", err)
	}
}

func TestCheckRepoKeysRefusesEverythingElse(t *testing.T) {
	cases := []struct{ src, want string }{
		{"[builder]\ncommand = [\"evil\"]\n", "builder.command"},
		{"[presets.x]\ncommand = [\"evil\"]\nprompt = \"stdin\"\n", "may not define presets"},
		{"[limits]\noutput_mib = 1\n", "limits.output_mib"},
		// The table key comes first in document order, so the refusal
		// names the table.
		{"[caps]\nwall_clock_minutes = 1\n", "caps"},
		{"[retention]\ndays = 1\n", "retention"},
		{"[digest]\nkey_file = \"/x\"\n", "digest"},
		{"[gate]\ncolour = \"red\"\n", "gate.colour"},
		{"top = 1\n", "top"},
	}
	for _, c := range cases {
		_, md, err := decodeLayer([]byte(c.src), "test")
		if err != nil {
			t.Fatalf("%q: decode: %v", c.src, err)
		}
		err = checkRepoKeys(md)
		var r *Refusal
		if !errors.As(err, &r) {
			t.Fatalf("%q: err = %v, want *Refusal", c.src, err)
		}
		if !strings.Contains(r.Reason, c.want) {
			t.Fatalf("%q: reason = %q, want it to mention %q", c.src, r.Reason, c.want)
		}
	}
}
