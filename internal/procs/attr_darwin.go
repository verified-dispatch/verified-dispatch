//go:build darwin

package procs

import (
	"errors"
	"fmt"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
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

// DisableAttach asks the kernel to refuse debugger attachment and stops
// core dumps. If a debugger is attached already the kernel ends the
// process, which is the right outcome for a tool that holds a key in
// memory.
func DisableAttach() error {
	if err := unix.PtraceDenyAttach(); err != nil {
		return fmt.Errorf("procs: refuse attachment: %w", err)
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

// procInfo is one entry of the kernel's process table.
type procInfo struct {
	pid, ppid, pgid int
	zombie          bool
	start           uint64 // start time in microseconds; with the pid it names one process
}

// szomb is the p_stat value of a zombie, from <sys/proc.h>.
const szomb = 5

// fromKinfo converts the kernel's entries.
func fromKinfo(kp []unix.KinfoProc) map[int]procInfo {
	procs := make(map[int]procInfo, len(kp))
	for _, k := range kp {
		pid := int(k.Proc.P_pid)
		procs[pid] = procInfo{
			pid:    pid,
			ppid:   int(k.Eproc.Ppid),
			pgid:   int(k.Eproc.Pgid),
			zombie: k.Proc.P_stat == szomb,
			start:  uint64(k.Proc.P_starttime.Sec)*1_000_000 + uint64(k.Proc.P_starttime.Usec),
		}
	}
	return procs
}

// snapshot reads the whole process table from the kernel, with no fork
// and no dependence on PATH.
func snapshot() (map[int]procInfo, error) {
	kp, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("procs: kern.proc.all: %w", err)
	}
	return fromKinfo(kp), nil
}

// tracker follows one child and its descendants by pid and start time.
type tracker struct {
	mu      sync.Mutex
	pid     int
	start   uint64 // the child's start time in microseconds
	known   bool
	tracked map[int]uint64 // descendants seen so far, by pid and start time
}

func newTracker(p *Process) *tracker {
	t := &tracker{pid: p.pid, tracked: map[int]uint64{}}
	if procs, err := snapshot(); err == nil {
		if info, ok := procs[p.pid]; ok {
			t.start, t.known = info.start, true
		}
	}
	return t
}

func (t *tracker) childAlive() bool {
	procs, err := snapshot()
	if err != nil {
		return false
	}
	info, ok := procs[t.pid]
	return ok && t.known && info.start == t.start && !info.zombie
}

// scan builds the child's tree from one snapshot: the child while it is
// the same process, every tracked process still the same process, every
// process in the child's group while the pid is not held by a different
// process, every process in the child's session while the child is alive,
// then everything below them by parent chain. The group rule must outlive
// the child because nothing reparents an orphan to the tool on macOS, and
// it is safe because the kernel does not reuse a pid while a process group
// with that id exists. macOS reaps orphans itself, so nothing is reaped
// here.
func (t *tracker) scan(p *Process) ([]liveProc, int, error) {
	procs, err := snapshot()
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
	sameChild := childOK && t.known && child.start == t.start
	childAlive := sameChild && !child.zombie
	// The group id outlives the child: the kernel does not reuse a pid
	// while a process group with that id exists. So members of the
	// child's group are its own as long as the pid is not held by a
	// different process. The session rule is kept to a live child.
	groupRule := sameChild || !childOK
	if sameChild {
		seed(p.pid)
	}
	for pid, start := range t.tracked {
		if pi, ok := procs[pid]; ok && pi.start == start {
			seed(pid)
		} else {
			delete(t.tracked, pid)
		}
	}
	for pid, pi := range procs {
		if groupRule && pi.pgid == p.pid {
			seed(pid)
			continue
		}
		if childAlive {
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
	procs, err := snapshot()
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
