package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initRepo creates a repository with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "-q", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
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
	r := Runner{Dir: elsewhere, GitDir: filepath.Join(repo, ".git"), WorkTree: repo}
	out, err := r.Run(context.Background(), "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, _ := filepath.EvalSymlinks(repo)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
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
	for _, want := range FixedEnv {
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
func fakeGitOnPath(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
