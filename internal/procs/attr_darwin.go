//go:build darwin

package procs

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// sysProcAttr puts the child in a new session and process group. macOS has
// no parent-death signal and no subreaper; descendants are observed every
// second instead.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// EnableSubreaper has no equivalent on macOS.
func EnableSubreaper() error { return nil }

func ensureSubreaper() error { return nil }

// ptDenyAttach is the ptrace request that refuses later attachment, from
// <sys/ptrace.h>.
const ptDenyAttach = 31

// DisableAttach asks the kernel to refuse debugger attachment and stops
// core dumps. The request goes through the raw system call interface,
// which Apple does not support; a later batch moves it to the C library.
func DisableAttach() error {
	if _, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptDenyAttach, 0, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("procs: ptrace(PT_DENY_ATTACH): %w", e)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		return fmt.Errorf("procs: disable core dumps: %w", err)
	}
	return nil
}

// Containment is the receipt line that says how children are contained on
// this platform and what is not covered.
func Containment() string {
	return "containment: session and process group, descendants observed every second (1 s poll); " +
		"not covered: work handed to a process outside the tool's view, a child that stops or kills the tool, " +
		"a process that leaves the child's group and whose parent exits between two polls"
}

// psInfo is one line of ps output.
type psInfo struct {
	pid, ppid, pgid int
	zombie          bool
	start           string // lstart; with the pid it names one process
}

// psTimeout bounds one ps run so a stuck ps cannot hold a teardown past
// its bound.
const psTimeout = 5 * time.Second

// psSnapshot lists every process through /bin/ps by absolute path, so a
// ps planted earlier on PATH is never run.
func psSnapshot() (map[int]psInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,pgid=,stat=,lstart=").Output()
	if err != nil {
		return nil, fmt.Errorf("procs: ps: %w", err)
	}
	return parsePs(out), nil
}

func parsePs(out []byte) map[int]psInfo {
	procs := map[int]psInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		procs[pid] = psInfo{pid: pid, ppid: ppid, pgid: pgid, zombie: strings.HasPrefix(f[3], "Z"), start: strings.Join(f[4:], " ")}
	}
	return procs
}

// tracker follows one child and its descendants by pid and start time.
type tracker struct {
	mu      sync.Mutex
	pid     int
	start   string
	known   bool
	tracked map[int]string
}

func newTracker(p *Process) *tracker {
	t := &tracker{pid: p.pid, tracked: map[int]string{}}
	if procs, err := psSnapshot(); err == nil {
		if info, ok := procs[p.pid]; ok {
			t.start, t.known = info.start, true
		}
	}
	return t
}

func (t *tracker) childAlive() bool {
	procs, err := psSnapshot()
	if err != nil {
		return false
	}
	info, ok := procs[t.pid]
	return ok && t.known && info.start == t.start && !info.zombie
}

// scan builds the child's tree from one ps snapshot: the child while it is
// the same process, every tracked process still the same process, every
// process in the child's group or session while the child is alive, then
// everything below them by parent chain. macOS reaps orphans itself, so
// nothing is reaped here.
func (t *tracker) scan(p *Process) ([]liveProc, int, error) {
	procs, err := psSnapshot()
	if err != nil {
		return nil, 0, err
	}
	if _, ok := procs[p.selfPid]; !ok {
		return nil, 0, errors.New("procs: own entry missing from the process table")
	}
	byParent := make(map[int][]int, len(procs))
	for pid, pi := range procs {
		byParent[pi.ppid] = append(byParent[pi.ppid], pid)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	in := map[int]bool{}
	var queue []int
	seed := func(pid int) {
		if !in[pid] && pid != p.selfPid {
			in[pid] = true
			queue = append(queue, pid)
		}
	}
	child, childOK := procs[p.pid]
	childAlive := childOK && t.known && child.start == t.start && !child.zombie
	if childOK && t.known && child.start == t.start {
		seed(p.pid)
	}
	for pid, start := range t.tracked {
		if pi, ok := procs[pid]; ok && pi.start == start {
			seed(pid)
		} else {
			delete(t.tracked, pid)
		}
	}
	if childAlive {
		for pid, pi := range procs {
			if pi.pgid == p.pid {
				seed(pid)
				continue
			}
			if sid, err := syscall.Getsid(pid); err == nil && sid == p.pid {
				seed(pid)
			}
		}
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range byParent[pid] {
			seed(c)
		}
	}

	var alive []liveProc
	for pid := range in {
		pi := procs[pid]
		if pid != p.pid {
			t.tracked[pid] = pi.start
		}
		if pid == p.pid || pi.zombie {
			continue
		}
		alive = append(alive, liveProc{pid: pid, pgid: pi.pgid})
	}
	return alive, 0, nil
}

// kill sends sig to pid only if it is still the tracked process.
func (t *tracker) kill(pid int, sig syscall.Signal) error {
	t.mu.Lock()
	start, ok := t.tracked[pid]
	t.mu.Unlock()
	if !ok {
		return syscall.ESRCH
	}
	procs, err := psSnapshot()
	if err != nil {
		return err
	}
	if info, present := procs[pid]; !present || info.start != start {
		return syscall.ESRCH
	}
	return syscall.Kill(pid, sig)
}

// killGroup signals the child's process group while the child is still
// the same process, so a reused pid is never a target.
func (t *tracker) killGroup(sig syscall.Signal) {
	if t.childAlive() {
		_ = syscall.Kill(-t.pid, sig)
	}
}
