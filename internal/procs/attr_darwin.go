//go:build darwin

package procs

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// sysProcAttr puts the child in a new session and process group. macOS has
// no parent-death signal and no subreaper; descendants are polled instead.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// ptDenyAttach is the ptrace request that refuses later attachment, from
// <sys/ptrace.h>.
const ptDenyAttach = 31

// EnableSubreaper has no equivalent on macOS. Descendants are observed by
// polling instead; see Containment.
func EnableSubreaper() error { return nil }

// DisableAttach stops a debugger from attaching to this process. If one is
// attached already the kernel ends the process, which is the right outcome
// for a tool that holds a key in memory.
func DisableAttach() error {
	_, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptDenyAttach, 0, 0, 0, 0, 0)
	if e != 0 {
		return fmt.Errorf("procs: ptrace(PT_DENY_ATTACH): %w", e)
	}
	return nil
}

// Containment is the receipt line that says how children are contained on
// this platform.
func Containment() string {
	return "containment: process group plus observed descendants (1 s poll)"
}

// psInfo is one line of ps output.
type psInfo struct {
	pid, ppid, pgid int
	zombie          bool
	start           string // lstart, used to tell a reused pid from the same process
}

// psSnapshot lists every process through ps, which is the portable way to
// read the process table on macOS without a C dependency.
func psSnapshot() (map[int]psInfo, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,pgid=,stat=,lstart=").Output()
	if err != nil {
		return nil, fmt.Errorf("procs: ps: %w", err)
	}
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
	return procs, nil
}

// platformState observes the child's descendants by polling, because
// macOS has no subreaper: a process that is orphaned goes to launchd and
// would otherwise be lost. Every process seen in the child's group or
// below it is remembered with its start time, so it can still be found and
// killed after its parent is gone.
type platformState struct {
	mu       sync.Mutex
	observed map[int]string
	stop     chan struct{}
	stopped  chan struct{}
}

// pollInterval is how often descendants are observed. The receipt line
// from Containment states it.
const pollInterval = time.Second

func startPlatform(p *Process) *platformState {
	s := &platformState{observed: map[int]string{}, stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(s.stopped)
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		s.poll(p)
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.poll(p)
			}
		}
	}()
	return s
}

// poll takes one snapshot and adds every process that belongs to the
// child: in its process group, below it by parent chain, or below a
// process already observed and still the same process.
func (s *platformState) poll(p *Process) {
	procs, err := psSnapshot()
	if err != nil {
		return
	}
	in := map[int]bool{p.pid: true}
	s.mu.Lock()
	for pid, start := range s.observed {
		if pi, ok := procs[pid]; ok && pi.start == start {
			in[pid] = true
		}
	}
	s.mu.Unlock()
	for changed := true; changed; {
		changed = false
		for pid, pi := range procs {
			if in[pid] || pid == p.selfPid {
				continue
			}
			if pi.pgid == p.pid || in[pi.ppid] {
				in[pid] = true
				changed = true
			}
		}
	}
	s.mu.Lock()
	for pid := range in {
		if pid == p.pid {
			continue
		}
		if pi, ok := procs[pid]; ok {
			s.observed[pid] = pi.start
		}
	}
	s.mu.Unlock()
}

// Descendants polls once more and lists every observed process that still
// exists with the same start time and is not a zombie. The child itself is
// not listed. A process that left the group between two polls and whose
// parent exited in the same gap is not found; Containment says so.
func (p *Process) Descendants() ([]int, error) {
	p.plat.poll(p)
	procs, err := psSnapshot()
	if err != nil {
		return nil, err
	}
	p.plat.mu.Lock()
	defer p.plat.mu.Unlock()
	var out []int
	for pid, start := range p.plat.observed {
		if pi, ok := procs[pid]; ok && pi.start == start && !pi.zombie {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out, nil
}

// Close stops the poller. It is safe to call more than once.
func (p *Process) Close() {
	p.closeOnce.Do(func() {
		close(p.plat.stop)
		<-p.plat.stopped
	})
}
