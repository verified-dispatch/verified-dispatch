package procs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Spec describes one child.
type Spec struct {
	// Argv is the command and its arguments. Argv[0] is resolved through
	// the tool's PATH when it has no slash.
	Argv []string
	// Dir is the working directory. Empty means the tool's.
	Dir string
	// Env is the whole environment. Nil means empty; nothing is inherited.
	Env []string
	// Stdin is fed to the child and then closed. Nil means /dev/null.
	Stdin io.Reader
	// Stdout and Stderr receive the child's output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Status is how a child ended.
type Status struct {
	ExitCode int
	Signaled bool
	Signal   syscall.Signal
}

func (s Status) String() string {
	if s.Signaled {
		return "signal " + s.Signal.String()
	}
	return fmt.Sprintf("exit %d", s.ExitCode)
}

// waitDelay bounds how long Wait waits for the stdout and stderr copies
// after the child exits. A grandchild that inherited a pipe keeps it open;
// after this delay the pipe is closed, the status is reported, and
// teardown finds the holder.
const waitDelay = 2 * time.Second

// Process is a started child.
type Process struct {
	cmd       *exec.Cmd
	pid       int
	selfPid   int
	startedAt time.Time
	done      chan struct{} // closed when the child has exited and Wait has run
	release   chan struct{} // closed to let the forking thread go
	status    Status
	waitErr   error
	plat      *platformState
	//lint:ignore U1000 only the darwin Close uses it; Linux has nothing to release.
	closeOnce sync.Once
}

// Start runs spec in a new session and process group. It returns once the
// child has started.
func Start(spec Spec) (*Process, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("procs: empty argv")
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	if cmd.Env == nil {
		// exec.Cmd treats a nil Env as "inherit"; this package never does.
		cmd.Env = []string{}
	}
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = waitDelay

	p := &Process{
		cmd:     cmd,
		selfPid: os.Getpid(),
		done:    make(chan struct{}),
		release: make(chan struct{}),
	}
	started := make(chan error, 1)
	go func() {
		// On Linux the parent-death signal is tied to the thread that
		// forked the child, not to the process. This goroutine pins its
		// thread and holds it until the child has exited, so the signal
		// means what the receipt says: the child dies when the tool dies.
		runtime.LockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-p.release
		}
		runtime.UnlockOSThread()
	}()
	if err := <-started; err != nil {
		return nil, fmt.Errorf("procs: start %s: %w", spec.Argv[0], err)
	}
	p.pid = cmd.Process.Pid
	p.startedAt = time.Now()
	p.plat = startPlatform(p)
	go func() {
		err := cmd.Wait()
		p.status, p.waitErr = statusOf(cmd.ProcessState, err)
		close(p.done)
		close(p.release)
	}()
	return p, nil
}

// statusOf turns the result of exec.Cmd.Wait into a Status. A nonzero exit
// and a cut-off pipe copy are statuses, not errors.
func statusOf(ps *os.ProcessState, err error) (Status, error) {
	if ps == nil {
		return Status{ExitCode: -1}, err
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return Status{Signaled: true, Signal: ws.Signal()}, nil
	}
	st := Status{ExitCode: ps.ExitCode()}
	var exit *exec.ExitError
	if err == nil || errors.Is(err, exec.ErrWaitDelay) || errors.As(err, &exit) {
		return st, nil
	}
	return st, err
}

// Pid is the child's process id, which is also its session and group id.
func (p *Process) Pid() int { return p.pid }

// StartedAt is when the child started.
func (p *Process) StartedAt() time.Time { return p.startedAt }

// Wait returns the child's status once it has exited, or ctx's error. It
// never kills anything; the caller tears down.
func (p *Process) Wait(ctx context.Context) (Status, error) {
	select {
	case <-p.done:
		return p.status, p.waitErr
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}

// Exited reports whether the child has exited and been waited for.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
