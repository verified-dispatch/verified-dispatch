package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const homeOK = `
[gate]
command = "make test"
prepare = "make deps"
prepare_inputs = ["go.sum"]
protected = ["Makefile"]

[condense]
patterns = ["^ok "]

[redaction]
patterns = ["home-secret-[0-9]+"]

[retention]
days = 7

[digest]
key_file = "/var/keys/vd.key"

[presets.open]
command = ["open-tool", "--yes"]
prompt = "arg:{prompt_file}"
repo_selectable = true

[presets.closed]
command = ["closed-tool"]
prompt = "stdin"
`

const repoOK = `
[gate]
command = "ruff check . && pytest -q"
prepare_inputs = ["uv.lock", "go.sum"]
minutes = 15
protected = ["conftest.py", "Makefile"]

[builder]
preset = "open"

[limits]
commit_stall_minutes = 10

[condense]
patterns = ["^ok ", "^passed"]

[contract]
commit_style = "any"
additions = "docs/rules.md"
`

func mustLoad(t *testing.T, in Inputs) *Resolved {
	t.Helper()
	r, err := Load(in)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func wantRefusal(t *testing.T, in Inputs, mention string) {
	t.Helper()
	_, err := Load(in)
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want *Refusal mentioning %q", err, mention)
	}
	if !strings.Contains(r.Reason, mention) {
		t.Fatalf("reason = %q, want it to mention %q", r.Reason, mention)
	}
}

func wantPlainError(t *testing.T, in Inputs, mention string) {
	t.Helper()
	_, err := Load(in)
	var r *Refusal
	if err == nil || errors.As(err, &r) || !strings.Contains(err.Error(), mention) {
		t.Fatalf("err = %v, want a plain error mentioning %q", err, mention)
	}
}

func TestLoadWithNothingUsesDefaultsAndNeedsAGate(t *testing.T) {
	wantRefusal(t, Inputs{}, "no gate command")
	r := mustLoad(t, Inputs{Flags: Flags{Gate: "make test"}})
	if r.Gate.Command != "make test" || r.Sources.Gate != SourceFlag {
		t.Fatalf("gate = %+v sources = %+v", r.Gate, r.Sources)
	}
	if r.Builder.Preset != "claude-code" || r.Builder.Command[0] != "claude" || r.Sources.Builder != SourceBuiltin {
		t.Fatalf("builder = %+v sources = %+v", r.Builder, r.Sources)
	}
	if r.RepoLayerHash != "" {
		t.Fatal("RepoLayerHash set without a repository config")
	}
}

func TestLoadLayersOverrideInOrderAndMergeLists(t *testing.T) {
	r := mustLoad(t, Inputs{Home: []byte(homeOK), Repo: []byte(repoOK)})
	if r.Gate.Command != "ruff check . && pytest -q" || r.Sources.Gate != SourceRepo {
		t.Fatalf("gate command = %q from %s", r.Gate.Command, r.Sources.Gate)
	}
	if r.Gate.Prepare != "make deps" || r.Sources.Prepare != SourceHome {
		t.Fatalf("prepare = %q from %s", r.Gate.Prepare, r.Sources.Prepare)
	}
	if got := strings.Join(r.Gate.PrepareInputs, ","); got != "go.sum,uv.lock" {
		t.Fatalf("prepare_inputs = %q, want home first then new repo entries", got)
	}
	if got := strings.Join(r.Gate.Protected, ","); got != "Makefile,conftest.py" {
		t.Fatalf("protected = %q", got)
	}
	if got := strings.Join(r.Condense, ","); got != "^ok ,^passed" {
		t.Fatalf("condense = %q", got)
	}
	if len(r.Redaction) != 1 || r.Redaction[0] != "home-secret-[0-9]+" {
		t.Fatalf("redaction = %q", r.Redaction)
	}
	if r.Gate.Minutes != 15 || r.Gate.PrepareMinutes != 30 || !r.Gate.Isolated {
		t.Fatalf("gate = %+v", r.Gate)
	}
	if r.Builder.Preset != "open" || r.Builder.Command[0] != "open-tool" || r.Sources.Builder != SourceRepo {
		t.Fatalf("builder = %+v from %s", r.Builder, r.Sources.Builder)
	}
	if r.Limits.CommitStallMinutes != 10 || r.Limits.WallClockMinutes != 90 {
		t.Fatalf("limits = %+v", r.Limits)
	}
	if r.Contract.CommitStyle != "any" || r.Contract.Additions != "docs/rules.md" {
		t.Fatalf("contract = %+v", r.Contract)
	}
	if r.RetentionDays != 7 || r.DigestKeyFile != "/var/keys/vd.key" {
		t.Fatalf("retention = %d key = %q", r.RetentionDays, r.DigestKeyFile)
	}
	if r.RepoLayerHash == "" || len(r.RepoLayerHash) != 64 {
		t.Fatalf("RepoLayerHash = %q", r.RepoLayerHash)
	}
}

func TestLoadFlagsOverrideEverything(t *testing.T) {
	r := mustLoad(t, Inputs{Home: []byte(homeOK), Repo: []byte(repoOK), Flags: Flags{Gate: "go test ./...", Builder: "closed"}})
	if r.Gate.Command != "go test ./..." || r.Sources.Gate != SourceFlag {
		t.Fatalf("gate = %q from %s", r.Gate.Command, r.Sources.Gate)
	}
	if r.Builder.Preset != "closed" || r.Sources.Builder != SourceFlag {
		t.Fatalf("builder = %q from %s", r.Builder.Preset, r.Sources.Builder)
	}
}

func TestLoadRepoLayerHashTracksTheWholeRepoLayer(t *testing.T) {
	a := mustLoad(t, Inputs{Repo: []byte(repoOK), Home: []byte(homeOK)})
	b := mustLoad(t, Inputs{Repo: []byte(strings.Replace(repoOK, `protected = ["conftest.py", "Makefile"]`, `protected = ["conftest.py"]`, 1)), Home: []byte(homeOK)})
	c := mustLoad(t, Inputs{Repo: []byte(repoOK), Home: []byte(strings.Replace(homeOK, "days = 7", "days = 9", 1))})
	if a.RepoLayerHash == b.RepoLayerHash {
		t.Fatal("a protected path change did not change RepoLayerHash")
	}
	if a.RepoLayerHash != c.RepoLayerHash {
		t.Fatal("a home change altered RepoLayerHash")
	}
}

func TestLoadRefusesValuesAboveCapsAndBelowOne(t *testing.T) {
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[limits]\nwall_clock_minutes = 120\n")}, "limits.wall_clock_minutes = 120, above the cap 90")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\nminutes = 45\n")}, "gate.minutes = 45, above the cap 30")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[limits]\ngit_timeout_seconds = 601\n")}, "above the cap 600")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[limits]\ncommit_stall_minutes = 0\n")}, "must be at least 1")
	wantRefusal(t, Inputs{Home: []byte("[limits]\noutput_mib = 51\n"), Flags: Flags{Gate: "x"}}, "limits.output_mib = 51, above the cap 50")
	wantRefusal(t, Inputs{Home: []byte("[retention]\ndays = 31\n"), Flags: Flags{Gate: "x"}}, "retention.days = 31, above the cap 30")
}

func TestLoadHomeCapsOnlyLower(t *testing.T) {
	home := "[caps]\nwall_clock_minutes = 60\ngate_minutes = 10\n"
	r := mustLoad(t, Inputs{Home: []byte(home), Flags: Flags{Gate: "x"}})
	if r.Caps.WallClockMinutes != 60 || r.Caps.GateMinutes != 10 || r.Caps.CommitStallMinutes != 20 {
		t.Fatalf("caps = %+v", r.Caps)
	}
	if r.Limits.WallClockMinutes != 60 || r.Gate.Minutes != 10 {
		t.Fatalf("defaults were not pulled down to the lowered caps: %+v %+v", r.Limits, r.Gate)
	}
	wantRefusal(t, Inputs{Home: []byte(home), Repo: []byte("[gate]\ncommand = \"x\"\n[limits]\nwall_clock_minutes = 61\n")}, "above the cap 60")
	wantPlainError(t, Inputs{Home: []byte("[caps]\nwall_clock_minutes = 91\n"), Flags: Flags{Gate: "x"}}, "above the built-in cap")
	wantPlainError(t, Inputs{Home: []byte("[caps]\nwall_clock_minutes = 0\n"), Flags: Flags{Gate: "x"}}, "at least 1")
}

func TestLoadRefusesRepositoryEscalations(t *testing.T) {
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[builder]\ncommand = [\"evil\"]\n")}, "builder.command")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[presets.evil]\ncommand = [\"evil\"]\nprompt = \"stdin\"\n")}, "may not define presets")
	wantRefusal(t, Inputs{Home: []byte(homeOK), Repo: []byte("[gate]\ncommand = \"x\"\n[builder]\npreset = \"closed\"\n")}, "repo_selectable")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[builder]\npreset = \"ghost\"\n")}, "not defined")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\nprotected = [\"../etc/passwd\"]\n")}, "gate.protected")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\nprepare_inputs = [\"/abs\"]\n")}, "gate.prepare_inputs")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[contract]\nadditions = \"\"\n")}, "contract.additions")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[contract]\ncommit_style = \"loose\"\n")}, "commit_style")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[redaction]\npatterns = [\"(\"]\n")}, "does not compile")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"   \"\n")}, "empty command")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"" + strings.Repeat("x", MaxCommandBytes+1) + "\"\n")}, "gate.command")
	wantPlainError(t, Inputs{Repo: []byte("[gate\ncommand = \"x\"\n")}, "repository config")
	wantRefusal(t, Inputs{Repo: make([]byte, ConfigFileBytes+1)}, "larger than")
	many := "[gate]\ncommand = \"x\"\n[condense]\npatterns = [" + strings.TrimSuffix(strings.Repeat("\"p\",", MaxPatterns+1), ",") + "]\n"
	wantRefusal(t, Inputs{Repo: []byte(many)}, "condense.patterns")
}

func TestLoadHomeFileProblemsAreErrors(t *testing.T) {
	wantPlainError(t, Inputs{Home: []byte("[gate\n"), Flags: Flags{Gate: "x"}}, "home config")
	wantPlainError(t, Inputs{Home: []byte("[gate]\ncolour = \"red\"\n"), Flags: Flags{Gate: "x"}}, "unknown key gate.colour")
	wantPlainError(t, Inputs{Home: []byte("[presets.bad]\ncommand = []\nprompt = \"stdin\"\n"), Flags: Flags{Gate: "x"}}, "no command")
	wantPlainError(t, Inputs{Home: []byte("[digest]\nkey_file = \"relative.key\"\n"), Flags: Flags{Gate: "x"}}, "digest.key_file")
	wantPlainError(t, Inputs{Home: []byte("[builder]\npreset = \"ghost\"\n"), Flags: Flags{Gate: "x"}}, "not defined")
	wantPlainError(t, Inputs{Home: make([]byte, ConfigFileBytes+1), Flags: Flags{Gate: "x"}}, "larger than")
}

func TestLoadDedupesMergedLists(t *testing.T) {
	r := mustLoad(t, Inputs{
		Home: []byte("[gate]\ncommand = \"x\"\nprotected = [\"a\", \"b\", \"a\"]\n"),
		Repo: []byte("[gate]\nprotected = [\"b\", \"c\"]\n"),
	})
	if got := strings.Join(r.Gate.Protected, ","); got != "a,b,c" {
		t.Fatalf("protected = %q, want a,b,c", got)
	}
}

func TestLoadRefusesControlCharactersInCommands(t *testing.T) {
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"make test\\u001b[2K\\rmake evil\"\n")}, "repository config sets gate.command with a control character")
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\nprepare = \"a\\nb\"\n")}, "gate.prepare with a control character")
	wantRefusal(t, Inputs{Home: []byte("[gate]\ncommand = \"a\\u0085b\"\n")}, "home config sets gate.command with a control character")
	wantRefusal(t, Inputs{Flags: Flags{Gate: "a\x7fb"}}, "--gate sets gate.command with a control character")
	// A tab is ordinary whitespace in a shell command.
	r := mustLoad(t, Inputs{Repo: []byte("[gate]\ncommand = \"make\\ttest\"\n")})
	if r.Gate.Command != "make\ttest" {
		t.Fatalf("command = %q", r.Gate.Command)
	}
}

func TestLoadRefusesBadPathsPerLayer(t *testing.T) {
	repo := func(body string) Inputs { return Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n" + body)} }
	wantRefusal(t, repo("protected = [\"../etc/passwd\"]\n"), "repository config sets gate.protected entry \"../etc/passwd\", which is not a local relative path")
	wantRefusal(t, repo("prepare_inputs = [\"/abs\"]\n"), "repository config sets gate.prepare_inputs entry")
	wantRefusal(t, repo("protected = [\".git/config\"]\n"), "names a .git component")
	wantRefusal(t, repo("protected = [\"sub/.GIT/hooks\"]\n"), "names a .git component")
	wantRefusal(t, repo("protected = [\"a//b\"]\n"), "is not in clean form")
	wantRefusal(t, repo("protected = [\"./a\"]\n"), "is not in clean form")
	wantRefusal(t, repo("protected = [\"a/\"]\n"), "is not in clean form")
	wantRefusal(t, repo("protected = [\"a\\u0000b\"]\n"), "control character")
	wantRefusal(t, repo("protected = [\"a\\u001bb\"]\n"), "control character")
	wantRefusal(t, repo("protected = [\"\"]\n"), "empty entry")
	wantRefusal(t, repo("protected = [\""+strings.Repeat("a", MaxPathBytes+1)+"\"]\n"), "above the limit")
	many := "protected = [" + strings.TrimSuffix(strings.Repeat("\"p\",", MaxPathEntries+1), ",") + "]\n"
	wantRefusal(t, repo(many), "gate.protected with "+fmt.Sprint(MaxPathEntries+1)+" entries")
	wantRefusal(t, repo("[contract]\nadditions = \".\"\n"), "contract.additions")
	wantRefusal(t, repo("[contract]\nadditions = \".vdispatch.toml\"\n"), "may not name a .vdispatch file")
	wantRefusal(t, repo("[contract]\nadditions = \"docs/.vdispatch-rules.md\"\n"), "may not name a .vdispatch file")
	wantRefusal(t, repo("prepare_inputs = [\".vdispatch.toml\"]\n"), "may not name a .vdispatch file")
	wantRefusal(t, Inputs{Home: []byte("[gate]\ncommand = \"x\"\nprotected = [\"../x\"]\n")}, "home config sets gate.protected entry")
	// gate.protected may name the repository config; that is useful.
	r := mustLoad(t, repo("protected = [\".vdispatch.toml\", \"Makefile\", \"docs/.gitkeep\"]\n"))
	if len(r.Gate.Protected) != 3 {
		t.Fatalf("protected = %q", r.Gate.Protected)
	}
}

func TestLoadRefusesBadCommitStylePerLayer(t *testing.T) {
	wantRefusal(t, Inputs{Repo: []byte("[gate]\ncommand = \"x\"\n[contract]\ncommit_style = \"loose\"\n")}, "repository config sets contract.commit_style")
	wantRefusal(t, Inputs{Home: []byte("[contract]\ncommit_style = \"loose\"\n"), Flags: Flags{Gate: "x"}}, "home config sets contract.commit_style")
}

func TestLoadHashIsTheSameWithNoHomeFileAndAnEmptyOne(t *testing.T) {
	a := mustLoad(t, Inputs{Flags: Flags{Gate: "x"}})
	b := mustLoad(t, Inputs{Home: []byte(""), Flags: Flags{Gate: "x"}})
	if a.Hash() != b.Hash() {
		t.Fatal("an empty home file changed the hash of an otherwise equal configuration")
	}
	for name, l := range map[string][]string{"prepare_inputs": a.Gate.PrepareInputs, "protected": a.Gate.Protected, "condense": a.Condense, "redaction": a.Redaction} {
		if l == nil {
			t.Errorf("%s is nil; lists must be empty slices so the hash is stable", name)
		}
	}
}

func TestLoadRepoLayerHashCoversEveryRepositoryKey(t *testing.T) {
	base := mustLoad(t, Inputs{Repo: []byte(repoOK), Home: []byte(homeOK)}).RepoLayerHash
	variants := map[string]string{
		"isolated":  strings.Replace(repoOK, "minutes = 15", "minutes = 15\nisolated = false", 1),
		"limits":    strings.Replace(repoOK, "commit_stall_minutes = 10", "commit_stall_minutes = 11", 1),
		"additions": strings.Replace(repoOK, "docs/rules.md", "docs/other.md", 1),
		"patterns":  strings.Replace(repoOK, `"^passed"`, `"^PASS"`, 1),
		"preset":    strings.Replace(repoOK, `preset = "open"`, `preset = "claude-code"`, 1),
		"command":   strings.Replace(repoOK, "pytest -q", "pytest", 1),
		"inputs":    strings.Replace(repoOK, `"uv.lock"`, `"poetry.lock"`, 1),
	}
	for name, src := range variants {
		if src == repoOK {
			t.Fatalf("variant %s did not change the fixture", name)
		}
		got := mustLoad(t, Inputs{Repo: []byte(src), Home: []byte(homeOK)}).RepoLayerHash
		if got == base {
			t.Errorf("changing %s did not change RepoLayerHash", name)
		}
	}
	// Comments and table form do not change the decoded layer.
	same := mustLoad(t, Inputs{Repo: []byte("# a comment\n" + repoOK), Home: []byte(homeOK)}).RepoLayerHash
	if same != base {
		t.Fatal("a comment changed RepoLayerHash")
	}
}

func TestLoadHomePresetPinsTheBuilder(t *testing.T) {
	home := "[builder]\npreset = \"claude-code\"\n"
	repo := "[gate]\ncommand = \"x\"\n[builder]\npreset = \"codex\"\n"
	r := mustLoad(t, Inputs{Home: []byte(home), Repo: []byte(repo)})
	if r.Builder.Preset != "claude-code" || r.Sources.Builder != SourceHome {
		t.Fatalf("builder = %q from %s; the home choice must stand", r.Builder.Preset, r.Sources.Builder)
	}
	// A repository preset the home config ignores is not resolved, so it
	// need not exist.
	r = mustLoad(t, Inputs{Home: []byte(home), Repo: []byte(strings.Replace(repo, "codex", "ghost", 1))})
	if r.Builder.Preset != "claude-code" {
		t.Fatalf("builder = %q", r.Builder.Preset)
	}
	// With no home choice the repository may pick an embedded preset.
	r = mustLoad(t, Inputs{Repo: []byte(repo)})
	if r.Builder.Preset != "codex" || r.Sources.Builder != SourceRepo {
		t.Fatalf("builder = %q from %s", r.Builder.Preset, r.Sources.Builder)
	}
	// A flag beats both.
	r = mustLoad(t, Inputs{Home: []byte(home), Repo: []byte(repo), Flags: Flags{Builder: "codex"}})
	if r.Builder.Preset != "codex" || r.Sources.Builder != SourceFlag {
		t.Fatalf("builder = %q from %s", r.Builder.Preset, r.Sources.Builder)
	}
	// The ignored repository choice still changes the repository layer hash.
	a := mustLoad(t, Inputs{Home: []byte(home), Repo: []byte(repo)})
	b := mustLoad(t, Inputs{Home: []byte(home), Repo: []byte("[gate]\ncommand = \"x\"\n")})
	if a.RepoLayerHash == b.RepoLayerHash {
		t.Fatal("an ignored builder.preset did not change RepoLayerHash")
	}
}
