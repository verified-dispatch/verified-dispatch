// Package gitx is the only way the tool invokes git. Every command runs with
// inherited GIT_* variables cleared, global configuration disabled, a fixed
// set of environment variables and -c settings, pinned repository
// directories, an attribute source, a timeout, an output cap, and its own
// process group so that nothing it starts outlives it.
//
// Repository-level configuration ($GIT_DIR/config and $GIT_DIR/info) is
// still read. The tool trusts it at baseline and detects any later change
// before it runs a command that could act on it.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// FixedEnv is appended to the environment of every git command. A later
// entry wins over an inherited one with the same name.
var FixedEnv = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_GLOBAL=/dev/null",
	"GIT_NO_REPLACE_OBJECTS=1",
	"GIT_NO_LAZY_FETCH=1",
	// Paths this tool passes to git are literal, never pathspec syntax
	// such as ":(glob)" or ":(top)".
	"GIT_LITERAL_PATHSPECS=1",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_TERMINAL_PROMPT=0",
	"LC_ALL=C",
}

// FixedConfig is passed as -c settings to every git command. A -c setting
// overrides the same key in any configuration file.
var FixedConfig = []string{
	"core.fsmonitor=",
	"core.hooksPath=/dev/null",
	"core.untrackedCache=false",
	"core.excludesFile=/dev/null",
	"core.attributesFile=/dev/null",
	"status.showUntrackedFiles=all",
	"core.quotePath=false",
	"core.commitGraph=false",
	"core.multiPackIndex=false",
	// Git may not start background maintenance from a command this tool
	// runs: it would detach into a new session, be attributed to the
	// running child, and change the object store during a run.
	"gc.auto=0",
	"maintenance.auto=false",
}

// DefaultTimeout bounds one git command when Runner.Timeout is zero.
const DefaultTimeout = 120 * time.Second

// DefaultMaxOutput caps stdout when Runner.MaxOutput is zero.
const DefaultMaxOutput = 64 << 20

// maxStderr caps stderr for every command.
const maxStderr = 1 << 20

// ErrTimeout is returned when a git command exceeds its timeout.
var ErrTimeout = errors.New("gitx: git command timed out")

// ErrOutputTooLarge is returned when a git command writes more than the
// output cap.
var ErrOutputTooLarge = errors.New("gitx: git output exceeded the cap")

// ErrPartialPin is returned when some but not all of GitDir, WorkTree, and
// CommonDir are set. A half-pinned command would let repository
// configuration such as core.worktree choose the rest.
var ErrPartialPin = errors.New("gitx: GitDir, WorkTree, and CommonDir must all be set or all be empty")

// Runner carries the pinned directories for one repository. The zero value
// runs git unpinned from Dir, which is only right for the first rev-parse
// that discovers the directories.
type Runner struct {
	// Dir is the working directory the command starts in.
	Dir string
	// GitDir, WorkTree, and CommonDir pin the repository. Set all or none.
	GitDir, WorkTree, CommonDir string
	// AttrSource is the tree-ish attributes are read from. Empty means unset.
	AttrSource string
	// Timeout bounds the command. Zero means DefaultTimeout.
	Timeout time.Duration
	// MaxOutput caps stdout in bytes. Zero means DefaultMaxOutput.
	MaxOutput int64
}

// gitPath resolves the git binary once per process so that a later change
// to a PATH directory cannot swap the binary between commands.
var gitPath = sync.OnceValues(func() (string, error) {
	return exec.LookPath("git")
})

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

func (r Runner) pinned() (bool, error) {
	set := 0
	for _, v := range []string{r.GitDir, r.WorkTree, r.CommonDir} {
		if v != "" {
			set++
		}
	}
	switch set {
	case 0:
		return false, nil
	case 3:
		return true, nil
	default:
		return false, ErrPartialPin
	}
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

// cappedBuffer collects output up to limit bytes and calls over once when a
// write would exceed it. Writes after that are dropped.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int64
	over  func()
	done  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.done {
		return len(p), nil
	}
	if int64(c.buf.Len())+int64(len(p)) > c.limit {
		c.done = true
		c.over()
		return len(p), nil
	}
	return c.buf.Write(p)
}

// Run executes one git command and returns its stdout.
//
// Errors: a timeout wraps ErrTimeout; output past the cap wraps
// ErrOutputTooLarge; cancellation of ctx wraps ctx's cause (for example
// context.Canceled); a nonzero exit returns an error that names the
// subcommand and carries git's stderr.
func (r Runner) Run(ctx context.Context, sub ...string) ([]byte, error) {
	pinned, err := r.pinned()
	if err != nil {
		return nil, err
	}
	git, err := gitPath()
	if err != nil {
		return nil, fmt.Errorf("gitx: %w", err)
	}

	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	maxOut := r.MaxOutput
	if maxOut == 0 {
		maxOut = DefaultMaxOutput
	}

	ctx, cancelTimeout := context.WithTimeoutCause(ctx, timeout, ErrTimeout)
	defer cancelTimeout()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	cmd := exec.CommandContext(ctx, git, r.args(sub)...)
	cmd.Dir = r.Dir
	cmd.Env = BaseEnv()
	if pinned {
		cmd.Env = append(cmd.Env, "GIT_COMMON_DIR="+r.CommonDir)
	}
	// Each command gets its own process group, and cancellation kills the
	// group, so helpers git starts (filters, ssh, credential programs)
	// die with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// A grandchild that inherits a pipe must not keep Wait blocked after
	// git itself exits; WaitDelay closes the pipes. Wait then reports
	// exec.ErrWaitDelay even though git succeeded, and Run kills the
	// group so the grandchild does not outlive the command.
	cmd.WaitDelay = time.Second

	over := func() { cancel(ErrOutputTooLarge) }
	stdout := &cappedBuffer{limit: maxOut, over: over}
	stderr := &cappedBuffer{limit: maxStderr, over: over}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	name := strings.Join(sub, " ")
	if err := cmd.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			if errors.Is(cause, ErrTimeout) {
				return nil, fmt.Errorf("%w after %s: git %s", ErrTimeout, timeout, name)
			}
			return nil, fmt.Errorf("gitx: git %s: %w", name, cause)
		}
		if errors.Is(err, exec.ErrWaitDelay) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return stdout.buf.Bytes(), nil
		}
		return nil, fmt.Errorf("gitx: git %s: %w: %s", name, err, strings.TrimSpace(stderr.buf.String()))
	}
	return stdout.buf.Bytes(), nil
}
