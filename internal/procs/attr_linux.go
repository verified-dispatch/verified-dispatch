//go:build linux

package procs

import (
	"bytes"
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
