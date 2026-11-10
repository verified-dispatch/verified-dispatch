//go:build linux

package procs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// sysProcAttr puts the child in a new session and process group and has
// the kernel send it SIGKILL when the thread that forked it dies, which
// Start keeps alive for the child's lifetime.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
}

// prctl options this package uses, from <linux/prctl.h>.
const (
	prGetDumpable       = 3
	prSetDumpable       = 4
	prSetChildSubreaper = 36
)

func prctl(option, arg uintptr) (uintptr, error) {
	r, _, e := syscall.Syscall(syscall.SYS_PRCTL, option, arg, 0)
	if e != 0 {
		return 0, fmt.Errorf("procs: prctl(%d): %w", option, e)
	}
	return r, nil
}

// EnableSubreaper makes this process the parent of every descendant that
// is orphaned below it, so a process the child left behind is visible as
// a direct child here instead of vanishing under init. Start calls it;
// callers may call it earlier.
func EnableSubreaper() error {
	_, err := prctl(prSetChildSubreaper, 1)
	return err
}

var subreaperOnce sync.Once
var subreaperErr error

func ensureSubreaper() error {
	subreaperOnce.Do(func() { subreaperErr = EnableSubreaper() })
	return subreaperErr
}

// DisableAttach stops a same-user debugger from attaching to this process
// and stops core dumps, so the per-run key and the resolved configuration
// held in memory cannot be read from outside. It fails when a debugger is
// attached already, because that link survives the flag. A host that lets
// a capable debugger attach regardless is outside this guard; the docs
// say so.
func DisableAttach() error {
	if _, err := prctl(prSetDumpable, 0); err != nil {
		return err
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		return fmt.Errorf("procs: disable core dumps: %w", err)
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("procs: read own status: %w", err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if v, ok := strings.CutPrefix(line, "TracerPid:"); ok {
			if strings.TrimSpace(v) != "0" {
				return errors.New("procs: a debugger is attached to this process")
			}
		}
	}
	return nil
}

// dumpable reads the flag DisableAttach clears. Tests use it.
func dumpable() (int, error) {
	r, err := prctl(prGetDumpable, 0)
	return int(r), err
}

// Containment is the receipt line that says how children are contained on
// this platform and what is not covered.
func Containment() string {
	return "containment: session and process group, child subreaper, parent-death signal, descendants observed every second; " +
		"not covered: work handed to a process outside the tool's view, a child that stops or kills the tool, " +
		"a process that changes its pid faster than the process table is read"
}

// procInfo is what /proc/<pid>/stat says about one process.
type procInfo struct {
	pid, ppid, pgid, sid int
	state                byte
	threads              int
	start                uint64 // clock ticks since boot; with the pid it names one process
}

// exited reports a true zombie: nothing of it runs. A process in state Z
// with more than one thread has ended only its main thread and is alive.
func (pi procInfo) exited() bool { return pi.state == 'Z' && pi.threads <= 1 }

// parseStat reads the fields after the command name. The name is in
// parentheses and may itself hold spaces or parentheses, so the split
// starts at the last closing parenthesis. Field numbers follow proc(5):
// after the name come state, ppid, pgrp, session (fields 3 to 6), then
// num_threads (20) and starttime (22).
func parseStat(pid int, b []byte) (procInfo, bool) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return procInfo{}, false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return procInfo{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	pgid, err2 := strconv.Atoi(f[2])
	sid, err3 := strconv.Atoi(f[3])
	threads, err4 := strconv.Atoi(f[17])
	start, err5 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(f[0]) != 1 {
		return procInfo{}, false
	}
	return procInfo{pid: pid, ppid: ppid, pgid: pgid, sid: sid, state: f[0][0], threads: threads, start: start}, true
}

func readStat(pid int) (procInfo, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procInfo{}, false
	}
	return parseStat(pid, b)
}

// snapshot reads every process in /proc. A process that exits between the
// directory listing and the read is skipped.
func snapshot() (map[int]procInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("procs: /proc: %w", err)
	}
	out := make(map[int]procInfo, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if info, ok := readStat(pid); ok {
			out[pid] = info
		}
	}
	return out, nil
}

// tracker follows one child and its descendants by pid and start time.
type tracker struct {
	mu      sync.Mutex
	pid     int            // the child's pid
	start   uint64         // the child's start time; zero when it was gone before it could be read
	known   bool           // start was read
	tracked map[int]uint64 // descendants seen so far, by pid and start time
}

func newTracker(p *Process) *tracker {
	t := &tracker{pid: p.pid, tracked: map[int]uint64{}}
	if info, ok := readStat(p.pid); ok {
		t.start, t.known = info.start, true
	}
	return t
}

// childAlive reports whether the process holding the child's pid is the
// child and has not exited.
func (t *tracker) childAlive() bool {
	info, ok := readStat(t.childPid())
	return ok && t.known && info.start == t.start && !info.exited()
}

func (t *tracker) childPid() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pid
}

// scan builds the child's tree from one snapshot: the child while it is
// the same process, every tracked process still the same process, and on
// this platform every process reparented here from another session that
// no other live child owns (how an orphan looks to a subreaper), then
// everything below them by parent chain. True zombies this process
// parents and that are not live children are reaped. The walk is one pass
// over a parent index, so the cost is linear in the table size.
func (t *tracker) scan(p *Process) ([]liveProc, int, error) {
	procs, err := snapshot()
	if err != nil {
		return nil, 0, err
	}
	self, ok := procs[p.selfPid]
	if !ok {
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
	if child, ok := procs[p.pid]; ok && t.known && child.start == t.start {
		seed(p.pid)
	}
	for pid, start := range t.tracked {
		if pi, ok := procs[pid]; ok && pi.start == start {
			seed(pid)
		} else {
			delete(t.tracked, pid)
		}
	}
	live.Lock()
	for pid, pi := range procs {
		if pi.ppid != p.selfPid || pi.sid == self.sid || live.m[pid] {
			continue
		}
		if pi.sid != p.pid && live.m[pi.sid] {
			continue // another live child's session
		}
		seed(pid)
	}
	live.Unlock()
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range byParent[pid] {
			seed(c)
		}
	}

	var alive []liveProc
	reaped := 0
	for pid := range in {
		pi := procs[pid]
		if pid != p.pid {
			t.tracked[pid] = pi.start
		}
		if pi.exited() {
			if pi.ppid == p.selfPid && !liveHas(pid) {
				reap(pid)
				reaped++
			}
			continue
		}
		if pid == p.pid {
			continue
		}
		alive = append(alive, liveProc{pid: pid, pgid: pi.pgid})
	}
	return alive, reaped, nil
}

// kill sends sig to pid only if it is still the tracked process.
func (t *tracker) kill(pid int, sig syscall.Signal) error {
	t.mu.Lock()
	start, ok := t.tracked[pid]
	t.mu.Unlock()
	info, present := readStat(pid)
	if !ok || !present || info.start != start {
		return syscall.ESRCH
	}
	return syscall.Kill(pid, sig)
}

// killGroup signals the child's process group while the child is still
// the same process, so a reused pid is never a target.
func (t *tracker) killGroup(sig syscall.Signal) {
	if t.childAlive() {
		_ = syscall.Kill(-t.childPid(), sig)
	}
}

// reap collects the exit status of a zombie this process parents but did
// not start, so it leaves the process table. Live children are never
// passed here; their own Wait collects them.
func reap(pid int) {
	var ws syscall.WaitStatus
	_, _ = syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
}
