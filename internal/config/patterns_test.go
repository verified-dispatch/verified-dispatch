package config

import (
	"strings"
	"testing"
)

func TestPatternCostCountsInstructions(t *testing.T) {
	small, err := patternCost("^ok ")
	if err != nil || small < 1 || small > 50 {
		t.Fatalf("small pattern: cost %d, err %v", small, err)
	}
	big, err := patternCost(strings.Repeat(`[^\x00]{1000}`, 39))
	if err != nil {
		t.Fatalf("big pattern did not compile: %v", err)
	}
	if big <= MaxPatternInstructions {
		t.Fatalf("big pattern cost %d, expected it above %d", big, MaxPatternInstructions)
	}
	if _, err := patternCost("("); err == nil {
		t.Fatal("unbalanced pattern compiled")
	}
}

func TestCheckPatternNamesLayerAndKey(t *testing.T) {
	cases := []struct{ pattern, mention string }{
		{strings.Repeat("x", MaxPatternBytes+1), "bytes, above the limit"},
		{"(", "does not compile"},
		{strings.Repeat(`[^\x00]{1000}`, 39), "instructions, above the limit"},
		{"", "matches the empty string"},
		{"(?s).*", "matches the empty string"},
		{"a*", "matches the empty string"},
		{"^", "matches the empty string"},
	}
	for _, c := range cases {
		err := checkPattern("repository config", "condense.patterns", c.pattern)
		if err == nil {
			t.Errorf("checkPattern(%q) = nil, want a refusal mentioning %q", c.pattern, c.mention)
			continue
		}
		if !strings.Contains(err.Error(), c.mention) || !strings.Contains(err.Error(), "repository config") || !strings.Contains(err.Error(), "condense.patterns") {
			t.Errorf("checkPattern(%q) = %q, want it to mention %q, the layer, and the key", c.pattern, err, c.mention)
		}
	}
	for _, p := range []string{"^ok ", "ghp_[A-Za-z0-9]{36}", "a+", "secret-[0-9]+"} {
		if err := checkPattern("home config", "redaction.patterns", p); err != nil {
			t.Errorf("checkPattern(%q) = %v, want nil", p, err)
		}
	}
}

func TestLoadBoundsTotalPatternCost(t *testing.T) {
	// Each pattern is under the per-pattern bound; together they exceed the
	// run bound.
	one := strings.Repeat(`[^\x00]{100}`, 15) // about 1500 instructions
	if cost, err := patternCost(one); err != nil || cost > MaxPatternInstructions {
		t.Fatalf("fixture pattern cost %d, err %v; want under %d", cost, err, MaxPatternInstructions)
	}
	var ps []string
	for i := 0; i < 20; i++ {
		ps = append(ps, `"`+one+strings.Repeat("a", i)+`"`)
	}
	src := "[gate]\ncommand = \"x\"\n[condense]\npatterns = [" + strings.Join(ps, ",") + "]\n"
	wantRefusal(t, Inputs{Repo: []byte(src)}, "across all patterns")
}

func TestLoadRefusesEmptyMatchAndCostlyPatternsPerLayer(t *testing.T) {
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[redaction]\npatterns = [\"\"]\n")}, "repository config sets redaction.patterns")
	wantRefusal(t, Inputs{Home: []byte("[condense]\npatterns = [\"(?s).*\"]\n"), Flags: Flags{Gate: "x"}}, "home config sets condense.patterns")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[condense]\npatterns = [\"" + strings.Repeat(`[^\x00]{1000}`, 39) + "\"]\n")}, "instructions")
}
