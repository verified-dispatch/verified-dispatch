//go:build linux

package procs

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// sysProcAttr puts the child in a new session and process group and has
// the kernel send it SIGKILL when the thread that forked it dies, which
// Start keeps alive for the child's lifetime.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
}

// platformState holds nothing on Linux: the subreaper rule makes orphans
// visible without polling.
type platformState struct{}

func startPlatform(*Process) *platformState { return nil }

// Close releases platform resources. It is safe to call more than once.
func (p *Process) Close() {}

// procInfo is what /proc/<pid>/stat says about one process.
type procInfo struct {
	pid, ppid, pgid, sid int
	zombie               bool
}

// parseStat reads the fields after the command name. The name is in
// parentheses and may itself hold spaces or parentheses, so the split
// starts at the last closing parenthesis.
func parseStat(pid int, b []byte) (procInfo, bool) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return procInfo{}, false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 4 {
		return procInfo{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	pgid, err2 := strconv.Atoi(f[2])
	sid, err3 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return procInfo{}, false
	}
	return procInfo{pid: pid, ppid: ppid, pgid: pgid, sid: sid, zombie: f[0] == "Z"}, true
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
// a direct child here instead of vanishing under init.
func EnableSubreaper() error {
	_, err := prctl(prSetChildSubreaper, 1)
	return err
}

// DisableAttach stops a same-user debugger from attaching to this process
// and stops core dumps, so the per-run key and the resolved configuration
// held in memory cannot be read from outside. A host that lets a capable
// debugger attach regardless is outside this guard; the docs say so.
func DisableAttach() error {
	_, err := prctl(prSetDumpable, 0)
	return err
}

// dumpable reads the flag DisableAttach clears. Tests use it.
func dumpable() (int, error) {
	r, err := prctl(prGetDumpable, 0)
	return int(r), err
}

// Containment is the receipt line that says how children are contained on
// this platform.
func Containment() string {
	return "containment: session and process group, child subreaper, parent-death signal"
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
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if info, ok := parseStat(pid, b); ok {
			out[pid] = info
		}
	}
	return out, nil
}

// Descendants lists every live process that belongs to the child: those in
// its session or process group, those whose parent chain reaches it, and
// those reparented to this process from another session, which is how an
// orphan that called setsid appears to a subreaper. The child itself is
// not listed. A zombie in the set that this process now parents is reaped
// and skipped.
func (p *Process) Descendants() ([]int, error) {
	procs, err := snapshot()
	if err != nil {
		return nil, err
	}
	selfSid := -1
	if self, ok := procs[p.selfPid]; ok {
		selfSid = self.sid
	}
	in := map[int]bool{p.pid: true}
	for changed := true; changed; {
		changed = false
		for pid, pi := range procs {
			if in[pid] || pid == p.selfPid {
				continue
			}
			if pi.sid == p.pid || pi.pgid == p.pid || in[pi.ppid] || (pi.ppid == p.selfPid && pi.sid != selfSid) {
				in[pid] = true
				changed = true
			}
		}
	}
	var out []int
	for pid := range in {
		if pid == p.pid {
			continue
		}
		pi := procs[pid]
		if pi.zombie {
			if pi.ppid == p.selfPid {
				reap(pid)
			}
			continue
		}
		out = append(out, pid)
	}
	sort.Ints(out)
	return out, nil
}

// reap collects the exit status of a zombie this process parents but did
// not start, so it leaves the process table. Children started through
// exec.Cmd are never passed here; they are collected by their own Wait.
func reap(pid int) {
	var ws syscall.WaitStatus
	_, _ = syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
}
