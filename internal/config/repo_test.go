package config

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/verified-dispatch/verified-dispatch/internal/gitx"
)

// hostGit runs git for fixtures with the host's configuration and inherited
// GIT_* variables removed.
func hostGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitx.BaseEnv(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func repoWithHead(t *testing.T) (string, gitx.Runner) {
	t.Helper()
	dir := t.TempDir()
	hostGit(t, dir, "init", "-q", "-b", "main")
	hostGit(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	r := gitx.Runner{Dir: dir, GitDir: filepath.Join(dir, ".git"), WorkTree: dir, CommonDir: filepath.Join(dir, ".git")}
	return dir, r
}

func TestReadRepoBlobAbsent(t *testing.T) {
	_, r := repoWithHead(t)
	data, present, err := ReadRepoBlob(context.Background(), r)
	if err != nil || present || data != nil {
		t.Fatalf("absent: %q, %v, %v", data, present, err)
	}
}

func TestReadRepoBlobReadsHeadNotDisk(t *testing.T) {
	dir, r := repoWithHead(t)
	if err := os.WriteFile(filepath.Join(dir, RepoFile), []byte("[gate]\ncommand = \"committed\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostGit(t, dir, "add", RepoFile)
	hostGit(t, dir, "commit", "-q", "-m", "add config")
	// A later edit on disk must not be what the tool reads.
	if err := os.WriteFile(filepath.Join(dir, RepoFile), []byte("[gate]\ncommand = \"on disk\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, present, err := ReadRepoBlob(context.Background(), r)
	if err != nil || !present {
		t.Fatalf("present: %v, %v", present, err)
	}
	if string(data) != "[gate]\ncommand = \"committed\"\n" {
		t.Fatalf("data = %q, want the HEAD blob", data)
	}
}

func TestReadRepoBlobRefusesSymlinkAndTree(t *testing.T) {
	dir, r := repoWithHead(t)
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, RepoFile)); err != nil {
		t.Fatal(err)
	}
	hostGit(t, dir, "add", RepoFile)
	hostGit(t, dir, "commit", "-q", "-m", "symlink")
	_, _, err := ReadRepoBlob(context.Background(), r)
	var ref *Refusal
	if err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink: err = %v, want a plain error about a symbolic link", err)
	}

	dir2, r2 := repoWithHead(t)
	if err := os.MkdirAll(filepath.Join(dir2, RepoFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, RepoFile, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostGit(t, dir2, "add", RepoFile)
	hostGit(t, dir2, "commit", "-q", "-m", "tree")
	if _, _, err := ReadRepoBlob(context.Background(), r2); err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "tree") {
		t.Fatalf("tree: err = %v, want a plain error about a tree", err)
	}
}

func TestReadRepoBlobRefusesOversizedBlob(t *testing.T) {
	dir, r := repoWithHead(t)
	if err := os.WriteFile(filepath.Join(dir, RepoFile), make([]byte, ConfigFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	hostGit(t, dir, "add", RepoFile)
	hostGit(t, dir, "commit", "-q", "-m", "big")
	_, _, err := ReadRepoBlob(context.Background(), r)
	var ref *Refusal
	if err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized: err = %v, want a plain error about the size", err)
	}
}

func TestReadRepoBlobReadsTheTopLevelFromASubdirectory(t *testing.T) {
	dir, r := repoWithHead(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RepoFile), []byte("[gate]\ncommand = \"top\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", RepoFile), []byte("[gate]\ncommand = \"sub\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostGit(t, dir, "add", "-A")
	hostGit(t, dir, "commit", "-q", "-m", "two configs")
	r.Dir = filepath.Join(dir, "sub")
	data, present, err := ReadRepoBlob(context.Background(), r)
	if err != nil || !present {
		t.Fatalf("present: %v, %v", present, err)
	}
	if string(data) != "[gate]\ncommand = \"top\"\n" {
		t.Fatalf("data = %q, want the top-level config", data)
	}
}
