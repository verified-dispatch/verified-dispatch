// Package gitx is the only way the tool invokes git. Every command runs with
// inherited GIT_* variables cleared, the fixed environment and -c settings
// from the design's section 5, the pinned repository directories, an
// attribute source, and a timeout.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// FixedEnv is appended to the environment of every git command.
var FixedEnv = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_NO_REPLACE_OBJECTS=1",
	"GIT_NO_LAZY_FETCH=1",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_TERMINAL_PROMPT=0",
}

// FixedConfig is passed as -c settings to every git command.
var FixedConfig = []string{
	"core.fsmonitor=",
	"core.hooksPath=/dev/null",
	"core.untrackedCache=false",
	"status.showUntrackedFiles=all",
	"core.quotePath=false",
	"core.commitGraph=false",
	"core.multiPackIndex=false",
}

// DefaultTimeout bounds one git command when Runner.Timeout is zero.
const DefaultTimeout = 120 * time.Second

// ErrTimeout is returned when a git command exceeds its timeout.
var ErrTimeout = errors.New("gitx: git command timed out")

// Runner carries the pinned directories for one repository. The zero value
// runs git from Dir with no pinning, which is only right for the first
// rev-parse that discovers the directories.
type Runner struct {
	// Dir is the working directory the command starts in.
	Dir string
	// GitDir, WorkTree, and CommonDir pin the repository. Empty means unset.
	GitDir, WorkTree, CommonDir string
	// AttrSource is the tree-ish attributes are read from. Empty means unset.
	AttrSource string
	// Timeout bounds the command. Zero means DefaultTimeout.
	Timeout time.Duration
}

// BaseEnv returns the current environment without any GIT_* variable, with
// FixedEnv appended.
func BaseEnv() []string {
	env := make([]string, 0, len(os.Environ())+len(FixedEnv))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, FixedEnv...)
}

func (r Runner) args(sub []string) []string {
	a := make([]string, 0, 3+2*len(FixedConfig)+len(sub))
	if r.GitDir != "" {
		a = append(a, "--git-dir="+r.GitDir)
	}
	if r.WorkTree != "" {
		a = append(a, "--work-tree="+r.WorkTree)
	}
	if r.AttrSource != "" {
		a = append(a, "--attr-source="+r.AttrSource)
	}
	for _, c := range FixedConfig {
		a = append(a, "-c", c)
	}
	return append(a, sub...)
}

// Run executes one git command and returns its stdout. A nonzero exit
// returns an error that names the subcommand and carries git's stderr. A
// timeout returns an error wrapping ErrTimeout.
func (r Runner) Run(ctx context.Context, sub ...string) ([]byte, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", r.args(sub)...)
	cmd.Dir = r.Dir
	cmd.Env = BaseEnv()
	if r.CommonDir != "" {
		cmd.Env = append(cmd.Env, "GIT_COMMON_DIR="+r.CommonDir)
	}
	// Children that inherit stdout must not keep Wait blocked past the
	// deadline; WaitDelay closes the pipes after the process is killed.
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w after %s: git %s", ErrTimeout, timeout, strings.Join(sub, " "))
	}
	if err != nil {
		return nil, fmt.Errorf("gitx: git %s: %w: %s", strings.Join(sub, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
