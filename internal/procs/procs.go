package procs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
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
	tr        *tracker // platform tracking of the child and its descendants
	obs       *observer
	closeOnce sync.Once
}

// live is the registry of children this package started and has not torn
// down. A scan for one child never attributes another live child, or a
// process in another live child's session, and never reaps a live child.
var live = struct {
	sync.Mutex
	m map[int]bool
}{m: map[int]bool{}}

func liveHas(pid int) bool {
	live.Lock()
	defer live.Unlock()
	return live.m[pid]
}

// Start runs spec in a new session and process group. It returns once the
// child has started. The caller must end with Teardown, or Close when it
// has waited and has nothing to tear down.
func Start(spec Spec) (*Process, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("procs: empty argv")
	}
	if spec.Dir != "" {
		if _, err := os.Stat(spec.Dir); err != nil {
			return nil, fmt.Errorf("procs: directory %s: %w", spec.Dir, err)
		}
	}
	if err := ensureSubreaper(); err != nil {
		return nil, err
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
		// The registry lock is held from the fork until the pid is
		// registered. A scan checks the registry only after it has read
		// the process table, so a child it saw there is registered by
		// then and is never attributed to another child or reaped.
		live.Lock()
		err := cmd.Start()
		if err == nil {
			live.m[cmd.Process.Pid] = true
		}
		live.Unlock()
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
	// The tracker reads the child's start time before anything can reap
	// it: the wait goroutine has not started yet.
	p.tr = newTracker(p)
	go func() {
		err := cmd.Wait()
		p.status, p.waitErr = statusOf(cmd.ProcessState, err)
		close(p.done)
		close(p.release)
	}()
	p.obs = startObserver(p)
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

// observer refreshes the child's tree every second so processes are
// tracked while their parent chain still exists. Without it a process
// whose parent exits between two listings would be seen only through the
// subreaper rule on Linux, and not at all on macOS.
type observer struct {
	stop    chan struct{}
	stopped chan struct{}
}

// pollInterval is how often the observer scans. Containment states it.
const pollInterval = time.Second

func startObserver(p *Process) *observer {
	o := &observer{stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(o.stopped)
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		_, _, _ = scanFn(p)
		for {
			select {
			case <-o.stop:
				return
			case <-t.C:
				_, _, _ = scanFn(p)
			}
		}
	}()
	return o
}

// Close stops the observer and removes the child from the registry. It is
// safe to call more than once. Teardown calls it.
func (p *Process) Close() {
	p.closeOnce.Do(func() {
		if p.obs != nil {
			close(p.obs.stop)
			<-p.obs.stopped
		}
		live.Lock()
		delete(live.m, p.pid)
		live.Unlock()
	})
}

// liveProc is one running process found by a scan.
type liveProc struct {
	pid  int
	pgid int
}

// scanFn is the platform scan, replaced in tests that simulate a listing
// failure. It returns the running processes attributed to the child with
// their group ids, how many zombies it reaped, and an error when the
// process table could not be read.
var scanFn = func(p *Process) ([]liveProc, int, error) { return p.tr.scan(p) }

// Descendants lists the running processes attributed to the child, by
// pid, sorted. The child itself is not listed. An error means the process
// table could not be read; callers must treat that as unknown, never as
// empty.
func (p *Process) Descendants() ([]int, error) {
	alive, _, err := scanFn(p)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(alive))
	for _, lp := range alive {
		out = append(out, lp.pid)
	}
	sort.Ints(out)
	return out, nil
}

// childDone reports whether the child has ended: waited for, or shown by
// the process table as exited or gone. The second form matters during the
// pipe cut after exit, when Wait has not returned yet.
func (p *Process) childDone() bool {
	return p.Exited() || !p.tr.childAlive()
}
