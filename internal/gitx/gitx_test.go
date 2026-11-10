package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hostGit runs git for test fixtures with the host's configuration and any
// inherited GIT_* variables removed, so the fixture is the same on every
// machine and inside a git hook.
func hostGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(BaseEnv(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// initRepo creates a repository with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hostGit(t, dir, "init", "-q", "-b", "main")
	hostGit(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	return dir
}

// pinnedRunner returns a fully pinned Runner for a repository created by
// initRepo, starting in dir.
func pinnedRunner(repo, dir string) Runner {
	return Runner{
		Dir:       dir,
		GitDir:    filepath.Join(repo, ".git"),
		WorkTree:  repo,
		CommonDir: filepath.Join(repo, ".git"),
	}
}

func TestRunClearsInheritedGitVariables(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	out, err := (Runner{Dir: repo}).Run(context.Background(), "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "true" {
		t.Fatalf("rev-parse = %q, want %q (GIT_DIR was not cleared)", got, "true")
	}
}

func TestRunAppliesFixedConfig(t *testing.T) {
	repo := initRepo(t)
	out, err := (Runner{Dir: repo}).Run(context.Background(), "config", "--get", "core.quotePath")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "false" {
		t.Fatalf("core.quotePath = %q, want %q", got, "false")
	}
}

func TestRunPinsGitDirAndWorkTree(t *testing.T) {
	repo := initRepo(t)
	elsewhere := t.TempDir()
	out, err := pinnedRunner(repo, elsewhere).Run(context.Background(), "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("toplevel = %q, want %q", got, want)
	}
}

func TestRunReturnsStderrInError(t *testing.T) {
	repo := initRepo(t)
	_, err := (Runner{Dir: repo}).Run(context.Background(), "rev-parse", "--verify", "refs/heads/nope")
	if err == nil {
		t.Fatal("Run: want an error for a missing ref")
	}
	if !strings.Contains(err.Error(), "rev-parse") {
		t.Fatalf("error = %q, want it to name the subcommand", err)
	}
}

func TestBaseEnvDropsGitVariablesAndAddsFixedOnes(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -v")
	t.Setenv("GIT_NO_LAZY_FETCH", "0")
	env := BaseEnv()
	for _, kv := range env {
		if kv == "GIT_SSH_COMMAND=ssh -v" || kv == "GIT_NO_LAZY_FETCH=0" {
			t.Fatalf("BaseEnv kept inherited %q", kv)
		}
	}
	for _, want := range append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_LITERAL_PATHSPECS=1", "LC_ALL=C"}, FixedEnv...) {
		found := false
		for _, kv := range env {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("BaseEnv lacks %q", want)
		}
	}
}

// fakeGitOnPath puts a `git` script first on PATH that execs the given body.
// The git binary is resolved once per process, so the helper also points the
// resolved path at the script until the test ends.
func fakeGitOnPath(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	saved := gitPath
	gitPath = func() (string, error) { return script, nil }
	t.Cleanup(func() { gitPath = saved })
}

func TestRunTimesOutAndReportsErrTimeout(t *testing.T) {
	fakeGitOnPath(t, "exec sleep 5")
	start := time.Now()
	_, err := (Runner{Timeout: 200 * time.Millisecond}).Run(context.Background(), "status")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Run took %s, want it to return near the timeout", took)
	}
}

func TestRunRejectsPartialPinning(t *testing.T) {
	repo := initRepo(t)
	for _, r := range []Runner{
		{Dir: repo, GitDir: filepath.Join(repo, ".git")},
		{Dir: repo, WorkTree: repo},
		{Dir: repo, CommonDir: filepath.Join(repo, ".git")},
		{Dir: repo, GitDir: filepath.Join(repo, ".git"), WorkTree: repo},
	} {
		if _, err := r.Run(context.Background(), "rev-parse", "HEAD"); !errors.Is(err, ErrPartialPin) {
			t.Errorf("%+v: err = %v, want ErrPartialPin", r, err)
		}
	}
}

func TestRunIgnoresGlobalConfigAndGlobalIgnores(t *testing.T) {
	repo := initRepo(t)
	home := t.TempDir()
	// A global config that hides every untracked file, and the XDG ignore
	// file that would do the same when no excludesFile is configured.
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\texcludesFile = "+filepath.Join(home, "ignore")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "ignore"), []byte("*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(home, ".config", "git")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "ignore"), []byte("*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := pinnedRunner(repo, repo).Run(context.Background(), "status", "--porcelain=v2", "-z")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(out), "new.txt") {
		t.Fatalf("status = %q, want it to list new.txt (a global ignore was honored)", out)
	}
}

func TestRunDoesNotRunFiltersFromWorkTreeOrConfiguredAttributes(t *testing.T) {
	repo := initRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	// A filter in repository config is trusted at baseline. It must not be
	// reachable through a work-tree .gitattributes when AttrSource names a
	// tree without one, nor through core.attributesFile.
	hostGit(t, repo, "config", "filter.probe.clean", "touch "+marker+"; cat")
	attrs := filepath.Join(t.TempDir(), "attributes")
	if err := os.WriteFile(attrs, []byte("* filter=probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostGit(t, repo, "config", "core.attributesFile", attrs)
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("* filter=probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "data.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := pinnedRunner(repo, repo)
	r.AttrSource = "HEAD"
	for _, sub := range [][]string{
		{"status", "--porcelain=v2", "-z"},
		{"add", "--intent-to-add", "data.txt"},
		{"diff", "--no-ext-diff", "--no-textconv", "--", "data.txt"},
	} {
		if _, err := r.Run(context.Background(), sub...); err != nil {
			t.Fatalf("Run %v: %v", sub, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter from a work-tree .gitattributes or core.attributesFile ran")
	}
}

func TestRunReportsParentCancellation(t *testing.T) {
	fakeGitOnPath(t, "exec sleep 5")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := (Runner{Dir: t.TempDir(), Timeout: 10 * time.Second}).Run(ctx, "version")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, must not be reported as a timeout", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancellation did not stop the command promptly")
	}
}

func TestRunKillsGrandchildrenOnTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeGitOnPath(t, "sleep 30 &\necho $! > "+pidFile+"\nwait")
	_, err := (Runner{Dir: t.TempDir(), Timeout: 200 * time.Millisecond}).Run(context.Background(), "version")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the fake git did not record its child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived the timeout", pid)
}

func TestRunReturnsWhenGrandchildHoldsStdout(t *testing.T) {
	fakeGitOnPath(t, "sleep 5 &\nexit 0")
	start := time.Now()
	if _, err := (Runner{Dir: t.TempDir(), Timeout: 10 * time.Second}).Run(context.Background(), "version"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("Run waited for a grandchild that held the pipe open")
	}
}

func TestRunCapsOutput(t *testing.T) {
	fakeGitOnPath(t, "exec head -c 200000 /dev/zero")
	_, err := (Runner{Dir: t.TempDir(), MaxOutput: 1000}).Run(context.Background(), "version")
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("err = %v, want ErrOutputTooLarge", err)
	}
}
